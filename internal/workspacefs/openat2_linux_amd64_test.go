//go:build linux && amd64

package workspacefs

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

// Kernel ABI from linux/openat2.h. Probe code is test-only.
func openBeneath(fd int, path string) (int, error) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return -1, err
	}
	how := [3]uint64{syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW, 0, 0x08 | 0x04 | 0x01}
	r, _, e := syscall.Syscall6(437, uintptr(fd), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&how[0])), 24, 0, 0)
	if e != 0 {
		return -1, e
	}
	return int(r), nil
}
func TestOpenat2ResolutionDoesNotImmobilizeParent(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	parent := filepath.Join(root, "parent")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "file"), []byte("known"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("parent/file", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	for _, path := range []string{"../outside", "/etc/passwd", "link"} {
		f, e := openBeneath(fd, path)
		if e == nil {
			syscall.Close(f)
			t.Fatal("unsafe resolution accepted")
		}
	}
	pinned, err := openBeneath(fd, "parent")
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(pinned)
	outside := filepath.Join(base, "outside")
	if err := os.Rename(parent, outside); err != nil {
		t.Fatal(err)
	}
	f, err := openBeneath(pinned, "file")
	if err != nil {
		t.Fatal(err)
	}
	syscall.Close(f)
	t.Log("openat2 still resolves the moved parent outside root; path resolution is not physical isolation")
}
