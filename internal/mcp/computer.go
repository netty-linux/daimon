package mcp

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/tools"
)

// ComputerTransport is a narrow transport handle. It is never registered as a
// generic Tool: the Computer domain must validate arguments, hold a lease, and
// apply its own exact capability mapping and per-call authorization.
type ComputerTransport struct{ tool *Tool }

func (t ComputerTransport) Definition() Definition {
	d := t.tool.definition
	d.Schema = append(json.RawMessage(nil), d.Schema...)
	return d
}
func (t ComputerTransport) Call(ctx context.Context, args json.RawMessage, acceptText func(string) bool) (tools.ToolResult, error) {
	if acceptText == nil {
		return tools.ToolResult{}, ErrDenied
	}
	return t.tool.client.callWithTextFilter(ctx, t.tool.definition.RemoteName, args, acceptText)
}

type ComputerSource struct {
	ID, Backend, Status string
	Reason              string
}

func (m *Manager) isComputerServer(id string) bool {
	for _, s := range m.servers {
		if s.id == id {
			return s.backend != ""
		}
	}
	return false
}
func (m *Manager) ComputerSources() []ComputerSource {
	result := []ComputerSource{}
	if m == nil {
		return result
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.servers {
		if s.backend == "" {
			continue
		}
		status := s.startup
		if m.closed {
			status = "unavailable"
		} else if !s.enabled {
			status = "configured"
		} else if s.client != nil {
			status = "unavailable"
			if s.client.Available() {
				status = "connected"
			}
		}
		reason := ""
		if status == "startup_failed" {
			reason = s.reason
			if reason == "" {
				reason = "unknown"
			}
		}
		result = append(result, ComputerSource{ID: s.id, Backend: s.backend, Status: status, Reason: reason})
	}
	return result
}
func (m *Manager) ComputerTool(name string) (ComputerTransport, Classification, error) {
	if m == nil {
		return ComputerTransport{}, "", ErrUnavailable
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	t := m.tools[name]
	if m.closed || t == nil || !t.client.Available() {
		return ComputerTransport{}, "", ErrUnavailable
	}
	if !m.isComputerServer(t.ServerID()) {
		return ComputerTransport{}, "", ErrDenied
	}
	return ComputerTransport{t}, t.classification, nil
}
