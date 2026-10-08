package server

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/mcp"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestComputerReadOnlyRoutesAndMissingProjection(t *testing.T) {
	f := setup(t, finalModel, 32)
	source, err := mcp.NewManager(context.Background(), mcp.Config{Version: 1, Servers: []mcp.ServerConfig{{ID: "cua", ComputerBackend: "cua-local", Command: filepath.Join(t.TempDir(), func() string {
		if runtime.GOOS == "windows" {
			return "cua-driver.exe"
		}
		return "cua-driver"
	}()), Args: []string{"mcp"}, Enabled: true, Tools: map[string]mcp.ToolConfig{}}}}, mcp.Options{InitializeTimeout: time.Second, CallTimeout: time.Second, CloseTimeout: time.Millisecond}, func(context.Context, string) ([]string, error) { return []string{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close(context.Background())
	backend, _ := computer.NewCUA(source)
	manager, _ := computer.NewManager(backend)
	f.server.deps.Computers = manager
	for _, path := range []string{"/api/v1/computers", "/api/v1/computers/cua"} {
		w := request(f.server, "GET", path, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "executable_missing") {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, secret := range []string{"command", "arguments", "screenshot", "stderr", "env", "private", "schema"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("private field")
			}
		}
		for _, method := range []string{"POST", "PUT", "DELETE"} {
			if request(f.server, method, path, map[string]string{"tool": "click"}).Code != 405 {
				t.Fatal("action route")
			}
		}
		if request(f.server, "GET", path+"?screenshot=true", nil).Code != 400 {
			t.Fatal("query")
		}
	}
	for _, path := range []string{"/api/v1/computers/cua/click", "/api/v1/computers/cua/type", "/api/v1/computers/cua/screenshot", "/api/v1/computers/cua/actions", "/api/v1/computers/cua/launch"} {
		if request(f.server, "POST", path, map[string]string{"text": "private"}).Code != 404 {
			t.Fatal(path)
		}
	}
}
func TestBotHTTPOptionalProfileIsExactAndNoComputerActionFields(t *testing.T) {
	f := setup(t, finalModel, 32)
	b := f.bot
	b.ComputerProfile = &computer.Profile{Enabled: true, Backend: computer.CUALocal, MCPServerID: "cua"}
	encoded, _ := json.Marshal(b)
	w := request(f.server, "PUT", "/api/v1/bots/bot", string(encoded))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "computer_profile") {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "instructions") {
		t.Fatal("instructions leak")
	}
	for _, bad := range []string{`{"enabled":true,"backend":"cua-local"}`, `{"enabled":true,"Enabled":false,"backend":"cua-local","mcp_server_id":"cua"}`, `{"enabled":true,"enabled":false,"backend":"cua-local","mcp_server_id":"cua"}`, `{"enabled":true,"backend":"cua-local","mcp_server_id":"cua","unrestricted":true}`} {
		p, _ := json.Marshal(b.ComputerProfile)
		data := strings.Replace(string(encoded), string(p), bad, 1)
		if request(f.server, "PUT", "/api/v1/bots/bot", data).Code != 400 {
			t.Fatal(bad)
		}
	}
	if request(f.server, "POST", "/api/v1/sessions", `{"id":"run","thread_id":"thread","message":"act","computer":true}`).Code != 400 {
		t.Fatal("browser grants computer")
	}
}
