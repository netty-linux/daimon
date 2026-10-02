package groq

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers/openai"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGroqDefaultsAndToolCycle(t *testing.T) {
	for _, override := range []string{"", "explicit-model"} {
		t.Run(override, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/openai/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer fake-key" {
					t.Error("incorrect endpoint or auth")
				}
				var body struct {
					Model    string
					Stream   bool
					Messages []json.RawMessage
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				want := override
				if want == "" {
					want = DefaultModel
				}
				if body.Model != want || body.Stream {
					t.Error("incorrect model or streaming")
				}
				if calls == 1 {
					w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"DAIMON\"}"}}]}}]}`))
				} else {
					if len(body.Messages) != 3 || !strings.Contains(string(body.Messages[2]), "c1") {
						t.Error("missing receipt")
					}
					w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"DAIMON"}}]}`))
				}
			}))
			defer server.Close()
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "api.groq.com" {
					t.Error("incorrect Groq destination")
				}
				copy := r.Clone(r.Context())
				copy.URL.Scheme = "http"
				copy.URL.Host = strings.TrimPrefix(server.URL, "http://")
				return server.Client().Transport.RoundTrip(copy)
			})}
			p, err := New(Config{APIKey: "fake-key", Model: override, MaxResponseBytes: 4096, HTTPClient: client})
			if err != nil {
				t.Fatal(err)
			}
			req := model.ModelRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "echo DAIMON"}}}
			response, err := p.Generate(context.Background(), req)
			if err != nil || len(response.ToolCalls) != 1 {
				t.Fatalf("response=%+v err=%v", response, err)
			}
			req.Messages = append(req.Messages, model.Message{Role: model.RoleAssistant, ToolCalls: response.ToolCalls}, model.Message{Role: model.RoleTool, ToolCallID: "c1", Content: "DAIMON"})
			response, err = p.Generate(context.Background(), req)
			if err != nil || response.FinalText != "DAIMON" || calls != 2 {
				t.Fatalf("response=%+v err=%v", response, err)
			}
		})
	}
}

func TestMissingKeyFailsClosed(t *testing.T) {
	_, err := New(Config{MaxResponseBytes: 4096})
	var config *openai.ConfigError
	if !errors.As(err, &config) {
		t.Fatal(err)
	}
}
