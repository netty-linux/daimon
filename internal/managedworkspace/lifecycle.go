package managedworkspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"
)

// Summary separates a manifest's claim from evidence verified by Report.
// Paths and file contents are deliberately absent.
type Summary struct {
	RetentionRequested   bool              `json:"retention_requested,omitempty"`
	PreimageCount        int               `json:"preimage_count,omitempty"`
	RetentionState       string            `json:"retention_state,omitempty"`
	Retention            *RetentionSummary `json:"preimage_retention,omitempty"`
	RunID                string            `json:"run_id"`
	Version              int               `json:"version"`
	Declared             string            `json:"declared"`
	State                string            `json:"state"`
	Reason               string            `json:"reason"`
	Created              time.Time         `json:"created"`
	Audited              time.Time         `json:"audited"`
	Manifest             bool              `json:"manifest"`
	ManifestVerified     bool              `json:"manifest_verified"`
	Journal              bool              `json:"journal"`
	JournalVerified      bool              `json:"journal_verified"`
	ApprovedPlan         bool              `json:"approved_plan"`
	ApprovedPlanVerified bool              `json:"approved_plan_verified"`
	AuditVerified        bool              `json:"audit_verified"`
	Lock                 bool              `json:"lock"`
	Staging              bool              `json:"staging"`
	Files                int               `json:"files"`
	Directories          int               `json:"directories"`
	Bytes                int64             `json:"total_size_bytes"`
	InventoryVerified    bool              `json:"inventory_verified"`
	InventorySHA256      string            `json:"inventory_sha256,omitempty"`
	SourceAccess         string            `json:"source_access"`
	ThreatModel          string            `json:"threat_model"`
}

// Store pins the opened directory and checks that its pathname still names it.
// This boundary trusts the owning UID and administrators, like managed runs.
type Store struct {
	base     string
	root     *os.Root
	identity Identity
}

func canonicalBase(base string) (string, error) {
	if base == "" {
		return "", ErrPrivate
	}
	abs, err := filepath.Abs(base)
	if err != nil || !utf8.ValidString(abs) || len(abs) > 4096 {
		return "", ErrPrivate
	}
	return abs, nil
}

func OpenStore(base string) (*Store, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	abs, err := canonicalBase(base)
	if err != nil {
		return nil, ErrPrivate
	}
	if err = checkPrivate(abs); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, ErrPrivate
	}
	info, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, ErrPrivate
	}
	s := &Store{abs, root, identityInfo(info)}
	if err = s.check(); err != nil {
		root.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error {
	if s == nil || s.root == nil {
		return ErrPrivate
	}
	return s.root.Close()
}
func (s *Store) check() error {
	if s == nil || s.root == nil {
		return ErrPrivate
	}
	if err := checkPrivate(s.base); err != nil {
		return err
	}
	named, err := identity(s.base)
	if err != nil || named != s.identity {
		return ErrPrivate
	}
	opened, err := s.root.Stat(".")
	if err != nil || identityInfo(opened) != s.identity {
		return ErrPrivate
	}
	return nil
}
func (s *Store) Inspect(ctx context.Context, id string) (Summary, error) {
	return s.inspect(ctx, id, false)
}
func (s *Store) inspect(ctx context.Context, id string, ownAudit bool) (Summary, error) {
	if !Supported() {
		return Summary{}, ErrUnsupported
	}
	result := Summary{RunID: id, State: "invalid", Reason: "integrity_unverified", SourceAccess: "none", ThreatModel: "owner_uid_and_administrators_trusted"}
	if !validID(id) {
		return Summary{}, ErrPrivate
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if err := s.check(); err != nil {
		return Summary{}, err
	}
	entry, err := s.root.Lstat(id)
	if errors.Is(err, os.ErrNotExist) {
		result.State = "missing"
		result.Reason = "run_absent"
		var audit tombstone
		tomb, e := s.readTombstone(id, &audit)
		if e != nil {
			result.State = "invalid"
			result.Reason = "audit_invalid"
		} else if tomb != "" {
			result.State = tomb
			result.Reason = "audit_verified"
			result.Version = audit.Version
			result.Audited = audit.Timestamp
			result.AuditVerified = true
		}
		return result, s.check()
	}
	if err != nil || !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
		return result, nil
	}
	if err := checkPrivate(filepath.Join(s.base, id)); err != nil {
		return result, nil
	}
	// Presence never implies integrity. Unsafe artifacts are rejected by Open/Report.
	runRoot, err := s.root.OpenRoot(id)
	if err != nil {
		return result, nil
	}
	for _, item := range []struct {
		name    string
		present *bool
	}{
		{"manifest.json", &result.Manifest}, {"artifacts/journal.jsonl", &result.Journal},
		{"artifacts/approved-plan.json", &result.ApprovedPlan}, {"attempt.lock", &result.Lock}, {"manifest.next", &result.Staging},
	} {
		if checkPrivate(filepath.Join(s.base, id, filepath.Dir(filepath.FromSlash(item.name)))) != nil {
			continue
		}
		_, e := runRoot.Lstat(item.name)
		*item.present = e == nil
	}
	runRoot.Close()
	var audit tombstone
	tomb, tombErr := s.readTombstone(id, &audit)
	if tombErr == nil && tomb != "" {
		result.Audited = audit.Timestamp
		result.AuditVerified = true
		result.Version = audit.Version
	}
	if ownAudit {
		tomb = ""
		tombErr = nil
		result.Audited = time.Time{}
		result.AuditVerified = false
	}
	if tombErr != nil || tomb == "discarded" {
		result.Reason = "audit_inconsistent"
		return result, s.check()
	}
	r, err := Open(s.base, id)
	if err != nil {
		if tomb == "unknown_interrupted" {
			result.State = tomb
			result.Reason = "audit_incomplete"
		}
		return result, s.check()
	}
	defer r.Close()
	m := r.Manifest()
	result.Version = m.Version
	result.ManifestVerified = true
	result.Declared = m.Status
	result.Created = m.Created
	result.Retention = copyRetention(m.Retention)
	if m.Retention != nil {
		result.RetentionRequested = true
		result.PreimageCount = m.Retention.Count
		result.RetentionState = m.Retention.State
	}
	report, err := r.Report()
	if err == nil {
		if len(report.Operations) > 0 {
			result.JournalVerified = result.Journal
			result.ApprovedPlanVerified = result.ApprovedPlan
		}
		switch report.Status {
		case "ready", "succeeded", "partial", "unknown_interrupted":
			result.State = report.Status
		case "unknown":
			result.State = "unknown_interrupted"
		case "denied", "failed":
			result.State = "partial"
		}
		result.Reason = "verified"
	}
	inv, e := inventoryRun(ctx, s.base, id, r.root)
	if e == nil {
		result.Files = inv.Files
		result.Directories = inv.Directories
		result.Bytes = inv.Bytes
		result.InventoryVerified = true
		result.InventorySHA256 = inv.Hash
	} else {
		result.State = "invalid"
		result.Reason = "inventory_invalid"
	}
	if tomb == "unknown_interrupted" {
		result.State = tomb
		result.Reason = "audit_incomplete"
	}
	if result.State == "ready" {
		_, _, _, _, snapshot, e := readSnapshot(ctx, r.output, true)
		if e != nil || snapshot != m.Snapshot {
			result.State = "invalid"
			result.Reason = "snapshot_changed"
		}
		if result.Journal || result.ApprovedPlan {
			result.State = "invalid"
			result.Reason = "unexpected_artifact"
		}
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if err := r.check(); err != nil {
		result.State = "invalid"
		result.Reason = "run_changed"
		result.InventoryVerified = false
		result.ManifestVerified = false
		result.JournalVerified = false
		result.ApprovedPlanVerified = false
		if tomb == "unknown_interrupted" {
			result.State = tomb
			result.Reason = "audit_incomplete"
		}
	}
	return result, s.check()
}

const MaxStoreEntries = 256

// List ignores unexpected names without printing them. No entry is repaired.
func (s *Store) List(ctx context.Context) ([]Summary, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	dir, err := openRead(s.root, ".", true)
	if err != nil {
		return nil, ErrPrivate
	}
	defer dir.Close()
	entries, err := dir.ReadDir(MaxStoreEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, ErrPrivate
	}
	if len(entries) > MaxStoreEntries {
		return nil, ErrLimit
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if validID(entry.Name()) {
			ids = append(ids, entry.Name())
			seen[entry.Name()] = true
		}
	}
	tombIDs, err := s.tombstoneIDs()
	if err != nil {
		return nil, err
	}
	for _, id := range tombIDs {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	if len(ids) > MaxStoreEntries {
		return nil, ErrLimit
	}
	sort.Strings(ids)
	results := make([]Summary, 0, len(ids))
	for _, id := range ids {
		summary, err := s.Inspect(ctx, id)
		if err != nil {
			return nil, err
		}
		results = append(results, summary)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, s.check()
}
