package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Options struct{ InitializeTimeout, CallTimeout, CloseTimeout time.Duration }

func DefaultOptions() Options { return Options{8 * time.Second, 30 * time.Second, 2 * time.Second} }
func (o Options) valid() bool {
	return o.InitializeTimeout > 0 && o.CallTimeout > 0 && o.CloseTimeout > 0 && o.InitializeTimeout <= time.Minute && o.CallTimeout <= 5*time.Minute && o.CloseTimeout <= 10*time.Second
}

type response struct {
	result json.RawMessage
	err    error
}
type outgoing struct {
	data    []byte
	written chan error
}
type Client struct {
	mu         sync.Mutex
	next       uint64
	pending    map[uint64]chan response
	failure    error
	done       chan struct{}
	exited     chan struct{}
	readerDone chan struct{}
	writerDone chan struct{}
	cancel     context.CancelFunc
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	writes     chan outgoing
	options    Options
	release    func()
	tools      []Definition
	protocol   string
}

// Env is supplied by the composition root, never os.Environ or persistent config.
func validateEnv(env []string) error {
	if len(env) > 32 {
		return ErrConfig
	}
	seen := map[string]bool{}
	total := 0
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if !ok || name == "" || seen[upper] || strings.ContainsAny(entry, "\x00\r\n") || len(value) > 8192 || strings.HasPrefix(upper, "DAIMON_") || strings.HasPrefix(upper, "OPENAI_") || strings.HasPrefix(upper, "GROQ_") || strings.HasPrefix(upper, "ANTHROPIC_") || strings.HasPrefix(upper, "OPENROUTER_") || strings.HasPrefix(upper, "CUA_DRIVER_") {
			return ErrConfig
		}
		for _, ch := range name {
			if !(ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9') {
				return ErrConfig
			}
		}
		seen[upper] = true
		total += len(entry)
	}
	if total > 32*1024 {
		return ErrConfig
	}
	return nil
}
func NewClient(ctx context.Context, config ServerConfig, env []string, options Options) (*Client, error) {
	return newClient(ctx, config, env, options, nil)
}

func newClient(ctx context.Context, config ServerConfig, env []string, options Options, phase *string) (*Client, error) {
	if phase != nil {
		*phase = "unknown"
	}
	if ctx == nil || !options.valid() || Validate(Config{Version: 1, Servers: []ServerConfig{config}}) != nil || validateEnv(env) != nil {
		return nil, ErrConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(life, config.Command, append([]string(nil), config.Args...)...)
	cmd.Env = append([]string{}, env...)
	cmd.Stderr = io.Discard // Zero retained/logged stderr bytes; always drained.
	cmd.WaitDelay = 500 * time.Millisecond
	if err := prepareProcess(cmd); err != nil {
		cancel()
		return nil, ErrUnavailable
	}
	cmd.Cancel = func() error { return killProcess(cmd) }
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, ErrUnavailable
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = in.Close()
		return nil, ErrUnavailable
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = in.Close()
		_ = out.Close()
		return nil, ErrUnavailable
	}
	release, err := containProcess(cmd)
	if err != nil {
		cancel()
		_ = in.Close()
		_ = out.Close()
		_ = killProcess(cmd)
		_ = cmd.Wait()
		return nil, ErrUnavailable
	}
	c := &Client{pending: map[uint64]chan response{}, done: make(chan struct{}), exited: make(chan struct{}), readerDone: make(chan struct{}), writerDone: make(chan struct{}), cancel: cancel, cmd: cmd, stdin: in, stdout: out, writes: make(chan outgoing, 32), options: options, release: release}
	go c.readLoop()
	go c.writeLoop()
	go func() { <-c.readerDone; _ = cmd.Wait(); c.fail(ErrUnavailable); release(); close(c.exited) }()
	c.protocol = ProtocolVersion
	if config.ComputerBackend == "cua-local" {
		c.protocol = CUALegacyProtocol
	}
	initCtx, end := context.WithTimeout(ctx, options.InitializeTimeout)
	defer end()
	if phase != nil {
		*phase = "handshake"
	}
	result, err := c.request(initCtx, "initialize", map[string]any{"protocolVersion": c.protocol, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "daimon", "version": "0.10"}})
	if err == nil {
		err = c.initialize(result)
	}
	if err == nil {
		err = c.notify(initCtx, "notifications/initialized", map[string]any{})
	}
	if err == nil {
		if phase != nil {
			*phase = "discovery"
		}
		c.tools, err = c.discover(initCtx, config.ID)
	}
	if err != nil {
		c.fail(ErrUnavailable)
		closeCtx, stop := context.WithTimeout(context.Background(), options.CloseTimeout+time.Second)
		defer stop()
		_ = c.Close(closeCtx)
		return nil, err
	}
	return c, nil
}
func (c *Client) initialize(raw json.RawMessage) error {
	if c.protocol == "" {
		c.protocol = ProtocolVersion
	}
	v, err := object(raw)
	if err != nil {
		return err
	}
	var version string
	if json.Unmarshal(v["protocolVersion"], &version) != nil || version != c.protocol {
		return ErrUnsupported
	}
	caps, err := object(v["capabilities"])
	if err != nil {
		return err
	}
	if _, err = object(caps["tools"]); err != nil {
		return ErrUnsupported
	}
	info, err := object(v["serverInfo"])
	if err != nil {
		return err
	}
	var name, revision string
	if json.Unmarshal(info["name"], &name) != nil || json.Unmarshal(info["version"], &revision) != nil || len(name) == 0 || len(name) > 256 || len(revision) == 0 || len(revision) > 128 {
		return ErrProtocol
	}
	// Server instructions/capability hints never become policy, prompts or context.
	return nil
}
func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.failure != nil {
		c.mu.Unlock()
		return
	}
	c.failure = err
	close(c.done)
	for id, ch := range c.pending {
		ch <- response{err: err}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.cancel()
	c.release()
	_ = c.stdin.Close()
	_ = c.stdout.Close()
}
func (c *Client) Available() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.failure == nil }
func (c *Client) writeLoop() {
	defer close(c.writerDone)
	for {
		select {
		case <-c.done:
			return
		case out := <-c.writes:
			_, err := c.stdin.Write(out.data)
			if err != nil {
				err = ErrUnavailable
				c.fail(err)
			}
			out.written <- err
			if err != nil {
				return
			}
		}
	}
}
func (c *Client) send(ctx context.Context, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return ErrArguments
	}
	if len(raw) > MaxMessageBytes {
		return ErrLimit
	}
	out := outgoing{data: append(raw, '\n'), written: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrUnavailable
	case c.writes <- out:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrUnavailable
	case err := <-out.written:
		return err
	}
}
func (c *Client) notify(ctx context.Context, method string, params any) error {
	return c.send(ctx, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.failure != nil {
		c.mu.Unlock()
		return nil, ErrUnavailable
	}
	if len(c.pending) >= 32 || c.next == ^uint64(0) {
		c.mu.Unlock()
		return nil, ErrLimit
	}
	c.next++
	id := c.next
	ch := make(chan response, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.send(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		if ctx.Err() != nil {
			c.cancelRequest(id, method)
			return nil, ctx.Err()
		}
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.cancelRequest(id, method)
		return nil, ctx.Err()
	case r := <-ch:
		if ctx.Err() != nil {
			c.cancelRequest(id, method)
			return nil, ctx.Err()
		}
		return r.result, r.err
	}
}
func (c *Client) cancelRequest(id uint64, method string) {
	// initialize cannot be canceled by an MCP notification. Retire the connection
	// after cancellation, so an uncooperative external server cannot keep a call alive.
	if method != "initialize" {
		ctx, end := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_ = c.notify(ctx, "notifications/cancelled", map[string]any{"requestId": id})
		end()
	}
	c.fail(ErrUnavailable)
}
func (c *Client) readLoop() {
	defer close(c.readerDone)
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 4096), MaxMessageBytes+2)
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) > MaxMessageBytes {
			c.fail(ErrLimit)
			return
		}
		if strictJSON(raw) != nil {
			c.fail(ErrProtocol)
			return
		}
		if err := c.receive(raw); err != nil {
			c.fail(err)
			return
		}
	}
	c.fail(ErrUnavailable)
}
func (c *Client) receive(raw []byte) error {
	v, err := object(raw)
	if err != nil {
		return err
	}
	var version string
	if json.Unmarshal(v["jsonrpc"], &version) != nil || version != "2.0" {
		return ErrProtocol
	}
	for key := range v {
		switch key {
		case "jsonrpc", "id", "method", "params", "result", "error":
		default:
			return ErrProtocol
		}
	}
	if methodRaw, ok := v["method"]; ok {
		var method string
		if json.Unmarshal(methodRaw, &method) != nil {
			return ErrProtocol
		}
		if _, ok := v["result"]; ok {
			return ErrProtocol
		}
		if _, ok := v["error"]; ok {
			return ErrProtocol
		}
		if id, ok := v["id"]; ok {
			// The only supported server request is protocol ping. No roots/sampling/etc.
			if method != "ping" {
				return ErrUnsupported
			}
			if params, ok := v["params"]; ok {
				if _, err := object(params); err != nil {
					return ErrProtocol
				}
			}
			var requestID any
			decoder := json.NewDecoder(bytes.NewReader(id))
			decoder.UseNumber()
			if decoder.Decode(&requestID) != nil {
				return ErrProtocol
			}
			switch value := requestID.(type) {
			case string:
				if value == "" || len(value) > 128 {
					return ErrProtocol
				}
			case json.Number:
				if _, err := value.Int64(); err != nil {
					return ErrProtocol
				}
			default:
				return ErrProtocol
			}
			data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": requestID, "result": map[string]any{}})
			select {
			case c.writes <- outgoing{append(data, '\n'), make(chan error, 1)}:
				return nil
			case <-c.done:
				return ErrUnavailable
			default:
				return ErrLimit
			}
		}
		// Catalog changes invalidate frozen discovery. Unsupported notifications fail
		// explicitly, rather than silently enabling advertised features.
		return ErrUnsupported
	}
	if _, ok := v["params"]; ok {
		return ErrProtocol
	}
	var id uint64
	if json.Unmarshal(v["id"], &id) != nil || id == 0 {
		return ErrProtocol
	}
	result, hasResult := v["result"]
	rawError, hasError := v["error"]
	if hasResult == hasError {
		return ErrProtocol
	}
	r := response{result: append(json.RawMessage(nil), result...)}
	if hasError {
		fields, e := object(rawError)
		if e != nil {
			return ErrProtocol
		}
		var code int64
		var message string
		if string(fields["code"]) == "null" || string(fields["message"]) == "null" || json.Unmarshal(fields["code"], &code) != nil || json.Unmarshal(fields["message"], &message) != nil {
			return ErrProtocol
		}
		r.err = ErrRemote
		r.result = nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.pending[id]
	if !ok {
		return ErrProtocol
	}
	delete(c.pending, id)
	ch <- r
	return nil
}
func (c *Client) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	_ = c.stdin.Close() // MCP stdio shutdown has no RPC shutdown method.
	timer := time.NewTimer(c.options.CloseTimeout)
	defer timer.Stop()
	var err error
	select {
	case <-c.exited:
	case <-ctx.Done():
		err = ctx.Err()
		c.fail(ErrUnavailable)
	case <-timer.C:
		_ = terminateProcess(c.cmd)
		grace := time.NewTimer(100 * time.Millisecond)
		select {
		case <-c.exited:
		case <-grace.C:
			c.fail(ErrUnavailable)
		case <-ctx.Done():
			err = ctx.Err()
			c.fail(ErrUnavailable)
		}
		grace.Stop()
	}
	c.fail(ErrUnavailable)
	select {
	case <-c.exited:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-c.writerDone
	return err
}

// Errors are fixed categories; remote error message/data and process stderr are discarded.
