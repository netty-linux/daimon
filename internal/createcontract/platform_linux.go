//go:build linux

package createcontract

import (
	"os"
	"syscall"
)

func Supported() bool { return true }
func singleLink(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Nlink == 1
}

func sameCreatedFile(a, b os.FileInfo) bool {
	if !os.SameFile(a, b) || a.Size() != b.Size() || a.Mode() != b.Mode() || !a.ModTime().Equal(b.ModTime()) {
		return false
	}
	sa, okA := a.Sys().(*syscall.Stat_t)
	sb, okB := b.Sys().(*syscall.Stat_t)
	return okA && okB && sa.Nlink == 1 && sb.Nlink == 1 && sa.Ctim == sb.Ctim && sa.Uid == sb.Uid && sa.Gid == sb.Gid
}
