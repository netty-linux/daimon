package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/environments"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/sandbox"
	"github.com/netty-linux/daimon/internal/tools"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type sessionSandboxBackend struct {
	transfer               environments.Transfer
	mu                     sync.Mutex
	r                      sandbox.Remote
	failCreate, failDelete bool
	gate, started          chan struct{}
	actions                atomic.Int32
}

func (f *sessionSandboxBackend) Workspace(context.Context, sandbox.Remote) (environments.Transfer, error) {
	if f.transfer == nil {
		return nil, environments.ErrTransfer
	}
	return f.transfer, nil
}

func (f *sessionSandboxBackend) Create(ctx context.Context, r sandbox.CreateRequest) (sandbox.Remote, error) {
	f.mu.Lock()
	f.r = sandbox.Remote{Ref: string(r.Profile.EffectivePlacement()) + ":" + r.Name, Name: r.Name, Runtime: "gvisor", Ready: true}
	remote := f.r
	f.mu.Unlock()
	if f.started != nil {
		close(f.started)
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return sandbox.Remote{}, ctx.Err()
		}
	}
	if f.failCreate {
		return sandbox.Remote{}, errors.New("private create error")
	}
	return remote, nil
}
func (f *sessionSandboxBackend) Get(ctx context.Context, ref string) (sandbox.Remote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.r.Ref != ref {
		return sandbox.Remote{}, &sandbox.Error{Kind: sandbox.NotFound}
	}
	return f.r, nil
}
func (f *sessionSandboxBackend) Delete(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDelete {
		return errors.New("private delete error")
	}
	f.r = sandbox.Remote{}
	return nil
}
func (f *sessionSandboxBackend) Close(context.Context) error { return nil }
func (f *sessionSandboxBackend) Computer(ctx context.Context, r sandbox.Remote, id string) (*computer.Manager, error) {
	backend := computer.CUALocal
	if strings.HasPrefix(r.Ref, "cloud:") {
		backend = computer.CUACloud
	}
	a, e := computer.NewPlacedSandboxAdapter(id, backend, func(context.Context, string, json.RawMessage) (tools.ToolResult, error) {
		f.actions.Add(1)
		return tools.ToolResult{Content: "guest apps"}, nil
	}, "list_apps")
	if e != nil {
		return nil, e
	}
	return computer.NewManager(a)
}
func sandboxDeps(t *testing.T, f *sessionSandboxBackend, generate modelFunc) (Dependencies, Options, *sandbox.Manager) {
	t.Helper()
	deps, opts, b, _ := fixture(t, generate)
	catalog, _ := computer.NewManager(nil)
	sm, e := sandbox.NewManagerWithCloud(filepath.Join(t.TempDir(), "sandboxes.json"), f, f, catalog, sandbox.DefaultOptions())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = sm.Close(testContext(t)) })
	deps.Computers = catalog
	deps.Sandboxes = sm
	deps.WebApprovals = true
	b.bot.SandboxProfile = &sandbox.Profile{Backend: sandbox.CUALocal, Image: "linux", Runtime: "gvisor", Resources: "small", Network: "outbound"}
	b.bot.Tools = []string{"mcp__sandbox__list_apps"}
	return deps, opts, sm
}
func TestSandboxSessionApprovalCompletionAndNewEnvironment(t *testing.T) {
	f := &sessionSandboxBackend{}
	var calls atomic.Int32
	deps, opts, sm := sandboxDeps(t, f, func(ctx context.Context, r model.ModelRequest) (model.ModelResponse, error) {
		if calls.Add(1) == 1 {
			return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "guest-observe", Name: "mcp__sandbox__list_apps", Arguments: []byte(`{}`)}}}, nil
		}
		return model.ModelResponse{FinalText: "done"}, nil
	})
	m := manager(t, deps, opts)
	start(t, m, "sandbox-run", "thread")
	p := awaitApproval(t, m, "sandbox-run", "")
	if p.ServerID != "sandbox" || p.ComputerID == "sandbox" || f.actions.Load() != 0 {
		t.Fatal(p)
	}
	if e := m.ResolveApproval("sandbox-run", p.ID, ApprovalAllow); e != nil {
		t.Fatal(e)
	}
	s, e := m.Wait(testContext(t), "sandbox-run")
	if e != nil {
		t.Fatal(e)
	}
	if s.Environment == nil || s.Environment.Cleanup != "complete" || f.actions.Load() != 1 || sm.List()[0].Status != sandbox.Deleted {
		t.Fatal(s)
	}
	first := s.Environment.SandboxID
	start(t, m, "sandbox-run-two", "thread")
	s, e = m.Wait(testContext(t), "sandbox-run-two")
	if e != nil || s.Environment.SandboxID == first {
		t.Fatal(s, e)
	}
}
func TestSandboxSessionFailureAndAbortBeforeModel(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(map[bool]string{false: "provision failure", true: "abort during create"}[abort], func(t *testing.T) {
			f := &sessionSandboxBackend{failCreate: !abort}
			if abort {
				f.started = make(chan struct{})
				f.gate = make(chan struct{})
			}
			var called atomic.Bool
			deps, opts, sm := sandboxDeps(t, f, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
				called.Store(true)
				return model.ModelResponse{FinalText: "unexpected"}, nil
			})
			m := manager(t, deps, opts)
			start(t, m, "sandbox-run", "thread")
			if abort {
				<-f.started
				if e := m.Abort("sandbox-run"); e != nil {
					t.Fatal(e)
				}
			}
			s, e := m.Wait(testContext(t), "sandbox-run")
			if e == nil || called.Load() || sm.List()[0].Cleanup != "complete" {
				t.Fatal(s, e)
			}
			if abort && s.Status != Aborted {
				t.Fatal(s)
			}
		})
	}
}

func TestSandboxSessionAbortRunAndDeletionFailure(t *testing.T) {
	for _, failDelete := range []bool{false, true} {
		t.Run(map[bool]string{false: "abort while approval pending", true: "deletion unresolved"}[failDelete], func(t *testing.T) {
			f := &sessionSandboxBackend{failDelete: failDelete}
			deps, opts, sm := sandboxDeps(t, f, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
				return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "guest-call", Name: "mcp__sandbox__list_apps", Arguments: []byte(`{}`)}}}, nil
			})
			m := manager(t, deps, opts)
			start(t, m, "sandbox-run", "thread")
			_ = awaitApproval(t, m, "sandbox-run", "")
			if e := m.Abort("sandbox-run"); e != nil {
				t.Fatal(e)
			}
			s, e := m.Wait(testContext(t), "sandbox-run")
			if e == nil || s.Status != Aborted || f.actions.Load() != 0 {
				t.Fatal(s, e)
			}
			want := "complete"
			if failDelete {
				want = "unresolved"
			}
			if s.Environment.Cleanup != want || sm.List()[0].Cleanup != want {
				t.Fatal(s, sm.List())
			}
			f.mu.Lock()
			f.failDelete = false
			f.mu.Unlock()
		})
	}
}

func TestSandboxProfileFrozenWhileProvisioning(t *testing.T) {
	f := &sessionSandboxBackend{started: make(chan struct{}), gate: make(chan struct{})}
	deps, opts, sm := sandboxDeps(t, f, nil)
	m := manager(t, deps, opts)
	start(t, m, "sandbox-run", "thread")
	<-f.started
	reader := deps.Bots.(*botReader)
	reader.mu.Lock()
	reader.bot.SandboxProfile.Resources = "standard"
	reader.bot.SandboxProfile.Runtime = "runc"
	reader.mu.Unlock()
	close(f.gate)
	s, e := m.Wait(testContext(t), "sandbox-run")
	if e != nil || s.Environment.Cleanup != "complete" {
		t.Fatal(s, e)
	}
	i := sm.List()[0]
	if i.Runtime != "gvisor" || i.Resources.CPU != 1 || i.Resources.MemoryMiB != 2048 {
		t.Fatal("mutable active profile", i)
	}
}

func TestCloudSessionApprovalAndCleanup(t *testing.T) {
	f := &sessionSandboxBackend{}
	var calls atomic.Int32
	deps, opts, sm := sandboxDeps(t, f, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
		if calls.Add(1) == 1 {
			return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "cloud-call", Name: "mcp__sandbox__list_apps", Arguments: []byte(`{}`)}}}, nil
		}
		return model.ModelResponse{FinalText: "done"}, nil
	})
	reader := deps.Bots.(*botReader)
	reader.bot.SandboxProfile.Backend = sandbox.CUACloud
	reader.bot.SandboxProfile.Placement = sandbox.Cloud
	m := manager(t, deps, opts)
	start(t, m, "cloud-run", "thread")
	p := awaitApproval(t, m, "cloud-run", "")
	if p.Backend != "cua-cloud" || p.ServerID != "sandbox" || !strings.Contains(p.Warning, "CLOUD") {
		t.Fatal(p)
	}
	if e := m.ResolveApproval("cloud-run", p.ID, ApprovalAllow); e != nil {
		t.Fatal(e)
	}
	snap, e := m.Wait(testContext(t), "cloud-run")
	if e != nil || snap.Environment.Placement != sandbox.Cloud || snap.Environment.Cleanup != "complete" || snap.Computer.Backend != computer.CUACloud || sm.List()[0].Placement != sandbox.Cloud {
		t.Fatal(snap, e)
	}
}
