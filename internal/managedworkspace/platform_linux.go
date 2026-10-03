//go:build linux

package managedworkspace

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

func supported() bool { return runtime.GOARCH == "amd64" }

// Advisory directory locks coordinate cooperating managed operations without
// creating a lock artifact. The owning UID and administrators remain trusted.
func exclusiveDirectory(root *os.Root) (*os.File, error) {
	f, err := openRead(root, ".", true)
	if err != nil {
		return nil, ErrPrivate
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, ErrState
	}
	return f, nil
}

// Topology guard: no mount crossings/bind aliases from the namespace root.
// This does not immobilize shared trees; private ownership is still required.
func checkLocalPath(name string) error {
	if !supported() {
		return ErrUnsupported
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ErrPrivate
	}
	defer syscall.Close(fd)
	rel := strings.TrimPrefix(filepath.Clean(name), "/")
	if rel == "" {
		rel = "."
	}
	p, err := syscall.BytePtrFromString(rel)
	if err != nil {
		return ErrPrivate
	}
	// Linux amd64 openat2 ABI, linux/openat2.h: NO_XDEV|NO_SYMLINKS|BENEATH.
	how := [3]uint64{uint64(syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW), 0, 0x01 | 0x04 | 0x08}
	result, _, errno := syscall.Syscall6(437, uintptr(fd), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&how[0])), 24, 0, 0)
	if errno != 0 {
		return ErrPrivate
	}
	syscall.Close(int(result))
	return nil
}
func openRead(r *os.Root, name string, directory bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if directory {
		flags |= syscall.O_DIRECTORY
	}
	return r.OpenFile(name, flags, 0)
}
func identityInfo(i os.FileInfo) Identity {
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}
	}
	return Identity{uint64(s.Dev), s.Ino}
}

func changeInfo(i os.FileInfo) string {
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", s.Ctim.Sec, s.Ctim.Nsec)
}
func identity(name string) (Identity, error) {
	i, err := os.Lstat(name)
	if err != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return Identity{}, ErrPrivate
	}
	return identityInfo(i), nil
}
func safeRegular(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && i.Mode().IsRegular() && s.Nlink == 1 && s.Uid == uint32(os.Geteuid()) && i.Mode().Perm() == 0600
}
func sourceRegular(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && i.Mode().IsRegular() && s.Nlink == 1 && i.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}
func checkAncestors(name string) error {
	for {
		i, err := os.Lstat(name)
		if err != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
			return ErrPrivate
		}
		s, ok := i.Sys().(*syscall.Stat_t)
		if !ok || (s.Uid != 0 && s.Uid != uint32(os.Geteuid())) {
			return ErrPrivate
		}
		// Sticky directories owned by root/current UID protect this user's entry
		// from other UIDs; root/current UID are explicitly trusted.
		if i.Mode().Perm()&0022 != 0 && i.Mode()&os.ModeSticky == 0 {
			return ErrPrivate
		}
		parent := filepath.Dir(name)
		if parent == name {
			return nil
		}
		name = parent
	}
}
func checkPrivate(name string) error {
	if err := checkLocalPath(name); err != nil {
		return err
	}
	if err := checkAncestors(filepath.Dir(name)); err != nil {
		return err
	}
	i, err := os.Lstat(name)
	if err != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm() != 0700 {
		return ErrPrivate
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) {
		return ErrPrivate
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(name, &fs); err != nil {
		return ErrPrivate
	}
	// Only local filesystems with conventional Linux ownership/mode semantics.
	switch uint64(fs.Type) {
	case 0xef53, 0x01021994, 0x794c7630, 0x58465342, 0x9123683e:
		return nil
	default:
		return ErrPrivate
	}
}
