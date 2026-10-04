package managedworkspace

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/workspacejournal"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

var forbiddenMetadataFields = []string{
	"before", "after", "bytes", "content", "expected_content", "preimage", "postimage",
	"data", "payload", "body", "raw", "encoded", "base64", "hex", "diff", "patch", "prompt", "response",
}

// Check actual emitted JSON, including nested records, rather than searching
// string values (warnings legitimately mention content and patch).
func assertExplicitMetadata(t *testing.T, data []byte) {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		t.Fatal("metadata JSON", err)
	}
	var visit func(any)
	visit = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, value := range v {
				for _, prohibited := range forbiddenMetadataFields {
					if key == prohibited {
						t.Fatalf("prohibited metadata key %q", key)
					}
				}
				if strings.HasSuffix(key, "_sha256") {
					s, ok := value.(string)
					if !ok || !validHash(s) {
						t.Fatalf("invalid digest field %q", key)
					}
				}
				if strings.HasSuffix(key, "_bytes") {
					n, ok := value.(json.Number)
					if !ok {
						t.Fatalf("non-number size field %q", key)
					}
					if size, err := n.Int64(); err != nil || size < 0 {
						t.Fatalf("invalid size field %q", key)
					}
				}
				visit(value)
			}
		case []any:
			for _, item := range v {
				visit(item)
			}
		}
	}
	visit(value)
}

func TestMetadataDigestAndSizeScalarContracts(t *testing.T) {
	hash := workspaceplan.Hash([]byte("synthetic metadata fixture"))
	quotedHash := strconv.Quote(hash)
	decoders := []struct {
		name string
		new  func() any
		read func([]byte, any) error
		hash string
		size string
		max  int
	}{
		{"manifest", func() any { return new(Manifest) }, strictMetadata, "snapshot_sha256", "total_size_bytes", MaxRunBytes},
		{"operation", func() any { return new(evidenceOperation) }, evidenceDecode, "after_sha256", "after_bytes", MaxFileBytes},
		{"inventory", func() any { return new(evidenceInventoryEntry) }, evidenceDecode, "output_sha256", "file_size_bytes", MaxFileBytes},
		{"artifact", func() any { return new(EvidenceArtifact) }, evidenceDecode, "artifact_sha256", "artifact_size_bytes", MaxEvidenceFileBytes},
		{"audit", func() any { return new(evidenceAuditRecord) }, func(b []byte, v any) error { return evidenceAuditDecode(b, v.(*evidenceAuditRecord)) }, "preview_sha256", "total_size_bytes", MaxRunBytes},
	}
	badHashes := []string{`null`, `0`, `{}`, `[]`, `""`, strconv.Quote(strings.ToUpper(hash)), strconv.Quote(hash[:63]), strconv.Quote(hash + "0"), strconv.Quote(strings.Repeat("g", 64)), `"c3ludGhldGlj"`, `"ff"`, `"payload"`}
	badSizes := []string{`null`, `"0"`, `0.0`, `1.5`, `-1`, `{}`, `[]`, `"c3ludGhldGlj"`, `"ff"`, `"payload"`, `9223372036854775808`}
	for _, d := range decoders {
		t.Run(d.name, func(t *testing.T) {
			for _, size := range []int{0, d.max} {
				data := []byte(`{"` + d.hash + `":` + quotedHash + `,"` + d.size + `":` + strconv.Itoa(size) + `}`)
				if err := d.read(data, d.new()); err != nil {
					t.Fatal("valid scalar contract rejected", err)
				}
			}
			for _, value := range badHashes {
				if d.read([]byte(`{"`+d.hash+`":`+value+`}`), d.new()) == nil {
					t.Fatal("invalid hash accepted", value)
				}
			}
			for _, value := range append(badSizes, strconv.Itoa(d.max+1)) {
				if d.read([]byte(`{"`+d.size+`":`+value+`}`), d.new()) == nil {
					t.Fatal("invalid size accepted", value)
				}
			}
			for _, field := range forbiddenMetadataFields {
				for _, prefix := range []string{"", `"` + d.hash + `":` + quotedHash + `,"` + d.size + `":0,`} {
					if d.read([]byte(`{`+prefix+strconv.Quote(field)+`:"synthetic"}`), d.new()) == nil {
						t.Fatal("legacy/content alias accepted", field)
					}
				}
			}
		})
	}
}

func TestMetadataSerializerSchemas(t *testing.T) {
	h := workspaceplan.Hash([]byte("synthetic fixture"))
	op := evidenceOperation{BeforeSHA256: h, AfterSHA256: h}
	values := []any{
		Manifest{SourceReference: h, Snapshot: h}, Report{Operations: []OperationReport{{BeforeSHA256: h, AfterSHA256: h}}},
		Summary{InventorySHA256: h}, EvidenceResult{},
		workspacejournal.Record{Operation: workspacejournal.Metadata{BeforeSHA256: h, AfterSHA256: h}},
		evidenceRunManifest{SourceReference: h, Snapshot: h}, evidenceReport{Operations: []evidenceOperation{op}},
		evidenceOperations{Operations: []evidenceOperation{op}}, evidenceInventory{Entries: []evidenceInventoryEntry{{SHA256: h}}},
		evidenceJournalRecord{Operation: evidenceJournalOperation{BeforeSHA256: h, AfterSHA256: h}},
		EvidenceManifest{Artifacts: []EvidenceArtifact{{SHA256: h}}},
		evidenceAuditRecord{StoreReference: h, RunBinding: h, PreviewHash: h, ManifestHash: h, PackageHash: h},
		tombstone{StateHash: h, PreviewHash: h},
	}
	for _, value := range values {
		t.Run(reflect.TypeOf(value).Name(), func(t *testing.T) {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			assertExplicitMetadata(t, data)
			// Type shapes themselves cannot acquire a forbidden serialized field.
			typeOf := reflect.TypeOf(value)
			for i := 0; i < typeOf.NumField(); i++ {
				key := strings.Split(typeOf.Field(i).Tag.Get("json"), ",")[0]
				for _, prohibited := range forbiddenMetadataFields {
					if key == prohibited {
						t.Fatal("content-bearing schema field", key)
					}
				}
			}
		})
	}
	// An unavailable inventory digest is absent, not empty or substituted.
	data, err := json.Marshal(Summary{State: "missing"})
	if err != nil || strings.Contains(string(data), "inventory_sha256") {
		t.Fatal("unavailable hash was emitted", err)
	}
}
