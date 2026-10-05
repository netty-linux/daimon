package managedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"path/filepath"
	"time"

	"github.com/netty-linux/daimon/internal/workspaceplan"
)

// ApplyOptions is an explicit operator decision, never part of a model plan.
type preimageError struct{ cause error }

func (e *preimageError) Error() string        { return ErrArtifact.Error() }
func (e *preimageError) Unwrap() error        { return e.cause }
func (e *preimageError) Is(target error) bool { return target == ErrArtifact }

type ApplyOptions struct{ RetainPreimages, ReplaceEnabled bool }

// RetentionSummary is metadata-only. No internal filenames are public.
type RetentionSummary struct {
	Version        int    `json:"preimage_format_version"`
	Requested      bool   `json:"retention_requested"`
	Count          int    `json:"preimage_count"`
	State          string `json:"retention_state"`
	JournalSHA256  string `json:"preimage_journal_sha256,omitempty"`
	Expected       string `json:"expected_before_sha256,omitempty"`
	Captured       string `json:"captured_before_sha256,omitempty"`
	After          string `json:"approved_after_sha256,omitempty"`
	Size           int    `json:"preimage_size_bytes"`
	OperationID    string `json:"operation_id,omitempty"`
	PlanSHA256     string `json:"plan_sha256,omitempty"`
	ApprovalSHA256 string `json:"approval_sha256,omitempty"`
}

type preimageMetadata struct {
	Version         int       `json:"preimage_format_version"`
	RunID           string    `json:"run_id"`
	OperationID     string    `json:"operation_id"`
	Index           int       `json:"operation_index"`
	Path            string    `json:"relative_path"`
	Expected        string    `json:"expected_before_sha256"`
	Captured        string    `json:"captured_before_sha256"`
	After           string    `json:"approved_after_sha256"`
	Size            int       `json:"preimage_size_bytes"`
	StorageID       string    `json:"storage_id"`
	StorageIdentity Identity  `json:"storage_identity"`
	StorageChanged  string    `json:"storage_change_id"`
	Timestamp       time.Time `json:"capture_timestamp_utc"`
	PlanSHA256      string    `json:"plan_sha256"`
	ApprovalSHA256  string    `json:"approval_sha256"`
	JournalSequence int       `json:"journal_sequence"`
	State           string    `json:"integrity_state"`
}

func preimageID(runID, planHash string, index int) string {
	return workspaceplan.Hash([]byte(fmt.Sprintf("preimage-v1:%s:%s:%d", runID, planHash, index)))
}

func retentionValid(s *RetentionSummary) bool {
	if s == nil {
		return true
	}
	if s.Version != 1 || !s.Requested || s.Count < 0 || s.Count > 1 {
		return false
	}
	// No capture bindings are published until a complete capture is verified.
	// Incomplete summaries must not accept unbound IDs or other free-form data.
	empty := s.Count == 0 && s.Size == 0 && s.JournalSHA256 == "" && s.Expected == "" && s.Captured == "" && s.After == "" && s.OperationID == "" && s.PlanSHA256 == "" && s.ApprovalSHA256 == ""
	switch s.State {
	case "not_requested", "capturing", "captured", "persisted":
		return empty
	case "failed", "unknown":
		return empty || (s.Count == 1 && validHash(s.JournalSHA256) && validHash(s.Expected) && s.Captured == s.Expected && validHash(s.After) && s.Size >= 0 && s.Size <= MaxFileBytes && validHash(s.OperationID) && validHash(s.PlanSHA256) && validHash(s.ApprovalSHA256))
	case "verified":
		return s.Count == 1 && validHash(s.JournalSHA256) && validHash(s.Expected) && s.Captured == s.Expected && validHash(s.After) && s.Size >= 0 && s.Size <= MaxFileBytes && validHash(s.OperationID) && validHash(s.PlanSHA256) && validHash(s.ApprovalSHA256)
	}
	return false
}

func (p *Proposal) preimageStep(ctx context.Context, phase string) error {
	if err := errors.Join(ctx.Err(), p.runContext.Err()); err != nil {
		return err
	}
	if p.preimageHook != nil {
		if err := p.preimageHook(phase); err != nil {
			return &preimageError{cause: err}
		}
	}
	return errors.Join(ctx.Err(), p.runContext.Err())
}

// captureRead opens only private regular files below a pinned run. Every close
// and before/after version check is part of the accepted result.
func (r *Run) captureRead(name string, limit int) ([]byte, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	if checkPrivate(filepath.Join(r.directory, filepath.Dir(filepath.FromSlash(name)))) != nil {
		return nil, ErrPrivate
	}
	i, err := r.root.Lstat(name)
	if err != nil || !safeRegular(i) || i.Size() < 0 || i.Size() > int64(limit) {
		return nil, ErrArtifact
	}
	f, err := openRead(r.root, name, false)
	if err != nil {
		return nil, ErrArtifact
	}
	opened, statErr := f.Stat()
	if statErr != nil || !safeRegular(opened) || entryVersion(i) != entryVersion(opened) {
		f.Close()
		return nil, ErrArtifact
	}
	data, readErr := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	end, endErr := f.Stat()
	closeErr := f.Close()
	current, pathErr := r.root.Lstat(name)
	if readErr != nil || endErr != nil || closeErr != nil || pathErr != nil ||
		entryVersion(i) != entryVersion(end) || entryVersion(i) != entryVersion(current) || len(data) != int(i.Size()) || len(data) > limit {
		return nil, ErrArtifact
	}
	return data, r.check()
}

func (p *Proposal) preimageWrite(ctx context.Context, name string, data []byte) error {
	if err := p.preimageStep(ctx, "open:"+name); err != nil {
		return err
	}
	f, err := openPreimageNew(p.run.root, name)
	if err != nil {
		return ErrArtifact
	}
	var cause error
	if cause = p.preimageStep(ctx, "write:"+name); cause == nil {
		var n int
		n, cause = f.Write(data)
		if cause == nil && n != len(data) {
			cause = io.ErrShortWrite
		}
	}
	if cause == nil {
		cause = p.preimageStep(ctx, "sync:"+name)
	}
	if cause == nil {
		cause = f.Sync()
	}
	// Always close, even when an earlier phase failed.
	closeErr := f.Close()
	cause = errors.Join(cause, closeErr, p.preimageStep(ctx, "close:"+name))
	if cause != nil {
		return &preimageError{cause: cause}
	}
	dir, err := openRead(p.run.root, filepath.ToSlash(filepath.Dir(name)), true)
	if err != nil {
		return ErrArtifact
	}
	cause = p.preimageStep(ctx, "directory_sync:"+name)
	if cause == nil {
		cause = dir.Sync()
	}
	cause = errors.Join(cause, dir.Close(), p.preimageStep(ctx, "directory_close:"+name))
	if e := errors.Join(cause, p.run.check()); e != nil {
		return &preimageError{cause: e}
	}
	return nil
}

// capturePreimage runs only after approval and the persisted execution started
// entry. No content is added to the public journal or approval display.
func (p *Proposal) capturePreimage(ctx context.Context, index int) error {
	r := p.run
	op := p.plan.Operations[index]
	if op.Type != "replace_file" || p.retention == nil {
		return ErrState
	}
	p.retention.State = "capturing"
	if err := p.preimageStep(ctx, "capture"); err != nil {
		return err
	}
	targetBefore, statErr := r.root.Lstat("output/" + op.Path)
	if statErr != nil {
		return ErrArtifact
	}
	data, err := r.captureRead("output/"+op.Path, MaxFileBytes)
	if err != nil || workspaceplan.Hash(data) != op.Precondition.SHA256 {
		return ErrArtifact
	}
	p.retention.State = "captured"
	if err := p.preimageStep(ctx, "mkdir"); err != nil {
		return err
	}
	if r.root.Mkdir("preimages", 0700) != nil {
		return ErrArtifact
	}
	if checkPrivate(filepath.Join(r.directory, "preimages")) != nil {
		return ErrPrivate
	}
	if err := r.syncDirectory("."); err != nil {
		return err
	}
	id := preimageID(r.manifest.RunID, workspaceplan.Hash(p.planBytes), index)
	name := "preimages/" + id + ".bin"
	if err := p.preimageWrite(ctx, name, data); err != nil {
		return err
	}
	p.retention.State = "persisted"
	if err := p.preimageStep(ctx, "reopen"); err != nil {
		return err
	}
	stored, err := r.captureRead(name, MaxFileBytes)
	if err != nil || !bytes.Equal(stored, data) {
		return ErrArtifact
	}
	info, e := r.root.Lstat(name)
	if e != nil {
		return ErrArtifact
	}
	m := preimageMetadata{Version: 1, RunID: r.manifest.RunID, OperationID: id, Index: index, Path: op.Path, Expected: op.Precondition.SHA256, Captured: workspaceplan.Hash(stored), After: op.Validation.SHA256, Size: len(stored), StorageID: id, StorageIdentity: identityInfo(info), StorageChanged: changeInfo(info), Timestamp: time.Now().UTC(), PlanSHA256: workspaceplan.Hash(p.planBytes), ApprovalSHA256: p.view.ID, JournalSequence: 2*index + 1, State: "persisted"}
	encoded, err := json.Marshal(m)
	if err != nil {
		return ErrArtifact
	}
	// The capture record is synced/closed before verified is declared anywhere.
	journal := append(encoded, '\n')
	if err := p.preimageWrite(ctx, "artifacts/preimage-journal.jsonl", journal); err != nil {
		return err
	}
	m.State = "verified"
	encoded, err = json.Marshal(m)
	if err != nil {
		return ErrArtifact
	}
	if err := p.preimageWrite(ctx, "preimages/"+id+".json", append(encoded, '\n')); err != nil {
		return err
	}
	if err := p.preimageStep(ctx, "verify"); err != nil {
		return err
	}
	if err := r.verifyPreimage(m, p.planBytes, p.view.ID); err != nil {
		return err
	}
	p.retention.State = "verified"
	p.retention.Count = 1
	p.retention.JournalSHA256 = workspaceplan.Hash(journal)
	p.retention.Expected = m.Expected
	p.retention.Captured = m.Captured
	p.retention.After = m.After
	p.retention.Size = m.Size
	p.retention.OperationID = m.OperationID
	p.retention.PlanSHA256 = m.PlanSHA256
	p.retention.ApprovalSHA256 = m.ApprovalSHA256
	r.manifest.Retention = copyRetention(p.retention)
	if err := r.saveManifest(); err != nil {
		return err
	}
	if err := p.preimageStep(ctx, "before_replace"); err != nil {
		return err
	}
	current, err := r.captureRead("output/"+op.Path, MaxFileBytes)
	if err != nil || workspaceplan.Hash(current) != m.Expected {
		return ErrArtifact
	}
	targetAfter, e := r.root.Lstat("output/" + op.Path)
	if e != nil || entryVersion(targetBefore) != entryVersion(targetAfter) {
		return ErrArtifact
	}
	if e := p.preimageStep(ctx, "journal_sync"); e != nil {
		return e
	}
	if r.journalFile == nil || r.journalFile.Sync() != nil {
		return ErrArtifact
	}
	// Re-read every persisted binding, not just the captured bytes. The operation
	// is still started; no report has been published and no replace has occurred.
	pending := p.newReport("unknown")
	pending.Retention = copyRetention(p.retention)
	for i := 0; i < index; i++ {
		pending.Operations[i].Status = "succeeded"
	}
	pending.Operations[index].Status = "unknown"
	return r.verifyReport(pending)
}

func copyRetention(s *RetentionSummary) *RetentionSummary {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

func decodePreimage(data []byte) (preimageMetadata, error) {
	keys := map[string]bool{}
	for _, k := range []string{"preimage_format_version", "run_id", "operation_id", "operation_index", "relative_path", "expected_before_sha256", "captured_before_sha256", "approved_after_sha256", "preimage_size_bytes", "storage_id", "storage_identity", "storage_change_id", "device", "inode", "capture_timestamp_utc", "plan_sha256", "approval_sha256", "journal_sequence", "integrity_state"} {
		keys[k] = true
	}
	var m preimageMetadata
	if len(data) > 8192 || strictJSON(data, &m, keys) != nil {
		return m, ErrArtifact
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return m, ErrArtifact
	}
	for _, key := range []string{"preimage_format_version", "run_id", "operation_id", "operation_index", "relative_path", "expected_before_sha256", "captured_before_sha256", "approved_after_sha256", "preimage_size_bytes", "storage_id", "storage_identity", "storage_change_id", "capture_timestamp_utc", "plan_sha256", "approval_sha256", "journal_sequence", "integrity_state"} {
		if _, ok := fields[key]; !ok {
			return m, ErrArtifact
		}
	}

	if m.Version != 1 || !validID(m.RunID) || !validHash(m.OperationID) || m.Index < 0 || m.Index > 1 || !workspaceplan.ValidPath(m.Path, workspaceplan.DefaultLimits()) || !validHash(m.Expected) || m.Captured != m.Expected || !validHash(m.After) || m.Size < 0 || m.Size > MaxFileBytes || m.StorageChanged == "" || m.StorageIdentity.Inode == 0 || m.StorageID != m.OperationID || m.Timestamp.IsZero() || !validHash(m.PlanSHA256) || !validHash(m.ApprovalSHA256) || m.JournalSequence != 2*m.Index+1 || (m.State != "persisted" && m.State != "verified") {
		return m, ErrArtifact
	}
	return m, nil
}

func (r *Run) verifyPreimage(m preimageMetadata, planBytes []byte, approval string) error {
	plan, err := workspaceplan.Parse(planBytes, workspaceplan.DefaultLimits())
	if err != nil || m.Index >= len(plan.Operations) || m.RunID != r.manifest.RunID || m.PlanSHA256 != workspaceplan.Hash(planBytes) || m.OperationID != preimageID(r.manifest.RunID, m.PlanSHA256, m.Index) || m.ApprovalSHA256 != approval {
		return ErrArtifact
	}
	op := plan.Operations[m.Index]
	if op.Type != "replace_file" || op.Path != m.Path || op.Precondition.SHA256 != m.Expected || op.Validation.SHA256 != m.After {
		return ErrArtifact
	}
	info, e := r.root.Lstat("preimages/" + m.StorageID + ".bin")
	if e != nil || identityInfo(info) != m.StorageIdentity || changeInfo(info) != m.StorageChanged {
		return ErrArtifact
	}
	data, err := r.captureRead("preimages/"+m.StorageID+".bin", MaxFileBytes)
	if err != nil || len(data) != m.Size || workspaceplan.Hash(data) != m.Captured {
		return ErrArtifact
	}
	return nil
}

// Independent integrity evidence; never repair or read the source.
func (r *Run) verifyRetention(report Report, planBytes []byte) error {
	s := r.manifest.Retention
	if s == nil {
		for _, name := range []string{"artifacts/preimage-journal.jsonl", "artifacts/retention-approval.json"} {
			absent, e := r.artifactAbsent(name)
			if e != nil || !absent {
				return ErrArtifact
			}
		}
		absent, e := r.artifactAbsent("preimages")
		if e != nil || !absent {
			return ErrArtifact
		}
		return nil
	}
	if !retentionValid(s) || report.Retention == nil || *report.Retention != *s {
		return ErrArtifact
	}
	if s.State != "verified" {
		if report.Status == "succeeded" {
			return ErrArtifact
		}
		return nil
	}
	journal, err := r.captureRead("artifacts/preimage-journal.jsonl", 8192)
	if err != nil || workspaceplan.Hash(journal) != s.JournalSHA256 {
		return ErrArtifact
	}
	m, err := decodePreimage(journal)
	if err != nil || m.State != "persisted" || s.Expected != m.Expected || s.Captured != m.Captured || s.After != m.After || s.Size != m.Size || s.OperationID != m.OperationID || s.PlanSHA256 != m.PlanSHA256 || s.ApprovalSHA256 != m.ApprovalSHA256 {
		return ErrArtifact
	}
	meta, err := r.captureRead("preimages/"+m.StorageID+".json", 8192)
	if err != nil {
		return ErrArtifact
	}
	verified, err := decodePreimage(meta)
	if err != nil || verified.State != "verified" {
		return ErrArtifact
	}
	verified.State = "persisted"
	if verified != m {
		return ErrArtifact
	}
	if err := r.verifyPreimage(m, planBytes, m.ApprovalSHA256); err != nil {
		return err
	}
	// The approved display digest is persisted separately and bound to the plan.
	approval, err := r.captureRead("artifacts/retention-approval.json", 4096)
	var binding struct {
		Plan     string `json:"plan_sha256"`
		Approval string `json:"approval_sha256"`
		Run      string `json:"run_id"`
	}
	if err != nil || strictJSON(approval, &binding, map[string]bool{"plan_sha256": true, "approval_sha256": true, "run_id": true}) != nil || binding.Plan != m.PlanSHA256 || binding.Approval != m.ApprovalSHA256 || binding.Run != m.RunID {
		return ErrArtifact
	}
	regular, err := r.captureRead("artifacts/journal.jsonl", 32768)
	if err != nil {
		return ErrArtifact
	}
	lines := bytes.Split(bytes.TrimSuffix(regular, []byte{'\n'}), []byte{'\n'})
	if m.JournalSequence > len(lines) {
		return ErrArtifact
	}
	var entry struct {
		Sequence  int    `json:"sequence"`
		Status    string `json:"status"`
		Operation struct {
			Path   string `json:"path"`
			Before string `json:"before_sha256"`
			After  string `json:"after_sha256"`
			Type   string `json:"type"`
		} `json:"operation"`
		Version   int       `json:"version"`
		Timestamp time.Time `json:"timestamp"`
	}
	if strictMetadata(lines[m.JournalSequence-1], &entry) != nil || entry.Sequence != m.JournalSequence || entry.Status != "started" || entry.Operation.Path != m.Path || entry.Operation.Before != m.Expected || entry.Operation.After != m.After || entry.Operation.Type != "replace_file" {
		return ErrArtifact
	}
	return nil
}
