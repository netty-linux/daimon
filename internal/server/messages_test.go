package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Content is deliberate on this endpoint; the shared expect helper checks
// privacy for metadata endpoints and must continue to reject private text there.
func expectMessages(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("messages response: %d", w.Code)
	}
}

type conversationTestOptions struct {
	generate modelFunc
	failRole conversations.Role
}
type conversationFailure struct {
	*conversations.Store
	role conversations.Role
}

func (s conversationFailure) Append(ctx context.Context, message conversations.Message) error {
	if message.Role == s.role {
		return errors.New("private-persistence-cause")
	}
	return s.Store.Append(ctx, message)
}
func withConversation(t *testing.T, f *fixture, configured ...conversationTestOptions) *conversations.Store {
	t.Helper()
	store, err := conversations.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, end := context.WithTimeout(context.Background(), time.Second)
	defer end()
	if err = f.manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	registry := &providers.Registry{}
	generate := modelFunc(finalModel)
	var persistence sessions.ConversationStore = store
	if len(configured) != 0 {
		if configured[0].generate != nil {
			generate = configured[0].generate
		}
		persistence = conversationFailure{store, configured[0].failRole}
	}
	if err = registry.Register(providers.Factory{ID: "fake", Build: func(providers.Config) (model.Model, error) { return generate, nil }}); err != nil {
		t.Fatal(err)
	}
	f.manager, err = sessions.NewManager(sessions.Dependencies{Bots: f.bots, Threads: f.threads, Providers: registry, Config: func(context.Context, providers.ID) (providers.Config, error) {
		return providers.Config{MaxResponseBytes: 1024}, nil
	}, Conversations: persistence}, sessions.Options{Budget: agentloop.DefaultBudget(), MaxSessions: 64, EventCapacity: 64})
	if err != nil {
		t.Fatal(err)
	}
	f.server.deps.Sessions = f.manager
	f.server.deps.Conversations = store
	t.Cleanup(func() {
		ctx, end := context.WithTimeout(context.Background(), time.Second)
		defer end()
		_ = f.manager.Close(ctx)
		_ = store.Close()
	})
	return store
}
func TestConversationHTTPFailuresAndAbort(t *testing.T) {
	for _, mode := range []string{"model-failed", "aborted", "user-persistence", "assistant-persistence"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			options := conversationTestOptions{generate: func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
				close(entered)
				if mode == "aborted" {
					<-ctx.Done()
					return model.ModelResponse{}, ctx.Err()
				}
				if mode == "model-failed" {
					return model.ModelResponse{}, errors.New("private-model-content")
				}
				return model.ModelResponse{FinalText: "private-answer"}, nil
			}}
			if mode == "user-persistence" {
				options.failRole = conversations.User
			}
			if mode == "assistant-persistence" {
				options.failRole = conversations.Assistant
			}
			f := setup(t, finalModel, 8)
			withConversation(t, f, options)
			status := 202
			if mode == "user-persistence" {
				status = 500
			}
			expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"failed-run", "thread", "private-user", "message-failed"}), status)
			if mode == "aborted" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("model not entered")
				}
				expect(t, request(f.server, "POST", "/api/v1/sessions/failed-run/abort", nil), 202)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			snapshot, err := f.manager.Wait(ctx, "failed-run")
			if err == nil || snapshot.Status == sessions.Completed {
				t.Fatal("failure hidden")
			}
			w := request(f.server, "GET", "/api/v1/threads/thread/messages", nil)
			expectMessages(t, w)
			var page struct {
				Messages []conversations.Message `json:"messages"`
			}
			if json.Unmarshal(w.Body.Bytes(), &page) != nil {
				t.Fatal("page")
			}
			want := 1
			if mode == "user-persistence" {
				want = 0
			}
			if len(page.Messages) != want || (want == 1 && page.Messages[0].Role != conversations.User) {
				t.Fatal("fabricated assistant")
			}
			if mode == "assistant-persistence" && snapshot.ErrorCategory != sessions.Persistence {
				t.Fatal("classification")
			}
			expect(t, request(f.server, "GET", "/api/v1/sessions/failed-run", nil), 200)
		})
	}
}
func TestMessagesHTTPPersistencePaginationPrivacy(t *testing.T) {
	f := setup(t, finalModel, 8)
	store := withConversation(t, f)
	expect(t, request(f.server, "GET", "/api/v1/threads/thread/messages", nil), 200)
	expect(t, request(f.server, "GET", "/api/v1/threads/missing/messages", nil), 404)
	for _, q := range []string{"after=-1", "after=18446744073709551616", "limit=0", "limit=101", "after=1&after=2", "unknown=1", "limit=", "after=+1"} {
		expect(t, request(f.server, "GET", "/api/v1/threads/thread/messages?"+q, nil), 400)
	}
	expect(t, request(f.server, "POST", "/api/v1/threads/thread/messages", `{}`), 405)
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"run", "thread", "PRIVATE_USER_TEXT", "message-one"}), 202)
	wait(t, f, "run")
	w := request(f.server, "GET", "/api/v1/threads/thread/messages?limit=1", nil)
	expectMessages(t, w)
	var page struct {
		Messages []conversations.Message `json:"messages"`
		Next     uint64                  `json:"next_after"`
		More     bool                    `json:"has_more"`
	}
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Messages) != 1 || page.Messages[0].Content != "PRIVATE_USER_TEXT" || !page.More || page.Next != 1 {
		t.Fatal("first page")
	}
	w = request(f.server, "GET", "/api/v1/threads/thread/messages?after=1&limit=1", nil)
	expectMessages(t, w)
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Messages) != 1 || page.Messages[0].Role != conversations.Assistant || page.Messages[0].Content != "private-result" || page.More {
		t.Fatal("assistant page")
	}
	for _, route := range []string{"/api/v1/health", "/api/v1/providers", "/api/v1/sessions/run", "/api/v1/sessions/run/events"} {
		w = request(f.server, "GET", route, nil)
		if strings.Contains(w.Body.String(), "PRIVATE_USER_TEXT") || strings.Contains(w.Body.String(), "private-result") {
			t.Fatal("content leak")
		}
	}
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"duplicate", "thread", "retry", "message-one"}), 409)
	expect(t, request(f.server, "DELETE", "/api/v1/threads/thread", nil), 409)
	if _, err := f.threads.Get("thread"); err != nil {
		t.Fatal("history thread deleted")
	}
	items, err := store.List(context.Background(), "thread")
	if err != nil || len(items) != 2 {
		t.Fatal("duplicate changed history")
	}
}
func TestMessagesCorruptionAndEmptyThreadDelete(t *testing.T) {
	f := setup(t, finalModel, 8)
	store := withConversation(t, f)
	// An empty Thread can be removed; deletion never creates/deletes a transcript.
	expect(t, request(f.server, "DELETE", "/api/v1/threads/thread", nil), 204)
	if err := f.threads.Create(f.thread); err != nil {
		t.Fatal(err)
	}
	// Inject classified read failure without exposing arbitrary cause text.
	f.server.deps.Conversations = badConversationReader{}
	w := request(f.server, "GET", "/api/v1/threads/thread/messages", nil)
	expect(t, w, 500)
	if strings.Contains(w.Body.String(), "private") {
		t.Fatal("error leak")
	}
	f.server.deps.Conversations = store
}

type badConversationReader struct{}

func (badConversationReader) List(context.Context, threads.ID) ([]conversations.Message, error) {
	return nil, errors.New("private-path")
}
func TestEncodedMessagePageBound(t *testing.T) {
	f := setup(t, finalModel, 8)
	store := withConversation(t, f)
	content := strings.Repeat("\x01", conversations.MaxAssistantBytes)
	for i := 0; i < 4; i++ {
		session := "large-" + string(rune('a'+i))
		user := conversations.Message{ID: conversations.ID("user-" + session), ThreadID: "thread", Role: conversations.User, Content: "question", CreatedAt: time.Now().UTC(), SessionID: session}
		if err := store.Append(context.Background(), user); err != nil {
			t.Fatal(err)
		}
		assistant := user
		assistant.ID = conversations.ID("assistant-" + session)
		assistant.Role = conversations.Assistant
		assistant.Content = content
		if err := store.Append(context.Background(), assistant); err != nil {
			t.Fatal(err)
		}
	}
	w := request(f.server, "GET", "/api/v1/threads/thread/messages?limit=100", nil)
	expectMessages(t, w)
	if w.Body.Len() > MaxResponseBytes || !strings.Contains(w.Body.String(), `"has_more":true`) {
		t.Fatal("encoded response bound")
	}
}
