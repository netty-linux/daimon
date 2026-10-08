//go:build approvalsmoke

package main

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/environments"
	"github.com/netty-linux/daimon/internal/sandbox"
	"sync"
)

var environmentFixtureMu sync.Mutex
var environmentFixtureCurrent *environmentFixture

type environmentFixture struct {
	mu       sync.Mutex
	w        environments.Workspace
	failSync bool
}

func (f *environmentFixture) Hydrate(ctx context.Context, w environments.Workspace) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	f.w = environments.Clone(w)
	return nil
}
func (f *environmentFixture) Export(ctx context.Context) (environments.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.failSync {
		return nil, errors.New("private simulated sync")
	}
	return environments.Clone(f.w), nil
}
func (f *sandboxFixture) Workspace(ctx context.Context, r sandbox.Remote) (environments.Transfer, error) {
	f.mu.Lock()
	_, ok := f.remotes[r.Ref]
	f.mu.Unlock()
	if !ok || ctx.Err() != nil {
		return nil, environments.ErrTransfer
	}
	workspace := &environmentFixture{}
	environmentFixtureMu.Lock()
	environmentFixtureCurrent = workspace
	environmentFixtureMu.Unlock()
	return workspace, nil
}
func currentEnvironmentFixture() *environmentFixture {
	environmentFixtureMu.Lock()
	defer environmentFixtureMu.Unlock()
	return environmentFixtureCurrent
}
