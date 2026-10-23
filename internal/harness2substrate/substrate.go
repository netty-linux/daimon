// Package harness2substrate prepares private in-memory candidates and fixed
// structural probes. It has no Session, store, provider, tool or execution seam.
package harness2substrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/harness2"
)

const (
	Version          = 1
	PromptFile       = "systemprompt.md" // Logical upstream surface key, never a host path.
	MaxEvidenceBytes = 2048
	MaxProbeDuration = time.Second
)

var (
	ErrConfig      = errors.New("harness2substrate: invalid configuration")
	ErrFixture     = errors.New("harness2substrate: unknown fixture")
	ErrLimit       = errors.New("harness2substrate: evidence limit exceeded")
	ErrUnavailable = errors.New("harness2substrate: real execution unavailable")
)

// Plan owns the Phase A snapshot; there is no staging directory or Install.
// A source-tree Install must not be fabricated without safe materialisation.
type Plan struct{ snapshot harness2.Snapshot }

func (Plan) String() string                                  { return "harness2substrate: private plan" }
func (p Plan) GoString() string                              { return p.String() }
func (p Plan) SHA256() string                                { return p.snapshot.SHA256() }
func (p Plan) Manifest() (harness2.Manifest, error)          { return p.snapshot.Manifest() }
func (p Plan) Candidate(baseline bots.Bot) (bots.Bot, error) { return p.snapshot.Candidate(baseline) }
func (p Plan) Tree() (map[string]string, error) {
	m, err := p.Manifest()
	if err != nil {
		return nil, err
	}
	return map[string]string{PromptFile: m.Components.SystemPrompt}, nil
}

func Prepare(ctx context.Context, data []byte, baseline bots.Bot) (Plan, error) {
	if err := check(ctx); err != nil {
		return Plan{}, err
	}
	m, err := harness2.Decode(data)
	if err != nil {
		return Plan{}, err
	}
	s, err := harness2.Freeze(m, baseline)
	if err != nil {
		return Plan{}, err
	}
	if _, err = s.Candidate(baseline); err != nil {
		return Plan{}, err
	}
	if err = check(ctx); err != nil {
		return Plan{}, err
	}
	return Plan{snapshot: s}, nil
}

type Fixture string

const (
	Identity        Fixture = "identity-v1"
	PromptRoundTrip Fixture = "prompt-roundtrip-v1"
)

// Record is private structural evidence, never a performance score or trajectory.
// It deliberately contains no prompt, timestamps, model output or filesystem path.
type Record struct {
	Version              int     `json:"version"`
	Fixture              Fixture `json:"fixture"`
	BaselineSHA256       string  `json:"baseline_sha256"`
	CandidateSHA256      string  `json:"candidate_sha256"`
	PromptSHA256         string  `json:"prompt_sha256"`
	PromptBytes          int     `json:"prompt_bytes"`
	Checks               int     `json:"checks"`
	StructuralOnly       bool    `json:"structural_only"`
	PerformanceEvaluated bool    `json:"performance_evaluated"`
	RealExecution        bool    `json:"real_execution"`
}

type Evidence struct{ encoded string }

func (Evidence) String() string     { return "harness2substrate: private structural evidence" }
func (e Evidence) GoString() string { return e.String() }
func (e Evidence) JSON() []byte     { return []byte(e.encoded) }

// Probe runs only the two closed fixtures. Candidate instructions are inert bytes.
// Deadline covers the entire call; cancellation is checked during bounded hashing
// and again after evidence serialization. No partial evidence escapes on failure.
func Probe(parent context.Context, p Plan, baseline bots.Bot, fixture Fixture, outputLimit int) (Evidence, error) {
	if err := check(parent); err != nil {
		return Evidence{}, err
	}
	if outputLimit <= 0 || outputLimit > MaxEvidenceBytes {
		return Evidence{}, ErrConfig
	}
	if fixture != Identity && fixture != PromptRoundTrip {
		return Evidence{}, ErrFixture
	}
	ctx, cancel := context.WithTimeout(parent, MaxProbeDuration)
	defer cancel()
	checkProbe := func() error {
		if err := check(parent); err != nil {
			return err
		}
		return check(ctx)
	}
	bot, err := p.Candidate(baseline)
	if err != nil {
		return Evidence{}, err
	}
	m, err := p.Manifest()
	if err != nil {
		return Evidence{}, err
	}
	checks := 1
	if fixture == PromptRoundTrip {
		tree, err := p.Tree()
		if err != nil {
			return Evidence{}, err
		}
		if len(tree) != 1 || tree[PromptFile] != bot.Instructions {
			return Evidence{}, harness2.ErrConflict
		}
		checks++
	}
	h := sha256.New()
	for i := 0; i < len(bot.Instructions); i++ {
		if err := checkProbe(); err != nil {
			return Evidence{}, err
		}
		_, _ = h.Write([]byte{bot.Instructions[i]})
	}
	r := Record{Version: Version, Fixture: fixture, BaselineSHA256: m.BaseBotSHA256,
		CandidateSHA256: p.SHA256(), PromptSHA256: hex.EncodeToString(h.Sum(nil)),
		PromptBytes: len(bot.Instructions), Checks: checks, StructuralOnly: true}
	encoded, err := json.Marshal(r)
	if err != nil {
		return Evidence{}, ErrConfig
	}
	if len(encoded) > outputLimit {
		return Evidence{}, ErrLimit
	}
	if err = checkProbe(); err != nil {
		return Evidence{}, err
	}
	return Evidence{encoded: string(encoded)}, nil
}

func check(ctx context.Context) error {
	if ctx == nil {
		return ErrConfig
	}
	return ctx.Err()
}
