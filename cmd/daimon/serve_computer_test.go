package main

import (
	"github.com/netty-linux/daimon/internal/mcp"
	"strings"
	"testing"
)

func TestCUAEnvironmentKeepsDesktopContextAndExcludesSecrets(t *testing.T) {
	config := mcp.Config{Version: 1, Servers: []mcp.ServerConfig{{ID: "cua", ComputerBackend: "cua-local"}, {ID: "generic"}}}
	values := map[string]string{"PATH": "/bin", "DISPLAY": ":10", "XAUTHORITY": "/user/.Xauthority", "HOME": "/user", "USERPROFILE": "user-directory", "DAIMON_API_KEY": "DAIMON-SECRET", "OPENAI_API_KEY": "OPENAI-SECRET", "GROQ_API_KEY": "GROQ-SECRET", "ANTHROPIC_API_KEY": "ANTHROPIC-SECRET", "OPENROUTER_API_KEY": "OPENROUTER-SECRET", "CUA_DRIVER_PERMISSION_MODE": "unrestricted", "CUA_DRIVER_DANGEROUSLY_BYPASS_APPROVALS": "1"}
	get := func(key string) string { return values[key] }
	env := strings.Join(mcpEnvironment(config, "cua", get), "\n")
	for _, wanted := range []string{"DISPLAY=:10", "XAUTHORITY=/user/.Xauthority", "HOME=/user", "USERPROFILE=user-directory"} {
		if !strings.Contains(env, wanted) {
			t.Fatal("lost desktop context")
		}
	}
	for _, blocked := range []string{"SECRET", "API_KEY", "CUA_DRIVER_", "unrestricted"} {
		if strings.Contains(env, blocked) {
			t.Fatal("credential/permission inherited")
		}
	}
	generic := strings.Join(mcpEnvironment(config, "generic", get), "\n")
	if strings.Contains(generic, "DISPLAY=") || strings.Contains(generic, "HOME=") {
		t.Fatal("generic MCP environment expanded")
	}
}
