package openai

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/model"
)

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
}
type chatMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []chatCall `json:"tool_calls,omitempty"`
}
type chatCall struct {
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Function *chatFunction `json:"function"`
}
type chatFunction struct {
	Name      string  `json:"name"`
	Arguments *string `json:"arguments"`
}
type chatTool struct {
	Type     string         `json:"type"`
	Function chatDefinition `json:"function"`
}
type chatDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
type chatResponse struct {
	Choices []struct {
		Message *chatMessage `json:"message"`
	} `json:"choices"`
}

func encodeRequest(modelName string, req model.ModelRequest) ([]byte, error) {
	return encodeRequestWithSystem(modelName, req, "")
}

func encodeRequestWithSystem(modelName string, req model.ModelRequest, instruction string) ([]byte, error) {
	return encodeRequestWithContext(modelName, req, instruction, "")
}
func encodeRequestWithContext(modelName string, req model.ModelRequest, instruction, contextual string) ([]byte, error) {
	if len(req.Messages) == 0 {
		return nil, &RequestError{Reason: "messages are required"}
	}
	wire := chatRequest{Model: modelName, Messages: make([]chatMessage, 0, len(req.Messages)), Stream: false}
	if instruction != "" {
		wire.Messages = append(wire.Messages, chatMessage{Role: "system", Content: &instruction})
	}
	if contextual != "" {
		wire.Messages = append(wire.Messages, chatMessage{Role: "system", Content: &contextual})
	}
	for _, msg := range req.Messages {
		if !utf8.ValidString(msg.Content) || !utf8.ValidString(msg.ToolCallID) {
			return nil, &RequestError{Reason: "invalid UTF-8 in message"}
		}
		if msg.Role != model.RoleTool && (msg.ToolCallID != "" || msg.IsError) {
			return nil, &RequestError{Reason: "tool metadata on a non-tool message"}
		}
		content := msg.Content
		out := chatMessage{Role: string(msg.Role), Content: &content}
		switch msg.Role {
		case model.RoleUser:
			if len(msg.ToolCalls) != 0 {
				return nil, &RequestError{Reason: "user message has tool calls"}
			}
		case model.RoleAssistant:
			if len(msg.ToolCalls) == 0 {
				if strings.TrimSpace(msg.Content) == "" {
					return nil, &RequestError{Reason: "empty assistant message"}
				}
			} else {
				if msg.Content != "" {
					return nil, &RequestError{Reason: "ambiguous assistant message"}
				}
				out.Content = nil
				for _, call := range msg.ToolCalls {
					if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || call.Arguments == nil {
						return nil, &RequestError{Reason: "incomplete tool call"}
					}
					if !utf8.ValidString(call.ID) || !utf8.ValidString(call.Name) || !utf8.Valid(call.Arguments) {
						return nil, &RequestError{Reason: "invalid UTF-8 in tool call"}
					}
					// Do not parse arguments: malformed JSON must survive a recovery turn.
					args := string(call.Arguments)
					out.ToolCalls = append(out.ToolCalls, chatCall{ID: call.ID, Type: "function", Function: &chatFunction{Name: call.Name, Arguments: &args}})
				}
			}
		case model.RoleTool:
			if strings.TrimSpace(msg.ToolCallID) == "" || len(msg.ToolCalls) != 0 {
				return nil, &RequestError{Reason: "invalid tool result"}
			}
			out.ToolCallID = msg.ToolCallID
		default:
			return nil, &RequestError{Reason: "unsupported role"}
		}
		wire.Messages = append(wire.Messages, out)
	}
	for _, tool := range req.Tools {
		schema := bytes.TrimSpace(tool.InputSchema)
		if strings.TrimSpace(tool.Name) == "" || !utf8.ValidString(tool.Name) || !utf8.ValidString(tool.Description) {
			return nil, &RequestError{Reason: "invalid tool description"}
		}
		if !json.Valid(schema) || !utf8.Valid(schema) || schema[0] != '{' {
			return nil, &RequestError{Reason: "tool schema must be a JSON object"}
		}
		wire.Tools = append(wire.Tools, chatTool{Type: "function", Function: chatDefinition{Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema}})
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, &RequestError{Reason: "JSON encoding failed"}
	}
	return body, nil
}

func decodeResponse(body []byte) (model.ModelResponse, error) {
	if !json.Valid(body) || !utf8.Valid(body) {
		return model.ModelResponse{}, &JSONError{}
	}
	var wire chatResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return model.ModelResponse{}, &ProtocolError{Reason: "unsupported field types"}
	}
	if len(wire.Choices) == 0 || wire.Choices[0].Message == nil {
		return model.ModelResponse{}, &ProtocolError{Reason: "missing choice or message"}
	}
	// n is not requested; if a compatible service returns more choices, use the first.
	msg := wire.Choices[0].Message
	if msg.Role != "assistant" {
		return model.ModelResponse{}, &ProtocolError{Reason: "message role must be assistant"}
	}
	text := ""
	if msg.Content != nil {
		text = *msg.Content
	}
	if len(msg.ToolCalls) == 0 {
		if strings.TrimSpace(text) == "" {
			return model.ModelResponse{}, &ProtocolError{Reason: "empty response"}
		}
		return model.ModelResponse{FinalText: text}, nil
	}
	if text != "" {
		return model.ModelResponse{}, &ProtocolError{Reason: "ambiguous response"}
	}
	response := model.ModelResponse{}
	for _, call := range msg.ToolCalls {
		if strings.TrimSpace(call.ID) == "" || call.Type != "function" || call.Function == nil ||
			strings.TrimSpace(call.Function.Name) == "" || call.Function.Arguments == nil {
			return model.ModelResponse{}, &ProtocolError{Reason: "incomplete or unsupported tool call"}
		}
		response.ToolCalls = append(response.ToolCalls, model.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(*call.Function.Arguments)})
	}
	return response, nil
}
