//go:build linux

package createcontract

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Characterization, not a claim of strong confinement. A pinned directory can
// be moved by an external writer after the last path check. Post-validation
// and cleanup detect the move but cannot undo the transient physical write.
func TestCanonicalPathConfinementRequiresExternalMutationIsolation(t *testing.T) {
	w, root, ctx := setup(t)
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "moved")
	permit := approved(t, w, ctx, "parent/new", []byte("approved"))
	ops := realOps()
	realCreate := ops.create
	observed := false
	ops.create = func(r *os.Root, p string) (createFile, error) {
		if err := os.Rename(parent, external); err != nil {
			return nil, err
		}
		f, err := realCreate(r, p)
		if err != nil {
			return nil, err
		}
		file := f.(*os.File)
		return &faultyFile{File: file, write: func(b []byte) (int, error) {
			n, e := file.Write(b)
			data, readErr := os.ReadFile(filepath.Join(external, "new"))
			observed = readErr == nil && string(data) == "approved"
			return n, e
		}}, nil
	}
	err := permit.apply(ctx, ops)
	if err == nil || !observed {
		t.Fatal("expected controlled failure after observed transient external write")
	}
	if !errors.Is(err, ErrFile) && !errors.Is(err, ErrChanged) {
		t.Fatal("unexpected failure category")
	}
	noFile(t, filepath.Join(external, "new"))
	t.Log("confirmed: concurrent parent move permits a transient write outside the original canonical path; cleanup succeeds; strong canonical-path guarantee is unavailable")
}
