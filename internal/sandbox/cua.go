package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/tools"
)

type commandRunner interface {
	Run(context.Context, []string) ([]byte, int, error)
}
type CUA struct {
	runner    commandRunner
	placement Placement
	mu        sync.Mutex
	closed    bool
}

// CUA control is embedded and uses a private state/home, never a discovered daemon.
func NewCUA(executable, stateDir string, environment []string) (*CUA, error) {
	return newCUA(executable, stateDir, environment, Local)
}

// NewCloudCUA accepts only explicit process credentials; they never enter a guest.
func NewCloudCUA(executable, stateDir string, environment []string) (*CUA, error) {
	return newCUA(executable, stateDir, environment, Cloud)
}
func newCUA(executable, stateDir string, environment []string, placement Placement) (*CUA, error) {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(stateDir) {
		return nil, errorOf(Invalid)
	}
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(executable), ".exe"))
	switch name {
	case "sh", "bash", "cmd", "powershell", "pwsh", "zsh", "fish":
		return nil, errorOf(Invalid)
	}
	allowed := map[string]bool{"PATH": true, "HOME": true, "XDG_RUNTIME_DIR": true, "TMPDIR": true, "LANG": true, "LC_ALL": true}
	if placement == Cloud {
		allowed["FLEETS_TOKEN"] = true
		allowed["CUA_CLIENT_ID"] = true
		allowed["CUA_CLIENT_SECRET"] = true
	}
	env := []string{}
	for _, v := range environment {
		k, _, ok := strings.Cut(v, "=")
		if !ok || !allowed[k] || strings.ContainsAny(v, "\x00\r\n") {
			return nil, errorOf(Invalid)
		}
		env = append(env, v)
	}
	env = append(env, "CUA_HOME="+stateDir, "CUA_NO_DAEMON_AUTOSTART=1")
	if placement == Cloud {
		env = append(env, "CUA_FLEET_BASE_URL=https://run.cua.ai")
	}
	return &CUA{placement: placement, runner: &execRunner{executable: executable, stateDir: filepath.Join(stateDir, "sandboxes"), env: env}}, nil
}
func (c *CUA) run(ctx context.Context, args ...string) ([]byte, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errorOf(Unavailable)
	}
	if e := ctx.Err(); e != nil {
		return nil, &Error{Kind: Canceled, Cause: e}
	}
	raw, exit, e := c.runner.Run(ctx, args)
	if ctx.Err() != nil {
		return nil, &Error{Kind: Canceled, Cause: ctx.Err()}
	}
	if e != nil {
		var typed *Error
		if errors.As(e, &typed) {
			return nil, e
		}
		return nil, &Error{Kind: Transport, Cause: e}
	}
	if exit != 0 {
		k := Provision
		switch exit {
		case 2:
			k = Invalid
		case 3:
			k = NotFound
		case 4:
			k = Unsupported
		case 5:
			k = Transport
		case 6:
			k = Permission
		case 7:
			k = DiskFull
		case 130:
			k = Canceled
		}
		return nil, errorOf(k)
	}
	if strictJSON(raw) != nil {
		return nil, errorOf(Protocol)
	}
	return raw, nil
}
func (c *CUA) Probe(ctx context.Context) (RuntimeInfo, error) {
	if c.placement == Cloud {
		return c.cloudProbe(ctx)
	}
	out := RuntimeInfo{Backend: CUALocal, Runtime: "gvisor"}
	if runtime.GOOS != "linux" {
		return out, errorOf(Unsupported)
	}
	probeCtx, end := context.WithTimeout(ctx, 5*time.Second)
	defer end()
	raw, e := c.run(probeCtx, "runtime", "doctor")
	if e != nil {
		return out, e
	}
	var report struct {
		Host struct {
			OS string `json:"os"`
		} `json:"host"`
		Container struct {
			Reachable bool   `json:"reachable"`
			GVisor    bool   `json:"gvisor"`
			Endpoint  string `json:"endpoint"`
		} `json:"container"`
	}
	if json.Unmarshal(raw, &report) != nil || report.Host.OS != "linux" || !report.Container.Reachable || !report.Container.GVisor || !strings.HasPrefix(report.Container.Endpoint, "unix:///") {
		return out, errorOf(Unavailable)
	}
	out.Available = true
	return out, nil
}
func parseRemote(raw []byte, ref string) (Remote, error) {
	var data struct {
		ID        string  `json:"id"`
		Name      string  `json:"name"`
		Runtime   string  `json:"runtime"`
		Location  string  `json:"location"`
		Kind      string  `json:"kind"`
		State     string  `json:"state"`
		Image     *string `json:"image"`
		ExpiresAt string  `json:"expires_at"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return Remote{}, errorOf(Protocol)
	}
	for _, key := range []string{"id", "name", "runtime", "location", "kind", "state"} {
		var value string
		if json.Unmarshal(fields[key], &value) != nil || value == "" {
			return Remote{}, errorOf(Protocol)
		}
	}
	placement, _, _ := strings.Cut(ref, ":")
	if json.Unmarshal(raw, &data) != nil || data.ID != ref || data.Name != strings.TrimPrefix(ref, placement+":") || data.Location != placement || data.Kind != "container" || data.Runtime != "gvisor" {
		return Remote{}, errorOf(Protocol)
	}
	if data.ExpiresAt != "" {
		expires, e := time.Parse(time.RFC3339Nano, data.ExpiresAt)
		if e != nil {
			return Remote{}, errorOf(Protocol)
		}
		_, offset := expires.Zone()
		if offset != 0 {
			return Remote{}, errorOf(Protocol)
		}
		data.ExpiresAt = expires.UTC().Format(time.RFC3339Nano)
	}
	image := ""
	if data.Image != nil {
		image = *data.Image
		for _, ch := range image {
			if ch < 33 || ch > 126 {
				return Remote{}, errorOf(Protocol)
			}
		}
		if len(image) > 256 || image != "linux" && !strings.HasPrefix(image, "ghcr.io/trycua/linux:") && !strings.HasPrefix(image, "ghcr.io/trycua/linux@sha256:") {
			return Remote{}, errorOf(Protocol)
		}
	}
	switch data.State {
	case "ready", "starting", "provisioning", "stopped":
	default:
		return Remote{}, errorOf(Protocol)
	}
	return Remote{Ref: data.ID, Name: data.Name, Runtime: data.Runtime, Image: image, Ready: data.State == "ready", ExpiresAt: data.ExpiresAt}, nil
}
func validRef(ref string) bool {
	prefix, name, ok := strings.Cut(ref, ":")
	return ok && (prefix == "local" || prefix == "cloud") && strings.HasPrefix(name, "daimon-") && idPattern.MatchString("sb-"+strings.TrimPrefix(name, "daimon-"))
}
func (c *CUA) Create(ctx context.Context, r CreateRequest) (Remote, error) {
	if ValidateProfile(r.Profile) != nil || r.Profile.EffectivePlacement() != c.effectivePlacement() || !validRef(string(c.effectivePlacement())+":"+r.Name) || r.Limits != limits(r.Profile, time.Duration(r.Limits.LifetimeSeconds)*time.Second) || r.Limits.LifetimeSeconds < 1 || r.Limits.LifetimeSeconds > 3600 || r.ReadyTimeout <= 0 {
		return Remote{}, errorOf(Invalid)
	}
	if _, e := c.Probe(ctx); e != nil {
		return Remote{}, e
	}
	args := []string{"sb", "create", "linux", "--name", r.Name, "--on", string(c.effectivePlacement()), "--kind", "container", "--runtime", "gvisor", "--cpu", strconv.Itoa(r.Limits.CPU), "--memory", strconv.Itoa(r.Limits.MemoryMiB) + "MB", "--network", "default", "--port", c.serviceName() + "=3211", "--wait", "desktop", "--ready-timeout", strconv.Itoa(max(1, int(r.ReadyTimeout/time.Second)))}
	if c.placement == Cloud {
		args = append(args, "--claim-ttl", strconv.Itoa(r.Limits.LifetimeSeconds), "--max-pool-size", "2", "--no-warm")
	}
	if r.Profile.Browser {
		args = append(args, "--browser")
	}
	raw, e := c.run(ctx, args...)
	if e != nil {
		return Remote{}, e
	}
	remote, e := parseRemote(raw, string(c.effectivePlacement())+":"+r.Name)
	if e == nil && !remote.Ready {
		e = errorOf(Unavailable)
	}
	return remote, e
}
func (c *CUA) Get(ctx context.Context, ref string) (Remote, error) {
	if !c.validRef(ref) {
		return Remote{}, errorOf(Invalid)
	}
	raw, e := c.run(ctx, "sb", "info", ref, "--"+string(c.effectivePlacement()))
	if e != nil {
		return Remote{}, e
	}
	return parseRemote(raw, ref)
}
func (c *CUA) Delete(ctx context.Context, ref string) error {
	if !c.validRef(ref) {
		return errorOf(Invalid)
	}
	remote, e := c.Get(ctx, ref)
	if errors.Is(e, errorOf(NotFound)) {
		return nil
	}
	if e != nil || remote.Ref != ref {
		if e != nil {
			return e
		}
		return errorOf(Protocol)
	}
	raw, e := c.run(ctx, "sb", "rm", ref, "--"+string(c.effectivePlacement()), "--force")
	if e != nil {
		return e
	}
	var result struct {
		Deleted string `json:"deleted"`
		Missing bool   `json:"missing"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Deleted != ref {
		return errorOf(Protocol)
	}
	return nil
}
func (c *CUA) Close(context.Context) error { c.mu.Lock(); c.closed = true; c.mu.Unlock(); return nil }
func (c *CUA) Computer(ctx context.Context, r Remote, id string) (*computer.Manager, error) {
	if !c.validRef(r.Ref) || r.Name != strings.TrimPrefix(r.Ref, string(c.effectivePlacement())+":") || !r.Ready {
		return nil, errorOf(Unavailable)
	}
	raw, e := c.run(ctx, "sb", "mcp", r.Ref, c.serviceName(), "tools")
	if e != nil {
		return nil, errorOf(Unavailable)
	}
	var catalog struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if json.Unmarshal(raw, &catalog) != nil || len(catalog.Tools) == 0 || len(catalog.Tools) > 64 {
		return nil, errorOf(Protocol)
	}
	names := []string{}
	seen := map[string]bool{}
	for _, t := range catalog.Tools {
		if seen[t.Name] {
			return nil, errorOf(Protocol)
		}
		seen[t.Name] = true
		names = append(names, t.Name)
	}
	adapter, e := computer.NewPlacedSandboxAdapter(id, computer.BackendID("cua-"+string(c.effectivePlacement())), func(ctx context.Context, name string, args json.RawMessage) (tools.ToolResult, error) {
		callCtx, end := context.WithTimeout(ctx, 30*time.Second)
		defer end()
		raw, e := c.run(callCtx, "sb", "mcp", r.Ref, c.serviceName(), "call", name, string(args))
		if e != nil {
			return tools.ToolResult{}, e
		}
		var result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		if json.Unmarshal(raw, &result) != nil || result.IsError || len(result.Content) > 64 {
			return tools.ToolResult{}, errorOf(Protocol)
		}
		var out bytes.Buffer
		for i, b := range result.Content {
			if b.Type != "text" || out.Len()+len(b.Text)+1 > 64*1024 {
				return tools.ToolResult{}, errorOf(Unsupported)
			}
			if i > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(b.Text)
		}
		return tools.ToolResult{Content: out.String()}, nil
	}, names...)
	if e != nil {
		return nil, errorOf(Unavailable)
	}
	child, e := computer.NewManager(adapter)
	if e != nil {
		return nil, errorOf(Unavailable)
	}
	// Media configuration is private and optional. Actions stay scoped to the
	// guest even when this guest has no compatible Phase 13 media service.

	raw, e = c.run(ctx, "sb", "mcp", r.Ref, c.serviceName(), "config", "--show-secrets")
	if e == nil {
		var cfg struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		}
		if json.Unmarshal(raw, &cfg) == nil && cfg.Type == "http" {
			u, e := url.Parse(cfg.URL)
			if c.placement == Cloud {
				if e == nil && strings.HasSuffix(u.Path, "/mcp") {
					u.Path = strings.TrimSuffix(u.Path, "/mcp")
					if media, e := computer.NewFleetMedia(u.String(), cfg.Headers); e == nil {
						_ = child.ConfigureMedia(media)
					}
				}
				return child, nil
			}
			if e == nil && u.Path == "/mcp" && u.RawQuery == "" && u.Fragment == "" && u.User == nil {
				u.Path = ""
				secret := ""
				valid := true
				for k, v := range cfg.Headers {
					if (strings.EqualFold(k, "authorization") || strings.EqualFold(k, "x-cua-env-authorization")) && strings.HasPrefix(v, "Bearer ") {
						if secret != "" {
							valid = false
						}
						secret = strings.TrimPrefix(v, "Bearer ")
					} else {
						valid = false
					}
				}
				if media, e := computer.NewCUAMedia(u.String(), secret); valid && e == nil {
					_ = child.ConfigureMedia(media)
				}
			}
		}
	}
	return child, nil
}

func (c *CUA) effectivePlacement() Placement {
	if c.placement == Cloud {
		return Cloud
	}
	return Local
}
func (c *CUA) validRef(ref string) bool {
	return validRef(ref) && strings.HasPrefix(ref, string(c.effectivePlacement())+":")
}
func (c *CUA) cloudProbe(ctx context.Context) (RuntimeInfo, error) {
	out := RuntimeInfo{Backend: CUACloud, Runtime: "gvisor"}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return out, errorOf(Unavailable)
	}
	probeCtx, end := context.WithTimeout(ctx, 5*time.Second)
	defer end()
	raw, exit, e := c.runner.Run(probeCtx, []string{"auth", "whoami"})
	if probeCtx.Err() != nil {
		return out, &Error{Kind: Canceled, Cause: probeCtx.Err()}
	}
	if e != nil {
		return out, e
	}
	var identity struct {
		Authenticated *bool  `json:"authenticated"`
		Fleet         string `json:"fleet"`
	}
	if strictJSON(raw) == nil && json.Unmarshal(raw, &identity) == nil && identity.Authenticated != nil {
		if !*identity.Authenticated && (exit == 0 || exit == 1) {
			return out, errorOf(SignedOut)
		}
		if *identity.Authenticated && exit == 0 && identity.Fleet == "https://run.cua.ai" {
			out.Available = true
			return out, nil
		}
	}
	if exit == 4 || exit == 6 {
		return out, errorOf(SignedOut)
	}
	return out, errorOf(CloudUnavailable)
}

func (c *CUA) serviceName() string {
	if c.placement == Cloud {
		return "env"
	}
	return "spacesd"
}
