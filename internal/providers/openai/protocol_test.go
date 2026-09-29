package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
)

func TestMessageAndToolTranslationIsImmutable(t *testing.T) {
	input := model.ModelRequest{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "olá"},
			{Role: model.RoleAssistant, Content: "previous answer"},
			{Role: model.RoleUser, Content: "use tools"},
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "a", Name: "echo", Arguments: []byte(" {\"text\": \"é\"} \n")},
				{ID: "b", Name: "other", Arguments: []byte("{")},
			}},
			{Role: model.RoleTool, ToolCallID: "a", Content: "é"},
			{Role: model.RoleTool, ToolCallID: "b", Content: "invalid JSON arguments", IsError: true},
		},
		Tools: []model.ToolDescription{
			{Name: "echo", Description: "  keep this description  ", InputSchema: []byte(`{"type":"object","properties":{"text":{"type":"string"}},"additionalProperties":false}`)},
			{Name: "other", Description: "other", InputSchema: []byte(`{"type":"object"}`)},
		},
	}
	snapshot := model.CloneRequest(input)
	received := make(chan []byte, 2)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		received <- raw
		io.WriteString(w, textResponse)
	}))
	defer s.Close()
	p := newProvider(t, s, "", 4096)
	for range 2 {
		if _, err := p.Generate(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(input, snapshot) {
			t.Fatal("input mutated")
		}
		var got struct {
			Messages []struct {
				Role       string  `json:"role"`
				Content    *string `json:"content"`
				ToolCallID string  `json:"tool_call_id"`
				ToolCalls  []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
			Tools []struct {
				Type     string `json:"type"`
				Function struct {
					Name        string          `json:"name"`
					Description string          `json:"description"`
					Parameters  json.RawMessage `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		raw := <-received
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Messages) != 6 || len(got.Tools) != 2 || strings.Contains(string(raw), "IsError") || strings.Contains(string(raw), "is_error") {
			t.Fatal(string(raw))
		}
		for i, msg := range got.Messages {
			want := input.Messages[i]
			if msg.Role != string(want.Role) || msg.ToolCallID != want.ToolCallID {
				t.Fatal("message order/correlation changed")
			}
			if i == 3 {
				if msg.Content != nil {
					t.Fatal("tool call assistant must omit content")
				}
			} else if msg.Content == nil || *msg.Content != want.Content {
				t.Fatal("content changed")
			}
		}
		for i, call := range got.Messages[3].ToolCalls {
			want := input.Messages[3].ToolCalls[i]
			if call.ID != want.ID || call.Type != "function" || call.Function.Name != want.Name || call.Function.Arguments != string(want.Arguments) {
				t.Fatal("tool call changed")
			}
		}
		for i, tool := range got.Tools {
			want := input.Tools[i]
			if tool.Type != "function" || tool.Function.Name != want.Name || tool.Function.Description != want.Description {
				t.Fatal("tool description changed")
			}
			var gotSchema, wantSchema any
			json.Unmarshal(tool.Function.Parameters, &gotSchema)
			json.Unmarshal(want.InputSchema, &wantSchema)
			if !reflect.DeepEqual(gotSchema, wantSchema) {
				t.Fatal("schema changed")
			}
		}
	}
	// A later request has no retained messages/tools from earlier calls.
	if _, err := p.Generate(context.Background(), userRequest()); err != nil {
		t.Fatal(err)
	}
	var next map[string]json.RawMessage
	json.Unmarshal(<-received, &next)
	if _, ok := next["tools"]; ok {
		t.Fatal("tools leaked across calls")
	}
}

func TestInvalidRequestNeverSendsHTTP(t *testing.T) {
	validCall := model.ToolCall{ID: "id", Name: "echo", Arguments: []byte(`{}`)}
	for _, tc := range []struct {
		name    string
		request model.ModelRequest
	}{
		{"no messages", model.ModelRequest{}},
		{"unknown role", model.ModelRequest{Messages: []model.Message{{Role: "system", Content: "x"}}}},
		{"missing result ID", model.ModelRequest{Messages: []model.Message{{Role: model.RoleTool, Content: "x"}}}},
		{"user calls", model.ModelRequest{Messages: []model.Message{{Role: model.RoleUser, ToolCalls: []model.ToolCall{validCall}}}}},
		{"assistant ambiguous", model.ModelRequest{Messages: []model.Message{{Role: model.RoleAssistant, Content: "x", ToolCalls: []model.ToolCall{validCall}}}}},
		{"assistant empty", model.ModelRequest{Messages: []model.Message{{Role: model.RoleAssistant}}}},
		{"assistant whitespace", model.ModelRequest{Messages: []model.Message{{Role: model.RoleAssistant, Content: " "}}}},
		{"tool calls", model.ModelRequest{Messages: []model.Message{{Role: model.RoleTool, ToolCallID: "id", ToolCalls: []model.ToolCall{validCall}}}}},
		{"missing arguments", model.ModelRequest{Messages: []model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "id", Name: "echo"}}}}}},
		{"missing call ID", model.ModelRequest{Messages: []model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{Name: "echo", Arguments: []byte(`{}`)}}}}}},
		{"missing call name", model.ModelRequest{Messages: []model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "id", Arguments: []byte(`{}`)}}}}}},
		{"unexpected correlation", model.ModelRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "x", ToolCallID: "id"}}}},
		{"unexpected error flag", model.ModelRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "x", IsError: true}}}},
		{"bad utf8", model.ModelRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "\xff"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertRejectedRequest(t, tc.request) })
	}
	for _, schema := range []string{"", `{`, "null", "true", "[]", "{} {}"} {
		req := userRequest()
		req.Tools = []model.ToolDescription{{Name: "echo", InputSchema: []byte(schema)}}
		t.Run("schema="+schema, func(t *testing.T) { assertRejectedRequest(t, req) })
	}
	req := userRequest()
	req.Tools = []model.ToolDescription{{Name: " ", InputSchema: []byte(`{}`)}}
	assertRejectedRequest(t, req)
}
func assertRejectedRequest(t *testing.T, req model.ModelRequest) {
	t.Helper()
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); io.WriteString(w, textResponse) }))
	defer s.Close()
	p := newProvider(t, s, "", 4096)
	_, err := p.Generate(context.Background(), req)
	var requestErr *RequestError
	if !errors.As(err, &requestErr) || calls.Load() != 0 {
		t.Fatalf("%T %v requests=%d", err, err, calls.Load())
	}
}

func messageResponse(message string) string {
	return `{"choices":[{"message":` + message + `,"finish_reason":"stop"}],"unknown_compatible_field":true}`
}
func TestResponseProtocol(t *testing.T) {
	call := `{"id":"a","type":"function","function":{"name":"echo","arguments":" {\"x\": 1} "}}`
	// The second argument below is deliberately malformed JSON; preserve it too.
	multiple := `{"role":"assistant","content":null,"tool_calls":[` + call + `,{"id":"b","type":"function","function":{"name":"other","arguments":"{"}}]}`
	for _, tc := range []struct {
		name, body, text string
		calls            int
		category         string
	}{
		{"text", textResponse, "DAIMON", 0, ""},
		{"unknown fields", messageResponse(`{"role":"assistant","content":"ok","extra":12}`), "ok", 0, ""},
		{"one call", messageResponse(`{"role":"assistant","tool_calls":[` + call + `]}`), "", 1, ""},
		{"multiple calls null content", messageResponse(multiple), "", 2, ""},
		{"empty string arguments", messageResponse(`{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"echo","arguments":""}}]}`), "", 1, ""},
		{"empty body", "", "", 0, "json"},
		{"whitespace body", " \n", "", 0, "json"},
		{"malformed", "{", "", 0, "json"},
		{"extra JSON", textResponse + ` {}`, "", 0, "json"},
		{"no choices", `{}`, "", 0, "protocol"},
		{"empty choices", `{"choices":[]}`, "", 0, "protocol"},
		{"null choices", `{"choices":null}`, "", 0, "protocol"},
		{"missing message", `{"choices":[{}]}`, "", 0, "protocol"},
		{"null message", messageResponse(`null`), "", 0, "protocol"},
		{"role", messageResponse(`{"role":"user","content":"x"}`), "", 0, "protocol"},
		{"content array", messageResponse(`{"role":"assistant","content":[{"type":"text","text":"x"}]}`), "", 0, "protocol"},
		{"content number", messageResponse(`{"role":"assistant","content":4}`), "", 0, "protocol"},
		{"empty response", messageResponse(`{"role":"assistant","content":""}`), "", 0, "protocol"},
		{"blank response", messageResponse(`{"role":"assistant","content":" "}`), "", 0, "protocol"},
		{"null response", messageResponse(`{"role":"assistant","content":null}`), "", 0, "protocol"},
		{"ambiguous", messageResponse(`{"role":"assistant","content":"text","tool_calls":[` + call + `]}`), "", 0, "protocol"},
		{"no ID", messageResponse(`{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"echo","arguments":"{}"}}]}`), "", 0, "protocol"},
		{"no type", messageResponse(`{"role":"assistant","tool_calls":[{"id":"a","function":{"name":"echo","arguments":"{}"}}]}`), "", 0, "protocol"},
		{"custom type", messageResponse(`{"role":"assistant","tool_calls":[{"id":"a","type":"custom","function":{"name":"echo","arguments":"{}"}}]}`), "", 0, "protocol"},
		{"no function", messageResponse(`{"role":"assistant","tool_calls":[{"id":"a","type":"function"}]}`), "", 0, "protocol"},
		{"no name", messageResponse(`{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"arguments":"{}"}}]}`), "", 0, "protocol"},
		{"no arguments", messageResponse(`{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"echo"}}]}`), "", 0, "protocol"},
		{"null arguments", messageResponse(`{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"echo","arguments":null}}]}`), "", 0, "protocol"},
		{"object arguments", messageResponse(`{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"echo","arguments":{}}}]}`), "", 0, "protocol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, tc.body) }))
			defer s.Close()
			p := newProvider(t, s, "", 4096)
			got, err := p.Generate(context.Background(), userRequest())
			var invalidJSON *JSONError
			var protocol *ProtocolError
			switch tc.category {
			case "json":
				if !errors.As(err, &invalidJSON) {
					t.Fatalf("%T %v", err, err)
				}
			case "protocol":
				if !errors.As(err, &protocol) {
					t.Fatalf("%T %v", err, err)
				}
			default:
				if err != nil || got.FinalText != tc.text || len(got.ToolCalls) != tc.calls {
					t.Fatal(got, err)
				}
				if tc.calls == 2 {
					if string(got.ToolCalls[0].Arguments) != ` {"x": 1} ` {
						t.Fatal("argument bytes changed")
					}
					if got.ToolCalls[0].ID != "a" || got.ToolCalls[1].ID != "b" || got.ToolCalls[0].Name != "echo" || got.ToolCalls[1].Name != "other" || string(got.ToolCalls[1].Arguments) != "{" {
						t.Fatal(got)
					}
				}
			}
		})
	}
}

func TestLoopCanRecoverFromMalformedArguments(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []chatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if calls.Add(1) == 1 {
			io.WriteString(w, messageResponse(`{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"echo","arguments":"{"}}]}`))
			return
		}
		if len(payload.Messages) != 3 || payload.Messages[2].ToolCallID != "a" || payload.Messages[2].Content == nil || *payload.Messages[2].Content != "invalid JSON arguments" || *payload.Messages[1].ToolCalls[0].Function.Arguments != "{" {
			t.Error("recovery history not preserved")
		}
		io.WriteString(w, textResponse)
	}))
	defer s.Close()
	p := newProvider(t, s, "", 4096)
	registry := &tools.Registry{}
	if err := registry.Register(tools.Echo{}); err != nil {
		t.Fatal(err)
	}
	loop := agentloop.Loop{Model: p, Registry: registry, Budget: agentloop.DefaultBudget(), Authorizer: agentloop.AllowAllAuthorizer{}}
	got, err := loop.Run(context.Background(), "hello")
	if err != nil || got.FinalAnswer != "DAIMON" || got.Steps != 2 || calls.Load() != 2 {
		t.Fatal(got, err)
	}
}

func TestLoopBudgetGovernsHTTP(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer s.Close()
	p := newProvider(t, s, "", 4096)
	budget := agentloop.DefaultBudget()
	budget.MaxModelCallDuration = 50 * time.Millisecond
	loop := agentloop.Loop{Model: p, Registry: &tools.Registry{}, Budget: budget, Authorizer: agentloop.AllowAllAuthorizer{}}
	result, err := loop.Run(context.Background(), "hello")
	if !errors.Is(err, context.DeadlineExceeded) || result.StopReason != agentloop.StopReasonModelTimeout {
		t.Fatalf("%+v %v", result, err)
	}
}
