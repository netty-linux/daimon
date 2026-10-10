package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/mcp"
	"github.com/netty-linux/daimon/internal/mcp/mcptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMCPProcessHelper(t *testing.T) {
	if mcptest.Run() {
		os.Exit(0)
	}
}
func config(t *testing.T, id string) mcp.ServerConfig {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return mcp.ServerConfig{ID: id, Command: binary, Args: []string{"-test.run=TestMCPProcessHelper", "--", "literal; $(not-shell)"}, Enabled: true, Tools: map[string]mcp.ToolConfig{"lookup": {Classification: mcp.Read}, "mutate": {Classification: mcp.Write}}}
}
func options() mcp.Options {
	return mcp.Options{InitializeTimeout: 3 * time.Second, CallTimeout: 200 * time.Millisecond, CloseTimeout: 150 * time.Millisecond}
}
func manager(t *testing.T, mode string) *mcp.Manager {
	t.Helper()
	m, err := mcp.NewManager(t.Context(), mcp.Config{Version: 1, Servers: []mcp.ServerConfig{config(t, "fixture")}}, options(), func(context.Context, string) ([]string, error) {
		return []string{"MCP_FIXTURE=" + mode, "GORACE=atexit_sleep_ms=0"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, end := context.WithTimeout(context.Background(), 3*time.Second)
		defer end()
		if err := m.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})
	return m
}
func TestProtocolDiscoveryAndToolAdapter(t *testing.T) {
	m := manager(t, "pagination")
	if m.Servers()[0].Status != "connected" || len(m.Tools()) != 3 {
		t.Fatal("handshake/discovery")
	}
	tool, classification, err := m.Lookup("mcp__fixture__lookup")
	if err != nil || classification != mcp.Read {
		t.Fatal(err)
	}
	schema := tool.InputSchema()
	schema[0] = '!'
	if !json.Valid(tool.InputSchema()) {
		t.Fatal("schema alias")
	}
	result, err := tool.Execute(t.Context(), json.RawMessage(`{"query":"exact","n":9007199254740993}`))
	if err != nil || !strings.Contains(result.Content, "9007199254740993") || !strings.Contains(result.Content, "exact") {
		t.Fatal("args forwarding", err)
	}
	if _, _, err := m.Lookup("mcp__fixture__mutate"); !errors.Is(err, mcp.ErrDenied) {
		t.Fatal("write permission")
	}
	if _, _, err := m.Lookup("mcp__fixture__unknown"); !errors.Is(err, mcp.ErrDenied) {
		t.Fatal("unknown permission")
	}
	if _, _, err := m.Lookup("lookup"); !errors.Is(err, mcp.ErrUnavailable) {
		t.Fatal("raw namespace")
	}
	for _, args := range []json.RawMessage{[]byte(`null`), []byte(`[]`), []byte(`{"x":1,"x":2}`), []byte(`invalid`)} {
		if _, err := tool.Execute(t.Context(), args); !errors.Is(err, mcp.ErrArguments) {
			t.Fatal("invalid args")
		}
	}
	if _, err := tool.Execute(t.Context(), json.RawMessage(strings.Repeat("x", mcp.MaxArgumentBytes+1))); !errors.Is(err, mcp.ErrLimit) {
		t.Fatal("args limit")
	}
}
func TestProtocolViolationsFailClosed(t *testing.T) {
	for _, mode := range []string{"malformed", "wrong-id", "huge", "exit", "hang-init", "invalid-schema", "name", "duplicate", "version"} {
		t.Run(mode, func(t *testing.T) {
			c := config(t, "bad")
			o := options()
			o.InitializeTimeout = 150 * time.Millisecond
			client, err := mcp.NewClient(t.Context(), c, []string{"MCP_FIXTURE=" + mode, "GORACE=atexit_sleep_ms=0"}, o)
			if err == nil {
				_ = client.Close(t.Context())
				t.Fatal("bad server accepted")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("public error")
			}
		})
	}
}
func TestToolResultAndCallFailures(t *testing.T) {
	for _, mode := range []string{"tool-error", "rpc-error", "image", "large-result", "crash", "malformed-call", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			m := manager(t, mode)
			tool, _, err := m.Lookup("mcp__fixture__lookup")
			if err != nil {
				t.Fatal(err)
			}
			result, err := tool.Execute(t.Context(), []byte(`{"query":"secret"}`))
			switch mode {
			case "tool-error":
				if err != nil || !result.IsError || strings.Contains(result.Content, "secret") {
					t.Fatal("isError")
				}
			case "rpc-error":
				if !errors.Is(err, mcp.ErrRemote) || strings.Contains(err.Error(), "secret-remote") {
					t.Fatal("RPC error")
				}
			case "image":
				if !errors.Is(err, mcp.ErrUnsupported) {
					t.Fatal(err)
				}
			case "large-result":
				if !errors.Is(err, mcp.ErrLimit) {
					t.Fatal(err)
				}
			case "timeout":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			default:
				if err == nil {
					t.Fatal("failure")
				}
			}
			if mode == "crash" || mode == "timeout" || mode == "malformed-call" {
				if _, _, err := m.Lookup("mcp__fixture__lookup"); err == nil {
					t.Fatal("unavailable client reused")
				}
			}
		})
	}
}
func TestEnvironmentIsolationAndStderrDrain(t *testing.T) {
	t.Setenv("DAIMON_API_KEY", "host-provider-secret")
	for _, mode := range []string{"stderr", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			m := manager(t, mode)
			tool, _, err := m.Lookup("mcp__fixture__lookup")
			if err != nil {
				t.Fatal(err)
			}
			result, err := tool.Execute(t.Context(), []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(result.Content, "host-provider-secret") {
				t.Fatal("environment inherited")
			}
			if mode == "secrets" && !strings.Contains(result.Content, "arg=literal; $(not-shell)") {
				t.Fatal("argument interpolation")
			}
		})
	}
	if _, err := mcp.NewClient(t.Context(), config(t, "bad"), []string{"DAIMON_API_KEY=secret"}, options()); !errors.Is(err, mcp.ErrConfig) {
		t.Fatal("provider env accepted")
	}
}
func TestManagerIsolationDisabledAndClose(t *testing.T) {
	a, b, c := config(t, "a"), config(t, "b"), config(t, "disabled")
	c.Enabled = false
	m, err := mcp.NewManager(t.Context(), mcp.Config{Version: 1, Servers: []mcp.ServerConfig{a, b, c}}, options(), func(_ context.Context, id string) ([]string, error) {
		if id == "disabled" {
			t.Fatal("disabled spawn")
		}
		mode := "normal"
		if id == "a" {
			mode = "malformed"
		}
		return []string{"MCP_FIXTURE=" + mode, "GORACE=atexit_sleep_ms=0"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(t.Context())
	statuses := m.Servers()
	if statuses[0].Status != "unavailable" || statuses[1].Status != "connected" || statuses[2].Status != "disabled" {
		t.Fatal(statuses)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tool, _, err := m.Lookup("mcp__b__lookup")
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := tool.Execute(t.Context(), []byte(`{}`)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := m.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Lookup("mcp__b__lookup"); err == nil {
		t.Fatal("closed lookup")
	}
}
func TestStrictLocalConfiguration(t *testing.T) {
	c := mcp.Config{Version: 1, Servers: []mcp.ServerConfig{config(t, "local")}}
	raw, _ := json.Marshal(c)
	if _, err := mcp.DecodeConfig(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"version":1,"version":1,"servers":[]}`, `{"version":1,"servers":[],"env":{}}`, `{"Version":1,"servers":[]}`, `{"version":2,"servers":[]}`, `{"version":1,"servers":null}`} {
		if _, err := mcp.DecodeConfig([]byte(bad)); err == nil {
			t.Fatal("strict config")
		}
	}
	for _, mutate := range []func(*mcp.Config){func(c *mcp.Config) { c.Servers = append(c.Servers, c.Servers[0]) }, func(c *mcp.Config) { c.Servers[0].Command = filepath.Join(string(filepath.Separator), "bin", "sh") }, func(c *mcp.Config) { c.Servers[0].Tools["lookup"] = mcp.ToolConfig{Classification: "unknown"} }} {
		c := mcp.Config{Version: 1, Servers: []mcp.ServerConfig{config(t, "local")}}
		mutate(&c)
		if mcp.Validate(c) == nil {
			t.Fatal("invalid config")
		}
	}
	for _, name := range []string{"Upper", "dots.name", strings.Repeat("x", 64)} {
		if _, err := mcp.Name("server", name); err == nil {
			t.Fatal("lossy namespace")
		}
	}
	path := filepath.Join(t.TempDir(), "mcp.json")
	if _, err := mcp.LoadConfig(path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := mcp.LoadConfig(path, false); err == nil {
		t.Fatal("explicit missing config")
	}
}

func TestFloodBoundedAndOtherServerSurvivesCancellation(t *testing.T) {
	c, err := mcp.NewClient(t.Context(), config(t, "flood"), []string{"MCP_FIXTURE=flood", "GORACE=atexit_sleep_ms=0"}, options())
	if c != nil || err == nil {
		if c != nil {
			c.Close(t.Context())
		}
		t.Fatal("flood accepted")
	}
	m, err := mcp.NewManager(t.Context(), mcp.Config{Version: 1, Servers: []mcp.ServerConfig{config(t, "slow"), config(t, "healthy")}}, options(), func(_ context.Context, id string) ([]string, error) {
		mode := "normal"
		if id == "slow" {
			mode = "timeout"
		}
		return []string{"MCP_FIXTURE=" + mode, "GORACE=atexit_sleep_ms=0"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(t.Context())
	slow, _, err := m.Lookup("mcp__slow__lookup")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := slow.Execute(ctx, []byte(`{}`)); done <- err }()
	time.AfterFunc(30*time.Millisecond, cancel)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	healthy, _, err := m.Lookup("mcp__healthy__lookup")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := healthy.Execute(t.Context(), []byte(`{"query":"still healthy"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestGenericDescriptionLimitUnchanged(t *testing.T) {
	for _, mode := range []string{"description-boundary", "description-limit"} {
		t.Run(mode, func(t *testing.T) {
			c, err := mcp.NewClient(t.Context(), config(t, "fixture"), []string{"MCP_FIXTURE=" + mode, "GORACE=atexit_sleep_ms=0"}, options())
			if mode == "description-limit" {
				if !errors.Is(err, mcp.ErrLimit) {
					t.Fatalf("expected existing limit, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close(context.Background())
		})
	}
}
