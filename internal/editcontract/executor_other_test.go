//go:build !linux

package editcontract

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestApplyUnsupportedSpendsPermit(t *testing.T) {
	w, _ := fixture(t)
	p := prepared(t, w)
	permit, err := p.Approve(context.Background(), allow)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := permit.Apply(ctx); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if err := permit.Apply(ctx); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
}
