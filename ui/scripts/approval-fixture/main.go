//go:build approvalsmoke

// Optional offline browser fixture. Uses the real runtime/contracts/HTTP server
// with a deterministic injected Model; only disposable /fixture data is touched.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/computer/mediatest"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/environments"
	"github.com/netty-linux/daimon/internal/mcp"
	"github.com/netty-linux/daimon/internal/mcp/mcptest"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sandbox"
	"github.com/netty-linux/daimon/internal/server"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type fakeModel struct{ workspace *environmentFixture }

func (f fakeModel) Generate(ctx context.Context, r model.ModelRequest) (model.ModelResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ModelResponse{}, err
	}
	last := r.Messages[len(r.Messages)-1]
	user, index := "", 0
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == model.RoleUser {
			user, index = r.Messages[i].Content, i
			break
		}
	}
	if strings.HasPrefix(user, "Environment") {
		if f.workspace == nil {
			return model.ModelResponse{}, errors.New("fixture missing workspace")
		}
		if user == "Environment abort" {
			<-ctx.Done()
			return model.ModelResponse{}, ctx.Err()
		}
		f.workspace.mu.Lock()
		defer f.workspace.mu.Unlock()
		if user == "Environment fail" {
			return model.ModelResponse{}, errors.New("fixture controlled failure")
		}
		if user == "Environment sync fail" {
			f.workspace.failSync = true
			return model.ModelResponse{FinalText: "should not persist"}, nil
		}
		if user == "Environment add A" {
			f.workspace.w = append(f.workspace.w, environments.Entry{Path: "A", Data: []byte("file A")})
		}
		if user == "Environment add B" {
			if len(f.workspace.w) != 1 || f.workspace.w[0].Path != "A" {
				return model.ModelResponse{}, errors.New("fixture missing A")
			}
			f.workspace.w = append(f.workspace.w, environments.Entry{Path: "B", Data: []byte("file B")})
		}
		if user == "Environment check" {
			if len(f.workspace.w) != 2 {
				return model.ModelResponse{}, errors.New("fixture missing A+B")
			}
		}
		return model.ModelResponse{FinalText: "Environment complete"}, nil
	}
	if strings.HasPrefix(user, "Sandbox") {
		count := 0
		for _, msg := range r.Messages[index+1:] {
			if msg.Role == model.RoleTool {
				count++
			}
		}
		if count < 2 {
			return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: fmt.Sprintf("sandbox-action-%d", count), Name: "mcp__sandbox__click", Arguments: []byte(`{"pid":1,"window_id":2,"x":3,"y":4}`)}}}, nil
		}
		return model.ModelResponse{FinalText: "Sandbox takeover completed"}, nil
	}
	if strings.HasPrefix(user, "Computer") {
		if user == "Computer takeover" {
			count := 0
			for _, msg := range r.Messages[index+1:] {
				if msg.Role == model.RoleTool {
					count++
				}
			}
			if count < 2 {
				return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: fmt.Sprintf("takeover-%d", count), Name: "mcp__cua__click", Arguments: []byte(`{"pid":1,"window_id":2,"x":3,"y":4}`)}}}, nil
			}
			return model.ModelResponse{FinalText: "Computer takeover completed"}, nil
		}
		if last.IsError {
			return model.ModelResponse{FinalText: "Computer controlled failure"}, nil
		}
		count := 0
		for _, msg := range r.Messages[index+1:] {
			if msg.Role == model.RoleTool {
				count++
			}
		}
		if user == "Computer sequence" && count < 3 {
			names := []string{"list_apps", "click", "type_text"}
			args := []string{`{}`, `{"pid":1,"window_id":2,"x":3,"y":4}`, `{"pid":1,"window_id":2,"text":"Exact browser text\n<safe>"}`}
			return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: fmt.Sprintf("computer-call-%d", count), Name: "mcp__cua__" + names[count], Arguments: []byte(args[count])}}}, nil
		}
		if count == 0 {
			return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "computer-click", Name: "mcp__cua__click", Arguments: []byte(`{"pid":1,"window_id":2,"x":3,"y":4}`)}}}, nil
		}
		return model.ModelResponse{FinalText: "Computer sequence completed"}, nil
	}
	if last.Role == model.RoleUser {
		name, args := "read_file", `{"path":"read.txt"}`
		if strings.HasPrefix(last.Content, "MCP") {
			name, args = "mcp__local__lookup", `{"query":"browser-private-args"}`
		}
		if last.Content == "Write deny" || last.Content == "Write allow" {
			name, args = "replace_file", `{"path":"target.txt","content":"proposed\n<safe>"}`
		}
		return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "call-fixture", Name: name, Arguments: []byte(args)}}}, nil
	}
	user = ""
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == model.RoleUser {
			user = r.Messages[i].Content
			break
		}
	}
	if strings.HasPrefix(user, "MCP") {
		if last.IsError {
			return model.ModelResponse{FinalText: "MCP denied"}, nil
		}
		return model.ModelResponse{FinalText: "MCP response persisted"}, nil
	}
	text := "Read allowed"
	if user == "Write deny" || user == "Write allow" {
		text = "Write allowed"
	}
	if last.IsError {
		if text == "Read allowed" {
			text = "Read denied"
		} else {
			text = "Write denied"
		}
	}
	return model.ModelResponse{FinalText: text}, nil
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	const base = "/fixture"
	if err := os.MkdirAll(filepath.Join(base, "workspace"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(base, "conversations"), 0700); err != nil {
		return err
	}
	for name, content := range map[string]string{"read.txt": "READ-MARKER", "target.txt": "original"} {
		if err := os.WriteFile(filepath.Join(base, "workspace", name), []byte(content), 0600); err != nil {
			return err
		}
	}
	b, err := bots.NewStore(filepath.Join(base, "bots.json"))
	if err != nil {
		return err
	}
	th, err := threads.NewStore(filepath.Join(base, "threads.json"))
	if err != nil {
		return err
	}
	conv, err := conversations.NewStore(filepath.Join(base, "conversations"))
	if err != nil {
		return err
	}
	defer conv.Close()
	sharedB, sharedT := server.WrapBots(b), server.WrapThreads(th)
	mcpConfig := mcp.Config{Version: 1, Servers: []mcp.ServerConfig{}}
	if os.Getenv("MCP_SMOKE") == "1" {
		mcpConfig.Servers = append(mcpConfig.Servers, mcp.ServerConfig{ID: "local", Command: "/tmp/approval-fixture", Args: []string{}, Enabled: true, Tools: map[string]mcp.ToolConfig{"lookup": {Classification: mcp.Read}}})
	}
	if os.Getenv("COMPUTER_SMOKE") == "1" {
		command := "/tmp/cua-driver"
		if os.Getenv("CUA_MISSING") == "1" {
			command = "/fixture/missing/cua-driver"
		}
		mcpConfig.Servers = append(mcpConfig.Servers, mcp.ServerConfig{ID: "cua", ComputerBackend: "cua-local", Command: command, Args: []string{"mcp"}, Enabled: true, Tools: map[string]mcp.ToolConfig{"list_apps": {Classification: mcp.Read}, "click": {Classification: mcp.Write}, "type_text": {Classification: mcp.Write}, "future_untrusted": {Classification: mcp.Read}}})
	}
	mcpManager, err := mcp.NewManager(ctx, mcpConfig, mcp.DefaultOptions(), func(context.Context, string) ([]string, error) {
		return []string{"MCP_FIXTURE=normal", "MCP_TRACE=/fixture/mcp-trace"}, nil
	})
	if err != nil {
		return err
	}
	defer mcpManager.Close(context.Background())
	var backend computer.Backend
	if len(mcpManager.ComputerSources()) > 0 {
		backend, err = computer.NewCUA(mcpManager)
		if err != nil {
			return err
		}
	}
	computers, err := computer.NewManager(backend)
	if err != nil {
		return err
	}
	defer computers.Close(context.Background())
	var mediaFixture *mediatest.Fixture
	if os.Getenv("COMPUTER_VIEW_SMOKE") == "1" {
		mediaFixture = mediatest.New("fixture-server-only-media-secret")
		defer mediaFixture.Close()
		daemon := httptest.NewServer(mediaFixture)
		defer daemon.Close()
		media, err := computer.NewCUAMedia(daemon.URL, mediaFixture.Token)
		if err != nil {
			return err
		}
		if err = computers.ConfigureMedia(media); err != nil {
			return err
		}
	}
	var environmentStore *environments.Store
	if os.Getenv("ENVIRONMENT_SMOKE") == "1" {
		directory := filepath.Join(base, "environments")
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		environmentStore, err = environments.NewStore(directory)
		if err != nil {
			return err
		}
		defer environmentStore.Close()
	}
	var sandboxManager *sandbox.Manager
	if os.Getenv("ENVIRONMENT_SMOKE") == "1" || os.Getenv("SANDBOX_SMOKE") == "1" || os.Getenv("CLOUD_SMOKE") == "1" {
		sb := &sandboxFixture{remotes: map[string]sandbox.Remote{}, media: mediaFixture, cloud: os.Getenv("CLOUD_SMOKE") == "1"}
		if os.Getenv("ENVIRONMENT_SMOKE") == "1" {
			cloud := &sandboxFixture{remotes: map[string]sandbox.Remote{}, cloud: true}
			sandboxManager, err = sandbox.NewManagerWithCloud(filepath.Join(base, "sandboxes.json"), sb, cloud, computers, sandbox.DefaultOptions())
		} else if sb.cloud {
			sandboxManager, err = sandbox.NewManagerWithCloud(filepath.Join(base, "sandboxes.json"), sandbox.UnavailableBackend{}, sb, computers, sandbox.DefaultOptions())
		} else {
			sandboxManager, err = sandbox.NewManager(filepath.Join(base, "sandboxes.json"), sb, computers, sandbox.DefaultOptions())
		}
		if err != nil {
			return err
		}
		defer sandboxManager.Close(context.Background())
	}
	registry := &providers.Registry{}
	if err := registry.Register(providers.Factory{ID: "fixture", Build: func(providers.Config) (model.Model, error) {
		return fakeModel{workspace: currentEnvironmentFixture()}, nil
	}}); err != nil {
		return err
	}
	m, err := sessions.NewManager(sessions.Dependencies{Environments: environmentStore, Bots: sharedB, Threads: sharedT, Providers: registry, Conversations: conv, WebApprovals: true, MCP: mcpManager, Computers: computers, Sandboxes: sandboxManager, Config: func(context.Context, providers.ID) (providers.Config, error) {
		return providers.Config{MaxResponseBytes: 4096}, nil
	}}, sessions.Options{Budget: agentloop.DefaultBudget(), EventCapacity: 1024, MaxSessions: 32})
	if err != nil {
		return err
	}
	s, err := server.New(server.Dependencies{Environments: environmentStore, Bots: sharedB, Threads: sharedT, Providers: registry, Sessions: m, MCP: mcpManager, Computers: computers, Sandboxes: sandboxManager, SessionContext: ctx, Conversations: conv, EnableReplaceFile: true})
	if err != nil {
		return err
	}
	l, err := net.Listen("tcp", "127.0.0.1:3000")
	if err != nil {
		return err
	}
	// Port forwarding reaches the container interface. Serve still requires an
	// actual loopback listener, so a fixture-only TCP forwarder bridges the port.
	forward, err := net.Listen("tcp", "0.0.0.0:3001")
	if err != nil {
		return err
	}
	defer forward.Close()
	go func() {
		for {
			conn, err := forward.Accept()
			if err != nil {
				return
			}
			go forwardConnection(conn)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	if mediaFixture != nil {
		// Test-only loopback observation port; never part of the product API.
		probe := &http.Server{Addr: "127.0.0.1:3002", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"inputs": mediaFixture.Inputs(), "active": mediaFixture.Active()})
		})}
		go probe.ListenAndServe()
		defer probe.Close()
	}
	fmt.Println("READY")
	<-ctx.Done()
	deadline, end := context.WithTimeout(context.Background(), 8*time.Second)
	defer end()
	if err := s.Shutdown(deadline); err != nil {
		_ = s.Close()
	}
	if err := computers.StopMedia(deadline); err != nil {
		return err
	}
	if err := m.Close(deadline); err != nil {
		return err
	}
	if err := <-done; err != nil {
		return err
	}
	if sandboxManager != nil {
		if err := sandboxManager.Close(deadline); err != nil {
			return err
		}
	}
	if err := computers.Close(deadline); err != nil {
		return err
	}
	if mediaFixture != nil {
		if mediaFixture.Active() != 0 {
			return fmt.Errorf("media leak")
		}
		fmt.Println("MEDIA CLOSED")
	}
	if err := mcpManager.Close(deadline); err != nil {
		return err
	}
	fmt.Println("COMPUTER CLOSED")
	fmt.Println("MCP CLOSED")
	fmt.Println("CLOSED")
	return nil
}
func forwardConnection(conn net.Conn) {
	defer conn.Close()
	target, err := net.Dial("tcp", "127.0.0.1:3000")
	if err != nil {
		return
	}
	defer target.Close()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(target, conn)
		if tcp, ok := target.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		close(done)
	}()
	_, _ = io.Copy(conn, target)
	_ = conn.Close()
	_ = target.Close()
	<-done
}
func main() {
	if mcptest.Run() {
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fixture failed")
		os.Exit(1)
	}
}
