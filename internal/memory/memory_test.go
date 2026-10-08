package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func input(id string) Input {
	return Input{ID: ID(id), Scope: Global, Kind: Note, Content: " exact durable context\n", Tags: []string{"go", "local"}}
}
func record(id string, scope Scope, scopeID, content string) Memory {
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	return Memory{ID(id), scope, scopeID, Note, content, []string{"go"}, now, now, Provenance{"manual"}}
}
func store(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "memory.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestStructuralValidation(t *testing.T) {
	good := record("memory", Global, "", "UTF-8 exact \n é 🌱")
	if Validate(good) != nil {
		t.Fatal("valid")
	}
	cases := map[string]func(*Memory){"id": func(m *Memory) { m.ID = "../private" }, "scope": func(m *Memory) { m.Scope = "workspace" }, "global-id": func(m *Memory) { m.ScopeID = "bot" }, "bot-id": func(m *Memory) { m.Scope = Bot; m.ScopeID = "Upper" }, "kind": func(m *Memory) { m.Kind = "system" }, "blank": func(m *Memory) { m.Content = " \n" }, "utf8": func(m *Memory) { m.Content = string([]byte{0xff}) }, "content-limit": func(m *Memory) { m.Content = strings.Repeat("x", MaxContentBytes+1) }, "duplicates": func(m *Memory) { m.Tags = []string{"go", "go"} }, "tag-count": func(m *Memory) {
		for i := 0; i < MaxTags; i++ {
			m.Tags = append(m.Tags, fmt.Sprintf("tag-%d", i))
		}
	}, "tag-length": func(m *Memory) { m.Tags = []string{strings.Repeat("x", 65)} }, "wildcard": func(m *Memory) { m.Tags = []string{"*"} }, "time": func(m *Memory) { m.CreatedAt = time.Time{} }, "time-order": func(m *Memory) { m.UpdatedAt = m.CreatedAt.Add(-time.Second) }, "timezone": func(m *Memory) { m.UpdatedAt = m.UpdatedAt.In(time.FixedZone("offset", 3600)) }, "provenance": func(m *Memory) { m.Provenance.SourceType = "learned" }}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := Clone(good)
			mutate(&m)
			if !errors.Is(Validate(m), ErrInvalid) {
				t.Fatal("accepted")
			}
		})
	}
	good.Content = strings.Repeat("x", MaxContentBytes)
	if Validate(good) != nil {
		t.Fatal("inclusive limit")
	}
	for _, scope := range []Scope{Bot, Thread} {
		good.Scope, good.ScopeID = scope, "missing-reference"
		if Validate(good) != nil {
			t.Fatal("structural validation resolves existence")
		}
	}
}
func TestCRUDReopenImmutableAndCopies(t *testing.T) {
	s := store(t)
	ctx := t.Context()
	i := input("memory-a")
	created, err := s.Create(ctx, i)
	if err != nil {
		t.Fatal(err)
	}
	if created.Content != i.Content || created.Provenance.SourceType != "manual" {
		t.Fatal("exact/manual")
	}
	i.Tags[0] = "mutated"
	created.Tags[0] = "mutated"
	got, err := s.Get(ctx, "memory-a")
	if err != nil || got.Tags[0] != "go" {
		t.Fatal("alias", err)
	}
	if _, err := s.Create(ctx, input("memory-a")); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	s.now = func() time.Time { return got.UpdatedAt }
	edit := input("memory-a")
	edit.Content = " next exact content "
	edit.Kind = Preference
	edit.Tags = []string{"zeta", "alpha"}
	updated, err := s.Update(ctx, edit)
	if err != nil || !updated.CreatedAt.Equal(got.CreatedAt) || !updated.UpdatedAt.After(got.UpdatedAt) || updated.Tags[0] != "alpha" {
		t.Fatal("update", err)
	}
	edit.Scope, edit.ScopeID = Bot, "bot"
	if _, err := s.Update(ctx, edit); !errors.Is(err, ErrImmutable) {
		t.Fatal(err)
	}
	edit = input("unknown")
	if _, err := s.Update(ctx, edit); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	reopened, err := NewStore(filepath.Join(s.root.Name(), s.name))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.Get(ctx, "memory-a")
	if err != nil || persisted.Content != updated.Content {
		t.Fatal("restart", err)
	}
	if err := s.Delete(ctx, "memory-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "memory-a"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	all, err := s.List(ctx)
	if err != nil || len(all) != 0 {
		t.Fatal(err)
	}
}
func TestStrictJSONAndCorruption(t *testing.T) {
	m := record("memory", Global, "", "private-text")
	valid, _ := json.Marshal(envelope{Version, []Memory{m}})
	bad := []string{`{"version":1,"version":1,"memories":[]}`, `{"Version":1,"memories":[]}`, `{"version":1,"memories":null}`, `{"version":1,"memories":[],"env":{}}`, string(valid) + `{}`, strings.Replace(string(valid), "private-text", `\ud800`, 1), strings.Replace(string(valid), `"manual"`, `"learned"`, 1), strings.Replace(string(valid), `"scope_id":"",`, "", 1), `not-json`}
	for _, raw := range bad {
		if _, err := decode([]byte(raw)); err == nil {
			t.Fatal("corruption accepted")
		}
	}
	if _, err := decode([]byte(`{"version":2,"memories":[]}`)); !errors.Is(err, ErrVersion) {
		t.Fatal(err)
	}
	duplicate, _ := json.Marshal(envelope{Version, []Memory{m, m}})
	if _, err := decode(duplicate); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
	s := store(t)
	if _, err := s.Create(t.Context(), input("before")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.root.Name(), s.name)
	if err := os.WriteFile(path, []byte("corrupt-private"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(t.Context()); !errors.Is(err, ErrFormat) || strings.Contains(err.Error(), "private") {
		t.Fatal("hidden corruption", err)
	}
	if _, err := s.Create(t.Context(), input("after")); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"id":"m","scope":"global","scope_id":"","kind":"note","content":"x"}`, `{"id":"m","scope":"global","scope_id":"","kind":"note","content":"é 🌱","tags":[]}`} {
		if _, err := DecodeInput([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{"id":"m","scope":"global","kind":"note","content":"x"}`, `{"id":"m","scope":"global","scope_id":"","kind":"note","content":"\ud800"}`, `{"id":"m","scope":"global","scope_id":"","kind":"note","content":"x","created_at":"forged"}`} {
		if _, err := DecodeInput([]byte(raw)); err == nil {
			t.Fatal("bad API input")
		}
	}
}
func TestLimitsAtomicFailureAndCancellation(t *testing.T) {
	s := store(t)
	original, err := s.Create(t.Context(), input("before"))
	if err != nil {
		t.Fatal(err)
	}
	s.rename = func(string, string) error { return errors.New("private-file-path") }
	if _, err := s.Update(t.Context(), input("before")); !errors.Is(err, ErrStore) || strings.Contains(err.Error(), "private-file-path") {
		t.Fatal(err)
	}
	got, _ := s.Get(t.Context(), "before")
	if !got.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatal("failed commit modified target")
	}
	entries, err := os.ReadDir(s.root.Name())
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary leaked", err)
	}
	s.remove = func(string) error { return errors.New("cleanup-private") }
	if _, err := s.Update(t.Context(), input("before")); !errors.Is(err, ErrCleanup) {
		t.Fatal(err)
	}
	s.remove = s.root.Remove
	s.rename = s.root.Rename
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Create(ctx, input("cancel")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	records := make([]Memory, MaxRecords)
	for i := range records {
		records[i] = record(fmt.Sprintf("m-%04d", i), Global, "", "small")
	}
	if err := s.save(t.Context(), records); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(t.Context(), input("overflow")); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	big := make([]Memory, 400)
	for i := range big {
		big[i] = record(fmt.Sprintf("large-%04d", i), Global, "", strings.Repeat("\x01", MaxContentBytes))
	}
	if err := s.save(t.Context(), big); !errors.Is(err, ErrLimit) {
		t.Fatal("encoded limit", err)
	}
	if err := os.Truncate(filepath.Join(s.root.Name(), s.name), MaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(t.Context()); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}
func TestStoreConcurrentSnapshots(t *testing.T) {
	s := store(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("m-%d", i)
			if _, err := s.Create(t.Context(), input(id)); err != nil {
				t.Error(err)
				return
			}
			if _, err := s.List(t.Context()); err != nil {
				t.Error(err)
			}
			if _, err := s.Update(t.Context(), input(id)); err != nil {
				t.Error(err)
			}
			if err := s.Delete(t.Context(), ID(id)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	items, err := s.List(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatal(err)
	}
}
func TestRetrievalScopesRankingTagsAndWholeRecordBudget(t *testing.T) {
	items := []Memory{record("global", Global, "", "go useful"), record("bot", Bot, "bot-a", "go useful"), record("thread", Thread, "thread-a", "other"), record("foreign-bot", Bot, "bot-b", "go"), record("foreign-thread", Thread, "thread-b", "go"), record("lexical", Global, "", "go useful twice")}
	q := Query{BotID: "bot-a", ThreadID: "thread-a", Text: "go useful", Limit: 16}
	found, err := Retrieve(items, q)
	if err != nil || len(found) != 4 || found[0].ID != "thread" || found[1].ID != "bot" || found[2].ID != "global" {
		t.Fatal("scope/order", err)
	}
	found[0].Tags[0] = "external"
	if items[2].Tags[0] != "go" {
		t.Fatal("alias")
	}
	for n := 0; n < 5; n++ {
		again, _ := Retrieve(items, q)
		if again[2].ID != "global" {
			t.Fatal("unstable tie")
		}
	}
	q.Tags = []string{"missing"}
	found, err = Retrieve(items, q)
	if err != nil || len(found) != 0 {
		t.Fatal("tag filter")
	}
	q.Tags = []string{"go"}
	q.Limit = 1
	found, _ = Retrieve(items, q)
	if len(found) != 1 {
		t.Fatal("limit")
	}
	q.Tags = nil
	q.Limit = 16
	items[2].Content = strings.Repeat("x", 5000)
	text, err := Context(items, q, 1000)
	if err != nil || len(text) > 1000 || strings.Contains(text, strings.Repeat("x", 30)) {
		t.Fatal("partial record", err)
	}
	if !strings.Contains(text, "DAIMON MEMORY CONTEXT") || !strings.Contains(text, `"id":"bot"`) {
		t.Fatal("framing/skipping")
	}
	text, err = Context(items, q, 0)
	if err != nil || text != "" {
		t.Fatal("zero available bytes")
	}
	items[5].Content = "go useful additional"
	q.Text = "additional"
	found, _ = Retrieve(items, q)
	if found[2].ID != "lexical" {
		t.Fatal("lexical")
	}
	items[5].Content = items[0].Content
	items[5].UpdatedAt = items[0].UpdatedAt.Add(time.Second)
	q.Text = ""
	found, _ = Retrieve(items, q)
	if found[2].ID != "lexical" {
		t.Fatal("recency")
	}
	empty, err := Context(nil, q, MaxContextBytes)
	if err != nil || empty != "" {
		t.Fatal("zero records compatibility")
	}
}
