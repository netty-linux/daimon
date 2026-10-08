//go:build linux

package conversations

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestObservedSymlinkAndPermissions(t *testing.T) {
	s, dir := newTestStore(t)
	target := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(target, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "thread.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), "thread"); err == nil {
		t.Fatal("symlink read")
	}
	if err := s.Append(context.Background(), message("message", "session", User)); err == nil {
		t.Fatal("symlink write")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(context.Background(), message("message", "session", User)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(link)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("permissions")
	}
}
