package sessions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers"
)

func TestResolutionFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		kind   Kind
		change func(*Dependencies, *botReader, *threadReader)
	}{
		{"thread missing", ThreadResolution, func(_ *Dependencies, _ *botReader, r *threadReader) {
			r.err = errors.New("private-thread fixture-secret")
		}},
		{"thread invalid", ThreadResolution, func(_ *Dependencies, _ *botReader, r *threadReader) { r.thread.CreatedAt = time.Time{} }},
		{"bot missing", BotResolution, func(_ *Dependencies, r *botReader, _ *threadReader) { r.err = errors.New("private-bot fixture-secret") }},
		{"bot invalid", BotResolution, func(_ *Dependencies, r *botReader, _ *threadReader) { r.bot.Instructions = "" }},
		{"bot mismatch", BotResolution, func(_ *Dependencies, r *botReader, _ *threadReader) { r.bot.ID = "other" }},
		{"provider missing", ProviderResolution, func(_ *Dependencies, r *botReader, _ *threadReader) { r.bot.ProviderID = "missing" }},
		{"tool missing", ToolResolution, func(_ *Dependencies, r *botReader, _ *threadReader) { r.bot.Tools = []string{"not_available"} }},
		{"readonly write", ToolResolution, func(_ *Dependencies, r *botReader, _ *threadReader) {
			r.bot.PermissionMode = bots.PermissionReadOnly
			r.bot.Tools = []string{"create_file"}
		}},
		{"readonly other", ToolResolution, func(_ *Dependencies, r *botReader, _ *threadReader) {
			r.bot.PermissionMode = bots.PermissionReadOnly
			r.bot.Tools = []string{"echo"}
		}},
		{"ask write lacks opt-in", ToolResolution, func(_ *Dependencies, r *botReader, _ *threadReader) { r.bot.Tools = []string{"replace_file"} }},
		{"workspace missing", WorkspaceResolution, func(_ *Dependencies, _ *botReader, r *threadReader) {
			r.thread.Workspace = "/not-existing-daimon-session-fixture"
		}},
		{"workspace relative", WorkspaceResolution, func(_ *Dependencies, _ *botReader, r *threadReader) { r.thread.Workspace = "relative-root" }},
		{"config missing", ConfigResolution, func(d *Dependencies, _ *botReader, _ *threadReader) {
			d.Config = func(context.Context, providers.ID) (providers.Config, error) {
				return providers.Config{}, errors.New("fixture-secret")
			}
		}},
		{"config invalid", ConfigResolution, func(d *Dependencies, _ *botReader, _ *threadReader) {
			d.Config = func(context.Context, providers.ID) (providers.Config, error) { return providers.Config{}, nil }
		}},
		{"model drift", ConfigResolution, func(d *Dependencies, _ *botReader, _ *threadReader) {
			d.Config = func(context.Context, providers.ID) (providers.Config, error) {
				return providers.Config{MaxResponseBytes: 4096, Model: "different"}, nil
			}
		}},
		{"instruction drift", ConfigResolution, func(d *Dependencies, _ *botReader, _ *threadReader) {
			d.Config = func(context.Context, providers.ID) (providers.Config, error) {
				return providers.Config{MaxResponseBytes: 4096, SystemInstruction: "different"}, nil
			}
		}},
		{"approval absent", ApprovalResolution, func(_ *Dependencies, r *botReader, _ *threadReader) { r.bot.Tools = []string{"read_file"} }},
		{"approval empty", ApprovalResolution, func(d *Dependencies, r *botReader, _ *threadReader) {
			r.bot.Tools = []string{"read_file"}
			d.Approvals = func(context.Context, ID) (Approvals, error) { return Approvals{}, nil }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			deps, options, bot, thread := fixture(t, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
				called = true
				return model.ModelResponse{FinalText: "done"}, nil
			})
			tt.change(&deps, bot, thread)
			m := manager(t, deps, options)
			start(t, m, "session-a", "thread-a")
			snap, err := m.Wait(testContext(t), "session-a")
			if !errors.Is(err, &Error{Kind: tt.kind}) || snap.Status != Failed || called || snap.Steps != 0 {
				t.Fatalf("resolution: %v %v", snap, err)
			}
			if strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "private-") {
				t.Fatal("unsafe resolution error")
			}
			r, _ := m.EventsSince("session-a", 0)
			if len(r.Events) != 0 {
				t.Fatal("loop started after failed resolution")
			}
		})
	}
}

func TestFactoryConfigErrorPreservesCause(t *testing.T) {
	deps, options, _, _ := fixture(t, nil)
	cause := errors.New("fixture-secret")
	deps.Providers = &providers.Registry{}
	if err := deps.Providers.Register(providers.Factory{ID: "fixture", Build: func(providers.Config) (model.Model, error) { return nil, cause }}); err != nil {
		t.Fatal(err)
	}
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	_, err := m.Wait(testContext(t), "session-a")
	if !errors.Is(err, &Error{Kind: ConfigResolution}) || !errors.Is(err, cause) || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal(err)
	}
}

func TestStartupAbortAndCloseOwnWorkers(t *testing.T) {
	entered := make(chan struct{})
	deps, options, _, _ := fixture(t, nil)
	deps.Config = func(ctx context.Context, _ providers.ID) (providers.Config, error) {
		close(entered)
		<-ctx.Done()
		return providers.Config{}, ctx.Err()
	}
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	ctx := testContext(t)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if snap, _ := m.Get("session-a"); snap.Status != Created {
		t.Fatal(snap)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	snap, err := m.Wait(ctx, "session-a")
	if snap.Status != Aborted || !errors.Is(err, ErrAborted) || snap.StopReason != "" {
		t.Fatal(snap, err)
	}
}

func TestAbortWaitingApproval(t *testing.T) {
	entered := make(chan struct{})
	deps, options, bot, _ := fixture(t, nil)
	bot.bot.Tools = []string{"read_file"}
	deps.Providers = &providers.Registry{}
	if err := deps.Providers.Register(providers.Factory{ID: "fixture", Build: func(providers.Config) (model.Model, error) {
		return model.NewScripted(model.ScriptStep{Response: model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "read-1", Name: "read_file", Arguments: []byte(`{"path":"private"}`)}}}}), nil
	}}); err != nil {
		t.Fatal(err)
	}
	deps.Approvals = func(context.Context, ID) (Approvals, error) {
		return Approvals{Reads: approvalFunc(func(ctx context.Context, _ agentloop.ToolAuthorizationRequest) (bool, error) {
			close(entered)
			<-ctx.Done()
			return false, ctx.Err()
		})}, nil
	}
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	ctx := testContext(t)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := m.Abort("session-a"); err != nil {
		t.Fatal(err)
	}
	snap, err := m.Wait(ctx, "session-a")
	if snap.Status != Aborted || !errors.Is(err, ErrAborted) || snap.ToolCalls != 0 {
		t.Fatal(snap, err)
	}
}

func TestModelDeadlineRejectsLateSuccess(t *testing.T) {
	deps, options, _, _ := fixture(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
		<-ctx.Done()
		return model.ModelResponse{FinalText: "late success"}, nil
	})
	options.Budget.MaxModelCallDuration = 20 * time.Millisecond
	m := manager(t, deps, options)
	start(t, m, "session-a", "thread-a")
	snap, err := m.Wait(testContext(t), "session-a")
	if snap.Status != Failed || snap.StopReason != agentloop.StopReasonModelTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(snap, err)
	}
}
