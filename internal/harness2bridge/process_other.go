//go:build !windows && !linux

package harness2bridge

import "os/exec"

func prepareProcess(cmd *exec.Cmd)                       {}
func killProcess(cmd *exec.Cmd) error                    { return cmd.Process.Kill() }
func containProcess(cmd *exec.Cmd) (func() error, error) { return nil, ErrConfig }
