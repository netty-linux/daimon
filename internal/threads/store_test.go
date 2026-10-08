package threads

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "threads.json"))
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
	b.Title = "caller_mutation"
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
	list[1].Title = "list_mutation"
	list[1].Workspace = "list workspace mutation"
	got, err := s.Get("zeta")
	if err != nil || got.Title != "Private title" || got.Workspace != "explicit/workspace" {
		t.Fatal("shared input/list metadata")
	}
	got.Title = "get_mutation"
	got, _ = s.Get("zeta")
	if got.Title != "Private title" {
		t.Fatal("shared get metadata")
	}
	got.Title = "Updated"
	got.UpdatedAt = got.UpdatedAt.Add(time.Hour)
	expected := got
	if err := s.Update(got); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	got, err = reopened.Get("zeta")
	if err != nil || got.Title != "Updated" || !got.CreatedAt.Equal(expected.CreatedAt) || !got.UpdatedAt.Equal(expected.UpdatedAt) {
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
		t.Fatal("delete last thread")
	}
	data, _ := os.ReadFile(s.path)
	if !bytes.Contains(data, []byte(`"threads": []`)) || !bytes.Contains(data, []byte(`"version": 1`)) {
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
	valid := `{"version":1,"threads":[` + string(validBot) + `]}`
	tests := []struct {
		name, data string
		kind       error
	}{
		{"malformed", "{", ErrFormat},
		{"missing version", `{"threads":[]}`, ErrFormat},
		{"unknown version", `{"version":2,"threads":[]}`, ErrVersion},
		{"missing threads", `{"version":1}`, ErrFormat},
		{"null threads", `{"version":1,"threads":null}`, ErrFormat},
		{"array root", `[]`, ErrFormat},
		{"unknown field", `{"version":1,"threads":[],"api_key":"fixture-secret"}`, ErrFormat},
		{"thread credentials", strings.Replace(valid, `"title":`, `"api_key":"fixture-secret","title":`, 1), ErrFormat},
		{"bot invalid", strings.Replace(valid, `"id":"coder"`, `"id":"../private"`, 1), ErrFormat},
		{"missing workspace", strings.Replace(valid, `"workspace":"explicit/workspace",`, "", 1), ErrFormat},
		{"missing created time", strings.Replace(valid, `"created_at":"2026-10-07T12:00:00.000000123Z",`, "", 1), ErrFormat},
		{"nonUTC time", strings.ReplaceAll(valid, "000000123Z", "000000123+01:00"), ErrFormat},
		{"duplicate version", `{"version":1,"version":1,"threads":[]}`, ErrFormat},
		{"duplicate bot key", strings.Replace(valid, `"id":"coder"`, `"id":"coder","id":"other"`, 1), ErrFormat},
		{"duplicate IDs", `{"version":1,"threads":[` + string(validBot) + `,` + string(validBot) + `]}`, ErrFormat},
		{"wrong key case", strings.Replace(valid, `"title"`, `"Title"`, 1), ErrFormat},
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
	b.UpdatedAt = time.Time{}
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

func TestStoreUpdatePreservesAssociationsAndTime(t *testing.T) {
	s := newStore(t)
	original := fixture("thread-a")
	original.BotID = "unavailable-bot"
	original.Workspace = "/unavailable-workspace"
	original.UpdatedAt = original.CreatedAt.Add(time.Hour)
	if err := s.Create(original); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.path)
	for _, change := range []func(*Thread){
		func(v *Thread) { v.BotID = "different-bot" },
		func(v *Thread) { v.Workspace = "/different-workspace" },
		func(v *Thread) { v.CreatedAt = v.CreatedAt.Add(time.Minute) },
	} {
		v := original
		change(&v)
		if err := s.Update(v); !errors.Is(err, ErrImmutable) {
			t.Fatal(err)
		}
	}
	v := original
	v.UpdatedAt = v.CreatedAt
	if err := s.Update(v); !errors.Is(err, ErrInvalid) {
		t.Fatal("updated time regressed")
	}
	v = original
	v.ID = "new-id"
	if err := s.Update(v); !errors.Is(err, ErrNotFound) {
		t.Fatal("ID change created new thread")
	}
	after, _ := os.ReadFile(s.path)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected update modified store")
	}
	v = original
	v.Title = ""
	if err := s.Update(v); err != nil {
		t.Fatal("equal updated time should be allowed", err)
	}
	got, err := s.Get(original.ID)
	if err != nil || got.Title != "" || got.BotID != original.BotID || got.Workspace != original.Workspace || !got.CreatedAt.Equal(original.CreatedAt) || !got.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatal("store rewrote caller metadata")
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
	for _, path := range []string{"", " ", filepath.Join(t.TempDir(), "missing", "threads.json"), t.TempDir()} {
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
	threads := []Thread{}
	for i := 0; i < MaxThreads; i++ {
		b := fixture(ID("bot-" + strings.Repeat("a", i/26) + string(rune('a'+i%26))))
		threads = append(threads, b)
	}
	if err := s.save(threads); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(fixture("additional")); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	data, _ := json.Marshal(envelope{Version: 1, Threads: append(threads, fixture("additional"))})
	if _, err := decode(data); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	// Valid bounded fields can expand during JSON escaping. The serialized limit
	// still applies before a temporary file is created or the target is changed.
	for i := range threads {
		threads[i].Workspace = strings.Repeat("\x01", MaxWorkspaceBytes)
	}
	before, _ := os.ReadFile(s.path)
	if err := s.save(threads); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(s.path)
	if !bytes.Equal(before, after) {
		t.Fatal("oversized encoding changed target")
	}
}
