package computer

import (
	"context"
	"strings"
)

// A catalog aggregates independent Computers; it never provisions environments.
func (m *Manager) RegisterComputer(id string, child *Manager) error {
	if child == nil || child == m || !strings.HasPrefix(id, "sandbox-") || !serverID.MatchString(id) {
		return ErrConfig
	}
	infos, e := child.Infos(context.Background())
	if e != nil || len(infos) != 1 || infos[0].ID != id {
		return ErrConfig
	}
	child.mu.Lock()
	nested := len(child.children) > 0
	_, scoped := child.backend.(interface{ Namespace() string })
	child.mu.Unlock()
	if nested || !scoped {
		return ErrConfig
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || len(m.children) >= 4 || m.children[id] != nil {
		return ErrBusy
	}
	if m.children == nil {
		m.children = map[string]*Manager{}
	}
	m.children[id] = child
	return nil
}
func (m *Manager) UnregisterComputer(id string, child *Manager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.children[id] == child {
		delete(m.children, id)
	}
}
func (m *Manager) ForComputer(ctx context.Context, id string) (*Manager, error) {
	// Reserved sandbox IDs have a closed resolution path. Missing or stale
	// sandbox IDs never query the host backend, regardless of its reported ID.
	if strings.HasPrefix(id, "sandbox-") {
		m.mu.Lock()
		child := m.children[id]
		m.mu.Unlock()
		if child == nil {
			return nil, ErrUnavailable
		}
		return child, nil
	}
	m.mu.Lock()
	backend := m.backend
	m.mu.Unlock()
	if backend == nil {
		return nil, ErrUnavailable
	}
	info, e := backend.Probe(ctx)
	if e != nil || info.ID != id {
		return nil, ErrUnavailable
	}
	return m, nil
}
func (m *Manager) childComputers() []*Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*Manager{}
	for _, c := range m.children {
		out = append(out, c)
	}
	return out
}
