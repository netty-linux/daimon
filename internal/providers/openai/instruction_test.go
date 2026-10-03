package openai

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSystemInstructionSeparateImmutableAndRepeated(t *testing.T) {
	input := model.ModelRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "original user"}}}
	before := model.CloneRequest(input)
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		var wire chatRequest
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		if len(wire.Messages) != 2 || wire.Messages[0].Role != "system" || wire.Messages[0].Content == nil || *wire.Messages[0].Content != "fixed instruction" || wire.Messages[1].Role != "user" || *wire.Messages[1].Content != "original user" {
			t.Error("instruction composition")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"}}]}`))
	}))
	defer server.Close()
	p, err := New(Config{BaseURL: server.URL, Model: "test", MaxResponseBytes: 4096, SystemInstruction: "fixed instruction"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := p.Generate(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	if count != 2 || !reflect.DeepEqual(before, input) {
		t.Fatal("mutated input or request count")
	}
	// Adapter guidance is not inserted into loop history or typed events.
	sink := &agentloop.MemoryEventSink{}
	result, err := (agentloop.Loop{Model: p, Registry: &tools.Registry{}, Budget: agentloop.DefaultBudget(), Authorizer: agentloop.AllowAllAuthorizer{}, Sink: sink}).Run(context.Background(), "original user")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{result.History, sink.Events()} {
		encoded, err := json.Marshal(value)
		if err != nil || strings.Contains(string(encoded), "fixed instruction") {
			t.Fatal("guidance leaked into loop", err)
		}
	}
}

func TestSystemInstructionBoundedConfiguration(t *testing.T) {
	for _, text := range []string{strings.Repeat("x", 8193), string([]byte{0xff})} {
		_, err := New(Config{BaseURL: "http://localhost", Model: "test", MaxResponseBytes: 4096, SystemInstruction: text})
		var cfg *ConfigError
		if !errors.As(err, &cfg) || cfg.Field != "SystemInstruction" || strings.Contains(err.Error(), text) {
			t.Fatal("unsafe config error", err)
		}
	}
	if _, err := New(Config{BaseURL: "http://localhost", Model: "test", MaxResponseBytes: 4096, SystemInstruction: strings.Repeat("x", 8192)}); err != nil {
		t.Fatal(err)
	}
}
