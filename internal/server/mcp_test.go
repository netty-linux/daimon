package server

import (
	"github.com/netty-linux/daimon/internal/mcp"
	"net/http"
	"strings"
	"testing"
)

type mcpMetadataFixture struct{}

func (mcpMetadataFixture) Servers() []mcp.ServerMetadata {
	return []mcp.ServerMetadata{{ID: "local", Status: "unavailable"}}
}
func (mcpMetadataFixture) Tools() []mcp.ToolMetadata {
	return []mcp.ToolMetadata{{Name: "mcp__local__lookup", ServerID: "local", Description: `"safe description"`, Classification: mcp.Read, Available: false, Permitted: true}}
}
func TestMCPReadOnlyMetadataBoundary(t *testing.T) {
	f := setup(t, finalModel, 32)
	f.server.deps.MCP = mcpMetadataFixture{}
	for _, route := range []string{"/api/v1/mcp/servers", "/api/v1/mcp/tools"} {
		w := request(f.server, http.MethodGet, route, nil)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		for _, secret := range []string{"command", "env", "stderr", "arguments", "inputSchema", "private"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("private metadata")
			}
		}
		for _, method := range []string{"POST", "PUT", "DELETE"} {
			w = request(f.server, method, route, map[string]string{"command": "untrusted"})
			if w.Code != 405 {
				t.Fatal("browser config accepted", w.Code)
			}
		}
		if w := request(f.server, "GET", route+"?unknown=1", nil); w.Code != 400 {
			t.Fatal("query")
		}
	}
	for _, route := range []string{"/api/v1/mcp", "/api/v1/mcp/invalid", "/api/v1/mcp/servers/local"} {
		if w := request(f.server, "GET", route, nil); w.Code != 404 {
			t.Fatal("unknown route", w.Code)
		}
	}
}
