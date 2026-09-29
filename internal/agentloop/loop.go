package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
)

func (l Loop) Run(ctx context.Context, userMessage string) (result Result, err error) {
	result.History = []model.Message{{Role: model.RoleUser, Content: userMessage}}
	sink := l.Sink
	if sink == nil {
		sink = NoopEventSink{}
	}
	sink.Record(ctx, Event{Kind: LoopStarted})
	defer func() { sink.Record(ctx, Event{Kind: LoopStopped, Step: result.Steps}) }()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if l.Model == nil || l.Registry == nil || l.MaxSteps <= 0 {
		return result, ErrInvalidConfig
	}
	ids := make(map[string]bool)
	for result.Steps < l.MaxSteps {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		sink.Record(ctx, Event{Kind: ModelRequested, Step: result.Steps + 1})
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Steps++
		response, generateErr := l.Model.Generate(ctx, model.CloneRequest(model.ModelRequest{
			Messages: result.History, Tools: l.Registry.Descriptions(),
		}))
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if generateErr != nil {
			return result, &ModelError{Step: result.Steps, Cause: generateErr}
		}
		sink.Record(ctx, Event{Kind: ModelResponded, Step: result.Steps})
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := validate(response, ids); err != nil {
			return result, err
		}
		if len(response.ToolCalls) == 0 {
			result.FinalAnswer = response.FinalText
			result.History = append(result.History, model.Message{Role: model.RoleAssistant, Content: response.FinalText})
			sink.Record(ctx, Event{Kind: FinalAnswer, Step: result.Steps})
			return result, nil
		}
		calls := model.CloneCalls(response.ToolCalls)
		result.History = append(result.History, model.Message{Role: model.RoleAssistant, ToolCalls: model.CloneCalls(calls)})
		for i, call := range calls {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			sink.Record(ctx, Event{Kind: ToolRequested, Step: result.Steps, ToolIndex: i + 1})
			if err := ctx.Err(); err != nil {
				return result, err
			}
			toolResult := tools.ToolResult{}
			tool, exists := l.Registry.Find(call.Name)
			switch {
			case !exists:
				toolResult = tools.ToolResult{Content: "unknown tool", IsError: true}
			case !json.Valid(call.Arguments):
				toolResult = tools.ToolResult{Content: "invalid JSON arguments", IsError: true}
			default:
				var toolErr error
				toolResult, toolErr = tool.Execute(ctx, call.Arguments)
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				if errors.Is(toolErr, context.Canceled) || errors.Is(toolErr, context.DeadlineExceeded) {
					return result, toolErr
				}
				if toolErr != nil {
					toolResult = tools.ToolResult{Content: toolErr.Error(), IsError: true}
				}
			}
			result.History = append(result.History, model.Message{
				Role: model.RoleTool, Content: toolResult.Content, ToolCallID: call.ID, IsError: toolResult.IsError,
			})
			kind := ToolCompleted
			if toolResult.IsError {
				kind = ToolFailed
			}
			sink.Record(ctx, Event{Kind: kind, Step: result.Steps, ToolIndex: i + 1})
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, ErrMaxSteps
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
