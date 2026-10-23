//go:build windows

package harness2bridge

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Job limits are lifecycle/resource controls, not filesystem/network isolation.
var kernel = syscall.NewLazyDLL("kernel32.dll")
var createJob = kernel.NewProc("CreateJobObjectW")
var setJob = kernel.NewProc("SetInformationJobObject")
var assignJob = kernel.NewProc("AssignProcessToJobObject")
var terminateJob = kernel.NewProc("TerminateJobObject")
var queryJob = kernel.NewProc("QueryInformationJobObject")
var threadFirst = kernel.NewProc("Thread32First")
var threadNext = kernel.NewProc("Thread32Next")
var openThread = kernel.NewProc("OpenThread")
var resumeThread = kernel.NewProc("ResumeThread")
var threadProcessID = kernel.NewProc("GetProcessIdOfThread")

const createSuspended = 0x00000004

type threadEntry struct {
	Size, Usage, ID, Owner      uint32
	BasePriority, DeltaPriority int32
	Flags                       uint32
}

type jobProcessIDs struct {
	Assigned, Count uint32
	IDs             [2]uintptr // Job's fixed active-process ceiling, never an unbounded list.
}

type jobAccounting struct {
	UserTime, KernelTime, PeriodUserTime, PeriodKernelTime           int64
	PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
}

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

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createSuspended}
}
func killProcess(cmd *exec.Cmd) error { return cmd.Process.Kill() }
func containProcess(cmd *exec.Cmd) (func() error, error) {
	job, _, _ := createJob.Call(0, 0)
	if job == 0 {
		return nil, ErrWorker
	}
	var once sync.Once
	var closeErr error
	closeJob := func() error {
		once.Do(func() {
			// ActiveProcesses can reach zero before process handles signal.
			// Retain current Job member handles and wait on their final state.
			var ids jobProcessIDs
			handles := make([]syscall.Handle, 0, 2)
			ok, _, _ := queryJob.Call(job, 3, uintptr(unsafe.Pointer(&ids)), unsafe.Sizeof(ids), 0)
			if ok == 0 || ids.Count > 2 {
				closeErr = ErrCleanup
			} else {
				for _, pid := range ids.IDs[:ids.Count] {
					handle, err := syscall.OpenProcess(0x00100000, false, uint32(pid)) // SYNCHRONIZE only
					if err == nil {
						handles = append(handles, handle)
					} else if err != syscall.Errno(87) {
						closeErr = ErrCleanup
					}
				}
			}
			// Termination targets the Job, never a numeric PID from a snapshot.
			ok, _, _ = terminateJob.Call(job, 1)
			deadline := time.Now().Add(time.Second)
			if ok == 0 {
				closeErr = ErrCleanup
			}
			for _, handle := range handles {
				remaining := time.Until(deadline)
				if remaining < 0 {
					remaining = 0
				}
				state, err := syscall.WaitForSingleObject(handle, uint32(remaining/time.Millisecond))
				if err != nil || state != syscall.WAIT_OBJECT_0 {
					closeErr = ErrCleanup
				}
				if syscall.CloseHandle(handle) != nil {
					closeErr = ErrCleanup
				}
			}
			for {
				var accounting jobAccounting
				ok, _, _ = queryJob.Call(job, 1, uintptr(unsafe.Pointer(&accounting)), unsafe.Sizeof(accounting), 0)
				if ok == 0 {
					closeErr = ErrCleanup
					break
				}
				if accounting.ActiveProcesses == 0 {
					break
				}
				if !time.Now().Before(deadline) {
					closeErr = ErrCleanup
					break
				}
				time.Sleep(time.Millisecond)
			}
			if syscall.CloseHandle(syscall.Handle(job)) != nil {
				closeErr = ErrCleanup
			}
		})
		return closeErr
	}
	limits := jobLimits{}
	// Kill on close, two processes (venv redirector + Python), 256 MiB total, five user-mode CPU seconds per process.
	limits.Basic.Flags = 0x2000 | 0x0008 | 0x0100 | 0x0200 | 0x0002
	limits.Basic.ActiveProcessLimit = 2
	limits.Basic.ProcessTime = 5 * 10000000
	limits.ProcessMemory = 256 * 1024 * 1024
	limits.JobMemory = 256 * 1024 * 1024
	ok, _, _ := setJob.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		return nil, errors.Join(ErrWorker, closeJob())
	}
	process, err := syscall.OpenProcess(0x0100|0x0001, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, errors.Join(ErrWorker, closeJob())
	}
	defer syscall.CloseHandle(process)
	ok, _, _ = assignJob.Call(job, uintptr(process))
	if ok == 0 {
		return nil, errors.Join(ErrWorker, closeJob())
	}
	// No application code or venv redirector may run until the Job is assigned.
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createSuspended == 0 || resumePrimary(uint32(cmd.Process.Pid)) != nil {
		return nil, errors.Join(ErrWorker, closeJob())
	}
	return closeJob, nil
}

// os/exec does not expose CreateProcess's primary thread handle. While the
// process is CREATE_SUSPENDED, identify its sole initial thread via Toolhelp.
// Ambiguity or enumeration failure fails closed instead of resuming other code.
func resumePrimary(pid uint32) error {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return ErrWorker
	}
	defer syscall.CloseHandle(snapshot)
	entry := threadEntry{Size: uint32(unsafe.Sizeof(threadEntry{}))}
	ok, _, callErr := threadFirst.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	var tid uint32
	for ok != 0 {
		if entry.Owner == pid {
			if tid != 0 {
				return ErrWorker
			}
			tid = entry.ID
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		ok, _, callErr = threadNext.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	}
	if callErr != syscall.ERROR_NO_MORE_FILES || tid == 0 {
		return ErrWorker
	}
	thread, _, _ := openThread.Call(0x0802, 0, uintptr(tid)) // SUSPEND_RESUME | QUERY_LIMITED_INFORMATION
	if thread == 0 {
		return ErrWorker
	}
	defer syscall.CloseHandle(syscall.Handle(thread))
	owner, _, _ := threadProcessID.Call(thread)
	if uint32(owner) != pid {
		return ErrWorker
	} // Reject a recycled thread ID.
	previous, _, _ := resumeThread.Call(thread)
	if uint32(previous) != 1 {
		return ErrWorker
	}
	return nil
}
