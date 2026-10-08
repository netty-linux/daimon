// Package sessions assembles persistent configuration into ephemeral executions.
// The core loop remains independent of this package.
package sessions

import (
	"context"
	"time"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/createcontract"
	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/environments"
	"github.com/netty-linux/daimon/internal/mcp"
	"github.com/netty-linux/daimon/internal/memory"
	"github.com/netty-linux/daimon/internal/policy"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sandbox"
	"github.com/netty-linux/daimon/internal/threads"
	"github.com/netty-linux/daimon/internal/tools"
)

type ID string
type Status string

const (
	Created         Status = "created"
	Running         Status = "running"
	WaitingApproval Status = "waiting_approval"
	Completed       Status = "completed"
	Failed          Status = "failed"
	Aborted         Status = "aborted"
)

// ValidTransition defines the closed lifecycle, including startup failures.
func ValidTransition(from, to Status) bool {
	switch from {
	case Created:
		return to == Running || to == Failed || to == Aborted
	case Running:
		return to == WaitingApproval || to == Completed || to == Failed || to == Aborted
	case WaitingApproval:
		return to == Running || to == Failed || to == Aborted
	default:
		return false
	}
}

func ValidateID(id ID) error {
	if providers.ValidateID(providers.ID(id)) != nil {
		return &Error{Kind: Invalid}
	}
	return nil
}

// Binding is private configuration exposed only through the deliberate Binding
// accessor. It contains no credentials, live resources or workspace handles.
type Binding struct {
	EnvironmentID    string
	StartingRevision uint64
	SandboxProfile   *sandbox.Profile
	Computer         *computer.Metadata
	BotID            bots.ID
	ProviderID       providers.ID
	Model            string
	Instructions     string
	Tools            []string
	PermissionMode   bots.PermissionMode
}

func cloneBinding(b Binding) Binding {
	if b.SandboxProfile != nil {
		copy := *b.SandboxProfile
		b.SandboxProfile = &copy
	}
	b.Tools = append([]string{}, b.Tools...)
	b.Computer = cloneComputerMetadata(b.Computer)
	return b
}
func cloneComputerMetadata(v *computer.Metadata) *computer.Metadata {
	if v == nil {
		return nil
	}
	copy := *v
	copy.CapabilityIDs = append([]string{}, v.CapabilityIDs...)
	return &copy
}

// BindingMetadata omits instructions, model text and tool names.
type BindingMetadata struct {
	BotID          bots.ID
	ProviderID     providers.ID
	ToolCount      int
	PermissionMode bots.PermissionMode
}

type EnvironmentMetadata struct {
	Placement sandbox.Placement `json:"placement,omitempty"`
	Backend   sandbox.BackendID `json:"backend,omitempty"`
	Resources string            `json:"resources,omitempty"`
	ExpiresAt string            `json:"expires_at,omitempty"`
	Mode      string            `json:"mode"`
	State     string            `json:"state"`
	SandboxID string            `json:"sandbox_id,omitempty"`
	Runtime   string            `json:"runtime"`
	Cleanup   string            `json:"cleanup"`
}

func cloneEnvironment(e *EnvironmentMetadata) *EnvironmentMetadata {
	if e == nil {
		return nil
	}
	copy := *e
	return &copy
}

type Snapshot struct {
	PersistentWorkspace                    *PersistentMetadata
	Environment                            *EnvironmentMetadata
	Computer                               *computer.Metadata
	ID                                     ID
	ThreadID                               threads.ID
	Status                                 Status
	StartedAt, FinishedAt                  time.Time
	Bound                                  bool
	Binding                                BindingMetadata
	LastEventSequence                      uint64
	StopReason                             agentloop.StopReason // Empty when the loop has not run.
	ErrorCategory                          Kind
	Steps, ToolCalls, TruncatedToolResults int
}

type StartRequest struct {
	SessionID ID
	ThreadID  threads.ID
	Message   string
	MessageID conversations.ID
	// Exposure only: one-shot full preview approval remains mandatory.
	EnableReplaceFile, EnableCreateFile bool
}

type BotReader interface {
	Get(bots.ID) (bots.Bot, error)
}
type ThreadReader interface {
	Get(threads.ID) (threads.Thread, error)
}

// Each callback must honor ctx and return resources dedicated to this session.
// Readers must support simultaneous Get; concurrent store mutations require
// caller ownership. Provider registry is frozen into a private map at construction.
type Dependencies struct {
	Environments *environments.Store
	Bots         BotReader
	Threads      ThreadReader
	Providers    *providers.Registry
	Config       func(context.Context, providers.ID) (providers.Config, error)
	Approvals    func(context.Context, ID) (Approvals, error)
	// Optional for existing non-conversation callers; serve injects this explicitly.
	Conversations ConversationStore
	// WebApprovals explicitly selects the ephemeral human-decision adapter.
	// It supplies no capabilities or policy grants; terminal callers keep false.
	Sandboxes    *sandbox.Manager
	Computers    *computer.Manager
	WebApprovals bool
	Memory       MemoryStore // Application-owned; content is private to context assembly.
	MCP          MCPCatalog  // Application-owned; closed after Sessions, never owned by Binding.
}

type MemoryStore interface {
	List(context.Context) ([]memory.Memory, error)
}

type MCPCatalog interface {
	Lookup(string) (tools.Tool, mcp.Classification, error)
}

type ConversationStore interface {
	List(context.Context, threads.ID) ([]conversations.Message, error)
	Append(context.Context, conversations.Message) error
}

type Approvals struct {
	Reads        policy.ApprovalProvider
	Replacements editcontract.Reviewer
	Creations    createcontract.Reviewer
}

type Options struct {
	Budget                agentloop.Budget
	EventCapacity         int // 1..16384; no implicit default.
	MaxMemoryContextBytes int // Required when Memory is configured; 1..32KiB.
	MaxMemoryRecords      int // Required when Memory is configured; 1..64.
	MaxSessions           int // 1..1024, includes retained terminal sessions.
}
