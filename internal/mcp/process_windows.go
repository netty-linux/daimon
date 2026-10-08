//go:build windows

package mcp

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// A kill-on-close Job owns the server's ordinary descendant tree. This is
// lifecycle control, not a sandbox against hostile user/admin processes.
var kernel = syscall.NewLazyDLL("kernel32.dll")
var createJob = kernel.NewProc("CreateJobObjectW")
var setJob = kernel.NewProc("SetInformationJobObject")
var assignJob = kernel.NewProc("AssignProcessToJobObject")

type basicJobLimits struct {
	ProcessTime, JobTime                 int64
	Flags                                uint32
	MinimumWorkingSet, MaximumWorkingSet uintptr
	ActiveProcessLimit                   uint32
	Affinity                             uintptr
	PriorityClass, SchedulingClass       uint32
}
type jobLimits struct {
	Basic                                                      basicJobLimits
	IO                                                         [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

func prepareProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return nil
}
func containProcess(cmd *exec.Cmd) (func(), error) {
	job, _, _ := createJob.Call(0, 0)
	if job == 0 {
		return nil, ErrUnavailable
	}
	closeJob := func() { _ = syscall.CloseHandle(syscall.Handle(job)) }
	limits := jobLimits{}
	limits.Basic.Flags = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	ok, _, _ := setJob.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		closeJob()
		return nil, ErrUnavailable
	}
	process, err := syscall.OpenProcess(0x0100|0x0001, false, uint32(cmd.Process.Pid))
	if err != nil {
		closeJob()
		return nil, ErrUnavailable
	}
	defer syscall.CloseHandle(process)
	ok, _, _ = assignJob.Call(job, uintptr(process))
	if ok == 0 {
		closeJob()
		return nil, ErrUnavailable
	}
	var once sync.Once
	return func() { once.Do(closeJob) }, nil
}
func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
func terminateProcess(cmd *exec.Cmd) error { return killProcess(cmd) }
