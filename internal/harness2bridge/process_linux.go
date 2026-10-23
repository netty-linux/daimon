//go:build linux

package harness2bridge

import (
	"errors"
	"os/exec"
	"syscall"
)

func prepareProcess(cmd *exec.Cmd)    { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func killProcess(cmd *exec.Cmd) error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
func containProcess(cmd *exec.Cmd) (func() error, error) {
	return func() error {
		err := killProcess(cmd)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}, nil
}
