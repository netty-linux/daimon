package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
)

func assertStopped(t *testing.T, sink *MemoryEventSink, r Result, want StopReason) {
	t.Helper()
	events := sink.Events()
	stopped := 0
	for _, event := range events {
		if event.Kind == LoopStopped {
			stopped++
			if event.StopReason != want {
				t.Fatalf("event reason=%s want=%s", event.StopReason, want)
			}
		}
	}
	if stopped != 1 || events[len(events)-1].Kind != LoopStopped || r.StopReason != want {
		t.Fatalf("result=%+v events=%+v", r, events)
	}
}

func TestInputAndFinalLimits(t *testing.T) {
	for _, tc := range []struct {
		name              string
		user, answer      string
		budget            Budget
		reason            StopReason
		kind              LimitKind
		requests, history int
	}{
		{"user exact", "é", "ok", withLimits(2, 2, 3, 100), StopReasonCompleted, "", 1, 2},
		{"user over", "é!", "ok", withLimits(2, 2, 3, 100), StopReasonUserMessageLimit, LimitMaxUserMessageBytes, 0, 0},
		{"final exact", "u", "é", withLimits(3, 2, 3, 100), StopReasonCompleted, "", 1, 2},
		{"final over", "u", "é!", withLimits(3, 2, 3, 100), StopReasonFinalAnswerLimit, LimitMaxFinalAnswerBytes, 1, 1},
		{"initial history", "abc", "ok", withLimits(10, 10, 3, 2), StopReasonHistoryLimit, LimitMaxHistoryBytes, 0, 0},
		{"final history bytes", "u", "ok", withLimits(10, 10, 3, 2), StopReasonHistoryLimit, LimitMaxHistoryBytes, 1, 1},
		{"final history messages", "u", "ok", withLimits(10, 10, 1, 100), StopReasonHistoryLimit, LimitMaxHistoryMessages, 1, 1},
		{"history exact", "u", "ok", withLimits(10, 10, 2, 3), StopReasonCompleted, "", 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, m, sink := setup(t, final(tc.answer))
			l.Budget = tc.budget
			r, err := l.Run(context.Background(), tc.user)
			if tc.kind == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var limit LimitError
				if !errors.As(err, &limit) || limit.Kind != tc.kind || r.FinalAnswer != "" {
					t.Fatalf("%+v %v", r, err)
				}
			}
			if len(m.Requests()) != tc.requests || len(r.History) != tc.history {
				t.Fatalf("%+v", r)
			}
			if historyBytes(r.History) > tc.budget.MaxHistoryBytes || len(r.History) > tc.budget.MaxHistoryMessages {
				t.Fatal("history exceeded")
			}
			assertStopped(t, sink, r, tc.reason)
		})
	}
}
func withLimits(user, final, messages, bytes int) Budget {
	b := DefaultBudget()
	b.MaxUserMessageBytes = user
	b.MaxFinalAnswerBytes = final
	b.MaxHistoryMessages = messages
	b.MaxHistoryBytes = bytes
	return b
}

func TestBatchRejectedBeforeEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind LimitKind
	}{
		{"calls per step", LimitMaxToolCallsPerStep},
		{"total calls", LimitMaxTotalToolCalls},
		{"last argument", LimitMaxToolArgumentBytes},
		{"receipt ids", LimitMaxHistoryBytes},
		{"messages", LimitMaxHistoryMessages},
		{"overflow", LimitMaxHistoryBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []model.ToolCall{call("long-a", "echo", `{}`), call("long-b", "echo", `{"text":"x"}`)}
			l, _, sink := setup(t, toolStep(calls...), final("never"))
			p := &probeTool{}
			l.Registry = &tools.Registry{}
			if err := l.Registry.Register(p); err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "calls per step":
				l.Budget.MaxToolCallsPerStep = 1
			case "total calls":
				l.Budget.MaxTotalToolCalls = 1
			case "last argument":
				l.Budget.MaxToolArgumentBytes = 2
			case "receipt ids":
				l.Budget.MaxToolResultBytes = 10
				l.Budget.MaxHistoryBytes = 1 + messageBytes(model.Message{ToolCalls: calls}) + 20 // Missing receipt IDs.
			case "messages":
				l.Budget.MaxHistoryMessages = 3
			case "overflow":
				l.Budget.MaxHistoryBytes = maxInt
				l.Budget.MaxToolResultBytes = maxInt
			}
			r, err := l.Run(context.Background(), "u")
			var limit LimitError
			if !errors.As(err, &limit) || limit.Kind != tc.kind || len(p.inputs) != 0 || r.ToolCalls != 0 || len(r.History) != 1 {
				t.Fatalf("%+v %v inputs=%v", r, err, p.inputs)
			}
			assertStopped(t, sink, r, reasonFor(err))
			for _, event := range sink.Events() {
				if event.Kind == ToolRequested {
					t.Fatal("requested rejected tool")
				}
			}
		})
	}
}
func TestTotalCallsAcrossSteps(t *testing.T) {
	l, _, sink := setup(t, toolStep(call("a", "echo", `{"text":"a"}`)), toolStep(call("b", "echo", `{"text":"b"}`), call("c", "echo", `{"text":"c"}`)))
	l.Budget.MaxTotalToolCalls = 2
	r, err := l.Run(context.Background(), "u")
	if !errors.Is(err, LimitError{Kind: LimitMaxTotalToolCalls}) || r.ToolCalls != 1 || len(r.History) != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	assertStopped(t, sink, r, StopReasonMaxToolCalls)
}

type fixedResultTool struct {
	tools.Echo
	content string
	err     error
	calls   int
}

func (p *fixedResultTool) Execute(context.Context, json.RawMessage) (tools.ToolResult, error) {
	p.calls++
	return tools.ToolResult{Content: p.content}, p.err
}
func TestResultsFitReservation(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		err           error
		truncated     bool
	}{
		{"exact", strings.Repeat("x", 40), nil, false},
		{"oversized", strings.Repeat("é", 100), nil, true},
		{"invalid utf8", "a\xffb", nil, true},
		{"tool error", "", errors.New(strings.Repeat("failure", 20)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []model.ToolCall{call("long-id", "echo", `{}`)}
			l, _, sink := setup(t, toolStep(calls...))
			l.Budget.MaxSteps = 1
			l.Budget.MaxToolResultBytes = 40
			l.Budget.MaxToolArgumentBytes = 2
			l.Budget.MaxTotalToolCalls = 1
			l.Budget.MaxToolCallsPerStep = 1
			l.Budget.MaxHistoryMessages = 3
			l.Budget.MaxHistoryBytes = 1 + messageBytes(model.Message{ToolCalls: calls}) + len("long-id") + 40
			p := &fixedResultTool{content: tc.content, err: tc.err}
			l.Registry = &tools.Registry{}
			if err := l.Registry.Register(p); err != nil {
				t.Fatal(err)
			}
			r, err := l.Run(context.Background(), "u")
			if !errors.Is(err, ErrMaxSteps) || p.calls != 1 || r.ToolCalls != 1 || len(r.History) != 3 {
				t.Fatalf("%+v %v", r, err)
			}
			receipt := r.History[2]
			if receipt.ToolCallID != "long-id" || len(receipt.Content) > 40 || !utf8.ValidString(receipt.Content) || receipt.IsError != (tc.err != nil) {
				t.Fatal(receipt)
			}
			if (r.TruncatedToolResults == 1) != tc.truncated || historyBytes(r.History) > l.Budget.MaxHistoryBytes {
				t.Fatal(r)
			}
			assertStopped(t, sink, r, StopReasonMaxSteps)
		})
	}
}

type waitingModel struct{ returnNil bool }

func (m waitingModel) Generate(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
	<-ctx.Done()
	if m.returnNil {
		return model.ModelResponse{FinalText: "late"}, nil
	}
	return model.ModelResponse{}, ctx.Err()
}

type waitingTool struct {
	tools.Echo
	returnNil bool
	calls     int
}

func (p *waitingTool) Execute(ctx context.Context, _ json.RawMessage) (tools.ToolResult, error) {
	p.calls++
	<-ctx.Done()
	if p.returnNil {
		return tools.ToolResult{Content: "late"}, nil
	}
	return tools.ToolResult{}, ctx.Err()
}
func TestExecutionDeadlines(t *testing.T) {
	for _, target := range []string{"model", "tool"} {
		for _, scope := range []string{"call", "run", "external"} {
			for _, returnNil := range []bool{false, true} {
				t.Run(target+"/"+scope+"/"+map[bool]string{false: "error", true: "nil"}[returnNil], func(t *testing.T) {
					l, m, sink := setup(t, toolStep(call("a", "echo", `{}`), call("b", "echo", `{}`)), final("never"))
					p := &waitingTool{returnNil: returnNil}
					l.Registry = &tools.Registry{}
					if err := l.Registry.Register(p); err != nil {
						t.Fatal(err)
					}
					if target == "model" {
						l.Model = waitingModel{returnNil: returnNil}
					}
					ctx := context.Background()
					var cancel context.CancelFunc
					want := StopReasonRunTimeout
					kind := LimitMaxRunDuration
					switch scope {
					case "external":
						ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
						defer cancel()
						want = StopReasonExternalDeadline
						kind = ""
					case "run":
						l.Budget.MaxRunDuration = 30 * time.Millisecond
					case "call":
						if target == "model" {
							l.Budget.MaxModelCallDuration = 30 * time.Millisecond
							want = StopReasonModelTimeout
							kind = LimitMaxModelCallDuration
						} else {
							l.Budget.MaxToolCallDuration = 30 * time.Millisecond
							want = StopReasonToolTimeout
							kind = LimitMaxToolCallDuration
						}
					}
					r, err := l.Run(ctx, "u")
					if !errors.Is(err, context.DeadlineExceeded) || r.FinalAnswer != "" {
						t.Fatalf("%+v %v", r, err)
					}
					if kind != "" && !errors.Is(err, LimitError{Kind: kind}) {
						t.Fatal(err)
					}
					if target == "tool" && (p.calls != 1 || len(m.Requests()) != 1 || r.ToolCalls != 1) {
						t.Fatalf("calls=%d result=%+v", p.calls, r)
					}
					if target == "model" && p.calls != 0 {
						t.Fatal("tool executed")
					}
					assertStopped(t, sink, r, want)
				})
			}
		}
	}
}
func TestCanceledToolErrorStopsBatch(t *testing.T) {
	l, _, sink := setup(t, toolStep(call("a", "echo", `{}`), call("b", "echo", `{}`)))
	p := &fixedResultTool{err: context.Canceled}
	l.Registry = &tools.Registry{}
	if err := l.Registry.Register(p); err != nil {
		t.Fatal(err)
	}
	r, err := l.Run(context.Background(), "u")
	if !errors.Is(err, context.Canceled) || p.calls != 1 || r.ToolCalls != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	assertStopped(t, sink, r, StopReasonCanceled)
}

func TestToolOwnDeadlineStopsBatch(t *testing.T) {
	l, _, sink := setup(t, toolStep(call("a", "echo", `{}`), call("b", "echo", `{}`)))
	p := &fixedResultTool{err: context.DeadlineExceeded}
	l.Registry = &tools.Registry{}
	if err := l.Registry.Register(p); err != nil {
		t.Fatal(err)
	}
	r, err := l.Run(context.Background(), "u")
	if !errors.Is(err, context.DeadlineExceeded) || p.calls != 1 || r.ToolCalls != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	assertStopped(t, sink, r, StopReasonToolTimeout)
}

func TestMissingComponentsHaveStopReason(t *testing.T) {
	for _, missing := range []string{"model", "registry"} {
		l, m, sink := setup(t, final("never"))
		if missing == "model" {
			l.Model = nil
		} else {
			l.Registry = nil
		}
		r, err := l.Run(context.Background(), "u")
		if !errors.Is(err, ErrInvalidConfig) || len(m.Requests()) != 0 {
			t.Fatal(err)
		}
		assertStopped(t, sink, r, StopReasonInvalidConfig)
	}
}
func TestStopReasonsAndSingleStopEvent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script model.ScriptStep
		want   StopReason
	}{
		{"completed", final("ok"), StopReasonCompleted},
		{"model error", model.ScriptStep{Err: errors.New("failure")}, StopReasonModelError},
		{"invalid response", final(" "), StopReasonInvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, _, sink := setup(t, tc.script)
			r, _ := l.Run(context.Background(), "u")
			assertStopped(t, sink, r, tc.want)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l, m, sink := setup(t, final("never"))
	r, err := l.Run(ctx, "u")
	if !errors.Is(err, context.Canceled) || len(m.Requests()) != 0 {
		t.Fatal(err)
	}
	assertStopped(t, sink, r, StopReasonCanceled)
}
