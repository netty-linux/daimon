package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
)

// scriptedAuthorizer records every request and returns a fixed result,
// unless authorize overrides the behavior for special cases.
type scriptedAuthorizer struct {
	decision  ToolDecision
	err       error
	requests  []ToolAuthorizationRequest
	authorize func(context.Context, ToolAuthorizationRequest) (ToolDecision, error)
}

func (a *scriptedAuthorizer) Authorize(ctx context.Context, request ToolAuthorizationRequest) (ToolDecision, error) {
	a.requests = append(a.requests, request)
	if a.authorize != nil {
		return a.authorize(ctx, request)
	}
	return a.decision, a.err
}

// blockingAuthorizer never returns before the supplied context ends.
type blockingAuthorizer struct{}

func (blockingAuthorizer) Authorize(ctx context.Context, _ ToolAuthorizationRequest) (ToolDecision, error) {
	<-ctx.Done()
	return ToolDecisionDeny, context.Cause(ctx)
}

// namedProbe records executions and is registerable under any tool name.
type namedProbe struct {
	name    string
	inputs  []string
	observe func(string)
}

func (p *namedProbe) Name() string                 { return p.name }
func (p *namedProbe) Description() string          { return "Record the supplied text." }
func (p *namedProbe) InputSchema() json.RawMessage { return tools.Echo{}.InputSchema() }
func (p *namedProbe) Execute(_ context.Context, args json.RawMessage) (tools.ToolResult, error) {
	p.inputs = append(p.inputs, string(args))
	if p.observe != nil {
		p.observe(string(args))
	}
	return tools.ToolResult{Content: string(args)}, nil
}

func probeLoop(t *testing.T, loop Loop, probes ...*namedProbe) Loop {
	t.Helper()
	loop.Registry = &tools.Registry{}
	for _, probe := range probes {
		if err := loop.Registry.Register(probe); err != nil {
			t.Fatal(err)
		}
	}
	return loop
}

func TestNilAuthorizerFailsBeforeModelCall(t *testing.T) {
	l, m, sink := setup(t, final("never"))
	l.Authorizer = nil
	r, err := l.Run(context.Background(), "start")
	if !errors.Is(err, ErrInvalidConfig) || len(m.Requests()) != 0 || r.Steps != 0 || r.ToolCalls != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	assertStopped(t, sink, r, StopReasonInvalidConfig)
}

func TestAllowAllAuthorizerKeepsExecution(t *testing.T) {
	l, m, sink := setup(t, toolStep(call("id-1", "echo", `{"text":"DAIMON"}`)), final("done"))
	r, err := l.Run(context.Background(), "start")
	if err != nil || r.FinalAnswer != "done" || r.Steps != 2 || r.ToolCalls != 1 || len(r.History) != 4 {
		t.Fatalf("%+v %v", r, err)
	}
	receipt := r.History[2]
	if receipt.Role != model.RoleTool || receipt.ToolCallID != "id-1" || receipt.IsError || receipt.Content != "DAIMON" {
		t.Fatal(receipt)
	}
	events := sink.Events()
	assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, ToolAllowed, ToolRequested, ToolCompleted, ModelRequested, ModelResponded, FinalAnswer, LoopStopped)
	if events[3].Kind != ToolAllowed || events[3].Step != 1 || events[3].ToolIndex != 1 || events[4].Kind != ToolRequested || events[4].ToolIndex != 1 {
		t.Fatal(events)
	}
	if !reflect.DeepEqual(m.Requests()[1].Messages, r.History[:3]) {
		t.Fatal("model did not receive history")
	}
}

func TestDenyAllAuthorizerExecutesNoTool(t *testing.T) {
	l, m, sink := setup(t, toolStep(call("id-1", "echo", `{"text":"DAIMON"}`)), final("done"))
	l.Authorizer = DenyAllAuthorizer{}
	probe := &namedProbe{name: "echo"}
	l = probeLoop(t, l, probe)
	r, err := l.Run(context.Background(), "start")
	if err != nil || r.Steps != 2 || r.ToolCalls != 1 || len(probe.inputs) != 0 || r.FinalAnswer != "done" {
		t.Fatalf("%+v %v probe=%v", r, err, probe.inputs)
	}
	receipt := r.History[2]
	if receipt.Role != model.RoleTool || receipt.ToolCallID != "id-1" || !receipt.IsError || receipt.Content != "tool denied" {
		t.Fatal(receipt)
	}
	assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, ToolDenied, ModelRequested, ModelResponded, FinalAnswer, LoopStopped)
	for _, event := range sink.Events() {
		if event.Kind == ToolRequested || event.Kind == ToolAllowed {
			t.Fatal(event)
		}
	}
	if !reflect.DeepEqual(m.Requests()[1].Messages, r.History[:3]) {
		t.Fatal("denial receipt not sent to model")
	}
}

func TestMixedAllowDenyBatch(t *testing.T) {
	auth := &scriptedAuthorizer{authorize: func(_ context.Context, request ToolAuthorizationRequest) (ToolDecision, error) {
		if request.Call.Name == "alpha" {
			return ToolDecisionAllow, nil
		}
		return ToolDecisionDeny, nil
	}}
	l, m, sink := setup(t,
		toolStep(call("a", "alpha", `{"text":"first"}`), call("b", "beta", `{"text":"second"}`)),
		final("ok"),
	)
	l.Authorizer = auth
	alpha := &namedProbe{name: "alpha"}
	beta := &namedProbe{name: "beta"}
	l = probeLoop(t, l, alpha, beta)
	r, err := l.Run(context.Background(), "start")
	if err != nil || r.Steps != 2 || r.ToolCalls != 2 || r.FinalAnswer != "ok" {
		t.Fatalf("%+v %v", r, err)
	}
	if !reflect.DeepEqual(alpha.inputs, []string{`{"text":"first"}`}) || len(beta.inputs) != 0 {
		t.Fatalf("alpha=%v beta=%v", alpha.inputs, beta.inputs)
	}
	if len(auth.requests) != 2 {
		t.Fatalf("requests=%+v", auth.requests)
	}
	if r.History[2].ToolCallID != "a" || r.History[2].IsError || r.History[2].Content != `{"text":"first"}` {
		t.Fatal(r.History)
	}
	if r.History[3].ToolCallID != "b" || !r.History[3].IsError || r.History[3].Content != "tool denied" {
		t.Fatal(r.History)
	}
	events := sink.Events()
	assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, ToolAllowed, ToolRequested, ToolCompleted, ToolDenied, ModelRequested, ModelResponded, FinalAnswer, LoopStopped)
	for i, want := range []EventKind{ToolAllowed, ToolRequested, ToolCompleted, ToolDenied} {
		if events[i+3].Kind != want {
			t.Fatal(events)
		}
	}
	if events[3].ToolIndex != 1 || events[6].ToolIndex != 2 || events[5].Step != 1 {
		t.Fatal(events)
	}
	if !reflect.DeepEqual(m.Requests()[1].Messages, r.History[:4]) {
		t.Fatal("model did not receive history")
	}
}

func TestUnknownAndInvalidArgumentsSkipAuthorization(t *testing.T) {
	auth := &scriptedAuthorizer{decision: ToolDecisionDeny}
	l, _, sink := setup(t, toolStep(call("a", "missing", `{}`), call("b", "echo", `{`)), final("ok"))
	l.Authorizer = auth
	r, err := l.Run(context.Background(), "start")
	if err != nil || r.ToolCalls != 2 || len(auth.requests) != 0 {
		t.Fatalf("%+v %v requests=%+v", r, err, auth.requests)
	}
	if r.History[2].Content != "unknown tool" || !r.History[2].IsError || r.History[3].Content != "invalid JSON arguments" || !r.History[3].IsError {
		t.Fatal(r.History)
	}
	assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, ToolRequested, ToolFailed, ToolRequested, ToolFailed, ModelRequested, ModelResponded, FinalAnswer, LoopStopped)
}

func TestAuthorizerReceivesCallAndArguments(t *testing.T) {
	auth := &scriptedAuthorizer{decision: ToolDecisionAllow}
	l, _, _ := setup(t, toolStep(call("id-7", "echo", `{"text":"payload"}`)), final("ok"))
	l.Authorizer = auth
	r, err := l.Run(context.Background(), "start")
	if err != nil || len(auth.requests) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	request := auth.requests[0]
	if request.Call.ID != "id-7" || request.Call.Name != "echo" || string(request.Call.Arguments) != `{"text":"payload"}` {
		t.Fatal(request)
	}
	if request.Step != 1 || request.ToolIndex != 1 {
		t.Fatal(request)
	}
}

func TestAuthorizationArgumentsNeverAppearInEvents(t *testing.T) {
	const secret = "argument-secret-42"
	l, _, sink := setup(t, toolStep(call("id-1", "echo", fmt.Sprintf(`{"text":%q}`, secret))), final("ok"))
	r, err := l.Run(context.Background(), "start")
	if err != nil {
		t.Fatal(err)
	}
	if r.FinalAnswer != "ok" || !strings.Contains(r.History[2].Content, secret) {
		t.Fatalf("%+v %v", r, r.History)
	}
	for _, event := range sink.Events() {
		text := fmt.Sprintf("%+v", event)
		if strings.Contains(text, secret) {
			t.Fatal(text)
		}
	}
}

func TestAuthorizationRequestIsDefensivelyCopied(t *testing.T) {
	auth := &scriptedAuthorizer{authorize: func(_ context.Context, request ToolAuthorizationRequest) (ToolDecision, error) {
		for i := range request.Call.Arguments {
			request.Call.Arguments[i] = 'z'
		}
		return ToolDecisionAllow, nil
	}}
	l, m, _ := setup(t, toolStep(call("id-1", "echo", `{"text":"original"}`)), final("ok"))
	l.Authorizer = auth
	r, err := l.Run(context.Background(), "start")
	if err != nil || r.ToolCalls != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if string(r.History[1].ToolCalls[0].Arguments) != `{"text":"original"}` {
		t.Fatal("authorizer changed loop history")
	}
	if r.History[2].IsError || r.History[2].Content != "original" {
		t.Fatal(r.History[2])
	}
	sent := m.Requests()[1].Messages[1].ToolCalls[0].Arguments
	if string(sent) != `{"text":"original"}` {
		t.Fatal("authorizer changed the model request")
	}
}

func TestAuthorizerErrorFailsClosed(t *testing.T) {
	cause := errors.New("policy unavailable")
	auth := &scriptedAuthorizer{err: cause}
	probe := &namedProbe{name: "echo"}
	l, m, sink := setup(t, toolStep(call("id-1", "echo", `{"text":"x"}`)), final("never"))
	l.Authorizer = auth
	l = probeLoop(t, l, probe)
	r, err := l.Run(context.Background(), "start")
	var authErr *AuthorizationError
	if !errors.As(err, &authErr) || authErr.Step != 1 || authErr.ToolIndex != 1 || !errors.Is(err, cause) {
		t.Fatalf("%+v %v", r, err)
	}
	if authErr.Error() != "authorization failed at step 1 tool 1" || strings.Contains(authErr.Error(), cause.Error()) {
		t.Fatal(authErr.Error())
	}
	if r.StopReason != StopReasonAuthorizationError || len(probe.inputs) != 0 || len(m.Requests()) != 1 || len(r.History) != 1 {
		t.Fatalf("%+v inputs=%v", r, probe.inputs)
	}
	assertStopped(t, sink, r, StopReasonAuthorizationError)
	assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, LoopStopped)
}

func TestInvalidToolDecisionFailsClosed(t *testing.T) {
	auth := &scriptedAuthorizer{decision: ToolDecision("maybe")}
	probe := &namedProbe{name: "echo"}
	l, m, sink := setup(t, toolStep(call("id-1", "echo", `{"text":"x"}`)), final("never"))
	l.Authorizer = auth
	l = probeLoop(t, l, probe)
	r, err := l.Run(context.Background(), "start")
	var authErr *AuthorizationError
	if !errors.As(err, &authErr) || !errors.Is(err, errInvalidDecision) {
		t.Fatalf("%+v %v", r, err)
	}
	if authErr.Step != 1 || authErr.ToolIndex != 1 || authErr.Error() != "authorization failed at step 1 tool 1" {
		t.Fatal(authErr)
	}
	if r.StopReason != StopReasonAuthorizationError || len(probe.inputs) != 0 || len(m.Requests()) != 1 || len(r.History) != 1 {
		t.Fatalf("%+v inputs=%v", r, probe.inputs)
	}
	assertStopped(t, sink, r, StopReasonAuthorizationError)
	assertKinds(t, sink, LoopStarted, ModelRequested, ModelResponded, LoopStopped)
}

func TestParentDeadlineDuringAuthorization(t *testing.T) {
	probe := &namedProbe{name: "echo"}
	l, _, sink := setup(t, toolStep(call("a", "echo", `{}`)), final("never"))
	l.Authorizer = blockingAuthorizer{}
	l = probeLoop(t, l, probe)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r, err := l.Run(ctx, "u")
	if !errors.Is(err, context.DeadlineExceeded) || len(probe.inputs) != 0 || len(r.History) != 1 {
		t.Fatalf("%+v %v inputs=%v", r, err, probe.inputs)
	}
	assertStopped(t, sink, r, StopReasonExternalDeadline)
}

func TestRunTimeoutDuringAuthorization(t *testing.T) {
	probe := &namedProbe{name: "echo"}
	l, _, sink := setup(t, toolStep(call("a", "echo", `{}`)), final("never"))
	l.Authorizer = blockingAuthorizer{}
	l.Budget.MaxRunDuration = 30 * time.Millisecond
	l = probeLoop(t, l, probe)
	r, err := l.Run(context.Background(), "u")
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, LimitError{Kind: LimitMaxRunDuration}) {
		t.Fatalf("%+v %v", r, err)
	}
	if len(probe.inputs) != 0 || len(r.History) != 1 {
		t.Fatalf("%+v inputs=%v", r, probe.inputs)
	}
	assertStopped(t, sink, r, StopReasonRunTimeout)
}

func TestCanceledAuthorizationStopsBatch(t *testing.T) {
	auth := &scriptedAuthorizer{err: context.Canceled}
	probe := &namedProbe{name: "echo"}
	l, m, sink := setup(t, toolStep(call("a", "echo", `{}`)), final("never"))
	l.Authorizer = auth
	l = probeLoop(t, l, probe)
	r, err := l.Run(context.Background(), "u")
	var authErr *AuthorizationError
	if !errors.Is(err, context.Canceled) || errors.As(err, &authErr) {
		t.Fatalf("%+v %v", r, err)
	}
	if len(probe.inputs) != 0 || len(m.Requests()) != 1 || len(r.History) != 1 {
		t.Fatalf("%+v inputs=%v", r, probe.inputs)
	}
	assertStopped(t, sink, r, StopReasonCanceled)
}

func TestBudgetRejectedBeforeAuthorization(t *testing.T) {
	auth := &scriptedAuthorizer{decision: ToolDecisionAllow}
	l, _, sink := setup(t, toolStep(call("a", "echo", `{}`), call("b", "echo", `{}`)), final("never"))
	l.Authorizer = auth
	l.Budget.MaxToolCallsPerStep = 1
	r, err := l.Run(context.Background(), "u")
	var limit LimitError
	if !errors.As(err, &limit) || limit.Kind != LimitMaxToolCallsPerStep {
		t.Fatalf("%+v %v", r, err)
	}
	if len(auth.requests) != 0 || r.ToolCalls != 0 || len(r.History) != 1 {
		t.Fatalf("%+v requests=%+v", r, auth.requests)
	}
	assertStopped(t, sink, r, StopReasonMaxToolCalls)
}

func TestDecisionsCollectedBeforeFirstExecute(t *testing.T) {
	var order []string
	auth := &scriptedAuthorizer{authorize: func(_ context.Context, request ToolAuthorizationRequest) (ToolDecision, error) {
		order = append(order, "auth:"+request.Call.ID)
		return ToolDecisionAllow, nil
	}}
	alpha := &namedProbe{name: "alpha", observe: func(args string) { order = append(order, "exec:"+args) }}
	beta := &namedProbe{name: "beta", observe: func(args string) { order = append(order, "exec:"+args) }}
	l, _, _ := setup(t, toolStep(call("a", "alpha", `{"text":"first"}`), call("b", "beta", `{"text":"second"}`)), final("ok"))
	l.Authorizer = auth
	l = probeLoop(t, l, alpha, beta)
	r, err := l.Run(context.Background(), "u")
	if err != nil || r.ToolCalls != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	want := []string{"auth:a", "auth:b", `exec:{"text":"first"}`, `exec:{"text":"second"}`}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}
