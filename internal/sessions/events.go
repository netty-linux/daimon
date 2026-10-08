package sessions

import (
	"context"

	"github.com/netty-linux/daimon/internal/agentloop"
)

// Event wraps only the existing private-data-free semantic event.
type Event struct {
	Sequence uint64
	Event    agentloop.Event
}
type Replay struct {
	Events                       []Event
	FirstAvailable, LastSequence uint64
	Gap                          bool // Requested next event was evicted. Returned suffix is still ordered.
}

type eventBuffer struct {
	items        []Event
	start, count int
	last         uint64
}

func (b *eventBuffer) append(event agentloop.Event) bool {
	if b.last == ^uint64(0) {
		return false
	}
	b.last++
	item := Event{Sequence: b.last, Event: event}
	if b.count < len(b.items) {
		b.items[(b.start+b.count)%len(b.items)] = item
		b.count++
	} else {
		b.items[b.start] = item
		b.start = (b.start + 1) % len(b.items)
	}
	return true
}

func (b *eventBuffer) since(after uint64) Replay {
	r := Replay{Events: []Event{}, LastSequence: b.last}
	if b.count == 0 {
		return r
	}
	r.FirstAvailable = b.items[b.start].Sequence
	r.Gap = after < r.FirstAvailable-1
	for i := 0; i < b.count; i++ {
		item := b.items[(b.start+i)%len(b.items)]
		if item.Sequence > after {
			r.Events = append(r.Events, item)
		}
	}
	return r
}

type sessionSink struct {
	manager *Manager
	id      ID
}

func (s sessionSink) Record(_ context.Context, event agentloop.Event) {
	m := s.manager
	m.mu.Lock()
	state := m.sessions[s.id]
	if !state.events.append(event) {
		cancel := state.cancel
		m.mu.Unlock()
		cancel(&Error{Kind: Capacity})
		return
	}
	defer m.mu.Unlock()
	close(state.changed)
	state.changed = make(chan struct{})
	state.snapshot.LastEventSequence = state.events.last
	if event.Kind == agentloop.LoopStopped {
		state.snapshot.StopReason = event.StopReason
	}
	if event.Kind == agentloop.ApprovalRequested && state.snapshot.Status == Running {
		state.snapshot.Status = WaitingApproval
	}
	if (event.Kind == agentloop.ApprovalGranted || event.Kind == agentloop.ApprovalDenied) && state.snapshot.Status == WaitingApproval {
		state.snapshot.Status = Running
	}
}

// EventObservation is an atomic observation of the current change generation
// and finalized status. Capture it BEFORE EventsSince, then wait on Changed only
// if nonterminal. An event/finalization between capture, replay and waiting closes
// that generation, so no notification is lost. Multiple observers share signals;
// no registration, unsubscribe, per-client queue or event consumption exists.
type EventObservation struct {
	Changed  <-chan struct{}
	Status   Status
	Terminal bool
}

func (m *Manager) ObserveEvents(id ID) (EventObservation, error) {
	if m == nil {
		return EventObservation{}, &Error{Kind: Invalid}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return EventObservation{}, &Error{Kind: NotFound}
	}
	return EventObservation{Changed: s.changed, Status: s.snapshot.Status, Terminal: terminal(s.snapshot.Status)}, nil
}
