package managedworkspace

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) readTombstone(id string, metadata *tombstone) (state string, resultErr error) {
	// Only return details after the complete record contract is accepted.
	var verified tombstone
	defer func() {
		if metadata != nil && resultErr == nil && state != "" {
			*metadata = verified
		}
	}()
	if !validID(id) {
		return "", ErrPrivate
	}
	name := tombstoneDirectory + "/" + id + ".jsonl"
	_, err := s.root.Lstat(tombstoneDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", ErrArtifact
	}
	if err = checkPrivate(filepath.Join(s.base, tombstoneDirectory)); err != nil {
		return "", err
	}
	info, err := s.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || !safeRegular(info) || info.Size() > 16384 {
		return "", ErrArtifact
	}
	f, err := openRead(s.root, name, false)
	if err != nil {
		return "", ErrArtifact
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", ErrArtifact
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(data) > 16384 || !bytes.HasSuffix(data, []byte{'\n'}) {
		return "", ErrArtifact
	}
	lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	if len(lines) < 1 || len(lines) > 2 {
		return "", ErrArtifact
	}
	allowed := map[string]bool{}
	for _, key := range []string{"version", "sequence", "timestamp", "store", "run_id", "permit", "state_sha256", "preview_sha256", "initial", "files", "directories", "total_size_bytes", "status", "reason"} {
		allowed[key] = true
	}
	records := make([]tombstone, len(lines))
	for i, line := range lines {
		r := &records[i]
		if strictJSON(line, r, allowed) != nil || r.Version != 1 || r.Sequence != i+1 || r.Timestamp.IsZero() || r.Store != s.base || r.RunID != id || !validID(r.Permit) || !validHash(r.StateHash) || !validHash(r.PreviewHash) || r.Files < 1 || r.Files > MaxRunEntries || r.Directories < 0 || r.Directories > MaxRunEntries || r.Bytes < 0 || r.Bytes > MaxRunBytes {
			return "", ErrArtifact
		}
		if r.Initial != "ready" && r.Initial != "succeeded" && r.Initial != "partial" {
			return "", ErrArtifact
		}
	}
	first := records[0]
	if first.Status != "started" || first.Reason != "none" {
		return "", ErrArtifact
	}
	if len(records) == 1 {
		verified = first
		return "unknown_interrupted", nil
	}
	last := records[1]
	verified = last
	if last.Timestamp.Before(first.Timestamp) {
		return "", ErrArtifact
	}
	copy := last
	copy.Sequence = first.Sequence
	copy.Timestamp = first.Timestamp
	copy.Status = first.Status
	copy.Reason = first.Reason
	if copy != first {
		return "", ErrArtifact
	}
	switch last.Status {
	case "discarded":
		if last.Reason != "none" {
			return "", ErrArtifact
		}
		return "discarded", nil
	case "unknown", "failed":
		if last.Reason != "removal_failed" {
			return "", ErrArtifact
		}
		return "unknown_interrupted", nil
	case "cancelled_before_delete":
		if last.Reason != "revalidation_failed" {
			return "", ErrArtifact
		}
		return "unknown_interrupted", nil
	default:
		return "", ErrArtifact
	}
}
func (s *Store) tombstoneIDs() ([]string, error) {
	_, err := s.root.Lstat(tombstoneDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrPrivate
	}
	if err = checkPrivate(filepath.Join(s.base, tombstoneDirectory)); err != nil {
		return nil, err
	}
	dir, err := openRead(s.root, tombstoneDirectory, true)
	if err != nil {
		return nil, ErrPrivate
	}
	defer dir.Close()
	entries, err := dir.ReadDir(MaxStoreEntries + 1)
	if err != nil && err != io.EOF {
		return nil, ErrPrivate
	}
	if len(entries) > MaxStoreEntries {
		return nil, ErrLimit
	}
	ids := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			id := strings.TrimSuffix(entry.Name(), ".jsonl")
			if validID(id) {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}
