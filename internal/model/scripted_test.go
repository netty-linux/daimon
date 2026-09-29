package model

import (
	"context"
	"errors"
	"testing"
)

func TestScriptedSequenceErrorAndExhaustion(t *testing.T) {
	cause := errors.New("failure on call two")
	s := NewScripted(ScriptStep{Response: ModelResponse{FinalText: "one"}}, ScriptStep{Err: cause})
	r, err := s.Generate(context.Background(), ModelRequest{})
	if err != nil || r.FinalText != "one" {
		t.Fatal(r, err)
	}
	if _, err := s.Generate(context.Background(), ModelRequest{}); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if _, err := s.Generate(context.Background(), ModelRequest{}); !errors.Is(err, ErrScriptExhausted) {
		t.Fatal(err)
	}
	if len(s.Requests()) != 3 {
		t.Fatal("requests not recorded")
	}
}

func TestScriptedCopiesAndCancellation(t *testing.T) {
	req := ModelRequest{Messages: []Message{{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "a", Arguments: []byte(`{}`)}}}}, Tools: []ToolDescription{{InputSchema: []byte(`{}`)}}}
	step := ScriptStep{Response: ModelResponse{ToolCalls: []ToolCall{{ID: "a", Arguments: []byte(`{}`)}}}}
	s := NewScripted(step)
	step.Response.ToolCalls[0].Arguments[0] = 'x'
	r, err := s.Generate(context.Background(), req)
	if err != nil || string(r.ToolCalls[0].Arguments) != `{}` {
		t.Fatal(r, err)
	}
	req.Messages[0].ToolCalls[0].Arguments[0] = 'x'
	req.Tools[0].InputSchema[0] = 'x'
	first := s.Requests()
	if string(first[0].Messages[0].ToolCalls[0].Arguments) != `{}` || string(first[0].Tools[0].InputSchema) != `{}` {
		t.Fatal("aliased request")
	}
	first[0].Messages[0].ToolCalls[0].Arguments[0] = 'y'
	if string(s.Requests()[0].Messages[0].ToolCalls[0].Arguments) != `{}` {
		t.Fatal("aliased inspection")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Generate(ctx, ModelRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(s.Requests()) != 1 {
		t.Fatal("canceled call recorded")
	}
}
