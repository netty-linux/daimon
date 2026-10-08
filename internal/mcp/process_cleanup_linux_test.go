//go:build linux

package mcp_test

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/mcp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLinuxProcessReapedAfterCloseAndCancellation(t *testing.T) {
	for _, mode := range []string{"normal", "timeout", "crash"} {
		t.Run(mode, func(t *testing.T) {
			trace := filepath.Join(t.TempDir(), "pid")
			m, err := mcp.NewManager(t.Context(), mcp.Config{Version: 1, Servers: []mcp.ServerConfig{config(t, "cleanup")}}, options(), func(context.Context, string) ([]string, error) {
				return []string{"MCP_FIXTURE=" + mode, "MCP_TRACE=" + trace, "GORACE=atexit_sleep_ms=0"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.Split(string(raw), "\n")[0])
			if err != nil {
				t.Fatal(err)
			}
			if mode != "normal" {
				tool, _, err := m.Lookup("mcp__cleanup__lookup")
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
				_, err = tool.Execute(ctx, []byte(`{}`))
				cancel()
				if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			}
			ctx, end := context.WithTimeout(context.Background(), 3*time.Second)
			defer end()
			if err := m.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("process still exists: %v", err)
			}
		})
	}
}
