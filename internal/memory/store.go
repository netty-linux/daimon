package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type envelope struct {
	Version  int      `json:"version"`
	Memories []Memory `json:"memories"`
}

// One Store owns a controlled private directory; no cross-process writer protection.
// Operations reread disk, so corruption is never hidden by a successful old cache.
type Store struct {
	mu     sync.RWMutex
	root   *os.Root
	name   string
	now    func() time.Time
	rename func(string, string) error
	remove func(string) error
}

func NewStore(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrStore
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, &StoreError{ErrStore, err}
	}
	parent, err := os.Lstat(filepath.Dir(abs))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return nil, ErrStore
	}
	root, err := os.OpenRoot(filepath.Dir(abs))
	if err != nil {
		return nil, &StoreError{ErrStore, err}
	}
	s := &Store{root: root, name: filepath.Base(abs), now: func() time.Time { return time.Now().UTC() }, rename: root.Rename, remove: root.Remove}
	if _, err = s.load(context.Background()); err != nil {
		root.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { s.mu.Lock(); defer s.mu.Unlock(); return s.root.Close() }
func (s *Store) List(ctx context.Context) ([]Memory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.load(ctx)
}
func (s *Store) Get(ctx context.Context, id ID) (Memory, error) {
	if ValidateID(string(id)) != nil {
		return Memory{}, ErrInvalid
	}
	items, err := s.List(ctx)
	if err != nil {
		return Memory{}, err
	}
	for _, m := range items {
		if m.ID == id {
			return Clone(m), nil
		}
	}
	return Memory{}, ErrNotFound
}
func (s *Store) Create(ctx context.Context, input Input) (Memory, error) {
	input.Tags = append([]string{}, input.Tags...)
	sort.Strings(input.Tags)
	if ValidateInput(input) != nil {
		return Memory{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.load(ctx)
	if err != nil {
		return Memory{}, err
	}
	for _, m := range items {
		if m.ID == input.ID {
			return Memory{}, ErrDuplicate
		}
	}
	if len(items) >= MaxRecords {
		return Memory{}, ErrLimit
	}
	now := s.now()
	m := Memory{input.ID, input.Scope, input.ScopeID, input.Kind, input.Content, input.Tags, now, now, Provenance{"manual"}}
	if Validate(m) != nil {
		return Memory{}, ErrInvalid
	}
	if err = s.save(ctx, append(items, m)); err != nil {
		return Memory{}, err
	}
	return Clone(m), nil
}
func (s *Store) Update(ctx context.Context, input Input) (Memory, error) {
	input.Tags = append([]string{}, input.Tags...)
	sort.Strings(input.Tags)
	if ValidateInput(input) != nil {
		return Memory{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.load(ctx)
	if err != nil {
		return Memory{}, err
	}
	for i, old := range items {
		if old.ID != input.ID {
			continue
		}
		if old.Scope != input.Scope || old.ScopeID != input.ScopeID {
			return Memory{}, ErrImmutable
		}
		next := old
		next.Kind, next.Content, next.Tags = input.Kind, input.Content, input.Tags
		next.UpdatedAt = s.now()
		if !next.UpdatedAt.After(old.UpdatedAt) {
			next.UpdatedAt = old.UpdatedAt.Add(time.Nanosecond)
		}
		if Validate(next) != nil {
			return Memory{}, ErrInvalid
		}
		items[i] = next
		if err = s.save(ctx, items); err != nil {
			return Memory{}, err
		}
		return Clone(next), nil
	}
	return Memory{}, ErrNotFound
}
func (s *Store) Delete(ctx context.Context, id ID) error {
	if ValidateID(string(id)) != nil {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.load(ctx)
	if err != nil {
		return err
	}
	for i, m := range items {
		if m.ID == id {
			return s.save(ctx, append(items[:i], items[i+1:]...))
		}
	}
	return ErrNotFound
}
func (s *Store) target() (os.FileInfo, error) {
	info, err := s.root.Lstat(s.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &StoreError{ErrStore, err}
	}
	if !info.Mode().IsRegular() {
		return nil, ErrStore
	}
	return info, nil
}
func (s *Store) load(ctx context.Context) ([]Memory, error) {
	if err := ctx.Err(); err != nil {
		return nil, &StoreError{ErrStore, err}
	}
	info, err := s.target()
	if err != nil {
		return nil, err
	}
	if info == nil {
		return []Memory{}, nil
	}
	if info.Size() > MaxFileBytes {
		return nil, ErrLimit
	}
	f, err := s.root.Open(s.name)
	if err != nil {
		return nil, &StoreError{ErrStore, err}
	}
	opened, e := f.Stat()
	if e != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		f.Close()
		return nil, ErrStore
	}
	data, e := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return nil, &StoreError{ErrStore, errors.Join(e, closeErr)}
	}
	if len(data) > MaxFileBytes {
		return nil, ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, &StoreError{ErrStore, err}
	}
	return decode(data)
}
func decode(data []byte) ([]Memory, error) {
	if !strictJSON(data) || !exactObject(data, []string{"version", "memories"}) {
		return nil, ErrFormat
	}
	var wire struct {
		Version  int               `json:"version"`
		Memories []json.RawMessage `json:"memories"`
	}
	if json.Unmarshal(data, &wire) != nil {
		return nil, ErrFormat
	}
	if wire.Version != Version {
		return nil, ErrVersion
	}
	if wire.Memories == nil {
		return nil, ErrFormat
	}
	if len(wire.Memories) > MaxRecords {
		return nil, ErrLimit
	}
	items := make([]Memory, 0, len(wire.Memories))
	seen := map[ID]bool{}
	for _, raw := range wire.Memories {
		if !exactObject(raw, []string{"id", "scope", "scope_id", "kind", "content", "tags", "created_at", "updated_at", "provenance"}) {
			return nil, ErrFormat
		}
		var m Memory
		if json.Unmarshal(raw, &m) != nil || Validate(m) != nil || seen[m.ID] {
			return nil, ErrFormat
		}
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		if !exactObject(fields["provenance"], []string{"source_type"}) {
			return nil, ErrFormat
		}
		seen[m.ID] = true
		sort.Strings(m.Tags)
		items = append(items, Clone(m))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}
func (s *Store) save(ctx context.Context, items []Memory) (err error) {
	if len(items) > MaxRecords {
		return ErrLimit
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, err := json.Marshal(envelope{Version, items})
	if err != nil {
		return ErrFormat
	}
	data = append(data, '\n')
	if len(data) > MaxFileBytes {
		return ErrLimit
	}
	if _, err = s.target(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return &StoreError{ErrStore, err}
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return &StoreError{ErrStore, err}
	}
	name := ".daimon-memory-" + hex.EncodeToString(nonce) + ".tmp"
	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return &StoreError{ErrStore, err}
	}
	committed := false
	defer func() {
		if !committed {
			if cleanup := s.remove(name); cleanup != nil {
				err = &StoreError{ErrCleanup, errors.Join(err, cleanup)}
			}
		}
	}()
	writeErr := func() (err error) {
		defer func() { err = errors.Join(err, f.Close()) }()
		if err = f.Chmod(0600); err != nil {
			return err
		}
		for offset := 0; offset < len(data); {
			if err = ctx.Err(); err != nil {
				return err
			}
			end := min(offset+64*1024, len(data))
			n, e := f.Write(data[offset:end])
			if e != nil {
				return e
			}
			if n != end-offset {
				return io.ErrShortWrite
			}
			offset = end
		}
		return f.Sync()
	}()
	if writeErr != nil {
		return &StoreError{ErrStore, writeErr}
	}
	if _, err = s.target(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return &StoreError{ErrStore, err}
	}
	if err = s.rename(name, s.name); err != nil {
		return &StoreError{ErrStore, err}
	}
	committed = true
	return nil
}
