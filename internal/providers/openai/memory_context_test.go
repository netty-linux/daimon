package openai

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/model"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdditionalContextSeparateAndOrdered(t *testing.T) {
	var wire struct {
		Messages []struct{ Role, Content string }
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		io.WriteString(w, textResponse)
	}))
	defer server.Close()
	for _, contextual := range []string{"", "bounded-memory-context"} {
		p, err := New(Config{BaseURL: server.URL, Model: "fake", MaxResponseBytes: 4096, SystemInstruction: "bot-instructions", AdditionalContext: contextual})
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Generate(context.Background(), model.ModelRequest{Messages: []model.Message{{Role: model.RoleAssistant, Content: "past"}, {Role: model.RoleUser, Content: "current"}}})
		if err != nil {
			t.Fatal(err)
		}
		expected := []string{"bot-instructions", "past", "current"}
		if contextual != "" {
			expected = []string{"bot-instructions", contextual, "past", "current"}
		}
		if len(wire.Messages) != len(expected) {
			t.Fatal("unexpected message count")
		}
		for i, text := range expected {
			if wire.Messages[i].Content != text {
				t.Fatal("context order changed")
			}
		}
		if wire.Messages[0].Role != "system" || (contextual != "" && wire.Messages[1].Role != "system") {
			t.Fatal("context framing role")
		}
	}
	for _, bad := range []string{strings.Repeat("x", 32*1024+1), string([]byte{255})} {
		if _, err := New(Config{BaseURL: server.URL, Model: "fake", MaxResponseBytes: 4096, AdditionalContext: bad}); err == nil {
			t.Fatal("invalid contextual data accepted")
		}
	}
	if _, err := New(Config{BaseURL: server.URL, Model: "fake", MaxResponseBytes: 4096, SystemInstruction: strings.Repeat("x", 8193)}); err == nil {
		t.Fatal("instructions limit weakened")
	}
}
