// Package environments owns durable workspace revisions independently of compute.
package environments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/netty-linux/daimon/internal/managedworkspace"
	"github.com/netty-linux/daimon/internal/workspaceplan"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	MaxEnvironments  = 32
	MaxFiles         = 64
	MaxDirectories   = 32
	MaxFileBytes     = 64 * 1024
	MaxBytes         = 1024 * 1024
	MaxDepth         = 8
	TransferDuration = 60 * time.Second
)

var (
	ErrInvalid  = errors.New("environment_invalid")
	ErrLimit    = errors.New("environment_limit")
	ErrNotFound = errors.New("environment_not_found")
	ErrExists   = errors.New("environment_exists")
	ErrBusy     = errors.New("environment_busy")
	ErrConflict = errors.New("environment_conflict")
	ErrStorage  = errors.New("environment_storage")
	ErrCleanup  = errors.New("environment_cleanup")
	ErrTransfer = errors.New("environment_transfer")
	ErrVersion  = errors.New("environment_version")
)

type Entry = managedworkspace.Entry
type Workspace []Entry
type File struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Size      int    `json:"size"`
	SHA256    string `json:"sha256"`
}
type Manifest struct {
	Version     int    `json:"version"`
	Entries     []File `json:"entries"`
	Files       int    `json:"files"`
	Directories int    `json:"directories"`
	Bytes       int    `json:"bytes"`
	SHA256      string `json:"sha256"`
}

func Hash(p []byte) string { h := sha256.Sum256(p); return hex.EncodeToString(h[:]) }
func ValidPath(p string) bool {
	return workspaceplan.ValidPath(p, workspaceplan.DefaultLimits()) && strings.Count(p, "/")+1 <= MaxDepth
}
func Clone(w Workspace) Workspace {
	out := make(Workspace, len(w))
	for i, e := range w {
		out[i] = Entry{Path: e.Path, Directory: e.Directory, Data: append([]byte{}, e.Data...)}
	}
	return out
}
func Describe(w Workspace) (Manifest, error) {
	m := Manifest{Version: 1, Entries: []File{}}
	if len(w) > MaxFiles+MaxDirectories {
		return m, ErrLimit
	}
	entries := Clone(w)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	seen := map[string]bool{}
	dirs := map[string]bool{}
	for _, e := range entries {
		if !ValidPath(e.Path) || seen[strings.ToLower(e.Path)] || e.Directory && len(e.Data) != 0 {
			return m, ErrInvalid
		}
		seen[strings.ToLower(e.Path)] = true
		if i := strings.LastIndexByte(e.Path, '/'); i >= 0 && !dirs[e.Path[:i]] {
			return m, ErrInvalid
		}
		if e.Directory {
			m.Directories++
			dirs[e.Path] = true
		} else {
			m.Files++
			if len(e.Data) > MaxFileBytes || len(e.Data) > MaxBytes-m.Bytes {
				return m, ErrLimit
			}
			m.Bytes += len(e.Data)
		}
		if m.Files > MaxFiles || m.Directories > MaxDirectories {
			return m, ErrLimit
		}
		m.Entries = append(m.Entries, File{e.Path, e.Directory, len(e.Data), Hash(e.Data)})
	}
	b, err := json.Marshal(m.Entries)
	if err != nil {
		return m, ErrInvalid
	}
	m.SHA256 = Hash(b)
	return m, nil
}
func equalManifest(a, b Manifest) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func ReadWorkspace(ctx context.Context, directory string) (Workspace, error) {
	for p := directory; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
			return nil, ErrStorage
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	w, err := managedworkspace.Snapshot(ctx, directory)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrStorage
	}
	if _, err = Describe(w); err != nil {
		return nil, err
	}
	r, e := os.OpenRoot(directory)
	if e != nil {
		return nil, ErrStorage
	}
	defer r.Close()
	for _, entry := range w {
		i, e := r.Lstat(entry.Path)
		if e != nil || entry.Directory && !privateDirectory(i) || !entry.Directory && !regular(i) {
			return nil, ErrStorage
		}
	}
	return w, nil
}

// Transfer is internal infrastructure bound to one owned guest, never a Tool.
type Transfer interface {
	Hydrate(context.Context, Workspace) error
	Export(context.Context) (Workspace, error)
}

func revisionName(n uint64) string { return fmt.Sprintf("r-%016x", n) }
