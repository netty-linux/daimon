package agentloop

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// StopReason indica por que uma execução do agent loop terminou.
type StopReason string

const (
	StopReasonCompleted         StopReason = "completed"
	StopReasonInvalidConfig     StopReason = "invalid_config"
	StopReasonCanceled          StopReason = "canceled"
	StopReasonExternalDeadline  StopReason = "external_deadline"
	StopReasonRunTimeout        StopReason = "run_timeout"
	StopReasonModelTimeout      StopReason = "model_timeout"
	StopReasonToolTimeout       StopReason = "tool_timeout"
	StopReasonModelError        StopReason = "model_error"
	StopReasonInvalidResponse   StopReason = "invalid_response"
	StopReasonMaxSteps          StopReason = "max_steps"
	StopReasonMaxToolCalls      StopReason = "max_tool_calls"
	StopReasonArgumentLimit     StopReason = "argument_limit"
	StopReasonFinalAnswerLimit  StopReason = "final_answer_limit"
	StopReasonHistoryLimit      StopReason = "history_limit"
)

// LimitKind identifica qual limite foi violado.
type LimitKind string

const (
	LimitMaxSteps                 LimitKind = "max_steps"
	LimitMaxToolCallsPerStep      LimitKind = "max_tool_calls_per_step"
	LimitMaxTotalToolCalls        LimitKind = "max_total_tool_calls"
	LimitMaxUserMessageBytes      LimitKind = "max_user_message_bytes"
	LimitMaxFinalAnswerBytes      LimitKind = "max_final_answer_bytes"
	LimitMaxToolArgumentBytes     LimitKind = "max_tool_argument_bytes"
	LimitMaxToolResultBytes       LimitKind = "max_tool_result_bytes"
	LimitMaxHistoryMessages       LimitKind = "max_history_messages"
	LimitMaxHistoryBytes          LimitKind = "max_history_bytes"
	LimitMaxRunDuration           LimitKind = "max_run_duration"
	LimitMaxModelCallDuration     LimitKind = "max_model_call_duration"
	LimitMaxToolCallDuration      LimitKind = "max_tool_call_duration"
)

// LimitError é retornado quando um limite do Budget é violado.
type LimitError struct {
	Kind   LimitKind
	Limit  int64
	Actual int64
}

func (e LimitError) Error() string {
	return fmt.Sprintf("budget limit exceeded: %s (limit=%d, actual=%d)", e.Kind, e.Limit, e.Actual)
}

// Budget define limites explícitos para uma execução do agent loop.
//
// Unidades:
//   - MaxSteps, MaxToolCallsPerStep, MaxTotalToolCalls, MaxHistoryMessages: quantidade
//   - MaxUserMessageBytes, MaxFinalAnswerBytes, MaxToolArgumentBytes, MaxToolResultBytes, MaxHistoryBytes: bytes (len UTF-8)
//   - MaxRunDuration, MaxModelCallDuration, MaxToolCallDuration: time.Duration
//
// Regras:
//   - Todos os limites devem ser positivos (> 0).
//   - Zero não significa ilimitado; é inválido.
//   - Budget inválido deve falhar antes da primeira chamada ao modelo.
type Budget struct {
	MaxSteps             int
	MaxToolCallsPerStep  int
	MaxTotalToolCalls    int
	MaxUserMessageBytes  int
	MaxFinalAnswerBytes  int
	MaxToolArgumentBytes int
	MaxToolResultBytes   int
	MaxHistoryMessages   int
	MaxHistoryBytes      int
	MaxRunDuration       time.Duration
	MaxModelCallDuration time.Duration
	MaxToolCallDuration  time.Duration
}

// DefaultBudget retorna valores conservadores para produção inicial.
// Estes são pontos de partida, não garantias de segurança.
func DefaultBudget() Budget {
	return Budget{
		MaxSteps:             8,
		MaxToolCallsPerStep:  4,
		MaxTotalToolCalls:    16,
		MaxUserMessageBytes:  32 * 1024,       // 32 KiB
		MaxFinalAnswerBytes:  256 * 1024,      // 256 KiB
		MaxToolArgumentBytes: 64 * 1024,       // 64 KiB
		MaxToolResultBytes:   64 * 1024,       // 64 KiB
		MaxHistoryMessages:   128,
		MaxHistoryBytes:      2 * 1024 * 1024, // 2 MiB
		MaxRunDuration:       5 * time.Minute,
		MaxModelCallDuration: 2 * time.Minute,
		MaxToolCallDuration:  30 * time.Second,
	}
}

// Validate valida o Budget. Retorna LimitError ou InvalidConfigError se inválido.
func (b Budget) Validate() error {
	if b.MaxSteps <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxSteps, Limit: 1, Actual: int64(b.MaxSteps)}}
	}
	if b.MaxToolCallsPerStep <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxToolCallsPerStep, Limit: 1, Actual: int64(b.MaxToolCallsPerStep)}}
	}
	if b.MaxTotalToolCalls <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxTotalToolCalls, Limit: 1, Actual: int64(b.MaxTotalToolCalls)}}
	}
	if b.MaxUserMessageBytes <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxUserMessageBytes, Limit: 1, Actual: int64(b.MaxUserMessageBytes)}}
	}
	if b.MaxFinalAnswerBytes <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxFinalAnswerBytes, Limit: 1, Actual: int64(b.MaxFinalAnswerBytes)}}
	}
	if b.MaxToolArgumentBytes <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxToolArgumentBytes, Limit: 1, Actual: int64(b.MaxToolArgumentBytes)}}
	}
	if b.MaxToolResultBytes <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxToolResultBytes, Limit: 1, Actual: int64(b.MaxToolResultBytes)}}
	}
	if b.MaxHistoryMessages <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxHistoryMessages, Limit: 1, Actual: int64(b.MaxHistoryMessages)}}
	}
	if b.MaxHistoryBytes <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxHistoryBytes, Limit: 1, Actual: int64(b.MaxHistoryBytes)}}
	}
	if b.MaxRunDuration <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxRunDuration, Limit: 1, Actual: int64(b.MaxRunDuration)}}
	}
	if b.MaxModelCallDuration <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxModelCallDuration, Limit: 1, Actual: int64(b.MaxModelCallDuration)}}
	}
	if b.MaxToolCallDuration <= 0 {
		return InvalidConfigError{Err: LimitError{Kind: LimitMaxToolCallDuration, Limit: 1, Actual: int64(b.MaxToolCallDuration)}}
	}
	return nil
}

// ApplyRunTimeout deriva um contexto com timeout de execução total.
// Preserve deadlines externos menores e não amplia deadlines recebidos.
func (b Budget) ApplyRunTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return parent, func() {}
		}
		if remaining < b.MaxRunDuration {
			return context.WithTimeout(parent, remaining)
		}
	}
	return context.WithTimeout(parent, b.MaxRunDuration)
}

// ApplyModelTimeout deriva um contexto para uma chamada ao modelo.
// Preserve deadlines externos menores.
func (b Budget) ApplyModelTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return parent, func() {}
		}
		if remaining < b.MaxModelCallDuration {
			return context.WithTimeout(parent, remaining)
		}
	}
	return context.WithTimeout(parent, b.MaxModelCallDuration)
}

// ApplyToolTimeout deriva um contexto para uma chamada de ferramenta.
// Preserve deadlines externos menores.
func (b Budget) ApplyToolTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return parent, func() {}
		}
		if remaining < b.MaxToolCallDuration {
			return context.WithTimeout(parent, remaining)
		}
	}
	return context.WithTimeout(parent, b.MaxToolCallDuration)
}

// truncateResult trunca um resultado de ferramenta para respeitar MaxToolResultBytes.
// Garante UTF-8 válido, inclui indicador de truncamento se necessário.
// Retorna o resultado truncado e true se houve truncamento.
func (b Budget) truncateResult(result string) (string, bool) {
	bytes := []byte(result)
	if len(bytes) <= b.MaxToolResultBytes {
		return result, false
	}

	// Indicador de truncamento
	indicator := fmt.Sprintf("\n[truncated: original_bytes=%d]", len(bytes))
	indicatorBytes := []byte(indicator)

	// Se o indicador não couber, usa forma mínima
	if len(indicatorBytes) >= b.MaxToolResultBytes {
		indicator = "\n[truncated]"
		indicatorBytes = []byte(indicator)
	}

	// Espaço disponível para conteúdo
	available := b.MaxToolResultBytes - len(indicatorBytes)
	if available <= 0 {
		// Caso extremo: só o indicador cabe
		return string(indicatorBytes[:b.MaxToolResultBytes]), true
	}

	// Trunca em fronteira UTF-8 válida
	truncated := bytes[:available]
	for !utf8.Valid(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}

	return string(truncated) + indicator, true
}

// errors.Is e errors.As suporte para LimitError
var _ error = LimitError{}

// Is permite errors.Is(err, LimitError{})
func (e LimitError) Is(target error) bool {
	_, ok := target.(LimitError)
	return ok
}

// As permite errors.As(err, &LimitError{})
func (e LimitError) As(target interface{}) bool {
	if t, ok := target.(*LimitError); ok {
		*t = e
		return true
	}
	return false
}

// wrapLimitError envolve um LimitError em InvalidConfigError quando apropriado.
func wrapLimitError(err error) error {
	var le LimitError
	if errors.As(err, &le) {
		return InvalidConfigError{Err: le}
	}
	return err
}
