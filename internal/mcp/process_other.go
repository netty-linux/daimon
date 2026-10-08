//go:build !linux && !windows

package mcp

import "os/exec"

func prepareProcess(*exec.Cmd) error           { return ErrUnsupported }
func containProcess(*exec.Cmd) (func(), error) { return func() {}, ErrUnsupported }
func killProcess(cmd *exec.Cmd) error          { return cmd.Process.Kill() }
func terminateProcess(cmd *exec.Cmd) error     { return cmd.Process.Kill() }
