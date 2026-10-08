package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/netty-linux/daimon/internal/threads"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, dir
}
func message(id, session string, role Role) Message {
	return Message{ID: ID(id), ThreadID: "thread", Role: role, Content: " exact\n<text> é ", CreatedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC), SessionID: session}
}
func TestAppendReopenOrderOwnership(t *testing.T) {
	s, dir := newTestStore(t)
	ctx := context.Background()
	items, err := s.List(ctx, "thread")
	if err != nil || len(items) != 0 {
		t.Fatal("missing is empty")
	}
	user := message("message-a", "session-a", User)
	assistant := message("message-b", "session-a", Assistant)
	if err = s.Append(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, assistant); err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, user); !errors.Is(err, ErrDuplicate) {
		t.Fatal("duplicate")
	}
	if err = s.Append(ctx, message("message-c", "session-a", User)); !errors.Is(err, ErrDuplicate) {
		t.Fatal("duplicate session identity")
	}
	again, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	items, err = again.List(ctx, "thread")
	if err != nil || len(items) != 2 || items[0].Sequence != 1 || items[1].Sequence != 2 || items[1].Content != assistant.Content {
		t.Fatal("exact persistent order")
	}
	items[0].Content = "mutation"
	items, err = s.List(ctx, "thread")
	if err != nil || items[0].Content != user.Content {
		t.Fatal("snapshot alias")
	}
}
func TestValidationBoundaries(t *testing.T) {
	for _, role := range []Role{User, Assistant} {
		m := message("message", "session", role)
		limit := MaxUserBytes
		if role == Assistant {
			limit = MaxAssistantBytes
		}
		m.Content = strings.Repeat("é", limit/2)
		if Validate(m) != nil {
			t.Fatal("exact bytes")
		}
		m.Content += "a"
		if !errors.Is(Validate(m), ErrInvalid) {
			t.Fatal("byte bound")
		}
	}
	for _, change := range []func(*Message){func(m *Message) { m.Role = "tool" }, func(m *Message) { m.ID = "../secret" }, func(m *Message) { m.Content = " \n" }, func(m *Message) { m.Content = string([]byte{0xff}) }, func(m *Message) { m.CreatedAt = time.Time{} }, func(m *Message) { m.CreatedAt = m.CreatedAt.In(time.FixedZone("offset", 3600)) }} {
		m := message("message", "session", User)
		change(&m)
		if Validate(m) == nil {
			t.Fatal("invalid accepted")
		}
	}
	s, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(s.Append(ctx, message("message", "session", User)), context.Canceled) {
		t.Fatal("context")
	}
	if err := s.Append(context.Background(), message("message", "session", Assistant)); !errors.Is(err, ErrInvalid) {
		t.Fatal("assistant without user")
	}
}
func TestStrictCorruptionPreserved(t *testing.T) {
	s, dir := newTestStore(t)
	ctx := context.Background()
	m := message("message", "session", User)
	m.Sequence = 1
	data, _ := json.Marshal(envelope{Version, "thread", []Message{m}})
	for _, bad := range []string{
		strings.Replace(string(data), `"version":1`, `"version":2`, 1),
		strings.Replace(string(data), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(data), `"version":1`, `"Version":1`, 1),
		strings.Replace(string(data), `"sequence":1`, `"sequence":2`, 1),
		strings.Replace(string(data), `"session_id":"session"`, `"unknown":"session"`, 1),
		`{"version":1,"thread_id":"thread","messages":null}`, string(data) + `{}`, "{",
		strings.Replace(string(data), `"content":" exact\n\u003ctext\u003e é "`, `"content":"\ud800"`, 1),
	} {
		name := filepath.Join(dir, "thread.json")
		if err := os.WriteFile(name, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.List(ctx, "thread"); err == nil {
			t.Fatal("corrupt history accepted")
		}
		if err := s.Append(ctx, message("next", "next-session", User)); err == nil {
			t.Fatal("corrupt history overwritten")
		}
		retained, _ := os.ReadFile(name)
		if string(retained) != bad {
			t.Fatal("corruption modified")
		}
	}
}
func TestFailedCommitPreservesFileAndCleansTemporary(t *testing.T) {
	s, dir := newTestStore(t)
	ctx := context.Background()
	if err := s.Append(ctx, message("message", "session", User)); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(filepath.Join(dir, "thread.json"))
	s.rename = func(string, string) error { return errors.New("private-path-secret") }
	err := s.Append(ctx, message("assistant", "session", Assistant))
	if !errors.Is(err, ErrStore) || strings.Contains(err.Error(), "private") {
		t.Fatal("commit error")
	}
	retained, _ := os.ReadFile(filepath.Join(dir, "thread.json"))
	if string(old) != string(retained) {
		t.Fatal("valid file changed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("temporary leaked")
	}
}
func TestConcurrentThreadsAndReaders(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for _, id := range []threads.ID{"one", "two", "three", "four"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 12; i++ {
				m := message(IDText(i), IDText(i), User)
				m.ThreadID = id
				if err := s.Append(ctx, m); err != nil {
					t.Error(err)
				}
				if _, err := s.List(ctx, id); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	for _, id := range []threads.ID{"one", "two", "three", "four"} {
		items, err := s.List(ctx, id)
		if err != nil || len(items) != 12 {
			t.Fatal("lost append")
		}
	}
}
func IDText(i int) string { return "message-" + string(rune('a'+i)) }

func TestCountAndEncodedFileLimitsPreserveHistory(t *testing.T) {
	for _, mode := range []string{"count", "encoded-bytes"} {
		t.Run(mode, func(t *testing.T) {
			s, dir := newTestStore(t)
			items := []Message{}
			count := MaxMessages
			if mode == "encoded-bytes" {
				count = 42
			}
			for i := 0; i < count; i++ {
				m := message(fmt.Sprintf("message-%d", i), fmt.Sprintf("session-%d", i/2), User)
				if mode == "count" {
					m.SessionID = fmt.Sprintf("session-%d", i)
				}
				if mode == "encoded-bytes" {
					if i%2 == 1 {
						m.Role = Assistant
					}
					m.Content = strings.Repeat("\x01", MaxUserBytes)
					if m.Role == Assistant {
						m.Content = strings.Repeat("\x01", MaxAssistantBytes)
					}
				}
				m.Sequence = uint64(i + 1)
				items = append(items, m)
			}
			if mode == "encoded-bytes" {
				// Keep the existing encoded transcript below 16 MiB; the next
				// maximal assistant crosses it due to JSON control-character escapes.
				items = items[:19]
			}
			data, err := json.Marshal(envelope{Version, "thread", items})
			if err != nil || len(data) > MaxFileBytes {
				t.Fatal("fixture bound")
			}
			name := filepath.Join(dir, "thread.json")
			if err = os.WriteFile(name, data, 0600); err != nil {
				t.Fatal(err)
			}
			m := message("new-message", "new-session", User)
			if mode == "encoded-bytes" {
				m.Role = Assistant
				m.SessionID = items[len(items)-1].SessionID
				m.Content = strings.Repeat("\x01", MaxAssistantBytes)
			}
			if err = s.Append(context.Background(), m); !errors.Is(err, ErrLimit) {
				t.Fatalf("limit: %v", err)
			}
			after, err := os.ReadFile(name)
			if err != nil || string(after) != string(data) {
				t.Fatal("valid history changed")
			}
		})
	}
}

func TestReadDuringAppendAndCleanupFailure(t *testing.T) {
	s, dir := newTestStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, err := s.List(ctx, "thread"); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		if err := s.Append(ctx, message(fmt.Sprintf("item-%d", i), fmt.Sprintf("run-%d", i), User)); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	s.rename = func(temp, _ string) error {
		if err := os.Remove(filepath.Join(dir, temp)); err != nil {
			t.Fatal(err)
		}
		return errors.New("private-commit-failure")
	}
	err := s.Append(ctx, message("new", "new", User))
	if !errors.Is(err, ErrCleanup) || strings.Contains(err.Error(), "private") {
		t.Fatal("cleanup error hidden or leaked")
	}
	items, err := s.List(ctx, "thread")
	if err != nil || len(items) != 20 {
		t.Fatal("failed cleanup changed transcript")
	}
}
