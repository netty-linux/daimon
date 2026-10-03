//go:build linux

package managedworkspace

import (
	"os"
	"syscall"
	"unsafe"
)

// Linux amd64 renameat2(RENAME_NOREPLACE), relative to one pinned parent.
// Unsupported kernel/filesystem or seccomp denial fails closed; no fallback.
func evidenceRename(parent *os.File, old, new string) error {
	if !Supported() {
		return ErrUnsupported
	}
	oldPtr, err := syscall.BytePtrFromString(old)
	if err != nil {
		return ErrPrivate
	}
	newPtr, err := syscall.BytePtrFromString(new)
	if err != nil {
		return ErrPrivate
	}
	_, _, errno := syscall.Syscall6(316, parent.Fd(), uintptr(unsafe.Pointer(oldPtr)), parent.Fd(), uintptr(unsafe.Pointer(newPtr)), 1, 0)
	if errno != 0 {
		return ErrArtifact
	}
	return nil
}
