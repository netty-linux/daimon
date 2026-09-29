// Package model defines the provider-independent conversation protocol.
package model

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
	IsError    bool
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ToolDescription struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type ModelRequest struct {
	Messages []Message
	Tools    []ToolDescription
}

// ModelResponse must contain either nonblank FinalText or nonempty ToolCalls.
type ModelResponse struct {
	FinalText string
	ToolCalls []ToolCall
}

// Model implementations must honor ctx. Calls are synchronous.
type Model interface {
	Generate(ctx context.Context, req ModelRequest) (ModelResponse, error)
}

// CloneRequest prevents a model from changing the loop's owned history.
func CloneRequest(req ModelRequest) ModelRequest {
	copyReq := ModelRequest{
		Messages: append([]Message(nil), req.Messages...),
		Tools:    append([]ToolDescription(nil), req.Tools...),
	}
	for i := range copyReq.Messages {
		copyReq.Messages[i].ToolCalls = CloneCalls(req.Messages[i].ToolCalls)
	}
	for i := range copyReq.Tools {
		copyReq.Tools[i].InputSchema = append(json.RawMessage(nil), req.Tools[i].InputSchema...)
	}
	return copyReq
}

func CloneCalls(calls []ToolCall) []ToolCall {
	result := append([]ToolCall(nil), calls...)
	for i := range result {
		result[i].Arguments = append(json.RawMessage(nil), calls[i].Arguments...)
	}
	return result
}
