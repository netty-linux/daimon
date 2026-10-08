package sandbox

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/environments"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
)

//go:embed workspace_inspector.py
var workspaceInspector string

const guestWorkspace = "/workspace"

type WorkspaceProvider interface {
	Workspace(context.Context, Remote) (environments.Transfer, error)
}

func (l *Lease) Workspace(ctx context.Context) (environments.Transfer, error) {
	l.manager.mu.Lock()
	r, ok := l.manager.records[l.id]
	backend := l.manager.backend
	if r.Info.Placement == Cloud {
		backend = l.manager.cloud
	}
	l.manager.mu.Unlock()
	if !ok || r.Info.Status != Running || ctx.Err() != nil || l.Context.Err() != nil {
		return nil, environments.ErrTransfer
	}
	p, ok := backend.(WorkspaceProvider)
	if !ok {
		return nil, environments.ErrTransfer
	}
	return p.Workspace(ctx, Remote{Ref: r.Ref, Name: r.Name, Runtime: r.Info.Runtime, Ready: true})
}

type workspaceRPC interface {
	WorkspaceRPC(context.Context, string, string, []byte) ([][]byte, error)
}
type workspaceTransfer struct{ rpc workspaceRPC }

func (c *CUA) Workspace(ctx context.Context, r Remote) (environments.Transfer, error) {
	if !c.validRef(r.Ref) || r.Runtime != "gvisor" || !r.Ready {
		return nil, environments.ErrTransfer
	}
	raw, e := c.run(ctx, "sb", "mcp", r.Ref, c.serviceName(), "config", "--show-secrets")
	if e != nil {
		return nil, e
	}
	var cfg struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if strictJSON(raw) != nil || json.Unmarshal(raw, &cfg) != nil || cfg.Type != "http" {
		return nil, environments.ErrTransfer
	}
	u, e := url.Parse(cfg.URL)
	if e != nil || !strings.HasSuffix(u.Path, "/mcp") {
		return nil, environments.ErrTransfer
	}
	u.Path = strings.TrimSuffix(u.Path, "/mcp")
	var client *computer.CUAMedia
	if c.placement == Cloud {
		client, e = computer.NewFleetMedia(u.String(), cfg.Headers)
	} else {
		if u.Path != "" {
			return nil, environments.ErrTransfer
		}
		secret := ""
		for k, v := range cfg.Headers {
			if secret != "" || !(strings.EqualFold(k, "authorization") || strings.EqualFold(k, "x-cua-env-authorization")) || !strings.HasPrefix(v, "Bearer ") {
				return nil, environments.ErrTransfer
			}
			secret = strings.TrimPrefix(v, "Bearer ")
		}
		client, e = computer.NewCUAMedia(u.String(), secret)
	}
	if e != nil {
		return nil, environments.ErrTransfer
	}
	return &workspaceTransfer{rpc: client}, nil
}
func (t *workspaceTransfer) call(ctx context.Context, method string, p []byte) ([]byte, error) {
	m, e := t.rpc.WorkspaceRPC(ctx, "FilesystemService", method, p)
	if e != nil {
		return nil, e
	}
	if len(m) != 1 {
		return nil, environments.ErrTransfer
	}
	return m[0], nil
}

type inspectedFile struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Size      uint64 `json:"size"`
	Inode     uint64 `json:"inode"`
	Device    uint64 `json:"device"`
	Links     uint64 `json:"links"`
	Mtime     int64  `json:"mtime"`
	Ctime     int64  `json:"ctime"`
}
type inspection struct {
	Version int             `json:"version"`
	Entries []inspectedFile `json:"entries"`
}

func (t *workspaceTransfer) inspect(ctx context.Context) (inspection, error) {
	var out inspection
	ctx, end := context.WithTimeout(ctx, 15*time.Second)
	defer end()
	// Fixed executable, code, cwd and timeout. No caller/model string enters argv.
	cfg := ws(1, "/usr/bin/python3")
	for _, arg := range []string{"-I", "-c", workspaceInspector} {
		cfg = append(cfg, ws(2, arg)...)
	}
	cfg = append(cfg, ws(4, "/")...)
	cfg = append(cfg, wb(6, wi(1, 15))...)
	p := wb(1, cfg)
	p = append(p, wi(6, 65536)...)
	p = append(p, wi(7, 1)...)
	frames, e := t.rpc.WorkspaceRPC(ctx, "ProcessService", "StartProcess", p)
	if e != nil {
		return out, e
	}
	data := []byte{}
	started, ended := false, false
	offset := uint64(0)
	for _, frame := range frames {
		outer, e := wire(frame)
		if e != nil {
			return out, environments.ErrTransfer
		}
		v := only(outer, 1)
		if v.wire != 2 {
			return out, environments.ErrTransfer
		}
		event, e := wire(v.data)
		if e != nil || len(event) != 1 || ended {
			return out, environments.ErrTransfer
		}
		if value, ok := event[1]; ok {
			if started || len(value) != 1 {
				return out, environments.ErrTransfer
			}
			start, e := wire(value[0].data)
			pid, pe := number(start, 1)
			if e != nil || pe != nil || pid == 0 {
				return out, environments.ErrTransfer
			}
			started = true
			continue
		}
		if !started {
			return out, environments.ErrTransfer
		}
		if value, ok := event[2]; ok {
			if len(value) != 1 {
				return out, environments.ErrTransfer
			}
			chunk, e := wire(value[0].data)
			if e != nil {
				return out, environments.ErrTransfer
			}
			n, e := number(chunk, 1)
			if e != nil || n != offset || len(chunk[3]) != 0 || len(chunk[4]) != 0 {
				return out, environments.ErrTransfer
			}
			bytes := only(chunk, 2)
			if bytes.wire != 2 || len(bytes.data) > 65536-len(data) {
				return out, environments.ErrTransfer
			}
			data = append(data, bytes.data...)
			offset += uint64(len(bytes.data))
			continue
		}
		if value, ok := event[3]; ok {
			if len(value) != 1 {
				return out, environments.ErrTransfer
			}
			last, e := wire(value[0].data)
			exit := only(last, 1)
			signal, se := number(last, 2)
			timeout, te := number(last, 3)
			message, me := textField(last, 4)
			if e != nil || exit.wire != 0 || exit.number != 0 || se != nil || signal != 0 || te != nil || timeout != 0 || me != nil || message != "" {
				return out, environments.ErrTransfer
			}
			ended = true
			continue
		}
		if _, ok := event[4]; !ok {
			return out, environments.ErrTransfer
		}
	}
	if !started || !ended || strictJSON(data) != nil || !exactSchema(data, reflect.TypeFor[inspection]()) || json.Unmarshal(data, &out) != nil || out.Version != 1 || len(out.Entries) > environments.MaxFiles+environments.MaxDirectories {
		return out, environments.ErrTransfer
	}
	seen := map[string]bool{}
	identities := map[[2]uint64]bool{}
	dirs := map[string]bool{}
	files, total, dirCount := 0, uint64(0), 0
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Path < out.Entries[j].Path })
	for _, f := range out.Entries {
		if !environments.ValidPath(f.Path) || seen[strings.ToLower(f.Path)] || f.Inode == 0 || f.Links == 0 {
			return out, environments.ErrTransfer
		}
		seen[strings.ToLower(f.Path)] = true
		if i := strings.LastIndexByte(f.Path, '/'); i >= 0 && !dirs[f.Path[:i]] {
			return out, environments.ErrTransfer
		}
		if f.Directory {
			if f.Size != 0 {
				return out, environments.ErrTransfer
			}
			dirs[f.Path] = true
			dirCount++
		} else {
			identity := [2]uint64{f.Device, f.Inode}
			if identities[identity] {
				return out, environments.ErrTransfer
			}
			identities[identity] = true
			if f.Links != 1 || f.Size > environments.MaxFileBytes || f.Size > environments.MaxBytes-total {
				return out, environments.ErrTransfer
			}
			files++
			total += f.Size
		}
		if files > environments.MaxFiles || dirCount > environments.MaxDirectories {
			return out, environments.ErrTransfer
		}
	}
	return out, nil
}
func sameInspection(a, b inspection) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (t *workspaceTransfer) read(ctx context.Context, f inspectedFile) ([]byte, error) {
	p := ws(1, guestWorkspace+"/"+f.Path)
	p = append(p, wi(3, environments.MaxFileBytes+1)...)
	p = append(p, wi(4, 8192)...)
	p = append(p, wi(5, 1)...)
	frames, e := t.rpc.WorkspaceRPC(ctx, "FilesystemService", "ReadFile", p)
	if e != nil {
		return nil, e
	}
	data := []byte{}
	first, last := false, false
	for _, frame := range frames {
		m, e := wire(frame)
		if e != nil || len(m) != 1 || last {
			return nil, environments.ErrTransfer
		}
		if values, ok := m[1]; ok {
			if first || len(values) != 1 {
				return nil, environments.ErrTransfer
			}
			entry, e := wire(values[0].data)
			size, se := number(entry, 4)
			kind, ke := number(entry, 3)
			path, pe := textField(entry, 2)
			if e != nil || se != nil || ke != nil || pe != nil || size != f.Size || kind != 1 || path != guestWorkspace+"/"+f.Path {
				return nil, environments.ErrTransfer
			}
			first = true
			continue
		}
		if !first {
			return nil, environments.ErrTransfer
		}
		if values, ok := m[2]; ok {
			if len(values) != 1 {
				return nil, environments.ErrTransfer
			}
			chunk, e := wire(values[0].data)
			offset, oe := number(chunk, 1)
			b := only(chunk, 2)
			if e != nil || oe != nil || offset != uint64(len(data)) || b.wire != 2 || len(b.data) > environments.MaxFileBytes-len(data) {
				return nil, environments.ErrTransfer
			}
			data = append(data, b.data...)
			continue
		}
		values, ok := m[3]
		if !ok || len(values) != 1 {
			return nil, environments.ErrTransfer
		}
		end, e := wire(values[0].data)
		count, ce := number(end, 1)
		hash, he := textField(end, 2)
		if e != nil || ce != nil || he != nil || count != uint64(len(data)) || hash != environments.Hash(data) {
			return nil, environments.ErrTransfer
		}
		last = true
	}
	if !first || !last || uint64(len(data)) != f.Size {
		return nil, environments.ErrTransfer
	}
	return data, nil
}
func (t *workspaceTransfer) Export(ctx context.Context) (environments.Workspace, error) {
	ctx, cancel := context.WithTimeout(ctx, environments.TransferDuration)
	defer cancel()
	before, e := t.inspect(ctx)
	if e != nil {
		return nil, e
	}
	w := environments.Workspace{}
	for _, f := range before.Entries {
		entry := environments.Entry{Path: f.Path, Directory: f.Directory}
		if !f.Directory {
			entry.Data, e = t.read(ctx, f)
			if e != nil {
				return nil, e
			}
		}
		w = append(w, entry)
	}
	after, e := t.inspect(ctx)
	if e != nil {
		return nil, e
	}
	if !sameInspection(before, after) {
		return nil, environments.ErrTransfer
	}
	if _, e = environments.Describe(w); e != nil {
		return nil, e
	}
	return w, nil
}
func (t *workspaceTransfer) upload(ctx context.Context, f environments.Entry) (err error) {
	header := ws(1, guestWorkspace+"/"+f.Path)
	header = append(header, wi(2, 2)...)
	header = append(header, wi(3, 0600)...)
	header = append(header, wi(5, uint64(len(f.Data)))...)
	header = append(header, ws(6, environments.Hash(f.Data))...)
	p := wb(1, header)
	p = append(p, wb(3, wi(1, 60))...)
	raw, e := t.call(ctx, "BeginUpload", p)
	if e != nil {
		return e
	}
	m, e := wire(raw)
	id, ie := textField(m, 1)
	received, re := number(m, 2)
	chunkSize, ce := number(m, 3)
	if e != nil || ie != nil || re != nil || ce != nil || id == "" || len(id) > 128 || received != 0 || chunkSize < 1 {
		return environments.ErrTransfer
	}
	committed := false
	defer func() {
		if !committed {
			cleanup, end := context.WithTimeout(context.Background(), 5*time.Second)
			defer end()
			if _, e := t.call(cleanup, "AbortUpload", ws(1, id)); e != nil {
				err = errors.Join(err, environments.ErrCleanup)
			}
		}
	}()
	for offset := 0; offset < len(f.Data); {
		n := len(f.Data) - offset
		if n > 8192 {
			n = 8192
		}
		if uint64(n) > chunkSize {
			n = int(chunkSize)
		}
		p = ws(1, id)
		p = append(p, wi(2, uint64(offset))...)
		p = append(p, wb(3, f.Data[offset:offset+n])...)
		raw, e = t.call(ctx, "UploadChunk", p)
		if e != nil {
			return e
		}
		m, e = wire(raw)
		count, ce := number(m, 1)
		duplicate, de := number(m, 2)
		if e != nil || ce != nil || de != nil || duplicate != 0 || count != uint64(offset+n) {
			return environments.ErrTransfer
		}
		offset += n
	}
	p = ws(1, id)
	p = append(p, ws(2, environments.Hash(f.Data))...)
	raw, e = t.call(ctx, "CommitUpload", p)
	if e != nil {
		return e
	}
	m, e = wire(raw)
	hash, he := textField(m, 2)
	if e != nil || he != nil || hash != environments.Hash(f.Data) {
		return environments.ErrTransfer
	}
	committed = true
	return nil
}
func (t *workspaceTransfer) Hydrate(ctx context.Context, w environments.Workspace) error {
	ctx, cancel := context.WithTimeout(ctx, environments.TransferDuration)
	defer cancel()
	manifest, e := environments.Describe(w)
	if e != nil {
		return e
	}
	p := ws(1, guestWorkspace)
	p = append(p, wi(3, 0700)...)
	if _, e = t.call(ctx, "MakeDir", p); e != nil {
		return e
	}
	initial, e := t.inspect(ctx)
	if e != nil {
		return e
	}
	if len(initial.Entries) != 0 {
		return environments.ErrTransfer
	}
	for _, f := range manifest.Entries {
		if f.Directory {
			p = ws(1, guestWorkspace+"/"+f.Path)
			p = append(p, wi(3, 0700)...)
			if _, e = t.call(ctx, "MakeDir", p); e != nil {
				return e
			}
		} else {
			for _, entry := range w {
				if entry.Path == f.Path {
					if e = t.upload(ctx, entry); e != nil {
						return e
					}
					break
				}
			}
		}
	}
	verified, e := t.Export(ctx)
	if e != nil {
		return e
	}
	actual, e := environments.Describe(verified)
	if e != nil || actual.SHA256 != manifest.SHA256 {
		return environments.ErrTransfer
	}
	return nil
}
