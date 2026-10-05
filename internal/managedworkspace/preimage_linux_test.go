//go:build linux && amd64

package managedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

const preimageCanary = "API_KEY=preimage-canary\npassword=preimage-password-canary\nJWT-like-token=aaa.bbb.ccc\n-----BEGIN PRIVATE KEY-----\npreimage-private-key-canary\n-----END PRIVATE KEY-----\n"

func preimageFixture(t *testing.T, old string) (context.Context, string, string, *Run, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	base := filepath.Join(dir, "store")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "config.txt"), []byte(old), 0640); err != nil {
		t.Fatal(err)
	}
	r, err := Create(ctx, base, source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	p := workspaceplan.Plan{Version: 1, Kind: "workspace_apply", Operations: []workspaceplan.Operation{{Type: "replace_file", Path: "config.txt", Content: "final\n", Precondition: workspaceplan.Precondition{SHA256: workspaceplan.Hash([]byte(old))}, Validation: workspaceplan.Validation{SHA256: workspaceplan.Hash([]byte("final\n"))}}}, Blockers: []string{}, Assumptions: []string{}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, source, base, r, b
}

func preimagePrepare(t *testing.T, ctx context.Context, r *Run, b []byte) *Proposal {
	t.Helper()
	p, err := r.PrepareWithOptions(ctx, b, ApplyOptions{true, true})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPreimageSuccessPrivacyExportDiscard(t *testing.T) {
	ctx, source, base, r, b := preimageFixture(t, preimageCanary)
	other, err := Create(ctx, base, source)
	if err != nil {
		t.Fatal(err)
	}
	otherID := other.ID()
	other.Close()
	p := preimagePrepare(t, ctx, r, b)
	for _, marker := range strings.Split(strings.TrimSpace(preimageCanary), "\n") {
		if strings.Contains(p.View().Display, marker) {
			t.Fatal("old bytes in preview")
		}
	}
	if !strings.Contains(p.View().Display, "Retenção privada") {
		t.Fatal("preview")
	}
	if _, err := os.Stat(filepath.Join(r.directory, "preimages")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("capture before approval")
	}
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	report, err := permit.Apply(ctx)
	if err != nil || report.Status != "succeeded" || report.Retention == nil || report.Retention.State != "verified" {
		t.Fatal(report, err)
	}
	entries, err := os.ReadDir(filepath.Join(r.directory, "preimages"))
	if err != nil || len(entries) != 2 {
		t.Fatal("storage", err)
	}
	id := preimageID(r.manifest.RunID, workspaceplan.Hash(b), 0)
	stored := read(t, filepath.Join(r.directory, "preimages", id+".bin"))
	if string(stored) != preimageCanary {
		t.Fatal("wrong capture")
	}
	copies := 0
	if err := filepath.WalkDir(r.directory, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data := read(t, name)
		for _, marker := range strings.Split(strings.TrimSpace(preimageCanary), "\n") {
			if bytes.Contains(data, []byte(marker)) {
				if name != filepath.Join(r.directory, "preimages", id+".bin") {
					t.Fatal("unauthorized retained copy", entry.Name())
				}
			}
		}
		if bytes.Equal(data, []byte(preimageCanary)) {
			copies++
		}
		return nil
	}); err != nil || copies != 1 {
		t.Fatal("private retained copies", copies, err)
	}
	for _, name := range []string{"preimages", "preimages/" + id + ".bin", "preimages/" + id + ".json"} {
		i, e := r.root.Lstat(name)
		if e != nil {
			t.Fatal(e)
		}
		mode := os.FileMode(0600)
		if i.IsDir() {
			mode = 0700
		}
		if i.Mode().Perm() != mode {
			t.Fatal("mode")
		}
	}
	for _, name := range []string{"manifest.json", "artifacts/journal.jsonl", "artifacts/preimage-journal.jsonl", "artifacts/report.json", "artifacts/retention-approval.json", "preimages/" + id + ".json"} {
		data := read(t, filepath.Join(r.directory, name))
		for _, marker := range strings.Split(strings.TrimSpace(preimageCanary), "\n") {
			if bytes.Contains(data, []byte(marker)) {
				t.Fatal("metadata leak", name)
			}
		}
	}
	if _, e := r.Report(); e != nil {
		t.Fatal("report integrity", e)
	}
	rID := r.ID()
	r.Close()
	store, err := OpenStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	summary, err := store.Inspect(ctx, rID)
	if err != nil || summary.State != "succeeded" || summary.Retention == nil || summary.Retention.Count != 1 {
		t.Fatal(summary, err)
	}
	exportParent := filepath.Join(filepath.Dir(base), "exports")
	if e := os.Mkdir(exportParent, 0700); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(exportParent, "evidence")
	exp, err := store.PrepareEvidence(ctx, rID, dest, source, true)
	if err != nil {
		t.Fatal("export prepare", err)
	}
	ep, err := exp.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ep.Export(ctx)
	if err != nil || result.State != "exported" {
		t.Fatal(result, err)
	}
	exp.Close()
	files, err := os.ReadDir(dest)
	if err != nil || len(files) != 6 {
		t.Fatal("six files", err)
	}
	for _, f := range files {
		data := read(t, filepath.Join(dest, f.Name()))
		if bytes.Contains(data, []byte("preimages/")) || bytes.Contains(data, []byte(id)) {
			t.Fatal("storage leak")
		}
		for _, m := range strings.Split(strings.TrimSpace(preimageCanary), "\n") {
			if bytes.Contains(data, []byte(m)) {
				t.Fatal("canary export")
			}
		}
	}
	dp, err := store.PrepareDiscard(ctx, rID, true)
	if err != nil {
		s, e := store.Inspect(ctx, rID)
		t.Fatal("discard", s, e, err)
	}
	defer dp.Close()
	dpermit, err := dp.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	state, err := dpermit.Discard(ctx)
	if err != nil || state != "discarded" {
		t.Fatal(state, err)
	}
	if _, e := os.Stat(filepath.Join(base, rID)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("run survives")
	}
	if string(read(t, filepath.Join(source, "config.txt"))) != preimageCanary {
		t.Fatal("source changed")
	}
	osummary, e := store.Inspect(ctx, otherID)
	if e != nil || osummary.State != "ready" {
		t.Fatal("other run changed", e)
	}
	auditFiles := []string{filepath.Join(base, tombstoneDirectory, rID+".jsonl"), filepath.Join(exportParent, evidenceAuditDirectory, exp.View().ID+".jsonl")}
	for _, name := range auditFiles {
		data := read(t, name)
		for _, marker := range strings.Split(strings.TrimSpace(preimageCanary), "\n") {
			if bytes.Contains(data, []byte(marker)) {
				t.Fatal("external audit content leak")
			}
		}
	}

}

func TestPreimageOptInBoundaries(t *testing.T) {
	for _, retain := range []bool{false, true} {
		t.Run("retention="+map[bool]string{false: "off", true: "on"}[retain], func(t *testing.T) {
			ctx, _, _, r, b := preimageFixture(t, "old")
			if retain {
				if _, e := r.PrepareWithOptions(ctx, b, ApplyOptions{true, false}); e == nil {
					t.Fatal("implicit replace")
				}
			}
			p, e := r.Prepare(ctx, b)
			if e != nil {
				t.Fatal(e)
			}
			permit, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			report, e := permit.Apply(ctx)
			if e != nil || report.Retention != nil {
				t.Fatal(e)
			}
			if _, e := os.Stat(filepath.Join(r.directory, "preimages")); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("implicit capture")
			}
		})
	}
	ctx, _, _, r, _ := preimageFixture(t, "old")
	absent := true
	create := workspaceplan.Plan{Version: 1, Kind: "workspace_apply", Operations: []workspaceplan.Operation{{Type: "create_file", Path: "new.txt", Content: "x", Precondition: workspaceplan.Precondition{Absent: &absent}, Validation: workspaceplan.Validation{SHA256: workspaceplan.Hash([]byte("x"))}}}, Blockers: []string{}, Assumptions: []string{}}
	b, _ := json.Marshal(create)
	if _, e := r.PrepareWithOptions(ctx, b, ApplyOptions{true, true}); e == nil {
		t.Fatal("create capture")
	}
}

func TestPreimageEmptyExactAndExcessLimits(t *testing.T) {
	for _, size := range []int{0, MaxFileBytes, MaxFileBytes + 1} {
		t.Run(string(rune('A'+size%3)), func(t *testing.T) {
			if size > MaxFileBytes { // import's stricter bound blocks before retention.
				dir := t.TempDir()
				src := filepath.Join(dir, "source")
				os.Mkdir(src, 0700)
				os.WriteFile(filepath.Join(src, "large"), bytes.Repeat([]byte{'x'}, size), 0600)
				if _, e := Create(context.Background(), filepath.Join(dir, "store"), src); e == nil {
					t.Fatal("oversized accepted")
				}
				return
			}
			ctx, _, _, r, b := preimageFixture(t, strings.Repeat("x", size))
			p := preimagePrepare(t, ctx, r, b)
			permit, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			report, e := permit.Apply(ctx)
			if e != nil || report.Retention.Size != size {
				t.Fatal(report, e)
			}
		})
	}
}

func TestPreimageFailuresBeforeReplace(t *testing.T) {
	for _, phase := range []string{"capture", "mkdir", "open:preimages/", "write:preimages/", "sync:preimages/", "close:preimages/", "directory_sync:preimages/", "directory_close:preimages/", "reopen", "write:artifacts/preimage-journal", "sync:artifacts/preimage-journal", "close:artifacts/preimage-journal", "open:preimages/metadata", "verify", "before_replace"} {
		t.Run(phase, func(t *testing.T) {
			ctx, source, _, r, b := preimageFixture(t, "old")
			p := preimagePrepare(t, ctx, r, b)
			triggered := false
			p.preimageHook = func(s string) error {
				match := strings.HasPrefix(s, phase)
				if phase == "open:preimages/metadata" {
					match = strings.HasPrefix(s, "open:preimages/") && strings.HasSuffix(s, ".json")
				}
				if match {
					triggered = true
					return errors.New("synthetic fault")
				}
				return nil
			}
			permit, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			report, e := permit.Apply(ctx)
			if !triggered || e == nil || report.Status == "succeeded" || (report.Retention != nil && report.Retention.State == "verified") {
				t.Fatal("false success", report, e)
			}
			if string(read(t, filepath.Join(r.output, "config.txt"))) != "old" || string(read(t, filepath.Join(source, "config.txt"))) != "old" {
				t.Fatal("write despite capture failure")
			}
			if _, e := permit.Apply(ctx); !errors.Is(e, editcontract.ErrUsed) {
				t.Fatal("permit reuse")
			}
		})
	}
}

func TestPreimageMutationsFailClosed(t *testing.T) {
	for _, kind := range []string{"target", "target-same-bytes", "capture-truncate", "capture-substitute", "storage-symlink", "target-symlink", "target-hardlink"} {
		t.Run(kind, func(t *testing.T) {
			ctx, _, _, r, b := preimageFixture(t, "old")
			p := preimagePrepare(t, ctx, r, b)
			p.preimageHook = func(phase string) error {
				if phase != "before_replace" {
					return nil
				}
				target := filepath.Join(r.output, "config.txt")
				id := preimageID(r.manifest.RunID, workspaceplan.Hash(b), 0)
				stored := filepath.Join(r.directory, "preimages", id+".bin")
				switch kind {
				case "target":
					return os.WriteFile(target, []byte("changed"), 0600)
				case "target-same-bytes":
					if e := os.Remove(target); e != nil {
						return e
					}
					return os.WriteFile(target, []byte("old"), 0600)
				case "capture-truncate":
					return os.WriteFile(stored, []byte("x"), 0600)
				case "capture-substitute":
					if e := os.Remove(stored); e != nil {
						return e
					}
					return os.WriteFile(stored, []byte("old"), 0600)
				case "storage-symlink":
					if e := os.Rename(filepath.Dir(stored), filepath.Dir(stored)+"-moved"); e != nil {
						return e
					}
					return os.Symlink(filepath.Dir(stored)+"-moved", filepath.Dir(stored))
				case "target-symlink":
					if e := os.Remove(target); e != nil {
						return e
					}
					return os.Symlink(stored, target)
				case "target-hardlink":
					return os.Link(target, filepath.Join(r.output, "alias"))
				}
				return nil
			}
			permit, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			report, e := permit.Apply(ctx)
			if e == nil || report.Status == "succeeded" {
				t.Fatal("mutation accepted", kind)
			}
			if string(read(t, filepath.Join(r.output, "config.txt"))) == "final\n" {
				t.Fatal("replace executed")
			}
		})
	}
}

func TestPreimageCopiedPermitSingleUse(t *testing.T) {
	ctx, _, _, r, b := preimageFixture(t, "old")
	p := preimagePrepare(t, ctx, r, b)
	permit, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	copyPermit := *permit
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, x := range []*Permit{permit, &copyPermit} {
		wg.Add(1)
		go func(p *Permit) { defer wg.Done(); _, e := p.Apply(ctx); results <- e }(x)
	}
	wg.Wait()
	close(results)
	success, used := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, editcontract.ErrUsed) {
			used++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || used != 1 {
		t.Fatal("approval race")
	}
}
