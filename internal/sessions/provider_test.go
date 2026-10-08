package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/providers"
)

func TestCompatibleProviderReceivesBindingOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("configuration was not passed to HTTP adapter")
		}
		var body struct {
			Model    string
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "fixture-model" || len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[0].Content != "private-instructions" || body.Messages[1].Content != "private-prompt" {
			t.Error("binding or initial turn changed")
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"private-final"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	deps, options, _, _ := fixture(t, nil)
	deps.Providers = &providers.Registry{}
	if err := deps.Providers.Register(providers.CompatibleFactory("fixture")); err != nil {
		t.Fatal(err)
	}
	deps.Config = func(context.Context, providers.ID) (providers.Config, error) {
		return providers.Config{BaseURL: server.URL + "/v1", APIKey: "fixture-secret", MaxResponseBytes: 4096}, nil
	}
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	snap := wait(t, m, "session-a")
	data, _ := json.Marshal(snap)
	if strings.Contains(string(data), "fixture-secret") || strings.Contains(string(data), "private-final") {
		t.Fatal("private result retained in metadata")
	}
}
