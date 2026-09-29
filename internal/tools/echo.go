package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

type Echo struct{}

func (Echo) Name() string        { return "echo" }
func (Echo) Description() string { return "Return the supplied text." }
func (Echo) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)
}
func (Echo) Execute(ctx context.Context, arguments json.RawMessage) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	var args struct {
		Text *string `json:"text"`
	}
	if err := decodeObject(arguments, "text", &args); err != nil {
		return ToolResult{}, err
	}
	if args.Text == nil {
		return ToolResult{}, fmt.Errorf("%w: text is required", ErrInvalidArguments)
	}
	return ToolResult{Content: *args.Text}, nil
}
