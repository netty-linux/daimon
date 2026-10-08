package computer

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/tools"
	"strings"
)

// GuestCaller is already bound by the sandbox backend to one immutable guest.
// Neither model arguments nor Computer identifiers may choose its destination.
type GuestCaller func(context.Context, string, json.RawMessage) (tools.ToolResult, error)
type SandboxAdapter struct {
	id      string
	backend BackendID
	call    GuestCaller
	names   map[string]bool
}

func NewSandboxAdapter(id string, call GuestCaller, names ...string) (*SandboxAdapter, error) {
	return NewPlacedSandboxAdapter(id, CUALocal, call, names...)
}
func NewPlacedSandboxAdapter(id string, backend BackendID, call GuestCaller, names ...string) (*SandboxAdapter, error) {
	if backend != CUALocal && backend != CUACloud {
		return nil, ErrConfig
	}
	if !strings.HasPrefix(id, "sandbox-") || !serverID.MatchString(id) || call == nil {
		return nil, ErrConfig
	}
	allowed := map[string]bool{}
	for _, n := range names {
		if supported(n) {
			allowed[n] = true
		}
	}
	if len(allowed) == 0 {
		return nil, ErrUnavailable
	}
	return &SandboxAdapter{id: id, backend: backend, call: call, names: allowed}, nil
}
func (s *SandboxAdapter) ID() BackendID     { return s.backend }
func (s *SandboxAdapter) Namespace() string { return "sandbox" }
func (s *SandboxAdapter) Probe(ctx context.Context) (Info, error) {
	if e := ctx.Err(); e != nil {
		return Info{}, e
	}
	out := Info{ID: s.id, Backend: s.backend, Status: "connected", Capabilities: []Capability{}}
	for _, name := range []string{"list_apps", "list_windows", "get_accessibility_tree", "get_window_state", "click", "type_text", "bring_to_front"} {
		out.Capabilities = append(out.Capabilities, Capability{ID: name, Tool: "mcp__sandbox__" + name, Class: Classify(name), Available: s.names[name]})
	}
	return out, nil
}
func (s *SandboxAdapter) Resolve(name string) (Operation, error) {
	remote := strings.TrimPrefix(name, "mcp__sandbox__")
	if name == remote || !supported(remote) || !s.names[remote] {
		return nil, ErrDenied
	}
	return sandboxOperation{name, remote, s.call}, nil
}

type sandboxOperation struct {
	name, remote string
	call         GuestCaller
}

func (s sandboxOperation) Definition() Definition {
	return Definition{Name: s.name, Description: "Disposable sandbox computer action. Individual human approval required.", Schema: actionSchema(s.remote)}
}
func (s sandboxOperation) Call(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	if _, e := validateAction(s.remote, args); e != nil {
		return tools.ToolResult{}, e
	}
	r, e := s.call(ctx, s.remote, append(json.RawMessage(nil), args...))
	if e != nil {
		return tools.ToolResult{}, e
	}
	if !safeText(r.Content) {
		return tools.ToolResult{}, ErrUnsupported
	}
	return r, nil
}
