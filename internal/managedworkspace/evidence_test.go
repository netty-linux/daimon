package managedworkspace

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/workspaceplan"
	"strings"
	"testing"
)

func TestEvidenceStrictMetadataSchemas(t *testing.T) {
	for _, data := range []string{`null`, `{"version":1,"version":1}`, `{"version":1,"operations":null}`, `{"version":1,"content":"secret"}`, `{"version":1} {}`, `{"version":"1"}`, `[]`, `{"version":1,"operations":[{"path":"src/config.txt","expected_content":"secret"}]}`} {
		var v evidenceOperations
		if evidenceDecode([]byte(data), &v) == nil {
			t.Fatalf("invalid metadata accepted: %s", data)
		}
	}
	var v evidenceInventory
	if evidenceDecode([]byte(`{"version":1,"entries":[`+strings.Repeat(`{},`, 32)+`{}]}`), &v) == nil {
		t.Fatal("excess entries accepted")
	}
}
func TestEvidenceUnsupportedBeforeEffects(t *testing.T) {
	if Supported() {
		return
	}
	var s *Store
	if _, err := s.PrepareEvidence(context.Background(), "invalid", "not-absolute", "source", true); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if result, err := VerifyEvidence(context.Background(), "not-absolute"); !errors.Is(err, ErrUnsupported) || result.State != "invalid" {
		t.Fatal(result, err)
	}
}

func TestEvidenceMetadataByteLimit(t *testing.T) {
	data, err := metadataJSON([]string{strings.Repeat("x", MaxEvidenceFileBytes-5)})
	if err != nil || len(data) != MaxEvidenceFileBytes {
		t.Fatal("exact byte limit", len(data), err)
	}
	if _, err = metadataJSON([]string{strings.Repeat("x", MaxEvidenceFileBytes-4)}); !errors.Is(err, ErrLimit) {
		t.Fatal("byte excess", err)
	}
}

func TestEvidenceSnapshotFramingPreservesLegacyHash(t *testing.T) {
	entries := []snapshotHashEntry{
		{"README.md", false, 23, workspaceplan.Hash([]byte("PRIVATE_SOURCE_CONTENT\n"))},
		{"docs", true, 0, workspaceplan.Hash(nil)},
		{"src", true, 0, workspaceplan.Hash(nil)},
		{"src/config.txt", false, 13, workspaceplan.Hash([]byte("mode=initial\n"))},
		{"src/info.txt", false, 5, workspaceplan.Hash([]byte("note\n"))},
	}
	const legacy = "7411292fc13f643d595ab6d67c43e0916ba7a39327ffd000b4de8ae8fb9134e6"
	if got := snapshotMetadataHash(entries); got != legacy {
		t.Fatal("legacy framing changed", got)
	}
}
