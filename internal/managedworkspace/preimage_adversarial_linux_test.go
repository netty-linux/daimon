//go:build linux && amd64

package managedworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

func TestPreimagePreviewTargetChangeAndLimits(t *testing.T) {
	for _, kind := range []string{"changed", "hash", "lines", "duplicate", "traversal"} {
		t.Run(kind, func(t *testing.T) {
			old := "old"
			if kind == "lines" {
				old = strings.Repeat("x\n", 1001)
			}
			ctx, _, _, r, b := preimageFixture(t, old)
			if kind == "hash" || kind == "duplicate" || kind == "traversal" {
				var plan workspaceplan.Plan
				json.Unmarshal(b, &plan)
				if kind == "hash" {
					plan.Operations[0].Precondition.SHA256 = strings.Repeat("a", 64)
				}
				if kind == "duplicate" {
					plan.Operations = append(plan.Operations, plan.Operations[0])
				}
				if kind == "traversal" {
					plan.Operations[0].Path = "../preimages/escape"
				}
				b, _ = json.Marshal(plan)
			}
			p, e := r.PrepareWithOptions(ctx, b, ApplyOptions{true, true})
			if kind != "changed" {
				if e == nil {
					t.Fatal("invalid proposal allowed")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			permit, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(r.output, "config.txt"), []byte("changed"), 0600); e != nil {
				t.Fatal(e)
			}
			if report, e := permit.Apply(ctx); e == nil || report.Status == "succeeded" {
				t.Fatal("preview mismatch applied")
			}
			if _, e := os.Stat(filepath.Join(r.directory, "preimages")); !os.IsNotExist(e) {
				t.Fatal("captured mismatched target")
			}
		})
	}
}

func TestPreimageStorageAndTopologyAdversaries(t *testing.T) {
	for _, kind := range []string{"fifo", "storage-hardlink", "storage-symlink", "parent-symlink", "run-swap", "store-swap", "journal-sync", "manifest"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, base, r, b := preimageFixture(t, "old")
			p := preimagePrepare(t, ctx, r, b)
			id := preimageID(r.ID(), workspaceplan.Hash(b), 0)
			stored := filepath.Join(r.directory, "preimages", id+".bin")
			originalTarget := filepath.Join(r.output, "config.txt")
			p.preimageHook = func(phase string) error {
				if kind == "journal-sync" && phase == "journal_sync" {
					return r.journalFile.Close()
				}
				if kind == "manifest" && phase == "verify" {
					return os.Mkdir(filepath.Join(r.directory, "manifest.next"), 0700)
				}
				if phase != "reopen" {
					return nil
				}
				switch kind {
				case "fifo":
					if e := os.Remove(stored); e != nil {
						return e
					}
					return syscall.Mkfifo(stored, 0600)
				case "storage-hardlink":
					return os.Link(stored, filepath.Join(r.directory, "preimages", "alias"))
				case "storage-symlink":
					if e := os.Rename(stored, stored+"-old"); e != nil {
						return e
					}
					return os.Symlink(stored+"-old", stored)
				case "parent-symlink":
					if e := os.Rename(r.output, r.output+"-old"); e != nil {
						return e
					}
					return os.Symlink(r.output+"-old", r.output)
				case "run-swap":
					if e := os.Rename(r.directory, r.directory+"-old"); e != nil {
						return e
					}
					return os.Mkdir(r.directory, 0700)
				case "store-swap":
					if e := os.Rename(base, base+"-old"); e != nil {
						return e
					}
					return os.Mkdir(base, 0700)
				}
				return nil
			}
			permit, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			report, e := permit.Apply(ctx)
			if e == nil || report.Status == "succeeded" {
				t.Fatal("unsafe capture allowed", kind)
			}
			if string(read(t, filepath.Join(source, "config.txt"))) != "old" {
				t.Fatal("source changed")
			}
			if kind != "run-swap" && kind != "store-swap" && string(read(t, originalTarget)) != "old" {
				t.Fatal("target written")
			}
		})
	}
}

func TestPreimagePartialBatchAndReportFailure(t *testing.T) {
	t.Run("prior create remains confirmed", func(t *testing.T) {
		ctx, source, _, r := fixture(t)
		p := preimagePrepare(t, ctx, r, plan(t))
		p.preimageHook = func(phase string) error {
			if phase == "capture" {
				return errors.New("synthetic capture")
			}
			return nil
		}
		permit, e := p.Approve(ctx, allow)
		if e != nil {
			t.Fatal(e)
		}
		report, e := permit.Apply(ctx)
		if e == nil || report.Status != "partial" || report.Operations[0].Status != "succeeded" {
			t.Fatal(report, e)
		}
		if string(read(t, filepath.Join(r.output, "docs/NOTES.md"))) != "approved note\n" {
			t.Fatal("prior effect lost")
		}
		if string(read(t, filepath.Join(r.output, "src/config.txt"))) != "mode=initial\n" || string(read(t, filepath.Join(source, "src/config.txt"))) != "mode=initial\n" {
			t.Fatal("replacement despite failure")
		}
	})
	t.Run("report persistence after effect", func(t *testing.T) {
		ctx, _, _, r, b := preimageFixture(t, "old")
		p := preimagePrepare(t, ctx, r, b)
		permit, e := p.Approve(ctx, allow)
		if e != nil {
			t.Fatal(e)
		}
		permit.applyOne = func(ctx context.Context, index int) error {
			if e := permit.applyPrepared(ctx, index); e != nil {
				return e
			}
			return os.WriteFile(filepath.Join(r.directory, "artifacts/report.json"), []byte("incomplete"), 0600)
		}
		report, e := permit.Apply(ctx)
		if e == nil || report.Status != "unknown" {
			t.Fatal("false success", report, e)
		}
		if string(read(t, filepath.Join(r.output, "config.txt"))) != "final\n" {
			t.Fatal("expected effect missing")
		}
		if recovered, e := r.Report(); e == nil && recovered.Status == "succeeded" {
			t.Fatal("partial report presented as success")
		}
	})
}

func TestPreimageConcurrentRunsAndEOF(t *testing.T) {
	ctx, _, base, r, b := preimageFixture(t, "old")
	p := preimagePrepare(t, ctx, r, b)
	other, e := Open(base, r.ID())
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	if _, e := other.PrepareWithOptions(ctx, b, ApplyOptions{true, true}); e == nil {
		t.Fatal("parallel run preparation")
	}
	terminal := editcontract.NewTerminal(strings.NewReader(""), &strings.Builder{})
	if _, e := p.Approve(ctx, terminal); !errors.Is(e, editcontract.ErrDenied) {
		t.Fatal("EOF approval", e)
	}
	if _, e := os.Stat(filepath.Join(r.directory, "preimages")); !os.IsNotExist(e) {
		t.Fatal("EOF captured content")
	}
}

func TestPreimageWriteRejectsInternalSymlinkRedirect(t *testing.T) {
	ctx, _, _, r, b := preimageFixture(t, "old")
	p := preimagePrepare(t, ctx, r, b)
	changed := false
	p.preimageHook = func(phase string) error {
		if !changed && strings.HasPrefix(phase, "open:preimages/") {
			changed = true
			if e := os.Rename(filepath.Join(r.directory, "preimages"), filepath.Join(r.directory, "preimages-old")); e != nil {
				return e
			}
			return os.Symlink(r.output, filepath.Join(r.directory, "preimages"))
		}
		return nil
	}
	permit, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	report, e := permit.Apply(ctx)
	if e == nil || report.Status == "succeeded" {
		t.Fatal("redirect accepted")
	}
	entries, e := os.ReadDir(r.output)
	if e != nil || len(entries) != 1 || entries[0].Name() != "config.txt" {
		t.Fatal("retention bytes escaped into output")
	}
	if string(read(t, filepath.Join(r.output, "config.txt"))) != "old" {
		t.Fatal("target changed")
	}
}

func TestPreimagePersistentBindingsBeforeReplace(t *testing.T) {
	for _, artifact := range []string{"journal", "metadata", "approval", "execution-journal", "approved-plan"} {
		t.Run(artifact, func(t *testing.T) {
			ctx, _, _, r, plan := preimageFixture(t, "old")
			p := preimagePrepare(t, ctx, r, plan)
			id := preimageID(r.ID(), workspaceplan.Hash(plan), 0)
			changed := false
			p.preimageHook = func(phase string) error {
				if phase != "before_replace" {
					return nil
				}
				name := "artifacts/preimage-journal.jsonl"
				if artifact == "metadata" {
					name = "preimages/" + id + ".json"
				}
				if artifact == "approval" {
					name = "artifacts/retention-approval.json"
				}
				if artifact == "approved-plan" {
					name = "artifacts/approved-plan.json"
				}
				if artifact == "execution-journal" {
					name = "artifacts/journal.jsonl"
					changed = true
					data := append(read(t, filepath.Join(r.directory, name)), []byte("{}\n")...)
					return os.WriteFile(filepath.Join(r.directory, name), data, 0600)
				}
				changed = true
				return os.WriteFile(filepath.Join(r.directory, name), []byte("{}\n"), 0600)
			}
			permit, err := p.Approve(ctx, allow)
			if err != nil {
				t.Fatal(err)
			}
			report, err := permit.Apply(ctx)
			if !changed || err == nil || report.Status == "succeeded" {
				t.Fatal("invalid persisted binding accepted", artifact, report.Status, err)
			}
			if string(read(t, filepath.Join(r.output, "config.txt"))) != "old" {
				t.Fatal("replace proceeded without integral capture evidence")
			}
		})
	}
}

func TestPreimagePersistentBindingsAfterReplace(t *testing.T) {
	for _, artifact := range []string{"journal", "postimage"} {
		t.Run(artifact, func(t *testing.T) {
			ctx, _, _, r, plan := preimageFixture(t, "old")
			p := preimagePrepare(t, ctx, r, plan)
			permit, err := p.Approve(ctx, allow)
			if err != nil {
				t.Fatal(err)
			}
			permit.applyOne = func(ctx context.Context, index int) error {
				if err := permit.applyPrepared(ctx, index); err != nil {
					return err
				}
				if artifact == "postimage" {
					return os.WriteFile(filepath.Join(r.output, "config.txt"), []byte("changed after effect\n"), 0600)
				}
				return os.WriteFile(filepath.Join(r.directory, "artifacts/preimage-journal.jsonl"), []byte("{}\n"), 0600)
			}
			report, err := permit.Apply(ctx)
			if err == nil || report.Status != "unknown" {
				t.Fatal("corrupt evidence after effect yielded success", report.Status, err)
			}
			if artifact == "journal" && report.Retention.State != "unknown" {
				t.Fatal("corrupt retention still labelled verified", report.Retention.State)
			}
			expected := "final\n"
			if artifact == "postimage" {
				expected = "changed after effect\n"
			}
			if string(read(t, filepath.Join(r.output, "config.txt"))) != expected {
				t.Fatal("effect was rolled back or never applied")
			}
			if checked, err := r.Report(); err == nil && checked.Status == "succeeded" {
				t.Fatal("invalid retained evidence reported as success")
			}
		})
	}
}
