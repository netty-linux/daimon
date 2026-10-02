//go:build !linux

package createcontract

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnsupportedDoesNotCreate(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Prepare(context.Background(), "new", []byte("x"), Limits{FinalBytes: 10, Lines: 10, PathBytes: 100, PreviewBytes: 1000}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
