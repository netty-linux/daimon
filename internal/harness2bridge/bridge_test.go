package harness2bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/harness2"
)

func TestProtocolRejects(t *testing.T) {
	r := response{Version: 1, ID: 1, Operation: "health", OK: true, Code: "ok", Result: Result{Revision: Revision, Operations: []string{"health", "capabilities", "validate_manifest"}}}
	good, _ := json.Marshal(r)
	if _, err := decodeResponse(good, "health"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(string(good), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(good), `"version":1`, `"Version":1`, 1),
		strings.Replace(string(good), `"version":1,`, ``, 1),
		strings.Replace(string(good), `"execution":false`, `"execution":true`, 1),
		strings.Replace(string(good), `"tree_files":0`, `"tree_files":null`, 1),
		strings.Replace(string(good), Revision, strings.Repeat("a", 40), 1),
		string(good) + `{}`, `null`, strings.Repeat("[", 100),
	} {
		if _, err := decodeResponse([]byte(bad), "health"); !errors.Is(err, ErrProtocol) {
			t.Fatal("accepted invalid response")
		}
	}
	if _, err := decodeResponse(make([]byte, MaxFrameBytes+1), "health"); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}

func TestOutputLimit(t *testing.T) {
	b := &limitedOutput{limit: 4}
	_, _ = b.Write([]byte("1234"))
	if _, err := b.Write([]byte("5")); !errors.Is(err, ErrLimit) || b.Len() != 4 {
		t.Fatal("unbounded output")
	}
}

func integrationConfig(t *testing.T) Config {
	t.Helper()
	python := os.Getenv("DAIMON_HARNESS2_TEST_PYTHON")
	if python == "" {
		t.Skip("optional real-worker integration: set DAIMON_HARNESS2_TEST_PYTHON and DAIMON_HARNESS2_TEST_SITE")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "python", "harness2_bridge"))
	if err != nil {
		t.Fatal(err)
	}
	return Config{Python: python, Worker: filepath.Join(root, "worker.py"), SitePackages: os.Getenv("DAIMON_HARNESS2_TEST_SITE"), ScratchRoot: t.TempDir(), Timeout: 10 * time.Second}
}

func TestRealWorkerEndToEnd(t *testing.T) {
	cfg := integrationConfig(t)
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, f := range []func(context.Context) (Result, error){c.Health, c.Capabilities} {
		r, err := f(context.Background())
		if err != nil || r.Revision != Revision {
			t.Fatalf("health/capabilities: %v", err)
		}
	}
	b := bots.Bot{ID: "bot", Name: "Bot", Instructions: "Respond with care.", ProviderID: "openai", Model: "offline", Tools: []string{}, PermissionMode: bots.PermissionAsk}
	m, err := harness2.Baseline(b)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(m)
	r, err := c.ValidateManifest(context.Background(), data)
	if err != nil || !r.ManifestValid || r.TreeFiles != 1 {
		t.Fatalf("validate: %v", err)
	}
	m.Components.SystemPrompt = "Run python scripts/missing.py"
	data, _ = json.Marshal(m)
	if _, err = c.ValidateManifest(context.Background(), data); !errors.Is(err, ErrWorker) {
		t.Fatal("real upstream rejection missing", err)
	}
	m.Components.Scripts = []harness2.Artifact{{ID: "script", SHA256: strings.Repeat("a", 64)}}
	data, _ = json.Marshal(m)
	if _, err = c.ValidateManifest(context.Background(), data); !errors.Is(err, harness2.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err = c.ValidateManifest(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("native validation bypassed")
	}
	c.Close()
	if _, err = c.Health(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cfg.ScratchRoot)
	if len(entries) != 0 {
		t.Fatal("scratch leak")
	}
}

func TestRealWorkerLifecycle(t *testing.T) {
	cfg := integrationConfig(t)
	fixture := filepath.Join(t.TempDir(), "worker.py")
	cfg.Worker = fixture
	for _, tc := range []struct {
		name, code string
		timeout    bool
	}{
		{"timeout", "import time; time.sleep(60)", true},
		{"crash", "raise SystemExit(3)", false},
		{"invalid", "print('{}')", false},
		{"flood", "import sys; sys.stdout.write('x'*200000)", false},
		{"environment", "import os; assert not os.environ.get('DAIMON_TEST_SECRET'); print('{}')", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(fixture, []byte(tc.code), 0600); err != nil {
				t.Fatal(err)
			}
			cfg.Timeout = 250 * time.Millisecond
			c, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Health(context.Background())
			c.Close()
			if err == nil {
				t.Fatal("expected failure")
			}
			if tc.timeout && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(cfg.ScratchRoot)
			if len(entries) != 0 {
				t.Fatal("scratch leak")
			}
		})
	}
	if err := os.WriteFile(fixture, []byte("import time; time.sleep(60)"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Timeout = 10 * time.Second
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.Health(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, _ = c.Health(context.Background()) }()
	}
	close(start)
	c.Close()
	wg.Wait()
	entries, _ := os.ReadDir(cfg.ScratchRoot)
	if len(entries) != 0 {
		t.Fatal("scratch leak")
	}
}

func TestRealWorkerEnvironment(t *testing.T) {
	cfg := integrationConfig(t)
	t.Setenv("DAIMON_TEST_SECRET", "must-not-cross")
	r := response{Version: 1, ID: 1, Operation: "health", OK: true, Code: "ok", Result: Result{Revision: Revision, Operations: []string{"health", "capabilities", "validate_manifest"}}}
	data, _ := json.Marshal(r)
	code := "import os; assert 'DAIMON_TEST_SECRET' not in os.environ; print(" + strconv.Quote(string(data)) + ")"
	cfg.Worker = filepath.Join(t.TempDir(), "worker.py")
	if err := os.WriteFile(cfg.Worker, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.Health(context.Background()); err != nil {
		t.Fatal("environment inherited", err)
	}
}

func TestConfigFailsClosed(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
}

// A containment failure must release the stdin copier without disclosing data.
func TestContainmentGateWithholdsRequest(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		ready := make(chan struct{})
		input := &gatedInput{ready: ready, reader: bytes.NewReader([]byte("private manifest")), allowed: allowed}
		close(ready)
		data, err := io.ReadAll(input)
		if err != nil {
			t.Fatal(err)
		}
		if !allowed && len(data) != 0 {
			t.Fatal("request leaked before containment")
		}
		if allowed && string(data) != "private manifest" {
			t.Fatal("authorized request lost")
		}
	}
}

func TestActiveWorkerCancellationAndClose(t *testing.T) {
	for _, method := range []string{"cancel", "close"} {
		t.Run(method, func(t *testing.T) {
			cfg := integrationConfig(t)
			marker := filepath.Join(cfg.ScratchRoot, "ready")
			cfg.Worker = filepath.Join(t.TempDir(), "worker.py")
			// Read stdin first: readiness proves containment and request delivery happened.
			code := "import sys,time; from pathlib import Path; sys.stdin.buffer.read(); Path(" + strconv.Quote(marker) + ").write_text('ready'); time.sleep(60)"
			if err := os.WriteFile(cfg.Worker, []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := c.Health(ctx); done <- err }()
			timeout := time.NewTimer(10 * time.Second)
			defer timeout.Stop()
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
		waitReady:
			for {
				select {
				case err := <-done:
					t.Fatalf("worker exited before readiness: %v", err)
				case <-timeout.C:
					t.Fatal("worker readiness deadline")
				case <-tick.C:
					if _, err := os.Stat(marker); err == nil {
						break waitReady
					} else if !errors.Is(err, os.ErrNotExist) {
						t.Fatal(err)
					}
				}
			}
			if method == "cancel" {
				cancel()
			} else {
				c.Close()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("active cancellation lost", err)
				}
			case <-timeout.C:
				t.Fatal("worker did not join")
			}
			c.Close()
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(cfg.ScratchRoot)
			if err != nil || len(entries) != 0 {
				t.Fatal("scratch cleanup failed", err)
			}
		})
	}
}
