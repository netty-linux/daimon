package policy

import (
	"context"
	"github.com/netty-linux/daimon/internal/agentloop"
	"testing"
)

func TestCreationRequiresSpecificApproval(t *testing.T) {
	if DefaultCLIPolicy().Decide("create_file") != Deny {
		t.Fatal("creation not denied by default")
	}
	for _, decision := range []Decision{Allow, RequireApproval, Decision(99)} {
		generic := &scriptedApprovals{granted: true}
		a := &Authorizer{Policy: StaticPolicy{Rules: map[string]Decision{"create_file": decision}}, Approvals: generic}
		got, err := a.Authorize(context.Background(), requestFor("create_file", `{"path":"new","content":"x"}`))
		if err == nil || got == agentloop.ToolDecisionAllow || generic.calls != 0 {
			t.Fatal("generic or direct approval bypass", got, err)
		}
	}
}
