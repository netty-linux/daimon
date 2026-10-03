//go:build linux && amd64

package managedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/policy"
	"github.com/netty-linux/daimon/internal/workspacejournal"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

func fixture(t *testing.T) (context.Context, string, string, *Run) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	base := filepath.Join(dir, "store")
	for _, p := range []string{source, filepath.Join(source, "src"), filepath.Join(source, "docs")} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for p, s := range map[string]string{"README.md": "PRIVATE_SOURCE_CONTENT\n", "src/config.txt": "mode=initial\n", "src/info.txt": "note\n"} {
		if err := os.WriteFile(filepath.Join(source, p), []byte(s), 0640); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Create(ctx, base, source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return ctx, source, base, r
}
func plan(t *testing.T) []byte {
	t.Helper()
	absent := true
	p := workspaceplan.Plan{Version: 1, Kind: "workspace_apply", Operations: []workspaceplan.Operation{
		{Type: "create_file", Path: "docs/NOTES.md", Content: "approved note\n", Precondition: workspaceplan.Precondition{Absent: &absent}, Validation: workspaceplan.Validation{SHA256: workspaceplan.Hash([]byte("approved note\n"))}},
		{Type: "replace_file", Path: "src/config.txt", Content: "mode=final\n", Precondition: workspaceplan.Precondition{SHA256: workspaceplan.Hash([]byte("mode=initial\n"))}, Validation: workspaceplan.Validation{SHA256: workspaceplan.Hash([]byte("mode=final\n"))}},
	}, Blockers: []string{}, Assumptions: []string{}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type reviewFunc func(context.Context, editcontract.Review) (editcontract.Decision, error)

func (f reviewFunc) Review(ctx context.Context, r editcontract.Review) (editcontract.Decision, error) {
	return f(ctx, r)
}

var allow = reviewFunc(func(context.Context, editcontract.Review) (editcontract.Decision, error) {
	return editcontract.Allow, nil
})

func read(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func assertSource(t *testing.T, ctx context.Context, source, snapshot string) {
	t.Helper()
	_, _, _, _, got, err := readSnapshot(ctx, source, false)
	if err != nil || got != snapshot {
		t.Fatal("source changed", err)
	}
	i, err := os.Stat(filepath.Join(source, "src/config.txt"))
	if err != nil || i.Mode().Perm() != 0640 {
		t.Fatal("source permissions changed")
	}
}
func TestManagedSuccessArtifactsAndSingleUse(t *testing.T) {
	ctx, source, base, r := fixture(t)
	initial := r.Manifest()
	p, err := r.Prepare(ctx, plan(t))
	if err != nil {
		t.Fatal(err)
	}
	view := p.View()
	if !strings.Contains(view.Display, "approved note") || !strings.Contains(view.Display, "mode=final") || view.RunID != r.ID() {
		t.Fatal("incomplete preview")
	}
	view.Display = "changed"
	view.Path = "outside" // detached review cannot retarget.
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	permitCopy := *permit
	proposalCopy := *p
	report, err := permit.Apply(ctx)
	if err != nil || report.Status != "succeeded" {
		t.Fatal(report, err)
	}
	if !bytes.Equal(read(t, filepath.Join(r.output, "docs/NOTES.md")), []byte("approved note\n")) || !bytes.Equal(read(t, filepath.Join(r.output, "src/config.txt")), []byte("mode=final\n")) {
		t.Fatal("wrong bytes")
	}
	for _, name := range []string{"docs/NOTES.md", "src/config.txt"} {
		i, e := os.Stat(filepath.Join(r.output, name))
		if e != nil || i.Mode().Perm() != 0600 {
			t.Fatal("private permissions")
		}
	}
	assertSource(t, ctx, source, initial.Snapshot)
	if _, err := permit.Apply(ctx); !errors.Is(err, editcontract.ErrUsed) {
		t.Fatal("permit reused")
	}
	if _, err := p.Approve(ctx, allow); !errors.Is(err, editcontract.ErrUsed) {
		t.Fatal("proposal reused")
	}
	if _, err := permitCopy.Apply(ctx); !errors.Is(err, editcontract.ErrUsed) {
		t.Fatal("copied permit reused")
	}
	if _, err := proposalCopy.Approve(ctx, allow); !errors.Is(err, editcontract.ErrUsed) {
		t.Fatal("copied proposal reused")
	}
	for _, name := range []string{"manifest.json", "artifacts/journal.jsonl", "artifacts/report.json"} {
		b := read(t, filepath.Join(r.directory, name))
		for _, s := range []string{"PRIVATE_SOURCE_CONTENT", "approved note", "mode=final", source, "API_KEY", "Authorization"} {
			if bytes.Contains(b, []byte(s)) {
				t.Fatal("metadata leakage", name)
			}
		}
	}
	if !bytes.Equal(read(t, filepath.Join(r.directory, "artifacts/approved-plan.json")), plan(t)) {
		t.Fatal("approved artifact differs")
	}
	id := r.ID()
	r.Close()
	reopened, err := Open(base, id)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if report, err := reopened.Report(); err != nil || report.Status != "succeeded" {
		t.Fatal(report, err)
	}
	if _, err := reopened.Prepare(ctx, plan(t)); !errors.Is(err, ErrState) {
		t.Fatal("run reused", err)
	}
	if (policy.WorkspacePolicy{Mode: policy.WorkspaceApply, Create: true, Replace: true}).Capability("apply_plan").Enabled {
		t.Fatal("shared apply enabled")
	}
}

func TestDeniedEOFDisplayAndChanged(t *testing.T) {
	for _, kind := range []string{"deny", "eof", "display_failure", "changed", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, _, r := fixture(t)
			snapshot := r.Manifest().Snapshot
			p, err := r.Prepare(ctx, plan(t))
			if err != nil {
				t.Fatal(err)
			}
			var reviewer editcontract.Reviewer = editcontract.NewTerminal(strings.NewReader("n\n"), io.Discard)
			switch kind {
			case "eof":
				reviewer = editcontract.NewTerminal(strings.NewReader("y"), io.Discard)
			case "display_failure":
				reviewer = editcontract.NewTerminal(strings.NewReader("y\n"), badWriter{})
			case "changed":
				reviewer = allow
			case "cancel":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
				reviewer = allow
			}
			permit, err := p.Approve(ctx, reviewer)
			if kind == "changed" {
				if err != nil {
					t.Fatal(err)
				}
				os.WriteFile(filepath.Join(r.output, "src/config.txt"), []byte("external change\n"), 0600)
				_, err = permit.Apply(ctx)
				if err == nil {
					t.Fatal("changed snapshot applied")
				}
			} else if permit != nil || err == nil {
				t.Fatal("unsafe approval", err)
			}
			if _, err := os.Stat(filepath.Join(r.output, "docs/NOTES.md")); !os.IsNotExist(err) {
				t.Fatal("unexpected creation")
			}
			assertSource(t, context.Background(), source, snapshot)
		})
	}
}

type badWriter struct{}

func (badWriter) Write([]byte) (int, error) { return 0, errors.New("SECRET must not appear") }
func TestJournalFailureAndPartial(t *testing.T) {
	for _, kind := range []string{"journal", "operation", "journal_after_first"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, _, r := fixture(t)
			snapshot := r.Manifest().Snapshot
			p, err := r.Prepare(ctx, plan(t))
			if err != nil {
				t.Fatal(err)
			}
			permit, err := p.Approve(ctx, allow)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "journal" {
				metadata := []workspacejournal.Metadata{}
				for _, op := range p.plan.Operations {
					metadata = append(metadata, workspacejournal.Metadata{Type: op.Type, Path: op.Path, Before: op.Precondition.SHA256, After: op.Validation.SHA256})
				}
				p.journal, _ = workspacejournal.New(badWriter{}, metadata)
			}
			calls := 0
			permit.applyOne = func(ctx context.Context, i int) error {
				calls++
				if kind == "operation" && i == 1 {
					return ErrApply
				}
				err := permit.applyPrepared(ctx, i)
				if kind == "journal_after_first" && i == 0 {
					r.journalFile.Close()
				}
				return err
			}
			report, err := permit.Apply(ctx)
			if err == nil {
				t.Fatal("failure swallowed")
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatal("error leaked")
			}
			if kind == "journal" {
				if calls != 0 || report.Operations[0].Status != "unknown" {
					t.Fatal(report, calls)
				}
			} else if kind == "operation" {
				if calls != 2 || report.Status != "partial" || report.Operations[0].Status != "succeeded" || report.Operations[1].Status != "unknown" {
					t.Fatal(report, calls)
				}
			} else if calls != 1 || report.Operations[0].Status != "unknown" {
				t.Fatal("continued after journal failure", report, calls)
			}
			if got := read(t, filepath.Join(r.output, "src/config.txt")); string(got) != "mode=initial\n" {
				t.Fatal("later operation ran")
			}
			assertSource(t, ctx, source, snapshot)
		})
	}
}

func TestUnsafeInputsLimitsAndRoots(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "limit_files", "limit_bytes", "limit_total", "limit_directories", "depth", "traversal_name"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, base, r := fixture(t)
			r.Close()
			switch kind {
			case "symlink":
				if err := os.Symlink("/etc/passwd", filepath.Join(source, "link")); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(filepath.Join(source, "README.md"), filepath.Join(source, "linked")); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := makeFIFO(filepath.Join(source, "fifo")); err != nil {
					t.Fatal(err)
				}
			case "limit_files":
				for i := 0; i < MaxFiles; i++ {
					os.WriteFile(filepath.Join(source, strings.Repeat("x", i+1)), nil, 0600)
				}
			case "limit_bytes":
				os.WriteFile(filepath.Join(source, "large"), make([]byte, MaxFileBytes+1), 0600)
			case "limit_total":
				for i := 0; i < 17; i++ {
					if err := os.WriteFile(filepath.Join(source, strings.Repeat("t", i+1)), make([]byte, MaxFileBytes), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "limit_directories":
				for i := 0; i < MaxDirectories; i++ {
					if err := os.Mkdir(filepath.Join(source, strings.Repeat("d", i+1)), 0700); err != nil {
						t.Fatal(err)
					}
				}
			case "depth":
				if err := os.MkdirAll(filepath.Join(source, strings.Repeat("d/", MaxDepth+1)), 0700); err != nil {
					t.Fatal(err)
				}
			case "traversal_name":
				os.WriteFile(filepath.Join(source, "a\\b"), nil, 0600)
			}
			if next, err := Create(ctx, base, source); err == nil {
				next.Close()
				t.Fatal("unsafe import accepted")
			}
		})
	}
	for _, kind := range []string{"run_removed", "run_swapped", "output_swapped", "symlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			ctx, _, base, r := fixture(t)
			id := r.ID()
			switch kind {
			case "run_removed":
				if err := os.Rename(r.directory, r.directory+"-moved"); err != nil {
					t.Fatal(err)
				}
			case "run_swapped":
				if err := os.Rename(r.directory, r.directory+"-old"); err != nil {
					t.Fatal(err)
				}
				os.Mkdir(r.directory, 0700)
			case "output_swapped":
				os.Rename(r.output, r.output+"-old")
				os.Mkdir(r.output, 0700)
			case "symlink":
				os.Rename(r.output, r.output+"-old")
				if err := os.Symlink(r.output+"-old", r.output); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				os.Chmod(r.output, 0777)
			}
			if _, err := r.Prepare(ctx, plan(t)); err == nil {
				t.Fatal("invalid run accepted")
			}
			if reopened, err := Open(base, id); err == nil {
				reopened.Close()
				t.Fatal("invalid root reopened")
			}
		})
	}
}

func TestManagedPrivacyBoundaryAndExpiredApproval(t *testing.T) {
	ctx, source, _, r := fixture(t)
	if err := checkLocalPath("/proc"); err == nil {
		t.Fatal("mount crossing accepted")
	}
	if err := checkPhysicalOverlap(filepath.Join(source, "nested-store"), source); !errors.Is(err, ErrPrivate) {
		t.Fatal("physical source overlap accepted", err)
	}
	unsafeParent := filepath.Join(t.TempDir(), "unsafe")
	if err := os.Mkdir(unsafeParent, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafeParent, 0777); err != nil {
		t.Fatal(err)
	}
	if next, err := Create(ctx, filepath.Join(unsafeParent, "store"), source); err == nil {
		next.Close()
		t.Fatal("writable ancestor accepted")
	}
	if next, err := Create(ctx, filepath.Join(source, "store"), source); err == nil {
		next.Close()
		t.Fatal("source/store overlap accepted")
	}
	short, cancel := context.WithCancel(ctx)
	p, err := r.Prepare(short, plan(t))
	if err != nil {
		t.Fatal(err)
	}
	permit, err := p.Approve(short, allow)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := permit.Apply(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("expired approval extended", err)
	}
	if _, err := os.Stat(filepath.Join(r.output, "docs/NOTES.md")); !os.IsNotExist(err) {
		t.Fatal("expired approval wrote")
	}
}
func TestInvalidPlanAndConcurrentAttempt(t *testing.T) {
	ctx, _, base, r := fixture(t)
	for _, data := range [][]byte{[]byte("{}"), append(plan(t), []byte(" trailing")...), bytes.ReplaceAll(plan(t), []byte("docs/NOTES.md"), []byte("../escape"))} {
		if _, err := r.Prepare(ctx, data); err == nil {
			t.Fatal("invalid plan accepted")
		}
	}
	wrongPrecondition := bytes.ReplaceAll(plan(t), []byte(workspaceplan.Hash([]byte("mode=initial\n"))), []byte(strings.Repeat("0", 64)))
	if _, err := r.Prepare(ctx, wrongPrecondition); err == nil {
		t.Fatal("wrong precondition accepted")
	}
	if _, err := os.Stat(filepath.Join(r.directory, "attempt.lock")); !os.IsNotExist(err) {
		t.Fatal("invalid plan claimed run")
	}
	r2, err := Open(base, r.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, run := range []*Run{r, r2} {
		wg.Add(1)
		go func(run *Run) { defer wg.Done(); _, err := run.Prepare(ctx, plan(t)); results <- err }(run)
	}
	wg.Wait()
	close(results)
	allowed := 0
	for err := range results {
		if err == nil {
			allowed++
		}
	}
	if allowed != 1 {
		t.Fatal("concurrent attempts allowed", allowed)
	}
}
