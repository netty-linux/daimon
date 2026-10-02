package tools

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/createcontract"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type creationReview func(context.Context, createcontract.Review) (createcontract.Decision, error)

func (f creationReview) Review(ctx context.Context, r createcontract.Review) (createcontract.Decision, error) {
	return f(ctx, r)
}
func TestCreateRequiresExactSingleUsePermit(t *testing.T) {
	dir := t.TempDir()
	creator, err := NewCreateFile(dir, createcontract.Limits{FinalBytes: 100, Lines: 10, PathBytes: 100, PreviewBytes: 10000})
	if err != nil {
		t.Fatal(err)
	}
	defer creator.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	raw := []byte(`{"path":"new","content":"approved"}`)
	if _, err := creator.Execute(ctx, raw); !errors.Is(err, createcontract.ErrDenied) {
		t.Fatal("unapproved execution", err)
	}
	err = creator.Prepare(ctx, raw)
	if !createcontract.Supported() {
		if !errors.Is(err, createcontract.ErrUnsupported) {
			t.Fatal(err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.Approve(ctx, creationReview(func(context.Context, createcontract.Review) (createcontract.Decision, error) {
		return createcontract.Allow, nil
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.Execute(ctx, []byte(`{"path":"other","content":"approved"}`)); !errors.Is(err, createcontract.ErrChanged) {
		t.Fatal("permit applied elsewhere", err)
	}
	if _, err := creator.Execute(ctx, raw); !errors.Is(err, createcontract.ErrDenied) {
		t.Fatal("permit reused", err)
	}
	if err := creator.Prepare(ctx, raw); !errors.Is(err, createcontract.ErrUsed) {
		t.Fatal("new attempt accepted", err)
	}
	for _, name := range []string{"new", "other"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal("unexpected file", err)
		}
	}
}
