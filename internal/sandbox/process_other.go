//go:build !linux

package sandbox

import "os/exec"

func configureProcess(*exec.Cmd) error { return errorOf(Unsupported) }
func finishProcess(*exec.Cmd)          {}
