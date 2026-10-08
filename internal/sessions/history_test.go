package sessions

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/threads"
	"strings"
	"testing"
)

func conversationFixture(t *testing.T) (*conversations.Store, Dependencies, Options) {
	t.Helper()
	store, err := conversations.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	deps, options, _, _ := fixture(t, nil)
	deps.Conversations = store
	return store, deps, options
}
func TestConversationCompletionAndNextTurnContext(t *testing.T) {
	store, deps, options := conversationFixture(t)
	calls := make(chan model.ModelRequest, 4)
	deps.Providers = nil
	base, _, _, _ := fixture(t, func(_ context.Context, req model.ModelRequest) (model.ModelResponse, error) {
		calls <- model.CloneRequest(req)
		req.Messages[0].Content = "mutated external copy"
		return model.ModelResponse{FinalText: "exact assistant\n é "}, nil
	})
	deps.Providers = base.Providers
	m := manager(t, deps, options)
	start(t, m, "first", "thread")
	wait(t, m, "first")
	start(t, m, "second", "thread")
	wait(t, m, "second")
	first, second := <-calls, <-calls
	if len(first.Messages) != 1 || len(second.Messages) != 3 || second.Messages[0].Content != "private-prompt" || second.Messages[1].Content != "exact assistant\n é " || second.Messages[2].Role != model.RoleUser {
		t.Fatal("continuity or cloning")
	}
	items, err := store.List(testContext(t), "thread")
	if err != nil || len(items) != 4 || items[1].Content != "exact assistant\n é " {
		t.Fatal("exact persistent transcript")
	}
	snap, _ := m.Get("second")
	if snap.Status != Completed {
		t.Fatal("completion")
	}
	// A new Manager cannot silently reuse the persisted turn identity after restart.
	restarted := manager(t, deps, options)
	_, err = restarted.Start(testContext(t), StartRequest{SessionID: "first", ThreadID: "thread", Message: "retry"})
	if !errors.Is(err, &Error{Kind: DuplicateMessage}) {
		t.Fatal("restart duplicate admitted")
	}
}
func TestFailedAndAbortedConversationHasUserOnly(t *testing.T) {
	for _, mode := range []string{"failed", "aborted", "invalid-utf8", "startup"} {
		t.Run(mode, func(t *testing.T) {
			store, deps, options := conversationFixture(t)
			entered := make(chan struct{})
			base, _, bot, _ := fixture(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
				close(entered)
				if mode == "aborted" {
					<-ctx.Done()
					return model.ModelResponse{}, ctx.Err()
				}
				if mode == "invalid-utf8" {
					return model.ModelResponse{FinalText: string([]byte{0xff})}, nil
				}
				return model.ModelResponse{}, errors.New("private failure")
			})
			deps.Providers = base.Providers
			if mode == "startup" {
				bot.err = errors.New("private bot")
				deps.Bots = bot
			}
			m := manager(t, deps, options)
			start(t, m, "run", "thread")
			if mode == "aborted" {
				<-entered
				if err := m.Abort("run"); err != nil {
					t.Fatal(err)
				}
			}
			snap, err := m.Wait(testContext(t), "run")
			if err == nil || snap.Status == Completed {
				t.Fatal("unexpected success")
			}
			items, readErr := store.List(testContext(t), "thread")
			if readErr != nil || len(items) != 1 || items[0].Role != conversations.User {
				t.Fatal("fabricated response")
			}
		})
	}
}

type failingConversation struct {
	ConversationStore
	assistant bool
}

func (f failingConversation) Append(ctx context.Context, m conversations.Message) error {
	if !f.assistant || m.Role == conversations.Assistant {
		return errors.New("private-persistence-path")
	}
	return f.ConversationStore.Append(ctx, m)
}
func TestPersistenceFailuresAreExplicit(t *testing.T) {
	for _, assistant := range []bool{false, true} {
		t.Run(map[bool]string{false: "user", true: "assistant"}[assistant], func(t *testing.T) {
			store, deps, options := conversationFixture(t)
			deps.Conversations = failingConversation{store, assistant}
			m := manager(t, deps, options)
			_, err := m.Start(testContext(t), StartRequest{SessionID: "run", ThreadID: "thread", Message: "text"})
			if assistant {
				if err != nil {
					t.Fatal(err)
				}
				snap, waitErr := m.Wait(testContext(t), "run")
				if snap.Status != Failed || snap.ErrorCategory != Persistence || waitErr == nil {
					t.Fatal("persistence failure hidden")
				}
			} else if !errors.Is(err, &Error{Kind: Persistence}) {
				t.Fatal("user persistence failure hidden")
			}
			items, _ := store.List(testContext(t), "thread")
			want := 0
			if assistant {
				want = 1
			}
			if len(items) != want {
				t.Fatal("partial transcript")
			}
		})
	}
}
func TestDuplicateMessageNoNewModelCall(t *testing.T) {
	store, deps, options := conversationFixture(t)
	m := manager(t, deps, options)
	req := StartRequest{SessionID: "one", ThreadID: "thread", MessageID: "message-fixed", Message: "text"}
	if _, err := m.Start(testContext(t), req); err != nil {
		t.Fatal(err)
	}
	wait(t, m, "one")
	req.SessionID = "two"
	if _, err := m.Start(testContext(t), req); !errors.Is(err, &Error{Kind: DuplicateMessage}) {
		t.Fatal("duplicate message")
	}
	items, _ := store.List(testContext(t), "thread")
	if len(items) != 2 {
		t.Fatal("duplicate appended")
	}
}
func TestWholeMessageSuffixAndBudget(t *testing.T) {
	store, deps, options := conversationFixture(t)
	options.Budget.MaxHistoryBytes = 80
	options.Budget.MaxFinalAnswerBytes = 20
	options.Budget.MaxHistoryMessages = 4
	for i, content := range []string{strings.Repeat("a", 40), "recent", strings.Repeat("é", 10)} {
		m := conversations.Message{ID: conversations.ID("message-" + string(rune('a'+i))), ThreadID: "thread", Role: conversations.User, Content: content, CreatedAt: nowUTC(), SessionID: "old-" + string(rune('a'+i))}
		if err := store.Append(testContext(t), m); err != nil {
			t.Fatal(err)
		}
	}
	history, err := prepareConversation(testContext(t), deps.Conversations, StartRequest{SessionID: "new", ThreadID: threads.ID("thread"), Message: "new"}, options.Budget)
	if err != nil || len(history) != 2 || history[0].Content != "recent" || history[1].Content != strings.Repeat("é", 10) {
		t.Fatal("whole-message ordered suffix")
	}
}
func TestIdleThreadReservationExcludesStart(t *testing.T) {
	_, deps, options := conversationFixture(t)
	m := manager(t, deps, options)
	err := m.WithIdleThread(testContext(t), "thread", func() error {
		_, err := m.Start(testContext(t), StartRequest{SessionID: "run", ThreadID: "thread", Message: "text"})
		if !errors.Is(err, &Error{Kind: ThreadBusy}) {
			t.Fatal("admission raced maintenance")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	start(t, m, "after", "thread")
	wait(t, m, "after")
}

type preparingConversation struct {
	ConversationStore
	entered chan struct{}
}

func (s preparingConversation) List(ctx context.Context, _ threads.ID) ([]conversations.Message, error) {
	close(s.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestAbortDuringConversationPreparation(t *testing.T) {
	store, deps, options := conversationFixture(t)
	entered := make(chan struct{})
	deps.Conversations = preparingConversation{store, entered}
	m := manager(t, deps, options)
	started := make(chan error, 1)
	go func() {
		_, err := m.Start(testContext(t), StartRequest{SessionID: "preparing", ThreadID: "thread", Message: "question"})
		started <- err
	}()
	<-entered
	if err := m.Abort("preparing"); err != nil {
		t.Fatal(err)
	}
	if err := <-started; !errors.Is(err, context.Canceled) || !errors.Is(err, ErrAborted) {
		t.Fatal("start cancellation identity")
	}
	snap, err := m.Wait(testContext(t), "preparing")
	if snap.Status != Aborted || snap.ErrorCategory != Canceled || !errors.Is(err, ErrAborted) {
		t.Fatal("preparation abort lifecycle")
	}
	items, err := store.List(testContext(t), "thread")
	if err != nil || len(items) != 0 {
		t.Fatal("aborted preparation wrote user")
	}
}

type cancelAfterAssistant struct {
	ConversationStore
	cancel context.CancelFunc
}

func (s cancelAfterAssistant) Append(ctx context.Context, message conversations.Message) error {
	if err := s.ConversationStore.Append(ctx, message); err != nil {
		return err
	}
	if message.Role == conversations.Assistant {
		s.cancel()
	}
	return nil
}

func TestConfirmedAssistantCommitWinsLaterCancellation(t *testing.T) {
	store, deps, options := conversationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps.Conversations = cancelAfterAssistant{store, cancel}
	m := manager(t, deps, options)
	if _, err := m.Start(ctx, StartRequest{SessionID: "committed", ThreadID: "thread", Message: "question"}); err != nil {
		t.Fatal(err)
	}
	snap, err := m.Wait(testContext(t), "committed")
	if err != nil || snap.Status != Completed {
		t.Fatal("confirmed commit hidden")
	}
	items, err := store.List(testContext(t), "thread")
	if err != nil || len(items) != 2 || items[1].Role != conversations.Assistant {
		t.Fatal("committed response lost")
	}
}
