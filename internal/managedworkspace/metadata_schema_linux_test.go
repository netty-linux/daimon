//go:build linux && amd64

package managedworkspace

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMetadataLegacyRunArtifactsFailClosed(t *testing.T) {
	for _, artifact := range []string{"manifest.json", "artifacts/report.json", "artifacts/journal.jsonl"} {
		t.Run(artifact, func(t *testing.T) {
			ctx, source, _, s, r, _ := evidenceFixture(t, true)
			snapshot := r.Manifest().Snapshot
			path := filepath.Join(r.directory, artifact)
			data := read(t, path)
			old := bytes.ReplaceAll(data, []byte(`"total_size_bytes"`), []byte(`"bytes"`))
			old = bytes.ReplaceAll(old, []byte(`"before_sha256"`), []byte(`"before"`))
			old = bytes.ReplaceAll(old, []byte(`"after_sha256"`), []byte(`"after"`))
			if bytes.Equal(data, old) {
				t.Fatal("legacy fixture did not alter a schema field")
			}
			if err := os.WriteFile(path, old, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := s.Inspect(ctx, r.ID())
			if err == nil && got.State != "invalid" {
				t.Fatal("legacy run accepted", got)
			}
			if !bytes.Equal(old, read(t, path)) {
				t.Fatal("legacy artifact was repaired or migrated")
			}
			assertSource(t, ctx, source, snapshot)
		})
	}
}

func TestMetadataLegacyTombstoneFailsClosed(t *testing.T) {
	ctx, source, base, s, r, _ := evidenceFixture(t, false)
	snapshot, id := r.Manifest().Snapshot, r.ID()
	p, err := s.PrepareDiscard(ctx, id, true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := permit.Discard(ctx); err != nil || state != "discarded" {
		t.Fatal(state, err)
	}
	path := filepath.Join(base, tombstoneDirectory, id+".jsonl")
	data := read(t, path)
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		assertExplicitMetadata(t, line)
	}
	old := bytes.ReplaceAll(data, []byte(`"state_sha256"`), []byte(`"state_hash"`))
	if bytes.Equal(data, old) {
		t.Fatal("legacy fixture did not alter schema")
	}
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Inspect(ctx, id); err == nil && got.State != "invalid" {
		t.Fatal("legacy tombstone accepted", got)
	}
	if !bytes.Equal(old, read(t, path)) {
		t.Fatal("legacy tombstone was repaired")
	}
	assertSource(t, ctx, source, snapshot)
}

func TestMetadataLegacyEvidenceFailsClosed(t *testing.T) {
	ctx, source, _, s, r, dest := evidenceFixture(t, true)
	p := evidencePrepare(t, ctx, s, r, dest, source)
	if _, err := evidencePermit(t, ctx, p).Export(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dest, "export-manifest.json")
	data := read(t, path)
	old := bytes.ReplaceAll(data, []byte(`"artifact_sha256"`), []byte(`"sha256"`))
	old = bytes.ReplaceAll(old, []byte(`"artifact_size_bytes"`), []byte(`"bytes"`))
	if bytes.Equal(data, old) {
		t.Fatal("legacy fixture did not alter schema")
	}
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := VerifyEvidence(ctx, dest); err == nil || result.State != "invalid" {
		t.Fatal("legacy evidence accepted", result, err)
	}
	if !bytes.Equal(old, read(t, path)) {
		t.Fatal("legacy export was repaired")
	}
}
