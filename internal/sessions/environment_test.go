//go:build linux

package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/environments"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/sandbox"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type fakeWorkspace struct {
	transferEntered           chan struct{}
	blockHydrate, blockExport bool
	mu                        sync.Mutex
	w                         environments.Workspace
	hydrateErr, exportErr     error
	hydrated                  bool
}

func (f *fakeWorkspace) Hydrate(ctx context.Context, w environments.Workspace) error {
	if f.blockHydrate {
		close(f.transferEntered)
		<-ctx.Done()
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if f.hydrateErr != nil {
		return f.hydrateErr
	}
	f.w = environments.Clone(w)
	f.hydrated = true
	return nil
}
func (f *fakeWorkspace) Export(ctx context.Context) (environments.Workspace, error) {
	if f.blockExport {
		close(f.transferEntered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return environments.Clone(f.w), f.exportErr
}

func TestAbortDuringEnvironmentHydrationAndSync(t *testing.T) {
	for _, phase := range []string{"hydrate", "sync"} {
		t.Run(phase, func(t *testing.T) {
			store := envStore(t)
			transfer := &fakeWorkspace{transferEntered: make(chan struct{}), blockHydrate: phase == "hydrate", blockExport: phase == "sync"}
			f := &sessionSandboxBackend{transfer: transfer}
			deps, opts, sm := sandboxDeps(t, f, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
				if phase == "hydrate" {
					t.Error("model ran during incomplete hydration")
				}
				transfer.appendFile("candidate")
				return model.ModelResponse{FinalText: "done"}, nil
			})
			deps.Environments = store
			m := manager(t, deps, opts)
			start(t, m, "run", "thread")
			select {
			case <-transfer.transferEntered:
			case <-testContext(t).Done():
				t.Fatal("transfer not reached")
			}
			if e := m.Abort("run"); e != nil {
				t.Fatal(e)
			}
			s, e := m.Wait(testContext(t), "run")
			if e == nil || s.Status != Aborted || !errors.Is(e, context.Canceled) || s.PersistentWorkspace.Committed {
				t.Fatal(s, e)
			}
			meta, e := store.Get(testContext(t), "thread")
			if e != nil || meta.Revision != 0 || sm.List()[0].Cleanup != "complete" {
				t.Fatal(meta, e, sm.List())
			}
		})
	}
}
func (f *fakeWorkspace) appendFile(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hydrated {
		panic("model before hydration")
	}
	f.w = append(f.w, environments.Entry{Path: path, Data: []byte(path)})
}
func envStore(t *testing.T) *environments.Store {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	s, e := environments.NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if _, e = s.Create(testContext(t), "thread"); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestEnvironmentLocalCloudLocalAndRestart(t *testing.T) {
	store := envStore(t)
	for i, placement := range []sandbox.Placement{sandbox.Local, sandbox.Cloud, sandbox.Local} {
		transfer := &fakeWorkspace{}
		backend := &sessionSandboxBackend{transfer: transfer}
		deps, opts, _ := sandboxDeps(t, backend, func(ctx context.Context, r model.ModelRequest) (model.ModelResponse, error) {
			if i == 0 {
				transfer.appendFile("A")
			} else if i == 1 {
				transfer.mu.Lock()
				ok := len(transfer.w) == 1 && transfer.w[0].Path == "A"
				transfer.mu.Unlock()
				if !ok {
					t.Error("cloud did not receive A")
				}
				transfer.appendFile("B")
			} else {
				transfer.mu.Lock()
				ok := len(transfer.w) == 2
				transfer.mu.Unlock()
				if !ok {
					t.Error("local did not receive A+B")
				}
			}
			return model.ModelResponse{FinalText: "done"}, nil
		})
		deps.Environments = store
		b := deps.Bots.(*botReader)
		if placement == sandbox.Cloud {
			b.bot.SandboxProfile.Backend = sandbox.CUACloud
			b.bot.SandboxProfile.Placement = sandbox.Cloud
		}
		m := manager(t, deps, opts)
		start(t, m, ID("run-"+strings.Repeat("a", i+1)), "thread")
		snapshot := wait(t, m, ID("run-"+strings.Repeat("a", i+1)))
		if snapshot.PersistentWorkspace == nil || !snapshot.PersistentWorkspace.Committed || snapshot.PersistentWorkspace.CommittedRevision != uint64(i+1) {
			t.Fatal(snapshot)
		}
		binding, _ := m.Binding(snapshot.ID)
		if binding.EnvironmentID == "" || binding.StartingRevision != uint64(i) {
			t.Fatal(binding)
		}
		bytes, _ := json.Marshal(snapshot)
		if strings.Contains(string(bytes), "/workspace") || strings.Contains(string(bytes), "\"A\"") {
			t.Fatal("snapshot leak")
		}
		if e := m.Close(testContext(t)); e != nil {
			t.Fatal(e)
		}
	}
	r, e := store.Acquire(testContext(t), "thread")
	if e != nil {
		t.Fatal(e)
	}
	expected := environments.Workspace{{Path: "A", Data: []byte("A")}, {Path: "B", Data: []byte("B")}}
	if !reflect.DeepEqual(r.Workspace(), expected) {
		t.Fatal(r.Workspace())
	}
	r.Close()
}
func TestEnvironmentFailuresPreserveRevisionAndCleanSandbox(t *testing.T) {
	for _, kind := range []string{"hydrate", "model", "sync", "abort", "delete"} {
		t.Run(kind, func(t *testing.T) {
			store := envStore(t)
			transfer := &fakeWorkspace{}
			f := &sessionSandboxBackend{transfer: transfer, failDelete: kind == "delete"}
			if kind == "hydrate" {
				transfer.hydrateErr = errors.New("private hydration")
			}
			if kind == "sync" {
				transfer.exportErr = errors.New("private contents")
			}
			entered := make(chan struct{})
			deps, opts, sm := sandboxDeps(t, f, func(ctx context.Context, r model.ModelRequest) (model.ModelResponse, error) {
				close(entered)
				if kind == "abort" {
					<-ctx.Done()
					return model.ModelResponse{}, ctx.Err()
				}
				transfer.appendFile("changed")
				if kind == "model" {
					return model.ModelResponse{}, errors.New("private model")
				}
				return model.ModelResponse{FinalText: "done"}, nil
			})
			deps.Environments = store
			m := manager(t, deps, opts)
			start(t, m, "run", "thread")
			if kind == "abort" {
				<-entered
				if e := m.Abort("run"); e != nil {
					t.Fatal(e)
				}
			}
			snapshot, e := m.Wait(testContext(t), "run")
			if e == nil || snapshot.Status == Completed {
				t.Fatal(snapshot, e)
			}
			meta, e := store.Get(testContext(t), "thread")
			if e != nil {
				t.Fatal(e)
			}
			expected := uint64(0)
			if kind == "delete" {
				expected = 1
				if !snapshot.PersistentWorkspace.Committed {
					t.Fatal(snapshot)
				}
			}
			if meta.Revision != expected {
				t.Fatal(meta)
			}
			if kind != "delete" && sm.List()[0].Cleanup != "complete" {
				t.Fatal(sm.List())
			}
			if kind == "delete" {
				f.mu.Lock()
				f.failDelete = false
				f.mu.Unlock()
			}
		})
	}
}
func TestPersistentEnvironmentRequiresSandboxBeforeModel(t *testing.T) {
	deps, opts, _, _ := fixture(t, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
		t.Error("called model")
		return model.ModelResponse{}, nil
	})
	deps.Environments = envStore(t)
	m := manager(t, deps, opts)
	start(t, m, "run", "thread")
	s, e := m.Wait(testContext(t), "run")
	if e == nil || s.ErrorCategory != EnvironmentResolution {
		t.Fatal(s, e)
	}
}
