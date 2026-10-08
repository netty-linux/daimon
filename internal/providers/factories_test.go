package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers/groq"
	"github.com/netty-linux/daimon/internal/providers/openai"
)

func TestCompatibleFactoryEndpoints(t *testing.T) {
	for _, id := range []ID{OpenAI, "custom"} {
		t.Run(string(id), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected path")
				}
				var body struct {
					Model    string
					Messages []struct{ Role, Content string }
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != "fixture" || len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[0].Content != "guidance" {
					t.Errorf("configuration lost")
				}
				w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			var registry Registry
			if err := registry.Register(CompatibleFactory(id)); err != nil {
				t.Fatal(err)
			}
			f, err := registry.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			m, err := f.New(Config{BaseURL: server.URL + "/v1", Model: "fixture", SystemInstruction: "guidance", MaxResponseBytes: 4096})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 0 {
				t.Fatal("construction performed network I/O")
			}
			response, err := m.Generate(context.Background(), model.ModelRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}})
			if err != nil || response.FinalText != "done" || calls != 1 {
				t.Fatalf("generate: %v", err)
			}
		})
	}
}

type localTransport struct{ endpoint, selectedModel, instruction string }

func (tr *localTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.endpoint = req.URL.String()
	var body struct {
		Model    string
		Messages []struct{ Role, Content string }
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	tr.selectedModel = body.Model
	if len(body.Messages) > 0 && body.Messages[0].Role == "system" {
		tr.instruction = body.Messages[0].Content
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
}

func TestGroqFactoryPreservesDefaults(t *testing.T) {
	tr := &localTransport{}
	m, err := GroqFactory().New(Config{APIKey: "fixture-key", MaxResponseBytes: 4096, HTTPClient: &http.Client{Transport: tr}})
	if err != nil {
		t.Fatal(err)
	}
	if tr.endpoint != "" {
		t.Fatal("construction performed I/O")
	}
	// The empty offline response is intentionally invalid; inspect the translated request.
	_, err = m.Generate(context.Background(), model.ModelRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}})
	var jsonErr *openai.JSONError
	if !errors.As(err, &jsonErr) {
		t.Fatal(err)
	}
	if tr.endpoint != groq.BaseURL+"/chat/completions" || tr.selectedModel != groq.DefaultModel {
		t.Fatal("Groq defaults changed")
	}
}

func TestGroqFactoryForwardsInstructions(t *testing.T) {
	tr := &localTransport{}
	m, err := GroqFactory().New(Config{APIKey: "fixture-key", MaxResponseBytes: 4096, SystemInstruction: "private-guidance", HTTPClient: &http.Client{Transport: tr}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Generate(context.Background(), model.ModelRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}})
	var jsonErr *openai.JSONError
	if !errors.As(err, &jsonErr) || tr.instruction != "private-guidance" || tr.selectedModel != groq.DefaultModel {
		t.Fatal("Groq instruction/default lost", err)
	}
	_, err = GroqFactory().New(Config{APIKey: "fixture-key", MaxResponseBytes: 4096, SystemInstruction: strings.Repeat("x", 8193)})
	var cfgErr *openai.ConfigError
	if !errors.As(err, &cfgErr) || cfgErr.Field != "SystemInstruction" {
		t.Fatal("instruction limit not preserved", err)
	}
}

func TestFactoryConfigErrorsDoNotExposeValues(t *testing.T) {
	secret := "fixture-private-value"
	configs := []Config{
		{BaseURL: "http://remote.invalid/" + secret, Model: "fixture", APIKey: secret, MaxResponseBytes: 4096},
		{BaseURL: "https://example.invalid/v1", Model: "", APIKey: secret, MaxResponseBytes: 4096},
		{BaseURL: "https://example.invalid/v1", Model: "fixture", APIKey: secret + "\n", MaxResponseBytes: 4096},
		{BaseURL: "https://example.invalid/v1", Model: "fixture", APIKey: secret},
	}
	for _, cfg := range configs {
		_, err := CompatibleFactory(OpenAI).New(cfg)
		var typed *openai.ConfigError
		if !errors.As(err, &typed) || strings.Contains(err.Error(), secret) {
			t.Fatalf("unsafe/missing config error")
		}
	}
	for _, cfg := range []Config{{}, {APIKey: secret, BaseURL: secret}, {APIKey: secret, SystemInstruction: secret}} {
		_, err := GroqFactory().New(cfg)
		var typed *openai.ConfigError
		if !errors.As(err, &typed) || strings.Contains(err.Error(), secret) {
			t.Fatal("unsafe/missing Groq error")
		}
	}
}
