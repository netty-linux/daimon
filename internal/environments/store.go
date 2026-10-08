package environments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Metadata struct {
	Version      int       `json:"version"`
	ID           string    `json:"id"`
	ThreadID     string    `json:"thread_id"`
	Revision     uint64    `json:"revision"`
	Files        int       `json:"files"`
	Directories  int       `json:"directories"`
	Bytes        int       `json:"bytes"`
	ManifestHash string    `json:"manifest_hash"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type intent struct {
	Version       int    `json:"version"`
	EnvironmentID string `json:"environment_id"`
	Revision      uint64 `json:"revision"`
}
type Store struct {
	mu        sync.Mutex
	root      *os.Root
	directory string
	busy      map[string]bool
	closed    bool
}

var envID = regexp.MustCompile(`^env-[a-f0-9]{24}$`)
var threadID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// NewStore opens an existing private Linux directory; it never chooses HOME.
// One Store/process owns it. Recovery uses the exact per-Environment intent.
func NewStore(directory string) (*Store, error) {
	if !Supported() || !filepath.IsAbs(directory) {
		return nil, ErrInvalid
	}
	for p := directory; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
			return nil, ErrStorage
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	i, e := os.Lstat(directory)
	if e != nil || !privateDirectory(i) {
		return nil, ErrStorage
	}
	root, e := os.OpenRoot(directory)
	if e != nil {
		return nil, ErrStorage
	}
	s := &Store{root: root, directory: directory, busy: map[string]bool{}}
	if e = s.recoverCreation(); e != nil {
		root.Close()
		return nil, e
	}
	if e = s.recover(); e != nil {
		root.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.root.Close()
}
func readJSON(r *os.Root, p string, out any) error {
	parts := strings.Split(p, "/")
	for n := 1; n < len(parts); n++ {
		i, e := r.Lstat(strings.Join(parts[:n], "/"))
		if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
			return ErrStorage
		}
	}
	i, e := r.Lstat(p)
	if e != nil {
		return e
	}
	if !regular(i) || i.Size() > 128*1024 {
		return ErrStorage
	}
	f, e := r.Open(p)
	if e != nil {
		return ErrStorage
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !regular(opened) || !os.SameFile(i, opened) {
		return ErrStorage
	}
	b, e := io.ReadAll(io.LimitReader(f, 128*1024+1))
	if e != nil {
		return ErrStorage
	}
	after, se := f.Stat()
	current, ce := r.Lstat(p)
	if se != nil || ce != nil || !regular(after) || !regular(current) || !os.SameFile(i, after) || !os.SameFile(after, current) || i.Size() != after.Size() || i.ModTime() != after.ModTime() || int64(len(b)) != after.Size() {
		return ErrStorage
	}
	return decode(b, out)
}
func writeJSON(ctx context.Context, r *os.Root, p string, v any) error {
	b, e := json.Marshal(v)
	if e != nil || len(b) > 128*1024 {
		return ErrLimit
	}
	return write(ctx, r, p, append(b, '\n'))
}
func write(ctx context.Context, r *os.Root, p string, b []byte) (err error) {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	f, e := r.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return ErrStorage
	}
	defer func() {
		if e := f.Close(); err == nil && e != nil {
			err = ErrStorage
		}
	}()
	if f.Chmod(0600) != nil {
		return ErrStorage
	}
	for len(b) > 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n := len(b)
		if n > 8192 {
			n = 8192
		}
		w, e := f.Write(b[:n])
		if e != nil || w != n {
			return ErrStorage
		}
		b = b[n:]
	}
	if f.Sync() != nil {
		return ErrStorage
	}
	return nil
}
func validMeta(m Metadata) error {
	if m.Version != 1 {
		return ErrVersion
	}
	if !envID.MatchString(m.ID) || !threadID.MatchString(m.ThreadID) || m.Files < 0 || m.Files > MaxFiles || m.Directories < 0 || m.Directories > MaxDirectories || m.Bytes < 0 || m.Bytes > MaxBytes || len(m.ManifestHash) != 64 || m.CreatedAt.IsZero() || m.UpdatedAt.Before(m.CreatedAt) || m.CreatedAt.Location() != time.UTC || m.UpdatedAt.Location() != time.UTC {
		return ErrStorage
	}
	return nil
}
func (s *Store) directories() ([]string, error) {
	f, e := s.root.Open(".")
	if e != nil {
		return nil, ErrStorage
	}
	defer f.Close()
	list, e := f.ReadDir(MaxEnvironments + 1)
	if e != nil && e != io.EOF {
		return nil, ErrStorage
	}
	if len(list) > MaxEnvironments {
		return nil, ErrLimit
	}
	out := []string{}
	for _, d := range list {
		if !envID.MatchString(d.Name()) || !d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil, ErrStorage
		}
		out = append(out, d.Name())
	}
	return out, nil
}
func (s *Store) meta(id string) (Metadata, error) {
	var m Metadata
	if e := readJSON(s.root, id+"/current.json", &m); e != nil {
		return m, ErrStorage
	}
	if e := validMeta(m); e != nil {
		return m, e
	}
	if m.ID != id {
		return m, ErrStorage
	}
	return m, nil
}
func (s *Store) find(thread string) (Metadata, error) {
	if s.closed || !threadID.MatchString(thread) {
		return Metadata{}, ErrInvalid
	}
	ids, e := s.directories()
	if e != nil {
		return Metadata{}, e
	}
	var found Metadata
	for _, id := range ids {
		m, e := s.meta(id)
		if e != nil {
			return Metadata{}, e
		}
		if m.ThreadID == thread {
			if found.ID != "" {
				return Metadata{}, ErrStorage
			}
			found = m
		}
	}
	if found.ID == "" {
		return Metadata{}, ErrNotFound
	}
	return found, nil
}
func (s *Store) Get(ctx context.Context, thread string) (Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return Metadata{}, ctx.Err()
	}
	m, e := s.find(thread)
	if e != nil {
		return m, e
	}
	_, e = s.workspace(ctx, m)
	return m, e
}
func (s *Store) workspace(ctx context.Context, m Metadata) (Workspace, error) {
	base := m.ID + "/" + revisionName(m.Revision)
	var saved Manifest
	if e := readJSON(s.root, base+"/manifest.json", &saved); e != nil {
		return nil, ErrStorage
	}
	w, e := ReadWorkspace(ctx, filepath.Join(s.directory, filepath.FromSlash(base), "workspace"))
	if e != nil {
		return nil, e
	}
	actual, e := Describe(w)
	if e != nil || !equalManifest(actual, saved) || actual.SHA256 != m.ManifestHash || actual.Files != m.Files || actual.Directories != m.Directories || actual.Bytes != m.Bytes {
		return nil, ErrStorage
	}
	return w, nil
}
func (s *Store) materialize(ctx context.Context, base string, w Workspace) (Manifest, error) {
	m, e := Describe(w)
	if e != nil {
		return m, e
	}
	if s.makeDir(base) != nil || s.makeDir(base+"/workspace") != nil {
		return m, ErrStorage
	}
	entries := Clone(w)
	// Describe already checks explicit parents; manifest order creates parents first.
	for _, f := range m.Entries {
		if ctx.Err() != nil {
			return m, ctx.Err()
		}
		p := base + "/workspace/" + f.Path
		if f.Directory {
			if s.makeDir(p) != nil {
				return m, ErrStorage
			}
		} else {
			var b []byte
			for _, entry := range entries {
				if entry.Path == f.Path {
					b = entry.Data
					break
				}
			}
			if e = write(ctx, s.root, p, b); e != nil {
				return m, e
			}
		}
	}
	if e = writeJSON(ctx, s.root, base+"/manifest.json", m); e != nil {
		return m, e
	}
	actual, e := ReadWorkspace(ctx, filepath.Join(s.directory, filepath.FromSlash(base), "workspace"))
	if e != nil {
		return m, e
	}
	check, e := Describe(actual)
	if e != nil || !equalManifest(m, check) {
		return m, ErrStorage
	}
	return m, nil
}
func (s *Store) Create(ctx context.Context, thread string) (out Metadata, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if !threadID.MatchString(thread) {
		return out, ErrInvalid
	}
	if _, e := s.find(thread); e == nil {
		return out, ErrExists
	} else if !errors.Is(e, ErrNotFound) {
		return out, e
	}
	ids, e := s.directories()
	if e != nil {
		return out, e
	}
	if len(ids) >= MaxEnvironments {
		return out, ErrLimit
	}
	var random [12]byte
	if _, e = rand.Read(random[:]); e != nil {
		return out, ErrStorage
	}
	id := "env-" + hex.EncodeToString(random[:])
	if _, e = s.root.Lstat(id); !errors.Is(e, os.ErrNotExist) {
		return out, ErrExists
	}
	if e = writeJSON(ctx, s.root, "create-next.json", intent{1, id, 0}); e != nil {
		if cleanup := s.root.Remove("create-next.json"); cleanup != nil && !errors.Is(cleanup, os.ErrNotExist) {
			e = errors.Join(e, ErrCleanup)
		}
		return out, e
	}
	if e = s.root.Rename("create-next.json", "creation.json"); e != nil {
		if s.root.Remove("create-next.json") != nil {
			return out, errors.Join(ErrStorage, ErrCleanup)
		}
		return out, ErrStorage
	}
	created := false
	ownsDirectory := false
	defer func() {
		if err != nil && !created && ownsDirectory {
			if e := s.root.RemoveAll(id); e != nil {
				err = errors.Join(err, ErrCleanup)
			}
		}
		if e := s.root.Remove("creation.json"); e != nil {
			err = errors.Join(err, ErrCleanup)
		}
	}()
	if e = s.root.Mkdir(id, 0700); e != nil {
		return out, ErrStorage
	}
	ownsDirectory = true
	if e = s.root.Chmod(id, 0700); e != nil {
		return out, ErrStorage
	}
	manifest, e := s.materialize(ctx, id+"/"+revisionName(0), Workspace{})
	if e != nil {
		return out, e
	}
	now := time.Now().UTC()
	out = Metadata{1, id, thread, 0, 0, 0, 0, manifest.SHA256, now, now}
	err = writeJSON(ctx, s.root, id+"/next.json", out)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		if e = s.root.Rename(id+"/next.json", id+"/current.json"); e != nil {
			err = ErrStorage
		} else {
			created = true
		}
	}
	return out, err
}

// Reservation pins the starting revision and prevents deletion for its lifetime.
type Reservation struct {
	active    bool
	id        string
	revision  uint64
	store     *Store
	Metadata  Metadata
	workspace Workspace
	once      sync.Once
}

func (s *Store) Acquire(ctx context.Context, thread string) (*Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, e := s.find(thread)
	if e != nil {
		return nil, e
	}
	if s.busy[m.ID] {
		return nil, ErrBusy
	}
	w, e := s.workspace(ctx, m)
	if e != nil {
		return nil, e
	}
	s.busy[m.ID] = true
	return &Reservation{store: s, Metadata: m, workspace: w, id: m.ID, revision: m.Revision, active: true}, nil
}
func (r *Reservation) Workspace() Workspace { return Clone(r.workspace) }
func (r *Reservation) Close() {
	r.once.Do(func() { r.store.mu.Lock(); r.active = false; delete(r.store.busy, r.id); r.store.mu.Unlock() })
}

// Commit reports committed even when subsequent cleanup fails. Never roll it back.
func (r *Reservation) Commit(ctx context.Context, w Workspace) (Metadata, bool, error) {
	s := r.store
	s.mu.Lock()
	defer s.mu.Unlock()
	m, e := s.meta(r.id)
	if e != nil {
		return m, false, e
	}
	if !r.active || !s.busy[m.ID] {
		return m, false, ErrBusy
	}
	if m.Revision != r.revision {
		return m, false, ErrConflict
	}
	if m.Revision == math.MaxUint64 {
		return m, false, ErrLimit
	}
	if ctx.Err() != nil {
		return m, false, ctx.Err()
	}
	if _, e = s.workspace(ctx, m); e != nil {
		return m, false, e
	}
	p := intent{1, m.ID, m.Revision + 1}
	// Never register/adopt a pre-existing unowned stage or next revision.
	for _, name := range []string{"pending.json", "stage", "next.json", revisionName(p.Revision)} {
		if _, e = s.root.Lstat(m.ID + "/" + name); !errors.Is(e, os.ErrNotExist) {
			return m, false, ErrStorage
		}
	}
	if e = writeJSON(ctx, s.root, m.ID+"/pending.json", p); e != nil {
		if cleanup := s.root.Remove(m.ID + "/pending.json"); cleanup != nil && !errors.Is(cleanup, os.ErrNotExist) {
			e = errors.Join(e, ErrCleanup)
		}
		return m, false, e
	}
	committed := false
	finish := func(cause error) (Metadata, bool, error) {
		if e := s.recoverOne(m.ID); e != nil {
			cause = errors.Join(cause, e)
		}
		return m, committed, cause
	}
	manifest, e := s.materialize(ctx, m.ID+"/stage", w)
	if e != nil {
		return finish(e)
	}
	if e = s.root.Rename(m.ID+"/stage", m.ID+"/"+revisionName(p.Revision)); e != nil {
		return finish(ErrStorage)
	}
	next := m
	next.Revision = p.Revision
	next.Files = manifest.Files
	next.Directories = manifest.Directories
	next.Bytes = manifest.Bytes
	next.ManifestHash = manifest.SHA256
	next.UpdatedAt = time.Now().UTC()
	if !next.UpdatedAt.After(m.UpdatedAt) {
		next.UpdatedAt = m.UpdatedAt.Add(time.Nanosecond)
	}
	if e = writeJSON(ctx, s.root, m.ID+"/next.json", next); e != nil {
		return finish(e)
	}
	if ctx.Err() != nil {
		return finish(ctx.Err())
	}
	current, e := s.meta(m.ID)
	if e != nil {
		return finish(e)
	}
	if current.Revision != r.revision || current.ManifestHash != m.ManifestHash {
		return finish(ErrConflict)
	}
	if e = s.root.Rename(m.ID+"/next.json", m.ID+"/current.json"); e != nil {
		return finish(ErrStorage)
	}
	committed = true
	m = next
	return finish(nil)
}
func (s *Store) recoverOne(id string) error {
	var p intent
	e := readJSON(s.root, id+"/pending.json", &p)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || p.Version != 1 || p.EnvironmentID != id || p.Revision == 0 {
		return ErrStorage
	}
	m, e := s.meta(id)
	if e != nil {
		return e
	}
	if _, e = s.workspace(context.Background(), m); e != nil {
		return e
	}
	if m.Revision != p.Revision && (m.Revision == math.MaxUint64 || m.Revision+1 != p.Revision) {
		return ErrConflict
	}
	// Intent is written before any of these exact owned paths are created.
	remove := []string{id + "/stage", id + "/next.json"}
	if m.Revision == p.Revision {
		remove = append(remove, id+"/"+revisionName(p.Revision-1))
	} else {
		remove = append(remove, id+"/"+revisionName(p.Revision))
	}
	for _, path := range remove {
		if e := s.root.RemoveAll(path); e != nil {
			return ErrCleanup
		}
	}
	if e = s.root.Remove(id + "/pending.json"); e != nil {
		return ErrCleanup
	}
	return nil
}
func (s *Store) recover() error {
	ids, e := s.directories()
	if e != nil {
		return e
	}
	threads := map[string]bool{}
	for _, id := range ids {
		if e = s.recoverOne(id); e != nil {
			return e
		}
		m, e := s.meta(id)
		if e != nil {
			return e
		}
		if threads[m.ThreadID] {
			return ErrStorage
		}
		threads[m.ThreadID] = true
		if _, e = s.workspace(context.Background(), m); e != nil {
			return e
		}
		if e = s.layout(m); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) makeDir(path string) error {
	if s.root.Mkdir(path, 0700) != nil {
		return ErrStorage
	}
	if s.root.Chmod(path, 0700) != nil {
		return ErrStorage
	}
	i, e := s.root.Lstat(path)
	if e != nil || !privateDirectory(i) {
		return ErrStorage
	}
	return nil
}
func (s *Store) layout(m Metadata) error {
	for base, allowed := range map[string]map[string]bool{m.ID: {"current.json": true, revisionName(m.Revision): true}, m.ID + "/" + revisionName(m.Revision): {"workspace": true, "manifest.json": true}} {
		f, e := s.root.Open(base)
		if e != nil {
			return ErrStorage
		}
		entries, e := f.ReadDir(3)
		f.Close()
		if e != nil && e != io.EOF || len(entries) != 2 {
			return ErrStorage
		}
		for _, entry := range entries {
			if !allowed[entry.Name()] {
				return ErrStorage
			}
		}
	}
	return nil
}

func (s *Store) recoverCreation() error {
	// This exact exclusive pre-intent file is never a published Environment.
	if i, e := s.root.Lstat("create-next.json"); e == nil {
		if !regular(i) {
			return ErrStorage
		}
		if s.root.Remove("create-next.json") != nil {
			return ErrCleanup
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return ErrStorage
	}
	var p intent
	e := readJSON(s.root, "creation.json", &p)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || p.Version != 1 || p.Revision != 0 || !envID.MatchString(p.EnvironmentID) {
		return ErrStorage
	}
	if _, e := s.root.Lstat(p.EnvironmentID + "/current.json"); errors.Is(e, os.ErrNotExist) {
		if s.root.RemoveAll(p.EnvironmentID) != nil {
			return ErrCleanup
		}
	} else {
		m, e := s.meta(p.EnvironmentID)
		if e != nil {
			return e
		}
		if _, e = s.workspace(context.Background(), m); e != nil {
			return e
		}
	}
	if s.root.Remove("creation.json") != nil {
		return ErrCleanup
	}
	return nil
}
func (s *Store) Delete(ctx context.Context, thread string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m, e := s.find(thread)
	if e != nil {
		return e
	}
	if s.busy[m.ID] {
		return ErrBusy
	}
	if _, e = s.workspace(ctx, m); e != nil {
		return e
	}
	if e = s.root.RemoveAll(m.ID); e != nil {
		return ErrCleanup
	}
	return nil
}
