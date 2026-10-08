package sessions

import (
	"context"
	"errors"
	"fmt"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func awaitApproval(t *testing.T, m *Manager, id ID, previous ApprovalID) ApprovalPresentation {
	t.Helper()
	ctx := testContext(t)
	for {
		o, err := m.ObserveEvents(id)
		if err != nil {
			t.Fatal(err)
		}
		p, err := m.PendingApproval(id)
		if err != nil {
			t.Fatal(err)
		}
		if p != nil && p.ID != previous && o.Status == WaitingApproval {
			return *p
		}
		if o.Terminal {
			t.Fatal("finished before approval")
		}
		select {
		case <-o.Changed:
		case <-ctx.Done():
			t.Fatal("approval timeout")
		}
	}
}
func webReadFixture(t *testing.T, calls []model.ToolCall) (*Manager, *threadReader, <-chan model.ModelRequest) {
	t.Helper()
	requests := make(chan model.ModelRequest, 2)
	deps, options, bot, thread := fixture(t, func(_ context.Context, r model.ModelRequest) (model.ModelResponse, error) {
		if r.Messages[len(r.Messages)-1].Role == model.RoleUser {
			return model.ModelResponse{ToolCalls: calls}, nil
		}
		requests <- model.CloneRequest(r)
		return model.ModelResponse{FinalText: "done"}, nil
	})
	deps.WebApprovals = true
	bot.bot.Tools = []string{"read_file", "list_dir"}
	if err := os.WriteFile(filepath.Join(thread.thread.Workspace, "one"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	return manager(t, deps, options), thread, requests
}
func TestWebReadAllowDenyReplay(t *testing.T) {
	for _, decision := range []ApprovalDecision{ApprovalAllow, ApprovalDeny} {
		t.Run(string(decision), func(t *testing.T) {
			m, _, requests := webReadFixture(t, []model.ToolCall{{ID: "private-call", Name: "read_file", Arguments: []byte(`{"path":"one"}`)}})
			start(t, m, "web", "thread")
			p := awaitApproval(t, m, "web", "")
			if p.Target != `"one"` || p.Preview != "" || p.Tool != "read_file" || !strings.Contains(p.Warning, "provider") {
				t.Fatal("read presentation")
			}
			p.Target = "mutation"
			again, _ := m.PendingApproval("web")
			if again.Target == "mutation" {
				t.Fatal("alias")
			}
			if err := m.ResolveApproval("web", p.ID, decision); err != nil {
				t.Fatal(err)
			}
			if err := m.ResolveApproval("web", p.ID, decision); !errors.Is(err, ErrApprovalResolved) {
				t.Fatal("replay")
			}
			if wait(t, m, "web").Status != Completed {
				t.Fatal("deny became abort")
			}
			r := <-requests
			receipt := r.Messages[len(r.Messages)-1]
			if decision == ApprovalAllow && receipt.Content != "original" {
				t.Fatal("read not executed")
			}
			if decision == ApprovalDeny && (!receipt.IsError || strings.Contains(receipt.Content, "original")) {
				t.Fatal("denial semantics")
			}
		})
	}
}
func TestWebBatchAuthorizationBeforeAnyRead(t *testing.T) {
	m, thread, requests := webReadFixture(t, []model.ToolCall{{ID: "first", Name: "read_file", Arguments: []byte(`{"path":"one"}`)}, {ID: "second", Name: "read_file", Arguments: []byte(`{"path":"one"}`)}})
	start(t, m, "batch", "thread")
	first := awaitApproval(t, m, "batch", "")
	if err := m.ResolveApproval("batch", first.ID, ApprovalAllow); err != nil {
		t.Fatal(err)
	}
	second := awaitApproval(t, m, "batch", first.ID)
	if err := os.WriteFile(filepath.Join(thread.thread.Workspace, "one"), []byte("changed-before-batch-approved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ResolveApproval("batch", second.ID, ApprovalAllow); err != nil {
		t.Fatal(err)
	}
	wait(t, m, "batch")
	request := <-requests
	for _, message := range request.Messages {
		if message.Role == model.RoleTool && message.Content != "changed-before-batch-approved" {
			t.Fatal("executed before all approvals")
		}
	}
}
func TestWebApprovalAbortDeadlineCloseExternal(t *testing.T) {
	for _, mode := range []string{"abort", "deadline", "close", "external"} {
		t.Run(mode, func(t *testing.T) {
			deps, options, bot, _ := fixture(t, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
				return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "one", Name: "list_dir", Arguments: []byte(`{"path":"."}`)}}}, nil
			})
			deps.WebApprovals = true
			bot.bot.Tools = []string{"list_dir"}
			if mode == "deadline" {
				options.Budget.MaxRunDuration = 100 * time.Millisecond
			}
			m := manager(t, deps, options)
			ctx, cancel := context.WithCancel(testContext(t))
			defer cancel()
			if _, err := m.Start(ctx, StartRequest{SessionID: "waiting", ThreadID: "thread", Message: "read"}); err != nil {
				t.Fatal(err)
			}
			p := awaitApproval(t, m, "waiting", "")
			switch mode {
			case "abort":
				if err := m.Abort("waiting"); err != nil {
					t.Fatal(err)
				}
			case "close":
				if err := m.Close(testContext(t)); err != nil {
					t.Fatal(err)
				}
			case "external":
				cancel()
			}
			snap, err := m.Wait(testContext(t), "waiting")
			if err == nil || snap.Status == Completed {
				t.Fatal("cancellation success")
			}
			pending, _ := m.PendingApproval("waiting")
			if pending != nil {
				t.Fatal("retained pending")
			}
			if err := m.ResolveApproval("waiting", p.ID, ApprovalAllow); !errors.Is(err, ErrApprovalNotPending) {
				t.Fatal("late decision")
			}
			if mode == "deadline" && snap.StopReason != agentloop.StopReasonRunTimeout {
				t.Fatal("budget identity")
			}
		})
	}
}
func TestWebApprovalConcurrentTabsBinding(t *testing.T) {
	m, _, _ := webReadFixture(t, []model.ToolCall{{ID: "call", Name: "read_file", Arguments: []byte(`{"path":"one"}`)}})
	start(t, m, "one", "thread")
	p := awaitApproval(t, m, "one", "")
	start(t, m, "two", "other-thread")
	other := awaitApproval(t, m, "two", "")
	if p.ID == other.ID {
		t.Fatal("same identity")
	}
	if err := m.ResolveApproval("two", p.ID, ApprovalAllow); !errors.Is(err, ErrApprovalMismatch) {
		t.Fatal("cross Session")
	}
	if err := m.ResolveApproval("one", "approval-unknown", ApprovalAllow); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatal("unknown")
	}
	if err := m.ResolveApproval("one", p.ID, "always_allow"); !errors.Is(err, ErrApprovalDecision) {
		t.Fatal("trust")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- m.ResolveApproval("one", p.ID, ApprovalDeny) }()
	}
	wg.Wait()
	a, b := <-results, <-results
	if (a == nil) == (b == nil) || (a != nil && !errors.Is(a, ErrApprovalResolved)) || (b != nil && !errors.Is(b, ErrApprovalResolved)) {
		t.Fatal("double decision")
	}
	if err := m.ResolveApproval("two", other.ID, ApprovalDeny); err != nil {
		t.Fatal(err)
	}
	wait(t, m, "one")
	wait(t, m, "two")
}
func TestIncompleteApprovalFailsClosed(t *testing.T) {
	for _, p := range []ApprovalPresentation{{Tool: "echo", Kind: "read", Target: "x", Warning: "x"}, {Tool: "read_file", Kind: "read", Target: "x", Warning: "x", Preview: "secret"}, {Tool: "replace_file", Kind: "write", Target: "x", Warning: "x"}, {Tool: "create_file", Kind: "write", Target: "x", Warning: "x", Preview: strings.Repeat("x", MaxApprovalPreviewBytes+1)}} {
		if validatePresentation(p) == nil {
			t.Fatal("incomplete presentation")
		}
	}
}

func TestApprovalCapacityStopsBatchBeforeEffects(t *testing.T) {
	m, _, _ := webReadFixture(t, []model.ToolCall{{ID: "first", Name: "read_file", Arguments: []byte(`{"path":"one"}`)}, {ID: "second", Name: "read_file", Arguments: []byte(`{"path":"one"}`)}})
	start(t, m, "bounded", "thread")
	p := awaitApproval(t, m, "bounded", "")
	m.mu.Lock()
	for i := 1; i < MaxApprovalsPerSession; i++ {
		m.sessions["bounded"].approvalIDs[ApprovalID(fmt.Sprintf("approval-prior-%d", i))] = true
	}
	m.mu.Unlock()
	if err := m.ResolveApproval("bounded", p.ID, ApprovalAllow); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Wait(testContext(t), "bounded")
	if err == nil || snapshot.Status != Failed || snapshot.ToolCalls != 0 {
		t.Fatal("capacity allowed effects")
	}
	pending, err := m.PendingApproval("bounded")
	if err != nil || pending != nil {
		t.Fatal("retained over-capacity presentation")
	}
}

func TestApprovalResolveRacesAbortAndCancellation(t *testing.T) {
	for _, mode := range []string{"abort", "context"} {
		t.Run(mode, func(t *testing.T) {
			m, _, _ := webReadFixture(t, []model.ToolCall{{ID: "call", Name: "read_file", Arguments: []byte(`{"path":"one"}`)}})
			ctx, cancel := context.WithCancel(testContext(t))
			defer cancel()
			if _, err := m.Start(ctx, StartRequest{SessionID: "racing", ThreadID: "thread", Message: "read"}); err != nil {
				t.Fatal(err)
			}
			p := awaitApproval(t, m, "racing", "")
			barrier := make(chan struct{})
			decision := make(chan error, 1)
			canceled := make(chan struct{})
			go func() { <-barrier; decision <- m.ResolveApproval("racing", p.ID, ApprovalAllow) }()
			go func() {
				<-barrier
				if mode == "abort" {
					_ = m.Abort("racing")
				} else {
					cancel()
				}
				close(canceled)
			}()
			close(barrier)
			err := <-decision
			<-canceled
			if err != nil && !errors.Is(err, ErrApprovalNotPending) {
				t.Fatal(err)
			}
			snapshot, _ := m.Wait(testContext(t), "racing")
			if !terminal(snapshot.Status) || snapshot.ToolCalls > 1 {
				t.Fatal("invalid race outcome")
			}
			if pending, _ := m.PendingApproval("racing"); pending != nil {
				t.Fatal("retained canceled approval")
			}
			if m.ResolveApproval("racing", p.ID, ApprovalAllow) == nil {
				t.Fatal("late decision accepted")
			}
		})
	}
}
