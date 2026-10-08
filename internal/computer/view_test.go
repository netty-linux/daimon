package computer

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMedia struct {
	fail      atomic.Bool
	failClose atomic.Bool
	active    atomic.Int32
	calls     atomic.Int32
	entered   chan struct{}
	release   chan struct{}
}

func (f *fakeMedia) Open(ctx context.Context, r MediaRequest) (MediaSession, error) {
	if f.fail.Load() {
		return nil, ErrMedia
	}
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.release:
		}
	}
	f.active.Add(1)
	return &fakeStream{parent: f, info: MediaInfo{SessionID: "fake-media", Codec: "bgra", Input: r.Input}, ack: make(chan MediaPacket, 1)}, nil
}

type fakeStream struct {
	parent *fakeMedia
	info   MediaInfo
	ack    chan MediaPacket
	once   sync.Once
}

func (s *fakeStream) Info() MediaInfo { return s.info }
func (s *fakeStream) Receive(ctx context.Context) (MediaPacket, error) {
	select {
	case p := <-s.ack:
		return p, nil
	case <-ctx.Done():
		return MediaPacket{}, ctx.Err()
	}
}
func (s *fakeStream) Send(ctx context.Context, raw []byte) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !s.info.Input {
		return nil
	}
	var msg struct {
		Payload struct {
			First  uint64            `json:"first_sequence"`
			Events []json.RawMessage `json:"events"`
		} `json:"payload"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return ErrArguments
	}
	s.parent.calls.Add(1)
	s.ack <- MediaPacket{Data: safeControl("interactive_input_acknowledgement", map[string]any{"session_id": s.info.SessionID, "through_sequence": msg.Payload.First + uint64(len(msg.Payload.Events)) - 1, "delivered": true})}
	return nil
}
func (s *fakeStream) Close(context.Context) error {
	if s.parent.failClose.Load() {
		return ErrMedia
	}
	s.once.Do(func() { s.parent.active.Add(-1) })
	return nil
}
func viewSetup(t *testing.T) (*Manager, *Binding, *fakeBackend, *fakeMedia, *ViewSession, ViewTicket) {
	t.Helper()
	backend := &fakeBackend{}
	m, _ := NewManager(backend)
	f := &fakeMedia{}
	if e := m.ConfigureMedia(f); e != nil {
		t.Fatal(e)
	}
	ctx := testCtx(t)
	b, e := m.Open(ctx, "run", profile(), []string{"mcp__cua__click", "mcp__cua__list_apps"}, false)
	if e != nil {
		t.Fatal(e)
	}
	v, ticket, e := m.OpenView(ctx, "cua", "desktop")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.AttachView("cua", ticket.ID, ticket.Token); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.failClose.Store(false); _ = m.Close(context.Background()) })
	return m, b, backend, f, v, ticket
}

const humanText = `{"type":"interactive_input","payload":{"first_sequence":1,"events":[{"kind":"text_commit","text":"Exact \n😀"}]}}`

func TestViewOnlyTakeReleaseAndOriginalAgentBinding(t *testing.T) {
	m, b, backend, f, _, v := viewSetup(t)
	ctx := testCtx(t)
	if _, e := m.HumanInput(ctx, "cua", v.ID, v.Token, []byte(humanText)); !errors.Is(e, ErrControl) {
		t.Fatal("view granted input", e)
	}
	lease, e := m.TakeControl(ctx, "cua", v.ID, v.Token)
	if e != nil {
		t.Fatal(e)
	}
	if lease.Token == v.Token || lease.Token == "run" {
		t.Fatal("secret scope")
	}
	if m.AttachControl("cua", v.ID, lease.Token) != nil {
		t.Fatal("attach")
	}
	tool, _ := b.Tool("mcp__cua__click")
	if _, e = tool.Execute(ctx, []byte(`{"pid":1,"window_id":2,"x":3,"y":4}`)); !errors.Is(e, ErrHumanControl) {
		t.Fatal(e)
	}
	if backend.calls.Load() != 0 {
		t.Fatal("agent bypass")
	}
	observe, _ := b.Tool("mcp__cua__list_apps")
	if _, e = observe.Execute(ctx, []byte(`{}`)); e != nil {
		t.Fatal("observation blocked", e)
	}
	if _, e = m.HumanInput(ctx, "cua", v.ID, lease.Token, []byte(humanText)); e != nil {
		t.Fatal(e)
	}
	if f.calls.Load() != 1 {
		t.Fatal("input dropped")
	}
	if _, e = m.HumanInput(ctx, "cua", v.ID, lease.Token, []byte(humanText)); !errors.Is(e, ErrArguments) {
		t.Fatal("replay")
	}
	if e = m.ReleaseControl(ctx, "cua", v.ID, lease.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = m.HumanInput(ctx, "cua", v.ID, lease.Token, []byte(humanText)); !errors.Is(e, ErrControl) {
		t.Fatal("stale secret")
	}
	if _, e = tool.Execute(ctx, []byte(`{"pid":1,"window_id":2,"x":3,"y":4}`)); e != nil {
		t.Fatal("agent not restored", e)
	}
	if e = b.Close(ctx); e != nil {
		t.Fatal(e)
	}
	state, _ := m.ViewState(ctx, "cua")
	if state.Owner != "view_only" || state.Viewers != 1 {
		t.Fatal(state)
	}
}
func TestTakeoverWaitsForInFlightAgentInputAndBlocksNewInput(t *testing.T) {
	m, b, backend, _, _, v := viewSetup(t)
	ctx := testCtx(t)
	backend.started = make(chan struct{})
	backend.release = make(chan struct{})
	tool, _ := b.Tool("mcp__cua__click")
	done := make(chan error, 1)
	go func() { _, e := tool.Execute(ctx, []byte(`{"pid":1,"window_id":2,"x":3,"y":4}`)); done <- e }()
	<-backend.started
	taken := make(chan ControlTicket, 1)
	go func() {
		lease, e := m.TakeControl(ctx, "cua", v.ID, v.Token)
		if e != nil {
			t.Error(e)
		}
		taken <- lease
	}()
	waitOwner(t, m, "transitioning")
	select {
	case <-taken:
		t.Fatal("dual input lease")
	default:
	}
	close(backend.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	lease := <-taken
	if lease.Token == "" {
		t.Fatal("missing lease")
	}
	backend.started = nil
	if _, e := tool.Execute(ctx, []byte(`{"pid":1,"window_id":2,"x":3,"y":4}`)); !errors.Is(e, ErrHumanControl) {
		t.Fatal(e)
	}
}
func waitOwner(t *testing.T, m *Manager, owner string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s, _ := m.ViewState(context.Background(), "cua")
		if s.Owner == owner {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("owner did not transition", owner)
}
func TestTwoTabsExactlyOneControllerAndCapacity(t *testing.T) {
	m, _, _, _, _, v := viewSetup(t)
	ctx := testCtx(t)
	_, v2, e := m.OpenView(ctx, "cua", "desktop")
	if e != nil {
		t.Fatal(e)
	}
	m.AttachView("cua", v2.ID, v2.Token)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for _, ticket := range []ViewTicket{v, v2} {
		wg.Add(1)
		go func(v ViewTicket) {
			defer wg.Done()
			_, e := m.TakeControl(ctx, "cua", v.ID, v.Token)
			if e == nil {
				wins.Add(1)
			} else if !errors.Is(e, ErrBusy) {
				t.Error(e)
			}
		}(ticket)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("multiple controllers")
	}
	for i := 2; i < MaxViewers; i++ {
		if _, _, e := m.OpenView(ctx, "cua", "desktop"); e != nil {
			t.Fatal(e)
		}
	}
	if _, _, e := m.OpenView(ctx, "cua", "desktop"); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	if _, e := m.AttachView("cua", v.ID, v.Token); !errors.Is(e, ErrControl) {
		t.Fatal("second socket stole viewer")
	}
}
func TestExpirySessionEndAndShutdownRevokeHumanControl(t *testing.T) {
	for _, mode := range []string{"expiry", "disconnect", "completion", "abort", "close"} {
		t.Run(mode, func(t *testing.T) {
			m, b, _, f, view, v := viewSetup(t)
			ctx := testCtx(t)
			lease, e := m.TakeControl(ctx, "cua", v.ID, v.Token)
			if e != nil {
				t.Fatal(e)
			}
			if m.AttachControl("cua", v.ID, lease.Token) != nil {
				t.Fatal("attach")
			}
			switch mode {
			case "expiry", "disconnect":
				if mode == "disconnect" {
					m.DisconnectControl("cua", v.ID, lease.Token)
				}
				m.mu.Lock()
				m.human.timer.Reset(time.Millisecond)
				m.mu.Unlock()
				waitOwner(t, m, "agent_control")
			case "completion":
				if e = b.Close(ctx); e != nil {
					t.Fatal(e)
				}
				waitOwner(t, m, "view_only")
			case "abort":
				b.cancel()
				if e = b.Close(ctx); e != nil {
					t.Fatal(e)
				}
				waitOwner(t, m, "view_only")
			case "close":
				if e = m.Close(ctx); e != nil {
					t.Fatal(e)
				}
				if f.active.Load() != 0 {
					t.Fatal("media leak")
				}
				select {
				case <-view.done:
				default:
					t.Fatal("viewer not joined")
				}
			}
			if _, e = m.HumanInput(ctx, "cua", v.ID, lease.Token, []byte(humanText)); !errors.Is(e, ErrControl) {
				t.Fatal("stale input", e)
			}
		})
	}
}
func TestMediaFailureDoesNotRemoveActionsAndRevocationFailsClosed(t *testing.T) {
	m, b, backend, f, _, v := viewSetup(t)
	ctx := testCtx(t)
	f.fail.Store(true)
	if _, e := m.TakeControl(ctx, "cua", v.ID, v.Token); e == nil {
		t.Fatal("backend failure accepted")
	}
	state, _ := m.ViewState(ctx, "cua")
	if state.Owner != "agent_control" {
		t.Fatal(state)
	}
	f.fail.Store(false)
	lease, e := m.TakeControl(ctx, "cua", v.ID, v.Token)
	if e != nil {
		t.Fatal(e)
	}
	f.failClose.Store(true)
	if e = m.ReleaseControl(ctx, "cua", v.ID, lease.Token); !errors.Is(e, ErrMedia) {
		t.Fatal(e)
	}
	tool, _ := b.Tool("mcp__cua__click")
	if _, e = tool.Execute(ctx, []byte(`{"pid":1,"window_id":2,"x":3,"y":4}`)); !errors.Is(e, ErrHumanControl) {
		t.Fatal("unsafe resume", e)
	}
	if backend.calls.Load() != 0 {
		t.Fatal("unsafe effect")
	}
}
func TestInputStrictnessNoScriptsClipboardAudioOrAmbiguousText(t *testing.T) {
	for _, event := range []string{`{"kind":"clipboard","text":"secret"}`, `{"kind":"text_commit","text":"\ud800"}`, `{"kind":"text_commit","text":"a","text":"b"}`, `{"kind":"text_commit","text":"a","script":"evil"}`, `{"kind":"pointer","phase":"down","button":null,"x_normalized":0,"y_normalized":0,"modifiers":[]}`, `{"kind":"pointer","phase":"move","button":null,"x_normalized":2,"y_normalized":0,"modifiers":[]}`} {
		raw := `{"type":"interactive_input","payload":{"first_sequence":1,"events":[` + event + `]}}`
		if _, e := ValidateInput([]byte(raw)); e == nil {
			t.Fatal(event)
		}
	}
	if _, e := ValidateInput([]byte(humanText)); e != nil {
		t.Fatal(e)
	}
}

func TestTakeoverAgainstSessionCompletionAndConcurrentRelease(t *testing.T) {
	m, b, _, f, _, v := viewSetup(t)
	ctx := testCtx(t)
	f.entered = make(chan struct{}, 1)
	f.release = make(chan struct{})
	taken := make(chan error, 1)
	go func() { _, e := m.TakeControl(ctx, "cua", v.ID, v.Token); taken <- e }()
	<-f.entered
	closed := make(chan error, 1)
	go func() { closed <- b.Close(ctx) }()
	select {
	case e := <-taken:
		if e == nil {
			t.Fatal("completed binding granted input")
		}
	case <-ctx.Done():
		t.Fatal("takeover blocked completion")
	}
	if e := <-closed; e != nil {
		t.Fatal(e)
	}
	s, _ := m.ViewState(ctx, "cua")
	if s.Owner != "view_only" {
		t.Fatal(s)
	}
}

func TestConcurrentDisconnectReleaseAndShutdownCannotRestoreStaleInput(t *testing.T) {
	m, _, _, _, _, v := viewSetup(t)
	ctx := testCtx(t)
	lease, e := m.TakeControl(ctx, "cua", v.ID, v.Token)
	if e != nil {
		t.Fatal(e)
	}
	m.AttachControl("cua", v.ID, lease.Token)
	var wg sync.WaitGroup
	for _, action := range []func(){func() { m.DisconnectControl("cua", v.ID, lease.Token) }, func() { _ = m.ReleaseControl(ctx, "cua", v.ID, lease.Token) }, func() { _ = m.Close(ctx) }, func() { _, _ = m.HumanInput(ctx, "cua", v.ID, lease.Token, []byte(humanText)) }} {
		wg.Add(1)
		go func(f func()) { defer wg.Done(); f() }(action)
	}
	wg.Wait()
	if _, e := m.HumanInput(ctx, "cua", v.ID, lease.Token, []byte(humanText)); !errors.Is(e, ErrControl) {
		t.Fatal("shutdown input", e)
	}
}

type partialOpenMedia struct{ *fakeMedia }

func (p partialOpenMedia) Open(ctx context.Context, r MediaRequest) (MediaSession, error) {
	s, e := p.fakeMedia.Open(ctx, r)
	if r.Input && e == nil {
		return s, ErrMedia
	}
	return s, e
}
func TestFailedOpenAndFailedCloseRetainHandleAndShutdownError(t *testing.T) {
	m, b, backend, f, _, v := viewSetup(t)
	ctx := testCtx(t)
	m.mu.Lock()
	m.media = partialOpenMedia{f}
	m.mu.Unlock()
	f.failClose.Store(true)
	if _, e := m.TakeControl(ctx, "cua", v.ID, v.Token); !errors.Is(e, ErrMedia) {
		t.Fatal(e)
	}
	state, _ := m.ViewState(ctx, "cua")
	if state.Owner != "transitioning" {
		t.Fatal("unsafe restoration", state)
	}
	tool, _ := b.Tool("mcp__cua__click")
	if _, e := tool.Execute(ctx, []byte(`{"pid":1,"window_id":2,"x":3,"y":4}`)); !errors.Is(e, ErrHumanControl) {
		t.Fatal(e)
	}
	if backend.calls.Load() != 0 {
		t.Fatal("unsafe effect")
	}
	if e := m.StopMedia(ctx); !errors.Is(e, ErrMedia) {
		t.Fatal("cleanup failure hidden", e)
	}
}
