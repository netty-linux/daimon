// Package harness2bridge owns an optional fixed validation worker. It is not
// connected to Sessions, serve, providers, tools, Bot stores or optimization.
package harness2bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/harness2"
)

const Revision = "1cfe254e8d25b15f37048c3730134686abe60335"
const MaxFrameBytes = 96 * 1024

var (
	ErrConfig   = errors.New("harness2bridge: invalid configuration")
	ErrClosed   = errors.New("harness2bridge: closed")
	ErrProtocol = errors.New("harness2bridge: invalid protocol")
	ErrWorker   = errors.New("harness2bridge: worker failed")
	ErrLimit    = errors.New("harness2bridge: frame limit exceeded")
	ErrCleanup  = errors.New("harness2bridge: cleanup failed")
)

// Config paths are explicit trusted deployment inputs, never model/browser data.
// Python must be the isolated venv interpreter; Worker is the fixed worker.py.
// SitePackages holds the pinned installed upstream. ScratchRoot is caller-owned.
// The caller trusts interpreter, stdlib, worker, lock and same-UID processes.
// This is a trusted data validator, NOT containment for adversarial Python code.
type Config struct {
	Python, Worker, SitePackages, ScratchRoot string
	Timeout                                   time.Duration
}

type Client struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	slot   chan struct{}
}

func New(cfg Config) (*Client, error) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return nil, ErrConfig
	}
	for _, p := range []string{cfg.Python, cfg.Worker, cfg.SitePackages, cfg.ScratchRoot} {
		if !filepath.IsAbs(p) {
			return nil, ErrConfig
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, ErrConfig
		}
		if (p == cfg.SitePackages || p == cfg.ScratchRoot) != info.IsDir() {
			return nil, ErrConfig
		}
	}
	if filepath.Base(cfg.Worker) != "worker.py" || cfg.Timeout <= 0 || cfg.Timeout > 30*time.Second {
		return nil, ErrConfig
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{cfg: cfg, ctx: ctx, cancel: cancel, slot: make(chan struct{}, 1)}, nil
}

// Close cancels active/queued operations and waits for their process and cleanup.
// It is idempotent. Each call owns exactly one process; no restart or retry.
func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.wg.Wait()
}

type Result struct {
	Revision      string   `json:"revision"`
	Operations    []string `json:"operations"`
	ManifestValid bool     `json:"manifest_valid"`
	TreeFiles     int      `json:"tree_files"`
	Execution     bool     `json:"execution"`
	OSSandbox     bool     `json:"os_sandbox"`
}

type response struct {
	Version   int    `json:"version"`
	ID        int    `json:"id"`
	Operation string `json:"operation"`
	OK        bool   `json:"ok"`
	Code      string `json:"code"`
	Result    Result `json:"result"`
}

func (c *Client) Health(ctx context.Context) (Result, error) {
	return c.call(ctx, "health", map[string]any{})
}
func (c *Client) Capabilities(ctx context.Context) (Result, error) {
	return c.call(ctx, "capabilities", map[string]any{})
}

// ValidateManifest preserves Go's authoritative native validation. The Python
// check is an additional upstream in-memory tree validation, never permission,
// baseline resolution, candidate execution, or proof of improvement.
func (c *Client) ValidateManifest(ctx context.Context, data []byte) (Result, error) {
	m, err := harness2.Decode(data)
	if err != nil {
		return Result{}, err
	}
	x := m.Components
	if x.Guardrails != "" || x.Memory != "" || x.ToolGuidance != "" || len(x.Subagents)+len(x.Skills)+len(x.Scripts)+len(x.Plugins) != 0 {
		return Result{}, harness2.ErrUnsupported
	}
	return c.call(ctx, "validate_manifest", map[string]any{"manifest": m})
}

type gatedInput struct {
	ready   <-chan struct{}
	reader  *bytes.Reader
	allowed bool
}

func (r *gatedInput) Read(p []byte) (int, error) {
	<-r.ready
	if !r.allowed {
		return 0, io.EOF
	}
	return r.reader.Read(p)
}

type limitedOutput struct {
	bytes.Buffer
	limit int
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, ErrLimit
	}
	return b.Buffer.Write(p)
}

func (c *Client) call(parent context.Context, op string, payload any) (result Result, err error) {
	if parent == nil {
		return Result{}, ErrConfig
	}
	data, e := json.Marshal(struct {
		Version   int    `json:"version"`
		ID        int    `json:"id"`
		Operation string `json:"operation"`
		Payload   any    `json:"payload"`
	}{1, 1, op, payload})
	if e != nil || len(data) > MaxFrameBytes {
		return Result{}, ErrLimit
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Result{}, ErrClosed
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()
	ctx, cancel := context.WithTimeout(parent, c.cfg.Timeout)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	select {
	case c.slot <- struct{}{}:
		defer func() { <-c.slot }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	dir, e := os.MkdirTemp(c.cfg.ScratchRoot, "harness2-bridge-")
	if e != nil {
		return Result{}, ErrWorker
	}
	defer func() {
		if os.RemoveAll(dir) != nil {
			err = errors.Join(err, ErrCleanup)
		}
	}()
	cmd := exec.CommandContext(ctx, c.cfg.Python, "-I", "-S", "-B", c.cfg.Worker, c.cfg.SitePackages)
	cmd.Dir = dir
	// Empty environment: no PATH, HOME, provider keys, proxies, .env or PYTHON*.
	cmd.Env = []string{}
	ready := make(chan struct{})
	input := &gatedInput{ready: ready, reader: bytes.NewReader(data)}
	cmd.Stdin = input
	out := &limitedOutput{limit: MaxFrameBytes}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	// Bound waiting on inherited pipes even if a compromised trusted worker forks.
	// This does not provide descendant containment; engine launchers are forbidden.
	cmd.WaitDelay = time.Second
	prepareProcess(cmd)
	cmd.Cancel = func() error { return killProcess(cmd) }
	if cmd.Start() != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, ErrWorker
	}
	release, containmentErr := containProcess(cmd)
	if containmentErr != nil {
		_ = cmd.Process.Kill()
		close(ready)
		_ = cmd.Wait()
		if ctx.Err() != nil {
			if errors.Is(containmentErr, ErrCleanup) {
				return Result{}, errors.Join(ctx.Err(), ErrCleanup)
			}
			return Result{}, ctx.Err()
		}
		return Result{}, containmentErr
	}
	input.allowed = true
	close(ready) // No request bytes enter Python until process ownership is established.
	defer func() {
		if release() != nil {
			err = errors.Join(err, ErrCleanup)
		}
	}()
	runErr := cmd.Wait() // Start, context kill, pipe closure and Wait/join are owned here.
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if errors.Is(runErr, ErrLimit) {
		return Result{}, ErrLimit
	}
	if runErr != nil {
		return Result{}, ErrWorker
	}
	result, err = decodeResponse(out.Bytes(), op)
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	return result, err
}

func decodeResponse(data []byte, op string) (Result, error) {
	if len(data) > MaxFrameBytes {
		return Result{}, ErrLimit
	}
	if !utf8.Valid(data) || !strictJSON(data) {
		return Result{}, ErrProtocol
	}
	var r response
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil {
		return Result{}, ErrProtocol
	}
	// Exact keys and types (including missing/null/bool-as-number) are verified
	// by roundtripping to the fixed wire schema, after duplicate rejection.
	var original, canonical any
	if json.Unmarshal(data, &original) != nil {
		return Result{}, ErrProtocol
	}
	encoded, _ := json.Marshal(r)
	_ = json.Unmarshal(encoded, &canonical)
	if !reflect.DeepEqual(original, canonical) {
		return Result{}, ErrProtocol
	}
	if r.Version != 1 || r.ID != 1 || r.Operation != op || !r.OK || r.Code != "ok" || r.Result.Revision != Revision ||
		!reflect.DeepEqual(r.Result.Operations, []string{"health", "capabilities", "validate_manifest"}) || r.Result.Execution || r.Result.OSSandbox {
		return Result{}, ErrProtocol
	}
	if r.Result.ManifestValid != (op == "validate_manifest") || (op == "validate_manifest" && r.Result.TreeFiles != 1) || (op != "validate_manifest" && r.Result.TreeFiles != 0) {
		return Result{}, ErrProtocol
	}
	return r.Result, nil
}

func strictJSON(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 8 {
			return false
		}
		token, e := d.Token()
		if e != nil || token == nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				t, e := d.Token()
				k, ok := t.(string)
				if e != nil || !ok || seen[k] {
					return false
				}
				seen[k] = true
				if !walk(depth + 1) {
					return false
				}
			}
		case '[':
			for d.More() {
				if !walk(depth + 1) {
					return false
				}
			}
		default:
			return false
		}
		_, e = d.Token()
		return e == nil
	}
	return walk(0) && d.Decode(new(any)) == io.EOF
}
