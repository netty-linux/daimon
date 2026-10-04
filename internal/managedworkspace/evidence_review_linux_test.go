//go:build linux && amd64

package managedworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestEvidenceCancellationDuringFinalClose(t *testing.T) {
	ctx, source, _, store, run, destination := evidenceFixture(t, false)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	proposal := evidencePrepare(t, ctx, store, run, destination, source)
	permit := evidencePermit(t, ctx, proposal)
	proposal.state.closeAudit = func(f *os.File) error { err := f.Close(); cancel(); return err }
	result, err := permit.Export(ctx)
	if !errors.Is(err, context.Canceled) || result.State != "unknown_interrupted" {
		t.Fatal("cancel during final close accepted", result, err)
	}
}

func TestEvidenceFinalAuditCloseFailure(t *testing.T) {
	ctx, source, _, store, run, destination := evidenceFixture(t, false)
	proposal := evidencePrepare(t, ctx, store, run, destination, source)
	permit := evidencePermit(t, ctx, proposal)
	proposal.state.closeAudit = func(f *os.File) error {
		if err := f.Close(); err != nil {
			return err
		}
		return os.ErrPermission
	}
	result, err := permit.Export(ctx)
	if err == nil || result.State != "unknown_interrupted" {
		t.Fatal("close failure accepted", result, err)
	}
}

// Non-root execution proves regular source files cannot be read by this process.
// A post-import FIFO/invalid portable filename also makes a source inventory fail.
// Export must still succeed: source-check only examines the directory boundary.
func TestEvidenceSourceCheckDoesNotReadContent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("unreadable-file proof requires non-root; exercised by offline Docker UID 65534")
	}
	ctx, source, _, store, run, destination := evidenceFixture(t, true)
	for _, name := range []string{"README.md", "src/config.txt", "src/info.txt"} {
		path := filepath.Join(source, name)
		if err := os.Chmod(path, 0000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(path, 0640); err != nil {
				t.Error(err)
			}
		})
		if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
			t.Fatal("source fixture remained readable", err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(source, "unreadable-pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "non-portable name"), []byte("synthetic-source-only"), 0600); err != nil {
		t.Fatal(err)
	}
	proposal := evidencePrepare(t, ctx, store, run, destination, source)
	result, err := evidencePermit(t, ctx, proposal).Export(ctx)
	if err != nil || result.State != "exported" {
		t.Fatal("source-check accessed content/inventory", result, err)
	}
	if _, err := VerifyEvidence(ctx, destination); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "src/config.txt", "src/info.txt"} {
		info, err := os.Stat(filepath.Join(source, name))
		if err != nil || info.Mode().Perm() != 0000 {
			t.Fatal("source permission changed", err)
		}
	}
}

func TestEvidenceSpecialOutputFailsClosed(t *testing.T) {
	ctx, source, base, store, run, destination := evidenceFixture(t, false)
	path := filepath.Join(base, run.ID(), "output", "README.md")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if proposal, err := store.PrepareEvidence(ctx, run.ID(), destination, source, true); err == nil {
		proposal.Close()
		t.Fatal("special output accepted")
	}
	assertNoEvidenceDestination(t, destination)
}

func TestEvidenceMountBoundaryFailsClosed(t *testing.T) {
	// /proc is an existing separate mount in the offline Linux container.
	// No mounts or privileges are created by this test.
	if err := checkLocalPath("/proc"); err == nil {
		t.Fatal("mount crossing accepted")
	}
}
