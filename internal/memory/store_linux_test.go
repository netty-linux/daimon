//go:build linux

package memory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxPermissionsAndObservedSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Create(t.Context(), input("private")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("permissions", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal("symlink must run", err)
	}
	if _, err := NewStore(link); !errors.Is(err, ErrStore) {
		t.Fatal("symlink accepted", err)
	}
	got, _ := os.ReadFile(outside)
	if string(got) != "untouched" {
		t.Fatal("outside modified")
	}
}
