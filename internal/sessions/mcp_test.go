package sessions

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/mcp"
	"github.com/netty-linux/daimon/internal/mcp/mcptest"
	"github.com/netty-linux/daimon/internal/model"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMCPProcessHelper(t *testing.T) {
	if mcptest.Run() {
		os.Exit(0)
	}
}
func sessionMCP(t *testing.T, mode string) (*mcp.Manager, string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "process.txt")
	config := mcp.Config{Version: 1, Servers: []mcp.ServerConfig{{ID: "local", Command: binary, Args: []string{"-test.run=TestMCPProcessHelper"}, Enabled: true, Tools: map[string]mcp.ToolConfig{"lookup": {Classification: mcp.Read}, "mutate": {Classification: mcp.Write}}}}}
	callTimeout := 2 * time.Second
	if mode == "timeout" {
		callTimeout = 150 * time.Millisecond
	}
	manager, err := mcp.NewManager(testContext(t), config, mcp.Options{InitializeTimeout: 3 * time.Second, CallTimeout: callTimeout, CloseTimeout: 100 * time.Millisecond}, func(context.Context, string) ([]string, error) {
		return []string{"MCP_FIXTURE=" + mode, "MCP_TRACE=" + trace, "GORACE=atexit_sleep_ms=0"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(testContext(t)); err != nil {
			t.Error(err)
		}
	})
	return manager, trace
}
func TestMCPSessionApprovalAndPersistence(t *testing.T) {
	for _, mode := range []string{"allow", "deny", "timeout", "crash", "malformed-call"} {
		t.Run(mode, func(t *testing.T) {
			serverMode := "normal"
			if mode == "timeout" || mode == "crash" || mode == "malformed-call" {
				serverMode = mode
			}
			catalog, trace := sessionMCP(t, serverMode)
			var calls atomic.Int32
			deps, options, bot, _ := fixture(t, func(_ context.Context, r model.ModelRequest) (model.ModelResponse, error) {
				calls.Add(1)
				if r.Messages[len(r.Messages)-1].Role == model.RoleUser {
					if len(r.Tools) != 1 || r.Tools[0].Name != "mcp__local__lookup" {
						t.Error("catalog intersection")
					}
					return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "private-exact-call", Name: "mcp__local__lookup", Arguments: []byte(`{"query":"private-argument"}`)}}}, nil
				}
				last := r.Messages[len(r.Messages)-1]
				if mode == "allow" && !strings.Contains(last.Content, "private-argument") {
					t.Error("MCP receipt missing")
				}
				if mode != "allow" && !last.IsError {
					t.Error("controlled denial/error")
				}
				return model.ModelResponse{FinalText: "persisted MCP response"}, nil
			})
			store, err := conversations.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			deps.Conversations = store
			deps.MCP = catalog
			deps.WebApprovals = true
			bot.bot.Tools = []string{"mcp__local__lookup"}
			runtime := manager(t, deps, options)
			start(t, runtime, "mcp-run", "thread")
			p := awaitApproval(t, runtime, "mcp-run", "")
			encoded, _ := json.Marshal(p)
			for _, secret := range []string{"private-argument", "private-exact-call", "instructions", "schema"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("approval disclosure")
				}
			}
			if p.ServerID != "local" || p.Classification != "read" || p.Preview != "" {
				t.Fatal("external presentation")
			}
			data, err := os.ReadFile(trace)
			if err != nil || strings.Contains(string(data), "call") {
				t.Fatal("discovery invoked tool")
			}
			decision := ApprovalAllow
			if mode == "deny" {
				decision = ApprovalDeny
			}
			if err := runtime.ResolveApproval("mcp-run", p.ID, decision); err != nil {
				t.Fatal(err)
			}
			snapshot, err := runtime.Wait(testContext(t), "mcp-run")
			if mode == "timeout" {
				if err == nil || snapshot.Status != Failed {
					t.Fatal("timeout became success")
				}
			} else {
				if err != nil || snapshot.Status != Completed {
					t.Fatal(err)
				}
			}
			data, _ = os.ReadFile(trace)
			if (mode == "deny") != (!strings.Contains(string(data), "call")) {
				t.Fatal("unauthorized execution")
			}
			records, err := store.List(testContext(t), "thread")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "timeout" {
				if len(records) != 1 {
					t.Fatal("failed assistant persisted")
				}
			} else {
				if len(records) != 2 || records[1].Content != "persisted MCP response" {
					t.Fatal("transcript")
				}
			}
			if calls.Load() < 1 {
				t.Fatal("model missing")
			}
			snapshotJSON, _ := json.Marshal(snapshot)
			if strings.Contains(string(snapshotJSON), "private-argument") {
				t.Fatal("snapshot content")
			}
		})
	}
}
func TestMCPStartupDeniesUnknownAndWrites(t *testing.T) {
	for _, name := range []string{"mcp__local__missing", "mcp__local__mutate", "mcp__local__unknown"} {
		t.Run(name, func(t *testing.T) {
			catalog, trace := sessionMCP(t, "normal")
			deps, options, bot, _ := fixture(t, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
				t.Error("model before capability validation")
				return model.ModelResponse{}, nil
			})
			deps.MCP = catalog
			deps.WebApprovals = true
			bot.bot.Tools = []string{name}
			runtime := manager(t, deps, options)
			start(t, runtime, "denied", "thread")
			snapshot, err := runtime.Wait(testContext(t), "denied")
			if err == nil || snapshot.ErrorCategory != ToolResolution {
				t.Fatal("denied resolution")
			}
			data, _ := os.ReadFile(trace)
			if strings.Contains(string(data), "call") {
				t.Fatal("denied effect")
			}
		})
	}
}
