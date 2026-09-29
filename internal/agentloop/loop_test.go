package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
)

func call(id, name, args string) model.ToolCall {
	return model.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}
func final(text string) model.ScriptStep {
	return model.ScriptStep{Response: model.ModelResponse{FinalText: text}}
}
func toolStep(calls ...model.ToolCall) model.ScriptStep {
	return model.ScriptStep{Response: model.ModelResponse{ToolCalls: calls}}
}
func setup(t *testing.T, script ...model.ScriptStep) (Loop, *model.Scripted, *MemoryEventSink) {
	t.Helper()
	r := &tools.Registry{}
	if err := r.Register(tools.Echo{}); err != nil {
		t.Fatal(err)
	}
	m := model.NewScripted(script...)
	sink := &MemoryEventSink{}
	b := DefaultBudget()
	b.MaxSteps = 3
	return Loop{Model: m, Registry: r, Budget: b, Sink: sink}, m, sink
}

func TestFinalWithoutTool(t *testing.T) {
	l, m, sink := setup(t, final("done"), final("must not run"))
	r, err := l.Run(context.Background(), "hello")
	if err != nil || r.FinalAnswer != "done" || r.Steps != 1 || len(r.History) != 2 || len(m.Requests()) != 1 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, FinalAnswer, LoopStopped)
	if r.History[0].Role != model.RoleUser || r.History[1].Role != model.RoleAssistant {
		t.Fatal(r.History)
	}
}

func TestToolRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, tool, args, content string
		failed                    bool
	}{
		{"echo", "echo", `{"text":"DAIMON"}`, "DAIMON", false},
		{"unknown", "missing", `{}`, "unknown tool", true},
		{"bad JSON", "echo", `{`, "invalid JSON arguments", true},
		{"missing field", "echo", `{}`, "", true},
		{"unknown field", "echo", `{"text":"x","other":1}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, m, sink := setup(t, toolStep(call("id-1", tc.tool, tc.args)), final("recovered"))
			r, err := l.Run(context.Background(), "hello")
			if err != nil || r.Steps != 2 || r.ToolCalls != 1 || len(r.History) != 4 {
				t.Fatalf("%+v %v", r, err)
			}
			msg := r.History[2]
			if msg.Role != model.RoleTool || msg.ToolCallID != "id-1" || msg.IsError != tc.failed {
				t.Fatal(msg)
			}
			if tc.content != "" && msg.Content != tc.content {
				t.Fatal(msg)
			}
			if !reflect.DeepEqual(m.Requests()[1].Messages, r.History[:3]) {
				t.Fatal("model did not receive history")
			}
			if len(m.Requests()[0].Tools) != 1 || m.Requests()[0].Tools[0].Name != "echo" {
				t.Fatal("missing tool description")
			}
			kind := ToolCompleted
			if tc.failed {
				kind = ToolFailed
			}
			assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, ToolRequested, kind, ModelRequested, ModelResponded, FinalAnswer, LoopStopped)
		})
	}
}

type probeTool struct {
	tools.Echo
	inputs     []string
	err        error
	controlled bool
	cancel     context.CancelFunc
}

func (p *probeTool) Execute(_ context.Context, args json.RawMessage) (tools.ToolResult, error) {
	p.inputs = append(p.inputs, string(args))
	if p.cancel != nil {
		p.cancel()
	}
	return tools.ToolResult{Content: string(args), IsError: p.controlled}, p.err
}

func TestMultipleToolsSequentialAndCorrelated(t *testing.T) {
	l, _, sink := setup(t, toolStep(call("a", "echo", `{"text":"first"}`), call("b", "echo", `{"text":"second"}`)), final("ok"))
	p := &probeTool{}
	l.Registry = &tools.Registry{}
	if err := l.Registry.Register(p); err != nil {
		t.Fatal(err)
	}
	r, err := l.Run(context.Background(), "start")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.inputs, []string{`{"text":"first"}`, `{"text":"second"}`}) {
		t.Fatal(p.inputs)
	}
	if r.History[2].ToolCallID != "a" || r.History[3].ToolCallID != "b" {
		t.Fatal(r.History)
	}
	assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, ToolRequested, ToolCompleted, ToolRequested, ToolCompleted, ModelRequested, ModelResponded, FinalAnswer, LoopStopped)
	if sink.Events()[5].ToolIndex != 2 || sink.Events()[6].Step != 1 {
		t.Fatal(sink.Events())
	}
}

func TestToolErrorsRecover(t *testing.T) {
	for _, controlled := range []bool{false, true} {
		l, _, sink := setup(t, toolStep(call("id", "echo", `{}`)), final("ok"))
		p := &probeTool{controlled: controlled}
		if !controlled {
			p.err = errors.New("tool unavailable")
		}
		l.Registry = &tools.Registry{}
		if err := l.Registry.Register(p); err != nil {
			t.Fatal(err)
		}
		r, err := l.Run(context.Background(), "start")
		if err != nil || !r.History[2].IsError || sink.Events()[4].Kind != ToolFailed {
			t.Fatalf("%+v %v", r, err)
		}
	}
}

func TestModelErrorAndMaxSteps(t *testing.T) {
	cause := errors.New("model unavailable")
	l, _, sink := setup(t, model.ScriptStep{Err: cause})
	r, err := l.Run(context.Background(), "start")
	var modelErr *ModelError
	if !errors.Is(err, cause) || !errors.As(err, &modelErr) || modelErr.Step != 1 || len(r.History) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	assertKinds(t, sink, LoopStarted, ModelRequested, LoopStopped)
	l, m, sink := setup(t, toolStep(call("1", "echo", `{"text":"x"}`)), final("not reached"))
	l.Budget.MaxSteps = 1
	r, err = l.Run(context.Background(), "start")
	if !errors.Is(err, ErrMaxSteps) || r.Steps != 1 || len(m.Requests()) != 1 || len(r.History) != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, ToolRequested, ToolCompleted, LoopStopped)
}

func TestInvalidResponses(t *testing.T) {
	for _, response := range []model.ModelResponse{
		{}, {FinalText: "  "},
		{FinalText: "ambiguous", ToolCalls: []model.ToolCall{call("a", "echo", `{}`)}},
		{ToolCalls: []model.ToolCall{call("", "echo", `{}`)}},
		{ToolCalls: []model.ToolCall{call("a", "", `{}`)}},
		{ToolCalls: []model.ToolCall{call("a", "echo", `{}`), call("a", "echo", `{}`)}},
	} {
		l, _, sink := setup(t, model.ScriptStep{Response: response})
		r, err := l.Run(context.Background(), "start")
		if !errors.Is(err, ErrInvalidResponse) || len(r.History) != 1 {
			t.Fatalf("%+v %v", r, err)
		}
		assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, LoopStopped)
	}
	l, _, _ := setup(t, toolStep(call("a", "echo", `{"text":"x"}`)), toolStep(call("a", "echo", `{"text":"x"}`)))
	if _, err := l.Run(context.Background(), "start"); !errors.Is(err, ErrInvalidResponse) {
		t.Fatal(err)
	}
}

func TestCanceledBeforeRun(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := context.Canceled
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Unix(1, 0))
			want = context.DeadlineExceeded
		} else {
			cancel()
		}
		l, m, sink := setup(t, final("never"))
		r, err := l.Run(ctx, "start")
		cancel()
		if !errors.Is(err, want) || r.Steps != 0 || len(m.Requests()) != 0 {
			t.Fatalf("%+v %v", r, err)
		}
		assertKinds(t, sink, LoopStarted, LoopStopped)
	}
}

func TestCancellationDuringTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l, m, sink := setup(t, toolStep(call("a", "echo", `{}`), call("b", "echo", `{}`)), final("never"))
	p := &probeTool{cancel: cancel}
	l.Registry = &tools.Registry{}
	if err := l.Registry.Register(p); err != nil {
		t.Fatal(err)
	}
	r, err := l.Run(ctx, "start")
	if !errors.Is(err, context.Canceled) || len(p.inputs) != 1 || len(m.Requests()) != 1 || len(r.History) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, ToolRequested, LoopStopped)
}

type cancelModel struct{ cancel context.CancelFunc }

func (m cancelModel) Generate(context.Context, model.ModelRequest) (model.ModelResponse, error) {
	m.cancel()
	return model.ModelResponse{FinalText: "must not succeed"}, nil
}
func TestCancellationDuringModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l, _, sink := setup(t)
	l.Model = cancelModel{cancel}
	r, err := l.Run(ctx, "start")
	if !errors.Is(err, context.Canceled) || r.FinalAnswer != "" {
		t.Fatalf("%+v %v", r, err)
	}
	assertKinds(t, sink, LoopStarted, ModelRequested, LoopStopped)
}

func TestInvalidConfigAndNoop(t *testing.T) {
	for _, l := range []Loop{{}, {Model: model.NewScripted(), Registry: &tools.Registry{}, Budget: Budget{MaxSteps: -1}}} {
		if _, err := l.Run(context.Background(), "start"); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal(err)
		}
	}
	l, _, _ := setup(t, final("ok"))
	l.Sink = nil
	if _, err := l.Run(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
}

func assertKinds(t *testing.T, sink *MemoryEventSink, want ...EventKind) {
	t.Helper()
	var got []EventKind
	for _, event := range sink.Events() {
		got = append(got, event.Kind)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%v want=%v", got, want)
	}
}

type cancelSink struct {
	MemoryEventSink
	on     EventKind
	cancel context.CancelFunc
}

func (s *cancelSink) Record(ctx context.Context, event Event) {
	s.MemoryEventSink.Record(ctx, event)
	if event.Kind == s.on {
		s.cancel()
	}
}

func TestCancellationImmediatelyBeforeEffects(t *testing.T) {
	for _, kind := range []EventKind{ModelRequested, ToolRequested, ToolCompleted, ModelResponded} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			l, m, _ := setup(t, toolStep(call("a", "echo", `{}`), call("b", "echo", `{}`)), final("never"))
			p := &probeTool{}
			l.Registry = &tools.Registry{}
			if err := l.Registry.Register(p); err != nil {
				t.Fatal(err)
			}
			sink := &cancelSink{on: kind, cancel: cancel}
			l.Sink = sink
			r, err := l.Run(ctx, "start")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			wantTools, wantModels := 0, 1
			if kind == ModelRequested {
				wantModels = 0
			}
			if kind == ToolCompleted {
				wantTools = 1
			}
			if len(p.inputs) != wantTools || len(m.Requests()) != wantModels || r.Steps != wantModels {
				t.Fatalf("tools=%d models=%d steps=%d", len(p.inputs), len(m.Requests()), r.Steps)
			}
			events := sink.Events()
			if events[len(events)-1].Kind != LoopStopped {
				t.Fatal(events)
			}
		})
	}
}

type mutatingModel struct{ calls int }

func (m *mutatingModel) Generate(_ context.Context, req model.ModelRequest) (model.ModelResponse, error) {
	m.calls++
	req.Messages[0].Content = "changed"
	req.Tools[0].InputSchema[0] = 'x'
	if m.calls == 1 {
		return toolStep(call("id", "echo", `{"text":"original"}`)).Response, nil
	}
	req.Messages[1].ToolCalls[0].Arguments[0] = 'x'
	return model.ModelResponse{FinalText: "done"}, nil
}

func TestLoopOwnsHistory(t *testing.T) {
	l, _, sink := setup(t)
	l.Model = &mutatingModel{}
	r, err := l.Run(context.Background(), "original user")
	if err != nil {
		t.Fatal(err)
	}
	if r.History[0].Content != "original user" || string(r.History[1].ToolCalls[0].Arguments) != `{"text":"original"}` || !json.Valid(l.Registry.Descriptions()[0].InputSchema) {
		t.Fatal("model changed loop-owned data")
	}
	events := sink.Events()
	events[0].Kind = ToolFailed
	if sink.Events()[0].Kind != LoopStarted {
		t.Fatal("aliased events")
	}
}
