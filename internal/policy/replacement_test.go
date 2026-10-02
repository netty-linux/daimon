package policy

import (
	"context"
	"testing"

	"github.com/netty-linux/daimon/internal/agentloop"
)

func TestReplacementCannotUseDirectAllowOrGenericPrompt(t *testing.T) {
	for _, decision := range []Decision{Allow, RequireApproval, Decision(99)} {
		approval := &scriptedApprovals{granted: true}
		a := &Authorizer{Policy: StaticPolicy{Rules: map[string]Decision{"replace_file": decision}}, Approvals: approval}
		got, err := a.Authorize(context.Background(), requestFor("replace_file", `{"path":"file.txt","content":"new"}`))
		if err == nil || got == agentloop.ToolDecisionAllow || approval.calls != 0 {
			t.Fatal(got, err, approval.calls)
		}
	}
}
