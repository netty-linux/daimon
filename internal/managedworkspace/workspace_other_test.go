//go:build !linux || !amd64

package managedworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnsupportedHasNoEffects(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-created")
	if Supported() {
		t.Fatal("unproven platform enabled")
	}
	if _, err := Create(context.Background(), base, "unused"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := Open(base, "unused"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatal("unsupported platform wrote")
	}
}
