//go:build linux && amd64

package managedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

func outputFixture(t *testing.T) (context.Context, string, string, *Store, string, string) {
	t.Helper()
	ctx, source, base, r, b := preimageFixture(t, "initial\n")
	if err := os.Chmod(filepath.Dir(base), 0700); err != nil {
		t.Fatal(err)
	}
	canary := "API_KEY=output-export-" + workspaceplan.Hash([]byte(t.TempDir())) + "\npassword=output-password\nJWT-like-token=aaa.bbb.ccc\n-----BEGIN PRIVATE KEY-----\noutput-private-canary\n-----END PRIVATE KEY-----\n"
	var plan workspaceplan.Plan
	if json.Unmarshal(b, &plan) != nil {
		t.Fatal("plan")
	}
	plan.Operations[0].Content = canary
	plan.Operations[0].Validation.SHA256 = workspaceplan.Hash([]byte(canary))
	b, _ = json.Marshal(plan)
	p, e := r.Prepare(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	permit, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	report, e := permit.Apply(ctx)
	if e != nil || report.Status != "succeeded" {
		t.Fatal(report, e)
	}
	id := r.ID()
	r.Close()
	s, e := OpenStore(base)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return ctx, source, base, s, id, canary
}
func outputPrepare(t *testing.T, ctx context.Context, s *Store, id, source string) *OutputProposal {
	t.Helper()
	p, e := s.PrepareOutput(ctx, id, "config.txt", "review-one", source, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestOutputExportSuccessPrivacyAndLegacy(t *testing.T) {
	ctx, source, base, s, id, canary := outputFixture(t)
	other, e := Create(ctx, base, source)
	if e != nil {
		t.Fatal(e)
	}
	otherID := other.ID()
	other.Close()
	controlBefore, e := s.Inspect(ctx, otherID)
	if e != nil {
		t.Fatal(e)
	}
	r, e := Open(base, id)
	if e != nil {
		t.Fatal(e)
	}
	before, e := inventoryRun(ctx, base, id, r.root)
	if e != nil {
		t.Fatal(e)
	}
	r.Close()
	p := outputPrepare(t, ctx, s, id, source)
	d := p.state
	for _, m := range strings.Split(strings.TrimSpace(canary), "\n") {
		if strings.Contains(p.View().Display, m) {
			t.Fatal("preview bytes")
		}
	}
	if _, e = os.Stat(d.areaPath); !os.IsNotExist(e) {
		t.Fatal("prepare provisioned")
	}
	permit, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	var phases []string
	d.hook = func(phase string) error { phases = append(phases, phase); return nil }
	result, e := permit.Export(ctx)
	if e != nil || result.State != "exported" {
		t.Fatal(result, e, phases)
	}
	dest := filepath.Join(d.areaPath, "review-one")
	if !bytes.Equal(read(t, filepath.Join(dest, "output.bin")), []byte(canary)) {
		t.Fatal("bytes")
	}
	for _, pair := range []struct {
		p    string
		mode fs.FileMode
	}{{d.areaPath, 0700}, {dest, 0700}, {filepath.Join(dest, "output.bin"), 0600}, {filepath.Join(dest, "manifest.json"), 0600}} {
		i, e := os.Lstat(pair.p)
		if e != nil || i.Mode().Perm() != pair.mode {
			t.Fatal("permissions")
		}
	}
	entries, e := os.ReadDir(dest)
	if e != nil || len(entries) != 2 {
		t.Fatal("package")
	}
	copies := 0
	if e = filepath.WalkDir(d.areaPath, func(path string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		data := read(t, path)
		for _, m := range strings.Split(strings.TrimSpace(canary), "\n") {
			if bytes.Contains(data, []byte(m)) && path != filepath.Join(dest, "output.bin") {
				t.Fatal("disclosure outside content file")
			}
		}
		if bytes.Equal(data, []byte(canary)) {
			copies++
		}
		return nil
	}); e != nil || copies != 1 {
		t.Fatal("copies", e)
	}
	r, e = Open(base, id)
	if e != nil {
		t.Fatal(e)
	}
	after, e := inventoryRun(ctx, base, id, r.root)
	r.Close()
	if e != nil || after.Hash != before.Hash {
		t.Fatal("run changed")
	}
	if string(read(t, filepath.Join(source, "config.txt"))) != "initial\n" {
		t.Fatal("source changed")
	}
	summary, e := s.Inspect(ctx, otherID)
	if e != nil || summary.State != "ready" || summary.InventorySHA256 != controlBefore.InventorySHA256 {
		t.Fatal("control changed")
	}
	for _, name := range []string{"manifest.json", "artifacts/journal.jsonl", "artifacts/report.json"} {
		metadata := read(t, filepath.Join(base, id, name))
		for _, marker := range strings.Split(strings.TrimSpace(canary), "\n") {
			if bytes.Contains(metadata, []byte(marker)) {
				t.Fatal("run metadata disclosure")
			}
		}
	}
	parent := filepath.Join(filepath.Dir(base), "evidence-review")
	if os.Mkdir(parent, 0700) != nil {
		t.Fatal("parent")
	}
	ep, e := s.PrepareEvidence(ctx, id, filepath.Join(parent, "evidence"), source, true)
	if e != nil {
		t.Fatal(e)
	}
	approved, e := ep.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	er, e := approved.Export(ctx)
	if e != nil || er.Files != 6 {
		t.Fatal(er, e)
	}
	ep.Close()
	for _, name := range evidencePaths {
		data := read(t, filepath.Join(parent, "evidence", name))
		for _, m := range strings.Split(strings.TrimSpace(canary), "\n") {
			if bytes.Contains(data, []byte(m)) {
				t.Fatal("evidence leak")
			}
		}
	}
	dp, e := s.PrepareDiscard(ctx, id, true)
	if e != nil {
		t.Fatal(e)
	}
	defer dp.Close()
	dpmt, e := dp.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	if state, e := dpmt.Discard(ctx); e != nil || state != "discarded" {
		t.Fatal(state, e)
	}
	if !bytes.Equal(read(t, filepath.Join(dest, "output.bin")), []byte(canary)) {
		t.Fatal("discard changed export")
	}
	tomb := read(t, filepath.Join(base, tombstoneDirectory, id+".jsonl"))
	for _, m := range strings.Split(strings.TrimSpace(canary), "\n") {
		if bytes.Contains(tomb, []byte(m)) {
			t.Fatal("tombstone leak")
		}
	}
	if _, e = s.PrepareOutput(ctx, id, "config.txt", "second", source, true); e == nil {
		t.Fatal("discarded eligible")
	}
}

func TestOutputExportRejectedInputs(t *testing.T) {
	for _, tc := range []struct {
		name, path, dest string
		enabled          bool
	}{{"disabled", "config.txt", "valid", false}, {"traversal", "../manifest.json", "valid", true}, {"absolute", "/etc/passwd", "valid", true}, {"preimage", "../preimages/x.bin", "valid", true}, {"control", "../artifacts/report.json", "valid", true}, {"directory", ".", "valid", true}, {"backslash", "..\\x", "valid", true}, {"destination-path", "config.txt", "/tmp/free", true}, {"destination-dot", "config.txt", "..", true}, {"destination-nested", "config.txt", "one/two", true}, {"destination-unicode", "config.txt", "ｒｅｖｉｅｗ", true}, {"missing", "missing.txt", "valid", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, source, _, s, id, _ := outputFixture(t)
			p, e := s.PrepareOutput(ctx, id, tc.path, tc.dest, source, tc.enabled)
			if e == nil {
				p.Close()
				t.Fatal("allowed")
			}
		})
	}
}

func TestOutputExportEligibilityAndArtifacts(t *testing.T) {
	for _, kind := range []string{"ready", "partial", "unknown", "unknown_interrupted", "invalid", "missing", "report", "manifest", "journal", "plan", "lock", "staging", "output", "symlink", "hardlink", "fifo", "source-check", "source-swap"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, base, s, id, _ := outputFixture(t)
			run := filepath.Join(base, id)
			target := filepath.Join(run, "output", "config.txt")
			switch kind {
			case "missing":
				id = "00000000000000000000000000000000"
			case "report", "manifest", "journal":
				name := map[string]string{"report": "artifacts/report.json", "manifest": "manifest.json", "journal": "artifacts/journal.jsonl"}[kind]
				if os.WriteFile(filepath.Join(run, name), []byte("{}\n"), 0600) != nil {
					t.Fatal("mutation")
				}
			case "plan", "lock":
				name := "artifacts/approved-plan.json"
				if kind == "lock" {
					name = "attempt.lock"
				}
				if err := os.WriteFile(filepath.Join(run, name), []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "staging":
				if err := os.WriteFile(filepath.Join(run, "manifest.next"), []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "output":
				os.WriteFile(target, []byte("changed"), 0600)
			case "symlink", "hardlink", "fifo":
				if os.Remove(target) != nil {
					t.Fatal("remove")
				}
				switch kind {
				case "symlink":
					if os.Symlink(filepath.Join(source, "config.txt"), target) != nil {
						t.Fatal("symlink")
					}
				case "hardlink":
					if os.Link(filepath.Join(source, "config.txt"), target) != nil {
						t.Fatal("link")
					}
				case "fifo":
					if syscall.Mkfifo(target, 0600) != nil {
						t.Fatal("fifo")
					}
				}
			case "source-check":
				source = filepath.Dir(source)
			case "source-swap":
				if os.Rename(source, source+"-old") != nil || os.Mkdir(source, 0700) != nil {
					t.Fatal("source swap")
				}
			default:
				data := read(t, filepath.Join(run, "manifest.json"))
				var m Manifest
				if json.Unmarshal(data, &m) != nil {
					t.Fatal("decode")
				}
				m.Status = kind
				data, _ = json.Marshal(m)
				os.WriteFile(filepath.Join(run, "manifest.json"), data, 0600)
			}
			p, e := s.PrepareOutput(ctx, id, "config.txt", "review-one", source, true)
			// Before preview, source identity has not been approved; a replacement with
			// the same path is legitimate structurally. The after-preview swap is below.
			if kind == "source-swap" {
				if e == nil {
					p.Close()
				}
				return
			}
			if e == nil {
				p.Close()
				t.Fatal("eligible", kind)
			}
		})
	}
}

func TestOutputExportApprovalAndCopies(t *testing.T) {
	for _, kind := range []string{"deny", "EOF", "display", "cancel", "nil"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, _, s, id, _ := outputFixture(t)
			p := outputPrepare(t, ctx, s, id, source)
			var reviewer editcontract.Reviewer
			switch kind {
			case "deny":
				reviewer = reviewFunc(func(context.Context, editcontract.Review) (editcontract.Decision, error) {
					return editcontract.Deny, nil
				})
			case "EOF":
				reviewer = editcontract.NewTerminal(strings.NewReader(""), &bytes.Buffer{})
			case "display":
				reviewer = reviewFunc(func(context.Context, editcontract.Review) (editcontract.Decision, error) {
					return editcontract.Allow, os.ErrPermission
				})
			case "cancel":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
				reviewer = allow
			}
			if _, e := p.Approve(ctx, reviewer); e == nil {
				t.Fatal("approved")
			}
			if _, e := os.Stat(p.state.areaPath); !os.IsNotExist(e) {
				t.Fatal("provisioned")
			}
		})
	}
	ctx, source, _, s, id, _ := outputFixture(t)
	p := outputPrepare(t, ctx, s, id, source)
	permit, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	copied := *permit
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, pm := range []*OutputPermit{permit, &copied} {
		wg.Add(1)
		go func(pm *OutputPermit) { defer wg.Done(); _, e := pm.Export(ctx); results <- e }(pm)
	}
	wg.Wait()
	close(results)
	success, used := 0, 0
	for e := range results {
		if e == nil {
			success++
		}
		if errors.Is(e, editcontract.ErrUsed) {
			used++
		}
	}
	if success != 1 || used != 1 {
		t.Fatal(success, used)
	}
	if _, e = p.Approve(ctx, allow); !errors.Is(e, editcontract.ErrUsed) {
		t.Fatal("proposal reused")
	}
}

func TestOutputExportFailuresAndMutations(t *testing.T) {
	phases := []string{"provision", "audit:started", "sync_audit:started", "sync_audit_directory", "open:output.bin", "write:output.bin", "sync:output.bin", "close:output.bin", "write:manifest.json", "verify_staging", "before_publish", "after_publish", "sync_published_parent", "audit:exported", "sync_audit:exported", "final_close", "staging-hash", "stage-directory-swap", "audit-tamper", "output-after-preview", "destination-existing", "destination-symlink", "source-after-preview", "store-after-preview", "run-after-preview", "overlap", "parent-symlink", "cancel"}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			ctx, source, base, s, id, _ := outputFixture(t)
			p := outputPrepare(t, ctx, s, id, source)
			d := p.state
			permit, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			hit := false
			d.hook = func(at string) error {
				if at == phase {
					hit = true
					return os.ErrPermission
				}
				if at != "before_publish" {
					return nil
				}
				hit = true
				switch phase {
				case "stage-directory-swap":
					stage := filepath.Join(d.areaPath, ".staging-"+d.manifest.ExportID)
					if e := os.Rename(stage, stage+"-old"); e != nil {
						return e
					}
					if e := os.Mkdir(stage, 0700); e != nil {
						return e
					}
					if e := os.WriteFile(filepath.Join(stage, "manifest.json"), d.manifestBytes, 0600); e != nil {
						return e
					}
					return os.WriteFile(filepath.Join(stage, "output.bin"), []byte("fake"), 0600)
				case "staging-hash":
					return os.WriteFile(filepath.Join(d.areaPath, ".staging-"+d.manifest.ExportID, "output.bin"), []byte("changed"), 0600)
				case "audit-tamper":
					return os.WriteFile(filepath.Join(d.areaPath, "_audit", d.manifest.ExportID+".jsonl"), []byte("{}\n"), 0600)
				case "output-after-preview":
					return os.WriteFile(filepath.Join(base, id, "output", "config.txt"), []byte("changed"), 0600)
				case "destination-existing":
					return os.Mkdir(filepath.Join(d.areaPath, "review-one"), 0700)
				case "destination-symlink":
					return os.Symlink(source, filepath.Join(d.areaPath, "review-one"))
				case "source-after-preview":
					if e := os.Rename(source, source+"-old"); e != nil {
						return e
					}
					return os.Mkdir(source, 0700)
				case "store-after-preview":
					return os.Rename(base, base+"-old")
				case "run-after-preview":
					return os.Rename(filepath.Join(base, id), filepath.Join(base, id+"-old"))
				case "overlap":
					d.source = d.areaPath
					return nil
				case "parent-symlink":
					if e := os.Rename(d.areaPath, d.areaPath+"-old"); e != nil {
						return e
					}
					return os.Symlink(d.areaPath+"-old", d.areaPath)
				case "cancel":
					return context.Canceled
				}
				return nil
			}
			result, e := permit.Export(ctx)
			if e == nil || result.State == "exported" || !hit {
				t.Fatal(phase, result, e, hit)
			}
			if phase == "after_publish" || phase == "sync_published_parent" || phase == "audit:exported" || phase == "sync_audit:exported" || phase == "final_close" {
				if result.State != "unknown_interrupted" {
					t.Fatal("false no effect")
				}
				if _, e := os.Stat(filepath.Join(d.areaPath, "review-one", "output.bin")); e != nil {
					t.Fatal("lost confirmed file")
				}
			}
			if _, e = permit.Export(ctx); !errors.Is(e, editcontract.ErrUsed) {
				t.Fatal("retry")
			}
		})
	}
}

func TestOutputExportExistingAreaDestinationAndConcurrentPrepare(t *testing.T) {
	ctx, source, _, s, id, _ := outputFixture(t)
	p := outputPrepare(t, ctx, s, id, source)
	if other, e := s.PrepareOutput(ctx, id, "config.txt", "another", source, true); e == nil {
		other.Close()
		t.Fatal("concurrent prepare")
	}
	permit, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = permit.Export(ctx); e != nil {
		t.Fatal(e)
	}
	if other, e := s.PrepareOutput(ctx, id, "config.txt", "review-one", source, true); e == nil {
		other.Close()
		t.Fatal("overwrite")
	}
	p2, e := s.PrepareOutput(ctx, id, "config.txt", "second", source, true)
	if e != nil {
		t.Fatal(e)
	}
	defer p2.Close()
	permit, e = p2.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = permit.Export(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestOutputExportApprovalBindingAndDeadline(t *testing.T) {
	for _, field := range []string{"run", "path", "hash", "destination", "size"} {
		t.Run(field, func(t *testing.T) {
			ctx, source, _, s, id, _ := outputFixture(t)
			p := outputPrepare(t, ctx, s, id, source)
			permit, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			switch field {
			case "run":
				p.state.manifest.RunID = "00000000000000000000000000000000"
			case "path":
				p.state.manifest.Path = "other.txt"
			case "hash":
				p.state.manifest.SHA256 = strings.Repeat("a", 64)
			case "destination":
				p.state.manifest.Destination = "other"
			case "size":
				p.state.manifest.Bytes++
			}
			if result, e := permit.Export(ctx); e == nil || result.State == "exported" {
				t.Fatal("binding not checked", result, e)
			}
		})
	}
	ctx, source, _, s, id, _ := outputFixture(t)
	child, cancel := context.WithCancel(ctx)
	p := outputPrepare(t, child, s, id, source)
	permit, e := p.Approve(child, allow)
	if e != nil {
		t.Fatal(e)
	}
	p.state.hook = func(phase string) error {
		if phase == "write:output.bin" {
			cancel()
		}
		return nil
	}
	result, e := permit.Export(ctx)
	if !errors.Is(e, context.Canceled) || result.State != "not_published" {
		t.Fatal("cancellation", result, e)
	}
}

func TestOutputExportByteLimits(t *testing.T) {
	for _, size := range []int{0, MaxFileBytes, MaxFileBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ctx, source, base, r, b := preimageFixture(t, "initial\n")
			if os.Chmod(filepath.Dir(base), 0700) != nil {
				t.Fatal("parent")
			}
			var plan workspaceplan.Plan
			json.Unmarshal(b, &plan)
			plan.Operations[0].Content = strings.Repeat("x", size)
			plan.Operations[0].Validation.SHA256 = workspaceplan.Hash([]byte(plan.Operations[0].Content))
			b, _ = json.Marshal(plan)
			proposal, e := r.Prepare(ctx, b)
			if size > MaxFileBytes {
				if e == nil {
					t.Fatal("excess approved")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			pm, e := proposal.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = pm.Apply(ctx); e != nil {
				t.Fatal(e)
			}
			id := r.ID()
			r.Close()
			s, e := OpenStore(base)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			p := outputPrepare(t, ctx, s, id, source)
			permit, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			result, e := permit.Export(ctx)
			if e != nil || result.Bytes != size {
				t.Fatal(result, e)
			}
		})
	}
}

func TestOutputExportCancellationDuringFinalClose(t *testing.T) {
	ctx, source, _, s, id, canary := outputFixture(t)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := outputPrepare(t, ctx, s, id, source)
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	p.state.hook = func(phase string) error {
		if phase == "handles_closed" {
			hit = true
			cancel()
		}
		return nil
	}
	result, err := permit.Export(ctx)
	if !hit || !errors.Is(err, context.Canceled) || result.State != "unknown_interrupted" || result.Files != 0 {
		t.Fatal("late cancellation reported as success", result, err, hit)
	}
	if string(read(t, filepath.Join(p.state.areaPath, "review-one", "output.bin"))) != canary {
		t.Fatal("confirmed publication lost")
	}
	if _, err := permit.Export(ctx); !errors.Is(err, editcontract.ErrUsed) {
		t.Fatal("permit reused", err)
	}
}

func TestOutputExportActualHandleCloseFailure(t *testing.T) {
	ctx, source, _, s, id, canary := outputFixture(t)
	p := outputPrepare(t, ctx, s, id, source)
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	p.state.hook = func(phase string) error {
		if phase == "final_close" {
			hit = true
			// Cause the executor's real Close call to fail; do not return a
			// synthetic error from this hook.
			if err := p.state.runLock.Close(); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	result, err := permit.Export(ctx)
	if !hit || !errors.Is(err, ErrArtifact) || result.State != "unknown_interrupted" || result.Files != 0 {
		t.Fatal("real close failure reported as success", result, err, hit)
	}
	if string(read(t, filepath.Join(p.state.areaPath, "review-one", "output.bin"))) != canary {
		t.Fatal("confirmed publication lost")
	}
}

func TestOutputExportSourceCheckDoesNotReadBytes(t *testing.T) {
	ctx, source, _, s, id, canary := outputFixture(t)
	file := filepath.Join(source, "config.txt")
	if err := os.Chmod(file, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(file, 0600) })
	if os.Geteuid() != 0 {
		if _, err := os.ReadFile(file); !errors.Is(err, os.ErrPermission) {
			t.Fatal("fixture is not unreadable", err)
		}
	}
	p := outputPrepare(t, ctx, s, id, source)
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := permit.Export(ctx)
	if err != nil || result.State != "exported" {
		t.Fatal(result, err)
	}
	if string(read(t, filepath.Join(p.state.areaPath, "review-one", "output.bin"))) != canary {
		t.Fatal("source used as fallback")
	}
}
