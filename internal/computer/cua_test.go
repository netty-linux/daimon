package computer

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/mcp"
	"github.com/netty-linux/daimon/internal/mcp/cuatest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cuaCatalog(t *testing.T, binary string) (*mcp.Manager, *CUA) {
	t.Helper()
	ctx := testCtx(t)
	config := mcp.Config{Version: 1, Servers: []mcp.ServerConfig{{ID: "cua", Command: binary, Args: []string{"mcp"}, ComputerBackend: "cua-local", Enabled: true, Tools: map[string]mcp.ToolConfig{"list_apps": {Classification: mcp.Read}, "get_window_state": {Classification: mcp.Read}, "click": {Classification: mcp.Write}, "type_text": {Classification: mcp.Write}, "future_untrusted": {Classification: mcp.Read}}}}}
	manager, err := mcp.NewManager(ctx, config, mcp.Options{InitializeTimeout: time.Second * 3, CallTimeout: time.Second, CloseTimeout: time.Millisecond * 50}, func(context.Context, string) ([]string, error) { return []string{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(testCtx(t)); err != nil {
			t.Error(err)
		}
	})
	backend, err := NewCUA(manager)
	if err != nil {
		t.Fatal(err)
	}
	return manager, backend
}
func TestCUARealStdioDiscoveryGatingAndSafeResults(t *testing.T) {
	for _, mode := range []string{"normal", "image", "text-image", "multi-text-image", "crash", "timeout", "invalid-discovery", "startup-failed"} {
		t.Run(mode, func(t *testing.T) {
			binary := cuatest.Build(t, mode)
			transport, backend := cuaCatalog(t, binary)
			ctx := testCtx(t)
			info, err := backend.Probe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "invalid-discovery" || mode == "startup-failed" {
				if info.Status != "startup_failed" {
					t.Fatal(info.Status)
				}
				want := "unknown"
				if mode == "invalid-discovery" {
					want = "discovery_failed"
				}
				if info.Reason != want {
					t.Fatalf("reason = %q, want %q", info.Reason, want)
				}
				manager, _ := NewManager(backend)
				infos, e := manager.Infos(ctx)
				if e != nil || len(infos) != 1 || infos[0].Reason != want {
					t.Fatal("metadata reason lost", e)
				}
				return
			}
			if info.Status != "connected" {
				t.Fatal(info.Status)
			}
			if _, _, err := transport.Lookup("mcp__cua__list_apps"); !errors.Is(err, mcp.ErrDenied) {
				t.Fatal("generic bypass", err)
			}
			for _, name := range []string{"future_untrusted", "get_desktop_state", "kill_app"} {
				if _, err := backend.Resolve("mcp__cua__" + name); !errors.Is(err, ErrDenied) {
					t.Fatal(name, err)
				}
			}
			m, _ := NewManager(backend)
			b, err := m.Open(ctx, "run", profile(), []string{"mcp__cua__list_apps"}, false)
			if err != nil {
				t.Fatal(err)
			}
			tool, _ := b.Tool("mcp__cua__list_apps")
			r, err := tool.Execute(ctx, []byte(`{}`))
			if mode == "normal" {
				if err != nil || r.Content != "controlled textual observation" {
					t.Fatal(r, err)
				}
			} else {
				if err == nil {
					t.Fatal("failure accepted")
				}
				if strings.Contains(r.Content, "SCREENSHOT-SECRET") || strings.Contains(err.Error(), "SCREENSHOT-SECRET") {
					t.Fatal("image escaped")
				}
			}
			if mode == "crash" || mode == "timeout" {
				info, _ = backend.Probe(ctx)
				if info.Status != "unavailable" {
					t.Fatal(info.Status)
				}
			}
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("context identity", err)
			}
			if err := b.Close(ctx); err != nil {
				t.Fatal(err)
			}
			infos, _ := m.Infos(ctx)
			if infos[0].Busy {
				t.Fatal("lease not released")
			}
			if err := transport.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if mode == "normal" {
				data, _ := os.ReadFile(binary + ".trace")
				if !strings.Contains(string(data), "closed") {
					t.Fatal("not reaped")
				}
			}
		})
	}
}
func TestCUAMissingConfiguredAndInvalidEnvironment(t *testing.T) {
	_, backend := cuaCatalog(t, filepath.Join(t.TempDir(), "cua-driver"+func() string {
		if os.PathSeparator == '\\' {
			return ".exe"
		}
		return ""
	}()))
	info, _ := backend.Probe(testCtx(t))
	if info.Status != "executable_missing" {
		t.Fatal(info.Status)
	}
	m, _ := NewManager(backend)
	if _, err := m.Open(testCtx(t), "run", profile(), nil, false); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
