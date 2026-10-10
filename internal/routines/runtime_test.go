package routines

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type modelFn func(context.Context, model.ModelRequest) (model.ModelResponse, error)

func (f modelFn) Generate(ctx context.Context, r model.ModelRequest) (model.ModelResponse, error) {
	return f(ctx, r)
}
func TestNativeRoutineUsesPolicyApprovalAndOriginalBudget(t *testing.T) {
	for _, decision := range []sessions.ApprovalDecision{sessions.ApprovalAllow, sessions.ApprovalDeny} {
		t.Run(string(decision), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "one"), []byte("private-file-content"), 0600)
			bs, e := bots.NewStore(filepath.Join(dir, "bots.json"))
			if e != nil {
				t.Fatal(e)
			}
			ts, e := threads.NewStore(filepath.Join(dir, "threads.json"))
			if e != nil {
				t.Fatal(e)
			}
			b := bots.Bot{ID: "bot", Name: "Bot", Instructions: "private", ProviderID: "fake", Model: "fake", Tools: []string{"read_file"}, PermissionMode: bots.PermissionAsk}
			if e := bs.Create(b); e != nil {
				t.Fatal(e)
			}
			now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
			for _, id := range []threads.ID{"thread", "other"} {
				if e := ts.Create(threads.Thread{ID: id, BotID: "bot", Workspace: dir, CreatedAt: now, UpdatedAt: now}); e != nil {
					t.Fatal(e)
				}
			}
			requests := make(chan model.ModelRequest, 2)
			registry := &providers.Registry{}
			registry.Register(providers.Factory{ID: "fake", Build: func(providers.Config) (model.Model, error) {
				return modelFn(func(_ context.Context, r model.ModelRequest) (model.ModelResponse, error) {
					if r.Messages[len(r.Messages)-1].Role == model.RoleUser {
						return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "read", Name: "read_file", Arguments: json.RawMessage(`{"path":"one"}`)}}}, nil
					}
					requests <- r
					return model.ModelResponse{FinalText: "done"}, nil
				}), nil
			}})
			budget := agentloop.DefaultBudget()
			budget.MaxSteps = 2
			m, e := sessions.NewManager(sessions.Dependencies{Bots: bs, Threads: ts, Providers: registry, WebApprovals: true, Config: func(context.Context, providers.ID) (providers.Config, error) {
				return providers.Config{MaxResponseBytes: 4096}, nil
			}}, sessions.Options{Budget: budget, EventCapacity: 64, MaxSessions: 16})
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close(ctx)
			store, e := Open(filepath.Join(dir, "routines.json"))
			if e != nil {
				t.Fatal(e)
			}
			s, e := New(store, m, ts, bs, false, false)
			if e != nil {
				t.Fatal(e)
			}
			s.Create(ctx, baseInput("one"), now)
			other := baseInput("two")
			other.ThreadID = "other"
			s.Create(ctx, other, now)
			if e := s.Tick(ctx, now.Add(time.Hour)); e != nil {
				t.Fatal(e)
			}
			views, _ := s.List()
			var id sessions.ID
			for _, v := range views {
				if v.SessionID != "" {
					id = sessions.ID(v.SessionID)
				}
			}
			if id == "" {
				t.Fatal("not admitted")
			}
			var pending *sessions.ApprovalPresentation
			for pending == nil {
				obs, e := m.ObserveEvents(id)
				if e != nil {
					t.Fatal(e)
				}
				pending, e = m.PendingApproval(id)
				if e != nil {
					t.Fatal(e)
				}
				if pending == nil {
					select {
					case <-obs.Changed:
					case <-ctx.Done():
						t.Fatal("approval timeout")
					}
				}
			}
			if m.ScheduledStatus("bot") != sessions.WaitingApproval {
				t.Fatal("missing waiting")
			}
			if e := s.Tick(ctx, now.Add(time.Hour+15*time.Minute)); e != nil {
				t.Fatal(e)
			}
			vs, _ := s.List()
			for _, v := range vs {
				if v.ID == "two" && v.SessionID != "" {
					t.Fatal("pending bypass")
				}
			}
			if _, e := m.Start(ctx, sessions.StartRequest{SessionID: "parallel", ThreadID: "other", Message: "user"}); e == nil {
				t.Fatal("manual overlap with scheduled bot")
			}
			if e := m.ResolveApproval(id, pending.ID, decision); e != nil {
				t.Fatal(e)
			}
			result, e := m.Wait(ctx, id)
			if e != nil || result.Status != sessions.Completed || result.Steps != 2 {
				t.Fatal("normal loop not used", result, e)
			}
			receipt := (<-requests).Messages
			if (receipt[len(receipt)-1].Content == "private-file-content") != (decision == sessions.ApprovalAllow) {
				t.Fatal("ToolPolicy or approval bypass")
			}
		})
	}
}
