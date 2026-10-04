package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
)

// callOutcome is the pass-1 result for one call: the resolved tool is
// carried into pass 2 so the registry is consulted exactly once.
type callOutcome struct {
	class callClass
	tool  tools.Tool
}

type callClass uint8

const (
	classUnknownTool callClass = iota
	classInvalidArguments
	classAllowed
	classDenied
)

func (l Loop) Run(ctx context.Context, userMessage string) (result Result, err error) {
	sink := l.Sink
	if sink == nil {
		sink = NoopEventSink{}
	}
	sink.Record(ctx, Event{Kind: LoopStarted})
	defer func() {
		result.StopReason = reasonFor(err)
		sink.Record(ctx, Event{Kind: LoopStopped, Step: result.Steps, StopReason: result.StopReason})
	}()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	b := l.Budget
	if err := b.Validate(); err != nil {
		return result, err
	}
	if l.Model == nil || l.Registry == nil || l.Authorizer == nil {
		return result, ErrInvalidConfig
	}
	runCtx, cancelRun := b.ApplyRunTimeout(ctx)
	defer cancelRun()

	if len(userMessage) > b.MaxUserMessageBytes {
		return result, LimitError{Kind: LimitMaxUserMessageBytes, Limit: int64(b.MaxUserMessageBytes), Actual: int64(len(userMessage))}
	}
	user := model.Message{Role: model.RoleUser, Content: userMessage}
	if err := historyLimit(nil, user, nil, b); err != nil {
		return result, err
	}
	result.History = []model.Message{user}
	ids := make(map[string]bool)
	recovering := false
	for result.Steps < b.MaxSteps {
		if err := interruption(ctx, runCtx); err != nil {
			return result, err
		}
		sink.Record(ctx, Event{Kind: ModelRequested, Step: result.Steps + 1})
		if err := interruption(ctx, runCtx); err != nil {
			return result, err
		}
		request := model.CloneRequest(model.ModelRequest{Messages: result.History, Tools: l.Registry.Descriptions()})
		if recovering {
			request.Tools = nil
		}
		modelCtx, cancelModel := b.ApplyModelTimeout(runCtx)
		if err := interruption(ctx, modelCtx); err != nil {
			cancelModel()
			return result, err
		}
		result.Steps++
		if recovering {
			sink.Record(ctx, Event{Kind: RecoveryModelRequested, Step: result.Steps})
		}
		response, modelErr := l.Model.Generate(modelCtx, request)
		// Inspect before cancel: cleanup cancellation must not look like a timeout.
		interrupted := interruption(ctx, modelCtx)
		cancelModel()
		if interrupted != nil {
			return result, interrupted
		}
		if modelErr != nil {
			return result, &ModelError{Step: result.Steps, Cause: modelErr}
		}
		sink.Record(ctx, Event{Kind: ModelResponded, Step: result.Steps})
		if err := interruption(ctx, runCtx); err != nil {
			return result, err
		}
		if recovering && len(response.ToolCalls) != 0 {
			return result, ErrInvalidResponse
		}
		if len(response.ToolCalls) == 0 {
			if err := validate(response, ids); err != nil {
				return result, err
			}
			if len(response.FinalText) > b.MaxFinalAnswerBytes {
				return result, LimitError{Kind: LimitMaxFinalAnswerBytes, Limit: int64(b.MaxFinalAnswerBytes), Actual: int64(len(response.FinalText))}
			}
			message := model.Message{Role: model.RoleAssistant, Content: response.FinalText}
			if err := historyLimit(result.History, message, nil, b); err != nil {
				return result, err
			}
			if err := interruption(ctx, runCtx); err != nil {
				return result, err
			}
			if l.ValidateFinal != nil {
				sink.Record(ctx, Event{Kind: FinalValidationRequested, Step: result.Steps})
				recovery, validationErr := l.ValidateFinal(response.FinalText)
				if err := interruption(ctx, runCtx); err != nil {
					return result, err
				}
				if validationErr != nil {
					sink.Record(ctx, Event{Kind: FinalValidationRejected, Step: result.Steps})
					return result, validationErr
				}
				if recovery != "" {
					sink.Record(ctx, Event{Kind: FinalValidationRejected, Step: result.Steps})
					if recovering || strings.TrimSpace(recovery) == "" {
						return result, ErrInvalidResponse
					}
					if len(recovery) > b.MaxUserMessageBytes {
						return result, LimitError{Kind: LimitMaxUserMessageBytes, Limit: int64(b.MaxUserMessageBytes), Actual: int64(len(recovery))}
					}
					pending := append(append([]model.Message(nil), result.History...), message)
					correction := model.Message{Role: model.RoleUser, Content: recovery}
					if err := historyLimit(pending, correction, nil, b); err != nil {
						return result, err
					}
					result.History = append(pending, correction)
					recovering = true
					sink.Record(ctx, Event{Kind: RecoveryRequested, Step: result.Steps})
					continue
				}
				sink.Record(ctx, Event{Kind: FinalValidationAccepted, Step: result.Steps})
			}
			result.History = append(result.History, message)
			result.FinalAnswer = response.FinalText
			sink.Record(ctx, Event{Kind: FinalAnswer, Step: result.Steps})
			return result, nil
		}

		// Validate all limits and reserve all receipts before the first effect.
		if len(response.ToolCalls) > b.MaxToolCallsPerStep {
			return result, LimitError{Kind: LimitMaxToolCallsPerStep, Limit: int64(b.MaxToolCallsPerStep), Actual: int64(len(response.ToolCalls))}
		}
		if len(response.ToolCalls) > b.MaxTotalToolCalls-result.ToolCalls {
			return result, LimitError{Kind: LimitMaxTotalToolCalls, Limit: int64(b.MaxTotalToolCalls), Actual: int64(sizeSum(result.ToolCalls, len(response.ToolCalls)))}
		}
		if err := validate(response, ids); err != nil {
			return result, err
		}
		for _, call := range response.ToolCalls {
			if len(call.Arguments) > b.MaxToolArgumentBytes {
				return result, LimitError{Kind: LimitMaxToolArgumentBytes, Limit: int64(b.MaxToolArgumentBytes), Actual: int64(len(call.Arguments))}
			}
		}
		assistant := model.Message{Role: model.RoleAssistant, ToolCalls: response.ToolCalls}
		if err := historyLimit(result.History, assistant, response.ToolCalls, b); err != nil {
			return result, err
		}
		calls := model.CloneCalls(response.ToolCalls)
		assistant.ToolCalls = model.CloneCalls(calls)
		if err := interruption(ctx, runCtx); err != nil {
			return result, err
		}
		// Pass 1: classify and authorize the whole batch before any event or
		// effect, so no tool executes before every decision is collected.
		outcomes := make([]callOutcome, len(calls))
		for i, call := range calls {
			tool, exists := l.Registry.Find(call.Name)
			switch {
			case !exists:
				outcomes[i] = callOutcome{class: classUnknownTool}
				continue
			case !json.Valid(call.Arguments):
				outcomes[i] = callOutcome{class: classInvalidArguments, tool: tool}
				continue
			}
			if err := interruption(ctx, runCtx); err != nil {
				return result, err
			}
			request := ToolAuthorizationRequest{
				Call: model.ToolCall{
					ID: call.ID, Name: call.Name,
					Arguments: append(json.RawMessage(nil), call.Arguments...),
				},
				Step: result.Steps, ToolIndex: i + 1,
			}
			decision, authErr := l.Authorizer.Authorize(runCtx, request)
			// An interruption ends the batch before the result is inspected:
			// the deadline that stopped the call is the reason, not its error.
			if err := interruption(ctx, runCtx); err != nil {
				return result, err
			}
			if authErr != nil {
				if errors.Is(authErr, context.Canceled) || errors.Is(authErr, context.DeadlineExceeded) {
					return result, authErr
				}
				return result, &AuthorizationError{Step: result.Steps, ToolIndex: i + 1, Cause: authErr}
			}
			if decision != ToolDecisionAllow && decision != ToolDecisionDeny {
				return result, &AuthorizationError{Step: result.Steps, ToolIndex: i + 1, Cause: errInvalidDecision}
			}
			outcomes[i] = callOutcome{class: classAllowed, tool: tool}
			if decision == ToolDecisionDeny {
				outcomes[i].class = classDenied
			}
		}
		if err := interruption(ctx, runCtx); err != nil {
			return result, err
		}
		result.History = append(result.History, assistant)
		// Pass 2: emit events and effects in call order.
		for i, call := range calls {
			if err := interruption(ctx, runCtx); err != nil {
				return result, err
			}
			outcome := outcomes[i]
			toolResult := tools.ToolResult{}
			switch outcome.class {
			case classUnknownTool:
				sink.Record(ctx, Event{Kind: ToolRequested, Step: result.Steps, ToolIndex: i + 1})
				if err := interruption(ctx, runCtx); err != nil {
					return result, err
				}
				result.ToolCalls++
				toolResult = tools.ToolResult{Content: "unknown tool", IsError: true}
			case classInvalidArguments:
				sink.Record(ctx, Event{Kind: ToolRequested, Step: result.Steps, ToolIndex: i + 1})
				if err := interruption(ctx, runCtx); err != nil {
					return result, err
				}
				result.ToolCalls++
				toolResult = tools.ToolResult{Content: "invalid JSON arguments", IsError: true}
			case classDenied:
				sink.Record(ctx, Event{Kind: ToolDenied, Step: result.Steps, ToolIndex: i + 1})
				if err := interruption(ctx, runCtx); err != nil {
					return result, err
				}
				result.ToolCalls++
				toolResult = tools.ToolResult{Content: "tool denied", IsError: true}
			default:
				sink.Record(ctx, Event{Kind: ToolAllowed, Step: result.Steps, ToolIndex: i + 1})
				if err := interruption(ctx, runCtx); err != nil {
					return result, err
				}
				sink.Record(ctx, Event{Kind: ToolRequested, Step: result.Steps, ToolIndex: i + 1})
				if err := interruption(ctx, runCtx); err != nil {
					return result, err
				}
				toolCtx, cancelTool := b.ApplyToolTimeout(runCtx)
				if err := interruption(ctx, toolCtx); err != nil {
					cancelTool()
					return result, err
				}
				result.ToolCalls++
				var toolErr error
				toolResult, toolErr = outcome.tool.Execute(toolCtx, call.Arguments)
				interrupted := interruption(ctx, toolCtx)
				cancelTool()
				if interrupted != nil {
					return result, interrupted
				}
				if errors.Is(toolErr, context.Canceled) {
					return result, toolErr
				}
				if errors.Is(toolErr, context.DeadlineExceeded) {
					return result, &toolDeadlineError{cause: toolErr}
				}
				if toolErr != nil {
					toolResult = tools.ToolResult{Content: toolErr.Error(), IsError: true}
				}
			}
			if err := interruption(ctx, runCtx); err != nil {
				return result, err
			}
			var changed bool
			toolResult.Content, changed = b.truncateResult(toolResult.Content)
			if err := interruption(ctx, runCtx); err != nil {
				return result, err
			}
			if changed {
				result.TruncatedToolResults++
			}
			receipt := model.Message{Role: model.RoleTool, Content: toolResult.Content, ToolCallID: call.ID, IsError: toolResult.IsError}
			// Capacity was reserved with this ID and the maximum normalized content.
			result.History = append(result.History, receipt)
			// A denial already reported ToolDenied: it never executed, so no
			// completion or failure event follows its receipt.
			if outcome.class != classDenied {
				kind := ToolCompleted
				if toolResult.IsError {
					kind = ToolFailed
				}
				sink.Record(ctx, Event{Kind: kind, Step: result.Steps, ToolIndex: i + 1})
			}
		}
	}
	if err := interruption(ctx, runCtx); err != nil {
		return result, err
	}
	return result, LimitError{Kind: LimitMaxSteps, Limit: int64(b.MaxSteps), Actual: int64(result.Steps)}
}

// Parent cancellation takes precedence when already observable; otherwise the
// inherited cause identifies which budget deadline expired, without wall clocks.
func interruption(parent, current context.Context) error {
	if err := parent.Err(); err != nil {
		return err
	}
	if current.Err() != nil {
		return context.Cause(current)
	}
	return nil
}

func reasonFor(err error) StopReason {
	if err == nil {
		return StopReasonCompleted
	}
	if errors.Is(err, ErrInvalidConfig) {
		return StopReasonInvalidConfig
	}
	var modelErr *ModelError
	if errors.As(err, &modelErr) {
		return StopReasonModelError
	}
	var authErr *AuthorizationError
	if errors.As(err, &authErr) {
		return StopReasonAuthorizationError
	}
	var toolDeadline *toolDeadlineError
	if errors.As(err, &toolDeadline) {
		return StopReasonToolTimeout
	}
	var limit LimitError
	if errors.As(err, &limit) {
		switch limit.Kind {
		case LimitMaxSteps:
			return StopReasonMaxSteps
		case LimitMaxToolCallsPerStep, LimitMaxTotalToolCalls:
			return StopReasonMaxToolCalls
		case LimitMaxUserMessageBytes:
			return StopReasonUserMessageLimit
		case LimitMaxFinalAnswerBytes:
			return StopReasonFinalAnswerLimit
		case LimitMaxToolArgumentBytes:
			return StopReasonArgumentLimit
		case LimitMaxHistoryMessages, LimitMaxHistoryBytes:
			return StopReasonHistoryLimit
		case LimitMaxRunDuration:
			return StopReasonRunTimeout
		case LimitMaxModelCallDuration:
			return StopReasonModelTimeout
		case LimitMaxToolCallDuration:
			return StopReasonToolTimeout
		}
	}
	if errors.Is(err, context.Canceled) {
		return StopReasonCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return StopReasonExternalDeadline
	}
	return StopReasonInvalidResponse
}

func validate(response model.ModelResponse, ids map[string]bool) error {
	if len(response.ToolCalls) == 0 {
		if strings.TrimSpace(response.FinalText) == "" {
			return ErrInvalidResponse
		}
		return nil
	}
	if response.FinalText != "" {
		return ErrInvalidResponse
	}
	for _, call := range response.ToolCalls {
		if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || ids[call.ID] {
			return ErrInvalidResponse
		}
		ids[call.ID] = true
	}
	return nil
}
