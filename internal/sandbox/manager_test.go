package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/tools"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	mu                     sync.Mutex
	remotes                map[string]Remote
	gate                   chan struct{}
	started                chan struct{}
	failCreate, failDelete bool
	calls                  []string
	journal                string
}

func (f *fakeBackend) Create(ctx context.Context, r CreateRequest) (Remote, error) {
	if _, e := os.ReadFile(f.journal); e != nil {
		return Remote{}, e
	}
	remote := Remote{Ref: "local:" + r.Name, Name: r.Name, Runtime: "gvisor", Ready: true}
	f.mu.Lock()
	f.remotes[remote.Ref] = remote
	f.mu.Unlock()
	if f.started != nil {
		close(f.started)
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return Remote{}, ctx.Err()
		}
	}
	if f.failCreate {
		return Remote{}, errorOf(Provision)
	}
	return remote, nil
}
func (f *fakeBackend) Get(ctx context.Context, ref string) (Remote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.remotes[ref]
	if !ok {
		return Remote{}, errorOf(NotFound)
	}
	return r, nil
}
func (f *fakeBackend) Delete(ctx context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDelete {
		return errorOf(Transport)
	}
	delete(f.remotes, ref)
	return nil
}
func (f *fakeBackend) Close(context.Context) error { return nil }
func (f *fakeBackend) Computer(ctx context.Context, r Remote, id string) (*computer.Manager, error) {
	a, e := computer.NewSandboxAdapter(id, func(ctx context.Context, name string, args json.RawMessage) (tools.ToolResult, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.remotes[r.Ref]; !ok {
			return tools.ToolResult{}, computer.ErrUnavailable
		}
		f.calls = append(f.calls, r.Ref)
		return tools.ToolResult{Content: "guest observation"}, nil
	}, "list_apps")
	if e != nil {
		return nil, e
	}
	return computer.NewManager(a)
}
func profile() Profile {
	return Profile{Backend: CUALocal, Image: "linux", Runtime: "gvisor", Resources: "small", Network: "outbound"}
}
func setup(t *testing.T) (*Manager, *fakeBackend, *computer.Manager) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sandboxes.json")
	f := &fakeBackend{remotes: map[string]Remote{}, journal: path}
	catalog, _ := computer.NewManager(nil)
	m, e := NewManager(path, f, catalog, DefaultOptions())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = m.Close(ctx)
	})
	return m, f, catalog
}
func TestCreateGuestActionAndCleanup(t *testing.T) {
	m, f, catalog := setup(t)
	l, e := m.Create(context.Background(), "session-one", profile())
	if e != nil {
		t.Fatal(e)
	}
	i, _ := m.Get(l.ID())
	if i.Status != Running || i.ComputerID != computerID(l.ID()) {
		t.Fatal(i)
	}
	child, e := catalog.ForComputer(context.Background(), i.ComputerID)
	if e != nil || child != l.Computer {
		t.Fatal(e)
	}
	b, e := child.Open(l.Context, "session-one", computer.Profile{Enabled: true, Backend: computer.CUALocal, MCPServerID: "sandbox"}, []string{"mcp__sandbox__list_apps"}, false)
	if e != nil {
		t.Fatal(e)
	}
	tool, ok := b.Tool("mcp__sandbox__list_apps")
	if !ok {
		t.Fatal("missing guest tool")
	}
	if _, e = tool.Execute(l.Context, json.RawMessage(`{}`)); e != nil {
		t.Fatal(e)
	}
	if len(f.calls) != 1 || f.calls[0] != "local:"+ownedName(l.ID()) {
		t.Fatal(f.calls)
	}
	if e = l.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	i, _ = m.Get(l.ID())
	if i.Status != Deleted || i.Cleanup != "complete" {
		t.Fatal(i)
	}
	if _, e = catalog.ForComputer(context.Background(), i.ComputerID); e == nil {
		t.Fatal("stale computer reused")
	}
	if len(f.remotes) != 0 {
		t.Fatal("leaked guest")
	}
	if _, e = m.Create(context.Background(), "session-one", profile()); e == nil {
		t.Fatal("same session admitted twice")
	}
}
func TestPartialCreateCanceledAndFailureCleaned(t *testing.T) {
	for _, cancelCreate := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "abort"}[cancelCreate], func(t *testing.T) {
			m, f, _ := setup(t)
			f.failCreate = !cancelCreate
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelCreate {
				f.gate = make(chan struct{})
				f.started = make(chan struct{})
				go func() { <-f.started; cancel() }()
			}
			_, e := m.Create(ctx, "session-one", profile())
			if e == nil {
				t.Fatal("expected failure")
			}
			if cancelCreate && !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			if len(f.remotes) != 0 || m.List()[0].Cleanup != "complete" {
				t.Fatal(m.List())
			}
		})
	}
}
func TestDeleteFailureRecoveryOnlyOwned(t *testing.T) {
	m, f, catalog := setup(t)
	l, e := m.Create(context.Background(), "session-one", profile())
	if e != nil {
		t.Fatal(e)
	}
	f.failDelete = true
	if e = l.Close(context.Background()); e == nil {
		t.Fatal("delete failure hidden")
	}
	i, _ := m.Get(l.ID())
	if i.Status != Failed || i.Cleanup != "unresolved" {
		t.Fatal(i)
	}
	if _, e = catalog.ForComputer(context.Background(), i.ComputerID); e == nil {
		t.Fatal("cleanup did not revoke computer")
	}
	unknown := Remote{Ref: "local:daimon-unknown", Name: "daimon-unknown", Runtime: "gvisor"}
	f.remotes[unknown.Ref] = unknown
	f.failDelete = false
	recovered, e := NewManager(m.path, f, catalog, DefaultOptions())
	if e != nil {
		t.Fatal(e)
	}
	if e = recovered.Reconcile(context.Background()); e != nil {
		t.Fatal(e)
	}
	i, _ = recovered.Get(l.ID())
	if !i.Orphan || i.Cleanup != "complete" {
		t.Fatal(i)
	}
	if len(f.remotes) != 1 || f.remotes[unknown.Ref] != unknown {
		t.Fatal("unknown resource touched")
	}
}
func TestProfileAndTransitionsClosed(t *testing.T) {
	raw, _ := json.Marshal(profile())
	if _, e := DecodeProfile(raw); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`null`, `{"backend":"cua-local","image":"linux","runtime":"runc","browser":false,"resources":"small","network":"outbound"}`, `{"backend":"cua-local","image":"linux","runtime":"gvisor","browser":null,"resources":"small","network":"outbound"}`, string(raw) + ` {}`} {
		if _, e := DecodeProfile([]byte(bad)); e == nil {
			t.Fatal(bad)
		}
	}
	for _, from := range []Status{Creating, Running, Deleting, Deleted, Failed, "unknown"} {
		if ValidTransition(from, from) || ValidTransition(from, "unknown") {
			t.Fatal("open transition")
		}
	}
	if !ValidTransition(Creating, Running) || !ValidTransition(Deleting, Deleted) || ValidTransition(Deleted, Running) {
		t.Fatal("transition table")
	}
}
func TestRegistryStrictAndDefensiveCopies(t *testing.T) {
	m, _, _ := setup(t)
	l, e := m.Create(context.Background(), "session-one", profile())
	if e != nil {
		t.Fatal(e)
	}
	list := m.List()
	list[0].Runtime = "runc"
	i, _ := m.Get(l.ID())
	if i.Runtime != "gvisor" {
		t.Fatal("mutable info")
	}
	raw, _ := os.ReadFile(m.path)
	var data map[string]json.RawMessage
	_ = json.Unmarshal(raw, &data)
	data["Version"] = data["version"]
	delete(data, "version")
	bad, _ := json.Marshal(data)
	if e = os.WriteFile(m.path, bad, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = loadJournal(m.path); e == nil {
		t.Fatal("case alias accepted")
	}
	_ = saveJournal(context.Background(), m.path, m.records)
}

func TestConcurrentCreationAndClose(t *testing.T) {
	m, _, catalog := setup(t)
	var wg sync.WaitGroup
	results := make(chan *Lease, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			l, e := m.Create(context.Background(), "session-"+string(rune('a'+n)), profile())
			if e == nil {
				results <- l
			}
		}(n)
	}
	wg.Wait()
	close(results)
	if len(results) != 4 {
		t.Fatal("active limit", len(results))
	}
	if e := m.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	for l := range results {
		if l.Context.Err() == nil {
			t.Fatal("lease alive after shutdown")
		}
	}
	for _, i := range m.List() {
		if i.Cleanup != "complete" {
			t.Fatal(i)
		}
		if _, e := catalog.ForComputer(context.Background(), i.ComputerID); e == nil {
			t.Fatal("computer alive after shutdown")
		}
	}
}
func TestLifetimeExpiresAndRevokes(t *testing.T) {
	m, _, catalog := setup(t)
	m.options.Lifetime = time.Second
	l, e := m.Create(context.Background(), "session-one", profile())
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-l.Context.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("lifetime not bounded")
	}
	deadline := time.Now().Add(time.Second)
	for {
		i, _ := m.Get(l.ID())
		if i.Cleanup == "complete" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(i)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, e = catalog.ForComputer(context.Background(), computerID(l.ID())); e == nil {
		t.Fatal("expired computer live")
	}
}
