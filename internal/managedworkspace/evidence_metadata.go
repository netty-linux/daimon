package managedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/netty-linux/daimon/internal/workspacejournal"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

var ErrEvidence = errors.New("exportação de evidências recusada ou interrompida")

const MaxEvidenceBytes = 256 * 1024
const MaxEvidenceFileBytes = 64 * 1024
const evidenceAuditDirectory = ".daimon-evidence-audit"
const MaxEvidenceEntries = 32

var evidenceWarnings = []string{"Exportar não é publicar na source.", "Sem conteúdo de arquivo, output, patch, preimagem, prompts ou respostas de provider.", "UID proprietário, processos do mesmo UID e administradores são confiáveis.", "Sem rollback, replay, retomada ou limpeza automática; sem prova de durabilidade contra queda de energia."}

type evidenceInventoryEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	SHA256 string `json:"output_sha256,omitempty"`
	Bytes  int    `json:"file_size_bytes"`
	Mode   string `json:"mode"`
}
type evidenceInventory struct {
	Version int                      `json:"version"`
	Entries []evidenceInventoryEntry `json:"entries"`
}

// These export types deliberately have no file-content or free-form plan fields.
type evidenceOperation struct {
	Order        int    `json:"order"`
	Type         string `json:"type"`
	Path         string `json:"path"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256"`
	Status       string `json:"status"`
	AfterBytes   int    `json:"after_bytes"`
}
type evidenceOperations struct {
	Version    int                 `json:"version"`
	Present    bool                `json:"present"`
	Operations []evidenceOperation `json:"operations"`
}
type evidenceRunManifest struct {
	Version         int       `json:"version"`
	RunID           string    `json:"run_id"`
	Created         time.Time `json:"created"`
	SourceReference string    `json:"source_reference_sha256"`
	Snapshot        string    `json:"snapshot_sha256"`
	Files           int       `json:"files"`
	Directories     int       `json:"directories"`
	Bytes           int       `json:"total_size_bytes"`
	Status          string    `json:"status"`
}
type evidenceReport struct {
	Version    int                 `json:"version"`
	Status     string              `json:"status"`
	Operations []evidenceOperation `json:"operations"`
}
type evidenceJournalRecord struct {
	Version   int                      `json:"version"`
	Sequence  int                      `json:"sequence"`
	Timestamp time.Time                `json:"timestamp"`
	Operation evidenceJournalOperation `json:"operation"`
	Status    string                   `json:"status"`
}
type evidenceJournalOperation struct {
	Type         string `json:"type"`
	Path         string `json:"path"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256"`
}
type EvidenceArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"artifact_sha256"`
	Bytes  int    `json:"artifact_size_bytes"`
}
type EvidenceManifest struct {
	FormatVersion            int                `json:"format_version"`
	ExportKind               string             `json:"export_kind"`
	ExportID                 string             `json:"export_id"`
	RunID                    string             `json:"run_id"`
	Created                  time.Time          `json:"created_at_utc"`
	RunState                 string             `json:"run_state"`
	Integrity                string             `json:"integrity"`
	ContainsFileContent      bool               `json:"contains_file_content"`
	ContainsPatchContent     bool               `json:"contains_patch_content"`
	SecretFreeGuarantee      string             `json:"secret_free_guarantee"`
	SourceNotModified        bool               `json:"source_not_modified"`
	ContentExportNotIncluded bool               `json:"content_export_not_included"`
	JournalPresent           bool               `json:"journal_present"`
	PlanPresent              bool               `json:"approved_plan_present"`
	ReportPersisted          bool               `json:"report_persisted"`
	Artifacts                []EvidenceArtifact `json:"artifacts"`
	MaxFiles                 int                `json:"max_files"`
	MaxBytes                 int                `json:"max_bytes"`
	Warnings                 []string           `json:"warnings"`
}

type evidenceFile struct {
	path string
	data []byte
}
type evidenceBundle struct {
	files    []evidenceFile
	binding  string
	total    int
	manifest EvidenceManifest
}

var evidencePaths = []string{"run-manifest.json", "journal.jsonl", "report.json", "approved-plan.metadata.json", "inventory.metadata.json", "export-manifest.json"}

func metadataJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || len(b)+1 > MaxEvidenceFileBytes {
		return nil, ErrLimit
	}
	return append(b, '\n'), nil
}

func (s *Store) buildEvidence(ctx context.Context, r *Run, exportID string, created time.Time) (evidenceBundle, error) {
	var bundle evidenceBundle
	summary, err := s.Inspect(ctx, r.ID())
	if err != nil {
		return bundle, err
	}
	if summary.Reason != "verified" || summary.Staging || !summary.ManifestVerified || !summary.InventoryVerified || (summary.State != "ready" && summary.State != "succeeded") {
		return bundle, ErrEvidence
	}
	report, err := r.Report()
	if err != nil {
		return bundle, ErrEvidence
	}
	m := r.Manifest()
	// Reuse the output snapshot validator even for files not touched by the plan.
	snapshot, _, _, _, _, err := readSnapshot(ctx, r.output, true)
	if err != nil {
		return bundle, ErrEvidence
	}
	if len(snapshot) > MaxEvidenceEntries {
		return bundle, ErrLimit
	}
	items := make([]evidenceInventoryEntry, 0, len(snapshot))
	for _, entry := range snapshot {
		item := evidenceInventoryEntry{Path: entry.Path, Type: "file", SHA256: workspaceplan.Hash(entry.Data), Bytes: len(entry.Data), Mode: "0600"}
		if entry.Directory {
			item.Type = "directory"
			item.SHA256 = ""
			item.Bytes = 0
			item.Mode = "0700"
		}
		items = append(items, item)
	}
	ops := make([]evidenceOperation, 0, len(report.Operations))
	journal := []byte{}
	present := summary.State == "succeeded"
	if !present {
		if summary.Lock || summary.Journal || summary.ApprovedPlan {
			return bundle, ErrEvidence
		}
		absent, e := r.artifactAbsent("artifacts/report.json")
		if e != nil || !absent {
			return bundle, ErrEvidence
		}
	} else {
		if !summary.JournalVerified || !summary.ApprovedPlanVerified {
			return bundle, ErrEvidence
		}
		planBytes, e := r.readArtifact("artifacts/approved-plan.json", int64(workspaceplan.DefaultLimits().PlanBytes))
		if e != nil {
			return bundle, ErrEvidence
		}
		plan, e := workspaceplan.Parse(planBytes, workspaceplan.DefaultLimits())
		if e != nil || len(plan.Operations) != len(report.Operations) {
			return bundle, ErrEvidence
		}
		for i, op := range plan.Operations {
			result := report.Operations[i]
			if result.Status != "succeeded" || result.Type != op.Type || result.Path != op.Path || result.BeforeSHA256 != op.Precondition.SHA256 || result.AfterSHA256 != op.Validation.SHA256 {
				return bundle, ErrEvidence
			}
			data, e := r.readArtifact("output/"+op.Path, MaxFileBytes)
			if e != nil || workspaceplan.Hash(data) != result.AfterSHA256 {
				return bundle, ErrEvidence
			}
			ops = append(ops, evidenceOperation{i + 1, result.Type, result.Path, result.BeforeSHA256, result.AfterSHA256, result.Status, len(data)})
		}
		data, e := r.readArtifact("artifacts/journal.jsonl", 32768)
		if e != nil {
			return bundle, ErrEvidence
		}
		for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'}) {
			var record workspacejournal.Record
			if strictMetadata(line, &record) != nil {
				return bundle, ErrEvidence
			}
			transformed := evidenceJournalRecord{record.Version, record.Sequence, record.Timestamp, evidenceJournalOperation{record.Operation.Type, record.Operation.Path, record.Operation.BeforeSHA256, record.Operation.AfterSHA256}, record.Status}
			encoded, e := metadataJSON(transformed)
			if e != nil {
				return bundle, e
			}
			journal = append(journal, encoded...)
		}
	}
	if err = verifyEvidenceBaseline(items, ops, m); err != nil {
		return bundle, err
	}
	values := []any{
		evidenceRunManifest{m.Version, m.RunID, m.Created, m.SourceReference, m.Snapshot, m.Files, m.Directories, m.Bytes, report.Status},
		nil,
		evidenceReport{1, report.Status, ops},
		evidenceOperations{1, present, ops},
		evidenceInventory{1, items},
	}
	manifest := EvidenceManifest{FormatVersion: 1, ExportKind: "evidence", ExportID: exportID, RunID: m.RunID, Created: created.UTC(), RunState: report.Status, Integrity: "verified", SecretFreeGuarantee: "not_applicable", SourceNotModified: true, ContentExportNotIncluded: true, JournalPresent: present, PlanPresent: present, ReportPersisted: present, MaxFiles: 6, MaxBytes: MaxEvidenceBytes, Artifacts: []EvidenceArtifact{}, Warnings: append([]string(nil), evidenceWarnings...)}
	for i, value := range values {
		data := journal
		if i != 1 {
			data, err = metadataJSON(value)
			if err != nil {
				return bundle, err
			}
		}
		bundle.files = append(bundle.files, evidenceFile{evidencePaths[i], data})
		manifest.Artifacts = append(manifest.Artifacts, EvidenceArtifact{evidencePaths[i], workspaceplan.Hash(data), len(data)})
	}
	manifestBytes, err := metadataJSON(manifest)
	if err != nil {
		return bundle, err
	}
	bundle.files = append(bundle.files, evidenceFile{"export-manifest.json", manifestBytes})
	bundle.manifest = manifest
	// Bind the complete run inventory, including versions; never serialize its contents.
	inv, err := inventoryRun(ctx, s.base, r.ID(), r.root)
	if err != nil {
		return bundle, err
	}
	if inv.Hash != summary.InventorySHA256 {
		return bundle, ErrEvidence
	}
	for _, f := range bundle.files {
		bundle.total += len(f.data)
		if len(f.data) > MaxEvidenceFileBytes || bundle.total > MaxEvidenceBytes {
			return evidenceBundle{}, ErrLimit
		}
	}
	bundle.binding = workspaceplan.Hash([]byte(inv.Hash + workspaceplan.Hash(manifestBytes)))
	return bundle, ctx.Err()
}

func evidenceDecode(data []byte, target any) error {
	allowed := map[string]bool{}
	for _, key := range []string{"format_version", "export_kind", "export_id", "run_id", "created_at_utc", "run_state", "integrity", "contains_file_content", "contains_patch_content", "secret_free_guarantee", "source_not_modified", "content_export_not_included", "journal_present", "approved_plan_present", "report_persisted", "artifacts", "path", "output_sha256", "artifact_sha256", "file_size_bytes", "artifact_size_bytes", "total_size_bytes", "max_files", "max_bytes", "warnings", "version", "created", "source_reference_sha256", "snapshot_sha256", "files", "directories", "status", "operations", "order", "type", "before_sha256", "after_sha256", "after_bytes", "present", "sequence", "timestamp", "operation", "entries", "mode"} {
		allowed[key] = true
	}
	return strictJSON(data, target, allowed)
}

func readEvidenceArtifact(root *os.Root, path string) ([]byte, error) {
	info, err := root.Lstat(path)
	if err != nil || !safeRegular(info) || info.Size() < 0 || info.Size() > MaxEvidenceFileBytes {
		return nil, ErrArtifact
	}
	f, err := openRead(root, path, false)
	if err != nil {
		return nil, ErrArtifact
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, ErrArtifact
	}
	data, readErr := io.ReadAll(io.LimitReader(f, MaxEvidenceFileBytes+1))
	closed := f.Close()
	if readErr != nil || closed != nil || int64(len(data)) != info.Size() || len(data) > MaxEvidenceFileBytes {
		return nil, ErrArtifact
	}
	return data, nil
}
