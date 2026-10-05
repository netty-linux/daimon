package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/netty-linux/daimon/internal/managedworkspace"
)

func TestManagedPreimageArguments(t *testing.T) {
	for _, tail := range [][]string{
		{"apply", "--run", "id", "--plan", "p", "--enable-replace-file", "--enable-preimage-retention"},
		{"apply", "--enable-preimage-retention", "--plan", "p", "--enable-replace-file", "--run", "id"},
		{"apply", "--run", "id", "--plan", "p"},
	} {
		if _, _, _, e := managedArguments(append([]string{"--base", "store"}, tail...)); e != nil {
			t.Fatal(e)
		}
	}
	for _, tail := range [][]string{
		{"apply", "--run", "id", "--plan", "p", "--enable-preimage-retention"},
		{"apply", "--run", "id", "--plan", "p", "--enable-replace-file", "--enable-preimage-retention", "--enable-preimage-retention"},
		{"create", "--source", "s", "--enable-preimage-retention"},
		{"list", "--enable-preimage-retention"},
		{"inspect", "--run", "id", "--enable-preimage-retention"},
		{"discard", "--run", "id", "--enable-discard", "--enable-preimage-retention"},
		{"export-evidence", "--run", "id", "--destination", "d", "--enable-export-evidence", "--enable-preimage-retention"},
		{"plan", "--enable-preimage-retention"},
	} {
		if _, _, _, e := managedArguments(append([]string{"--base", "store"}, tail...)); e == nil {
			t.Fatal("unexpected opt in", tail)
		}
	}
}

func TestManagedPreimageUnsupportedBeforeEffects(t *testing.T) {
	if managedworkspace.Supported() {
		t.Skip("unsupported-platform gate")
	}
	base := filepath.Join(t.TempDir(), "store")
	var out, display bytes.Buffer
	e := runManagedWorkspace(context.Background(), []string{"--base", base, "apply", "--run", "id", "--plan", "p", "--enable-replace-file", "--enable-preimage-retention"}, bytes.NewReader(nil), &out, &display)
	if e != managedworkspace.ErrUnsupported {
		t.Fatal(e)
	}
	if _, e := os.Stat(base); !os.IsNotExist(e) {
		t.Fatal("platform provisioned")
	}
}
