package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type modelFunc func(context.Context, model.ModelRequest) (model.ModelResponse, error)

func (f modelFunc) Generate(c context.Context, r model.ModelRequest) (model.ModelResponse, error) {
	return f(c, r)
}

type fixture struct {
	server  *Server
	manager *sessions.Manager
	bots    *BotStore
	threads *ThreadStore
	bot     bots.Bot
	thread  threads.Thread
}

func setup(t *testing.T, generate modelFunc, capacity int) *fixture {
	t.Helper()
	dir := t.TempDir()
	b, err := bots.NewStore(filepath.Join(dir, "bots.json"))
	if err != nil {
		t.Fatal(err)
	}
	th, err := threads.NewStore(filepath.Join(dir, "threads.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{bots: WrapBots(b), threads: WrapThreads(th)}
	f.bot = bots.Bot{ID: "bot", Name: "Bot", Instructions: "private-instructions", ProviderID: "fake", Model: "model", Tools: []string{}, PermissionMode: bots.PermissionAsk}
	now := time.Now().UTC()
	f.thread = threads.Thread{ID: "thread", BotID: "bot", Workspace: dir, CreatedAt: now, UpdatedAt: now}
	if f.bots.Create(f.bot) != nil || f.threads.Create(f.thread) != nil {
		t.Fatal("fixtures")
	}
	reg := &providers.Registry{}
	if err := reg.Register(providers.Factory{ID: "fake", Build: func(providers.Config) (model.Model, error) { return generate, nil }}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(providers.Factory{ID: "aaa", Build: func(providers.Config) (model.Model, error) { return generate, nil }}); err != nil {
		t.Fatal(err)
	}
	appCtx, cancel := context.WithCancel(context.Background())
	f.manager, err = sessions.NewManager(sessions.Dependencies{Bots: f.bots, Threads: f.threads, Providers: reg, Config: func(context.Context, providers.ID) (providers.Config, error) {
		return providers.Config{APIKey: "private-key", MaxResponseBytes: 1024}, nil
	}}, sessions.Options{Budget: agentloop.DefaultBudget(), EventCapacity: capacity, MaxSessions: 32})
	if err != nil {
		t.Fatal(err)
	}
	f.server, err = New(Dependencies{Bots: f.bots, Threads: f.threads, Providers: reg, Sessions: f.manager, SessionContext: appCtx})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		ctx, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		if err := f.manager.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return f
}
func finalModel(context.Context, model.ModelRequest) (model.ModelResponse, error) {
	return model.ModelResponse{FinalText: "private-result"}, nil
}
func request(s http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var data []byte
	switch b := body.(type) {
	case string:
		data = []byte(b)
	case nil:
	default:
		data, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func expect(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatal("content type")
	}
	if strings.Contains(w.Body.String(), "private-") {
		t.Fatalf("private data leaked: %s", w.Body.String())
	}
}
func wait(t *testing.T, f *fixture, id sessions.ID) sessions.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, _ := f.manager.Wait(ctx, id)
	if snap.FinishedAt.IsZero() {
		t.Fatal("unfinished session")
	}
	return snap
}

func TestHealthProvidersRoutes(t *testing.T) {
	f := setup(t, finalModel, 10)
	expect(t, request(f.server, "GET", "/api/v1/health", nil), 200)
	w := request(f.server, "POST", "/api/v1/health", nil)
	expect(t, w, 405)
	if w.Header().Get("Allow") != "GET" {
		t.Fatal("allow")
	}
	w = request(f.server, "GET", "/api/v1/providers", nil)
	expect(t, w, 200)
	if strings.Index(w.Body.String(), "aaa") > strings.Index(w.Body.String(), "fake") {
		t.Fatal("order")
	}
	for _, path := range []string{"/unknown", "/api/v2/health", "/api/v1/sessions/session/binding", "/api/v1/models", "/api/v1/health/extra"} {
		expect(t, request(f.server, "GET", path, nil), 404)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS")
	}
	expect(t, request(f.server, "OPTIONS", "/api/v1/bots", nil), 405)
}
func TestBotCRUD(t *testing.T) {
	f := setup(t, finalModel, 10)
	b := f.bot
	b.ID = "new"
	expect(t, request(f.server, "GET", "/api/v1/bots", nil), 200)
	expect(t, request(f.server, "POST", "/api/v1/bots", b), 201)
	expect(t, request(f.server, "POST", "/api/v1/bots", b), 409)
	expect(t, request(f.server, "GET", "/api/v1/bots/new", nil), 200)
	b.Name = "Updated"
	expect(t, request(f.server, "PUT", "/api/v1/bots/new", b), 200)
	stored, err := f.bots.Get("new")
	if err != nil || stored.Name != "Updated" || stored.Instructions != b.Instructions {
		t.Fatal("update")
	}
	expect(t, request(f.server, "PUT", "/api/v1/bots/bot", b), 400)
	b.Name = ""
	expect(t, request(f.server, "POST", "/api/v1/bots", b), 400)
	expect(t, request(f.server, "DELETE", "/api/v1/bots/new", nil), 204)
	for _, method := range []string{"GET", "DELETE", "PUT"} {
		body := any(nil)
		if method == "PUT" {
			b.Name = "New"
			body = b
		}
		expect(t, request(f.server, method, "/api/v1/bots/new", body), 404)
	}
}
func TestThreadCRUDInvariants(t *testing.T) {
	f := setup(t, finalModel, 10)
	th := f.thread
	th.ID = "new"
	expect(t, request(f.server, "GET", "/api/v1/threads", nil), 200)
	expect(t, request(f.server, "POST", "/api/v1/threads", th), 201)
	expect(t, request(f.server, "POST", "/api/v1/threads", th), 409)
	expect(t, request(f.server, "GET", "/api/v1/threads/new", nil), 200)
	th.Title = "Updated"
	th.UpdatedAt = th.UpdatedAt.Add(time.Second)
	expect(t, request(f.server, "PUT", "/api/v1/threads/new", th), 200)
	expect(t, request(f.server, "PUT", "/api/v1/threads/thread", th), 400)
	for _, mutate := range []func(*threads.Thread){func(x *threads.Thread) { x.BotID = "other" }, func(x *threads.Thread) { x.Workspace = "other" }, func(x *threads.Thread) { x.CreatedAt = x.CreatedAt.Add(-time.Second) }} {
		other := th
		mutate(&other)
		expect(t, request(f.server, "PUT", "/api/v1/threads/new", other), 409)
	}
	old := th
	old.UpdatedAt = old.CreatedAt
	expect(t, request(f.server, "PUT", "/api/v1/threads/new", old), 400)
	invalid := th
	invalid.CreatedAt = time.Time{}
	expect(t, request(f.server, "POST", "/api/v1/threads", invalid), 400)
	// The handler/store deliberately allow unresolved Bot references.
	other := th
	other.ID = "unresolved"
	other.BotID = "missing"
	expect(t, request(f.server, "POST", "/api/v1/threads", other), 201)
	expect(t, request(f.server, "DELETE", "/api/v1/threads/new", nil), 204)
	for _, method := range []string{"GET", "DELETE", "PUT"} {
		body := any(nil)
		if method == "PUT" {
			body = th
		}
		expect(t, request(f.server, method, "/api/v1/threads/new", body), 404)
	}
}
func TestStrictJSONAndLimits(t *testing.T) {
	f := setup(t, finalModel, 10)
	for _, data := range []string{"", "{", "{} {}", "null", "[]", `{"ID":"bot"}`, `{"id":"bot","id":"bot"}`, `{"api_key":"private-key"}`, `{"id":null}`, `{"tools":[null]}`, string([]byte{'{', '"', 'i', 'd', '"', ':', '"', 255, '"', '}'})} {
		expect(t, request(f.server, "POST", "/api/v1/bots", data), 400)
	}
	expect(t, request(f.server, "POST", "/api/v1/bots", strings.Repeat(" ", MaxBodyBytes+1)), 413)
	r := httptest.NewRequest("POST", "/api/v1/bots", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	expect(t, w, 415)
	for _, path := range []string{"/api/v1/bots/bot?key=value", "/api/v1/sessions/s/events?after=-1", "/api/v1/sessions/s/events?after=18446744073709551616", "/api/v1/sessions/s/events?after=1&after=2", "/api/v1/sessions/s/events?after=", "/api/v1/sessions/s/events?other=1", "/api/v1/sessions/s/events?after=%zz"} {
		expect(t, request(f.server, "GET", path, nil), 400)
	}
	expect(t, request(f.server, "POST", "/api/v1/sessions/s/abort", "{}"), 400)
}
func TestSessionLifecycleAndContextOwnership(t *testing.T) {
	entered := make(chan struct{})
	f := setup(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
		close(entered)
		<-ctx.Done()
		return model.ModelResponse{}, ctx.Err()
	}, 10)
	// Handler return/request cancellation must not end the admitted execution.
	reqCtx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(`{"id":"session","thread_id":"thread","message":"private-prompt"}`)).WithContext(reqCtx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	expect(t, w, 202)
	cancel()
	<-entered
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"session", "thread", "again", ""}), 409)
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"other", "thread", "again", ""}), 409)
	snap, err := f.manager.Get("session")
	if err != nil || snap.Status != sessions.Running {
		t.Fatal("HTTP context canceled session")
	}
	expect(t, request(f.server, "GET", "/api/v1/sessions/session", nil), 200)
	expect(t, request(f.server, "POST", "/api/v1/sessions/session/abort", nil), 202)
	if wait(t, f, "session").Status != sessions.Aborted {
		t.Fatal("abort")
	}
	expect(t, request(f.server, "POST", "/api/v1/sessions/session/abort", nil), 409)
	for _, suffix := range []string{"", "/events", "/abort"} {
		method := "GET"
		if suffix == "/abort" {
			method = "POST"
		}
		expect(t, request(f.server, method, "/api/v1/sessions/missing"+suffix, nil), 404)
	}
	for _, message := range []string{"", "  ", strings.Repeat("a", MaxMessageBytes+1)} {
		expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"invalid", "thread", message, ""}), 400)
	}
	expect(t, request(f.server, "POST", "/api/v1/sessions", `{"id":"write","thread_id":"thread","message":"write","enable_create_file":true}`), 400)
}
func TestAsyncResolutionAndPrivateFailures(t *testing.T) {
	for _, kind := range []string{"thread", "bot", "provider", "tool", "provider-error", "panic"} {
		t.Run(kind, func(t *testing.T) {
			generate := modelFunc(finalModel)
			if kind == "provider-error" {
				generate = func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
					return model.ModelResponse{}, errors.New("private-provider-error")
				}
			}
			if kind == "panic" {
				generate = func(context.Context, model.ModelRequest) (model.ModelResponse, error) { panic("private-panic") }
			}
			f := setup(t, generate, 10)
			threadID := threads.ID("thread")
			switch kind {
			case "thread":
				threadID = "missing"
			case "bot":
				if err := f.bots.Delete("bot"); err != nil {
					t.Fatal(err)
				}
			case "provider":
				b := f.bot
				b.ProviderID = "missing"
				if err := f.bots.Update(b); err != nil {
					t.Fatal(err)
				}
			case "tool":
				b := f.bot
				b.Tools = []string{"missing"}
				if err := f.bots.Update(b); err != nil {
					t.Fatal(err)
				}
			}
			expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"session", threadID, "private-prompt", ""}), 202)
			if wait(t, f, "session").Status != sessions.Failed {
				t.Fatal("expected async failure")
			}
			expect(t, request(f.server, "GET", "/api/v1/sessions/session", nil), 200)
			expect(t, request(f.server, "GET", "/api/v1/sessions/session/events", nil), 200)
		})
	}
}
func TestEventsReplayAndResponseLimit(t *testing.T) {
	f := setup(t, finalModel, 2)
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"session", "thread", "prompt", ""}), 202)
	wait(t, f, "session")
	w := request(f.server, "GET", "/api/v1/sessions/session/events?after=0", nil)
	expect(t, w, 200)
	var replay struct {
		Events []eventView
		Gap    bool
		Next   uint64 `json:"next_after"`
	}
	if json.Unmarshal(w.Body.Bytes(), &replay) != nil || !replay.Gap || len(replay.Events) != 2 || replay.Events[0].Sequence >= replay.Events[1].Sequence {
		t.Fatal(w.Body.String())
	}
	w = request(f.server, "GET", "/api/v1/sessions/session/events?after="+strconv.FormatUint(replay.Next, 10), nil)
	expect(t, w, 200)
	if !strings.Contains(w.Body.String(), `"events":[]`) {
		t.Fatal(w.Body.String())
	}
	// Projection of a bounded large manager buffer must paginate without drops.
	many := &replayManager{replay: sessions.Replay{FirstAvailable: 1, LastSequence: 300}}
	for seq := uint64(1); seq <= 300; seq++ {
		many.replay.Events = append(many.replay.Events, sessions.Event{Sequence: seq, Event: agentloop.Event{Kind: agentloop.ModelRequested}})
	}
	f.server.deps.Sessions = many
	w = request(f.server, "GET", "/api/v1/sessions/session/events", nil)
	expect(t, w, 200)
	var page struct {
		Events  []eventView
		HasMore bool   `json:"has_more"`
		Next    uint64 `json:"next_after"`
	}
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Events) != MaxEvents || !page.HasMore || page.Next != MaxEvents {
		t.Fatal(w.Body.String())
	}
}

type replayManager struct {
	SessionManager
	replay sessions.Replay
}

func (m *replayManager) EventsSince(sessions.ID, uint64) (sessions.Replay, error) {
	return m.replay, nil
}

func TestConcurrentPollingStoresAndAbort(t *testing.T) {
	entered := make(chan struct{}, 2)
	f := setup(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return model.ModelResponse{}, ctx.Err()
	}, 10)
	second := f.thread
	second.ID = "second"
	if err := f.threads.Create(second); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"thread", "second"} {
		expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{sessions.ID(id), threads.ID(id), "prompt", ""}), 202)
	}
	<-entered
	<-entered
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				expect(t, request(f.server, "GET", "/api/v1/sessions/thread", nil), 200)
				expect(t, request(f.server, "GET", "/api/v1/sessions/thread/events", nil), 200)
				expect(t, request(f.server, "GET", "/api/v1/bots", nil), 200)
				b := f.bot
				b.Name = "Updated"
				if err := f.bots.Update(b); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	expect(t, request(f.server, "POST", "/api/v1/sessions/thread/abort", nil), 202)
	expect(t, request(f.server, "POST", "/api/v1/sessions/second/abort", nil), 202)
	wg.Wait()
	wait(t, f, "thread")
	wait(t, f, "second")
}
func TestLoopbackListenerAndGracefulShutdown(t *testing.T) {
	f := setup(t, finalModel, 10)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.server.Serve(l) }()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + l.Addr().String() + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(data) != "{\"status\":\"ok\"}\n" {
		t.Fatal("health")
	}
	ctx, end := context.WithTimeout(context.Background(), 5*time.Second)
	defer end()
	if err := f.server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("serve leaked")
	}
	// HTTP shutdown does not close the caller-owned Manager.
	if _, err := f.manager.Start(ctx, sessions.StartRequest{SessionID: "after", ThreadID: "thread", Message: "prompt"}); err != nil {
		t.Fatal(err)
	}
	wait(t, f, "after")
	for _, ip := range []string{"0.0.0.0", "192.168.1.2", "8.8.8.8"} {
		if err := f.server.Serve(&addressListener{addr: &net.TCPAddr{IP: net.ParseIP(ip)}}); !errors.Is(err, ErrConfig) {
			t.Fatal("accepted public bind")
		}
	}
}

type addressListener struct{ addr net.Addr }

func (l *addressListener) Addr() net.Addr          { return l.addr }
func (*addressListener) Accept() (net.Conn, error) { return nil, errors.New("must not accept") }
func (*addressListener) Close() error              { return nil }
