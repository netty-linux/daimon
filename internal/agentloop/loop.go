package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/netty-linux/daimon/internal/model"
)

// Tool representa uma ferramenta executável pelo agent.
type Tool interface {
	Name() string
	Call(ctx context.Context, args json.RawMessage) (result string, err error)
}

// Loop é o agent loop com execution budget.
type Loop struct {
	opts Options
}

// NewLoop cria um novo loop com as opções fornecidas.
func NewLoop(opts Options) (*Loop, error) {
	// Se Budget não for fornecido, usa DefaultBudget
	if opts.Budget == (Budget{}) {
		opts.Budget = DefaultBudget()
	}

	// Valida budget
	if err := opts.Budget.Validate(); err != nil {
		return nil, err
	}

	// Valida modelo
	if opts.Model == nil {
		return nil, InvalidConfigError{Err: errors.New("model is required")}
	}

	// Valida ferramentas
	toolMap := make(map[string]Tool)
	for _, tool := range opts.Tools {
		if tool == nil {
			return nil, InvalidConfigError{Err: errors.New("tool cannot be nil")}
		}
		name := tool.Name()
		if name == "" {
			return nil, InvalidConfigError{Err: errors.New("tool name cannot be empty")}
		}
		if _, exists := toolMap[name]; exists {
			return nil, InvalidConfigError{Err: errors.New("duplicate tool name: " + name)}
		}
		toolMap[name] = tool
	}

	return &Loop{opts: opts}, nil
}

// Run executa o agent loop com budget.
func (l *Loop) Run(ctx context.Context, userMessage string) (Result, error) {
	b := l.opts.Budget

	// Valida mensagem do usuário
	if len(userMessage) > b.MaxUserMessageBytes {
		return Result{StopReason: StopReasonInvalidConfig}, wrapLimitError(LimitError{
			Kind:   LimitMaxUserMessageBytes,
			Limit:  int64(b.MaxUserMessageBytes),
			Actual: int64(len(userMessage)),
		})
	}

	// Aplica timeout de execução total
	runCtx, cancelRun := b.ApplyRunTimeout(ctx)
	defer cancelRun()

	// Inicializa histórico
	history := []model.Message{
		{Role: "user", Content: userMessage},
	}

	var (
		steps                = 0
		totalToolCalls       = 0
		truncatedToolResults = 0
		stopReason           StopReason
	)

	// Emite evento de início
	emitEvent(Event{
		Type: "loop_started",
		Data: map[string]interface{}{
			"user_message_bytes": len(userMessage),
		},
	})

	defer func() {
		// Emite evento de parada exatamente uma vez
		emitEvent(Event{
			Type: "loop_stopped",
			Data: map[string]interface{}{
				"steps":                 steps,
				"tool_calls":            totalToolCalls,
				"truncated_tool_results": truncatedToolResults,
				"stop_reason":           string(stopReason),
			},
		})
	}()

	for steps < b.MaxSteps {
		// Verifica cancelamento externo
		select {
		case <-runCtx.Done():
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				// Verifica se é deadline externo ou timeout do budget
				deadline, hasDeadline := ctx.Deadline()
				if hasDeadline {
					if time.Now().After(deadline) {
						stopReason = StopReasonExternalDeadline
					} else {
						stopReason = StopReasonRunTimeout
					}
				} else {
					stopReason = StopReasonRunTimeout
				}
			} else {
				stopReason = StopReasonCanceled
			}
			return Result{
				FinalAnswer:         "",
				History:             history,
				Steps:               steps,
				ToolCalls:           totalToolCalls,
				TruncatedToolResults: truncatedToolResults,
				StopReason:          stopReason,
			}, runCtx.Err()
		default:
		}

		steps++

		// Aplica timeout para chamada ao modelo
		modelCtx, cancelModel := b.ApplyModelTimeout(runCtx)

		response, err := l.opts.Model.Call(modelCtx, history)
		cancelModel()

		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				stopReason = StopReasonModelTimeout
				return Result{
					FinalAnswer:         "",
					History:             history,
					Steps:               steps,
					ToolCalls:           totalToolCalls,
					TruncatedToolResults: truncatedToolResults,
					StopReason:          stopReason,
				}, err
			}
			stopReason = StopReasonModelError
			return Result{
				FinalAnswer:         "",
				History:             history,
				Steps:               steps,
				ToolCalls:           totalToolCalls,
				TruncatedToolResults: truncatedToolResults,
				StopReason:          stopReason,
			}, ModelError{Err: err}
		}

		// Valida resposta básica
		if response.Content == "" && len(response.ToolCalls) == 0 {
			stopReason = StopReasonInvalidResponse
			return Result{
				FinalAnswer:         "",
				History:             history,
				Steps:               steps,
				ToolCalls:           totalToolCalls,
				TruncatedToolResults: truncatedToolResults,
				StopReason:          stopReason,
			}, InvalidResponseError{Err: errors.New("empty response")}
		}

		// Valida quantidade de tool calls no step
		if len(response.ToolCalls) > b.MaxToolCallsPerStep {
			stopReason = StopReasonMaxToolCalls
			return Result{
				FinalAnswer:         "",
				History:             history,
				Steps:               steps,
				ToolCalls:           totalToolCalls,
				TruncatedToolResults: truncatedToolResults,
				StopReason:          stopReason,
			}, LimitError{
				Kind:   LimitMaxToolCallsPerStep,
				Limit:  int64(b.MaxToolCallsPerStep),
				Actual: int64(len(response.ToolCalls)),
			}
		}

		// Valida total de tool calls
		if totalToolCalls+len(response.ToolCalls) > b.MaxTotalToolCalls {
			stopReason = StopReasonMaxToolCalls
			return Result{
				FinalAnswer:         "",
				History:             history,
				Steps:               steps,
				ToolCalls:           totalToolCalls,
				TruncatedToolResults: truncatedToolResults,
				StopReason:          stopReason,
			}, LimitError{
				Kind:   LimitMaxTotalToolCalls,
				Limit:  int64(b.MaxTotalToolCalls),
				Actual: int64(totalToolCalls + len(response.ToolCalls)),
			}
		}

		// Valida tool calls: IDs, nomes, duplicatas, argumentos
		if err := l.validateToolCalls(response.ToolCalls, b); err != nil {
			if le, ok := err.(LimitError); ok {
				stopReason = StopReasonArgumentLimit
				return Result{
					FinalAnswer:         "",
					History:             history,
					Steps:               steps,
					ToolCalls:           totalToolCalls,
					TruncatedToolResults: truncatedToolResults,
					StopReason:          stopReason,
				}, le
			}
			stopReason = StopReasonInvalidResponse
			return Result{
				FinalAnswer:         "",
				History:             history,
				Steps:               steps,
				ToolCalls:           totalToolCalls,
				TruncatedToolResults: truncatedToolResults,
				StopReason:          stopReason,
			}, InvalidResponseError{Err: err}
		}

		// Constrói mensagem assistant
		assistantMsg := model.Message{
			Role:      "assistant",
			Content:   response.Content,
			ToolCalls: response.ToolCalls,
		}

		// Reserva espaço para resultados antes de executar
		if !reserveHistoryForResults(history, assistantMsg, len(response.ToolCalls), b.MaxHistoryMessages, b.MaxHistoryBytes, b.MaxToolResultBytes) {
			stopReason = StopReasonHistoryLimit
			return Result{
				FinalAnswer:         "",
				History:             history,
				Steps:               steps,
				ToolCalls:           totalToolCalls,
				TruncatedToolResults: truncatedToolResults,
				StopReason:          stopReason,
			}, LimitError{
				Kind:   LimitMaxHistoryBytes,
				Limit:  int64(b.MaxHistoryBytes),
				Actual: int64(historyBytes(history) + messageBytes(assistantMsg) + len(response.ToolCalls)*b.MaxToolResultBytes),
			}
		}

		// Adiciona mensagem assistant ao histórico
		history = append(history, assistantMsg)

		// Executa tool calls
		for _, tc := range response.ToolCalls {
			tool, exists := l.getTool(tc.Name)
			if !exists {
				stopReason = StopReasonInvalidResponse
				return Result{
					FinalAnswer:         "",
					History:             history,
					Steps:               steps,
					ToolCalls:           totalToolCalls,
					TruncatedToolResults: truncatedToolResults,
					StopReason:          stopReason,
				}, InvalidResponseError{Err: errors.New("unknown tool: " + tc.Name)}
			}

			// Prepara argumentos
			var argsJSON json.RawMessage
			if tc.Arguments != nil {
				if argStr, ok := tc.Arguments.(string); ok {
					argsJSON = json.RawMessage(argStr)
				} else if argBytes, ok := tc.Arguments.([]byte); ok {
					argsJSON = argBytes
				} else {
					var err error
					argsJSON, err = json.Marshal(tc.Arguments)
					if err != nil {
						stopReason = StopReasonInvalidResponse
						return Result{
							FinalAnswer:         "",
							History:             history,
							Steps:               steps,
							ToolCalls:           totalToolCalls,
							TruncatedToolResults: truncatedToolResults,
							StopReason:          stopReason,
						}, InvalidResponseError{Err: err}
					}
				}
			}

			// Aplica timeout para ferramenta
			toolCtx, cancelTool := b.ApplyToolTimeout(runCtx)
			result, err := tool.Call(toolCtx, argsJSON)
			cancelTool()

			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					stopReason = StopReasonToolTimeout
					return Result{
						FinalAnswer:         "",
						History:             history,
						Steps:               steps,
						ToolCalls:           totalToolCalls,
						TruncatedToolResults: truncatedToolResults,
						StopReason:          stopReason,
					}, err
				}
				// Converte erro em resultado
				result = "error: " + err.Error()
			}

			// Trunca resultado se necessário
			result, wasTruncated := b.truncateResult(result)
			if wasTruncated {
				truncatedToolResults++
			}

			// Adiciona resultado ao histórico
			resultMsg := model.Message{
				Role:        "tool",
				Content:     result,
				ToolCallID:  tc.ID,
			}

			// Verifica se ainda cabe no histórico
			if !canFitInHistory(history, resultMsg, b.MaxHistoryMessages, b.MaxHistoryBytes) {
				stopReason = StopReasonHistoryLimit
				return Result{
					FinalAnswer:         "",
					History:             history,
					Steps:               steps,
					ToolCalls:           totalToolCalls,
					TruncatedToolResults: truncatedToolResults,
					StopReason:          stopReason,
				}, LimitError{
					Kind:   LimitMaxHistoryBytes,
					Limit:  int64(b.MaxHistoryBytes),
					Actual: int64(historyBytes(append(history, resultMsg))),
				}
			}

			history = append(history, resultMsg)
			totalToolCalls++
		}

		// Se não há tool calls e há conteúdo, é resposta final
		if len(response.ToolCalls) == 0 && response.Content != "" {
			// Valida tamanho da resposta final
			if len(response.Content) > b.MaxFinalAnswerBytes {
				stopReason = StopReasonFinalAnswerLimit
				return Result{
					FinalAnswer:         "",
					History:             history,
					Steps:               steps,
					ToolCalls:           totalToolCalls,
					TruncatedToolResults: truncatedToolResults,
					StopReason:          stopReason,
				}, LimitError{
					Kind:   LimitMaxFinalAnswerBytes,
					Limit:  int64(b.MaxFinalAnswerBytes),
					Actual: int64(len(response.Content)),
				}
			}

			stopReason = StopReasonCompleted
			return Result{
				FinalAnswer:         response.Content,
				History:             history,
				Steps:               steps,
				ToolCalls:           totalToolCalls,
				TruncatedToolResults: truncatedToolResults,
				StopReason:          stopReason,
			}, nil
		}
	}

	// MaxSteps atingido
	stopReason = StopReasonMaxSteps
	return Result{
		FinalAnswer:         "",
		History:             history,
		Steps:               steps,
		ToolCalls:           totalToolCalls,
		TruncatedToolResults: truncatedToolResults,
		StopReason:          stopReason,
	}, MaxStepsError{Max: b.MaxSteps}
}

// validateToolCalls valida um lote de tool calls antes da execução.
func (l *Loop) validateToolCalls(calls []model.ToolCall, b Budget) error {
	seenIDs := make(map[string]bool)

	for i, tc := range calls {
		// ID não vazio
		if tc.ID == "" {
			return InvalidResponseError{Err: errors.New("tool call ID cannot be empty")}
		}

		// Nome não vazio
		if tc.Name == "" {
			return InvalidResponseError{Err: errors.New("tool call name cannot be empty")}
		}

		// ID duplicado
		if seenIDs[tc.ID] {
			return InvalidResponseError{Err: errors.New("duplicate tool call ID: " + tc.ID)}
		}
		seenIDs[tc.ID] = true

		// Valida tamanho dos argumentos
		var argBytes int
		if tc.Arguments != nil {
			if argStr, ok := tc.Arguments.(string); ok {
				argBytes = len(argStr)
			} else if argB, ok := tc.Arguments.([]byte); ok {
				argBytes = len(argB)
			} else {
				if b, err := json.Marshal(tc.Arguments); err == nil {
					argBytes = len(b)
				}
			}
		}

		if argBytes > b.MaxToolArgumentBytes {
			return LimitError{
				Kind:   LimitMaxToolArgumentBytes,
				Limit:  int64(b.MaxToolArgumentBytes),
				Actual: int64(argBytes),
			}
		}

		_ = i // suppress unused
	}

	return nil
}

// getTool retorna a ferramenta pelo nome.
func (l *Loop) getTool(name string) (Tool, bool) {
	for _, tool := range l.opts.Tools {
		if tool.Name() == name {
			return tool, true
		}
	}
	return nil, false
}

// isWhitespace verifica se uma string é apenas whitespace.
func isWhitespace(s string) bool {
	return strings.TrimSpace(s) == ""
}
