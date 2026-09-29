package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/providers/openai"
)

func chatEnvironment(base, model, key string) func(string) string {
	return func(name string) string {
		switch name {
		case "DAIMON_BASE_URL":
			return base
		case "DAIMON_MODEL":
			return model
		case "DAIMON_API_KEY":
			return key
		default:
			return ""
		}
	}
}
func TestChatUsageAndConfig(t *testing.T) {
	for _, args := range [][]string{{"chat"}, {"chat", ""}, {"chat", " "}, {"chat", "a", "extra"}, {"chat", "a", "--api-key", "secret"}} {
		var out bytes.Buffer
		err := runWithContext(context.Background(), args, &out, func(string) string { t.Error("environment read for invalid usage"); return "" })
		if err == nil || !strings.Contains(err.Error(), "usage:") || strings.Contains(err.Error(), "secret") || out.Len() != 0 {
			t.Fatal(err, &out)
		}
	}
	for _, env := range []func(string) string{chatEnvironment("", "m", "secret"), chatEnvironment("http://localhost/v1", "", "secret")} {
		err := runWithContext(context.Background(), []string{"chat", "hello"}, io.Discard, env)
		var config *openai.ConfigError
		if !errors.As(err, &config) || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	}
}
func TestDemoDoesNotReadEnvironment(t *testing.T) {
	var out bytes.Buffer
	err := runWithContext(context.Background(), []string{"demo"}, &out, func(string) string { t.Fatal("demo read environment"); return "" })
	if err != nil || !strings.Contains(out.String(), "Resposta final: DAIMON") || !strings.Contains(out.String(), "loop_stopped") {
		t.Fatal(err, &out)
	}
}
func TestChatToolCycle(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("fixture.txt", []byte("public fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if bytes.Contains(body, []byte("public fixture")) {
			t.Error("file content sent to server")
		}
		var request struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if len(request.Tools) != 2 || request.Tools[0].Function.Name != "echo" || request.Tools[1].Function.Name != "read_file" {
			t.Error("tools not registered")
		}
		if calls.Add(1) == 1 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"echo-1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"DAIMON\"}"}}]}}]}`)
			return
		}
		if len(request.Messages) != 3 || request.Messages[2].Role != "tool" || request.Messages[2].ToolCallID != "echo-1" || request.Messages[2].Content != "DAIMON" {
			t.Errorf("history incorrect: %+v", request.Messages)
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"DAIMON"}}]}`)
	}))
	defer s.Close()
	var out bytes.Buffer
	err := runWithContext(context.Background(), []string{"chat", "use tools"}, &out, chatEnvironment(s.URL+"/v1", "test-model", ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Resposta final: DAIMON", "Passos do modelo: 2", "Tool calls: 1", "Resultados truncados: 0", "Stop reason: completed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, &out)
		}
	}
	if strings.Contains(out.String(), "public fixture") || strings.Contains(out.String(), "use tools") || strings.Contains(out.String(), "Eventos:") || calls.Load() != 2 {
		t.Fatal("unexpected output or requests", &out)
	}
}
func TestChatDeniesReadFileByDefault(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("secret.txt", []byte("internal secret"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if bytes.Contains(body, []byte("internal secret")) {
			t.Error("file content sent to server")
		}
		var request struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if calls.Add(1) == 1 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"read-1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"secret.txt\"}"}}]}}]}`)
			return
		}
		if len(request.Messages) != 3 || request.Messages[2].Role != "tool" || request.Messages[2].ToolCallID != "read-1" || request.Messages[2].Content != "tool denied" {
			t.Errorf("denial receipt missing: %+v", request.Messages)
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"bloqueado"}}]}`)
	}))
	defer s.Close()
	var out bytes.Buffer
	err := runWithContext(context.Background(), []string{"chat", "read it"}, &out, chatEnvironment(s.URL+"/v1", "test-model", ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Resposta final: bloqueado", "Passos do modelo: 2", "Tool calls: 1", "Resultados truncados: 0", "Stop reason: completed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, &out)
		}
	}
	if strings.Contains(out.String(), "internal secret") || calls.Load() != 2 {
		t.Fatal("unexpected output or requests", &out)
	}
}
func TestChatRedactsErrorsAndAnswers(t *testing.T) {
	const key = "sk-private-cli-test"
	for _, status := range []int{200, 401} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+key {
				t.Error("missing auth")
			}
			w.WriteHeader(status)
			if status == 200 {
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": key}}}})
			} else {
				io.WriteString(w, key)
			}
		}))
		var out bytes.Buffer
		err := runWithContext(context.Background(), []string{"chat", "hello"}, &out, chatEnvironment(s.URL, "m", key))
		s.Close()
		if strings.Contains(out.String(), key) || (err != nil && strings.Contains(err.Error(), key)) {
			t.Fatal("secret exposed")
		}
		if status == 200 {
			if err != nil || !strings.Contains(out.String(), "[REDACTED]") {
				t.Fatal(err, &out)
			}
		} else {
			var httpErr *openai.HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != 401 || out.Len() != 0 {
				t.Fatal(err, &out)
			}
		}
	}
}
func TestChatUsesCallerContext(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		cancel()
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
			t.Error("caller cancellation not propagated")
		}
		close(done)
	}))
	defer s.Close()
	defer cancel()
	err := runWithContext(ctx, []string{"chat", "hello"}, io.Discard, chatEnvironment(s.URL, "m", ""))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish")
	}
	// The same canceled context must not start a second request.
	err = runWithContext(ctx, []string{"chat", "hello"}, io.Discard, chatEnvironment(s.URL, "m", ""))
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
}
