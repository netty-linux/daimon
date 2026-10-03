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
	if _, err := OpenStore(base); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	s := &Store{}
	if _, err := s.List(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := s.Inspect(context.Background(), "unused"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := s.PrepareDiscard(context.Background(), "unused", true); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatal("unsupported platform wrote")
	}
}
