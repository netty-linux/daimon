package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupReason(t *testing.T) {
	secret := `C:\private\driver https://private.invalid CUA_DRIVER_SECRET=hidden stderr-private`
	cases := []struct {
		want, phase string
		err         error
	}{
		{"environment_failed", "environment", ErrConfig},
		{"handshake_failed", "handshake", ErrUnsupported},
		{"discovery_failed", "discovery", ErrRemote},
		{"tool_limit_exceeded", "tool_count", ErrLimit},
		{"timeout", "discovery", context.DeadlineExceeded}, {"cancelled", "environment", context.Canceled},
		{"protocol_error", "handshake", ErrProtocol}, {"unknown", "environment", errors.New(secret)},
		{"unknown", "discovery", ErrLimit}, {"unknown", "handshake", ErrUnavailable},
		{"unknown", "discovery", errors.New(secret)},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			reason := startupReason(fmt.Errorf("%s: %w", secret, c.err), c.phase)
			if reason != c.want {
				t.Fatalf("reason = %q, want %q", reason, c.want)
			}
			for _, value := range strings.Fields(secret) {
				if strings.Contains(reason, value) {
					t.Fatal("private value exposed")
				}
			}
		})
	}
}
func TestEnvironmentFailureMetadataIsPrivate(t *testing.T) {
	secret := `C:\private\driver https://secret.invalid CUA_DRIVER_TOKEN=secret stderr-private`
	executable := filepath.Join(t.TempDir(), "cua-driver")
	if err := os.WriteFile(executable, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Version: 1, Servers: []ServerConfig{{ID: "cua", Command: executable, Args: []string{"mcp"}, Enabled: true, ComputerBackend: "cua-local", Tools: map[string]ToolConfig{}}}}
	m, err := NewManager(t.Context(), cfg, DefaultOptions(), func(context.Context, string) ([]string, error) { return nil, fmt.Errorf("%s: %w", secret, ErrConfig) })
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	sources := m.ComputerSources()
	if len(sources) != 1 || sources[0].Status != "startup_failed" || sources[0].Reason != "environment_failed" {
		t.Fatal(sources)
	}
	raw, _ := json.Marshal(sources)
	for _, value := range strings.Fields(secret) {
		if strings.Contains(string(raw), value) {
			t.Fatal("private metadata exposed")
		}
	}
}
