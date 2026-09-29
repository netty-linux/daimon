package agentloop

import (
	"context"
	"errors"

	"github.com/netty-linux/daimon/internal/model"
)

type ToolDecision string

const (
	ToolDecisionAllow ToolDecision = "allow"
	ToolDecisionDeny  ToolDecision = "deny"
)

// ToolIndex is one-based within its model step, matching Event.ToolIndex.
type ToolAuthorizationRequest struct {
	Call      model.ToolCall
	Step      int
	ToolIndex int
}

// Authorize runs before any tool of the batch executes and must be
// deterministic for a given request. A returned error fails closed.
type ToolAuthorizer interface {
	Authorize(ctx context.Context, request ToolAuthorizationRequest) (ToolDecision, error)
}

type AllowAllAuthorizer struct{}

func (AllowAllAuthorizer) Authorize(context.Context, ToolAuthorizationRequest) (ToolDecision, error) {
	return ToolDecisionAllow, nil
}

type DenyAllAuthorizer struct{}

func (DenyAllAuthorizer) Authorize(context.Context, ToolAuthorizationRequest) (ToolDecision, error) {
	return ToolDecisionDeny, nil
}

var errInvalidDecision = errors.New("invalid tool decision")
