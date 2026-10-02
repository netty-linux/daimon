package editcontract

import (
	"os"
	"syscall"
)

func replacementSupported() bool { return true }
func samePlatformMetadata(a, b os.FileInfo) bool {
	x, xok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	return xok && yok && x.Uid == y.Uid && x.Gid == y.Gid && x.Ctim == y.Ctim && x.Nlink == y.Nlink
}
func checkLinks(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ErrFile
	}
	if stat.Nlink != 1 {
		return ErrHardLink
	}
	return nil
}
