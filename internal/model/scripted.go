package model

import (
	"context"
	"errors"
)

var ErrScriptExhausted = errors.New("scripted model: no responses remaining")

// ScriptStep can return a response or inject an error at a specific call.
type ScriptStep struct {
	Response ModelResponse
	Err      error
}

// Scripted is deliberately sequential, with no concurrency support.
type Scripted struct {
	script   []ScriptStep
	requests []ModelRequest
}

func NewScripted(script ...ScriptStep) *Scripted {
	copyScript := append([]ScriptStep(nil), script...)
	for i := range copyScript {
		copyScript[i].Response.ToolCalls = CloneCalls(script[i].Response.ToolCalls)
	}
	return &Scripted{script: copyScript}
}

func (s *Scripted) Generate(ctx context.Context, req ModelRequest) (ModelResponse, error) {
	if err := ctx.Err(); err != nil {
		return ModelResponse{}, err
	}
	i := len(s.requests)
	s.requests = append(s.requests, CloneRequest(req))
	if i >= len(s.script) {
		return ModelResponse{}, ErrScriptExhausted
	}
	step := s.script[i]
	step.Response.ToolCalls = CloneCalls(step.Response.ToolCalls)
	return step.Response, step.Err
}

func (s *Scripted) Requests() []ModelRequest {
	requests := make([]ModelRequest, len(s.requests))
	for i, req := range s.requests {
		requests[i] = CloneRequest(req)
	}
	return requests
}
