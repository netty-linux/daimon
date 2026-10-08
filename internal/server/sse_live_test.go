package server

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sessions"
)

func TestSSEConnectDuringStartupWithoutEvents(t *testing.T) {
	f := setup(t, finalModel, 64)
	entered, release := make(chan struct{}), make(chan struct{})
	registry := &providers.Registry{}
	if err := registry.Register(providers.Factory{ID: "fake", Build: func(providers.Config) (model.Model, error) { return modelFunc(finalModel), nil }}); err != nil {
		t.Fatal(err)
	}
	appCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	original := f.manager
	m, err := sessions.NewManager(sessions.Dependencies{Bots: f.bots, Threads: f.threads, Providers: registry, Config: func(ctx context.Context, _ providers.ID) (providers.Config, error) {
		close(entered)
		select {
		case <-release:
			return providers.Config{MaxResponseBytes: 1024}, nil
		case <-ctx.Done():
			return providers.Config{}, ctx.Err()
		}
	}}, sessions.Options{Budget: agentloop.DefaultBudget(), EventCapacity: 64, MaxSessions: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := original.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.manager = m
	f.server.deps.Sessions = m
	f.server.deps.SessionContext = appCtx
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"session", "thread", "prompt", ""}), 202)
	<-entered
	base, client := localSSE(t, f)
	response, reader := connect(t, client, base+"/api/v1/sessions/session/events/stream", "")
	defer response.Body.Close()
	replay, _ := m.EventsSince("session", 0)
	if len(replay.Events) != 0 {
		t.Fatal("not connected before first event")
	}
	close(release)
	if nextFrame(t, reader).id != "1" {
		t.Fatal("first live event lost")
	}
	for nextFrame(t, reader).name != "stream_end" {
	}
	if _, err := readFrame(reader); err != io.EOF {
		t.Fatal(err)
	}
}

type slowWriter struct{ *streamRecorder }

func (w *slowWriter) Write([]byte) (int, error) {
	timer := time.NewTimer(time.Until(w.deadline))
	defer timer.Stop()
	<-timer.C
	return 0, context.DeadlineExceeded
}
func TestSSESlowWriterDeadlineEndsOnlyObserver(t *testing.T) {
	entered := make(chan struct{})
	f := setup(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
		close(entered)
		<-ctx.Done()
		return model.ModelResponse{}, ctx.Err()
	}, 64)
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"session", "thread", "prompt", ""}), 202)
	<-entered
	f.server.sse.writeTimeout = 10 * time.Millisecond
	w := &slowWriter{&streamRecorder{ResponseRecorder: httptest.NewRecorder()}}
	done := make(chan struct{})
	go func() {
		f.server.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/sessions/session/events/stream", nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("write exceeded deadline")
	}
	snapshot, _ := f.manager.Get("session")
	if snapshot.Status != sessions.Running || len(f.server.streams) != 0 {
		t.Fatal("slow client affected runtime/leaked slot")
	}
}
