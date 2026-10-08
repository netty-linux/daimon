//go:build linux

package environments

import (
	"os"
	"syscall"
)

func Supported() bool { return true }
func privateDirectory(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && i.IsDir() && s.Uid == uint32(os.Getuid()) && i.Mode().Perm() == 0700
}
func regular(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && i.Mode().IsRegular() && s.Nlink == 1 && s.Uid == uint32(os.Getuid()) && i.Mode().Perm() == 0600
}
