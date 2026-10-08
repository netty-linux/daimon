package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/memory"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func memoryServer(t *testing.T) (*fixture, *memory.Store, string) {
	t.Helper()
	f := setup(t, finalModel, 64)
	path := filepath.Join(t.TempDir(), "memory.json")
	store, err := memory.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f.server.deps.Memory = store
	return f, store, path
}
func memoryInput(id memory.ID, scope memory.Scope, target string) memory.Input {
	return memory.Input{ID: id, Scope: scope, ScopeID: target, Kind: memory.Preference, Content: "chosen-memory-marker", Tags: []string{"coding"}}
}
func TestMemoryHTTPCRUDScopesAndOwnership(t *testing.T) {
	f, _, _ := memoryServer(t)
	for _, in := range []memory.Input{memoryInput("global", memory.Global, ""), memoryInput("bot-mem", memory.Bot, "bot"), memoryInput("thread-mem", memory.Thread, "thread")} {
		w := request(f.server, "POST", "/api/v1/memories", in)
		expect(t, w, 201)
		var record memory.Memory
		if json.Unmarshal(w.Body.Bytes(), &record) != nil || record.CreatedAt.IsZero() || record.Provenance.SourceType != "manual" {
			t.Fatal("missing server-owned provenance")
		}
		expect(t, request(f.server, "GET", "/api/v1/memories/"+string(in.ID), nil), 200)
	}
	expect(t, request(f.server, "DELETE", "/api/v1/bots/bot", nil), 409)
	expect(t, request(f.server, "DELETE", "/api/v1/threads/thread", nil), 409)
	w := request(f.server, "GET", "/api/v1/memories?limit=1", nil)
	expect(t, w, 200)
	var page struct {
		Memories []memory.Memory `json:"memories"`
		After    string          `json:"next_after"`
		More     bool            `json:"has_more"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Memories) != 1 || !page.More || page.After != "bot-mem" {
		t.Fatal(w.Body.String())
	}
	expect(t, request(f.server, "GET", "/api/v1/memories?after=bot-mem&limit=1", nil), 200)
	for _, scope := range []string{"global", "bot&scope_id=bot", "thread&scope_id=thread"} {
		w = request(f.server, "GET", "/api/v1/memories?scope="+scope, nil)
		expect(t, w, 200)
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		if len(page.Memories) != 1 {
			t.Fatal("scope filter failed")
		}
	}
	in := memoryInput("global", memory.Global, "")
	in.Content = "updated exact content  "
	expect(t, request(f.server, "PUT", "/api/v1/memories/global", in), 200)
	expect(t, request(f.server, "PUT", "/api/v1/memories/other", in), 400)
	in.Scope = memory.Bot
	in.ScopeID = "bot"
	expect(t, request(f.server, "PUT", "/api/v1/memories/global", in), 409)
	expect(t, request(f.server, "POST", "/api/v1/memories", memoryInput("missing", memory.Bot, "absent")), 404)
	for _, id := range []string{"global", "bot-mem", "thread-mem"} {
		expect(t, request(f.server, "DELETE", "/api/v1/memories/"+id, nil), 204)
	}
	expect(t, request(f.server, "DELETE", "/api/v1/threads/thread", nil), 204)
	expect(t, request(f.server, "DELETE", "/api/v1/bots/bot", nil), 204)
	expect(t, request(f.server, "GET", "/api/v1/memories/global", nil), 404)
}
func TestMemoryHTTPStrictInputAndSafeFailures(t *testing.T) {
	f, _, path := memoryServer(t)
	raw := `{"id":"test","scope":"global","scope_id":"","kind":"note","content":"chosen-memory-marker","tags":[]}`
	for _, bad := range []string{strings.Replace(raw, `"tags":[]`, `"created_at":"2026-10-07T12:00:00Z"`, 1), strings.Replace(raw, `"tags":[]`, `"provenance":{"source_type":"manual"}`, 1), strings.Replace(raw, `"content":"chosen-memory-marker"`, `"content":"\ud800"`, 1), strings.Replace(raw, `"tags":[]`, `"tags":null`, 1), strings.Replace(raw, `"scope_id":"",`, "", 1), strings.Replace(raw, `"kind":"note"`, `"kind":"note","kind":"fact"`, 1)} {
		w := request(f.server, "POST", "/api/v1/memories", bad)
		expect(t, w, 400)
		if strings.Contains(w.Body.String(), "chosen-memory-marker") {
			t.Fatal("error leaked input")
		}
	}
	for _, query := range []string{"scope=bot", "scope_id=bot", "scope=global&scope_id=bot", "limit=0", "limit=101", "scope=global&scope=bot", "search=chosen-memory-marker"} {
		expect(t, request(f.server, "GET", "/api/v1/memories?"+query, nil), 400)
	}
	if err := os.WriteFile(path, []byte("chosen-memory-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	w := request(f.server, "GET", "/api/v1/memories", nil)
	expect(t, w, 500)
	if strings.Contains(w.Body.String(), "chosen-memory-marker") {
		t.Fatal("corruption leaked")
	}
	expect(t, request(f.server, "POST", "/api/v1/memories", raw), 500)
}
func TestMemoryHTTPReferenceCreationDeletionRace(t *testing.T) {
	f, store, _ := memoryServer(t)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("bot-race-%d", i)
		bot := f.bot
		bot.ID = bots.ID(id)
		if err := f.bots.Create(bot); err != nil {
			t.Fatal(err)
		}
		var create, remove int
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			create = request(f.server, "POST", "/api/v1/memories", memoryInput(memory.ID(id), memory.Bot, id)).Code
		}()
		go func() { defer wg.Done(); remove = request(f.server, "DELETE", "/api/v1/bots/"+id, nil).Code }()
		wg.Wait()
		if !((create == 201 && remove == 409) || (create == 404 && remove == 204)) {
			t.Fatalf("non-serial reference result %d/%d", create, remove)
		}
		if create == 201 {
			if _, err := f.bots.Get(bots.ID(id)); err != nil {
				t.Fatal("orphan memory")
			}
			if err := store.Delete(context.Background(), memory.ID(id)); err != nil {
				t.Fatal(err)
			}
		}
	}
}
