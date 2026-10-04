//go:build linux && amd64

package managedworkspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/netty-linux/daimon/internal/editcontract"
)

func lifecycleFixture(t *testing.T) (context.Context, string, *Run, *Store) {
	t.Helper()
	ctx, source, base, r := fixture(t)
	s, err := OpenStore(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return ctx, source, r, s
}
func TestLifecycleReadOnlyAndDiscard(t *testing.T) {
	ctx, source, r, s := lifecycleFixture(t)
	id := r.ID()
	snapshot := r.Manifest().Snapshot
	other, err := Create(ctx, s.base, source)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	sourceTimes := map[string]int64{}
	if err := filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		sourceTimes[path] = info.ModTime().UnixNano()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := inventoryRun(ctx, s.base, id, r.root)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := s.List(ctx)
	if err != nil || len(summaries) != 2 || summaries[0].RunID > summaries[1].RunID {
		t.Fatal(summaries, err)
	}
	inspected, err := s.Inspect(ctx, id)
	if err != nil || inspected.State != "ready" || !inspected.InventoryVerified {
		t.Fatal(inspected, err)
	}
	if !inspected.ManifestVerified || inspected.JournalVerified || inspected.AuditVerified || inspected.SourceAccess != "none" || inspected.ThreatModel != "owner_uid_and_administrators_trusted" {
		t.Fatal("ambiguous evidence", inspected)
	}
	after, err := inventoryRun(ctx, s.base, id, r.root)
	if err != nil || after.Hash != before.Hash {
		t.Fatal("read-only changed bytes/metadata", err)
	}
	if err := filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if sourceTimes[path] != info.ModTime().UnixNano() {
			t.Fatal("source mtime changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p, err := s.PrepareDiscard(ctx, id, true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, value := range []string{"store=", "run_id=", "run_relative=", "format=", "manifest=", "journal=", "attempt.lock=", "staging=", "files=", "directories=", "total_size_bytes=", "state_sha256=", "limits:", "origem"} {
		if !strings.Contains(p.View().Display, value) {
			t.Fatal("missing preview field", value)
		}
	}
	if strings.Contains(p.View().Display, "PRIVATE_SOURCE_CONTENT") {
		t.Fatal("content in preview")
	}
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	copy := *permit
	state, err := permit.Discard(ctx)
	if err != nil || state != "discarded" {
		t.Fatal(state, err)
	}
	if _, err = copy.Discard(ctx); !errors.Is(err, editcontract.ErrUsed) {
		t.Fatal("copied approval reused", err)
	}
	if _, err = os.Lstat(filepath.Join(s.base, id)); !os.IsNotExist(err) {
		t.Fatal("run survived", err)
	}
	if got, err := s.Inspect(ctx, id); err != nil || got.State != "discarded" {
		t.Fatal(got, err)
	} else if !got.AuditVerified || got.Version != 1 || got.Audited.IsZero() {
		t.Fatal("missing audit evidence", got)
	}
	if got, err := s.Inspect(ctx, strings.Repeat("f", 32)); err != nil || got.State != "missing" {
		t.Fatal(got, err)
	}
	if got, err := s.Inspect(ctx, other.ID()); err != nil || got.State != "ready" {
		t.Fatal("other run touched", got, err)
	}
	assertSource(t, ctx, source, snapshot)
	if got, err := s.List(ctx); err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
}
func TestDiscardDenialEOFDisplayCancellation(t *testing.T) {
	for _, kind := range []string{"flag", "deny", "eof", "display", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, r, s := lifecycleFixture(t)
			snapshot := r.Manifest().Snapshot
			p, err := s.PrepareDiscard(ctx, r.ID(), kind != "flag")
			if kind == "flag" {
				if err == nil {
					t.Fatal("opt-in absent accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			var display bytes.Buffer
			reviewer := editcontract.NewTerminal(strings.NewReader("n\n"), &display)
			if kind == "eof" {
				reviewer = editcontract.NewTerminal(strings.NewReader(""), &display)
			}
			if kind == "display" {
				reviewer = editcontract.NewTerminal(strings.NewReader("y\n"), failDisplay{})
			}
			if kind == "cancel" {
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelCtx
			}
			if _, err = p.Approve(ctx, reviewer); err == nil {
				t.Fatal("unsafe approval")
			}
			if _, err = os.Stat(r.directory); err != nil {
				t.Fatal("denial removed run")
			}
			if _, err = os.Stat(filepath.Join(s.base, tombstoneDirectory)); !os.IsNotExist(err) {
				t.Fatal("denial wrote audit", err)
			}
			if kind != "cancel" {
				assertSource(t, ctx, source, snapshot)
			}
		})
	}
}

type failDisplay struct{}

func (failDisplay) Write([]byte) (int, error) { return 0, errors.New("sensitive untrusted error") }

func TestDiscardChangedTargetAndStore(t *testing.T) {
	for _, kind := range []string{"file", "manifest", "journal", "metadata_version", "root_mode", "lock", "staging", "run_removed", "run_symlink", "store_removed", "store_renamed", "store_swapped"} {
		t.Run(kind, func(t *testing.T) {
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
			switch kind {
			case "file":
				err = os.WriteFile(filepath.Join(r.output, "README.md"), []byte("changed"), 0600)
			case "manifest":
				err = os.WriteFile(filepath.Join(r.directory, "manifest.json"), []byte("null"), 0600)
			case "journal":
				err = os.WriteFile(filepath.Join(r.directory, "artifacts/journal.jsonl"), []byte("changed"), 0600)
			case "metadata_version":
				err = os.Chown(filepath.Join(r.output, "README.md"), -1, os.Getgid())
			case "root_mode":
				err = os.Chmod(r.directory, 0700|os.ModeSetgid)
			case "lock":
				err = os.WriteFile(filepath.Join(r.directory, "attempt.lock"), nil, 0600)
			case "staging":
				err = os.WriteFile(filepath.Join(r.directory, "manifest.next"), nil, 0600)
			case "run_removed":
				err = os.Rename(r.directory, r.directory+"-moved")
			case "run_symlink":
				err = os.Rename(r.directory, r.directory+"-moved")
				if err == nil {
					err = os.Symlink(r.directory+"-moved", r.directory)
				}
			case "store_removed", "store_renamed":
				err = os.Rename(s.base, s.base+"-moved")
			case "store_swapped":
				err = os.Rename(s.base, s.base+"-moved")
				if err == nil {
					err = os.Mkdir(s.base, 0700)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if state, err := permit.Discard(ctx); err == nil || state == "discarded" {
				t.Fatal("changed target accepted", state, err)
			}
		})
	}
}
func TestDiscardAuditFailures(t *testing.T) {
	for _, kind := range []string{"initial_write", "initial_sync", "remove", "final_sync"} {
		t.Run(kind, func(t *testing.T) {
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
			switch kind {
			case "initial_write":
				p.state.auditOpen = func(*os.Root, string) (*os.File, error) { return nil, os.ErrPermission }
			case "initial_sync":
				p.state.auditSync = func(*os.File) error { return errors.New("sync fault") }
			case "remove":
				p.state.remove = func(*os.Root, string) error { return errors.New("remove fault") }
			case "final_sync":
				count := 0
				p.state.auditSync = func(f *os.File) error {
					count++
					if count == 5 {
						return errors.New("terminal sync fault")
					}
					return f.Sync()
				}
			}
			state, err := permit.Discard(ctx)
			if err == nil || state == "discarded" {
				t.Fatal(state, err)
			}
			if kind == "initial_write" || kind == "initial_sync" {
				if _, e := os.Stat(filepath.Join(r.output, "README.md")); e != nil {
					t.Fatal("initial audit failure removed output")
				}
			}
			if kind == "remove" || kind == "final_sync" {
				if state != "unknown_interrupted" {
					t.Fatal("dishonest result", state)
				}
			}
			if kind == "remove" {
				if got, e := s.Inspect(ctx, r.ID()); e != nil || got.State != "unknown_interrupted" || !got.AuditVerified {
					t.Fatal("failed removal not audited", got, e)
				}
			}
		})
	}
}
func TestDiscardConcurrencyAndApplyExclusion(t *testing.T) {
	ctx, _, r, s := lifecycleFixture(t)
	proposal, err := r.Prepare(ctx, plan(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareDiscard(ctx, r.ID(), true); err == nil {
		t.Fatal("discard while apply active")
	}
	if _, err = proposal.Approve(ctx, reviewFunc(func(context.Context, editcontract.Review) (editcontract.Decision, error) {
		return editcontract.Deny, nil
	})); !errors.Is(err, editcontract.ErrDenied) {
		t.Fatal(err)
	}
	r.Close()
	p, err := s.PrepareDiscard(ctx, r.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err = s.PrepareDiscard(ctx, r.ID(), true); err == nil {
		t.Fatal("second discard preparation acquired lock")
	}
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	copy := *permit
	var wg sync.WaitGroup
	wg.Add(2)
	states := make(chan string, 2)
	for _, candidate := range []*DiscardPermit{permit, &copy} {
		go func(candidate *DiscardPermit) { defer wg.Done(); state, _ := candidate.Discard(ctx); states <- state }(candidate)
	}
	wg.Wait()
	close(states)
	successes := 0
	for state := range states {
		if state == "discarded" {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("concurrent reuse", successes)
	}
}
