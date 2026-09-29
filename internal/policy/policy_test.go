package policy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
)

func requestFor(name, arguments string) agentloop.ToolAuthorizationRequest {
	return agentloop.ToolAuthorizationRequest{
		Call: model.ToolCall{ID: "id-1", Name: name, Arguments: []byte(arguments)},
		Step: 1, ToolIndex: 1,
	}
}

func TestDefaultCLIPolicy(t *testing.T) {
	policy := DefaultCLIPolicy()
	for name, want := range map[string]Decision{
		"echo": Allow, "list_dir": RequireApproval, "read_file": RequireApproval,
		"write_file": Deny, "bash": Deny, "": Deny,
	} {
		if got := policy.Decide(name); got != want {
			t.Fatalf("%s=%d want=%d", name, got, want)
		}
	}
}

func TestStaticPolicyFallback(t *testing.T) {
	policy := StaticPolicy{Rules: map[string]Decision{"echo": Allow}}
	// Deny is the zero value: an under-specified policy fails closed.
	if policy.Decide("echo") != Allow || policy.Decide("other") != Deny {
		t.Fatal("unexpected decisions")
	}
}

type scriptedApprovals struct {
	granted bool
	err     error
	calls   int
}

func (s *scriptedApprovals) Approve(context.Context, agentloop.ToolAuthorizationRequest) (bool, error) {
	s.calls++
	return s.granted, s.err
}

func TestAuthorizerPolicyPaths(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision Decision
		granted  bool
		want     agentloop.ToolDecision
		wantErr  bool
		approve  bool
	}{
		{"allow", Allow, false, agentloop.ToolDecisionAllow, false, false},
		{"deny", Deny, false, agentloop.ToolDecisionDeny, false, false},
		{"approval granted", RequireApproval, true, agentloop.ToolDecisionAllow, false, true},
		{"approval denied", RequireApproval, false, agentloop.ToolDecisionDeny, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			approvals := &scriptedApprovals{granted: tc.granted}
			authorizer := &Authorizer{Policy: StaticPolicy{Rules: map[string]Decision{"echo": tc.decision}}, Approvals: approvals}
			decision, err := authorizer.Authorize(context.Background(), requestFor("echo", `{"text":"x"}`))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || decision != tc.want || (approvals.calls > 0) != tc.approve {
				t.Fatalf("decision=%s err=%v approvals=%d", decision, err, approvals.calls)
			}
		})
	}
}

func TestAuthorizerFailsClosedOnInvalidPolicy(t *testing.T) {
	approvals := &scriptedApprovals{granted: true}
	authorizer := &Authorizer{
		Policy:    StaticPolicy{Rules: map[string]Decision{"echo": Decision(9)}, Fallback: Decision(8)},
		Approvals: approvals,
	}
	decision, err := authorizer.Authorize(context.Background(), requestFor("echo", "{}"))
	if err == nil || decision != "" || approvals.calls != 0 {
		t.Fatalf("decision=%q err=%v approvals=%d", decision, err, approvals.calls)
	}
}

func TestAuthorizerApprovalErrorFailsClosed(t *testing.T) {
	cause := errors.New("terminal unavailable")
	authorizer := &Authorizer{
		Policy:    StaticPolicy{Rules: map[string]Decision{"echo": RequireApproval}},
		Approvals: &scriptedApprovals{err: cause},
	}
	decision, err := authorizer.Authorize(context.Background(), requestFor("echo", "{}"))
	if !errors.Is(err, cause) || decision != "" {
		t.Fatalf("decision=%q err=%v", decision, err)
	}
}

func TestAuthorizerContextErrorReturnsDirectly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	authorizer := &Authorizer{
		Policy:    DefaultCLIPolicy(),
		Approvals: &scriptedApprovals{err: context.Canceled},
	}
	if _, err := authorizer.Authorize(ctx, requestFor("read_file", "{}")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAuthorizerRecordsApprovalEventsWithoutSecrets(t *testing.T) {
	const secret = "argument-secret-42"
	sink := &agentloop.MemoryEventSink{}
	authorizer := &Authorizer{
		Policy:    StaticPolicy{Rules: map[string]Decision{"read_file": RequireApproval}},
		Approvals: &scriptedApprovals{granted: true},
		Sink:      sink,
	}
	decision, err := authorizer.Authorize(context.Background(), requestFor("read_file", `{"path":"`+secret+`"}`))
	if err != nil || decision != agentloop.ToolDecisionAllow {
		t.Fatal(decision, err)
	}
	// Denied path: fresh sink, no grant.
	deniedSink := &agentloop.MemoryEventSink{}
	denied := &Authorizer{
		Policy:    StaticPolicy{Rules: map[string]Decision{"read_file": RequireApproval}},
		Approvals: &scriptedApprovals{},
		Sink:      deniedSink,
	}
	decision, err = denied.Authorize(context.Background(), requestFor("read_file", `{"path":"x"}`))
	if err != nil || decision != agentloop.ToolDecisionDeny {
		t.Fatal(decision, err)
	}
	for _, tc := range []struct {
		sink  *agentloop.MemoryEventSink
		kinds []agentloop.EventKind
	}{
		{sink, []agentloop.EventKind{agentloop.ApprovalRequested, agentloop.ApprovalGranted}},
		{deniedSink, []agentloop.EventKind{agentloop.ApprovalRequested, agentloop.ApprovalDenied}},
	} {
		events := tc.sink.Events()
		if len(events) != len(tc.kinds) {
			t.Fatal(events)
		}
		for i, kind := range tc.kinds {
			if events[i].Kind != kind || events[i].Step != 1 || events[i].ToolIndex != 1 {
				t.Fatal(events)
			}
			if text := fmt.Sprintf("%+v", events[i]); strings.Contains(text, secret) {
				t.Fatal(events[i])
			}
		}
	}
}

func TestTerminalApprovalAnswers(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"y\n", true}, {"Y\n", true}, {"yes\n", true}, {"Yes\n", true}, {" y\n", true},
		{"\n", false}, {"n\n", false}, {"N\n", false}, {"no\n", false}, {"maybe\n", false},
		{"yy\n", false}, {"y", false}, {"", false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var out bytes.Buffer
			approvals := NewTerminalApproval(strings.NewReader(tc.input), &out)
			got, err := approvals.Approve(context.Background(), requestFor("read_file", `{"path":"internal/agentloop/loop.go"}`))
			if err != nil || got != tc.want {
				t.Fatalf("got=%v err=%v", got, err)
			}
			for _, want := range []string{"Ferramenta: read_file", "Caminho: internal/agentloop/loop.go", "Permitir uma vez? [y/N]:"} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing %q in %q", want, out.String())
				}
			}
			if strings.Contains(out.String(), "file contents") {
				t.Fatal("prompt leaked file contents")
			}
		})
	}
}

func TestTerminalApprovalQueuedInputAcrossApprovals(t *testing.T) {
	var out bytes.Buffer
	approvals := NewTerminalApproval(strings.NewReader("y\nn\n"), &out)
	first, err := approvals.Approve(context.Background(), requestFor("list_dir", `{"path":"."}`))
	if err != nil || !first {
		t.Fatalf("first=%v err=%v", first, err)
	}
	second, err := approvals.Approve(context.Background(), requestFor("read_file", `{"path":"x"}`))
	if err != nil || second {
		t.Fatalf("second=%v err=%v", second, err)
	}
	if strings.Count(out.String(), "Permitir uma vez?") != 2 {
		t.Fatalf("prompts=%q", out.String())
	}
}

func TestTerminalApprovalSanitizesArguments(t *testing.T) {
	var out bytes.Buffer
	arguments := `{"path":"bad\u001b[31m\nline","other":123}`
	approvals := NewTerminalApproval(strings.NewReader("n\n"), &out)
	if _, err := approvals.Approve(context.Background(), requestFor("read_file", arguments)); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, forbidden := range []string{"\x1b", "\nline"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unsanitized output: %q", text)
		}
	}
	if !strings.Contains(text, "Caminho: bad?[31m?line") || !strings.Contains(text, "other: 123") {
		t.Fatalf("unexpected display: %q", text)
	}
}

func TestTerminalApprovalCapsLongValues(t *testing.T) {
	var out bytes.Buffer
	long := strings.Repeat("a", 500)
	approvals := NewTerminalApproval(strings.NewReader("n\n"), &out)
	if _, err := approvals.Approve(context.Background(), requestFor("read_file", `{"path":"`+long+`"}`)); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "a") > maxValueRunes+len("Caminho: ")+10 {
		t.Fatal("value not capped")
	}
}

func TestTerminalApprovalUnparseableArguments(t *testing.T) {
	var out bytes.Buffer
	approvals := NewTerminalApproval(strings.NewReader("n\n"), &out)
	if _, err := approvals.Approve(context.Background(), requestFor("read_file", `{not json`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Argumentos: {not json") {
		t.Fatalf("display=%q", out.String())
	}
}

func TestTerminalApprovalContextCancellation(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out bytes.Buffer
	approvals := NewTerminalApproval(pr, &out)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-time.After(30 * time.Millisecond)
		cancel()
	}()
	_, err := approvals.Approve(ctx, requestFor("read_file", "{}"))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestTerminalApprovalCanceledBeforePrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	approvals := NewTerminalApproval(strings.NewReader("y\n"), io.Discard)
	if _, err := approvals.Approve(ctx, requestFor("echo", "{}")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
