package computer

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

const ViewAttachTimeout = 10 * time.Second
const HumanGrace = 3 * time.Second
const HumanLeaseTTL = 15 * time.Second

type ViewState struct {
	Available        bool   `json:"available"`
	Owner            string `json:"owner"`
	ControllerViewID string `json:"controller_view_id,omitempty"`
	AgentSessionID   string `json:"agent_session_id,omitempty"`
	Viewers          int    `json:"viewers"`
}
type ViewTicket struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}
type ControlTicket struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}
type ViewSession struct {
	manager             *Manager
	id, token, computer string
	media               MediaSession
	life                context.Context
	cancel              context.CancelFunc
	attached            bool // guarded by Manager.mu
	attachTimer         *time.Timer
	once                sync.Once
	done                chan struct{}
	ready               chan struct{}
	closeErr            error
}
type humanLease struct {
	view     *ViewSession
	binding  *Binding
	token    string
	media    MediaSession
	state    string
	attached bool
	timer    *time.Timer
	next     uint64
	rateAt   time.Time
	rate     int
	cancel   context.CancelFunc
	life     context.Context
}

func capability() (string, error) {
	var b [24]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", ErrMedia
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func secretEqual(a, b string) bool {
	return len(a) == 32 && len(b) == 32 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func (m *Manager) acquireGate(ctx context.Context) error {
	select {
	case m.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) releaseGate() { <-m.gate }

// ConfigureMedia is composition-only, before any Agent binding or viewer exists.
func (m *Manager) ConfigureMedia(p MediaProvider) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.owner != nil || len(m.views) != 0 || m.media != nil || p == nil || m.backend == nil {
		return ErrConfig
	}
	m.media = p
	return nil
}
func (m *Manager) ViewState(ctx context.Context, id string) (ViewState, error) {
	if m == nil {
		return ViewState{}, ErrUnavailable
	}
	m.mu.Lock()
	backend := m.backend
	m.mu.Unlock()
	if backend == nil {
		return ViewState{}, ErrUnavailable
	}
	if strings.HasPrefix(id, "sandbox-") {
		scoped, ok := backend.(interface{ Namespace() string })
		if !ok || scoped.Namespace() != "sandbox" {
			return ViewState{}, ErrUnavailable
		}
	}
	info, e := backend.Probe(ctx)
	if e != nil {
		return ViewState{}, e
	}
	if info.ID != id {
		return ViewState{}, ErrUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := ViewState{Available: m.media != nil && !m.closed, Owner: "view_only", Viewers: len(m.views)}
	if m.owner != nil {
		s.Owner = "agent_control"
		s.AgentSessionID = m.owner.session
	}
	if m.human != nil {
		s.Owner = m.human.state
		s.ControllerViewID = m.human.view.id
	}
	return s, nil
}
func (m *Manager) OpenView(ctx context.Context, id, target string) (*ViewSession, ViewTicket, error) {
	var empty ViewTicket
	if target != "desktop" {
		return nil, empty, ErrArguments
	}
	if _, e := m.ViewState(ctx, id); e != nil {
		return nil, empty, e
	}
	token, e := capability()
	if e != nil {
		return nil, empty, e
	}
	vid, e := capability()
	if e != nil {
		return nil, empty, e
	}
	life, cancel := context.WithCancel(context.Background())
	v := &ViewSession{manager: m, id: "view-" + vid, token: token, computer: id, life: life, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{})}
	m.mu.Lock()
	if m.closed || m.media == nil {
		m.mu.Unlock()
		cancel()
		return nil, empty, ErrMedia
	}
	if len(m.views) >= MaxViewers {
		m.mu.Unlock()
		cancel()
		return nil, empty, ErrCapacity
	}
	provider := m.media
	m.views[v.id] = v
	m.mu.Unlock()
	openCtx, end := context.WithTimeout(ctx, 5*time.Second)
	stop := context.AfterFunc(life, end)
	session, e := provider.Open(openCtx, MediaRequest{Target: target})
	stop()
	end()
	m.mu.Lock()
	v.media = session
	close(v.ready)
	if e == nil && (m.closed || life.Err() != nil || session == nil) {
		e = ErrClosed
	}
	if e != nil {
		m.mu.Unlock()
		_ = v.Close(context.Background())
		return nil, empty, ErrMedia
	}
	v.attachTimer = time.AfterFunc(ViewAttachTimeout, func() {
		closeCtx, end := context.WithTimeout(context.Background(), 10*time.Second)
		defer end()
		_ = v.Close(closeCtx)
	})
	m.mu.Unlock()
	return v, ViewTicket{ID: v.id, Token: token, ExpiresAt: time.Now().UTC().Add(ViewAttachTimeout)}, nil
}
func (m *Manager) AttachView(id, vid, token string) (*ViewSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.views[vid]
	if m.closed || v == nil || v.media == nil || v.computer != id || !secretEqual(v.token, token) || v.attached || v.life.Err() != nil {
		return nil, ErrControl
	}
	v.attached = true
	v.attachTimer.Stop()
	return v, nil
}
func (v *ViewSession) Info() MediaInfo { return v.media.Info() }
func (v *ViewSession) Receive(ctx context.Context) (MediaPacket, error) {
	life, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(v.life, cancel)
	defer func() { stop(); cancel() }()
	return v.media.Receive(life)
}
func (v *ViewSession) Keyframe(ctx context.Context) error {
	return v.media.Send(ctx, safeControl("request_keyframe", map[string]string{"session_id": v.media.Info().SessionID}))
}
func (v *ViewSession) Close(ctx context.Context) error {
	v.cancel()
	v.once.Do(func() {
		go func() {
			<-v.ready
			v.manager.mu.Lock()
			if v.attachTimer != nil {
				v.attachTimer.Stop()
			}
			v.manager.mu.Unlock()
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			v.closeErr = v.manager.releaseViewControl(closeCtx, v)
			if v.media != nil {
				if e := v.media.Close(closeCtx); e != nil {
					v.closeErr = e
				}
			}
			v.manager.mu.Lock()
			// An unconfirmed close consumes capacity and remains visible to
			// shutdown. Do not silently forget a remote media resource.
			if v.closeErr == nil {
				delete(v.manager.views, v.id)
			}
			v.manager.mu.Unlock()
			close(v.done)
		}()
	})
	select {
	case <-v.done:
		return v.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) TakeControl(ctx context.Context, id, vid, viewToken string) (ControlTicket, error) {
	m.mu.Lock()
	v := m.views[vid]
	if m.closed || v == nil || v.computer != id || !secretEqual(v.token, viewToken) || !v.attached || v.life.Err() != nil || m.owner == nil || m.owner.ctx.Err() != nil {
		m.mu.Unlock()
		return ControlTicket{}, ErrControl
	}
	if m.human != nil {
		m.mu.Unlock()
		return ControlTicket{}, ErrBusy
	}
	token, e := capability()
	if e != nil {
		m.mu.Unlock()
		return ControlTicket{}, e
	}
	life, cancel := context.WithCancel(context.Background())
	h := &humanLease{view: v, binding: m.owner, token: token, state: "transitioning", life: life, cancel: cancel}
	m.human = h
	provider := m.media
	m.mu.Unlock()
	if e = m.acquireGate(ctx); e != nil {
		m.mu.Lock()
		if m.human == h {
			m.human = nil
		}
		m.mu.Unlock()
		cancel()
		return ControlTicket{}, e
	}
	defer m.releaseGate()
	openCtx, end := context.WithTimeout(ctx, 5*time.Second)
	stop := context.AfterFunc(life, end)
	session, e := provider.Open(openCtx, MediaRequest{Target: "desktop", Input: true})
	stop()
	end()
	m.mu.Lock()
	valid := !m.closed && m.owner == h.binding && h.binding.ctx.Err() == nil && v.life.Err() == nil && m.human == h && life.Err() == nil
	if e != nil || !valid || session == nil || !session.Info().Input {
		m.mu.Unlock()
		closeOK := true
		if session != nil {
			closeOK = session.Close(context.Background()) == nil
		}
		m.mu.Lock()
		if m.human == h && closeOK {
			m.human = nil
		} else if m.human == h {
			// Retain the failed handle so shutdown also reports unconfirmed
			// revocation instead of treating an empty placeholder as success.
			h.media = session
		}
		m.mu.Unlock()
		cancel()
		return ControlTicket{}, ErrMedia
	}
	h.media = session
	h.state = "human_control"
	h.timer = time.AfterFunc(HumanGrace, func() { m.expire(h) })
	m.mu.Unlock()
	return ControlTicket{Token: token, ExpiresAt: time.Now().UTC().Add(HumanLeaseTTL)}, nil
}
func (m *Manager) AttachControl(id, vid, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.human
	if m.closed || h == nil || h.state != "human_control" || h.view.id != vid || h.view.computer != id || !secretEqual(h.token, token) || h.attached {
		return ErrControl
	}
	h.attached = true
	h.timer.Reset(HumanLeaseTTL)
	return nil
}
func (m *Manager) TouchControl(id, vid, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.human
	if m.closed || h == nil || h.state != "human_control" || !h.attached || h.view.computer != id || h.view.id != vid || !secretEqual(h.token, token) {
		return ErrControl
	}
	h.timer.Reset(HumanLeaseTTL)
	return nil
}
func (m *Manager) DisconnectControl(id, vid, token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.human
	if h != nil && h.view.computer == id && h.view.id == vid && secretEqual(h.token, token) {
		h.attached = false
		h.timer.Reset(HumanGrace)
	}
}
func (m *Manager) expire(h *humanLease) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = m.releaseHuman(ctx, h)
}
func (m *Manager) ReleaseControl(ctx context.Context, id, vid, token string) error {
	m.mu.Lock()
	h := m.human
	if h == nil || h.view.computer != id || h.view.id != vid || !secretEqual(h.token, token) {
		m.mu.Unlock()
		return ErrControl
	}
	m.mu.Unlock()
	return m.releaseHuman(ctx, h)
}
func (m *Manager) releaseHuman(ctx context.Context, h *humanLease) error {
	m.mu.Lock()
	if m.human != h {
		m.mu.Unlock()
		return ErrControl
	}
	h.state = "transitioning"
	h.cancel()
	if h.timer != nil {
		h.timer.Stop()
	}
	m.mu.Unlock()
	if e := m.acquireGate(ctx); e != nil {
		return e
	}
	defer m.releaseGate()
	m.mu.Lock()
	if m.human != h {
		m.mu.Unlock()
		return nil
	}
	session := h.media
	m.mu.Unlock()
	// Do not restore agent authority if daemon revocation could not be confirmed.
	if session != nil {
		if e := session.Close(ctx); e != nil {
			return ErrMedia
		}
	}
	m.mu.Lock()
	if m.human == h {
		m.human = nil
	}
	m.mu.Unlock()
	return nil
}
func (m *Manager) releaseViewControl(ctx context.Context, v *ViewSession) error {
	m.mu.Lock()
	h := m.human
	m.mu.Unlock()
	if h != nil && h.view == v {
		return m.releaseHuman(ctx, h)
	}
	return nil
}
func (m *Manager) revokeBinding(ctx context.Context, b *Binding) error {
	m.mu.Lock()
	h := m.human
	m.mu.Unlock()
	if h != nil && h.binding == b {
		return m.releaseHuman(ctx, h)
	}
	return nil
}
func (m *Manager) closeViews(ctx context.Context) error {
	m.mu.Lock()
	views := make([]*ViewSession, 0, len(m.views))
	for _, v := range m.views {
		views = append(views, v)
	}
	h := m.human
	m.mu.Unlock()
	var first error
	if h != nil {
		first = m.releaseHuman(ctx, h)
	}
	for _, v := range views {
		if e := v.Close(ctx); e != nil && first == nil {
			first = e
		}
	}
	return first
}

// StopMedia rejects new bindings/control/viewers and revokes human/media
// resources before application-owned Agent Session workers are joined.
func (m *Manager) StopMedia(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	b := m.owner
	m.mu.Unlock()
	if b != nil {
		b.cancel()
	}
	err := m.closeViews(ctx)
	for _, child := range m.childComputers() {
		if e := child.StopMedia(ctx); err == nil {
			err = e
		}
	}
	return err
}
func (m *Manager) HumanInput(ctx context.Context, id, vid, token string, raw []byte) ([]byte, error) {
	batch, e := ValidateInput(raw)
	if e != nil {
		return nil, e
	}
	if e = m.acquireGate(ctx); e != nil {
		return nil, e
	}
	defer m.releaseGate()
	m.mu.Lock()
	h := m.human
	if m.closed || h == nil || h.state != "human_control" || !h.attached || h.binding != m.owner || h.binding.ctx.Err() != nil || h.view.id != vid || h.view.computer != id || !secretEqual(h.token, token) {
		m.mu.Unlock()
		return nil, ErrControl
	}
	if h.next != 0 && batch.FirstSequence != h.next {
		m.mu.Unlock()
		return nil, ErrArguments
	}
	now := time.Now()
	if now.Sub(h.rateAt) >= time.Second {
		h.rateAt = now
		h.rate = 0
	}
	if h.rate+len(batch.Events) > 120 {
		m.mu.Unlock()
		return nil, ErrCapacity
	}
	h.rate += len(batch.Events)
	h.next = batch.FirstSequence + uint64(len(batch.Events))
	h.timer.Reset(HumanLeaseTTL)
	m.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	stop := context.AfterFunc(h.life, cancel)
	defer func() { stop(); cancel() }()
	if e = h.media.Send(callCtx, inputMessage(h.media.Info().SessionID, batch)); e != nil {
		return nil, ErrMedia
	}
	for i := 0; i < 128; i++ {
		p, e := h.media.Receive(callCtx)
		if e != nil {
			return nil, ErrMedia
		}
		if p.Binary {
			continue
		}
		var msg struct {
			Type    string `json:"type"`
			Payload struct {
				Delivered bool   `json:"delivered"`
				Accepted  uint64 `json:"through_sequence"`
				Session   string `json:"session_id"`
			} `json:"payload"`
		}
		if len(p.Data) > MaxMediaControl || json.Unmarshal(p.Data, &msg) != nil {
			return nil, ErrMedia
		}
		if msg.Type == "interactive_input_acknowledgement" {
			if !msg.Payload.Delivered || msg.Payload.Session != h.media.Info().SessionID || msg.Payload.Accepted != h.next-1 {
				return nil, ErrMedia
			}
			return safeControl("interactive_input_acknowledgement", map[string]any{"delivered": true, "through_sequence": h.next - 1}), nil
		}
		if msg.Type == "error" || msg.Type == "lifecycle" {
			return nil, ErrMedia
		}
	}
	return nil, ErrMedia
}
