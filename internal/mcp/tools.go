package mcp

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/tools"
	"strings"
	"unicode/utf8"
)

type Definition struct {
	Name, RemoteName, Description string
	Schema                        json.RawMessage
}

func (c *Client) discover(ctx context.Context, server string) ([]Definition, error) {
	found := []Definition{}
	names := map[string]bool{}
	cursors := map[string]bool{}
	cursor := ""
	for pages := 0; pages < MaxTools; pages++ {
		params := map[string]string{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		result, err := object(raw)
		if err != nil {
			return nil, err
		}
		var listed []json.RawMessage
		if json.Unmarshal(result["tools"], &listed) != nil || listed == nil {
			return nil, ErrProtocol
		}
		for _, rawTool := range listed {
			value, err := object(rawTool)
			if err != nil {
				return nil, err
			}
			var remote, description string
			if json.Unmarshal(value["name"], &remote) != nil {
				return nil, ErrProtocol
			}
			if raw, ok := value["description"]; ok {
				if string(raw) == "null" || json.Unmarshal(raw, &description) != nil {
					return nil, ErrProtocol
				}
			}
			name, err := Name(server, remote)
			if err != nil {
				return nil, ErrProtocol
			}
			if names[name] || len(found) >= MaxTools {
				return nil, ErrLimit
			}
			names[name] = true
			schema := value["inputSchema"]
			if len(schema) > MaxSchemaBytes || len(description) > 1024 {
				return nil, ErrLimit
			}
			fields, err := object(schema)
			if err != nil {
				return nil, ErrProtocol
			}
			if raw, ok := fields["type"]; ok {
				var kind string
				if json.Unmarshal(raw, &kind) != nil || kind != "object" {
					return nil, ErrUnsupported
				}
			}
			// Schema stays byte-for-byte owned. No schema interpreter or remote references.
			if raw, ok := value["outputSchema"]; ok {
				if len(raw) > MaxSchemaBytes {
					return nil, ErrLimit
				}
				if _, err := object(raw); err != nil {
					return nil, err
				}
			}
			if raw, ok := value["execution"]; ok {
				var execution struct {
					TaskSupport string `json:"taskSupport"`
				}
				if json.Unmarshal(raw, &execution) != nil {
					return nil, ErrProtocol
				}
				if execution.TaskSupport == "required" {
					return nil, ErrUnsupported
				}
			}
			found = append(found, Definition{name, remote, description, append(json.RawMessage(nil), schema...)})
		}
		next, ok := result["nextCursor"]
		if !ok {
			return found, nil
		}
		if json.Unmarshal(next, &cursor) != nil || cursor == "" || len(cursor) > 1024 || cursors[cursor] {
			return nil, ErrProtocol
		}
		cursors[cursor] = true
	}
	return nil, ErrLimit
}
func (c *Client) call(ctx context.Context, remote string, args json.RawMessage) (tools.ToolResult, error) {
	return c.callWithTextFilter(ctx, remote, args, nil)
}
func (c *Client) callWithTextFilter(ctx context.Context, remote string, args json.RawMessage, acceptText func(string) bool) (tools.ToolResult, error) {
	if len(args) > MaxArgumentBytes {
		return tools.ToolResult{}, ErrLimit
	}
	if strictJSON(args) != nil {
		return tools.ToolResult{}, ErrArguments
	}
	if _, err := object(args); err != nil {
		return tools.ToolResult{}, ErrArguments
	}
	callCtx, end := context.WithTimeout(ctx, c.options.CallTimeout)
	defer end()
	raw, err := c.request(callCtx, "tools/call", struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}{remote, append(json.RawMessage(nil), args...)})
	if err != nil {
		return tools.ToolResult{}, err
	}
	result, err := object(raw)
	if err != nil {
		c.fail(ErrProtocol)
		return tools.ToolResult{}, err
	}
	var content []json.RawMessage
	if json.Unmarshal(result["content"], &content) != nil || content == nil {
		c.fail(ErrProtocol)
		return tools.ToolResult{}, ErrProtocol
	}
	isError := false
	if raw, ok := result["isError"]; ok {
		if string(raw) == "null" || json.Unmarshal(raw, &isError) != nil {
			c.fail(ErrProtocol)
			return tools.ToolResult{}, ErrProtocol
		}
	}
	// Never pass the server's free-form error text into the loop's public error path.
	if isError {
		return tools.ToolResult{Content: "mcp: tool reported failure", IsError: true}, nil
	}
	var output strings.Builder
	for i, raw := range content {
		value, err := object(raw)
		if err != nil {
			c.fail(ErrProtocol)
			return tools.ToolResult{}, ErrProtocol
		}
		var kind, text string
		if json.Unmarshal(value["type"], &kind) != nil {
			c.fail(ErrProtocol)
			return tools.ToolResult{}, ErrProtocol
		}
		if kind != "text" {
			return tools.ToolResult{}, ErrUnsupported
		}
		if string(value["text"]) == "null" || json.Unmarshal(value["text"], &text) != nil || !utf8.ValidString(text) {
			c.fail(ErrProtocol)
			return tools.ToolResult{}, ErrProtocol
		}
		if acceptText != nil && !acceptText(text) {
			return tools.ToolResult{}, ErrUnsupported
		}
		separator := 0
		if i > 0 {
			separator = 1
		}
		if len(text) > MaxResultBytes-output.Len()-separator {
			return tools.ToolResult{}, ErrLimit
		}
		if i > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(text)
	}
	return tools.ToolResult{Content: output.String()}, nil
}

type Tool struct {
	client         *Client
	definition     Definition
	classification Classification
}

func (t *Tool) Name() string        { return t.definition.Name }
func (t *Tool) Description() string { return t.definition.Description }
func (t *Tool) InputSchema() json.RawMessage {
	return append(json.RawMessage(nil), t.definition.Schema...)
}
func (t *Tool) Classification() Classification { return t.classification }
func (t *Tool) ServerID() string               { parts := strings.SplitN(t.Name(), "__", 3); return parts[1] }
func (t *Tool) Execute(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	if t.classification != Read {
		return tools.ToolResult{}, ErrDenied
	}
	return t.client.call(ctx, t.definition.RemoteName, args)
}
