//go:build windows

package workspacefs

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// This probes a candidate primitive, not a complete confinement strategy.
func TestHandleWithoutDeleteSharingBlocksRename(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(h)
	if err := os.Rename(root, root+"-moved"); err == nil {
		t.Fatal("rename allowed despite exclusive delete sharing")
	}
	if err := os.Remove(root); err == nil {
		t.Fatal("removal allowed despite exclusive delete sharing")
	}
	if ApplyIsolation().Strong {
		t.Fatal("single handle probe must not enable apply")
	}
}
