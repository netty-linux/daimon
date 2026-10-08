package workspacefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBindingAndInvalidRoot(t *testing.T) {
	root := t.TempDir()
	r, b, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if b.RootID() == "" || b.RunID() == "" || b.Check() != nil {
		t.Fatal("missing binding")
	}
	r2, b2, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if b.RootID() != b2.RootID() || b.RunID() == b2.RunID() {
		t.Fatal("root/run identities incorrect")
	}
	if _, _, err := Open(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing root accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(filepath.Join(root, "file")); err == nil {
		t.Fatal("file accepted as root")
	}
}
