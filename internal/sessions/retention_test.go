package sessions

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/model"
)

func TestRetentionExact128AndIdentityAfterEviction(t *testing.T) {
	deps, options, _, _ := fixture(t, nil)
	options.MaxSessions = MaxRetainedSessions
	m := manager(t, deps, options)
	for i := 0; i < 128; i++ {
		id := ID(fmt.Sprintf("session-%03d", i))
		start(t, m, id, "thread")
		wait(t, m, id)
	}
	if len(m.sessions) != 128 {
		t.Fatal("wrong retention bound")
	}
	if _, err := m.Get("session-000"); err != nil {
		t.Fatal("premature eviction", err)
	}
	start(t, m, "session-new", "thread")
	wait(t, m, "session-new")
	if len(m.sessions) != 128 {
		t.Fatal("retained more than 128")
	}
	if _, err := m.Get("session-000"); !errors.Is(err, &Error{Kind: NotFound}) {
		t.Fatal("oldest not evicted", err)
	}
	if _, err := m.Get("session-001"); err != nil {
		t.Fatal("wrong victim", err)
	}
	if _, err := m.Start(testContext(t), StartRequest{SessionID: "session-000", ThreadID: "thread", Message: "hello"}); !errors.Is(err, &Error{Kind: Duplicate}) {
		t.Fatal("evicted identity reused", err)
	}
	options.MaxSessions = 129
	if _, err := NewManager(deps, options); !errors.Is(err, &Error{Kind: Invalid}) {
		t.Fatal("limit above 128 accepted")
	}
}

func TestRetentionOnlyFinalizedTerminalStates(t *testing.T) {
	for _, status := range []Status{Completed, Failed, Aborted, Created, Running, WaitingApproval} {
		t.Run(string(status), func(t *testing.T) {
			deps, options, _, _ := fixture(t, nil)
			options.MaxSessions = 1
			m := manager(t, deps, options)
			done := make(chan struct{})
			close(done)
			old := &state{snapshot: Snapshot{ID: "old", ThreadID: "old-thread", Status: status, StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, done: done, changed: make(chan struct{}), cancel: func(error) {}}
			m.sessions["old"] = old
			m.usedIDs["old"] = struct{}{}
			_, err := m.Start(testContext(t), StartRequest{SessionID: "new", ThreadID: "new-thread", Message: "hello"})
			if terminal(status) {
				if err != nil {
					t.Fatal(err)
				}
				wait(t, m, "new")
				if _, err = m.Get("old"); !errors.Is(err, &Error{Kind: NotFound}) {
					t.Fatal("terminal retained")
				}
			} else {
				if !errors.Is(err, &Error{Kind: Capacity}) || len(m.sessions) != 1 || m.sessions["old"] != old {
					t.Fatal("active state mutated", err)
				}
			}
		})
	}
}

func TestRetentionPreservesRealRunningAndPendingApproval(t *testing.T) {
	entered := make(chan struct{})
	deps, options, _, _ := fixture(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
		close(entered)
		<-ctx.Done()
		return model.ModelResponse{}, ctx.Err()
	})
	options.MaxSessions = 1
	m := manager(t, deps, options)
	start(t, m, "running", "thread")
	<-entered
	if _, err := m.Start(testContext(t), StartRequest{SessionID: "extra", ThreadID: "other", Message: "hello"}); !errors.Is(err, &Error{Kind: Capacity}) {
		t.Fatal(err)
	}
	if snapshot, err := m.Get("running"); err != nil || snapshot.Status != Running {
		t.Fatal("running lost")
	}
	pending, _, _ := webReadFixture(t, []model.ToolCall{{ID: "call", Name: "read_file", Arguments: []byte(`{"path":"one"}`)}})
	pending.options.MaxSessions = 1
	start(t, pending, "approval", "thread")
	presentation := awaitApproval(t, pending, "approval", "")
	if _, err := pending.Start(testContext(t), StartRequest{SessionID: "extra", ThreadID: "other", Message: "hello"}); !errors.Is(err, &Error{Kind: Capacity}) {
		t.Fatal(err)
	}
	after, err := pending.PendingApproval("approval")
	if err != nil || after == nil || after.ID != presentation.ID {
		t.Fatal("approval changed")
	}
}
