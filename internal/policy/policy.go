// Package policy composes a static tool policy with optional human approval
// into the single agentloop.ToolAuthorizer the loop knows. The loop itself
// never sees approval logic, terminal I/O or decision reasons.
package policy

import (
	"context"
	"errors"

	"github.com/netty-linux/daimon/internal/agentloop"
)

// Decision is what the static policy says about a tool name before any human
// is consulted.
type Decision uint8

const (
	// Deny is the zero value so an under-initialized StaticPolicy fails
	// closed instead of silently allowing everything.
	Deny Decision = iota
	Allow
	RequireApproval
)

type ToolPolicy interface {
	Decide(toolName string) Decision
}

// StaticPolicy resolves names from a fixed table with a fallback for
// unlisted tools.
type StaticPolicy struct {
	Rules    map[string]Decision
	Fallback Decision
}

func (p StaticPolicy) Decide(toolName string) Decision {
	if decision, ok := p.Rules[toolName]; ok {
		return decision
	}
	return p.Fallback
}

// DefaultCLIPolicy: echo runs automatically, the read-only workspace tools
// need one-shot human approval, everything else is denied. There is no
// global "approve everything" option.
func DefaultCLIPolicy() StaticPolicy {
	return StaticPolicy{
		Rules: map[string]Decision{
			"echo":      Allow,
			"list_dir":  RequireApproval,
			"read_file": RequireApproval,
		},
		Fallback: Deny,
	}
}

// ApprovalProvider asks the human operator about one pending call. A true
// result authorizes exactly that call; false denies it. Errors fail closed.
// Implementations must not persist decisions or offer "always allow".
type ApprovalProvider interface {
	Approve(ctx context.Context, request agentloop.ToolAuthorizationRequest) (bool, error)
}

// Authorizer implements agentloop.ToolAuthorizer by first consulting the
// policy and then, when required, the approval provider. Approval events are
// recorded before the loop emits any ToolAllowed/ToolDenied event, so an
// approved call yields: approval_requested, approval_granted, tool_allowed.
type Authorizer struct {
	Policy    ToolPolicy
	Approvals ApprovalProvider
	Sink      agentloop.EventSink
}

var (
	errUnknownPolicyDecision = errors.New("unknown policy decision")
	errMissingPolicy         = errors.New("missing tool policy")
	errMissingApprovals      = errors.New("missing approval provider")
)

func (a *Authorizer) Authorize(ctx context.Context, request agentloop.ToolAuthorizationRequest) (agentloop.ToolDecision, error) {
	if a == nil || a.Policy == nil {
		return "", errMissingPolicy
	}
	switch a.Policy.Decide(request.Call.Name) {
	case Allow:
		return agentloop.ToolDecisionAllow, nil
	case Deny:
		return agentloop.ToolDecisionDeny, nil
	case RequireApproval:
	default:
		// An invalid policy must fail closed, not fall through to approval.
		return "", errUnknownPolicyDecision
	}
	if a.Approvals == nil {
		return "", errMissingApprovals
	}
	a.record(ctx, agentloop.ApprovalRequested, request)
	granted, err := a.Approvals.Approve(ctx, request)
	if err != nil {
		// Context errors return directly; anything else becomes an
		// AuthorizationError naming only step and tool.
		return "", err
	}
	if granted {
		a.record(ctx, agentloop.ApprovalGranted, request)
		return agentloop.ToolDecisionAllow, nil
	}
	a.record(ctx, agentloop.ApprovalDenied, request)
	return agentloop.ToolDecisionDeny, nil
}

func (a *Authorizer) record(ctx context.Context, kind agentloop.EventKind, request agentloop.ToolAuthorizationRequest) {
	if a.Sink == nil {
		return
	}
	a.Sink.Record(ctx, agentloop.Event{Kind: kind, Step: request.Step, ToolIndex: request.ToolIndex})
}
