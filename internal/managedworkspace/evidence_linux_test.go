//go:build linux && amd64

package managedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

const evidenceSecret = "API_KEY=synthetic-only\npassword=synthetic-password\neyJhbGciOiJub25lIn0.eyJzdWIiOiJmaXh0dXJlIn0.synthetic\n-----BEGIN PRIVATE KEY-----\nsynthetic-private-key\n-----END PRIVATE KEY-----\n"

func TestEvidenceOverlapIncludesFilesystemRoot(t *testing.T) {
	for _, paths := range [][2]string{{"/", "/tmp/review"}, {"/tmp/review", "/"}, {"/tmp/source", "/tmp/source/child"}, {"/tmp/source", "/tmp/source"}} {
		if !overlaps(paths[0], paths[1]) {
			t.Fatal("overlap missed", paths)
		}
	}
	if overlaps("/tmp/source", "/tmp/source-old") {
		t.Fatal("ambiguous prefix")
	}
}

func evidenceFixture(t *testing.T, succeeded bool) (context.Context, string, string, *Store, *Run, string) {
	t.Helper()
	ctx, source, base, r := fixture(t)
	if succeeded {
		var p workspaceplan.Plan
		if json.Unmarshal(plan(t), &p) != nil {
			t.Fatal("plan")
		}
		for i := range p.Operations {
			p.Operations[i].Content = evidenceSecret
			p.Operations[i].Validation.SHA256 = workspaceplan.Hash([]byte(evidenceSecret))
		}
		p.Assumptions = []string{"PRIVATE_ASSUMPTION_NOT_EXPORTED"}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		proposal, err := r.Prepare(ctx, data)
		if err != nil {
			t.Fatal(err)
		}
		permit, err := proposal.Approve(ctx, allow)
		if err != nil {
			t.Fatal(err)
		}
		result, err := permit.Apply(ctx)
		if err != nil || result.Status != "succeeded" {
			t.Fatal(result, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(base, r.ID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	store, err := OpenStore(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	parent := filepath.Join(t.TempDir(), "review")
	if err = os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	return ctx, source, base, store, r, filepath.Join(parent, "evidence")
}
func evidenceInventoryHash(t *testing.T, ctx context.Context, base string, r *Run) string {
	t.Helper()
	inv, err := inventoryRun(ctx, base, r.ID(), r.root)
	if err != nil {
		t.Fatal(err)
	}
	return inv.Hash
}
func evidencePrepare(t *testing.T, ctx context.Context, s *Store, r *Run, dest, source string) *EvidenceProposal {
	t.Helper()
	p, err := s.PrepareEvidence(ctx, r.ID(), dest, source, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}
func evidencePermit(t *testing.T, ctx context.Context, p *EvidenceProposal) *EvidencePermit {
	t.Helper()
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	return permit
}
func assertNoEvidenceDestination(t *testing.T, dest string) {
	t.Helper()
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatal("destination exists", err)
	}
}

func TestEvidenceReadyAndLegacySucceededMetadataOnly(t *testing.T) {
	for _, succeeded := range []bool{false, true} {
		t.Run(fmt.Sprint(succeeded), func(t *testing.T) {
			ctx, source, base, s, r, dest := evidenceFixture(t, succeeded)
			sourceBefore := r.Manifest().Snapshot
			before := evidenceInventoryHash(t, ctx, base, r)
			other, err := Create(ctx, base, source)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			otherBefore := evidenceInventoryHash(t, ctx, base, other)
			storeInfo, err := os.Stat(base)
			if err != nil {
				t.Fatal(err)
			}
			p := evidencePrepare(t, ctx, s, r, dest, source)
			for _, secret := range []string{evidenceSecret, "API_KEY=", "password=synthetic-password", "eyJhbGciOiJub25lIn0", "BEGIN PRIVATE KEY", "PRIVATE_SOURCE_CONTENT", "PRIVATE_ASSUMPTION_NOT_EXPORTED"} {
				if strings.Contains(p.View().Display, secret) {
					t.Fatal("content in preview")
				}
			}
			for _, field := range []string{"destination=", "source_check=", "run_id=", "artifact=", "sha256=", "files=6", "approval_deadline_utc="} {
				if !strings.Contains(p.View().Display, field) {
					t.Fatal("incomplete preview", field)
				}
			}
			permit := evidencePermit(t, ctx, p)
			copied := *permit
			result, err := permit.Export(ctx)
			if err != nil || result.State != "exported" || result.Files != 6 {
				t.Fatal(result, err)
			}
			if _, err = copied.Export(ctx); !errors.Is(err, editcontract.ErrUsed) {
				t.Fatal("reused permit", err)
			}
			verified, err := VerifyEvidence(ctx, dest)
			if err != nil || verified != result {
				t.Fatal("verify", verified, err)
			}
			entries, err := os.ReadDir(dest)
			if err != nil || len(entries) != 6 {
				t.Fatal(entries, err)
			}
			for _, entry := range entries {
				if !evidenceOnlyName(entry.Name()) {
					t.Fatal("unexpected file")
				}
				data := read(t, filepath.Join(dest, entry.Name()))
				for _, secret := range []string{"API_KEY=", "password=synthetic-password", "eyJhbGciOiJub25lIn0", "BEGIN PRIVATE KEY", "PRIVATE_SOURCE_CONTENT", "PRIVATE_ASSUMPTION_NOT_EXPORTED", `"content"`, `"expected_content"`} {
					if bytes.Contains(data, []byte(secret)) {
						t.Fatal("content leaked", entry.Name())
					}
				}
				info, err := entry.Info()
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("mode", err)
				}
			}
			audit := read(t, filepath.Join(filepath.Dir(dest), evidenceAuditDirectory, p.View().ID+".jsonl"))
			for _, secret := range []string{"API_KEY=", "password=synthetic-password", "eyJhbGciOiJub25lIn0", "BEGIN PRIVATE KEY", "PRIVATE_SOURCE_CONTENT", "PRIVATE_ASSUMPTION_NOT_EXPORTED", source} {
				if bytes.Contains(audit, []byte(secret)) {
					t.Fatal("audit leak")
				}
			}
			if bytes.Count(audit, []byte{'\n'}) != 2 {
				t.Fatal("audit sequence")
			}
			var m EvidenceManifest
			if evidenceDecode(read(t, filepath.Join(dest, "export-manifest.json")), &m) != nil {
				t.Fatal("manifest")
			}
			if m.ExportKind != "evidence" || m.ContainsFileContent || m.ContainsPatchContent || m.SecretFreeGuarantee != "not_applicable" || !m.SourceNotModified || !m.ContentExportNotIncluded || m.PlanPresent != succeeded || m.JournalPresent != succeeded {
				t.Fatal(m)
			}
			if succeeded && (!bytes.Contains(read(t, filepath.Join(dest, "approved-plan.metadata.json")), []byte(`"path":"src/config.txt"`)) || !bytes.Contains(read(t, filepath.Join(dest, "inventory.metadata.json")), []byte(workspaceplan.Hash([]byte(evidenceSecret))))) {
				t.Fatal("metadata absent")
			}
			if !succeeded && len(read(t, filepath.Join(dest, "journal.jsonl"))) != 0 {
				t.Fatal("invented journal")
			}
			if evidenceInventoryHash(t, ctx, base, r) != before || evidenceInventoryHash(t, ctx, base, other) != otherBefore {
				t.Fatal("run changed")
			}
			after, err := os.Stat(base)
			if err != nil || !storeInfo.ModTime().Equal(after.ModTime()) {
				t.Fatal("store changed")
			}
			assertSource(t, ctx, source, sourceBefore)
		})
	}
}

func TestEvidenceDeniedEOFDisplayAndCancellation(t *testing.T) {
	for _, kind := range []string{"denied", "eof", "invalid", "display", "cancel", "nil"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, _, s, r, dest := evidenceFixture(t, false)
			p := evidencePrepare(t, ctx, s, r, dest, source)
			var reviewer editcontract.Reviewer
			switch kind {
			case "denied":
				reviewer = editcontract.NewTerminal(strings.NewReader("n\n"), io.Discard)
			case "eof":
				reviewer = editcontract.NewTerminal(strings.NewReader(""), io.Discard)
			case "invalid":
				reviewer = editcontract.NewTerminal(strings.NewReader("anything\n"), io.Discard)
			case "display":
				reviewer = editcontract.NewTerminal(strings.NewReader("y\n"), evidenceBrokenWriter{})
			case "cancel":
				reviewer = reviewFunc(func(context.Context, editcontract.Review) (editcontract.Decision, error) {
					return editcontract.Allow, context.Canceled
				})
			}
			if _, err := p.Approve(ctx, reviewer); err == nil {
				t.Fatal("approval accepted")
			}
			assertNoEvidenceDestination(t, dest)
			entries, err := os.ReadDir(filepath.Dir(dest))
			if err != nil || len(entries) != 0 {
				t.Fatal("effects before approval", err)
			}
			if _, err = p.Approve(ctx, allow); !errors.Is(err, editcontract.ErrUsed) {
				t.Fatal("approval reused", err)
			}
		})
	}
}

type evidenceBrokenWriter struct{}

func (evidenceBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrShortWrite }

func TestEvidenceDestinationBoundaries(t *testing.T) {
	for _, kind := range []string{"existing", "source", "store", "run", "output", "ancestor", "relative", "traversal", "symlink", "wrong_source", "invalid_id", "disabled", "parent_mode"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, base, s, r, dest := evidenceFixture(t, false)
			id := r.ID()
			enabled := true
			switch kind {
			case "existing":
				if os.Mkdir(dest, 0700) != nil {
					t.Fatal("mkdir")
				}
			case "source":
				dest = filepath.Join(source, "evidence")
			case "store":
				dest = filepath.Join(base, "evidence")
			case "run":
				dest = filepath.Join(base, id, "evidence")
			case "output":
				dest = filepath.Join(base, id, "output", "evidence")
			case "ancestor":
				dest = filepath.Join(filepath.Dir(source), "evidence")
			case "relative":
				dest = "evidence"
			case "traversal":
				dest = filepath.Dir(dest) + "/../review/evidence"
			case "symlink":
				if os.Symlink(source, dest) != nil {
					t.Fatal("symlink")
				}
			case "wrong_source":
				source = filepath.Dir(source)
			case "invalid_id":
				id = "../other"
			case "disabled":
				enabled = false
			case "parent_mode":
				if os.Chmod(filepath.Dir(dest), 0755) != nil {
					t.Fatal("chmod")
				}
			}
			if p, err := s.PrepareEvidence(ctx, id, dest, source, enabled); err == nil {
				p.Close()
				t.Fatal("unsafe destination accepted")
			}
		})
	}
}

func TestEvidenceRevalidationAndExclusiveRun(t *testing.T) {
	for _, kind := range []string{"output", "parent_move", "parent_symlink", "source_move", "store_move", "run_move", "expire", "cancel_at_approval"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, base, s, r, dest := evidenceFixture(t, false)
			p := evidencePrepare(t, ctx, s, r, dest, source)
			if _, err := s.PrepareEvidence(ctx, r.ID(), dest+"-second", source, true); err == nil {
				t.Fatal("concurrent proposal")
			}
			if _, err := s.PrepareDiscard(ctx, r.ID(), true); err == nil {
				t.Fatal("concurrent discard")
			}
			permit := evidencePermit(t, ctx, p)
			switch kind {
			case "output":
				if os.WriteFile(filepath.Join(base, r.ID(), "output/README.md"), []byte("changed"), 0600) != nil {
					t.Fatal("write")
				}
			case "parent_move":
				parent := filepath.Dir(dest)
				if os.Rename(parent, parent+"-moved") != nil || os.Mkdir(parent, 0700) != nil {
					t.Fatal("move")
				}
			case "parent_symlink":
				parent := filepath.Dir(dest)
				if os.Rename(parent, parent+"-moved") != nil || os.Symlink(parent+"-moved", parent) != nil {
					t.Fatal("symlink")
				}
			case "source_move":
				if os.Rename(source, source+"-moved") != nil || os.Mkdir(source, 0700) != nil {
					t.Fatal("move")
				}
			case "store_move":
				if os.Rename(base, base+"-moved") != nil || os.Mkdir(base, 0700) != nil {
					t.Fatal("store move")
				}
			case "run_move":
				path := filepath.Join(base, r.ID())
				if os.Rename(path, path+"-moved") != nil || os.Mkdir(path, 0700) != nil {
					t.Fatal("run move")
				}
			case "cancel_at_approval":
				c, cancel := context.WithCancel(ctx)
				p.state.approvalCtx = c
				cancel()
			case "expire":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if result, err := permit.Export(ctx); err == nil || result.State == "exported" {
				t.Fatal(result, err)
			}
			assertNoEvidenceDestination(t, dest)
		})
	}
}

func TestEvidenceCopiedPermitConcurrentSingleUse(t *testing.T) {
	ctx, source, _, s, r, dest := evidenceFixture(t, false)
	p := evidencePrepare(t, ctx, s, r, dest, source)
	permit := evidencePermit(t, ctx, p)
	copied := *permit
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, pp := range []*EvidencePermit{permit, &copied} {
		wg.Add(1)
		go func(p *EvidencePermit) { defer wg.Done(); _, err := p.Export(ctx); results <- err }(pp)
	}
	wg.Wait()
	close(results)
	success, used := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, editcontract.ErrUsed) {
			used++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || used != 1 {
		t.Fatal(success, used)
	}
	if _, err := VerifyEvidence(ctx, dest); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceConcurrentRunsCannotOverwriteDestination(t *testing.T) {
	ctx, source, base, s, r, dest := evidenceFixture(t, false)
	other, err := Create(ctx, base, source)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	p := evidencePrepare(t, ctx, s, r, dest, source)
	q := evidencePrepare(t, ctx, s, other, dest, source)
	permits := []*EvidencePermit{evidencePermit(t, ctx, p), evidencePermit(t, ctx, q)}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, permit := range permits {
		wg.Add(1)
		go func(p *EvidencePermit) { defer wg.Done(); _, err := p.Export(ctx); results <- err }(permit)
	}
	wg.Wait()
	close(results)
	success, failed := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			failed++
		}
	}
	if success != 1 || failed != 1 {
		t.Fatal(success, failed)
	}
	if _, err := VerifyEvidence(ctx, dest); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceInventoryLimitExactAndExceeded(t *testing.T) {
	for _, count := range []int{MaxEvidenceEntries, MaxEvidenceEntries + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx, source, base, s, _, dest := evidenceFixture(t, false)
			for i := 5; i < count; i++ {
				if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("extra-%02d.txt", i)), []byte("synthetic"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r, err := Create(ctx, base, source)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			p, err := s.PrepareEvidence(ctx, r.ID(), dest, source, true)
			if count > MaxEvidenceEntries {
				if !errors.Is(err, ErrLimit) {
					t.Fatal(err)
				}
				assertNoEvidenceDestination(t, dest)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if _, err := evidencePermit(t, ctx, p).Export(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyEvidence(ctx, dest); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEvidenceFaultsDoNotPublishArtificialSuccess(t *testing.T) {
	for _, step := range []int{1, 2, 3, 4, 7, 10, 11, 12} {
		t.Run(fmt.Sprint(step), func(t *testing.T) {
			ctx, source, base, s, r, dest := evidenceFixture(t, false)
			before := evidenceInventoryHash(t, ctx, base, r)
			p := evidencePrepare(t, ctx, s, r, dest, source)
			permit := evidencePermit(t, ctx, p)
			count := 0
			p.state.sync = func(f *os.File) error {
				count++
				if count == step {
					return io.ErrShortWrite
				}
				return f.Sync()
			}
			result, err := permit.Export(ctx)
			if err == nil || result.State != "unknown_interrupted" {
				t.Fatal(result, err, count)
			}
			if step <= 10 {
				assertNoEvidenceDestination(t, dest)
			}
			if step <= 3 {
				entries, _ := os.ReadDir(filepath.Dir(dest))
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".daimon-evidence-staging-") {
						t.Fatal("staging before durable audit")
					}
				}
			}
			if _, err = VerifyEvidence(ctx, dest); err == nil {
				t.Fatal("failed attempt presented as verified")
			}
			if evidenceInventoryHash(t, ctx, base, r) != before {
				t.Fatal("run changed")
			}
		})
	}
}

func TestEvidenceCancellationAfterSyncAndAuditFailure(t *testing.T) {
	for _, kind := range []string{"cancel", "audit_open", "audit_tamper", "rename", "late_run_change", "destination_race", "close"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, base, s, r, dest := evidenceFixture(t, false)
			p := evidencePrepare(t, ctx, s, r, dest, source)
			permit := evidencePermit(t, ctx, p)
			switch kind {
			case "cancel":
				cc, cancel := context.WithCancel(ctx)
				ctx = cc
				p.state.sync = func(f *os.File) error { e := f.Sync(); cancel(); return e }
			case "audit_open":
				p.state.openAudit = func(*os.Root, string) (*os.File, error) { return nil, os.ErrPermission }
			case "audit_tamper":
				p.state.sync = func(f *os.File) error {
					if filepath.Base(f.Name()) == evidenceAuditDirectory {
						if os.WriteFile(filepath.Join(filepath.Dir(dest), evidenceAuditDirectory, p.View().ID+".jsonl"), []byte("tampered\n"), 0600) != nil {
							t.Fatal("tamper")
						}
					}
					return f.Sync()
				}
			case "rename":
				p.state.rename = func(*os.File, string, string) error { return os.ErrPermission }
			case "late_run_change":
				p.state.rename = func(parent *os.File, a, b string) error {
					if err := evidenceRename(parent, a, b); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join(base, r.ID(), "output/README.md"), []byte("late change"), 0600)
				}
			case "destination_race":
				p.state.rename = func(parent *os.File, a, b string) error {
					if err := os.Mkdir(dest, 0700); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(dest, "sentinel"), []byte("intact"), 0600); err != nil {
						return err
					}
					return evidenceRename(parent, a, b)
				}
			case "close":
				p.state.sync = func(f *os.File) error {
					err := f.Sync()
					if filepath.Base(f.Name()) == p.View().ID+".jsonl" {
						info, _ := f.Stat()
						if info.Size() > 1100 {
							p.state.active.Close()
						}
					}
					return err
				}
			}
			result, err := permit.Export(ctx)
			if err == nil || result.State == "exported" {
				t.Fatal(result, err)
			}
			if kind == "destination_race" && string(read(t, filepath.Join(dest, "sentinel"))) != "intact" {
				t.Fatal("overwritten")
			}
		})
	}
}

func TestEvidenceBlocksIneligibleAndTamperedRuns(t *testing.T) {
	for _, kind := range []string{"manifest_missing", "manifest_duplicate", "manifest_null", "journal_missing", "journal_truncated", "plan_missing", "plan_divergent", "output_changed", "untouched_changed", "untouched_missing", "unexpected_file", "report_missing", "report_changed", "ready_lock", "ready_staging", "ready_unexpected_report", "missing", "discarded", "partial"} {
		t.Run(kind, func(t *testing.T) {
			succeeded := !strings.HasPrefix(kind, "ready_") && kind != "missing" && kind != "discarded"
			ctx, source, base, s, r, dest := evidenceFixture(t, succeeded)
			id := r.ID()
			path := filepath.Join(base, id)
			switch kind {
			case "manifest_missing":
				os.Remove(filepath.Join(path, "manifest.json"))
			case "manifest_duplicate":
				os.WriteFile(filepath.Join(path, "manifest.json"), []byte(`{"version":1,"version":1}`), 0600)
			case "manifest_null":
				os.WriteFile(filepath.Join(path, "manifest.json"), []byte(`null`), 0600)
			case "journal_truncated":
				os.WriteFile(filepath.Join(path, "artifacts/journal.jsonl"), []byte(`{"version":1`), 0600)
			case "journal_missing":
				if err := os.Remove(filepath.Join(path, "artifacts/journal.jsonl")); err != nil {
					t.Fatal(err)
				}
			case "plan_missing":
				if err := os.Remove(filepath.Join(path, "artifacts/approved-plan.json")); err != nil {
					t.Fatal(err)
				}
			case "report_missing":
				if err := os.Remove(filepath.Join(path, "artifacts/report.json")); err != nil {
					t.Fatal(err)
				}
			case "plan_divergent":
				os.WriteFile(filepath.Join(path, "artifacts/approved-plan.json"), plan(t), 0600)
			case "output_changed":
				os.WriteFile(filepath.Join(path, "output/src/config.txt"), []byte("changed"), 0600)
			case "untouched_changed":
				if err := os.WriteFile(filepath.Join(path, "output/README.md"), []byte("unapproved change"), 0600); err != nil {
					t.Fatal(err)
				}
			case "untouched_missing":
				if err := os.Remove(filepath.Join(path, "output/src/info.txt")); err != nil {
					t.Fatal(err)
				}
			case "unexpected_file":
				if err := os.WriteFile(filepath.Join(path, "output/extra.txt"), []byte("unapproved"), 0600); err != nil {
					t.Fatal(err)
				}
			case "report_changed":
				os.WriteFile(filepath.Join(path, "artifacts/report.json"), []byte(`{"version":1,"status":"succeeded","operations":[]}`), 0600)
			case "ready_lock":
				os.WriteFile(filepath.Join(path, "attempt.lock"), nil, 0600)
			case "ready_staging":
				os.WriteFile(filepath.Join(path, "manifest.next"), nil, 0600)
			case "ready_unexpected_report":
				os.WriteFile(filepath.Join(path, "artifacts/report.json"), nil, 0600)
			case "missing":
				id = strings.Repeat("a", 32)
			case "discarded":
				p, err := s.PrepareDiscard(ctx, id, true)
				if err != nil {
					t.Fatal(err)
				}
				permit, err := p.Approve(ctx, allow)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = permit.Discard(ctx); err != nil {
					t.Fatal(err)
				}
			case "partial":
				m := r.Manifest()
				m.Status = "partial"
				data, _ := json.Marshal(m)
				os.WriteFile(filepath.Join(path, "manifest.json"), data, 0600)
			}
			if p, err := s.PrepareEvidence(ctx, id, dest, source, true); err == nil {
				p.Close()
				t.Fatal("ineligible accepted")
			}
			assertNoEvidenceDestination(t, dest)
			entries, err := os.ReadDir(filepath.Dir(dest))
			if err != nil || len(entries) != 0 {
				t.Fatal("export effects", err)
			}
		})
	}
}

func TestEvidenceVerifiedPartialIsStillBlocked(t *testing.T) {
	ctx, source, base, r := fixture(t)
	p, err := r.Prepare(ctx, plan(t))
	if err != nil {
		t.Fatal(err)
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
	result, err := permit.Apply(ctx)
	if err == nil || result.Status != "partial" {
		t.Fatal(result, err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	summary, err := s.Inspect(ctx, r.ID())
	if err != nil || summary.State != "partial" || summary.Reason != "verified" {
		t.Fatal(summary, err)
	}
	parent := filepath.Join(t.TempDir(), "review")
	if err = os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(parent, "evidence")
	if p, err := s.PrepareEvidence(ctx, r.ID(), dest, source, true); !errors.Is(err, ErrEvidence) {
		if p != nil {
			p.Close()
		}
		t.Fatal(err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("partial export effects", err)
	}
}

func TestEvidenceVerificationRejectsTamperingWithoutRepair(t *testing.T) {
	for _, kind := range []string{"extra", "symlink", "hardlink", "manifest_duplicate", "manifest_null", "manifest_missing", "metadata_content", "journal_truncated", "audit_missing", "audit_truncated", "audit_duplicate", "audit_null", "audit_only_started"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, _, s, r, dest := evidenceFixture(t, true)
			p := evidencePrepare(t, ctx, s, r, dest, source)
			if _, err := evidencePermit(t, ctx, p).Export(ctx); err != nil {
				t.Fatal(err)
			}
			audit := filepath.Join(filepath.Dir(dest), evidenceAuditDirectory, p.View().ID+".jsonl")
			switch kind {
			case "extra":
				os.WriteFile(filepath.Join(dest, "output"), []byte(evidenceSecret), 0600)
			case "symlink":
				os.Remove(filepath.Join(dest, "report.json"))
				if os.Symlink(filepath.Join(source, "README.md"), filepath.Join(dest, "report.json")) != nil {
					t.Fatal("symlink")
				}
			case "hardlink":
				if os.Link(filepath.Join(dest, "report.json"), filepath.Join(filepath.Dir(dest), "alias")) != nil {
					t.Fatal("hardlink")
				}
			case "manifest_duplicate":
				os.WriteFile(filepath.Join(dest, "export-manifest.json"), []byte(`{"format_version":1,"format_version":1}`), 0600)
			case "manifest_null":
				os.WriteFile(filepath.Join(dest, "export-manifest.json"), []byte(`null`), 0600)
			case "manifest_missing":
				os.Remove(filepath.Join(dest, "export-manifest.json"))
			case "metadata_content":
				os.WriteFile(filepath.Join(dest, "approved-plan.metadata.json"), []byte(`{"content":"secret"}`), 0600)
			case "journal_truncated":
				os.WriteFile(filepath.Join(dest, "journal.jsonl"), []byte(`{"version":1`), 0600)
			case "audit_missing":
				os.Remove(audit)
			case "audit_truncated":
				os.WriteFile(audit, []byte(`{"version":1`), 0600)
			case "audit_duplicate":
				os.WriteFile(audit, []byte("{\"version\":1,\"version\":1}\n{}\n"), 0600)
			case "audit_null":
				os.WriteFile(audit, []byte("null\nnull\n"), 0600)
			case "audit_only_started":
				data := read(t, audit)
				os.WriteFile(audit, data[:bytes.IndexByte(data, '\n')+1], 0600)
			}
			if result, err := VerifyEvidence(ctx, dest); err == nil || result.State == "exported" {
				t.Fatal("adulteration accepted", result, err)
			}
		})
	}
}
