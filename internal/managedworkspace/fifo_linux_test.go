//go:build linux && amd64

package managedworkspace

import "syscall"

func makeFIFO(path string) error { return syscall.Mkfifo(path, 0600) }
