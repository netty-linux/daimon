package routines

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"sync"
	"time"
)

// Runtime has no CUA/harness/command surface. The only execution entry point
// is the application's ordinary SessionManager with Bot-wide admission.
type Runtime interface {
	Start(context.Context, sessions.StartRequest) (sessions.Snapshot, error)
	ScheduledStatus(bots.ID) sessions.Status
	Get(sessions.ID) (sessions.Snapshot, error)
}
type References interface {
	Get(threads.ID) (threads.Thread, error)
}
type BotReader interface {
	Get(bots.ID) (bots.Bot, error)
}
type View struct {
	ID        string    `json:"id"`
	BotID     string    `json:"bot_id"`
	ThreadID  string    `json:"thread_id"`
	Title     string    `json:"title"`
	DailyAt   string    `json:"daily_at"`
	Timezone  string    `json:"timezone"`
	Enabled   bool      `json:"enabled"`
	NextAt    time.Time `json:"next_at"`
	State     string    `json:"state"`
	SessionID string    `json:"session_id,omitempty"`
}
type Scheduler struct {
	mu              sync.Mutex
	store           *Store
	runtime         Runtime
	threads         References
	bots            BotReader
	outcomes        map[string]string
	sessionIDs      map[string]string
	lastBot         map[string]time.Time
	replace, create bool
}

func New(store *Store, runtime Runtime, threads References, bots BotReader, replace, create bool) (*Scheduler, error) {
	if store == nil || runtime == nil || threads == nil || bots == nil || (replace && create) {
		return nil, ErrInvalid
	}
	return &Scheduler{store: store, runtime: runtime, threads: threads, bots: bots, outcomes: map[string]string{}, sessionIDs: map[string]string{}, lastBot: map[string]time.Time{}, replace: replace, create: create}, nil
}
func (s *Scheduler) Validate(in Input) error {
	if Validate(in) != nil {
		return ErrInvalid
	}
	t, e := s.threads.Get(threads.ID(in.ThreadID))
	if e != nil || t.BotID != bots.ID(in.BotID) {
		return ErrInvalid
	}
	b, e := s.bots.Get(bots.ID(in.BotID))
	if e != nil || bots.Validate(b) != nil {
		return ErrInvalid
	}
	// Unattended paid provisioning has no reviewed persistent cost consent.
	if b.SandboxProfile != nil && b.SandboxProfile.EffectivePlacement() == "cloud" {
		return ErrInvalid
	}
	return nil
}
func (s *Scheduler) Create(ctx context.Context, in Input, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Validate(in) != nil {
		return ErrInvalid
	}
	return s.store.Create(ctx, in, now)
}
func (s *Scheduler) Enable(ctx context.Context, id string, enabled bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, e := s.store.List()
	if e != nil {
		return e
	}
	for _, r := range items {
		if r.ID == id && enabled {
			if s.Validate(r.Input) != nil {
				return ErrInvalid
			}
		}
	}
	return s.store.Enable(ctx, id, enabled, now)
}
func (s *Scheduler) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.store.Delete(ctx, id); e != nil {
		return e
	}
	delete(s.outcomes, id)
	delete(s.sessionIDs, id)
	return nil
}
func (s *Scheduler) HasReference(bot, thread string) (bool, error) {
	items, e := s.store.List()
	if e != nil {
		return false, e
	}
	for _, r := range items {
		if (bot != "" && r.BotID == bot) || (thread != "" && r.ThreadID == thread) {
			return true, nil
		}
	}
	return false, nil
}
func (s *Scheduler) List() ([]View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, e := s.store.List()
	if e != nil {
		return nil, e
	}
	views := []View{}
	for _, r := range items {
		state := "active"
		if !r.Enabled {
			state = "paused"
		} else if status := s.runtime.ScheduledStatus(bots.ID(r.BotID)); status != "" {
			state = "running"
			if status == sessions.WaitingApproval {
				state = "waiting_approval"
			}
		} else if s.outcomes[r.ID] != "" {
			state = s.outcomes[r.ID]
		}
		if r.Enabled && s.sessionIDs[r.ID] != "" && state == "active" {
			snapshot, e := s.runtime.Get(sessions.ID(s.sessionIDs[r.ID]))
			if e != nil || snapshot.Status == sessions.Failed || snapshot.Status == sessions.Aborted {
				state = "failed"
			}
		}
		views = append(views, View{r.ID, r.BotID, r.ThreadID, r.Title, r.DailyAt, r.Timezone, r.Enabled, r.NextAt, state, s.sessionIDs[r.ID]})
	}
	return views, nil
}
func randomID(prefix string) (string, error) {
	var data [16]byte
	if _, e := rand.Read(data[:]); e != nil {
		return "", ErrInvalid
	}
	return prefix + hex.EncodeToString(data[:]), nil
}

// Tick is serialized with CRUD. The slot is consumed before Start; admission
// races, capacity or errors are explicit failures, never automatic retries.
// WaitingApproval leaves due intent pending, without launching another Session.
func (s *Scheduler) Tick(ctx context.Context, now time.Time) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrInvalid
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	items, e := s.store.List()
	if e != nil {
		return e
	}
	s.lastBot = map[string]time.Time{}
	for _, r := range items {
		if r.LastAttemptAt.After(s.lastBot[r.BotID]) {
			s.lastBot[r.BotID] = r.LastAttemptAt
		}
	}
	for _, r := range items {
		if !r.Enabled || r.NextAt.After(now) {
			continue
		}
		if s.runtime.ScheduledStatus(bots.ID(r.BotID)) != "" {
			continue
		}
		if last := s.lastBot[r.BotID]; !last.IsZero() && now.Sub(last) < 15*time.Minute {
			continue
		}
		if e := s.store.consume(ctx, r, now); e != nil {
			return e
		}
		s.outcomes[r.ID] = "failed"
		if s.Validate(r.Input) != nil {
			continue
		}
		id, e := randomID("routine-session-")
		if e != nil {
			return e
		}
		msg, e := randomID("routine-message-")
		if e != nil {
			return e
		}
		s.lastBot[r.BotID] = now
		snapshot, e := s.runtime.Start(ctx, sessions.StartRequest{ScheduledBotID: bots.ID(r.BotID), SessionID: sessions.ID(id), ThreadID: threads.ID(r.ThreadID), Message: r.Prompt, MessageID: conversations.ID(msg), EnableReplaceFile: s.replace, EnableCreateFile: s.create})
		if e == nil {
			s.outcomes[r.ID] = "active"
			s.sessionIDs[r.ID] = string(snapshot.ID)
		}
	}
	return nil
}

// Run is application-owned. No worker/session survives the supplied context.
// Expired slots at process start are skipped; no offline catch-up.
func (s *Scheduler) Run(ctx context.Context) error {
	if e := s.store.reset(ctx, time.Now().UTC()); e != nil {
		return e
	}
	ticker := time.NewTicker(time.Second * 15)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if e := s.Tick(ctx, now.UTC()); e != nil {
				if ctx.Err() != nil {
					return nil
				}
				return e
			}
		}
	}
}
