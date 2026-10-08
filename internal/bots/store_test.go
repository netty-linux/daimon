package bots

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "bots.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreCRUDAndCopies(t *testing.T) {
	s := newStore(t)
	list, err := s.List()
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("constructor/list created a file")
	}
	if _, err := s.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	b := fixture("zeta")
	if err := s.Create(b); err != nil {
		t.Fatal(err)
	}
	b.Tools[0] = "caller_mutation"
	if err := s.Create(fixture("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(fixture("alpha")); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	list, err = s.List()
	if err != nil || len(list) != 2 || list[0].ID != "alpha" || list[1].ID != "zeta" {
		t.Fatalf("list: %v", err)
	}
	list[1].Tools[0] = "list_mutation"
	got, err := s.Get("zeta")
	if err != nil || !reflect.DeepEqual(got.Tools, []string{"read_file", "list_dir"}) {
		t.Fatal("shared input/list tools")
	}
	got.Tools[0] = "get_mutation"
	got, _ = s.Get("zeta")
	if got.Tools[0] != "read_file" {
		t.Fatal("shared get tools")
	}
	got.Name = "Updated"
	got.Tools = []string{}
	if err := s.Update(got); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	got, err = reopened.Get("zeta")
	if err != nil || got.Name != "Updated" || got.Tools == nil || len(got.Tools) != 0 {
		t.Fatal("update not persisted")
	}
	if err := s.Update(fixture("missing")); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Delete("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Delete("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("zeta"); err != nil {
		t.Fatal(err)
	}
	list, err = s.List()
	if err != nil || list == nil || len(list) != 0 {
		t.Fatal("delete last bot")
	}
	data, _ := os.ReadFile(s.path)
	if !bytes.Contains(data, []byte(`"bots": []`)) || !bytes.Contains(data, []byte(`"version": 1`)) {
		t.Fatal("unexpected envelope")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(s.path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("store permissions")
		}
	}
}

func TestStoreInvalidDataAndNoSilentRecovery(t *testing.T) {
	validBot, _ := json.Marshal(fixture("coder"))
	valid := `{"version":1,"bots":[` + string(validBot) + `]}`
	tests := []struct {
		name, data string
		kind       error
	}{
		{"malformed", "{", ErrFormat},
		{"missing version", `{"bots":[]}`, ErrFormat},
		{"unknown version", `{"version":2,"bots":[]}`, ErrVersion},
		{"missing bots", `{"version":1}`, ErrFormat},
		{"null bots", `{"version":1,"bots":null}`, ErrFormat},
		{"array root", `[]`, ErrFormat},
		{"unknown field", `{"version":1,"bots":[],"api_key":"fixture-secret"}`, ErrFormat},
		{"bot credentials", strings.Replace(valid, `"name":`, `"api_key":"fixture-secret","name":`, 1), ErrFormat},
		{"bot invalid", strings.Replace(valid, `"id":"coder"`, `"id":"../private"`, 1), ErrFormat},
		{"missing tools", strings.Replace(valid, `"tools":["read_file","list_dir"],`, "", 1), ErrFormat},
		{"duplicate version", `{"version":1,"version":1,"bots":[]}`, ErrFormat},
		{"duplicate bot key", strings.Replace(valid, `"id":"coder"`, `"id":"coder","id":"other"`, 1), ErrFormat},
		{"duplicate IDs", `{"version":1,"bots":[` + string(validBot) + `,` + string(validBot) + `]}`, ErrFormat},
		{"wrong key case", strings.Replace(valid, `"name"`, `"Name"`, 1), ErrFormat},
		{"trailing JSON", valid + `{}`, ErrFormat},
		{"UTF8", valid + "\xff", ErrFormat},
		{"oversized file", strings.Repeat(" ", MaxStoreBytes+1), ErrLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			if err := os.WriteFile(s.path, []byte(tt.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewStore(s.path); !errors.Is(err, tt.kind) {
				t.Fatalf("constructor: %v", err)
			}
			for _, err := range []error{func() error { _, err := s.List(); return err }(), s.Create(fixture("other")), s.Update(fixture("coder")), s.Delete("coder")} {
				var se *StoreError
				if !errors.Is(err, tt.kind) || !errors.As(err, &se) {
					t.Fatalf("wrong typed error: %v", err)
				}
				if strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "private") {
					t.Fatal("data leaked in error")
				}
			}
			data, err := os.ReadFile(s.path)
			if err != nil || string(data) != tt.data {
				t.Fatal("silently replaced invalid file")
			}
		})
	}
}

func TestStoreValidationBeforeWrite(t *testing.T) {
	s := newStore(t)
	if err := s.Create(fixture("coder")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.path)
	b := fixture("coder")
	b.PermissionMode = "allow-all"
	for _, err := range []error{s.Create(b), s.Update(b), s.Delete("../bad"), func() error { _, err := s.Get(""); return err }()} {
		if !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	after, _ := os.ReadFile(s.path)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid operations changed valid store")
	}
}

func TestAtomicWriteFailedCommit(t *testing.T) {
	s := newStore(t)
	if err := s.Create(fixture("coder")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.path)
	failure := errors.New("private-path-and-fixture-secret")
	err := atomicWrite(s.path, []byte("replacement"), func(temp, target string) error {
		data, err := os.ReadFile(temp)
		if err != nil || string(data) != "replacement" {
			t.Fatal("temporary not complete before commit")
		}
		original, _ := os.ReadFile(target)
		if !bytes.Equal(before, original) {
			t.Fatal("target truncated before commit")
		}
		return failure
	})
	if !errors.Is(err, ErrStore) || !errors.Is(err, failure) || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("wrong commit error")
	}
	after, _ := os.ReadFile(s.path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed commit changed target")
	}
	entries, _ := os.ReadDir(filepath.Dir(s.path))
	if len(entries) != 1 {
		t.Fatal("temporary left behind")
	}
}

func TestStoreConstructorAndTargets(t *testing.T) {
	for _, path := range []string{"", " ", filepath.Join(t.TempDir(), "missing", "bots.json"), t.TempDir()} {
		if _, err := NewStore(path); !errors.Is(err, ErrStore) {
			t.Fatal(err)
		}
	}
	var s *Store
	if _, err := s.List(); !errors.Is(err, ErrStore) {
		t.Fatal(err)
	}
}

func TestAtomicWriteCleanupFailureIsExplicit(t *testing.T) {
	s := newStore(t)
	if err := s.Create(fixture("coder")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.path)
	failure := errors.New("commit refused")
	err := atomicWrite(s.path, []byte("replacement"), func(temp, target string) error {
		// Simulate external interference with the owned temporary, so cleanup
		// cannot confirm its removal. This must not turn into success.
		if err := os.Remove(temp); err != nil {
			t.Fatal(err)
		}
		return failure
	})
	if !errors.Is(err, ErrCleanup) || !errors.Is(err, failure) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(s.path)
	if !bytes.Equal(before, after) {
		t.Fatal("cleanup failure changed target")
	}
}

func TestStoreLimits(t *testing.T) {
	s := newStore(t)
	bots := []Bot{}
	for i := 0; i < MaxBots; i++ {
		b := fixture(ID("bot-" + strings.Repeat("a", i/26) + string(rune('a'+i%26))))
		bots = append(bots, b)
	}
	if err := s.save(bots); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(fixture("additional")); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	data, _ := json.Marshal(envelope{Version: 1, Bots: append(bots, fixture("additional"))})
	if _, err := decode(data); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	// Valid bounded fields can expand during JSON escaping. The serialized limit
	// still applies before a temporary file is created or the target is changed.
	for i := range bots {
		bots[i].Instructions = strings.Repeat("\x00", MaxInstructionsBytes)
	}
	before, _ := os.ReadFile(s.path)
	if err := s.save(bots); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(s.path)
	if !bytes.Equal(before, after) {
		t.Fatal("oversized encoding changed target")
	}
}
