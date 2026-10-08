//go:build linux

package workspacefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalAliasAndRootReplacement(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	alias := filepath.Join(base, "alias")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	r, b, err := Open(alias)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := b.Check(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(b.Check(), ErrChanged) {
		t.Fatal("root replacement not detected")
	}
}

func TestRootReplacedWithSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	external := filepath.Join(base, "external")
	for _, p := range []string{root, external} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	r, b, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, root); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(b.Check(), ErrChanged) {
		t.Fatal("symlink replacement accepted")
	}
}
