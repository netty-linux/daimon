package routines

import (
	"context"
	"errors"
	"fmt"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type refs struct{}

func (refs) Get(id threads.ID) (threads.Thread, error) {
	return threads.Thread{ID: id, BotID: "bot"}, nil
}

type botRefs struct{}

func (botRefs) Get(id bots.ID) (bots.Bot, error) {
	return bots.Bot{ID: id, Name: "Bot", ProviderID: "fake", Model: "fake", Instructions: "private", Tools: []string{}, PermissionMode: bots.PermissionAsk}, nil
}

type runtimeFake struct {
	mu     sync.Mutex
	status sessions.Status
	calls  []sessions.StartRequest
	err    error
}

func (f *runtimeFake) Get(id sessions.ID) (sessions.Snapshot, error) {
	return sessions.Snapshot{ID: id, Status: sessions.Completed}, nil
}

func (f *runtimeFake) ScheduledStatus(bots.ID) sessions.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}
func (f *runtimeFake) Start(_ context.Context, r sessions.StartRequest) (sessions.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r)
	return sessions.Snapshot{ID: r.SessionID}, f.err
}
func baseInput(id string) Input {
	return Input{ID: id, BotID: "bot", ThreadID: "thread", Title: "Private title", Prompt: "private-prompt", DailyAt: "09:00", Timezone: "UTC", Enabled: true}
}
func setup(t *testing.T) (*Store, *Scheduler, *runtimeFake, time.Time) {
	t.Helper()
	store, e := Open(filepath.Join(t.TempDir(), "routines.json"))
	if e != nil {
		t.Fatal(e)
	}
	runtime := &runtimeFake{}
	s, e := New(store, runtime, refs{}, botRefs{}, false, false)
	if e != nil {
		t.Fatal(e)
	}
	return store, s, runtime, time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
}
func TestPendingApprovalNeverStartsAnotherTurn(t *testing.T) {
	store, s, runtime, now := setup(t)
	ctx := context.Background()
	if e := s.Create(ctx, baseInput("routine"), now); e != nil {
		t.Fatal(e)
	}
	runtime.status = sessions.WaitingApproval
	if e := s.Tick(ctx, now.Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	views, _ := s.List()
	if len(runtime.calls) != 0 || views[0].State != "waiting_approval" {
		t.Fatal("approval bypass")
	}
	items, _ := store.List()
	if !items[0].NextAt.Equal(now.Add(time.Hour)) {
		t.Fatal("lost pending slot")
	}
	runtime.status = ""
	if e := s.Tick(ctx, now.Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if len(runtime.calls) != 1 || runtime.calls[0].ScheduledBotID != "bot" || runtime.calls[0].Message != "private-prompt" || runtime.calls[0].EnableCreateFile || runtime.calls[0].EnableReplaceFile {
		t.Fatal("wrong authority")
	}
	s.Tick(ctx, now.Add(time.Hour))
	if len(runtime.calls) != 1 {
		t.Fatal("duplicate")
	}
}
func TestLimitsPersistenceAndRestartSkip(t *testing.T) {
	store, s, _, now := setup(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if e := s.Create(ctx, baseInput(fmt.Sprintf("routine-%d", i)), now); e != nil {
			t.Fatal(e)
		}
	}
	if !errors.Is(s.Create(ctx, baseInput("extra"), now), ErrLimit) {
		t.Fatal("per bot limit")
	}
	if e := s.Enable(ctx, "routine-0", false, now); e != nil {
		t.Fatal(e)
	}
	if e := s.Create(ctx, baseInput("extra"), now); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(store.path)
	if e != nil {
		t.Fatal(e)
	}
	if e := reopened.reset(ctx, now.Add(2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	items, _ := reopened.List()
	for _, r := range items {
		if !r.NextAt.After(now.Add(2 * time.Hour)) {
			t.Fatal("catchup")
		}
	}
	for i := len(items); i < 32; i++ {
		in := baseInput(fmt.Sprintf("routine-%d", i+10))
		in.BotID = fmt.Sprintf("bot-%d", i)
		if e := reopened.Create(ctx, in, now); e != nil {
			t.Fatal(e)
		}
	}
	in := baseInput("overflow")
	in.Enabled = false
	if !errors.Is(reopened.Create(ctx, in, now), ErrLimit) {
		t.Fatal("total limit")
	}
}
func TestIntervalSurvivesSchedulerRestartAndConcurrentTicks(t *testing.T) {
	store, s, runtime, now := setup(t)
	ctx := context.Background()
	s.Create(ctx, baseInput("one"), now)
	s.Create(ctx, baseInput("two"), now)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.Tick(ctx, now.Add(time.Hour)); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if len(runtime.calls) != 1 {
		t.Fatal("parallel/interval")
	}
	s2, e := New(store, runtime, refs{}, botRefs{}, false, false)
	if e != nil {
		t.Fatal(e)
	}
	s2.Tick(ctx, now.Add(time.Hour+time.Minute))
	if len(runtime.calls) != 1 {
		t.Fatal("restart interval")
	}
	s2.Tick(ctx, now.Add(time.Hour+15*time.Minute))
	if len(runtime.calls) != 2 {
		t.Fatal("second slot")
	}
}
func TestFailureConsumedCancellationAndPrivacy(t *testing.T) {
	store, s, runtime, now := setup(t)
	ctx := context.Background()
	s.Create(ctx, baseInput("one"), now)
	runtime.err = errors.New("private-secret")
	s.Tick(ctx, now.Add(time.Hour))
	s.Tick(ctx, now.Add(2*time.Hour))
	if len(runtime.calls) != 1 {
		t.Fatal("failure retry")
	}
	views, _ := s.List()
	if views[0].State != "failed" || strings.Contains(fmt.Sprint(views), "private-prompt") {
		t.Fatal("leak")
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if !errors.Is(store.Enable(c, "one", false, now), context.Canceled) {
		t.Fatal("cancel")
	}
	reopened, _ := Open(store.path)
	items, _ := reopened.List()
	if !items[0].Enabled {
		t.Fatal("canceled mutation")
	}
}
func TestCorruptionAndScheduleValidation(t *testing.T) {
	store, _, _, now := setup(t)
	for _, change := range []func(*Input){func(i *Input) { i.DailyAt = "9:00" }, func(i *Input) { i.Timezone = "Local" }, func(i *Input) { i.Prompt = " " }} {
		in := baseInput("bad")
		change(&in)
		if Validate(in) == nil {
			t.Fatal("invalid accepted")
		}
	}
	for _, data := range []string{`{"version":1,"version":1,"routines":[]}`, `{"version":3,"routines":[]}`, `{"version":1,"routines":null}`, `{"version":1,"routines":[],"secret":"x"}`} {
		os.WriteFile(store.path, []byte(data), 0600)
		if _, e := Open(store.path); e == nil {
			t.Fatal("corrupt accepted")
		}
	}
	_ = now
}

// Architecture regression: adding a CUA import, process spawning or an external
// harness call to production scheduling fails this test, even if a fake passes.
func TestSchedulerCannotCallCUAHarnesses(t *testing.T) {
	files, e := filepath.Glob("*.go")
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, e := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if e != nil {
			t.Fatal(e)
		}
		for _, in := range f.Imports {
			p := strings.Trim(in.Path.Value, `"`)
			if p == "os/exec" || strings.Contains(p, "cua") || strings.Contains(p, "/computer") || strings.Contains(p, "/mcp") || strings.Contains(p, "/sandbox") {
				t.Fatalf("external execution surface: %s", p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				for _, name := range []string{"agentStart", "routineAdd", "persistentAgentCreate", "persistentAgentSend"} {
					if strings.EqualFold(id.Name, name) {
						t.Fatalf("forbidden harness call %s", id.Name)
					}
				}
			}
			return true
		})
	}
}
