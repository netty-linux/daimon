package editcontract

import (
	"context"
	"testing"
)

func TestApprovalRootRunAndOperationBinding(t *testing.T) {
	w, root := fixture(t)
	w2, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	p := prepared(t, w)
	p2 := prepared(t, w2)
	a, b := p.View(), p2.View()
	if a.RootID == "" || a.RootID != b.RootID || a.RunID == "" || a.RunID == b.RunID || a.Operation != "replace_file" || a.ID == b.ID {
		t.Fatal("incomplete root/run/operation binding")
	}
	other, _ := fixture(t)
	c := prepared(t, other).View()
	if a.RootID == c.RootID {
		t.Fatal("root bindings collided")
	}
	// Mutating the detached preview cannot retarget the approval or its bytes.
	a.Path = "other"
	a.ProposedSHA256 = "wrong"
	a.RootID = c.RootID
	a.RunID = c.RunID
	if p.View().Path != p2.View().Path || p.View().ProposedSHA256 != p2.View().ProposedSHA256 {
		t.Fatal("detached review mutated proposal")
	}
	permit, err := p.Approve(context.Background(), allow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := permit.Consume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := permit.Consume(context.Background()); err != ErrUsed {
		t.Fatal("permit reused")
	}
}
