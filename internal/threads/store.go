package threads

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	StoreVersion  = 1
	MaxThreads    = 256
	MaxStoreBytes = 2 * 1024 * 1024
)

// Store has no cache or shared mutable Thread values. Operations reread the file.
// Use sequentially with one writer in a controlled directory. There are no locks
// across processes, automatic directories, HOME lookup or runtime integration.
type Store struct{ path string }

type envelope struct {
	Version int      `json:"version"`
	Threads []Thread `json:"threads"`
}

// NewStore requires an explicit filename and existing parent directory. It checks
// existing contents but does not create a missing file. Missing means empty store.
func NewStore(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, &StoreError{Kind: ErrStore}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, &StoreError{Kind: ErrStore, Cause: err}
	}
	parent, err := os.Stat(filepath.Dir(abs))
	if err != nil || !parent.IsDir() {
		return nil, &StoreError{Kind: ErrStore, Cause: err}
	}
	s := &Store{path: abs}
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// List returns a sorted, owned snapshot. Thread values contain no mutable slices or maps.
func (s *Store) List() ([]Thread, error) { return s.load() }

func (s *Store) Get(id ID) (Thread, error) {
	if err := validateID(id); err != nil {
		return Thread{}, err
	}
	threads, err := s.load()
	if err != nil {
		return Thread{}, err
	}
	for _, b := range threads {
		if b.ID == id {
			return b, nil
		}
	}
	return Thread{}, &StoreError{Kind: ErrNotFound}
}

func (s *Store) Create(b Thread) error {
	if err := Validate(b); err != nil {
		return err
	}
	threads, err := s.load()
	if err != nil {
		return err
	}
	for _, stored := range threads {
		if stored.ID == b.ID {
			return &StoreError{Kind: ErrDuplicate}
		}
	}
	if len(threads) >= MaxThreads {
		return &StoreError{Kind: ErrLimit}
	}
	return s.save(append(threads, b))
}

func (s *Store) Update(b Thread) error {
	if err := Validate(b); err != nil {
		return err
	}
	threads, err := s.load()
	if err != nil {
		return err
	}
	for i := range threads {
		if threads[i].ID == b.ID {
			original := threads[i]
			if b.BotID != original.BotID || b.Workspace != original.Workspace || !b.CreatedAt.Equal(original.CreatedAt) {
				return &StoreError{Kind: ErrImmutable}
			}
			if b.UpdatedAt.Before(original.UpdatedAt) {
				return &ValidationError{Field: "updated_at"}
			}
			threads[i] = b
			return s.save(threads)
		}
	}
	return &StoreError{Kind: ErrNotFound}
}

func (s *Store) Delete(id ID) error {
	if err := validateID(id); err != nil {
		return err
	}
	threads, err := s.load()
	if err != nil {
		return err
	}
	for i := range threads {
		if threads[i].ID == id {
			return s.save(append(threads[:i], threads[i+1:]...))
		}
	}
	return &StoreError{Kind: ErrNotFound}
}

func sortThreads(threads []Thread) {
	sort.Slice(threads, func(i, j int) bool { return threads[i].ID < threads[j].ID })
}

// checkTarget rejects observed symlinks and non-regular targets. It is not
// hostile-writer isolation; the directory and its ancestors are caller-controlled.
func checkTarget(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &StoreError{Kind: ErrStore, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return nil, &StoreError{Kind: ErrStore}
	}
	return info, nil
}

func (s *Store) load() ([]Thread, error) {
	if s == nil || s.path == "" {
		return nil, &StoreError{Kind: ErrStore}
	}
	info, err := checkTarget(s.path)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return []Thread{}, nil
	}
	if info.Size() > MaxStoreBytes {
		return nil, &StoreError{Kind: ErrLimit}
	}
	f, err := os.Open(s.path)
	if err != nil {
		return nil, &StoreError{Kind: ErrStore, Cause: err}
	}
	opened, statErr := f.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		closeErr := f.Close()
		return nil, &StoreError{Kind: ErrStore, Cause: errors.Join(statErr, closeErr)}
	}
	data, readErr := io.ReadAll(io.LimitReader(f, MaxStoreBytes+1))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return nil, &StoreError{Kind: ErrStore, Cause: errors.Join(readErr, closeErr)}
	}
	if len(data) > MaxStoreBytes {
		return nil, &StoreError{Kind: ErrLimit}
	}
	return decode(data)
}

func decode(data []byte) ([]Thread, error) {
	if !utf8.Valid(data) || !uniqueJSON(data) {
		return nil, &StoreError{Kind: ErrFormat}
	}
	var wire struct {
		Version *int      `json:"version"`
		Threads *[]Thread `json:"threads"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&wire) != nil || d.Decode(new(any)) != io.EOF || wire.Version == nil || wire.Threads == nil {
		return nil, &StoreError{Kind: ErrFormat}
	}
	if *wire.Version != StoreVersion {
		return nil, &StoreError{Kind: ErrVersion}
	}
	threads := *wire.Threads
	if len(threads) > MaxThreads {
		return nil, &StoreError{Kind: ErrLimit}
	}
	seen := make(map[ID]bool, len(threads))
	for _, b := range threads {
		if err := Validate(b); err != nil {
			return nil, &StoreError{Kind: ErrFormat, Cause: err}
		}
		if seen[b.ID] {
			return nil, &StoreError{Kind: ErrFormat}
		}
		seen[b.ID] = true
	}
	sortThreads(threads)
	return threads, nil
}

// Reject ambiguous duplicate keys, null, noncanonical key spelling and deep
// documents before strict decoding. Text values are not interpreted as secrets.
func uniqueJSON(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	allowed := map[string]bool{"version": true, "threads": true, "id": true, "bot_id": true, "workspace": true, "title": true, "created_at": true, "updated_at": true}
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 4 {
			return false
		}
		token, err := d.Token()
		if err != nil || token == nil {
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
				keyToken, err := d.Token()
				key, ok := keyToken.(string)
				if err != nil || !ok || !allowed[key] || seen[key] {
					return false
				}
				seen[key] = true
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
		_, err = d.Token()
		return err == nil
	}
	return walk(0)
}

func (s *Store) save(threads []Thread) error {
	if s == nil || s.path == "" {
		return &StoreError{Kind: ErrStore}
	}
	if len(threads) > MaxThreads {
		return &StoreError{Kind: ErrLimit}
	}
	sortThreads(threads)
	data, err := json.MarshalIndent(envelope{Version: StoreVersion, Threads: threads}, "", "  ")
	if err != nil {
		return &StoreError{Kind: ErrFormat, Cause: err}
	}
	data = append(data, '\n')
	if len(data) > MaxStoreBytes {
		return &StoreError{Kind: ErrLimit}
	}
	return atomicWrite(s.path, data, os.Rename)
}

// rename is injected only by private tests to verify failed commit preservation.
// No directory fsync/Windows atomicity or cross-process exclusion is promised.
func atomicWrite(path string, data []byte, rename func(string, string) error) (err error) {
	if _, err := checkTarget(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".daimon-threads-*")
	if err != nil {
		return &StoreError{Kind: ErrStore, Cause: err}
	}
	committed := false
	defer func() {
		if !committed {
			if cleanupErr := os.Remove(f.Name()); cleanupErr != nil {
				err = &StoreError{Kind: ErrCleanup, Cause: errors.Join(err, cleanupErr)}
			}
		}
	}()
	if err := writeTemporary(f, data); err != nil {
		return &StoreError{Kind: ErrStore, Cause: err}
	}
	if _, err := checkTarget(path); err != nil {
		return err
	}
	if err := rename(f.Name(), path); err != nil {
		return &StoreError{Kind: ErrStore, Cause: err}
	}
	committed = true
	return nil
}

func writeTemporary(f *os.File, data []byte) (err error) {
	defer func() { err = errors.Join(err, f.Close()) }()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	n, err := f.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return f.Sync()
}
