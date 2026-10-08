package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sessions"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setupApproval(t *testing.T) *fixture {
	t.Helper()
	generate := modelFunc(func(_ context.Context, r model.ModelRequest) (model.ModelResponse, error) {
		if r.Messages[len(r.Messages)-1].Role == model.RoleUser {
			return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "private-tool-call", Name: "list_dir", Arguments: []byte(`{"path":"."}`)}}}, nil
		}
		return model.ModelResponse{FinalText: "done"}, nil
	})
	f := setup(t, generate, 64)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	f.bot.Tools = []string{"list_dir"}
	if err := f.bots.Update(f.bot); err != nil {
		t.Fatal(err)
	}
	registry := &providers.Registry{}
	if err := registry.Register(providers.Factory{ID: "fake", Build: func(providers.Config) (model.Model, error) { return generate, nil }}); err != nil {
		t.Fatal(err)
	}
	var err error
	f.manager, err = sessions.NewManager(sessions.Dependencies{Bots: f.bots, Threads: f.threads, Providers: registry, Config: func(context.Context, providers.ID) (providers.Config, error) {
		return providers.Config{MaxResponseBytes: 4096}, nil
	}, WebApprovals: true}, sessions.Options{Budget: agentloop.DefaultBudget(), EventCapacity: 64, MaxSessions: 32})
	if err != nil {
		t.Fatal(err)
	}
	f.server.deps.Sessions = f.manager
	return f
}
func pendingHTTP(t *testing.T, f *fixture, id sessions.ID) sessions.ApprovalPresentation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		o, err := f.manager.ObserveEvents(id)
		if err != nil {
			t.Fatal(err)
		}
		w := request(f.server, "GET", "/api/v1/sessions/"+string(id)+"/approval", nil)
		expect(t, w, 200)
		var response struct {
			Approval *sessions.ApprovalPresentation `json:"approval"`
		}
		if json.Unmarshal(w.Body.Bytes(), &response) != nil {
			t.Fatal("presentation JSON")
		}
		if response.Approval != nil && o.Status == sessions.WaitingApproval {
			return *response.Approval
		}
		if o.Terminal {
			t.Fatal("terminal before review")
		}
		select {
		case <-o.Changed:
		case <-ctx.Done():
			t.Fatal("pending timeout")
		}
	}
}
func TestApprovalHTTPStrictDecisionsPrivacyAndReplay(t *testing.T) {
	f := setupApproval(t)
	expect(t, request(f.server, "GET", "/api/v1/sessions/unknown/approval", nil), 404)
	expect(t, request(f.server, "GET", "/api/v1/threads/thread/session", nil), 200)
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"review", "thread", "question", "message-review"}), 202)
	p := pendingHTTP(t, f, "review")
	path := "/api/v1/sessions/review/approvals/" + string(p.ID)
	for _, body := range []string{`{}`, `{"decision":"always_allow"}`, `{"decision":"allow_all"}`, `{"decision":""}`} {
		expect(t, request(f.server, "POST", path, body), 400)
	}
	for _, body := range []string{`{"decision":"allow","remember":"true"}`, `{"decision":"allow","decision":"deny"}`, `{"Decision":"allow"}`, `{"decision":null}`, `{"decision":"deny"}{}`} {
		expect(t, request(f.server, "POST", path, body), 400)
	}
	expect(t, request(f.server, "POST", path, strings.Repeat("x", MaxBodyBytes+1)), 413)
	w := request(f.server, "POST", path, `{"decision":"deny"}`)
	expect(t, w, 200)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS")
	}
	expect(t, request(f.server, "POST", path, `{"decision":"allow"}`), 409)
	wait(t, f, "review")
	w = request(f.server, "GET", "/api/v1/sessions/review/approval", nil)
	expect(t, w, 200)
	if !strings.Contains(w.Body.String(), `"approval":null`) {
		t.Fatal("pending retained")
	}
	for _, route := range []string{"/api/v1/sessions/review", "/api/v1/sessions/review/events", "/api/v1/health", "/api/v1/providers"} {
		w = request(f.server, "GET", route, nil)
		expect(t, w, 200)
		for _, secret := range []string{"private-tool-call", "arguments", "preview", "target", "question"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("approval data leak")
			}
		}
	}
}
func TestApprovalHTTPOriginAndExactSession(t *testing.T) {
	f := setupApproval(t)
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"review", "thread", "question", ""}), 202)
	p := pendingHTTP(t, f, "review")
	other := f.thread
	other.ID = "other"
	if err := f.threads.Create(other); err != nil {
		t.Fatal(err)
	}
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"other-review", "other", "question", ""}), 202)
	pendingHTTP(t, f, "other-review")
	expect(t, request(f.server, "POST", "/api/v1/sessions/other-review/approvals/"+string(p.ID), `{"decision":"allow"}`), 409)
	expect(t, request(f.server, "POST", "/api/v1/sessions/review/approvals/approval-unknown", `{"decision":"allow"}`), 404)
	path := "http://127.0.0.1:3000/api/v1/sessions/review/approvals/" + string(p.ID)
	for _, headers := range []map[string]string{{"Origin": "http://evil.example", "Sec-Fetch-Site": "cross-site"}, {"Sec-Fetch-Site": "same-origin"}, {"Origin": "null"}, {"Origin": "http://127.0.0.1:3001"}} {
		r := httptest.NewRequest("POST", path, bytes.NewBufferString(`{"decision":"allow"}`))
		r.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		f.server.localHTTP(w, r)
		expect(t, w, 403)
	}
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"decision":"allow"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:3000")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	f.server.localHTTP(w, r)
	expect(t, w, 200)
	wait(t, f, "review")
	if err := f.manager.Abort("other-review"); err != nil {
		t.Fatal(err)
	}
	wait(t, f, "other-review")
}
