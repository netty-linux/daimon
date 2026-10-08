package conversations

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/threads"
	"hash/fnv"
	"io"
	"os"
	"sync"
	"unicode/utf8"
)

type envelope struct {
	Version  int        `json:"version"`
	ThreadID threads.ID `json:"thread_id"`
	Messages []Message  `json:"messages"`
}

// Fixed lock stripes bound coordination state. Only colliding Threads serialize;
// no Manager state lock is held across persistence. One process owns the directory.
type Store struct {
	root   *os.Root
	locks  [64]sync.RWMutex
	rename func(string, string) error
}

func NewStore(directory string) (*Store, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, &StoreError{Kind: ErrStore}
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, &StoreError{Kind: ErrStore, Cause: err}
	}
	s := &Store{root: root}
	s.rename = root.Rename
	return s, nil
}
func (s *Store) Close() error { return s.root.Close() }
func (s *Store) lock(id threads.ID) *sync.RWMutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return &s.locks[h.Sum32()%64]
}
func (s *Store) List(ctx context.Context, id threads.ID) ([]Message, error) {
	if providers.ValidateID(providers.ID(id)) != nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lock := s.lock(id)
	lock.RLock()
	defer lock.RUnlock()
	return s.load(ctx, id)
}
func (s *Store) Append(ctx context.Context, m Message) error {
	if Validate(m) != nil || m.Sequence != 0 {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	lock := s.lock(m.ThreadID)
	lock.Lock()
	defer lock.Unlock()
	items, err := s.load(ctx, m.ThreadID)
	if err != nil {
		return err
	}
	for _, old := range items {
		if old.ID == m.ID || (old.SessionID == m.SessionID && old.Role == m.Role) {
			return ErrDuplicate
		}
	}
	if len(items) >= MaxMessages {
		return ErrLimit
	}
	if m.Role == Assistant {
		if len(items) == 0 || items[len(items)-1].Role != User || items[len(items)-1].SessionID != m.SessionID {
			return ErrInvalid
		}
	}
	m.Sequence = uint64(len(items)) + 1
	items = append(items, m)
	data, err := json.Marshal(envelope{Version, m.ThreadID, items})
	if err != nil {
		return &StoreError{Kind: ErrFormat}
	}
	data = append(data, '\n')
	if len(data) > MaxFileBytes {
		return ErrLimit
	}
	return s.save(ctx, string(m.ThreadID)+".json", data)
}
func (s *Store) target(name string) (os.FileInfo, error) {
	info, err := s.root.Lstat(name)
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
func (s *Store) load(ctx context.Context, id threads.ID) ([]Message, error) {
	name := string(id) + ".json"
	info, err := s.target(name)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return []Message{}, nil
	}
	if info.Size() > MaxFileBytes {
		return nil, ErrLimit
	}
	f, err := s.root.Open(name)
	if err != nil {
		return nil, &StoreError{Kind: ErrStore, Cause: err}
	}
	opened, statErr := f.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = f.Close()
		return nil, &StoreError{Kind: ErrStore}
	}
	data, readErr := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return nil, &StoreError{Kind: ErrStore, Cause: errors.Join(readErr, closeErr)}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, ErrLimit
	}
	return decode(data, id)
}
func decode(data []byte, id threads.ID) ([]Message, error) {
	if !utf8.Valid(data) || !validStringEscapes(data) || !strictJSON(data) {
		return nil, ErrFormat
	}
	var wire struct {
		Version  *int       `json:"version"`
		ThreadID threads.ID `json:"thread_id"`
		Messages *[]Message `json:"messages"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&wire) != nil || d.Decode(new(any)) != io.EOF || wire.Version == nil || wire.Messages == nil || wire.ThreadID != id {
		return nil, ErrFormat
	}
	if *wire.Version != Version {
		return nil, ErrVersion
	}
	items := *wire.Messages
	if len(items) > MaxMessages {
		return nil, ErrLimit
	}
	ids := map[ID]bool{}
	turns := map[string]map[Role]bool{}
	for i, m := range items {
		if Validate(m) != nil || m.ThreadID != id || m.Sequence != uint64(i)+1 || ids[m.ID] {
			return nil, ErrFormat
		}
		ids[m.ID] = true
		if turns[m.SessionID] == nil {
			turns[m.SessionID] = map[Role]bool{}
		}
		if turns[m.SessionID][m.Role] {
			return nil, ErrFormat
		}
		turns[m.SessionID][m.Role] = true
		if m.Role == Assistant && (i == 0 || items[i-1].Role != User || items[i-1].SessionID != m.SessionID) {
			return nil, ErrFormat
		}
	}
	return append([]Message{}, items...), nil
}
func strictJSON(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 4 {
			return false
		}
		tok, err := d.Token()
		if err != nil || tok == nil {
			return false
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			allowed := map[string]bool{}
			if depth == 0 {
				for _, k := range []string{"version", "thread_id", "messages"} {
					allowed[k] = true
				}
			} else if depth == 2 {
				for _, k := range []string{"id", "thread_id", "sequence", "role", "content", "created_at", "session_id"} {
					allowed[k] = true
				}
			} else {
				return false
			}
			seen := map[string]bool{}
			for d.More() {
				keyTok, e := d.Token()
				key, ok := keyTok.(string)
				if e != nil || !ok || !allowed[key] || seen[key] {
					return false
				}
				seen[key] = true
				if !walk(depth + 1) {
					return false
				}
			}
			if len(seen) != len(allowed) {
				return false
			}
			end, e := d.Token()
			return e == nil && end == json.Delim('}')
		case '[':
			if depth != 1 {
				return false
			}
			for d.More() {
				if !walk(depth + 1) {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim(']')
		}
		return false
	}
	return walk(0) && d.Decode(new(any)) == io.EOF
}
func (s *Store) save(ctx context.Context, name string, data []byte) (err error) {
	if _, err = s.target(name); err != nil {
		return err
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return &StoreError{Kind: ErrStore}
	}
	temp := ".conversation-" + hex.EncodeToString(nonce)
	f, err := s.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return &StoreError{Kind: ErrStore, Cause: err}
	}
	closed, committed := false, false
	defer func() {
		if !closed {
			if closeErr := f.Close(); closeErr != nil {
				err = &StoreError{Kind: ErrStore, Cause: errors.Join(err, closeErr)}
			}
		}
		if !committed {
			if cleanupErr := s.root.Remove(temp); cleanupErr != nil {
				err = &StoreError{Kind: ErrCleanup, Cause: errors.Join(err, cleanupErr)}
			}
		}
	}()
	if err = f.Chmod(0600); err != nil {
		return &StoreError{Kind: ErrStore, Cause: err}
	}
	for offset := 0; offset < len(data); {
		if err = ctx.Err(); err != nil {
			return err
		}
		end := offset + 32*1024
		if end > len(data) {
			end = len(data)
		}
		n, writeErr := f.Write(data[offset:end])
		if writeErr != nil || n != end-offset {
			return &StoreError{Kind: ErrStore, Cause: errors.Join(writeErr, io.ErrShortWrite)}
		}
		offset = end
	}
	if err = f.Sync(); err != nil {
		return &StoreError{Kind: ErrStore, Cause: err}
	}
	err = f.Close()
	closed = true
	if err != nil {
		return &StoreError{Kind: ErrStore, Cause: err}
	}
	if _, err = s.target(name); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.rename(temp, name); err != nil {
		return &StoreError{Kind: ErrStore, Cause: err}
	}
	committed = true
	return nil
}
