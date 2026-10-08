package main

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/threads"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type serveOutput struct {
	mu    sync.Mutex
	text  strings.Builder
	ready chan struct{}
}

// Retain the Phase 5 deny-provider contract as an injected test fixture; serve
// now selects the explicit Web adapter instead of this fallback implementation.
type denyServeRead struct{}

func (denyServeRead) Approve(ctx context.Context, _ agentloop.ToolAuthorizationRequest) (bool, error) {
	return false, ctx.Err()
}

func (o *serveOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n, err := o.text.Write(p)
	select {
	case <-o.ready:
	default:
		close(o.ready)
	}
	return n, err
}
func TestServeCLIAssemblyHealthShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	output := &serveOutput{ready: make(chan struct{})}
	done := make(chan error, 1)
	listening := make(chan net.Listener, 1)
	go func() {
		done <- serveApplication(ctx, []string{"--port", "0", "--data-dir", dir}, output, func(key string) string {
			if key == "DAIMON_API_KEY" {
				return "private-key"
			}
			return ""
		}, func() (string, error) { t.Error("HOME used despite explicit directory"); return "", errors.New("home") }, func(network, addr string) (net.Listener, error) {
			if addr != "127.0.0.1:0" {
				return nil, errors.New("binding")
			}
			l, err := net.Listen(network, addr)
			if err == nil {
				listening <- l
			}
			return l, err
		})
	}()
	testCtx, end := context.WithTimeout(context.Background(), 10*time.Second)
	defer end()
	var listener net.Listener
	select {
	case listener = <-listening:
	case <-testCtx.Done():
		t.Fatal("listen timeout")
	}
	select {
	case <-output.ready:
	case <-testCtx.Done():
		t.Fatal("startup timeout")
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + listener.Addr().String() + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || strings.Contains(string(body), "private") {
		t.Fatal("health")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-testCtx.Done():
		t.Fatal("shutdown timeout")
	}
	output.mu.Lock()
	text := output.text.String()
	output.mu.Unlock()
	if strings.Contains(text, "private-key") || strings.Contains(text, dir) {
		t.Fatal("startup leakage")
	}
	if _, err := bots.NewStore(filepath.Join(dir, "bots.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := threads.NewStore(filepath.Join(dir, "threads.json")); err != nil {
		t.Fatal(err)
	}
}
func TestServeInvalidArgumentsBeforeEffects(t *testing.T) {
	for _, args := range [][]string{{"--mcp-config", ""}, {"--mcp-config", "a", "--mcp-config", "b"}, {"--addr", "0.0.0.0:3000"}, {"--addr", "127.0.0.1:3000"}, {"--port", "-1"}, {"--port", "65536"}, {"--port", "x"}, {"--port", "1", "--port", "2"}, {"--data-dir", ""}, {"--api-key", "private-key"}, {"extra"}, {"--enable-replace-file", "--enable-create-file"}, {"--enable-replace-file", "--enable-replace-file"}, {"--enable-create-file", "--enable-create-file"}} {
		err := serveApplication(context.Background(), args, io.Discard, func(string) string { t.Error("env read"); return "" }, func() (string, error) { t.Error("HOME read"); return "", nil }, func(string, string) (net.Listener, error) { t.Error("listen called"); return nil, nil })
		if !errors.Is(err, errServe) || strings.Contains(err.Error(), "private-key") {
			t.Fatalf("invalid args %v: %v", args, err)
		}
	}
}
func TestServeReadApprovalFailsClosed(t *testing.T) {
	approved, err := (denyServeRead{}).Approve(context.Background(), agentloop.ToolAuthorizationRequest{})
	if approved || err != nil {
		t.Fatal("read allowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	approved, err = (denyServeRead{}).Approve(ctx, agentloop.ToolAuthorizationRequest{})
	if approved || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation")
	}
}

func TestServeMCPInvalidConfigFailsBeforeEnvironmentOrListener(t *testing.T) {
	for _, raw := range []string{`{"version":2,"servers":[]}`, `{"version":1,"servers":[],"env":{"secret":"private"}}`, `{"version":1,"servers":null}`} {
		dir := t.TempDir()
		path := filepath.Join(dir, "mcp.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		err := serveApplication(t.Context(), []string{"--data-dir", dir, "--mcp-config", path}, io.Discard, func(string) string { t.Error("environment before config validation"); return "" }, func() (string, error) { t.Error("home"); return "", nil }, func(string, string) (net.Listener, error) {
			t.Error("listener before config validation")
			return nil, errors.New("listener")
		})
		if !errors.Is(err, errServe) {
			t.Fatal(err)
		}
	}
}
