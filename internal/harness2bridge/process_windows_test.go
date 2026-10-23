//go:build windows

package harness2bridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWindowsResourceLimit(t *testing.T) {
	cfg := integrationConfig(t)
	cfg.Worker = filepath.Join(t.TempDir(), "worker.py")
	// A valid reply after an allocation above the Job ceiling must never arrive.
	code := `import sys
sys.stdin.buffer.read()
bytearray(400*1024*1024)
print('{}')
`
	if err := os.WriteFile(cfg.Worker, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.Health(context.Background()); !errors.Is(err, ErrWorker) {
		t.Fatal("memory ceiling not enforced", err)
	}
}

// The configured venv executable is itself a launcher. It must remain suspended
// until ownership is established, and its interpreter must die on Job closure.
func TestWindowsVenvBootstrapOwnership(t *testing.T) {
	cfg := integrationConfig(t)
	marker := filepath.Join(t.TempDir(), "pid")
	fixture := filepath.Join(t.TempDir(), "worker.py")
	code := "import os,time; from pathlib import Path; p=Path(" + strconv.Quote(marker+".tmp") + "); p.write_text(str(os.getpid())); p.replace(" + strconv.Quote(marker) + "); time.sleep(60)"
	if err := os.WriteFile(fixture, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cfg.Python, "-I", "-S", "-B", fixture)
	cmd.Env = []string{}
	prepareProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	// Deliberately hold the Start/containment window open. The process must not
	// execute Python during this window, even if stdin has not been delivered.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	childPID := 0
beforeContain:
	for {
		select {
		case <-deadline.C:
			break beforeContain
		case <-tick.C:
			data, _ := os.ReadFile(marker)
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				childPID = pid
				break beforeContain
			}
		}
	}
	if childPID != 0 {
		t.Error("Python executed before containment")
	}
	release, err := containProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if childPID == 0 {
		deadline.Reset(10 * time.Second)
	afterContain:
		for {
			select {
			case <-deadline.C:
				t.Fatal("worker did not resume")
			case <-tick.C:
				data, _ := os.ReadFile(marker)
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					childPID = pid
					break afterContain
				}
			}
		}
	}
	t.Logf("launcher PID=%d, interpreter PID=%d", cmd.Process.Pid, childPID)
	child, err := syscall.OpenProcess(0x00100000|0x0001, false, uint32(childPID))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(child)
	defer func() { _ = syscall.TerminateProcess(child, 1); _, _ = syscall.WaitForSingleObject(child, 5000) }()
	if err := release(); err != nil {
		t.Fatal(err)
	}
	// Successful release must join the interpreter, not only signal termination.
	state, err := syscall.WaitForSingleObject(child, 0)
	if err != nil || state != syscall.WAIT_OBJECT_0 {
		t.Error("venv child survived Job closure", state, err)
	}
}

func TestWindowsContainmentFailureDoesNotResume(t *testing.T) {
	cfg := integrationConfig(t)
	marker := filepath.Join(t.TempDir(), "must-not-run")
	fixture := filepath.Join(t.TempDir(), "worker.py")
	if err := os.WriteFile(fixture, []byte("from pathlib import Path; Path("+strconv.Quote(marker)+").write_text('ran')"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cfg.Python, "-I", "-S", "-B", fixture)
	cmd.Env = []string{}
	prepareProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	// Refuse to resume if the recorded startup mode is no longer trustworthy.
	// The real child is still suspended and owned; do not open an already-reaped PID.
	cmd.SysProcAttr.CreationFlags = 0
	release, err := containProcess(cmd)
	if release != nil {
		_ = release()
	}
	if !errors.Is(err, ErrWorker) {
		t.Fatal("expected initialization failure", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed startup executed worker", err)
	}
}
