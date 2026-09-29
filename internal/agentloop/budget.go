package agentloop

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type StopReason string

const (
	StopReasonCompleted          StopReason = "completed"
	StopReasonInvalidConfig      StopReason = "invalid_config"
	StopReasonCanceled           StopReason = "canceled"
	StopReasonExternalDeadline   StopReason = "external_deadline"
	StopReasonRunTimeout         StopReason = "run_timeout"
	StopReasonModelTimeout       StopReason = "model_timeout"
	StopReasonToolTimeout        StopReason = "tool_timeout"
	StopReasonModelError         StopReason = "model_error"
	StopReasonInvalidResponse    StopReason = "invalid_response"
	StopReasonMaxSteps           StopReason = "max_steps"
	StopReasonMaxToolCalls       StopReason = "max_tool_calls"
	StopReasonUserMessageLimit   StopReason = "user_message_limit"
	StopReasonArgumentLimit      StopReason = "argument_limit"
	StopReasonFinalAnswerLimit   StopReason = "final_answer_limit"
	StopReasonHistoryLimit       StopReason = "history_limit"
	StopReasonAuthorizationError StopReason = "authorization_error"
)

type LimitKind string

const (
	LimitMaxSteps             LimitKind = "max_steps"
	LimitMaxToolCallsPerStep  LimitKind = "max_tool_calls_per_step"
	LimitMaxTotalToolCalls    LimitKind = "max_total_tool_calls"
	LimitMaxUserMessageBytes  LimitKind = "max_user_message_bytes"
	LimitMaxFinalAnswerBytes  LimitKind = "max_final_answer_bytes"
	LimitMaxToolArgumentBytes LimitKind = "max_tool_argument_bytes"
	LimitMaxToolResultBytes   LimitKind = "max_tool_result_bytes"
	LimitMaxHistoryMessages   LimitKind = "max_history_messages"
	LimitMaxHistoryBytes      LimitKind = "max_history_bytes"
	LimitMaxRunDuration       LimitKind = "max_run_duration"
	LimitMaxModelCallDuration LimitKind = "max_model_call_duration"
	LimitMaxToolCallDuration  LimitKind = "max_tool_call_duration"
)

// Actual saturates at the platform's largest int for size/count overflow.
// For deadlines Actual equals Limit: the duration reached at expiration.
type LimitError struct {
	Kind   LimitKind
	Limit  int64
	Actual int64
}

func (e LimitError) Error() string {
	return fmt.Sprintf("budget limit exceeded: %s (limit=%d, actual=%d)", e.Kind, e.Limit, e.Actual)
}

// An empty target kind matches any budget limit; a specified kind must match.
func (e LimitError) Is(target error) bool {
	if target == ErrMaxSteps {
		return e.Kind == LimitMaxSteps
	}
	t, ok := target.(LimitError)
	return ok && (t.Kind == "" || t.Kind == e.Kind)
}
func (e LimitError) Unwrap() error {
	// Invalid duration configuration is not an expired deadline.
	if e.Limit <= 0 || e.Actual < e.Limit {
		return nil
	}
	switch e.Kind {
	case LimitMaxRunDuration, LimitMaxModelCallDuration, LimitMaxToolCallDuration:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// Budget uses byte counts (not tokens/runes), positive counts and durations.
// A zero field is invalid; callers explicitly opt into DefaultBudget.
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

func DefaultBudget() Budget {
	return Budget{
		MaxSteps: 8, MaxToolCallsPerStep: 4, MaxTotalToolCalls: 16,
		MaxUserMessageBytes: 32 * 1024, MaxFinalAnswerBytes: 256 * 1024,
		MaxToolArgumentBytes: 64 * 1024, MaxToolResultBytes: 64 * 1024,
		MaxHistoryMessages: 128, MaxHistoryBytes: 2 * 1024 * 1024,
		MaxRunDuration: 5 * time.Minute, MaxModelCallDuration: 2 * time.Minute, MaxToolCallDuration: 30 * time.Second,
	}
}
func (b Budget) Validate() error {
	limits := []struct {
		kind  LimitKind
		value int64
	}{
		{LimitMaxSteps, int64(b.MaxSteps)},
		{LimitMaxToolCallsPerStep, int64(b.MaxToolCallsPerStep)},
		{LimitMaxTotalToolCalls, int64(b.MaxTotalToolCalls)},
		{LimitMaxUserMessageBytes, int64(b.MaxUserMessageBytes)},
		{LimitMaxFinalAnswerBytes, int64(b.MaxFinalAnswerBytes)},
		{LimitMaxToolArgumentBytes, int64(b.MaxToolArgumentBytes)},
		{LimitMaxToolResultBytes, int64(b.MaxToolResultBytes)},
		{LimitMaxHistoryMessages, int64(b.MaxHistoryMessages)},
		{LimitMaxHistoryBytes, int64(b.MaxHistoryBytes)},
		{LimitMaxRunDuration, int64(b.MaxRunDuration)},
		{LimitMaxModelCallDuration, int64(b.MaxModelCallDuration)},
		{LimitMaxToolCallDuration, int64(b.MaxToolCallDuration)},
	}
	for _, limit := range limits {
		if limit.value <= 0 {
			return InvalidConfigError{Err: LimitError{Kind: limit.kind, Limit: 1, Actual: limit.value}}
		}
	}
	return nil
}

// WithTimeoutCause preserves earlier parent deadlines automatically.
func (b Budget) ApplyRunTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(parent, b.MaxRunDuration, LimitError{Kind: LimitMaxRunDuration, Limit: int64(b.MaxRunDuration), Actual: int64(b.MaxRunDuration)})
}
func (b Budget) ApplyModelTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(parent, b.MaxModelCallDuration, LimitError{Kind: LimitMaxModelCallDuration, Limit: int64(b.MaxModelCallDuration), Actual: int64(b.MaxModelCallDuration)})
}
func (b Budget) ApplyToolTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(parent, b.MaxToolCallDuration, LimitError{Kind: LimitMaxToolCallDuration, Limit: int64(b.MaxToolCallDuration), Actual: int64(b.MaxToolCallDuration)})
}

// truncateResult normalizes invalid UTF-8 and marks every changed result.
// Tiny budgets use '~' so even a one-byte budget has a complete marker.
func (b Budget) truncateResult(result string) (string, bool) {
	originalBytes := len(result)
	if utf8.ValidString(result) && originalBytes <= b.MaxToolResultBytes {
		return result, false
	}
	result = strings.ToValidUTF8(result, "\uFFFD")
	indicator := fmt.Sprintf("\n[truncated: original_bytes=%d]", originalBytes)
	if len(indicator) > b.MaxToolResultBytes {
		indicator = "[truncated]"
	}
	if len(indicator) > b.MaxToolResultBytes {
		indicator = "~"
	}
	available := b.MaxToolResultBytes - len(indicator)
	if len(result) > available {
		result = result[:available]
	}
	for !utf8.ValidString(result) {
		result = result[:len(result)-1]
	}
	return result + indicator, true
}
