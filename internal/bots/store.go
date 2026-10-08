package bots

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/sandbox"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/providers"
)

const (
	StoreVersion  = 1
	MaxBots       = 128
	MaxStoreBytes = 2 * 1024 * 1024
)

// Store has no cache or shared mutable Bot values. Operations reread the file.
// Use sequentially with one writer in a controlled directory. There are no locks
// across processes, automatic directories, HOME lookup or runtime integration.
type Store struct{ path string }

type envelope struct {
	Version int   `json:"version"`
	Bots    []Bot `json:"bots"`
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

// List returns a sorted, owned snapshot. Tool declaration order is preserved.
func (s *Store) List() ([]Bot, error) { return s.load() }

func (s *Store) Get(id ID) (Bot, error) {
	if err := validateID(id); err != nil {
		return Bot{}, err
	}
	bots, err := s.load()
	if err != nil {
		return Bot{}, err
	}
	for _, b := range bots {
		if b.ID == id {
			return Clone(b), nil
		}
	}
	return Bot{}, &StoreError{Kind: ErrNotFound}
}

func (s *Store) Create(b Bot) error {
	b = Clone(b)
	if err := Validate(b); err != nil {
		return err
	}
	bots, err := s.load()
	if err != nil {
		return err
	}
	for _, stored := range bots {
		if stored.ID == b.ID {
			return &StoreError{Kind: ErrDuplicate}
		}
	}
	if len(bots) >= MaxBots {
		return &StoreError{Kind: ErrLimit}
	}
	return s.save(append(bots, b))
}

func (s *Store) Update(b Bot) error {
	b = Clone(b)
	if err := Validate(b); err != nil {
		return err
	}
	bots, err := s.load()
	if err != nil {
		return err
	}
	for i := range bots {
		if bots[i].ID == b.ID {
			bots[i] = b
			return s.save(bots)
		}
	}
	return &StoreError{Kind: ErrNotFound}
}

func (s *Store) Delete(id ID) error {
	if err := validateID(id); err != nil {
		return err
	}
	bots, err := s.load()
	if err != nil {
		return err
	}
	for i := range bots {
		if bots[i].ID == id {
			return s.save(append(bots[:i], bots[i+1:]...))
		}
	}
	return &StoreError{Kind: ErrNotFound}
}

func validateID(id ID) error {
	if providers.ValidateID(providers.ID(id)) != nil {
		return &ValidationError{Field: "id"}
	}
	return nil
}

func sortBots(bots []Bot) { sort.Slice(bots, func(i, j int) bool { return bots[i].ID < bots[j].ID }) }

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

func (s *Store) load() ([]Bot, error) {
	if s == nil || s.path == "" {
		return nil, &StoreError{Kind: ErrStore}
	}
	info, err := checkTarget(s.path)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return []Bot{}, nil
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

func decode(data []byte) ([]Bot, error) {
	if !utf8.Valid(data) || !uniqueJSON(data) {
		return nil, &StoreError{Kind: ErrFormat}
	}
	var wire struct {
		Version *int   `json:"version"`
		Bots    *[]Bot `json:"bots"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&wire) != nil || d.Decode(new(any)) != io.EOF || wire.Version == nil || wire.Bots == nil {
		return nil, &StoreError{Kind: ErrFormat}
	}
	if *wire.Version != StoreVersion {
		return nil, &StoreError{Kind: ErrVersion}
	}
	bots := *wire.Bots
	var document struct {
		Bots []map[string]json.RawMessage `json:"bots"`
	}
	if json.Unmarshal(data, &document) != nil {
		return nil, &StoreError{Kind: ErrFormat}
	}
	for _, rawBot := range document.Bots {
		if profile, ok := rawBot["sandbox_profile"]; ok {
			if _, err := sandbox.DecodeProfile(profile); err != nil {
				return nil, &StoreError{Kind: ErrFormat}
			}
		}
		if profile, ok := rawBot["computer_profile"]; ok {
			if !validComputerJSON(profile) {
				return nil, &StoreError{Kind: ErrFormat}
			}
		}
	}
	if len(bots) > MaxBots {
		return nil, &StoreError{Kind: ErrLimit}
	}
	seen := make(map[ID]bool, len(bots))
	for _, b := range bots {
		if err := Validate(b); err != nil {
			return nil, &StoreError{Kind: ErrFormat, Cause: err}
		}
		if seen[b.ID] {
			return nil, &StoreError{Kind: ErrFormat}
		}
		seen[b.ID] = true
	}
	sortBots(bots)
	return bots, nil
}

// Reject ambiguous duplicate keys, null, noncanonical key spelling and deep
// documents before strict decoding. Text values are not interpreted as secrets.
func uniqueJSON(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	allowed := map[string]bool{"version": true, "bots": true, "id": true, "name": true, "description": true, "instructions": true, "provider_id": true, "model": true, "tools": true, "permission_mode": true, "placement": true, "sandbox_profile": true, "image": true, "runtime": true, "browser": true, "resources": true, "network": true, "computer_profile": true, "enabled": true, "backend": true, "mcp_server_id": true}
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

func (s *Store) save(bots []Bot) error {
	if s == nil || s.path == "" {
		return &StoreError{Kind: ErrStore}
	}
	if len(bots) > MaxBots {
		return &StoreError{Kind: ErrLimit}
	}
	sortBots(bots)
	data, err := json.MarshalIndent(envelope{Version: StoreVersion, Bots: bots}, "", "  ")
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
	f, err := os.CreateTemp(filepath.Dir(path), ".daimon-bots-*")
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

// Optional compatible extension; present profiles require every exact field.
func validComputerJSON(raw json.RawMessage) bool {
	_, err := computer.DecodeProfile(raw)
	return err == nil
}
