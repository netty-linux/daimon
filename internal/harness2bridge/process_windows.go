//go:build windows

package harness2bridge

import (
	"os/exec"
	"syscall"
	"unsafe"
)

// Job limits are lifecycle/resource controls, not filesystem/network isolation.
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

func prepareProcess(cmd *exec.Cmd)    { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
func killProcess(cmd *exec.Cmd) error { return cmd.Process.Kill() }
func containProcess(cmd *exec.Cmd) (func() error, error) {
	job, _, _ := createJob.Call(0, 0)
	if job == 0 {
		return nil, ErrWorker
	}
	closeJob := func() error { return syscall.CloseHandle(syscall.Handle(job)) }
	limits := jobLimits{}
	// Kill on close, two processes (venv redirector + Python), 256 MiB total, five CPU seconds per process.
	limits.Basic.Flags = 0x2000 | 0x0008 | 0x0100 | 0x0200 | 0x0002
	limits.Basic.ActiveProcessLimit = 2
	limits.Basic.ProcessTime = 5 * 10000000
	limits.ProcessMemory = 256 * 1024 * 1024
	limits.JobMemory = 256 * 1024 * 1024
	ok, _, _ := setJob.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		_ = closeJob()
		return nil, ErrWorker
	}
	process, err := syscall.OpenProcess(0x0100|0x0001, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = closeJob()
		return nil, ErrWorker
	}
	defer syscall.CloseHandle(process)
	ok, _, _ = assignJob.Call(job, uintptr(process))
	if ok == 0 {
		_ = closeJob()
		return nil, ErrWorker
	}
	return closeJob, nil
}
