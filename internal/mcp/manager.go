package mcp

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/tools"
	"os"
	"sort"
	"strconv"
	"sync"
)

type EnvironmentResolver func(context.Context, string) ([]string, error)
type serverState struct {
	backend string
	startup string
	id      string
	enabled bool
	client  *Client
}
type Manager struct {
	mu      sync.RWMutex
	servers []serverState
	tools   map[string]*Tool
	closed  bool
}
type ServerMetadata struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type ToolMetadata struct {
	Name           string         `json:"name"`
	ServerID       string         `json:"server_id"`
	Description    string         `json:"description"`
	Classification Classification `json:"classification"`
	Available      bool           `json:"available"`
	Permitted      bool           `json:"permitted"`
}

// Eager, isolated startup. Broken enabled servers remain visible as unavailable;
// disabled configurations never spawn. No restart or filesystem I/O in lookup.
func NewManager(ctx context.Context, config Config, options Options, env EnvironmentResolver) (*Manager, error) {
	if Validate(config) != nil || !options.valid() || ctx == nil || env == nil {
		return nil, ErrConfig
	}
	manager := &Manager{tools: map[string]*Tool{}}
	for _, s := range config.Servers {
		if err := ctx.Err(); err != nil {
			cleanup, end := context.WithCancel(context.Background())
			_ = manager.Close(cleanup)
			end()
			return nil, err
		}
		state := serverState{id: s.ID, enabled: s.Enabled, backend: s.ComputerBackend, startup: "configured"}
		if s.Enabled {
			state.startup = "startup_failed"
			if _, e := os.Stat(s.Command); errors.Is(e, os.ErrNotExist) {
				state.startup = "executable_missing"
			}
			environment, err := env(ctx, s.ID)
			if err == nil {
				state.client, err = NewClient(ctx, s, environment, options)
			}
			if err == nil {
				if len(manager.tools)+len(state.client.tools) > MaxTotalTools {
					_ = state.client.Close(context.Background())
					state.client = nil
				} else {
					for _, definition := range state.client.tools {
						kind := s.Tools[definition.RemoteName].Classification
						if kind == "" {
							kind = Other
						}
						manager.tools[definition.Name] = &Tool{state.client, definition, kind}
					}
				}
			}
		}
		manager.servers = append(manager.servers, state)
	}
	return manager, nil
}
func (m *Manager) Lookup(name string) (tools.Tool, Classification, error) {
	if m == nil {
		return nil, "", ErrUnavailable
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, "", ErrUnavailable
	}
	tool, ok := m.tools[name]
	if !ok || !tool.client.Available() {
		return nil, "", ErrUnavailable
	}
	if m.isComputerServer(tool.ServerID()) || tool.classification != Read {
		return nil, tool.classification, ErrDenied
	}
	return tool, tool.classification, nil
}
func (m *Manager) Servers() []ServerMetadata {
	result := []ServerMetadata{}
	if m == nil {
		return result
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.servers {
		status := "unavailable"
		if !s.enabled {
			status = "disabled"
		} else if !m.closed && s.client != nil && s.client.Available() {
			status = "connected"
		}
		result = append(result, ServerMetadata{s.id, status})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (m *Manager) Tools() []ToolMetadata {
	result := []ToolMetadata{}
	if m == nil {
		return result
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.tools {
		result = append(result, ToolMetadata{t.Name(), t.ServerID(), strconv.QuoteToASCII(t.Description()), t.classification, !m.closed && t.client.Available(), t.classification == Read && !m.isComputerServer(t.ServerID())})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
func (m *Manager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	clients := make([]*Client, 0, len(m.servers))
	for _, s := range m.servers {
		if s.client != nil {
			clients = append(clients, s.client)
		}
	}
	m.mu.Unlock()
	var result error
	for _, client := range clients {
		result = errors.Join(result, client.Close(ctx))
	}
	return result
}
