package computer

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/tools"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeBackend struct {
	calls            atomic.Int32
	started, release chan struct{}
}

func (f *fakeBackend) ID() BackendID { return CUALocal }
func (f *fakeBackend) Probe(ctx context.Context) (Info, error) {
	return Info{ID: "cua", Backend: CUALocal, Status: "connected", Capabilities: []Capability{{ID: "list_apps", Tool: "mcp__cua__list_apps", Class: Observe, Available: true}}}, ctx.Err()
}
func (f *fakeBackend) Resolve(name string) (Operation, error) { return fakeOperation{f, name}, nil }

type fakeOperation struct {
	backend *fakeBackend
	name    string
}

func (f fakeOperation) Definition() Definition {
	return Definition{Name: f.name, Schema: json.RawMessage(`{"type":"object"}`)}
}
func (f fakeOperation) Call(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	f.backend.calls.Add(1)
	if f.backend.started != nil {
		close(f.backend.started)
		<-f.backend.release
	}
	return tools.ToolResult{Content: "observed"}, ctx.Err()
}
func profile() Profile { return Profile{true, CUALocal, "cua"} }
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, end := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(end)
	return ctx
}
func TestProfileAndExactClassification(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"enabled":true,"backend":"cua-local"}`, `{"enabled":true,"enabled":false,"backend":"cua-local","mcp_server_id":"cua"}`, `{"Enabled":true,"backend":"cua-local","mcp_server_id":"cua"}`, `{"enabled":null,"backend":"cua-local","mcp_server_id":"cua"}`, `{"enabled":true,"backend":"cloud","mcp_server_id":"cua"}`} {
		if _, err := DecodeProfile([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
	for _, enabled := range []string{"true", "false"} {
		if _, err := DecodeProfile([]byte(`{"enabled":` + enabled + `,"backend":"cua-local","mcp_server_id":"cua"}`)); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]Class{"list_apps": Observe, "click": Navigate, "type_text": Input, "launch_app": System, "kill_app": Dangerous, "safe_read_click": Dangerous, "future_untrusted": Dangerous} {
		if Classify(name) != want {
			t.Fatal(name)
		}
	}
}
func TestActionsDenyUnsafeOptionsAndShowCompleteTyping(t *testing.T) {
	for _, text := range []string{`\ud800`, `\udfff`, `\ud800x`} {
		if _, err := validateAction("type_text", []byte(`{"pid":1,"window_id":2,"text":"`+text+`"}`)); err == nil {
			t.Fatal("ambiguous Unicode", text)
		}
	}
	for _, text := range []string{`\ud83d\ude00`, `\\ud800`} {
		if _, err := validateAction("type_text", []byte(`{"pid":1,"window_id":2,"text":"`+text+`"}`)); err != nil {
			t.Fatal("valid Unicode", text, err)
		}
	}
	for name, raw := range map[string]string{"get_window_state": `{"pid":1,"window_id":2,"include_screenshot":true}`, "click": `{"pid":1,"window_id":2,"x":3,"y":4,"debug_image_out":"secret.png"}`, "type_text": `{"text":"a"}`, "list_apps": `{"session":"override"}`, "get_accessibility_tree": `{"pid":1,"pid":2,"window_id":3}`} {
		if _, err := validateAction(name, []byte(raw)); !errors.Is(err, ErrArguments) {
			t.Fatal(name, err)
		}
	}
	if _, err := validateAction("click", []byte(`{"pid":1,"window_id":2,"x":3,"y":4}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := validateAction("get_window_state", []byte(`{"pid":1,"window_id":2,"include_screenshot":false}`)); err != nil {
		t.Fatal(err)
	}
	p, err := describeAction("type_text", []byte(`{"element_token":"s1:2","text":"full\n\u001b\u202e<text>"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(p.Preview, "\x1b\u202e") || !strings.Contains(p.Preview, `full\n\x1b\u202e<text>`) {
		t.Fatal("unsafe/incomplete typing preview", p.Preview)
	}
}
func TestExclusiveLeaseConcurrentAdmissionAndStaleTools(t *testing.T) {
	backend := &fakeBackend{}
	m, _ := NewManager(backend)
	ctx := testCtx(t)
	var wins atomic.Int32
	var winner *Binding
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := m.Open(ctx, "run", profile(), []string{"mcp__cua__list_apps"}, false)
			if err == nil {
				wins.Add(1)
				mu.Lock()
				winner = b
				mu.Unlock()
			} else if !errors.Is(err, ErrBusy) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("multiple controllers")
	}
	tool, _ := winner.Tool("mcp__cua__list_apps")
	meta := winner.Metadata()
	meta.CapabilityIDs[0] = "mutated"
	if winner.Metadata().CapabilityIDs[0] != "list_apps" {
		t.Fatal("alias")
	}
	infos, _ := m.Infos(ctx)
	if !infos[0].Busy || infos[0].ControllerSessionID != "run" {
		t.Fatal("ownership")
	}
	if err := winner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(ctx, []byte(`{}`)); !errors.Is(err, ErrClosed) {
		t.Fatal("stale tool")
	}
	next, err := m.Open(ctx, "second", profile(), []string{"mcp__cua__list_apps"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	closedInfo, _ := m.Infos(ctx)
	if closedInfo[0].Status != "unavailable" || closedInfo[0].Capabilities[0].Available {
		t.Fatal("closed manager advertised eligible tools")
	}
	if _, err := next.Tool("mcp__cua__list_apps"); err {
		t.Fatal("closed binding lookup")
	}
	if _, err := m.Open(ctx, "third", profile(), nil, false); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
func TestCloseKeepsLeaseUntilActiveEffectUnwinds(t *testing.T) {
	backend := &fakeBackend{started: make(chan struct{}), release: make(chan struct{})}
	m, _ := NewManager(backend)
	ctx := testCtx(t)
	b, _ := m.Open(ctx, "run", profile(), []string{"mcp__cua__list_apps"}, false)
	tool, _ := b.Tool("mcp__cua__list_apps")
	finished := make(chan error, 1)
	go func() { _, err := tool.Execute(ctx, []byte(`{}`)); finished <- err }()
	<-backend.started
	deadline, end := context.WithTimeout(ctx, 20*time.Millisecond)
	defer end()
	if err := b.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := m.Open(ctx, "second", profile(), nil, false); !errors.Is(err, ErrBusy) {
		t.Fatal("released before effect")
	}
	close(backend.release)
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := m.Open(ctx, "second", profile(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = next.Close(ctx)
}
func TestProfileReadOnlyAndUnknownFailClosed(t *testing.T) {
	m, _ := NewManager(&fakeBackend{})
	ctx := testCtx(t)
	for _, name := range []string{"mcp__cua__type_text", "mcp__cua__future_untrusted", "mcp__cua__get_desktop_state"} {
		if _, err := m.Open(ctx, "run", profile(), []string{name}, true); !errors.Is(err, ErrDenied) {
			t.Fatal(name, err)
		}
	}
	p := profile()
	p.Enabled = false
	if _, err := m.Open(ctx, "run", p, nil, false); err == nil {
		t.Fatal("disabled")
	}
}
