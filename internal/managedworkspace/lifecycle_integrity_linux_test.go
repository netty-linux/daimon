//go:build linux && amd64

package managedworkspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLifecycleEmptyInvalidAndUnsafeEntries(t *testing.T) {
	if _, err := canonicalBase(""); err == nil {
		t.Fatal("implicit current-directory store accepted")
	}
	ctx, _, r, s := lifecycleFixture(t)
	for _, id := range []string{".", "..", "../source", "/source", `C:\source`, strings.Repeat("a", 32) + "/output", tombstoneDirectory} {
		if _, err := s.Inspect(ctx, id); err == nil {
			t.Fatal("invalid ID accepted", id)
		}
	}
	badID := strings.Repeat("a", 32)
	if err := os.Symlink(r.directory, filepath.Join(s.base, badID)); err != nil {
		t.Fatal(err)
	}
	fifoID := strings.Repeat("b", 32)
	if err := syscall.Mkfifo(filepath.Join(s.base, fifoID), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.base, "unsafe-name"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 3 {
		t.Fatal(list, err)
	}
	for _, id := range []string{badID, fifoID} {
		got, err := s.Inspect(ctx, id)
		if err != nil || got.State != "invalid" {
			t.Fatal(got, err)
		}
	}
	empty := filepath.Join(t.TempDir(), "store")
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(empty)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got, err := store.List(ctx); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	for _, path := range []string{empty + "-absent", filepath.Join(s.base, badID)} {
		if _, err := OpenStore(path); err == nil {
			t.Fatal("unsafe store accepted")
		}
	}
	invalidBase := filepath.Join(t.TempDir(), string([]byte{0xff}))
	if err := os.Mkdir(invalidBase, 0700); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenStore(invalidBase); err == nil {
		store.Close()
		t.Fatal("irreversible audit path accepted")
	}
	newBase := filepath.Join(t.TempDir(), string([]byte{0xfe}))
	if run, err := Create(ctx, newBase, r.output); err == nil {
		run.Close()
		t.Fatal("create admitted an irreversible store path")
	}
	if _, err := os.Lstat(newBase); !os.IsNotExist(err) {
		t.Fatal("invalid store was provisioned")
	}
}
func TestLifecycleInspectCorruptArtifacts(t *testing.T) {
	for _, kind := range []string{"manifest_missing", "manifest_truncated", "manifest_duplicate", "manifest_null", "journal_missing", "journal_truncated", "plan_changed", "output_changed", "parent_symlink", "staging", "lock"} {
		t.Run(kind, func(t *testing.T) {
			ctx, _, r, s := lifecycleFixture(t)
			if kind != "lock" && kind != "staging" {
				p, err := r.Prepare(ctx, plan(t))
				if err != nil {
					t.Fatal(err)
				}
				permit, err := p.Approve(ctx, allow)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = permit.Apply(ctx); err != nil {
					t.Fatal(err)
				}
				got, err := s.Inspect(ctx, r.ID())
				if err != nil || got.State != "succeeded" || !got.ManifestVerified || !got.JournalVerified || !got.ApprovedPlanVerified {
					t.Fatal("missing integrity indicators", got, err)
				}
			}
			r.Close()
			var err error
			switch kind {
			case "manifest_missing":
				err = os.Remove(filepath.Join(r.directory, "manifest.json"))
			case "manifest_truncated":
				err = os.WriteFile(filepath.Join(r.directory, "manifest.json"), []byte("{"), 0600)
			case "manifest_duplicate":
				err = os.WriteFile(filepath.Join(r.directory, "manifest.json"), []byte(`{"version":1,"version":1}`), 0600)
			case "manifest_null":
				err = os.WriteFile(filepath.Join(r.directory, "manifest.json"), []byte("null"), 0600)
			case "journal_missing":
				err = os.Remove(filepath.Join(r.directory, "artifacts/journal.jsonl"))
			case "journal_truncated":
				err = os.WriteFile(filepath.Join(r.directory, "artifacts/journal.jsonl"), []byte("{"), 0600)
			case "plan_changed":
				err = os.WriteFile(filepath.Join(r.directory, "artifacts/approved-plan.json"), []byte("{}"), 0600)
			case "output_changed":
				err = os.WriteFile(filepath.Join(r.output, "src/config.txt"), []byte("changed"), 0600)
			case "parent_symlink":
				parent := filepath.Join(r.output, "src")
				err = os.Rename(parent, parent+"-moved")
				if err == nil {
					err = os.Symlink(parent+"-moved", parent)
				}
			case "staging":
				err = os.WriteFile(filepath.Join(r.directory, "manifest.next"), nil, 0600)
			case "lock":
				err = os.WriteFile(filepath.Join(r.directory, "attempt.lock"), nil, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Inspect(ctx, r.ID())
			if err != nil {
				t.Fatal(err)
			}
			expected := "invalid"
			if kind == "lock" || kind == "staging" {
				expected = "unknown_interrupted"
			}
			if got.State != expected {
				t.Fatal(got)
			}
		})
	}
}
func TestLifecycleAuditIntegrity(t *testing.T) {
	for _, kind := range []string{"truncated", "duplicate", "null", "run_id", "store", "missing_terminal"} {
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
			if _, err = permit.Discard(ctx); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.base, tombstoneDirectory, r.ID()+".jsonl")
			data := read(t, path)
			switch kind {
			case "truncated":
				data = data[:len(data)-1]
			case "duplicate":
				data = []byte("{\"version\":1,\"version\":1}\n")
			case "null":
				data = []byte("null\n")
			case "run_id":
				data = []byte(strings.ReplaceAll(string(data), r.ID(), strings.Repeat("0", 32)))
			case "store":
				data = []byte(strings.ReplaceAll(string(data), s.base, "/outside"))
			case "missing_terminal":
				data = []byte(strings.Split(string(data), "\n")[0] + "\n")
			}
			if err = os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := s.Inspect(ctx, r.ID())
			if err != nil {
				t.Fatal(err)
			}
			expected := "invalid"
			if kind == "missing_terminal" {
				expected = "unknown_interrupted"
			}
			if got.State != expected {
				t.Fatal(got)
			}
		})
	}
}
func TestLifecycleReadCancellation(t *testing.T) {
	ctx, _, r, s := lifecycleFixture(t)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Inspect(cancelled, r.ID()); err == nil {
		t.Fatal("cancel ignored")
	}
	if _, err := s.List(cancelled); err == nil {
		t.Fatal("cancel ignored")
	}
}
