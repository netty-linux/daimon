package harness2substrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/harness2"
	"github.com/netty-linux/daimon/internal/sandbox"
)

// Delay a cooperative checkpoint using the virtual clock. This exercises real
// context timers during work, without scheduler timing assumptions or wall sleeps.
type pauseAtCheck struct {
	context.Context
	calls atomic.Int32
}

func (c *pauseAtCheck) Err() error {
	if c.calls.Add(1) == 5 {
		time.Sleep(2 * MaxProbeDuration)
	}
	return c.Context.Err()
}

func TestReviewDeadlineExpiresDuringProbe(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "internal", true: "shorter-parent"}[external], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b, _, raw := fixture(t)
				p, err := Prepare(context.Background(), raw, b)
				if err != nil {
					t.Fatal(err)
				}
				parent := context.Background()
				if external {
					var cancel context.CancelFunc
					parent, cancel = context.WithTimeout(parent, MaxProbeDuration/10)
					defer cancel()
				}
				e, err := Probe(&pauseAtCheck{Context: parent}, p, b, PromptRoundTrip, MaxEvidenceBytes)
				if !errors.Is(err, context.DeadlineExceeded) || len(e.JSON()) != 0 {
					t.Fatal("expired probe published evidence", err)
				}
				if external && !errors.Is(parent.Err(), context.DeadlineExceeded) {
					t.Fatal("parent deadline lost")
				}
				// A failed call leaves no shared cancellation or partial state behind.
				if _, err := Probe(context.Background(), p, b, Identity, MaxEvidenceBytes); err != nil {
					t.Fatal("recovery after deadline", err)
				}
			})
		})
	}
}

func TestReviewExactEvidenceLimitAndProfileOwnership(t *testing.T) {
	b, _, _ := fixture(t)
	b.ComputerProfile = &computer.Profile{Enabled: true, Backend: computer.CUALocal, MCPServerID: "local"}
	m, err := harness2.Baseline(b)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	p, err := Prepare(context.Background(), raw, b)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := p.Candidate(b)
	if err != nil {
		t.Fatal(err)
	}
	candidate.ComputerProfile.MCPServerID = "changed"
	if b.ComputerProfile.MCPServerID != "local" {
		t.Fatal("candidate aliases baseline profile")
	}
	e, err := Probe(context.Background(), p, b, Identity, MaxEvidenceBytes)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := Probe(context.Background(), p, b, Identity, len(e.JSON()))
	if err != nil || !bytes.Equal(exact.JSON(), e.JSON()) {
		t.Fatal("exact limit changed evidence", err)
	}
	tooSmall, err := Probe(context.Background(), p, b, Identity, len(e.JSON())-1)
	if !errors.Is(err, ErrLimit) || len(tooSmall.JSON()) != 0 {
		t.Fatal("limit returned partial evidence", err)
	}
	b.ComputerProfile.MCPServerID = "changed"
	if _, err := p.Candidate(b); !errors.Is(err, harness2.ErrConflict) {
		t.Fatal("changed original profile bypassed binding", err)
	}
}

func TestReviewSandboxProfileOwnership(t *testing.T) {
	b, _, _ := fixture(t)
	b.SandboxProfile = &sandbox.Profile{Backend: sandbox.CUALocal, Image: "linux", Runtime: "gvisor", Resources: "small", Network: "outbound"}
	m, err := harness2.Baseline(b)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	p, err := Prepare(context.Background(), raw, b)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := p.Candidate(b)
	if err != nil {
		t.Fatal(err)
	}
	candidate.SandboxProfile.Resources = "standard"
	again, err := p.Candidate(b)
	if err != nil || b.SandboxProfile.Resources != "small" || again.SandboxProfile.Resources != "small" {
		t.Fatal("candidate aliases sandbox profile", err)
	}
}
