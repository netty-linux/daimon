//go:build linux && amd64

package managedworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/netty-linux/daimon/internal/editcontract"
)

func TestDiscardUnsafeInventory(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "permissions", "invalid_utf8", "limit_entries", "limit_file", "limit_bytes"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, r, s := lifecycleFixture(t)
			snapshot := r.Manifest().Snapshot
			path := filepath.Join(r.directory, "artifacts/extra")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(filepath.Join(source, "README.md"), path)
			case "hardlink":
				err = os.Link(filepath.Join(r.output, "README.md"), path)
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			case "permissions":
				err = os.WriteFile(path, nil, 0644)
			case "invalid_utf8":
				err = os.WriteFile(path+string([]byte{0xff}), nil, 0600)
			case "limit_file":
				err = os.WriteFile(path, make([]byte, MaxPreviewBytes+1), 0600)
			case "limit_bytes":
				for i := 0; i < 5; i++ {
					err = os.WriteFile(path+strings.Repeat("x", i+1), make([]byte, MaxPreviewBytes), 0600)
					if err != nil {
						break
					}
				}
			case "limit_entries":
				for i := 0; i < MaxRunEntries; i++ {
					err = os.WriteFile(path+strings.Repeat("x", i+1), nil, 0600)
					if err != nil {
						break
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if p, err := s.PrepareDiscard(ctx, r.ID(), true); err == nil {
				p.Close()
				t.Fatal("unsafe inventory accepted")
			}
			assertSource(t, ctx, source, snapshot)
		})
	}
}
func TestDiscardCancellationAfterApprovalAndDuringRemoval(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "during"}[during], func(t *testing.T) {
			ctx, source, r, s := lifecycleFixture(t)
			snapshot := r.Manifest().Snapshot
			cancelCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			p, err := s.PrepareDiscard(cancelCtx, r.ID(), true)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			permit, err := p.Approve(ctx, allow)
			if err != nil {
				t.Fatal(err)
			}
			if during {
				calls := 0
				p.state.remove = func(root *os.Root, path string) error {
					calls++
					err := root.Remove(path)
					if calls == 1 {
						cancel()
					}
					return err
				}
			} else {
				cancel()
			}
			state, err := permit.Discard(ctx)
			if !errors.Is(err, context.Canceled) || state == "discarded" {
				t.Fatal(state, err)
			}
			if during {
				got, err := s.Inspect(ctx, r.ID())
				if err != nil || got.State != "unknown_interrupted" {
					t.Fatal(got, err)
				}
			} else {
				if _, err = os.Stat(filepath.Join(r.output, "README.md")); err != nil {
					t.Fatal("cancel removed")
				}
			}
			assertSource(t, ctx, source, snapshot)
		})
	}
}
func TestDiscardDirectoryMovedDuringRemoval(t *testing.T) {
	ctx, source, r, s := lifecycleFixture(t)
	snapshot := r.Manifest().Snapshot
	p, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(filepath.Dir(s.base), "moved-run")
	calls := 0
	p.state.remove = func(root *os.Root, path string) error {
		calls++
		err := root.Remove(path)
		if calls == 1 && err == nil {
			err = os.Rename(r.directory, moved)
		}
		return err
	}
	state, err := permit.Discard(ctx)
	if err == nil || state != "unknown_interrupted" || calls != 1 {
		t.Fatal("continued after identity change", state, err, calls)
	}
	if _, err = os.Stat(filepath.Join(moved, "manifest.json")); err != nil {
		t.Fatal("later entries deleted outside store")
	}
	assertSource(t, ctx, source, snapshot)
}
func TestDiscardDetachedReviewAndCopiedProposal(t *testing.T) {
	ctx, _, r, s := lifecycleFixture(t)
	p, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	copy := *p
	view := p.View()
	view.RunID = "outside"
	view.Display = "changed"
	if _, err = p.Approve(ctx, reviewFunc(func(_ context.Context, actual editcontract.Review) (editcontract.Decision, error) {
		if actual.RunID != r.ID() || actual.Display == view.Display {
			t.Fatal("mutable binding")
		}
		return editcontract.Deny, nil
	})); !errors.Is(err, editcontract.ErrDenied) {
		t.Fatal(err)
	}
	if _, err = copy.Approve(ctx, allow); !errors.Is(err, editcontract.ErrUsed) {
		t.Fatal("copied proposal reused", err)
	}
}

func TestDiscardApprovalContextCannotBeExtended(t *testing.T) {
	ctx, _, r, s := lifecycleFixture(t)
	p, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	approvalCtx, cancel := context.WithCancel(ctx)
	permit, err := p.Approve(approvalCtx, allow)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if _, err = permit.Discard(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("approval cancellation extended", err)
	}
	if _, err = os.Stat(filepath.Join(r.output, "README.md")); err != nil {
		t.Fatal("cancelled approval removed")
	}
}

func TestDiscardAuditTamperedBeforeRemoval(t *testing.T) {
	ctx, _, r, s := lifecycleFixture(t)
	p, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	p.state.auditSync = func(f *os.File) error {
		count++
		if count == 3 {
			if err := os.WriteFile(filepath.Join(s.base, tombstoneDirectory, r.ID()+".jsonl"), []byte("null\n"), 0600); err != nil {
				return err
			}
		}
		return f.Sync()
	}
	if state, err := permit.Discard(ctx); err == nil || state == "discarded" {
		t.Fatal(state, err)
	}
	if _, err = os.Stat(filepath.Join(r.output, "README.md")); err != nil {
		t.Fatal("tampered audit allowed deletion")
	}
}

func TestDiscardParentSymlinkDuringRemoval(t *testing.T) {
	ctx, source, r, s := lifecycleFixture(t)
	snapshot := r.Manifest().Snapshot
	p, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.state.remove = func(root *os.Root, path string) error {
		calls++
		if err := root.Remove(path); err != nil {
			return err
		}
		if calls == 1 {
			parent := filepath.Join(r.output, "src")
			if err := os.Rename(parent, parent+"-moved"); err != nil {
				return err
			}
			return os.Symlink(filepath.Join(source, "src"), parent)
		}
		return nil
	}
	if state, err := permit.Discard(ctx); err == nil || state != "unknown_interrupted" || calls != 1 {
		t.Fatal("continued across symlink", state, err, calls)
	}
	assertSource(t, ctx, source, snapshot)
}

func TestDiscardFinalSyncCancellationIsNotSuccess(t *testing.T) {
	ctx, _, r, s := lifecycleFixture(t)
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	p, err := s.PrepareDiscard(cancelCtx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	p.state.auditSync = func(f *os.File) error {
		count++
		err := f.Sync()
		if count == 5 {
			cancel()
		}
		return err
	}
	if state, err := permit.Discard(ctx); !errors.Is(err, context.Canceled) || state != "unknown_interrupted" {
		t.Fatal("late success", state, err)
	}
}

func TestDiscardFinalCloseFailureIsNotSuccess(t *testing.T) {
	ctx, _, r, s := lifecycleFixture(t)
	p, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	p.state.auditSync = func(f *os.File) error {
		count++
		if err := f.Sync(); err != nil {
			return err
		}
		if count == 5 {
			return f.Close()
		}
		return nil
	}
	if state, err := permit.Discard(ctx); !errors.Is(err, ErrArtifact) || state != "unknown_interrupted" {
		t.Fatal("close failure presented as success", state, err)
	}
}

func TestDiscardRunHandleCloseFailureIsNotSuccess(t *testing.T) {
	ctx, _, r, s := lifecycleFixture(t)
	p, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	p.state.auditSync = func(f *os.File) error {
		count++
		if err := f.Sync(); err != nil {
			return err
		}
		if count == 5 {
			return p.state.active.Close()
		}
		return nil
	}
	if state, err := permit.Discard(ctx); !errors.Is(err, ErrArtifact) || state != "unknown_interrupted" {
		t.Fatal("run handle close failure presented as success", state, err)
	}
}

func TestDiscardInitialAuditSyncStagesPreventRemoval(t *testing.T) {
	for stage := 1; stage <= 3; stage++ {
		t.Run(string(rune('0'+stage)), func(t *testing.T) {
			ctx, source, r, s := lifecycleFixture(t)
			snapshot := r.Manifest().Snapshot
			p, err := s.PrepareDiscard(ctx, r.ID(), true)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			permit, err := p.Approve(ctx, allow)
			if err != nil {
				t.Fatal(err)
			}
			count, removals := 0, 0
			p.state.auditSync = func(f *os.File) error {
				count++
				if count == stage {
					return errors.New("initial sync fault")
				}
				return f.Sync()
			}
			p.state.remove = func(*os.Root, string) error { removals++; return nil }
			if state, err := permit.Discard(ctx); !errors.Is(err, ErrArtifact) || state == "discarded" || removals != 0 {
				t.Fatal("initial audit failure allowed removal", state, err, removals)
			}
			if _, err := os.Stat(filepath.Join(r.output, "README.md")); err != nil {
				t.Fatal(err)
			}
			assertSource(t, ctx, source, snapshot)
		})
	}
}

func TestLifecycleInterruptedDiscardWithoutManifest(t *testing.T) {
	ctx, source, r, s := lifecycleFixture(t)
	snapshot := r.Manifest().Snapshot
	p, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	p.state.remove = func(root *os.Root, path string) error {
		if err := root.Remove(path); err != nil {
			return err
		}
		if path == "manifest.json" {
			return errors.New("fault after manifest removal")
		}
		return nil
	}
	if state, err := permit.Discard(ctx); err == nil || state != "unknown_interrupted" {
		t.Fatal(state, err)
	}
	got, err := s.Inspect(ctx, r.ID())
	if err != nil || got.State != "unknown_interrupted" || !got.AuditVerified || got.ManifestVerified || got.InventoryVerified {
		t.Fatal("interrupted discard misclassified", got, err)
	}
	if _, err = s.PrepareDiscard(ctx, r.ID(), true); err == nil {
		t.Fatal("interrupted discard restarted")
	}
	assertSource(t, ctx, source, snapshot)
}

func TestLifecycleListWhileRunIsRemoved(t *testing.T) {
	ctx, _, r, s := lifecycleFixture(t)
	p, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	proceed := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(proceed) })
	calls := 0
	p.state.remove = func(root *os.Root, path string) error {
		calls++
		err := root.Remove(path)
		if calls == 1 && err == nil {
			close(started)
			<-proceed
		}
		return err
	}
	result := make(chan error, 1)
	go func() { _, err := permit.Discard(ctx); result <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	summaries, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].State != "unknown_interrupted" {
		t.Fatal("partial removal shown intact", summaries)
	}
	release.Do(func() { close(proceed) })
	select {
	case err = <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	got, err := s.Inspect(ctx, r.ID())
	if err != nil || got.State != "discarded" {
		t.Fatal(got, err)
	}
}
func TestLifecyclePartialAndInterruptedCannotDiscard(t *testing.T) {
	ctx, _, r, s := lifecycleFixture(t)
	p, err := r.Prepare(ctx, plan(t))
	if err != nil {
		t.Fatal(err)
	}
	if summary, err := s.Inspect(ctx, r.ID()); err != nil || summary.State != "unknown_interrupted" {
		t.Fatal(summary, err)
	}
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	permit.applyOne = func(ctx context.Context, i int) error {
		if i == 1 {
			return ErrApply
		}
		return permit.applyPrepared(ctx, i)
	}
	if report, err := permit.Apply(ctx); err == nil || report.Status != "partial" {
		t.Fatal(report, err)
	}
	r.Close()
	if summary, err := s.Inspect(ctx, r.ID()); err != nil || summary.State != "partial" {
		t.Fatal(summary, err)
	}
	discard, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	discard.Close()
	if err = os.WriteFile(filepath.Join(r.directory, "manifest.next"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareDiscard(ctx, r.ID(), true); err == nil {
		t.Fatal("interrupted discarded")
	}
}
