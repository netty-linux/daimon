package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/tools"
)

func cloudProfile() Profile { p := profile(); p.Backend = CUACloud; p.Placement = Cloud; return p }

type fakeCloud struct {
	*fakeBackend
	getError error
}

func (f *fakeCloud) Create(ctx context.Context, r CreateRequest) (Remote, error) {
	remote := Remote{Ref: "cloud:" + r.Name, Name: r.Name, Runtime: "gvisor", Ready: true, ExpiresAt: time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second).Format(time.RFC3339)}
	f.mu.Lock()
	f.remotes[remote.Ref] = remote
	fail := f.failCreate
	f.mu.Unlock()
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return Remote{}, ctx.Err()
		}
	}
	if fail {
		return Remote{}, errorOf(Provision)
	}
	return remote, nil
}
func (f *fakeCloud) Get(ctx context.Context, ref string) (Remote, error) {
	if f.getError != nil {
		return Remote{}, f.getError
	}
	return f.fakeBackend.Get(ctx, ref)
}
func (f *fakeCloud) Computer(ctx context.Context, r Remote, id string) (*computer.Manager, error) {
	a, e := computer.NewPlacedSandboxAdapter(id, computer.CUACloud, func(context.Context, string, json.RawMessage) (tools.ToolResult, error) {
		return tools.ToolResult{Content: "cloud guest"}, nil
	}, "list_apps")
	if e != nil {
		return nil, e
	}
	return computer.NewManager(a)
}
func cloudSetup(t *testing.T) (*Manager, *fakeCloud, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sandboxes.json")
	f := &fakeCloud{fakeBackend: &fakeBackend{remotes: map[string]Remote{}, journal: path}}
	catalog, _ := computer.NewManager(nil)
	m, e := NewManagerWithCloud(path, UnavailableBackend{}, f, catalog, DefaultOptions())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, end := context.WithTimeout(context.Background(), time.Second)
		defer end()
		_ = m.Close(ctx)
	})
	return m, f, path
}
func TestCloudProfileExplicitPlacementAndLegacy(t *testing.T) {
	p := profile()
	if ValidateProfile(p) != nil || p.EffectivePlacement() != Local {
		t.Fatal(p)
	}
	cloud := cloudProfile()
	if ValidateProfile(cloud) != nil {
		t.Fatal(cloud)
	}
	for _, mutate := range []func(*Profile){func(p *Profile) { p.Placement = "" }, func(p *Profile) { p.Backend = CUALocal }, func(p *Profile) { p.Resources = "standard" }, func(p *Profile) { p.Network = "host" }} {
		bad := cloud
		mutate(&bad)
		if ValidateProfile(bad) == nil {
			t.Fatal(bad)
		}
	}
	raw, _ := json.Marshal(cloud)
	if _, e := DecodeProfile(raw); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`{"backend":"cua-cloud","placement":null,"image":"linux","runtime":"gvisor","browser":false,"resources":"small","network":"outbound"}`, `{"backend":"cua-cloud","image":"linux","runtime":"gvisor","browser":false,"resources":"small","network":"outbound"}`} {
		if _, e := DecodeProfile([]byte(bad)); e == nil {
			t.Fatal(bad)
		}
	}
	if r := limits(cloud, 15*time.Minute); r.CPU != 2 || r.MemoryMiB != 4096 {
		t.Fatal(r)
	}
	cloud.Resources = "medium"
	if r := limits(cloud, 15*time.Minute); r.CPU != 4 || r.MemoryMiB != 8192 {
		t.Fatal(r)
	}
}
func TestCloudQuotaUnresolvedCleanupAndRestart(t *testing.T) {
	m, f, path := cloudSetup(t)
	one, e := m.Create(context.Background(), "session-one", cloudProfile())
	if e != nil {
		t.Fatal(e)
	}
	two, e := m.Create(context.Background(), "session-two", cloudProfile())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Create(context.Background(), "session-three", cloudProfile()); !errors.Is(e, errorOf(Capacity)) {
		t.Fatal(e)
	}
	info, _ := m.Get(one.ID())
	if info.Placement != Cloud || info.Backend != CUACloud || info.ExpiresAt == "" {
		t.Fatal(info)
	}
	b, e := one.Computer.Open(one.Context, "session-one", computer.Profile{Enabled: true, Backend: computer.CUACloud, MCPServerID: "sandbox"}, []string{"mcp__sandbox__list_apps"}, false)
	if e != nil {
		t.Fatal(e)
	}
	if b.Metadata().Backend != computer.CUACloud {
		t.Fatal(b.Metadata())
	}
	_ = b.Close(context.Background())
	f.failDelete = true
	if e = one.Close(context.Background()); e == nil {
		t.Fatal("delete failure accepted")
	}
	if _, e = m.Create(context.Background(), "session-three", cloudProfile()); !errors.Is(e, errorOf(Capacity)) {
		t.Fatal("unresolved lost reservation", e)
	}
	// A signed-out restart must retain exact owned records, never infer absence.
	catalog, _ := computer.NewManager(nil)
	recovery, e := NewManagerWithCloud(path, UnavailableBackend{}, f, catalog, DefaultOptions())
	if e != nil {
		t.Fatal(e)
	}
	f.getError = errorOf(SignedOut)
	if e = recovery.Reconcile(context.Background()); e == nil {
		t.Fatal("auth outage accepted")
	}
	for _, i := range recovery.List() {
		if i.Cleanup != "unresolved" {
			t.Fatal(i)
		}
	}
	f.getError = nil
	f.failDelete = false
	if e = recovery.Reconcile(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, i := range recovery.List() {
		if i.Cleanup != "complete" {
			t.Fatal(i)
		}
	}
	_ = two.Close(context.Background())
}
func TestCloudPartialProvisionAndCancellationCleanup(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancel"}[cancel], func(t *testing.T) {
			m, f, _ := cloudSetup(t)
			f.failCreate = !cancel
			ctx, end := context.WithTimeout(context.Background(), 2*time.Second)
			defer end()
			if cancel {
				f.gate = make(chan struct{})
			}
			if _, e := m.Create(ctx, "session-partial", cloudProfile()); e == nil {
				t.Fatal("provision succeeded")
			}
			if len(f.remotes) != 0 || len(m.List()) != 1 || m.List()[0].Cleanup != "complete" {
				t.Fatal(m.List())
			}
		})
	}
}
func TestCloudCLIAuthenticationAndBoundedFlags(t *testing.T) {
	ref := "cloud:daimon-" + strings.Repeat("a", 24)
	var calls [][]string
	c := &CUA{placement: Cloud, runner: runnerFunc(func(ctx context.Context, args []string) ([]byte, int, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "auth" {
			return []byte(`{"authenticated":true,"fleet":"https://run.cua.ai","user":{"secret":"never public"}}`), 0, nil
		}
		return []byte(`{"id":"` + ref + `","name":"` + strings.TrimPrefix(ref, "cloud:") + `","runtime":"gvisor","location":"cloud","kind":"container","state":"ready","expires_at":"2030-01-01T00:15:00Z"}`), 0, nil
	})}
	p := cloudProfile()
	_, e := c.Create(context.Background(), CreateRequest{Name: strings.TrimPrefix(ref, "cloud:"), Profile: p, Limits: limits(p, 15*time.Minute), ReadyTimeout: time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	argv := strings.Join(calls[1], " ")
	for _, flag := range []string{"--on cloud", "--port env=3211", "--claim-ttl 900", "--max-pool-size 2", "--no-warm", "--runtime gvisor", "--memory 4096MB"} {
		if !strings.Contains(argv, flag) {
			t.Fatal(argv)
		}
	}
	if _, e = c.Get(context.Background(), "local:"+strings.TrimPrefix(ref, "cloud:")); e == nil {
		t.Fatal("placement crossed")
	}
	c.runner = runnerFunc(func(context.Context, []string) ([]byte, int, error) { return []byte(`{"authenticated":false}`), 1, nil })
	if _, e = c.Probe(context.Background()); !errors.Is(e, errorOf(SignedOut)) {
		t.Fatal(e)
	}
	dir := t.TempDir()
	adapter, e := NewCloudCUA(filepath.Join(dir, "cua"), dir, []string{"FLEETS_TOKEN=private"})
	if e != nil {
		t.Fatal(e)
	}
	env := strings.Join(adapter.runner.(*execRunner).env, " ")
	if !strings.Contains(env, "CUA_FLEET_BASE_URL=https://run.cua.ai") || !strings.Contains(env, "CUA_NO_DAEMON_AUTOSTART=1") {
		t.Fatal("missing controls")
	}
	if _, e = NewCloudCUA(filepath.Join(dir, "cua"), dir, []string{"CUA_FLEET_BASE_URL=http://localhost"}); e == nil {
		t.Fatal("endpoint override")
	}
}

func TestCloudConcurrentReservationsBoundEffects(t *testing.T) {
	m, f, _ := cloudSetup(t)
	f.gate = make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := m.Create(context.Background(), fmt.Sprintf("session-%d", i), cloudProfile())
			results <- e
		}(i)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		count := len(f.remotes)
		f.mu.Unlock()
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reservations never provisioned")
		}
		time.Sleep(time.Millisecond)
	}
	close(f.gate)
	wg.Wait()
	close(results)
	successful, rejected := 0, 0
	for e := range results {
		if e == nil {
			successful++
		} else if errors.Is(e, errorOf(Capacity)) {
			rejected++
		} else {
			t.Fatal(e)
		}
	}
	if successful != 2 || rejected != 6 {
		t.Fatal(successful, rejected)
	}
}
