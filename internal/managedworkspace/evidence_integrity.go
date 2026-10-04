package managedworkspace

import (
	"bytes"
	"io"
	"os"
	"reflect"

	"github.com/netty-linux/daimon/internal/workspacejournal"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

func evidenceCanonical(data []byte, target any) error {
	if evidenceDecode(data, target) != nil {
		return ErrArtifact
	}
	encoded, err := metadataJSON(target)
	if err != nil || !bytes.Equal(encoded, data) {
		return ErrArtifact
	}
	return nil
}

// v1 permits at most one replacement. Its original length is determined by the
// original total minus the unchanged files. Only lengths and approved hashes
// are reconstructed, never preimage bytes. No source access is involved.
func verifyEvidenceBaseline(items []evidenceInventoryEntry, ops []evidenceOperation, m Manifest) error {
	operations := map[string]evidenceOperation{}
	replacements := 0
	for _, op := range ops {
		operations[op.Path] = op
		if op.Type == "replace_file" {
			replacements++
		}
	}
	if replacements > 1 {
		return ErrEvidence
	}
	entries := make([]snapshotHashEntry, 0, len(items))
	files, dirs, unchangedBytes, replaceIndex := 0, 0, 0, -1
	for _, item := range items {
		op := operations[item.Path]
		if op.Type == "create_file" {
			continue
		}
		e := snapshotHashEntry{item.Path, item.Type == "directory", item.Bytes, item.SHA256}
		if e.directory {
			dirs++
			e.sha256 = workspaceplan.Hash(nil)
		} else {
			files++
			if op.Type == "replace_file" {
				replaceIndex = len(entries)
				e.sha256 = op.BeforeSHA256
			} else {
				unchangedBytes += item.Bytes
			}
		}
		entries = append(entries, e)
	}
	if files != m.Files || dirs != m.Directories {
		return ErrEvidence
	}
	if replacements == 1 {
		oldBytes := m.Bytes - unchangedBytes
		if replaceIndex < 0 || oldBytes < 0 || oldBytes > MaxFileBytes {
			return ErrEvidence
		}
		entries[replaceIndex].bytes = oldBytes
	} else if unchangedBytes != m.Bytes {
		return ErrEvidence
	}
	if snapshotMetadataHash(entries) != m.Snapshot {
		return ErrEvidence
	}
	return nil
}

func validateEvidenceBundle(root *os.Root, manifest EvidenceManifest, manifestHash string) error {
	if manifest.FormatVersion != 1 || manifest.ExportKind != "evidence" || !validID(manifest.ExportID) || !validID(manifest.RunID) || manifest.Created.IsZero() || manifest.Integrity != "verified" || manifest.ContainsFileContent || manifest.ContainsPatchContent || manifest.SecretFreeGuarantee != "not_applicable" || !manifest.SourceNotModified || !manifest.ContentExportNotIncluded || manifest.MaxFiles != 6 || manifest.MaxBytes != MaxEvidenceBytes || len(manifest.Artifacts) != 5 || !reflect.DeepEqual(manifest.Warnings, evidenceWarnings) {
		return ErrArtifact
	}
	present := manifest.RunState == "succeeded"
	if (!present && manifest.RunState != "ready") || manifest.JournalPresent != present || manifest.PlanPresent != present || manifest.ReportPersisted != present {
		return ErrArtifact
	}
	dir, err := openRead(root, ".", true)
	if err != nil {
		return ErrArtifact
	}
	entries, e := dir.ReadDir(7)
	closed := dir.Close()
	if (e != nil && e != io.EOF) || closed != nil || len(entries) != 6 {
		return ErrArtifact
	}
	files := make(map[string][]byte, 6)
	total := 0
	for i, name := range evidencePaths {
		data, e := readEvidenceArtifact(root, name)
		if e != nil {
			return e
		}
		total += len(data)
		if total > MaxEvidenceBytes {
			return ErrLimit
		}
		if i < 5 {
			artifact := manifest.Artifacts[i]
			if artifact.Path != name || artifact.Bytes != len(data) || artifact.SHA256 != workspaceplan.Hash(data) {
				return ErrArtifact
			}
		} else {
			var actual EvidenceManifest
			if evidenceCanonical(data, &actual) != nil || !reflect.DeepEqual(manifest, actual) || workspaceplan.Hash(data) != manifestHash {
				return ErrArtifact
			}
		}
		files[name] = data
	}
	var run evidenceRunManifest
	var report evidenceReport
	var plan evidenceOperations
	var inventory evidenceInventory
	if evidenceCanonical(files[evidencePaths[0]], &run) != nil || evidenceCanonical(files[evidencePaths[2]], &report) != nil || evidenceCanonical(files[evidencePaths[3]], &plan) != nil || evidenceCanonical(files[evidencePaths[4]], &inventory) != nil {
		return ErrArtifact
	}
	if run.Version != 1 || run.RunID != manifest.RunID || run.Created.IsZero() || run.Created.After(manifest.Created) || !validHash(run.SourceReference) || !validHash(run.Snapshot) || run.Files < 0 || run.Files > MaxFiles || run.Directories < 0 || run.Directories > MaxDirectories || run.Bytes < 0 || run.Bytes > MaxTotalBytes || run.Status != manifest.RunState || report.Version != 1 || report.Status != manifest.RunState || plan.Version != 1 || plan.Present != present || !reflect.DeepEqual(plan.Operations, report.Operations) || plan.Operations == nil || len(plan.Operations) > 2 || inventory.Version != 1 || inventory.Entries == nil || len(inventory.Entries) > MaxEvidenceEntries {
		return ErrArtifact
	}
	if (present && len(plan.Operations) == 0) || (!present && len(plan.Operations) != 0) {
		return ErrArtifact
	}
	seen := map[string]evidenceInventoryEntry{}
	for _, entry := range inventory.Entries {
		if !workspaceplan.ValidPath(entry.Path, workspaceplan.DefaultLimits()) || seen[entry.Path].Path != "" || entry.Bytes < 0 || entry.Bytes > MaxFileBytes {
			return ErrArtifact
		}
		switch entry.Type {
		case "file":
			if entry.Mode != "0600" || !validHash(entry.SHA256) {
				return ErrArtifact
			}
		case "directory":
			if entry.Mode != "0700" || entry.SHA256 != "" || entry.Bytes != 0 {
				return ErrArtifact
			}
		default:
			return ErrArtifact
		}
		seen[entry.Path] = entry
	}
	metadata := make([]workspacejournal.Metadata, 0, 2)
	operations := map[string]evidenceOperation{}
	creates, replaces := 0, 0
	for i, op := range plan.Operations {
		if op.Order != i+1 || !workspaceplan.ValidPath(op.Path, workspaceplan.DefaultLimits()) || operations[op.Path].Path != "" || op.Status != "succeeded" || !validHash(op.AfterSHA256) || op.AfterBytes < 0 || op.AfterBytes > MaxFileBytes {
			return ErrArtifact
		}
		if op.Type == "create_file" {
			creates++
			if op.BeforeSHA256 != "" {
				return ErrArtifact
			}
		} else if op.Type == "replace_file" {
			replaces++
			if !validHash(op.BeforeSHA256) {
				return ErrArtifact
			}
		} else {
			return ErrArtifact
		}
		entry := seen[op.Path]
		if entry.Type != "file" || entry.SHA256 != op.AfterSHA256 || entry.Bytes != op.AfterBytes {
			return ErrArtifact
		}
		operations[op.Path] = op
		metadata = append(metadata, workspacejournal.Metadata{Type: op.Type, Path: op.Path, BeforeSHA256: op.BeforeSHA256, AfterSHA256: op.AfterSHA256})
	}
	if creates > 1 || replaces > 1 {
		return ErrArtifact
	}
	if verifyEvidenceBaseline(inventory.Entries, plan.Operations, Manifest{Snapshot: run.Snapshot, Files: run.Files, Directories: run.Directories, Bytes: run.Bytes}) != nil {
		return ErrArtifact
	}
	journal := files[evidencePaths[1]]
	if !present {
		if len(journal) != 0 {
			return ErrArtifact
		}
		return nil
	}
	if len(journal) == 0 || journal[len(journal)-1] != '\n' {
		return ErrArtifact
	}
	lines := bytes.Split(journal[:len(journal)-1], []byte{'\n'})
	if len(lines) > 8 {
		return ErrArtifact
	}
	checker, err := workspacejournal.New(io.Discard, metadata)
	if err != nil {
		return ErrArtifact
	}
	lastOrder := 0
	states := map[string]string{}
	for i, line := range lines {
		var rec evidenceJournalRecord
		if evidenceCanonical(append(append([]byte(nil), line...), '\n'), &rec) != nil || rec.Version != 1 || rec.Sequence != i+1 || rec.Timestamp.IsZero() {
			return ErrArtifact
		}
		op := operations[rec.Operation.Path]
		if op.Order == 0 || op.Order < lastOrder || rec.Operation.Type != op.Type || rec.Operation.BeforeSHA256 != op.BeforeSHA256 || rec.Operation.AfterSHA256 != op.AfterSHA256 || checker.Append(op.Path, rec.Status) != nil {
			return ErrArtifact
		}
		lastOrder = op.Order
		states[op.Path] = rec.Status
	}
	for _, op := range plan.Operations {
		if states[op.Path] != "succeeded" {
			return ErrArtifact
		}
	}
	return nil
}

// Kept explicit: no output, pre/postimage or patch artifact is admissible.
func evidenceOnlyName(name string) bool {
	for _, allowed := range evidencePaths {
		if name == allowed {
			return true
		}
	}
	return false
}
