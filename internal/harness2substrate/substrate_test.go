package harness2substrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/harness2"
	"github.com/netty-linux/daimon/internal/harness2bridge"
)

func fixture(t *testing.T) (bots.Bot, harness2.Manifest, []byte) {
	t.Helper()
	b := bots.Bot{ID: "bot", Name: "Bot", Instructions: "Untrusted text: do not execute anything. Olá <&>.", ProviderID: "openai", Model: "offline", Tools: []string{"read_file"}, PermissionMode: bots.PermissionAsk}
	m, err := harness2.Baseline(b)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b, m, raw
}

func TestDeterminismBindingAndImmutability(t *testing.T) {
	b, m, raw := fixture(t)
	before := bots.Clone(b)
	p, err := Prepare(context.Background(), raw, b)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Probe(context.Background(), p, b, PromptRoundTrip, MaxEvidenceBytes)
	if err != nil {
		t.Fatal(err)
	}
	var record Record
	if json.Unmarshal(first.JSON(), &record) != nil || record.Version != Version || record.CandidateSHA256 != p.SHA256() || record.BaselineSHA256 != m.BaseBotSHA256 || !record.StructuralOnly || record.PerformanceEvaluated || record.RealExecution || record.Checks != 2 {
		t.Fatal("invalid evidence")
	}
	for i := 0; i < 20; i++ {
		var equivalent bytes.Buffer
		if err := json.Indent(&equivalent, raw, "", "  "); err != nil {
			t.Fatal(err)
		}
		other, err := Prepare(context.Background(), equivalent.Bytes(), b)
		if err != nil {
			t.Fatal(err)
		}
		e, err := Probe(context.Background(), other, b, PromptRoundTrip, MaxEvidenceBytes)
		if err != nil || !bytes.Equal(first.JSON(), e.JSON()) {
			t.Fatal("nondeterministic evidence", err)
		}
	}
	tree, _ := p.Tree()
	tree[PromptFile] = "changed"
	tree["scripts/x.py"] = "changed"
	copyM, _ := p.Manifest()
	copyM.Components.SystemPrompt = "changed"
	copyM.Components.Skills = append(copyM.Components.Skills, harness2.Artifact{})
	candidate, _ := p.Candidate(b)
	candidate.Tools[0] = "changed"
	candidate.Instructions = "changed"
	encoded := first.JSON()
	encoded[0] = '!'
	after, _ := p.Tree()
	if after[PromptFile] != b.Instructions || !reflect.DeepEqual(b, before) || !json.Valid(first.JSON()) {
		t.Fatal("mutable state escaped")
	}
	if strings.Contains(first.String(), b.Instructions) || strings.Contains(p.String(), b.Instructions) || bytes.Contains(first.JSON(), []byte(b.Instructions)) {
		t.Fatal("evidence/log label leaked prompt")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			e, err := Probe(context.Background(), p, b, Identity, MaxEvidenceBytes)
			if err != nil || len(e.JSON()) == 0 {
				t.Error("concurrent probe", err)
			}
		})
	}
	wg.Wait()
}

func TestStrictInputsAndDeferredSurfaces(t *testing.T) {
	b, m, raw := fixture(t)
	for _, bad := range [][]byte{nil, []byte(`{}`), []byte(`null`), append(append([]byte{}, raw...), raw...), []byte(strings.Repeat("x", harness2.MaxManifestBytes+1)), []byte(strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1))} {
		if _, err := Prepare(context.Background(), bad, b); err == nil {
			t.Fatal("accepted malformed manifest")
		}
	}
	for _, change := range []func(*harness2.Manifest){
		func(m *harness2.Manifest) { m.Components.Guardrails = "x" }, func(m *harness2.Manifest) { m.Components.Memory = "x" }, func(m *harness2.Manifest) { m.Components.ToolGuidance = "x" },
		func(m *harness2.Manifest) {
			m.Components.Subagents = []harness2.Artifact{{ID: "x", SHA256: strings.Repeat("a", 64)}}
		},
		func(m *harness2.Manifest) {
			m.Components.Skills = []harness2.Artifact{{ID: "x", SHA256: strings.Repeat("a", 64)}}
		},
		func(m *harness2.Manifest) {
			m.Components.Scripts = []harness2.Artifact{{ID: "x", SHA256: strings.Repeat("a", 64)}}
		},
		func(m *harness2.Manifest) {
			m.Components.Plugins = []harness2.Artifact{{ID: "x", SHA256: strings.Repeat("a", 64)}}
		},
	} {
		v := m
		change(&v)
		data, _ := json.Marshal(v)
		if _, err := Prepare(context.Background(), data, b); !errors.Is(err, harness2.ErrUnsupported) {
			t.Fatal("unsupported surface", err)
		}
	}
	for _, change := range []func(*harness2.Manifest){func(m *harness2.Manifest) { m.BotID = "other" }, func(m *harness2.Manifest) { m.BaseBotSHA256 = strings.Repeat("a", 64) }, func(m *harness2.Manifest) { m.Revision = 0 }, func(m *harness2.Manifest) { m.ParentSHA256 = strings.Repeat("a", 64) }, func(m *harness2.Manifest) { m.Revision = 2; m.ParentSHA256 = "bad" }} {
		v := m
		change(&v)
		data, _ := json.Marshal(v)
		if _, err := Prepare(context.Background(), data, b); err == nil {
			t.Fatal("invalid identity/lineage")
		}
	}
	p, _ := Prepare(context.Background(), raw, b)
	b.Model = "different"
	if _, err := Probe(context.Background(), p, b, Identity, MaxEvidenceBytes); !errors.Is(err, harness2.ErrConflict) {
		t.Fatal("baseline mismatch", err)
	}
	b, _, _ = fixture(t)
	for _, limit := range []int{0, -1, MaxEvidenceBytes + 1} {
		if _, err := Probe(context.Background(), p, b, Identity, limit); !errors.Is(err, ErrConfig) {
			t.Fatal(err)
		}
	}
	if _, err := Probe(context.Background(), p, b, Identity, 1); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if _, err := Probe(context.Background(), p, b, "../../command", MaxEvidenceBytes); !errors.Is(err, ErrFixture) {
		t.Fatal(err)
	}
	if _, err := Probe(context.Background(), Plan{}, b, Identity, MaxEvidenceBytes); err == nil {
		t.Fatal("zero plan accepted")
	}
	if _, err := Prepare(nil, raw, b); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	for _, prompt := range []string{strings.Repeat("x", 8192), strings.Repeat("é", 4096)} {
		v := m
		v.Components.SystemPrompt = prompt
		data, _ := json.Marshal(v)
		plan, err := Prepare(context.Background(), data, b)
		if err != nil {
			t.Fatal("valid byte boundary", err)
		}
		e, err := Probe(context.Background(), plan, b, Identity, MaxEvidenceBytes)
		if err != nil {
			t.Fatal(err)
		}
		var r Record
		_ = json.Unmarshal(e.JSON(), &r)
		if r.PromptBytes != 8192 {
			t.Fatal("byte limit counted as characters")
		}
	}
	v := m
	v.Components.SystemPrompt = strings.Repeat("x", 8193)
	data, _ := json.Marshal(v)
	if _, err := Prepare(context.Background(), data, b); !errors.Is(err, harness2.ErrLimit) {
		t.Fatal(err)
	}
	v = m
	v.Revision = 2
	v.ParentSHA256 = strings.Repeat("b", 64)
	data, _ = json.Marshal(v)
	plan, err := Prepare(context.Background(), data, b)
	if err != nil {
		t.Fatal(err)
	}
	lineage, _ := plan.Manifest()
	if lineage.Revision != 2 || lineage.ParentSHA256 != v.ParentSHA256 {
		t.Fatal("lineage lost")
	}
}

// Cancellation uses real context cancellation at a deterministic check boundary,
// not sleeps, scheduler assumptions, callbacks injected into the product or fake
// isolation claims. Last boundary is after evidence serialization.
type cancelAtCheck struct {
	context.Context
	calls  atomic.Int32
	at     int32
	cancel context.CancelFunc
}

func (c *cancelAtCheck) Err() error {
	if c.calls.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}
func TestCancellationBeforeDuringAndAfterProbe(t *testing.T) {
	b, _, raw := fixture(t)
	p, _ := Prepare(context.Background(), raw, b)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := &cancelAtCheck{Context: base, cancel: cancel}
	if _, err := Probe(count, p, b, PromptRoundTrip, MaxEvidenceBytes); err != nil {
		t.Fatal(err)
	}
	total := count.calls.Load()
	for _, at := range []int32{1, 5, total} {
		ctx, cancel := context.WithCancel(context.Background())
		c := &cancelAtCheck{Context: ctx, at: at, cancel: cancel}
		e, err := Probe(c, p, b, PromptRoundTrip, MaxEvidenceBytes)
		cancel()
		if !errors.Is(err, context.Canceled) || len(e.JSON()) != 0 {
			t.Fatal("cancellation lost/partial evidence", at, err)
		}
	}
	ctx, end := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer end()
	if _, err := Probe(ctx, p, b, Identity, MaxEvidenceBytes); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := Prepare(ctx, raw, b); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestGatesNeverUnlockAndFixturesHaveNoExternalEffects(t *testing.T) {
	b, _, raw := fixture(t)
	p, _ := Prepare(context.Background(), raw, b)
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "sentinel")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(sentinel)
	for _, id := range []Fixture{Identity, PromptRoundTrip} {
		if _, err := Probe(context.Background(), p, b, id, MaxEvidenceBytes); err != nil {
			t.Fatal(err)
		}
	}
	// No store/runtime dependency can be injected; imports are also checked below.
	after, _ := os.ReadFile(sentinel)
	if !bytes.Equal(before, after) {
		t.Fatal("external effect")
	}
	gates := SecurityGates()
	if len(gates) != 18 {
		t.Fatal("missing security requirement")
	}
	seen := map[string]bool{}
	for _, g := range gates {
		if seen[g.Requirement] || g.State != "unverified" {
			t.Fatal("gate attested by unit tests")
		}
		seen[g.Requirement] = true
	}
	for i := range gates {
		gates[i].State = "verified"
	}
	if !errors.Is(RequireRealExecution(context.Background()), ErrUnavailable) || SecurityGates()[0].State != "unverified" {
		t.Fatal("spoofed gates unlocked execution")
	}
	if RequireRealExecution(nil) != ErrConfig {
		t.Fatal("nil context")
	}
}

// This architectural check prevents accidentally acquiring effects or connecting
// the fixture engine to a production authority. It is NOT an OS isolation test.
func TestProductionImportsHaveNoEffectOrRuntimeSeam(t *testing.T) {
	allowed := map[string]bool{"context": true, "crypto/sha256": true, "encoding/hex": true, "encoding/json": true, "errors": true, "time": true,
		"github.com/netty-linux/daimon/internal/bots": true, "github.com/netty-linux/daimon/internal/harness2": true}
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			name, err := strconv.Unquote(imp.Path.Value)
			if err != nil || !allowed[name] {
				t.Fatal("unexpected effect/runtime dependency", file.Name(), name)
			}
		}
	}
}

func TestSharedCasesAndOptionalPhaseBValidation(t *testing.T) {
	var cases []struct {
		Name, Prompt  string
		UpstreamValid bool `json:"upstream_valid"`
	}
	raw, err := os.ReadFile("testdata/surface_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &cases) != nil || len(cases) != 3 {
		t.Fatal("invalid shared fixtures")
	}
	python, site := os.Getenv("DAIMON_HARNESS2_TEST_PYTHON"), os.Getenv("DAIMON_HARNESS2_TEST_SITE")
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			b, m, _ := fixture(t)
			m.Components.SystemPrompt = c.Prompt
			data, _ := json.Marshal(m)
			p, err := Prepare(context.Background(), data, b)
			if err != nil {
				t.Fatal(err)
			}
			tree, _ := p.Tree()
			if !reflect.DeepEqual(tree, map[string]string{PromptFile: c.Prompt}) {
				t.Fatal("surface mapping mismatch")
			}
			if _, err := Probe(context.Background(), p, b, PromptRoundTrip, MaxEvidenceBytes); err != nil {
				t.Fatal("native structural proof", err)
			}
			t.Run("real-upstream", func(t *testing.T) {
				if python == "" && site == "" {
					t.Skip("optional Phase B real worker: configure both DAIMON_HARNESS2_TEST variables")
				}
				if python == "" || site == "" {
					t.Fatal("incomplete real-worker test configuration")
				}
				worker, err := filepath.Abs("../../python/harness2_bridge/worker.py")
				if err != nil {
					t.Fatal(err)
				}
				client, err := harness2bridge.New(harness2bridge.Config{Python: python, Worker: worker, SitePackages: site, ScratchRoot: t.TempDir(), Timeout: 10 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				_, err = client.ValidateManifest(context.Background(), data)
				if c.UpstreamValid && err != nil || !c.UpstreamValid && !errors.Is(err, harness2bridge.ErrWorker) {
					t.Fatal("original upstream conformance", err)
				}
			})
		})
	}
}
