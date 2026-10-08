//go:build linux

package threads

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRejectsObservedSymlink(t *testing.T) {
	s := newStore(t)
	target := filepath.Join(t.TempDir(), "target.json")
	original := []byte(`{"version":1,"threads":[]}`)
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, s.path); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(s.path); !errors.Is(err, ErrStore) {
		t.Fatal(err)
	}
	if err := s.Create(fixture("coder")); !errors.Is(err, ErrStore) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("symlink target changed")
	}
}
