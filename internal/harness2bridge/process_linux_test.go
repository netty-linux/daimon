//go:build linux

package harness2bridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLinuxOrdinaryDescendantGroupTermination(t *testing.T) {
	cfg := integrationConfig(t)
	marker := filepath.Join(t.TempDir(), "child-pid")
	fixture := filepath.Join(t.TempDir(), "worker.py")
	code := "import os,time; from pathlib import Path; child=os.fork(); " +
		"p=Path(" + strconv.Quote(marker+".tmp") + "); " +
		"p.write_text(str(child)) if child else None; " +
		"p.replace(" + strconv.Quote(marker) + ") if child else None; time.sleep(60)"
	if err := os.WriteFile(fixture, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cfg.Python, "-I", "-S", "-B", fixture)
	cmd.Env = []string{}
	prepareProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = killProcess(cmd); _ = cmd.Wait() }()
	release, err := containProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	var childPID int
waitReady:
	for {
		select {
		case <-deadline.C:
			t.Fatal("child readiness deadline")
		case <-tick.C:
			data, _ := os.ReadFile(marker)
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				childPID = pid
				break waitReady
			}
		}
	}
	pgid, err := syscall.Getpgid(childPID)
	if err != nil || pgid != cmd.Process.Pid {
		t.Fatal("ordinary child outside owned group", pgid, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	// Descendants are not direct Go children: Z proves termination, NOT reaping.
	// An adversarial tree/sessions escape requires a future cgroup/supervisor.
	for {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(childPID) + "/stat")
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		end := strings.LastIndex(string(data), ") ")
		if end >= 0 && len(data) > end+2 && data[end+2] == 'Z' {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("child still running after group kill")
		case <-tick.C:
		}
	}
}
