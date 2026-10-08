//go:build linux

package createcontract

import (
	"context"
	"testing"
)

type bindingReviewer struct{}

func (bindingReviewer) Review(context.Context, Review) (Decision, error) { return Allow, nil }
func TestCreationRootRunAndOperationBinding(t *testing.T) {
	root := t.TempDir()
	a, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	l := Limits{FinalBytes: 1024, Lines: 10, PathBytes: 100, PreviewBytes: 10000}
	p, err := a.Prepare(context.Background(), "new", []byte("exact"), l)
	if err != nil {
		t.Fatal(err)
	}
	q, err := b.Prepare(context.Background(), "new", []byte("exact"), l)
	if err != nil {
		t.Fatal(err)
	}
	v, w := p.View(), q.View()
	if v.RootID == "" || v.RootID != w.RootID || v.RunID == "" || v.RunID == w.RunID || v.Operation != "create_file" || v.ID == w.ID {
		t.Fatal("incomplete binding")
	}
	v.Path = "other"
	v.ProposedSHA256 = "wrong"
	v.RunID = w.RunID
	if p.View().Path != "new" || p.View().ProposedSHA256 != w.ProposedSHA256 {
		t.Fatal("detached review changed proposal")
	}
	permit, err := p.Approve(context.Background(), bindingReviewer{})
	if err != nil {
		t.Fatal(err)
	}
	permit.Invalidate()
	if err := permit.Apply(context.Background()); err != ErrUsed {
		t.Fatal("invalidated approval reused")
	}
}
