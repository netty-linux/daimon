//go:build linux

package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFixedInspectorActualLinuxMetadataAndLinks(t *testing.T) {
	for _, kind := range []string{"regular", "symlink", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a")
			outside := filepath.Join(t.TempDir(), "outside")
			if e := os.WriteFile(outside, []byte("not transferred"), 0600); e != nil {
				t.Fatal(e)
			}
			var e error
			switch kind {
			case "regular":
				e = os.WriteFile(path, []byte("content"), 0600)
			case "symlink":
				e = os.Symlink(outside, path)
			case "hardlink":
				e = os.Link(outside, path)
			case "fifo":
				e = syscall.Mkfifo(path, 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			// Only this test substitutes its own temporary root into the fixed program.
			script := strings.Replace(workspaceInspector, "root = '/workspace'", "root = "+strconv.Quote(dir), 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, e := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-c", script).Output()
			if kind != "regular" {
				if e == nil || len(out) != 0 {
					t.Fatal("unsafe inspection accepted", kind)
				}
				return
			}
			if e != nil {
				t.Fatal("fixed inspector unavailable", e)
			}
			var report inspection
			if json.Unmarshal(out, &report) != nil || len(report.Entries) != 1 || report.Entries[0].Links != 1 || report.Entries[0].Inode == 0 || report.Entries[0].Size != 7 || strings.Contains(string(out), "content") {
				t.Fatal(string(out))
			}
		})
	}
}
