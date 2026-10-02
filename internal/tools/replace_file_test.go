package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/editcontract"
)

type editReviewFunc func(context.Context, editcontract.Review) (editcontract.Decision, error)

func (f editReviewFunc) Review(ctx context.Context, r editcontract.Review) (editcontract.Decision, error) {
	return f(ctx, r)
}
func TestReplacementArgumentsStrict(t *testing.T) {
	for _, raw := range []string{`[]`, `null`, `{}`, `{"path":"p"}`, `{"path":"p","content":null}`, `{"path":null,"content":"x"}`, `{"path":"p","content":1}`, `{"path":"p","path":"q","content":"x"}`, `{"path":"p","content":"x","content":"y"}`, `{"path":"p","content":"x","extra":true}`, `{"path":"p","content":"x"} {}`, "{\"path\":\"p\",\"content\":\"\xff\"}"} {
		if _, _, err := replacementArguments([]byte(raw)); !errors.Is(err, ErrInvalidArguments) {
			t.Fatal(raw, err)
		}
	}
	p, b, err := replacementArguments([]byte(`{"path":"file.txt","content":"a\r\n\\r"}`))
	if err != nil || p != "file.txt" || string(b) != "a\r\n\\r" {
		t.Fatal(p, b, err)
	}
}
func TestReplaceFileRequiresBoundApproval(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	target, err := NewReplaceFile(dir, editcontract.Limits{InputBytes: 100, FinalBytes: 100, Lines: 10, PathBytes: 100, PreviewBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	raw := json.RawMessage(`{"path":"file.txt","content":"new"}`)
	if _, err := target.Execute(ctx, raw); !errors.Is(err, editcontract.ErrDenied) {
		t.Fatal(err)
	}
	if !editcontract.Supported() {
		if err := target.Prepare(ctx, raw); !errors.Is(err, editcontract.ErrUnsupported) {
			t.Fatal(err)
		}
		return
	}
	if err := target.Prepare(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if err := target.Approve(ctx, editReviewFunc(func(_ context.Context, r editcontract.Review) (editcontract.Decision, error) {
		if !strings.Contains(r.Display, `"new"`) {
			t.Fatal("incomplete review")
		}
		return editcontract.Allow, nil
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Execute(ctx, []byte(`{"path":"other","content":"new"}`)); !errors.Is(err, editcontract.ErrChanged) {
		t.Fatal(err)
	}
	if _, err := target.Execute(ctx, raw); !errors.Is(err, editcontract.ErrDenied) {
		t.Fatal(err)
	}
	if err := target.Prepare(ctx, raw); !errors.Is(err, editcontract.ErrUsed) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	if err != nil || string(data) != "old" {
		t.Fatal(data, err)
	}
}
