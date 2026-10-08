//go:build approvalsmoke

package main

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/computer/mediatest"
	"github.com/netty-linux/daimon/internal/sandbox"
	"github.com/netty-linux/daimon/internal/tools"
	"net/http/httptest"
	"os"
	"sync"
	"time"
)

// Test simulation only: lifecycle/scoping/media contracts, not OS isolation.
type sandboxFixture struct {
	cloud   bool
	mu      sync.Mutex
	remotes map[string]sandbox.Remote
	media   *mediatest.Fixture
	servers []*httptest.Server
}

func (f *sandboxFixture) Create(ctx context.Context, r sandbox.CreateRequest) (sandbox.Remote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	placement := r.Profile.EffectivePlacement()
	remote := sandbox.Remote{Ref: string(placement) + ":" + r.Name, Name: r.Name, Runtime: "gvisor", Ready: true}
	if placement == sandbox.Cloud {
		remote.ExpiresAt = time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second).Format(time.RFC3339)
	}
	f.remotes[remote.Ref] = remote
	return remote, nil
}
func (f *sandboxFixture) Get(ctx context.Context, ref string) (sandbox.Remote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.remotes[ref]
	if !ok {
		return sandbox.Remote{}, &sandbox.Error{Kind: sandbox.NotFound}
	}
	return r, nil
}
func (f *sandboxFixture) Delete(ctx context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.remotes, ref)
	return nil
}
func (f *sandboxFixture) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.servers {
		s.Close()
	}
	f.servers = nil
	return nil
}
func (f *sandboxFixture) Probe(context.Context) (sandbox.RuntimeInfo, error) {
	backend := sandbox.CUALocal
	if f.cloud {
		backend = sandbox.CUACloud
	}
	return sandbox.RuntimeInfo{Backend: backend, Runtime: "gvisor", Available: true}, nil
}
func (f *sandboxFixture) Computer(ctx context.Context, r sandbox.Remote, id string) (*computer.Manager, error) {
	backend := computer.CUALocal
	if f.cloud {
		backend = computer.CUACloud
	}
	adapter, e := computer.NewPlacedSandboxAdapter(id, backend, func(ctx context.Context, name string, args json.RawMessage) (tools.ToolResult, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.remotes[r.Ref]; !ok {
			return tools.ToolResult{}, computer.ErrUnavailable
		}
		file, e := os.OpenFile("/fixture/sandbox-actions", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			return tools.ToolResult{}, e
		}
		_, e = file.WriteString("guest-call\n")
		e2 := file.Close()
		if e != nil {
			return tools.ToolResult{}, e
		}
		return tools.ToolResult{Content: "guest action complete"}, e2
	}, "click")
	if e != nil {
		return nil, e
	}
	m, e := computer.NewManager(adapter)
	if e != nil {
		return nil, e
	}
	if f.media != nil {
		daemon := httptest.NewServer(f.media)
		f.mu.Lock()
		f.servers = append(f.servers, daemon)
		f.mu.Unlock()
		media, e := computer.NewCUAMedia(daemon.URL, f.media.Token)
		if e != nil {
			daemon.Close()
			return nil, e
		}
		if e = m.ConfigureMedia(media); e != nil {
			daemon.Close()
			return nil, e
		}
	}
	return m, nil
}
