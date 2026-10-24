package harness2isolation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/netty-linux/daimon/internal/harness2substrate"
)

func TestInspectionNeverAdmitsExecution(t *testing.T) {
	if _, err := Inspect(nil); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Inspect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s, err := Inspect(context.Background())
	if runtime.GOOS != "linux" {
		if !errors.Is(err, ErrUnsupported) {
			t.Fatal("unsupported platform accepted", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if len(s.JSON()) > 2048 || !json.Valid(s.JSON()) {
		t.Fatal("invalid or unbounded report")
	}
	var report struct {
		Version int
		Status  string `json:"fixture_status"`
		Reason  string
	}
	if json.Unmarshal(s.JSON(), &report) != nil || report.Version != 1 || report.Status != "NOT_RUN" || report.Reason != "validated-supervisor-unavailable" {
		t.Fatal("observation became admission")
	}
	copy := s.Observations()
	copy.RuntimeFileObserved = !copy.RuntimeFileObserved
	if copy == s.Observations() {
		t.Fatal("observations aliased")
	}
	b := s.JSON()
	b[0] = '!'
	if !json.Valid(s.JSON()) || fmt.Sprintf("%#v", s) != s.String() {
		t.Fatal("snapshot representation escaped")
	}
	if !errors.Is(harness2substrate.RequireRealExecution(context.Background()), harness2substrate.ErrUnavailable) {
		t.Fatal("C1 gate unlocked")
	}
	gates := harness2substrate.SecurityGates()
	if len(gates) != 18 {
		t.Fatal("gate inventory changed")
	}
	for _, gate := range gates {
		if gate.State != "unverified" {
			t.Fatal("metadata attested a gate")
		}
	}
}
