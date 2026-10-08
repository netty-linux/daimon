package computer

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/tools"
	"sort"
	"strings"
	"sync"
)

// One local physical desktop. Conservative v1 leases serialize observations
// as well as input, preventing another Session from invalidating capture tokens.
type Manager struct {
	mu       sync.Mutex
	backend  Backend
	owner    *Binding
	closed   bool
	children map[string]*Manager
	media    MediaProvider
	views    map[string]*ViewSession
	human    *humanLease
	gate     chan struct{}
}

func NewManager(backend Backend) (*Manager, error) {
	if backend != nil && backend.ID() != CUALocal && backend.ID() != CUACloud {
		return nil, ErrConfig
	}
	if backend != nil && backend.ID() == CUACloud {
		scoped, ok := backend.(interface{ Namespace() string })
		if !ok || scoped.Namespace() != "sandbox" {
			return nil, ErrConfig
		}
	}
	return &Manager{backend: backend, views: map[string]*ViewSession{}, gate: make(chan struct{}, 1)}, nil
}
func (m *Manager) Infos(ctx context.Context) ([]Info, error) {
	m.mu.Lock()
	backend, closed, owner := m.backend, m.closed, m.owner
	m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	out := []Info{}
	if backend != nil {
		info, e := backend.Probe(ctx)
		if e != nil {
			return nil, e
		}
		info.Capabilities = append([]Capability{}, info.Capabilities...)
		if closed {
			info.Status = "unavailable"
			for i := range info.Capabilities {
				info.Capabilities[i].Available = false
			}
		}
		if owner != nil {
			info.Busy = true
			info.ControllerSessionID = owner.session
		}
		out = append(out, info)
	}
	for _, child := range m.childComputers() {
		infos, e := child.Infos(ctx)
		if e != nil {
			return nil, e
		}
		out = append(out, infos...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (m *Manager) Open(ctx context.Context, session string, p Profile, names []string, readOnly bool) (*Binding, error) {
	if m == nil || ctx == nil || len(names) > 32 || ValidateProfile(p) != nil || !p.Enabled || !sessionID.MatchString(session) {
		return nil, ErrConfig
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.closed || m.backend == nil || m.backend.ID() != p.Backend {
		return nil, ErrUnavailable
	}
	info, err := m.backend.Probe(ctx)
	if err != nil {
		return nil, err
	}
	namespace := info.ID
	if scoped, ok := m.backend.(interface{ Namespace() string }); ok {
		namespace = scoped.Namespace()
	}
	if namespace != p.MCPServerID || info.Status != "connected" {
		return nil, ErrUnavailable
	}
	if m.owner != nil || m.human != nil {
		return nil, ErrBusy
	}
	life, cancel := context.WithCancel(ctx)
	b := &Binding{namespace: namespace, manager: m, session: session, ctx: life, cancel: cancel, metadata: Metadata{ID: info.ID, Backend: p.Backend, CapabilityIDs: []string{}}, operations: map[string]Operation{}, done: make(chan struct{})}
	for _, name := range names {
		if !strings.HasPrefix(name, "mcp__"+namespace+"__") {
			continue
		}
		remote := strings.TrimPrefix(name, "mcp__"+namespace+"__")
		if !supported(remote) || (readOnly && Classify(remote) != Observe) || b.operations[name] != nil {
			cancel()
			return nil, ErrDenied
		}
		operation, err := m.backend.Resolve(name)
		if err != nil || operation == nil {
			cancel()
			if err == nil {
				err = ErrDenied
			}
			return nil, err
		}
		definition := operation.Definition()
		if definition.Name != name || !json.Valid(definition.Schema) {
			cancel()
			return nil, ErrDenied
		}
		b.operations[name] = operation
		b.metadata.CapabilityIDs = append(b.metadata.CapabilityIDs, remote)
	}
	if err := ctx.Err(); err != nil {
		cancel()
		return nil, err
	}
	m.owner = b
	return b, nil
}
func (m *Manager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	b := m.owner
	m.mu.Unlock()
	if b != nil {
		b.cancel()
	}
	err := m.closeViews(ctx)
	for _, child := range m.childComputers() {
		if e := child.Close(ctx); err == nil {
			err = e
		}
	}
	if b != nil {
		if closeErr := b.Close(ctx); err == nil {
			err = closeErr
		}
	}
	return err
}

type Binding struct {
	namespace  string
	manager    *Manager
	session    string
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	closed     bool
	metadata   Metadata
	operations map[string]Operation
	closeOnce  sync.Once
	done       chan struct{}
}

func (b *Binding) Metadata() Metadata {
	v := b.metadata
	v.CapabilityIDs = append([]string{}, v.CapabilityIDs...)
	return v
}
func (b *Binding) Tool(name string) (tools.Tool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	op, ok := b.operations[name]
	if !ok || b.closed {
		return nil, false
	}
	return boundTool{b, op, strings.TrimPrefix(name, "mcp__"+b.namespace+"__")}, true
}
func (b *Binding) Presentation(name string, args json.RawMessage) (Presentation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.ctx.Err() != nil || b.operations[name] == nil {
		return Presentation{}, ErrClosed
	}
	return describeAction(strings.TrimPrefix(name, "mcp__"+b.namespace+"__"), args)
}
func (b *Binding) Close(ctx context.Context) error {
	b.cancel() // Cancels an active transport call before waiting for its lock.
	b.closeOnce.Do(func() {
		// No goroutine per call: this single close worker lets a caller deadline stop
		// waiting without releasing the lease ahead of an uncooperative operation.
		go func() {
			b.mu.Lock()
			b.closed = true
			b.mu.Unlock()
			// Human input belongs to this exact Agent binding. Revoke it before
			// allowing a new Session to acquire the physical computer.
			_ = b.manager.revokeBinding(context.Background(), b)
			b.manager.mu.Lock()
			if b.manager.owner == b {
				b.manager.owner = nil
			}
			b.manager.mu.Unlock()
			close(b.done)
		}()
	})
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type boundTool struct {
	binding   *Binding
	operation Operation
	remote    string
}

func (t boundTool) Name() string { return "mcp__" + t.binding.namespace + "__" + t.remote }
func (t boundTool) Description() string {
	return "Local computer " + string(Classify(t.remote)) + " action: " + t.remote + ". Human approval required. Explicit window target or element_token required. No screenshots."
}
func (t boundTool) InputSchema() json.RawMessage { return actionSchema(t.remote) }
func (t boundTool) Execute(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	copyArgs := append(json.RawMessage(nil), args...)
	if _, err := validateAction(t.remote, copyArgs); err != nil {
		return tools.ToolResult{}, err
	}
	b := t.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return tools.ToolResult{}, ErrClosed
	}
	if err := b.ctx.Err(); err != nil {
		return tools.ToolResult{}, err
	}
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(b.ctx, cancel)
	defer func() { stop(); cancel() }()
	if err := b.ctx.Err(); err != nil {
		return tools.ToolResult{}, err
	}
	if err := callCtx.Err(); err != nil {
		return tools.ToolResult{}, err
	}
	if Classify(t.remote) != Observe {
		if err := b.manager.acquireGate(callCtx); err != nil {
			return tools.ToolResult{}, err
		}
		defer b.manager.releaseGate()
		b.manager.mu.Lock()
		blocked := b.manager.human != nil
		closed := b.manager.closed
		b.manager.mu.Unlock()
		if closed {
			return tools.ToolResult{}, ErrClosed
		}
		if blocked {
			return tools.ToolResult{}, ErrHumanControl
		}
	}
	r, err := t.operation.Call(callCtx, copyArgs)
	if callCtx.Err() != nil {
		return tools.ToolResult{}, callCtx.Err()
	}
	return r, err
}
