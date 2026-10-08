//go:build linux

package mcp

import (
	"os/exec"
	"sync"
	"syscall"
)

func prepareProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
func containProcess(cmd *exec.Cmd) (func(), error) {
	var once sync.Once
	return func() { once.Do(func() { _ = killProcess(cmd) }) }, nil
}
func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
func terminateProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}
