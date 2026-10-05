package managedworkspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/netty-linux/daimon/internal/workspacejournal"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

// Strict bounded metadata decoding, not authentication against the trusted UID.
func strictMetadata(data []byte, target any) error {
	allowed := map[string]bool{}
	for _, key := range []string{"version", "run_id", "created", "source_reference_sha256", "snapshot_sha256", "files", "directories", "total_size_bytes", "root_identity", "output_identity", "device", "inode", "status", "rules", "operations", "type", "path", "before_sha256", "after_sha256", "sequence", "timestamp", "operation", "preimage_retention", "preimage_format_version", "retention_requested", "preimage_count", "retention_state", "preimage_journal_sha256", "expected_before_sha256", "captured_before_sha256", "approved_after_sha256", "preimage_size_bytes", "operation_id", "plan_sha256", "approval_sha256"} {
		allowed[key] = true
	}
	return strictJSON(data, target, allowed)
}
func strictJSON(data []byte, target any, allowed map[string]bool) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var walk func(int, string) bool
	walk = func(depth int, field string) bool {
		if depth > 4 {
			return false
		}
		token, err := d.Token()
		if err != nil || token == nil {
			return false
		}
		if strings.HasSuffix(field, "_sha256") {
			digest, ok := token.(string)
			return ok && validHash(digest)
		}
		if strings.HasSuffix(field, "_bytes") {
			number, ok := token.(json.Number)
			if !ok {
				return false
			}
			size, err := number.Int64()
			limit := int64(MaxRunBytes)
			switch field {
			case "file_size_bytes", "after_bytes":
				limit = MaxFileBytes
			case "artifact_size_bytes":
				limit = MaxEvidenceFileBytes
			case "max_bytes":
				limit = MaxEvidenceBytes
			}
			return err == nil && size >= 0 && size <= limit
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		if delim != '{' && delim != '[' {
			return false
		}
		keys := map[string]bool{}
		count := 0
		for d.More() {
			field := ""
			count++
			if count > 32 {
				return false
			}
			if delim == '{' {
				key, err := d.Token()
				s, ok := key.(string)
				if err != nil || !ok || !allowed[s] || keys[s] {
					return false
				}
				keys[s] = true
				field = s
			}
			if !walk(depth+1, field) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == map[json.Delim]json.Delim{'{': '}', '[': ']'}[delim]
	}
	if !walk(0, "") {
		return ErrArtifact
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrArtifact
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(new(any)) != io.EOF {
		return ErrArtifact
	}
	return nil
}

func (r *Run) artifactAbsent(name string) (bool, error) {
	_, err := r.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, ErrArtifact
	}
	return false, nil
}

// No replay, repair or writes. Incomplete/contradictory evidence is never success.
func (r *Run) verifyReport(report Report) error {
	if len(report.Operations) == 0 || len(report.Operations) > 2 {
		return ErrArtifact
	}
	metadata := make([]workspacejournal.Metadata, 0, len(report.Operations))
	states := make(map[string]string)
	succeeded, denied := 0, 0
	for _, op := range report.Operations {
		metadata = append(metadata, workspacejournal.Metadata{Type: op.Type, Path: op.Path, BeforeSHA256: op.BeforeSHA256, AfterSHA256: op.AfterSHA256})
		states[op.Path] = "prepared"
		if op.Status == "succeeded" {
			succeeded++
		}
		if op.Status == "denied" {
			denied++
		}
	}
	checker, err := workspacejournal.New(io.Discard, metadata)
	if err != nil {
		return ErrArtifact
	}
	switch report.Status {
	case "succeeded":
		if succeeded != len(metadata) {
			return ErrArtifact
		}
	case "denied":
		if denied != len(metadata) {
			return ErrArtifact
		}
	case "partial":
		if succeeded == 0 || succeeded == len(metadata) {
			return ErrArtifact
		}
	case "failed":
		for _, op := range report.Operations {
			if op.Status != "not_started" {
				return ErrArtifact
			}
		}
	case "unknown":
	default:
		return ErrArtifact
	}
	lock, err := r.readArtifact("attempt.lock", 0)
	if err != nil || len(lock) != 0 {
		return ErrArtifact
	}
	data, err := r.readArtifact("artifacts/journal.jsonl", 32768)
	if err != nil {
		return err
	}
	if len(data) != 0 && data[len(data)-1] != '\n' {
		return ErrArtifact
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'})
	if len(data) == 0 {
		lines = nil
	}
	if len(lines) > 8 {
		return ErrArtifact
	}
	lastOperation := 0
	for i, line := range lines {
		var record workspacejournal.Record
		if strictMetadata(line, &record) != nil || record.Version != 1 || record.Sequence != i+1 || record.Timestamp.IsZero() {
			return ErrArtifact
		}
		found := false
		for index, m := range metadata {
			if m == record.Operation {
				if index < lastOperation {
					return ErrArtifact
				}
				lastOperation = index
				found = true
			}
		}
		if !found || checker.Append(record.Operation.Path, record.Status) != nil {
			return ErrArtifact
		}
		status := record.Status
		if status == "failed" && states[record.Operation.Path] == "started" {
			status = "unknown"
		}
		states[record.Operation.Path] = status
	}
	for _, op := range report.Operations {
		state := states[op.Path]
		if op.Status == "succeeded" && state != "succeeded" || op.Status == "denied" && state != "denied" || op.Status == "not_started" && state != "prepared" {
			return ErrArtifact
		}
	}
	// A denied/pre-effect run need not have an approved plan. Any confirmed effect does.
	absent, err := r.artifactAbsent("artifacts/approved-plan.json")
	if err != nil {
		return err
	}
	if absent {
		if r.manifest.Retention != nil && (report.Retention == nil || *report.Retention != *r.manifest.Retention) {
			return ErrArtifact
		}
		if succeeded != 0 {
			return ErrArtifact
		}
		return nil
	}
	data, err = r.readArtifact("artifacts/approved-plan.json", int64(workspaceplan.DefaultLimits().PlanBytes))
	if err != nil {
		return err
	}
	dataForPlan := append([]byte(nil), data...)
	plan, err := workspaceplan.Parse(data, workspaceplan.DefaultLimits())
	if err != nil || len(plan.Operations) != len(metadata) || report.Status == "denied" {
		return ErrArtifact
	}
	for i, op := range plan.Operations {
		m := metadata[i]
		if m.Type != op.Type || m.Path != op.Path || m.BeforeSHA256 != op.Precondition.SHA256 || m.AfterSHA256 != op.Validation.SHA256 {
			return ErrArtifact
		}
		if report.Operations[i].Status == "succeeded" {
			data, err := r.readArtifact("output/"+op.Path, MaxFileBytes)
			if err != nil || workspaceplan.Hash(data) != m.AfterSHA256 {
				return ErrArtifact
			}
		}
	}
	return r.verifyRetention(report, dataForPlan)
}
