package main

import (
	"context"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/policy"
)

// Plan uses the same deliberate, informed reading approval as workspace.
// The original request remains intact for authorization and execution.
type planApproval struct{ terminal *policy.TerminalApproval }

func (p *planApproval) Approve(ctx context.Context, request agentloop.ToolAuthorizationRequest) (bool, error) {
	return p.terminal.Approve(ctx, request)
}
