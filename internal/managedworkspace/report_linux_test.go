//go:build linux && amd64

package managedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspacejournal"
)

func TestReportRefusesInconsistentSuccess(t *testing.T) {
	for _, kind := range []string{"empty", "null", "duplicate", "unknown_operation", "missing_plan", "changed_plan", "missing_journal", "truncated_journal", "changed_output", "missing_output", "missing_manifest", "path_injection", "symlink_parent", "staging_residue"} {
		t.Run(kind, func(t *testing.T) {
			ctx, _, _, r := fixture(t)
			p, err := r.Prepare(ctx, plan(t))
			if err != nil {
				t.Fatal(err)
			}
			permit, err := p.Approve(ctx, allow)
			if err != nil {
				t.Fatal(err)
			}
			report, err := permit.Apply(ctx)
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(r.directory, "artifacts/report.json")
			data := read(t, name)
			switch kind {
			case "empty":
				report.Operations = []OperationReport{}
				data, _ = json.Marshal(report)
			case "null":
				report.Operations = nil
				data, _ = json.Marshal(report)
			case "duplicate":
				data = bytes.Replace(data, []byte(`"status":"succeeded"`), []byte(`"status":"failed","status":"succeeded"`), 1)
			case "unknown_operation":
				report.Operations[0].Status = "unknown"
				data, _ = json.Marshal(report)
			case "missing_plan":
				if err := os.Remove(filepath.Join(r.directory, "artifacts/approved-plan.json")); err != nil {
					t.Fatal(err)
				}
			case "changed_plan":
				if err := os.WriteFile(filepath.Join(r.directory, "artifacts/approved-plan.json"), bytes.ReplaceAll(plan(t), []byte("docs/NOTES.md"), []byte("docs/OTHER.md")), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing_journal":
				if err := os.Remove(filepath.Join(r.directory, "artifacts/journal.jsonl")); err != nil {
					t.Fatal(err)
				}
			case "truncated_journal":
				if err := os.WriteFile(filepath.Join(r.directory, "artifacts/journal.jsonl"), []byte("{\"version\":1"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed_output":
				if err := os.WriteFile(filepath.Join(r.output, "src/config.txt"), []byte("changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing_output":
				if err := os.Remove(filepath.Join(r.output, "src/config.txt")); err != nil {
					t.Fatal(err)
				}
			case "missing_manifest":
				if err := os.Remove(filepath.Join(r.directory, "manifest.json")); err != nil {
					t.Fatal(err)
				}
			case "path_injection":
				report.Operations[0].Path = "../manifest.json"
				data, _ = json.Marshal(report)
			case "staging_residue":
				if err := os.WriteFile(filepath.Join(r.directory, "manifest.next"), []byte("incomplete"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink_parent":
				if err := os.Rename(filepath.Join(r.output, "docs"), filepath.Join(r.output, "docs-old")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("docs-old", filepath.Join(r.output, "docs")); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(name, data, 0600); err != nil {
				t.Fatal(err)
			}
			before := read(t, name)
			got, reportErr := r.Report()
			if reportErr == nil && got.Status == "succeeded" {
				t.Fatal("inconsistent artifacts accepted as success")
			}
			if kind == "staging_residue" && (reportErr != nil || got.Status != "unknown_interrupted") {
				t.Fatal("staging not classified as interrupted", got, reportErr)
			}
			if !bytes.Equal(before, read(t, name)) {
				t.Fatal("report wrote artifacts")
			}
		})
	}
}

func TestReportInterruptedBeforePreparedManifest(t *testing.T) {
	_, _, _, r := fixture(t)
	if err := os.WriteFile(filepath.Join(r.directory, "attempt.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := r.Report()
	if err != nil || got.Status != "unknown_interrupted" {
		t.Fatal("incomplete attempt presented as ready", got, err)
	}
}

func TestReportStagingOverridesReady(t *testing.T) {
	_, _, _, r := fixture(t)
	if err := os.WriteFile(filepath.Join(r.directory, "manifest.next"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := r.Report()
	if err != nil || got.Status != "unknown_interrupted" {
		t.Fatal(got, err)
	}
}

func TestArtifactFailureDoesNotReturnSucceeded(t *testing.T) {
	ctx, _, _, r := fixture(t)
	p, err := r.Prepare(ctx, plan(t))
	if err != nil {
		t.Fatal(err)
	}
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.directory, "artifacts/report.json"), []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := permit.Apply(ctx)
	if !errors.Is(err, ErrArtifact) || got.Status == "succeeded" {
		t.Fatal("artifact failure presented as complete", got, err)
	}
	if got.Operations[0].Status != "succeeded" || got.Operations[1].Status != "succeeded" {
		t.Fatal("confirmed effects lost", got)
	}
}

type cancelOnCompletion struct {
	file   *os.File
	cancel context.CancelFunc
	writes int
}

func (s *cancelOnCompletion) Write(data []byte) (int, error) { s.writes++; return s.file.Write(data) }
func (s *cancelOnCompletion) Sync() error {
	err := s.file.Sync()
	if s.writes == 4 {
		s.cancel()
	}
	return err
}
func TestCancellationDuringLastJournalSync(t *testing.T) {
	ctx, _, _, r := fixture(t)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p, err := r.Prepare(ctx, plan(t))
	if err != nil {
		t.Fatal(err)
	}
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	var metadata []workspacejournal.Metadata
	for _, op := range p.plan.Operations {
		metadata = append(metadata, workspacejournal.Metadata{Type: op.Type, Path: op.Path, BeforeSHA256: op.Precondition.SHA256, AfterSHA256: op.Validation.SHA256})
	}
	p.journal, err = workspacejournal.New(&cancelOnCompletion{file: r.journalFile, cancel: cancel}, metadata)
	if err != nil {
		t.Fatal(err)
	}
	got, err := permit.Apply(ctx)
	if !errors.Is(err, context.Canceled) || got.Status == "succeeded" {
		t.Fatal("cancelled run accepted as complete", got, err)
	}
}

func TestApprovalUsesPreparationBudget(t *testing.T) {
	ctx, _, _, r := fixture(t)
	preparedCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p, err := r.Prepare(preparedCtx, plan(t))
	if err != nil {
		t.Fatal(err)
	}
	deadline, _ := preparedCtx.Deadline()
	_, err = p.Approve(ctx, reviewFunc(func(reviewCtx context.Context, _ editcontract.Review) (editcontract.Decision, error) {
		got, ok := reviewCtx.Deadline()
		if !ok || got.After(deadline) {
			t.Error("review extended preparation deadline")
		}
		cancel()
		select {
		case <-reviewCtx.Done():
		case <-time.After(100 * time.Millisecond):
			t.Error("review ignored preparation cancellation")
		}
		return editcontract.Deny, nil
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPlanCannotReachControlArtifacts(t *testing.T) {
	ctx, _, _, r := fixture(t)
	for _, path := range []string{"../manifest.json", "../attempt.lock", "../artifacts/journal.jsonl", "../artifacts/approved-plan.json", "/manifest.json"} {
		data := bytes.ReplaceAll(plan(t), []byte("docs/NOTES.md"), []byte(path))
		if _, err := r.Prepare(ctx, data); err == nil {
			t.Fatal("control path accepted")
		}
	}
	// A homonymous user file is confined to output, not the run's manifest.
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(plan(t), &parsed); err != nil {
		t.Fatal(err)
	}
	var operations []json.RawMessage
	if err := json.Unmarshal(parsed["operations"], &operations); err != nil {
		t.Fatal(err)
	}
	parsed["operations"], _ = json.Marshal(operations[:1])
	data, _ := json.Marshal(parsed)
	data = bytes.ReplaceAll(data, []byte("docs/NOTES.md"), []byte("manifest.json"))
	p, err := r.Prepare(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := permit.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeManifest(read(t, filepath.Join(r.directory, "manifest.json"))); err != nil {
		t.Fatal("run metadata damaged", err)
	}
	if string(read(t, filepath.Join(r.output, "manifest.json"))) != "approved note\n" {
		t.Fatal("wrong namespace")
	}
}

func TestConcurrentCopiedPermit(t *testing.T) {
	ctx, _, _, r := fixture(t)
	p, err := r.Prepare(ctx, plan(t))
	if err != nil {
		t.Fatal(err)
	}
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	copy := *permit
	results := make(chan error, 2)
	for _, current := range []*Permit{permit, &copy} {
		go func(current *Permit) { _, err := current.Apply(ctx); results <- err }(current)
	}
	wins, used := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if errors.Is(err, editcontract.ErrUsed) {
			used++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || used != 1 {
		t.Fatal("copied permit ran twice")
	}
	if got, err := r.Report(); err != nil || got.Status != "succeeded" {
		t.Fatal("concurrent artifacts damaged", got, err)
	}
}
