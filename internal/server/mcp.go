package server

import (
	"github.com/netty-linux/daimon/internal/mcp"
	"net/http"
)

func (s *Server) mcpServers(w http.ResponseWriter) {
	items := []mcp.ServerMetadata{}
	if s.deps.MCP != nil {
		items = s.deps.MCP.Servers()
	}
	respond(w, 200, map[string]any{"servers": items})
}
func (s *Server) mcpTools(w http.ResponseWriter) {
	items := []mcp.ToolMetadata{}
	if s.deps.MCP != nil {
		items = s.deps.MCP.Tools()
	}
	respond(w, 200, map[string]any{"tools": items})
}
