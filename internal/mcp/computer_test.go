package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestComputerConfigIsExplicitLegacySingleDesktopAndNoBypass(t *testing.T) {
	s := ServerConfig{ID: "cua", ComputerBackend: "cua-local", Command: filepath.Join(os.TempDir(), "cua-driver"), Args: []string{"mcp"}, Enabled: true, Tools: map[string]ToolConfig{}}
	config := Config{Version: 1, Servers: []ServerConfig{s}}
	raw, _ := json.Marshal(config)
	if _, err := DecodeConfig(raw); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{}, {"mcp", "--permission-mode", "unrestricted"}, {"mcp", "--dangerously-bypass-approvals"}, {"daemon"}} {
		bad := s
		bad.Args = args
		if Validate(Config{Version: 1, Servers: []ServerConfig{bad}}) == nil {
			t.Fatal(args)
		}
	}
	other := s
	other.ID = "other"
	if Validate(Config{Version: 1, Servers: []ServerConfig{s, other}}) == nil {
		t.Fatal("second desktop alias")
	}
	s.ComputerBackend = ""
	if Validate(Config{Version: 1, Servers: []ServerConfig{s}}) == nil {
		t.Fatal("generic CUA bypass")
	}
	for _, env := range []string{"OPENAI_API_KEY=secret", "GROQ_API_KEY=secret", "ANTHROPIC_API_KEY=secret", "OPENROUTER_API_KEY=secret", "DAIMON_API_KEY=secret", "CUA_DRIVER_DANGEROUSLY_BYPASS_APPROVALS=1", "CUA_DRIVER_PERMISSION_MODE=unrestricted"} {
		if validateEnv([]string{env}) == nil {
			t.Fatal("credential/bypass inherited")
		}
	}
	if validateEnv([]string{"PATH=/usr/bin", "TEMP=/tmp"}) != nil {
		t.Fatal("operational env denied")
	}
}
