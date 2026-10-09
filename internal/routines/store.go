// Package routines schedules ordinary DAIMON Sessions; it grants no authority.
package routines

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/providers"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // IANA schedules work without an installed host timezone DB.
	"unicode/utf8"
)

var ErrInvalid = errors.New("routines: invalid configuration")
var ErrStore = errors.New("routines: storage failure")
var ErrLimit = errors.New("routines: capacity")
var ErrNotFound = errors.New("routines: not found")

const MaxRoutines = 32
const MaxActivePerBot = 4
const MaxBytes = 2 * 1024 * 1024

type Input struct {
	ID       string `json:"id"`
	BotID    string `json:"bot_id"`
	ThreadID string `json:"thread_id"`
	Title    string `json:"title"`
	Prompt   string `json:"prompt"`
	DailyAt  string `json:"daily_at"`
	Timezone string `json:"timezone"`
	Enabled  bool   `json:"enabled"`
}
type Routine struct {
	Input
	NextAt        time.Time `json:"next_at"`
	LastAttemptAt time.Time `json:"last_attempt_at"`
}
type envelope struct {
	Version  int       `json:"version"`
	Routines []Routine `json:"routines"`
}
type Store struct {
	mu   sync.Mutex
	path string
}

func Validate(in Input) error {
	for _, id := range []string{in.ID, in.BotID, in.ThreadID} {
		if providers.ValidateID(providers.ID(id)) != nil {
			return ErrInvalid
		}
	}
	if !utf8.ValidString(in.Title) || strings.TrimSpace(in.Title) == "" || len(in.Title) > 256 || !utf8.ValidString(in.Prompt) || strings.TrimSpace(in.Prompt) == "" || len(in.Prompt) > 32768 {
		return ErrInvalid
	}
	t, e := time.Parse("15:04", in.DailyAt)
	if e != nil || t.Format("15:04") != in.DailyAt {
		return ErrInvalid
	}
	if in.Timezone == "" || len(in.Timezone) > 128 || in.Timezone == "Local" {
		return ErrInvalid
	}
	if _, e := time.LoadLocation(in.Timezone); e != nil {
		return ErrInvalid
	}
	return nil
}
func next(in Input, now time.Time) time.Time {
	loc, _ := time.LoadLocation(in.Timezone)
	local := now.In(loc)
	clock, _ := time.Parse("15:04", in.DailyAt)
	at := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
	if !at.After(now) {
		at = time.Date(local.Year(), local.Month(), local.Day()+1, clock.Hour(), clock.Minute(), 0, 0, loc)
	}
	return at.UTC()
}
func Open(path string) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrInvalid
	}
	p, e := os.Lstat(filepath.Dir(path))
	if e != nil || !p.IsDir() || p.Mode()&os.ModeSymlink != 0 {
		return nil, ErrStore
	}
	s := &Store{path: path}
	if _, e = s.load(); e != nil {
		return nil, e
	}
	return s, nil
}
func target(path string) error {
	i, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || !i.Mode().IsRegular() {
		return ErrStore
	}
	return nil
}

// Reject duplicates, null, aliases and unknown keys before decoding. This is a
// private flat schema, not a generic JSON recovery mechanism.
func strict(data []byte) bool {
	if !validEscapes(data) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 4 {
			return false
		}
		t, e := d.Token()
		if e != nil || t == nil {
			return false
		}
		del, ok := t.(json.Delim)
		if !ok {
			return true
		}
		if del == '[' {
			for d.More() {
				if !walk(depth + 1) {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim(']')
		}
		if del != '{' {
			return false
		}
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			key, ok := k.(string)
			if e != nil || !ok || seen[key] {
				return false
			}
			seen[key] = true
			if depth == 0 {
				if key != "version" && key != "routines" {
					return false
				}
			} else {
				switch key {
				case "id", "bot_id", "thread_id", "title", "prompt", "daily_at", "timezone", "enabled", "next_at", "last_attempt_at":
				default:
					return false
				}
			}
			if !walk(depth + 1) {
				return false
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return false
		}
		if depth == 0 {
			return len(seen) == 2
		}
		return len(seen) == 10
	}
	if !utf8.Valid(data) || !walk(0) {
		return false
	}
	_, e := d.Token()
	return e == io.EOF
}

func validEscapes(data []byte) bool {
	quoted := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		v, e := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
		if v >= 0xd800 && v <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !quoted
}
func DecodeInput(data []byte) (Input, error) {
	var in Input
	if !utf8.Valid(data) || !validEscapes(data) || json.Unmarshal(data, &in) != nil || Validate(in) != nil {
		return Input{}, ErrInvalid
	}
	return in, nil
}
func check(items []Routine) error {
	if len(items) > MaxRoutines {
		return ErrLimit
	}
	seen := map[string]bool{}
	active := map[string]int{}
	for _, r := range items {
		if Validate(r.Input) != nil || r.NextAt.IsZero() || r.NextAt.Location() != time.UTC || r.LastAttemptAt.Location() != time.UTC || seen[r.ID] {
			return ErrStore
		}
		seen[r.ID] = true
		if r.Enabled {
			active[r.BotID]++
			if active[r.BotID] > MaxActivePerBot {
				return ErrLimit
			}
		}
	}
	return nil
}
func (s *Store) load() ([]Routine, error) {
	if target(s.path) != nil {
		return nil, ErrStore
	}
	f, e := os.Open(s.path)
	if errors.Is(e, os.ErrNotExist) {
		return []Routine{}, nil
	}
	if e != nil {
		return nil, ErrStore
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if e != nil || len(data) > MaxBytes || !strict(data) {
		return nil, ErrStore
	}
	var v envelope
	if json.Unmarshal(data, &v) != nil || v.Version != 1 || v.Routines == nil {
		return nil, ErrStore
	}
	if e := check(v.Routines); e != nil {
		return nil, e
	}
	sort.Slice(v.Routines, func(i, j int) bool { return v.Routines[i].ID < v.Routines[j].ID })
	return v.Routines, nil
}
func (s *Store) save(ctx context.Context, items []Routine) (err error) {
	if e := check(items); e != nil {
		return e
	}
	data, e := json.Marshal(envelope{1, items})
	if e != nil || len(data) > MaxBytes {
		return ErrLimit
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if target(s.path) != nil {
		return ErrStore
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".routines-*")
	if e != nil {
		return ErrStore
	}
	name := f.Name()
	defer func() {
		if name != "" {
			_ = f.Close()
			if e := os.Remove(name); e != nil {
				err = ErrStore
			}
		}
	}()
	if f.Chmod(0600) != nil {
		return ErrStore
	}
	if _, e = f.Write(data); e != nil {
		return ErrStore
	}
	if f.Sync() != nil || f.Close() != nil {
		return ErrStore
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if target(s.path) != nil || os.Rename(name, s.path) != nil {
		return ErrStore
	}
	name = ""
	return nil
}
func (s *Store) List() ([]Routine, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.load() }
func (s *Store) Create(ctx context.Context, in Input, now time.Time) error {
	if Validate(in) != nil || now.IsZero() {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, e := s.load()
	if e != nil {
		return e
	}
	for _, r := range items {
		if r.ID == in.ID {
			return ErrInvalid
		}
	}
	return s.save(ctx, append(items, Routine{Input: in, NextAt: next(in, now)}))
}
func (s *Store) Enable(ctx context.Context, id string, enabled bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, e := s.load()
	if e != nil {
		return e
	}
	for i := range items {
		if items[i].ID == id {
			items[i].Enabled = enabled
			items[i].NextAt = next(items[i].Input, now)
			return s.save(ctx, items)
		}
	}
	return ErrNotFound
}
func (s *Store) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, e := s.load()
	if e != nil {
		return e
	}
	for i := range items {
		if items[i].ID == id {
			return s.save(ctx, append(items[:i], items[i+1:]...))
		}
	}
	return ErrNotFound
}

// Consume persists the next slot before admission; errors never silently retry
// a possibly admitted turn. Busy routines remain due until the Bot is idle.
func (s *Store) consume(ctx context.Context, r Routine, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, e := s.load()
	if e != nil {
		return e
	}
	for i := range items {
		if items[i].ID == r.ID && items[i].Enabled && items[i].NextAt.Equal(r.NextAt) {
			items[i].NextAt = next(items[i].Input, now)
			items[i].LastAttemptAt = now.UTC()
			return s.save(ctx, items)
		}
	}
	return ErrNotFound
}
func (s *Store) reset(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, e := s.load()
	if e != nil {
		return e
	}
	changed := false
	for i := range items {
		if !items[i].NextAt.After(now) {
			items[i].NextAt = next(items[i].Input, now)
			changed = true
		}
	}
	if changed {
		return s.save(ctx, items)
	}
	return nil
}
