package managedworkspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/netty-linux/daimon/internal/workspaceplan"
)

// Fixed metadata, no free-form errors, content, prompts or absolute paths.
type evidenceAuditRecord struct {
	Version         int       `json:"version"`
	Sequence        int       `json:"sequence"`
	Timestamp       time.Time `json:"timestamp"`
	ExportID        string    `json:"export_id"`
	RunID           string    `json:"run_id"`
	RunState        string    `json:"run_state"`
	Integrity       string    `json:"integrity"`
	Approval        string    `json:"approval"`
	DestinationName string    `json:"destination_name"`
	StoreReference  string    `json:"store_reference_sha256"`
	RunBinding      string    `json:"run_binding_sha256"`
	PreviewHash     string    `json:"preview_sha256"`
	ManifestHash    string    `json:"manifest_sha256"`
	PackageHash     string    `json:"package_sha256"`
	Files           int       `json:"files"`
	Bytes           int       `json:"bytes"`
	Status          string    `json:"status"`
}

func evidencePackageHash(files []evidenceFile) string {
	var binding bytes.Buffer
	for _, f := range files {
		binding.WriteString(f.path)
		binding.WriteByte(0)
		binding.WriteString(workspaceplan.Hash(f.data))
		binding.WriteByte(0)
	}
	return workspaceplan.Hash(binding.Bytes())
}
func evidenceAuditDecode(data []byte, v *evidenceAuditRecord) error {
	allowed := map[string]bool{}
	for _, k := range []string{"version", "sequence", "timestamp", "export_id", "run_id", "run_state", "integrity", "approval", "destination_name", "store_reference_sha256", "run_binding_sha256", "preview_sha256", "manifest_sha256", "package_sha256", "files", "bytes", "status"} {
		allowed[k] = true
	}
	return strictJSON(data, v, allowed)
}
func (d *evidenceState) auditRecord(sequence int, status string) evidenceAuditRecord {
	return evidenceAuditRecord{Version: 1, Sequence: sequence, Timestamp: time.Now().UTC(), ExportID: d.bundle.manifest.ExportID, RunID: d.run.ID(), RunState: d.bundle.manifest.RunState, Integrity: "verified", Approval: "single_use_granted", DestinationName: d.destination.name, StoreReference: workspaceplan.Hash([]byte(d.store.base)), RunBinding: d.bundle.binding, PreviewHash: d.view.ProposedSHA256, ManifestHash: workspaceplan.Hash(d.bundle.files[5].data), PackageHash: evidencePackageHash(d.bundle.files), Files: 6, Bytes: d.bundle.total, Status: status}
}
func (d *evidenceState) startEvidenceAudit() (*os.File, error) {
	// Audit outside the store/run, synchronized before staging or publication.
	root := d.destination.root
	if err := root.Mkdir(evidenceAuditDirectory, 0700); err != nil && !os.IsExist(err) {
		return nil, ErrArtifact
	}
	if checkPrivate(filepath.Join(d.destination.parent, evidenceAuditDirectory)) != nil {
		return nil, ErrPrivate
	}
	name := evidenceAuditDirectory + "/" + d.bundle.manifest.ExportID + ".jsonl"
	f, err := d.openAudit(root, name)
	if err != nil {
		return nil, ErrArtifact
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil || !safeRegular(info) {
		return nil, ErrArtifact
	}
	d.auditIdentity = identityInfo(info)
	if err = d.appendEvidenceAudit(f, 1, "started"); err != nil {
		return nil, err
	}
	if err = evidenceSyncDirectory(root, evidenceAuditDirectory, d.sync); err != nil {
		return nil, err
	}
	if err = evidenceSyncDirectory(root, ".", d.sync); err != nil {
		return nil, err
	}
	ok = true
	return f, nil
}
func (d *evidenceState) appendEvidenceAudit(f *os.File, sequence int, status string) error {
	data, err := metadataJSON(d.auditRecord(sequence, status))
	if err != nil {
		return err
	}
	n, err := f.Write(data)
	if err != nil || n != len(data) {
		return ErrArtifact
	}
	d.auditSequence = sequence
	if err = d.sync(f); err != nil {
		return ErrArtifact
	}
	if sequence == 1 {
		d.auditHash = workspaceplan.Hash(data)
	}
	return nil
}
func (d *evidenceState) checkEvidenceAudit(f *os.File) error {
	if err := d.contextError(d.ctx); err != nil {
		return err
	}
	if checkPrivate(filepath.Join(d.destination.parent, evidenceAuditDirectory)) != nil {
		return ErrPrivate
	}
	info, err := f.Stat()
	if err != nil || !safeRegular(info) || identityInfo(info) != d.auditIdentity {
		return ErrArtifact
	}
	data, err := readEvidenceArtifact(d.destination.root, evidenceAuditDirectory+"/"+d.bundle.manifest.ExportID+".jsonl")
	if err != nil || workspaceplan.Hash(data) != d.auditHash {
		return ErrArtifact
	}
	named, err := d.destination.root.Lstat(evidenceAuditDirectory + "/" + d.bundle.manifest.ExportID + ".jsonl")
	if err != nil || !os.SameFile(info, named) {
		return ErrArtifact
	}
	return nil
}

// VerifyEvidence is read-only: no repair, store access, content import or cleanup.
// This verifies current evidence, not authenticity against the trusted UID.
func VerifyEvidence(ctx context.Context, destination string) (result EvidenceResult, resultErr error) {
	result.State = "invalid"
	root, parent, err := openEvidencePackage(ctx, destination)
	if err != nil {
		return result, err
	}
	defer func() {
		if root.Close() != nil {
			result.State = "invalid"
			resultErr = ErrArtifact
		}
		if parent.Close() != nil {
			result.State = "invalid"
			resultErr = ErrArtifact
		}
	}()
	first, err := root.Stat(".")
	if err != nil {
		return result, ErrArtifact
	}
	data, err := readEvidenceArtifact(root, "export-manifest.json")
	if err != nil {
		return result, err
	}
	var manifest EvidenceManifest
	if evidenceDecode(data, &manifest) != nil {
		return result, ErrArtifact
	}
	if err = validateEvidenceBundle(root, manifest, workspaceplan.Hash(data)); err != nil {
		return result, err
	}
	auditDir := filepath.Join(filepath.Dir(destination), evidenceAuditDirectory)
	if checkPrivate(auditDir) != nil {
		return result, ErrArtifact
	}
	audit, err := readEvidenceArtifact(parent, evidenceAuditDirectory+"/"+manifest.ExportID+".jsonl")
	if err != nil || len(audit) == 0 || audit[len(audit)-1] != '\n' {
		return result, ErrArtifact
	}
	lines := bytes.Split(audit[:len(audit)-1], []byte{'\n'})
	if len(lines) != 2 {
		return result, ErrArtifact
	}
	var a, b evidenceAuditRecord
	if evidenceAuditDecode(lines[0], &a) != nil || evidenceAuditDecode(lines[1], &b) != nil {
		return result, ErrArtifact
	}
	files := make([]evidenceFile, 0, 6)
	total := 0
	for _, name := range evidencePaths {
		content, e := readEvidenceArtifact(root, name)
		if e != nil {
			return result, e
		}
		files = append(files, evidenceFile{name, content})
		total += len(content)
	}
	if a.Version != 1 || b.Version != 1 || a.Sequence != 1 || b.Sequence != 2 || a.Status != "started" || b.Status != "exported" || a.Timestamp.IsZero() || b.Timestamp.Before(a.Timestamp) || a.Timestamp.Before(manifest.Created) {
		return result, ErrArtifact
	}
	for _, rec := range []evidenceAuditRecord{a, b} {
		if rec.ExportID != manifest.ExportID || rec.RunID != manifest.RunID || rec.RunState != manifest.RunState || rec.Integrity != "verified" || rec.Approval != "single_use_granted" || rec.DestinationName != filepath.Base(destination) || rec.ManifestHash != workspaceplan.Hash(data) || rec.PackageHash != evidencePackageHash(files) || rec.Files != 6 || rec.Bytes != total || !validHash(rec.StoreReference) || !validHash(rec.RunBinding) || !validHash(rec.PreviewHash) {
			return result, ErrArtifact
		}
	}
	a.Sequence = b.Sequence
	a.Timestamp = b.Timestamp
	a.Status = b.Status
	if a != b {
		return result, ErrArtifact
	}
	named, err := os.Lstat(destination)
	if err != nil || !os.SameFile(first, named) || checkPrivate(destination) != nil || checkPrivate(filepath.Dir(destination)) != nil {
		return result, ErrPrivate
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	return EvidenceResult{"exported", 6, total}, nil
}
