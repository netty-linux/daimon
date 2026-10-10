package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/policy"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/threads"
)

type botReader struct {
	mu  sync.Mutex
	bot bots.Bot
	err error
}

func (r *botReader) Get(bots.ID) (bots.Bot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bots.Clone(r.bot), r.err
}

type threadReader struct {
	thread threads.Thread
	err    error
}

func (r *threadReader) Get(id threads.ID) (threads.Thread, error) {
	v := r.thread
	v.ID = id
	return v, r.err
}

type modelFunc func(context.Context, model.ModelRequest) (model.ModelResponse, error)

func (f modelFunc) Generate(ctx context.Context, req model.ModelRequest) (model.ModelResponse, error) {
	return f(ctx, req)
}

type approvalFunc func(context.Context, agentloop.ToolAuthorizationRequest) (bool, error)

func (f approvalFunc) Approve(ctx context.Context, req agentloop.ToolAuthorizationRequest) (bool, error) {
	return f(ctx, req)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func fixture(t *testing.T, generate modelFunc) (Dependencies, Options, *botReader, *threadReader) {
	t.Helper()
	bot := &botReader{bot: bots.Bot{ID: "coder", Name: "Coder", Instructions: "private-instructions", ProviderID: "fixture", Model: "fixture-model", Tools: []string{}, PermissionMode: bots.PermissionAsk}}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	thread := &threadReader{thread: threads.Thread{BotID: "coder", Workspace: t.TempDir(), CreatedAt: now, UpdatedAt: now}}
	registry := &providers.Registry{}
	if generate == nil {
		generate = func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
			return model.ModelResponse{FinalText: "private-final"}, nil
		}
	}
	if err := registry.Register(providers.Factory{ID: "fixture", Build: func(cfg providers.Config) (model.Model, error) {
		if cfg.Model != "fixture-model" || cfg.SystemInstruction != "private-instructions" {
			return nil, errors.New("binding was not passed to factory")
		}
		return generate, nil
	}}); err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{Bots: bot, Threads: thread, Providers: registry, Config: func(context.Context, providers.ID) (providers.Config, error) {
		return providers.Config{APIKey: "fixture-secret", MaxResponseBytes: 4096}, nil
	}}
	return deps, Options{Budget: agentloop.DefaultBudget(), EventCapacity: 64, MaxSessions: 64}, bot, thread
}

func manager(t *testing.T, deps Dependencies, options Options) *Manager {
	t.Helper()
	m, err := NewManager(deps, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return m
}

func start(t *testing.T, m *Manager, id ID, thread threads.ID) Snapshot {
	t.Helper()
	snap, err := m.Start(testContext(t), StartRequest{SessionID: id, ThreadID: thread, Message: "private-prompt"})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}
func wait(t *testing.T, m *Manager, id ID) Snapshot {
	t.Helper()
	snap, err := m.Wait(testContext(t), id)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestLifecycleAndPrivacy(t *testing.T) {
	deps, options, _, _ := fixture(t, nil)
	m := manager(t, deps, options)
	initial := start(t, m, "session-a", "thread-a")
	if initial.Status != Created || initial.StartedAt.IsZero() {
		t.Fatal("invalid initial state")
	}
	final := wait(t, m, "session-a")
	if final.Status != Completed || final.StopReason != agentloop.StopReasonCompleted || final.Steps != 1 || final.FinishedAt.Before(final.StartedAt) {
		t.Fatal(final)
	}
	for _, value := range []any{final, func() Replay { r, _ := m.EventsSince("session-a", 0); return r }()} {
		data, _ := json.Marshal(value)
		for _, private := range []string{"fixture-secret", "private-instructions", "private-prompt", "private-final"} {
			if strings.Contains(string(data), private) {
				t.Fatal("private content in metadata")
			}
		}
	}
	if _, err := m.Start(testContext(t), StartRequest{SessionID: "session-a", ThreadID: "thread-a", Message: "hello"}); !errors.Is(err, &Error{Kind: Duplicate}) {
		t.Fatal(err)
	}
	if err := m.Abort("session-a"); !errors.Is(err, &Error{Kind: NotRunning}) {
		t.Fatal(err)
	}
	start(t, m, "session-b", "thread-a")
	wait(t, m, "session-b")
	for _, err := range []error{func() error { _, e := m.Get("missing"); return e }(), func() error { _, e := m.EventsSince("missing", 0); return e }(), m.Abort("missing"), func() error { _, e := m.Wait(testContext(t), "missing"); return e }(), func() error { _, e := m.Binding("missing"); return e }()} {
		if !errors.Is(err, &Error{Kind: NotFound}) {
			t.Fatal(err)
		}
	}
}

func TestConcurrentObservationAbortAndThreadReservation(t *testing.T) {
	entered := make(chan struct{}, 2)
	deps, options, _, _ := fixture(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return model.ModelResponse{}, ctx.Err()
	})
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	start(t, m, "session-b", "thread-b")
	ctx := testContext(t)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if snap, _ := m.Get("session-a"); snap.Status != Running {
		t.Fatal(snap)
	}
	if _, err := m.Start(ctx, StartRequest{SessionID: "blocked", ThreadID: "thread-a", Message: "hello"}); !errors.Is(err, &Error{Kind: ThreadBusy}) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				if _, err := m.Get("session-a"); err != nil {
					t.Error(err)
				}
				if _, err := m.EventsSince("session-b", 0); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	if err := m.Abort("session-a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Abort("session-b"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for _, id := range []ID{"session-a", "session-b"} {
		snap, err := m.Wait(ctx, id)
		if snap.Status != Aborted || !errors.Is(err, ErrAborted) || !errors.Is(err, context.Canceled) || snap.StopReason != agentloop.StopReasonCanceled {
			t.Fatalf("abort: %v %v", snap, err)
		}
	}
}

func TestBindingMutationAndFrozenRegistry(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	deps, options, bot, _ := fixture(t, func(ctx context.Context, req model.ModelRequest) (model.ModelResponse, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return model.ModelResponse{}, ctx.Err()
		}
		if len(req.Tools) != 1 || req.Tools[0].Name != "echo" {
			return model.ModelResponse{}, errors.New("capabilities changed")
		}
		return model.ModelResponse{FinalText: "done"}, nil
	})
	bot.bot.Tools = []string{"echo"}
	m := manager(t, deps, options)
	// Registration after construction cannot affect this manager.
	if err := deps.Providers.Register(providers.CompatibleFactory("later")); err != nil {
		t.Fatal(err)
	}
	start(t, m, "session-a", "thread-a")
	ctx := testContext(t)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	bot.mu.Lock()
	bot.bot.Instructions = "changed"
	bot.bot.Model = "changed"
	bot.bot.Tools[0] = "unknown"
	bot.mu.Unlock()
	binding, err := m.Binding("session-a")
	if err != nil {
		t.Fatal(err)
	}
	if binding.Model != "fixture-model" || binding.Instructions != "private-instructions" || binding.Tools[0] != "echo" {
		t.Fatal("binding changed")
	}
	binding.Tools[0] = "mutated"
	again, _ := m.Binding("session-a")
	if again.Tools[0] != "echo" {
		t.Fatal("binding slice shared")
	}
	close(release)
	wait(t, m, "session-a")
}

func TestWaitingApprovalAndDenial(t *testing.T) {
	requested, release := make(chan struct{}), make(chan struct{})
	deps, options, bot, _ := fixture(t, nil)
	bot.bot.Tools = []string{"read_file"}
	bot.bot.PermissionMode = bots.PermissionReadOnly
	deps.Providers = &providers.Registry{}
	if err := deps.Providers.Register(providers.Factory{ID: "fixture", Build: func(providers.Config) (model.Model, error) {
		return model.NewScripted(
			model.ScriptStep{Response: model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "read-1", Name: "read_file", Arguments: []byte(`{"path":"not-present"}`)}}}},
			model.ScriptStep{Response: model.ModelResponse{FinalText: "denied"}},
		), nil
	}}); err != nil {
		t.Fatal(err)
	}
	deps.Approvals = func(context.Context, ID) (Approvals, error) {
		return Approvals{Reads: approvalFunc(func(ctx context.Context, _ agentloop.ToolAuthorizationRequest) (bool, error) {
			close(requested)
			select {
			case <-release:
				return false, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		})}, nil
	}
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	ctx := testContext(t)
	select {
	case <-requested:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if snap, _ := m.Get("session-a"); snap.Status != WaitingApproval {
		t.Fatal(snap)
	}
	close(release)
	final := wait(t, m, "session-a")
	if final.ToolCalls != 1 {
		t.Fatal("controlled denial did not preserve loop attempt count")
	}
	replay, _ := m.EventsSince("session-a", 0)
	want := []agentloop.EventKind{agentloop.ApprovalRequested, agentloop.ApprovalDenied, agentloop.ToolDenied}
	index := 0
	for _, event := range replay.Events {
		if index < len(want) && event.Event.Kind == want[index] {
			index++
		}
		if event.Event.Kind == agentloop.ToolRequested {
			t.Fatal("denied read started")
		}
	}
	if index != len(want) {
		t.Fatal("approval order missing")
	}
}

func TestEventReplayBoundaries(t *testing.T) {
	deps, options, _, _ := fixture(t, nil)
	options.EventCapacity = 2
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	final := wait(t, m, "session-a")
	r, err := m.EventsSince("session-a", 0)
	if err != nil || !r.Gap || len(r.Events) != 2 || r.LastSequence != final.LastEventSequence {
		t.Fatal(r, err)
	}
	if r.Events[0].Sequence+1 != r.Events[1].Sequence || r.Events[1].Event.Kind != agentloop.LoopStopped {
		t.Fatal("event order")
	}
	r.Events[0].Event.Kind = "mutated"
	again, _ := m.EventsSince("session-a", r.FirstAvailable-1)
	if again.Gap || again.Events[0].Event.Kind == "mutated" {
		t.Fatal("shared/gap replay")
	}
	after, _ := m.EventsSince("session-a", final.LastEventSequence)
	if len(after.Events) != 0 || after.Gap {
		t.Fatal(after)
	}
	future, _ := m.EventsSince("session-a", ^uint64(0))
	if len(future.Events) != 0 || future.Gap {
		t.Fatal(future)
	}
}

func TestEventSequenceCannotWrap(t *testing.T) {
	b := eventBuffer{items: make([]Event, 1), last: ^uint64(0)}
	if b.append(agentloop.Event{Kind: agentloop.LoopStarted}) || b.last != ^uint64(0) || b.count != 0 {
		t.Fatal("sequence wrapped")
	}
}

func TestFailurePanicAndExternalCancellation(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(fmt.Sprint(panics), func(t *testing.T) {
			cause := errors.New("fixture-secret private-instructions private-prompt")
			deps, options, _, _ := fixture(t, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
				if panics {
					panic("fixture-secret")
				}
				return model.ModelResponse{}, cause
			})
			m := manager(t, deps, options)
			start(t, m, "session-a", "thread-a")
			snap, err := m.Wait(testContext(t), "session-a")
			if err == nil || snap.Status != Failed || snap.StopReason != agentloop.StopReasonModelError || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal(snap, err)
			}
			if !panics && !errors.Is(err, cause) {
				t.Fatal("lost cause")
			}
			r, _ := m.EventsSince("session-a", 0)
			stops := 0
			for _, e := range r.Events {
				if e.Event.Kind == agentloop.LoopStopped {
					stops++
					if e.Event.StopReason != agentloop.StopReasonModelError {
						t.Fatal("panic bypassed loop stop semantics")
					}
				}
			}
			if stops != 1 {
				t.Fatal("wrong loop_stopped count")
			}
		})
	}
	entered := make(chan struct{})
	deps, options, _, _ := fixture(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
		close(entered)
		<-ctx.Done()
		return model.ModelResponse{FinalText: "late success"}, nil
	})
	m := manager(t, deps, options)
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	if _, err := m.Start(ctx, StartRequest{SessionID: "session-a", ThreadID: "thread-a", Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	snap, err := m.Wait(testContext(t), "session-a")
	if snap.Status != Aborted || !errors.Is(err, context.Canceled) || errors.Is(err, ErrAborted) {
		t.Fatal(snap, err)
	}
}

func TestUntrustedErrorCategoryCannotEnterSnapshot(t *testing.T) {
	deps, options, _, _ := fixture(t, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
		return model.ModelResponse{}, &Error{Kind: "fixture-secret"}
	})
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	snap, err := m.Wait(testContext(t), "session-a")
	data, _ := json.Marshal(snap)
	if snap.ErrorCategory != Execution || strings.Contains(string(data), "fixture-secret") || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("untrusted error category exposed")
	}
}

func TestValidationTransitionsCapacityAndClose(t *testing.T) {
	for _, id := range []ID{"", "../bad", "UPPER", ID(strings.Repeat("a", 65))} {
		if err := ValidateID(id); !errors.Is(err, &Error{Kind: Invalid}) {
			t.Fatal(err)
		}
	}
	if err := ValidateID("session-a"); err != nil {
		t.Fatal(err)
	}
	if !ValidTransition(Created, Running) || !ValidTransition(Running, WaitingApproval) || !ValidTransition(WaitingApproval, Running) || !ValidTransition(Running, Completed) {
		t.Fatal("valid transition rejected")
	}
	for _, status := range []Status{Completed, Failed, Aborted, "unknown"} {
		if ValidTransition(status, Running) {
			t.Fatal("terminal/unknown transition accepted")
		}
	}
	deps, options, _, _ := fixture(t, nil)
	bad := options
	bad.Budget = agentloop.Budget{}
	if _, err := NewManager(deps, bad); !errors.Is(err, agentloop.ErrInvalidConfig) {
		t.Fatal(err)
	}
	bad = options
	bad.EventCapacity = 0
	if _, err := NewManager(deps, bad); !errors.Is(err, &Error{Kind: Invalid}) {
		t.Fatal(err)
	}
	options.MaxSessions = 1
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	wait(t, m, "session-a")
	start(t, m, "session-b", "thread-b")
	wait(t, m, "session-b")
	if _, err := m.Get("session-a"); !errors.Is(err, &Error{Kind: NotFound}) {
		t.Fatal(err)
	}
	if err := m.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(testContext(t), StartRequest{SessionID: "session-c", ThreadID: "thread-c", Message: "hello"}); !errors.Is(err, &Error{Kind: Closed}) {
		t.Fatal(err)
	}
}

func TestRealStoresAndBoundedWorkspaceRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "evidence.txt"), []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	botStore, err := bots.NewStore(filepath.Join(t.TempDir(), "bots.json"))
	if err != nil {
		t.Fatal(err)
	}
	threadStore, err := threads.NewStore(filepath.Join(t.TempDir(), "threads.json"))
	if err != nil {
		t.Fatal(err)
	}
	deps, options, bot, thread := fixture(t, nil)
	bot.bot.Tools = []string{"read_file"}
	thread.thread.ID = "thread-a"
	thread.thread.Workspace = root
	if err := botStore.Create(bot.bot); err != nil {
		t.Fatal(err)
	}
	if err := threadStore.Create(thread.thread); err != nil {
		t.Fatal(err)
	}
	deps.Bots, deps.Threads = botStore, threadStore
	deps.Providers = &providers.Registry{}
	if err := deps.Providers.Register(providers.Factory{ID: "fixture", Build: func(providers.Config) (model.Model, error) {
		step := 0
		return modelFunc(func(_ context.Context, req model.ModelRequest) (model.ModelResponse, error) {
			step++
			if len(req.Tools) != 1 || req.Tools[0].Name != "read_file" {
				return model.ModelResponse{}, errors.New("wrong tools")
			}
			if step == 1 {
				return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "read-1", Name: "read_file", Arguments: []byte(`{"path":"evidence.txt"}`)}}}, nil
			}
			if req.Messages[len(req.Messages)-1].Content != "evidence" {
				return model.ModelResponse{}, errors.New("read escaped workspace")
			}
			return model.ModelResponse{FinalText: "done"}, nil
		}), nil
	}}); err != nil {
		t.Fatal(err)
	}
	deps.Approvals = func(context.Context, ID) (Approvals, error) {
		return Approvals{Reads: approvalFunc(func(context.Context, agentloop.ToolAuthorizationRequest) (bool, error) { return true, nil })}, nil
	}
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	if final := wait(t, m, "session-a"); final.ToolCalls != 1 {
		t.Fatal(final)
	}
}

var _ policy.ApprovalProvider = approvalFunc(nil)
