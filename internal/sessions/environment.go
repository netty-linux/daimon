package sessions

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/environments"
	"github.com/netty-linux/daimon/internal/sandbox"
)

type PersistentMetadata struct {
	ID                string `json:"id"`
	StartingRevision  uint64 `json:"starting_revision"`
	CommittedRevision uint64 `json:"committed_revision"`
	Committed         bool   `json:"committed"`
	State             string `json:"state"`
}

func clonePersistent(p *PersistentMetadata) *PersistentMetadata {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}
func (m *Manager) setPersistent(id ID, p PersistentMetadata) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	s.snapshot.PersistentWorkspace = clonePersistent(&p)
	close(s.changed)
	s.changed = make(chan struct{})
}
func (m *Manager) reserveEnvironment(ctx context.Context, req StartRequest, sandboxMode bool, r *resolvedRuntime) (*environments.Reservation, error) {
	if m.deps.Environments == nil {
		return nil, nil
	}
	reservation, e := m.deps.Environments.Acquire(ctx, string(req.ThreadID))
	if errors.Is(e, environments.ErrNotFound) {
		return nil, nil
	}
	if e != nil {
		return nil, &Error{Kind: EnvironmentResolution, Cause: e}
	}
	r.closers = append(r.closers, func() error { reservation.Close(); return nil })
	if !sandboxMode {
		return nil, &Error{Kind: EnvironmentResolution, Cause: environments.ErrTransfer}
	}
	r.binding.EnvironmentID = reservation.Metadata.ID
	r.binding.StartingRevision = reservation.Metadata.Revision
	return reservation, nil
}
func (m *Manager) hydrateEnvironment(ctx context.Context, req StartRequest, sink agentloop.EventSink, r *resolvedRuntime, lease *sandbox.Lease, reservation *environments.Reservation) error {
	p := PersistentMetadata{ID: reservation.Metadata.ID, StartingRevision: reservation.Metadata.Revision, State: "hydrating"}
	m.setPersistent(req.SessionID, p)
	sink.Record(ctx, agentloop.Event{Kind: agentloop.EnvironmentHydrating})
	transfer, e := lease.Workspace(ctx)
	if e == nil {
		e = transfer.Hydrate(ctx, reservation.Workspace())
	}
	if e == nil {
		e = ctx.Err()
	}
	if e != nil {
		p.State = "failed"
		m.setPersistent(req.SessionID, p)
		sink.Record(ctx, agentloop.Event{Kind: agentloop.EnvironmentFailed})
		return &Error{Kind: EnvironmentHydration, Cause: e}
	}
	p.State = "ready"
	m.setPersistent(req.SessionID, p)
	sink.Record(ctx, agentloop.Event{Kind: agentloop.EnvironmentReady})
	r.syncWorkspace = func(ctx context.Context) error {
		p.State = "saving"
		m.setPersistent(req.SessionID, p)
		sink.Record(ctx, agentloop.Event{Kind: agentloop.EnvironmentSaving})
		syncCtx, end := context.WithTimeout(ctx, environments.TransferDuration)
		defer end()
		w, e := transfer.Export(syncCtx)
		if e == nil {
			var meta environments.Metadata
			meta, p.Committed, e = reservation.Commit(syncCtx, w)
			if p.Committed {
				p.CommittedRevision = meta.Revision
			}
		}
		kind := agentloop.EnvironmentSaved
		p.State = "saved"
		if e != nil {
			p.State = "failed"
			kind = agentloop.EnvironmentFailed
		}
		m.setPersistent(req.SessionID, p)
		sink.Record(ctx, agentloop.Event{Kind: kind})
		if e != nil {
			if errors.Is(e, environments.ErrConflict) {
				return &Error{Kind: EnvironmentConflict, Cause: e}
			}
			return &Error{Kind: EnvironmentSync, Cause: e}
		}
		return nil
	}
	return nil
}
