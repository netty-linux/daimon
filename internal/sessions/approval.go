package sessions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/mcp"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/threads"
	"regexp"
	"strings"
	"unicode/utf8"
)

type ApprovalID string
type ApprovalDecision string

const (
	ApprovalAllow                ApprovalDecision = "allow"
	ApprovalDeny                 ApprovalDecision = "deny"
	MaxApprovalPresentationBytes                  = 4*1024*1024 - 1024
	MaxApprovalPreviewBytes                       = 1024 * 1024
	MaxApprovalsPerSession                        = 128
)

var (
	ErrApprovalNotFound     = errors.New("approval: not found")
	ErrApprovalResolved     = errors.New("approval: already resolved")
	ErrApprovalMismatch     = errors.New("approval: session mismatch")
	ErrApprovalNotPending   = errors.New("approval: not pending")
	ErrApprovalDecision     = errors.New("approval: invalid decision")
	ErrApprovalPresentation = errors.New("approval: invalid or incomplete presentation")
	ErrApprovalLimit        = errors.New("approval: capacity exceeded")
)

// Deliberate human review only. It excludes raw arguments, call IDs, provider
// config, Bot instructions and unrelated conversation text.
type ApprovalPresentation struct {
	ComputerID     string     `json:"computer_id,omitempty"`
	Backend        string     `json:"backend,omitempty"`
	ID             ApprovalID `json:"id"`
	SessionID      ID         `json:"session_id"`
	Tool           string     `json:"tool"`
	Kind           string     `json:"kind"`
	Target         string     `json:"target"` // Native quoted path or complete sanitized Computer action target.
	Warning        string     `json:"warning"`
	Preview        string     `json:"preview"` // Native contract display or full Computer typing preview; empty for reads.
	ServerID       string     `json:"server_id,omitempty"`
	Classification string     `json:"classification,omitempty"`
}
type pendingApproval struct {
	presentation ApprovalPresentation
	callID       string // private exact-call identity; never a DTO/event.
	ctx          context.Context
	decision     chan ApprovalDecision // one buffered decision; no waiter goroutine.
}

func validatePresentation(p ApprovalPresentation) error {
	if p.Target == "" || p.Warning == "" || !utf8.ValidString(p.Target) || !utf8.ValidString(p.Warning) || !utf8.ValidString(p.Preview) {
		return ErrApprovalPresentation
	}
	if p.Kind == "computer" {
		if !mcp.IsName(p.Tool) || !validComputerNamespace(p.ServerID, p.ComputerID) || (p.Backend != string(computer.CUALocal) && (p.Backend != string(computer.CUACloud) || p.ServerID != "sandbox")) || !strings.HasPrefix(p.Tool, "mcp__"+p.ServerID+"__") || len(p.Preview) > MaxApprovalPreviewBytes {
			return ErrApprovalPresentation
		}
		class := computer.Classify(strings.SplitN(p.Tool, "__", 3)[2])
		if class == computer.Dangerous || string(class) != p.Classification {
			return ErrApprovalPresentation
		}
		if class == computer.Input && p.Preview == "" {
			return ErrApprovalPresentation
		}
	} else {
		switch p.Tool {
		case "read_file", "list_dir":
			if p.Kind != "read" || p.Preview != "" {
				return ErrApprovalPresentation
			}
		case "replace_file", "create_file":
			if p.Kind != "write" || p.Preview == "" || len(p.Preview) > MaxApprovalPreviewBytes {
				return ErrApprovalPresentation
			}
		default:
			if !mcp.IsName(p.Tool) || p.Kind != "read" || p.Preview != "" || p.Classification != "read" || p.ServerID == "" || p.Target != p.Tool {
				return ErrApprovalPresentation
			}
			name, err := mcp.Name(p.ServerID, strings.SplitN(p.Tool, "__", 3)[2])
			if err != nil || name != p.Tool {
				return ErrApprovalPresentation
			}
		}
	}
	data, err := json.Marshal(p)
	if err != nil || len(data) > MaxApprovalPresentationBytes {
		return ErrApprovalPresentation
	}
	return nil
}
func (m *Manager) waitApproval(ctx context.Context, session ID, callID string, p ApprovalPresentation, publish func()) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return false, ErrApprovalPresentation
	}
	p.ID, p.SessionID = ApprovalID("approval-"+hex.EncodeToString(nonce)), session
	if err := validatePresentation(p); err != nil {
		return false, err
	}
	if callID == "" {
		return false, ErrApprovalPresentation
	}
	pending := &pendingApproval{presentation: p, callID: callID, ctx: ctx, decision: make(chan ApprovalDecision, 1)}
	m.mu.Lock()
	s, exists := m.sessions[session]
	if !exists || m.closed || s.abortRequested || terminal(s.snapshot.Status) || s.pending != nil {
		m.mu.Unlock()
		return false, ErrApprovalNotPending
	}
	if len(s.approvalIDs) >= MaxApprovalsPerSession {
		m.mu.Unlock()
		return false, ErrApprovalLimit
	}
	if s.approvalIDs == nil {
		s.approvalIDs = map[ApprovalID]bool{}
	}
	s.approvalIDs[p.ID] = false
	s.pending = pending
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if s.pending == pending {
			s.pending = nil
		}
		m.mu.Unlock()
	}()
	// Publish the existing approval_requested only AFTER the presentation exists.
	// A browser reacting to it can immediately GET the deliberate review endpoint.
	publish()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case decision := <-pending.decision:
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return decision == ApprovalAllow, nil
	}
}
func (m *Manager) PendingApproval(session ID) (*ApprovalPresentation, error) {
	if m == nil || ValidateID(session) != nil {
		return nil, &Error{Kind: Invalid}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, exists := m.sessions[session]
	if !exists {
		return nil, &Error{Kind: NotFound}
	}
	if s.pending == nil {
		return nil, nil
	}
	if s.abortRequested || m.closed || s.pending.ctx.Err() != nil || terminal(s.snapshot.Status) {
		s.pending = nil
		return nil, nil
	}
	p := s.pending.presentation
	return &p, nil
}
func (m *Manager) ResolveApproval(session ID, approval ApprovalID, decision ApprovalDecision) error {
	if decision != ApprovalAllow && decision != ApprovalDeny {
		return ErrApprovalDecision
	}
	if m == nil || ValidateID(session) != nil || providers.ValidateID(providers.ID(approval)) != nil {
		return &Error{Kind: Invalid}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, exists := m.sessions[session]
	if !exists {
		return &Error{Kind: NotFound}
	}
	resolved, known := s.approvalIDs[approval]
	if !known {
		for _, other := range m.sessions {
			if _, found := other.approvalIDs[approval]; found {
				return ErrApprovalMismatch
			}
		}
		return ErrApprovalNotFound
	}
	if resolved {
		return ErrApprovalResolved
	}
	p := s.pending
	if p == nil || p.presentation.ID != approval || s.abortRequested || m.closed || p.ctx.Err() != nil || s.snapshot.Status != WaitingApproval {
		if p != nil && p.presentation.ID == approval {
			s.pending = nil
		}
		return ErrApprovalNotPending
	}
	s.approvalIDs[approval] = true
	s.pending = nil
	p.decision <- decision // buffered; exactly one sender under the state lock.
	return nil
}

// ActiveSession provides only safe metadata for reload/reconnection. Idle/delete
// admission reservations and finalized Sessions are not exposed as active runs.
func (m *Manager) ActiveSession(thread threads.ID) (*Snapshot, error) {
	if m == nil || providers.ValidateID(providers.ID(thread)) != nil {
		return nil, &Error{Kind: Invalid}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[m.active[string(thread)]]
	if s == nil || terminal(s.snapshot.Status) {
		return nil, nil
	}
	copy := s.snapshot
	copy.Computer = cloneComputerMetadata(copy.Computer)
	return &copy, nil
}

var sandboxComputerID = regexp.MustCompile(`^sandbox-[a-f0-9]{24}$`)

func validComputerNamespace(namespace, id string) bool {
	if strings.HasPrefix(id, "sandbox-") {
		return namespace == "sandbox" && sandboxComputerID.MatchString(id)
	}
	return namespace == id
}
