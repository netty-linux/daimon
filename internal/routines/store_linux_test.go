//go:build linux

package routines

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRejectsObservedSymlinksAndRestrictsFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "routines.json")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	if e := s.Create(context.Background(), baseInput("one"), now); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("permissions")
	}
	link := filepath.Join(dir, "link")
	if e := os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(link); e == nil {
		t.Fatal("file link accepted")
	}
	parent := filepath.Join(dir, "parent")
	if e := os.Symlink(dir, parent); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(filepath.Join(parent, "other.json")); e == nil {
		t.Fatal("parent link accepted")
	}
}
