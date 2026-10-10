package sessions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers"
)

type state struct {
	admissionBot   bots.ID
	scheduled      bool
	snapshot       Snapshot
	binding        Binding
	events         eventBuffer
	cancel         context.CancelCauseFunc
	done           chan struct{}
	changed        chan struct{}
	err            error
	abortRequested bool
	pending        *pendingApproval
	approvalIDs    map[ApprovalID]bool // true = resolved, false = invalidated; bounded.
}

// MaxRetainedSessions bounds retained Sessions in the application-owned Manager.
const MaxRetainedSessions = 128

// Manager owns every worker and retains bounded metadata, evicting finalized terminals.
// Short locks protect state only; external calls never run under these locks.
type Manager struct {
	mu        sync.Mutex
	deps      Dependencies
	options   Options
	factories map[providers.ID]providers.Factory
	sessions  map[ID]*state
	usedIDs   map[ID]struct{} // Identity tombstones only: never reuse an admitted ID after eviction.
	active    map[string]ID
	closed    bool
}

func NewManager(deps Dependencies, options Options) (*Manager, error) {
	if err := options.Budget.Validate(); err != nil {
		return nil, &Error{Kind: Invalid, Cause: err}
	}
	if deps.Bots == nil || deps.Threads == nil || deps.Providers == nil || deps.Config == nil ||
		(deps.Memory != nil && (options.MaxMemoryContextBytes < 1 || options.MaxMemoryContextBytes > 32*1024 || options.MaxMemoryRecords < 1 || options.MaxMemoryRecords > 64)) || options.EventCapacity < 1 || options.EventCapacity > 16384 || options.MaxSessions < 1 || options.MaxSessions > MaxRetainedSessions {
		return nil, &Error{Kind: Invalid}
	}
	m := &Manager{deps: deps, options: options, factories: map[providers.ID]providers.Factory{}, sessions: map[ID]*state{}, usedIDs: map[ID]struct{}{}, active: map[string]ID{}}
	for _, id := range deps.Providers.IDs() {
		factory, err := deps.Providers.Get(id)
		if err != nil {
			return nil, &Error{Kind: ProviderResolution, Cause: err}
		}
		m.factories[id] = factory
	}
	m.deps.Providers = nil // No subsequent shared registry reads or mutable globals.
	return m, nil
}

func nowUTC() time.Time { return time.Now().UTC() }

// Start reserves ID/Thread, then prepares/persists the user when configured.
// Conversation preparation failures return directly; subsequent runtime
// resolution/execution failures are observed through Wait/Get.
// ctx owns startup and run; Budget.MaxRunDuration starts inside agentloop.Run.
func (m *Manager) Start(ctx context.Context, req StartRequest) (Snapshot, error) {
	if m == nil || ctx == nil {
		return Snapshot{}, &Error{Kind: Invalid}
	}
	if err := ValidateID(req.SessionID); err != nil {
		return Snapshot{}, err
	}
	if req.MessageID != "" && providers.ValidateID(providers.ID(req.MessageID)) != nil {
		return Snapshot{}, &Error{Kind: Invalid}
	}
	if providers.ValidateID(providers.ID(req.ThreadID)) != nil || strings.TrimSpace(req.Message) == "" || !utf8.ValidString(req.Message) ||
		len(req.Message) > m.options.Budget.MaxUserMessageBytes || (req.EnableCreateFile && req.EnableReplaceFile) {
		return Snapshot{}, &Error{Kind: Invalid}
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, &Error{Kind: Canceled, Cause: err}
	}
	// Preserve asynchronous Thread resolution for ordinary starts. Only the
	// new scheduled admission or an existing scheduled lease needs this read.
	needsIdentity := req.ScheduledBotID != ""
	m.mu.Lock()
	for _, id := range m.active {
		if current := m.sessions[id]; current != nil && current.scheduled {
			needsIdentity = true
			break
		}
	}
	m.mu.Unlock()
	var admissionBot bots.ID
	if needsIdentity {
		admissionBot = m.admissionBot(req.ThreadID)
	}
	if req.ScheduledBotID != "" && (admissionBot == "" || admissionBot != req.ScheduledBotID) {
		return Snapshot{}, &Error{Kind: ThreadResolution}
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Snapshot{}, &Error{Kind: Closed}
	}
	if _, exists := m.usedIDs[req.SessionID]; exists {
		m.mu.Unlock()
		return Snapshot{}, &Error{Kind: Duplicate}
	}
	if _, exists := m.active[string(req.ThreadID)]; exists {
		m.mu.Unlock()
		return Snapshot{}, &Error{Kind: ThreadBusy}
	}
	for _, activeID := range m.active {
		current := m.sessions[activeID]
		if current == nil {
			if req.ScheduledBotID != "" {
				m.mu.Unlock()
				return Snapshot{}, &Error{Kind: ThreadBusy}
			}
			continue
		}
		if (req.ScheduledBotID != "" && (current.admissionBot == "" || current.admissionBot == admissionBot)) ||
			(current.scheduled && (admissionBot == "" || current.admissionBot == admissionBot)) {
			m.mu.Unlock()
			return Snapshot{}, &Error{Kind: ThreadBusy}
		}
	}
	if len(m.sessions) >= m.options.MaxSessions {
		var oldest ID
		for id, candidate := range m.sessions {
			if !terminal(candidate.snapshot.Status) {
				continue
			}
			select {
			case <-candidate.done:
			default:
				continue
			}
			if oldest == "" || candidate.snapshot.StartedAt.Before(m.sessions[oldest].snapshot.StartedAt) ||
				(candidate.snapshot.StartedAt.Equal(m.sessions[oldest].snapshot.StartedAt) && id < oldest) {
				oldest = id
			}
		}
		if oldest == "" {
			m.mu.Unlock()
			return Snapshot{}, &Error{Kind: Capacity}
		}
		delete(m.sessions, oldest)
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	s := &state{snapshot: Snapshot{ID: req.SessionID, ThreadID: req.ThreadID, Status: Created, StartedAt: nowUTC()},
		events: eventBuffer{items: make([]Event, m.options.EventCapacity)}, cancel: cancel, done: make(chan struct{}), changed: make(chan struct{})}
	s.admissionBot, s.scheduled = admissionBot, req.ScheduledBotID != ""
	m.sessions[req.SessionID] = s
	m.usedIDs[req.SessionID] = struct{}{}
	m.active[string(req.ThreadID)] = req.SessionID
	initial := s.snapshot
	m.mu.Unlock()
	var history []model.Message
	var memoryContext string
	if m.deps.Conversations != nil || m.deps.Memory != nil {
		// Resolve Thread existence before creating a file; a nonexistent reference
		// must not create orphan history. No Manager lock surrounds store I/O.
		var prepareErr error
		memoryContext, prepareErr = m.prepareMemory(runCtx, req)
		if prepareErr == nil && m.deps.Conversations != nil {
			history, prepareErr = m.prepareStart(runCtx, req, len(memoryContext))
		}
		if prepareErr != nil {
			prepareCategory := category(prepareErr)
			m.mu.Lock()
			s.snapshot.Status = Failed
			if ctxErr := runCtx.Err(); s.abortRequested || ctxErr != nil {
				s.snapshot.Status = Aborted
				prepareCategory = Canceled
				if ctxErr == context.DeadlineExceeded {
					prepareCategory = Deadline
				}
				cause := context.Cause(runCtx)
				if s.abortRequested {
					cause = errors.Join(ErrAborted, context.Canceled, cause)
				}
				prepareErr = &Error{Kind: prepareCategory, Cause: errors.Join(cause, prepareErr)}
			}
			s.snapshot.FinishedAt = nowUTC()
			s.snapshot.ErrorCategory = prepareCategory
			s.err = prepareErr
			delete(m.active, string(req.ThreadID))
			close(s.done)
			close(s.changed)
			m.mu.Unlock()
			cancel(nil)
			return Snapshot{}, prepareErr
		}
	}
	go m.execute(runCtx, req, history, memoryContext)
	return initial, nil
}

func (m *Manager) Get(id ID) (Snapshot, error) {
	if m == nil {
		return Snapshot{}, &Error{Kind: Invalid}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Snapshot{}, &Error{Kind: NotFound}
	}
	snapshot := s.snapshot
	snapshot.Computer = cloneComputerMetadata(snapshot.Computer)
	snapshot.Environment = cloneEnvironment(snapshot.Environment)
	snapshot.PersistentWorkspace = clonePersistent(snapshot.PersistentWorkspace)
	return snapshot, nil
}

// Binding is a deliberate private configuration surface, not public metadata.
func (m *Manager) Binding(id ID) (Binding, error) {
	if m == nil {
		return Binding{}, &Error{Kind: Invalid}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Binding{}, &Error{Kind: NotFound}
	}
	if !s.snapshot.Bound {
		return Binding{}, &Error{Kind: NotRunning}
	}
	return cloneBinding(s.binding), nil
}

func (m *Manager) EventsSince(id ID, after uint64) (Replay, error) {
	if m == nil {
		return Replay{}, &Error{Kind: Invalid}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Replay{}, &Error{Kind: NotFound}
	}
	return s.events.since(after), nil
}

func (m *Manager) Wait(ctx context.Context, id ID) (Snapshot, error) {
	if m == nil || ctx == nil {
		return Snapshot{}, &Error{Kind: Invalid}
	}
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return Snapshot{}, &Error{Kind: NotFound}
	}
	select {
	case <-ctx.Done():
		return Snapshot{}, &Error{Kind: Canceled, Cause: ctx.Err()}
	case <-s.done:
		m.mu.Lock()
		defer m.mu.Unlock()
		snapshot := s.snapshot
		snapshot.Computer = cloneComputerMetadata(snapshot.Computer)
		snapshot.Environment = cloneEnvironment(snapshot.Environment)
		snapshot.PersistentWorkspace = clonePersistent(snapshot.PersistentWorkspace)
		return snapshot, s.err
	}
}

func terminal(status Status) bool {
	return status == Completed || status == Failed || status == Aborted
}

func (m *Manager) Abort(id ID) error {
	if m == nil {
		return &Error{Kind: Invalid}
	}
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return &Error{Kind: NotFound}
	}
	if terminal(s.snapshot.Status) {
		m.mu.Unlock()
		return &Error{Kind: NotRunning}
	}
	s.abortRequested = true
	cancel := s.cancel
	m.mu.Unlock()
	cancel(ErrAborted)
	return nil
}

// Close prevents new starts, requests abort, and joins workers until ctx expires.
// Noncooperative dependencies cannot be forcibly interrupted; callers may Wait
// again after a timed-out Close. Terminal snapshots remain readable.
func (m *Manager) Close(ctx context.Context) error {
	if m == nil || ctx == nil {
		return &Error{Kind: Invalid}
	}
	m.mu.Lock()
	m.closed = true
	states := make([]*state, 0, len(m.sessions))
	for _, s := range m.sessions {
		if !terminal(s.snapshot.Status) {
			s.abortRequested = true
		}
		states = append(states, s)
	}
	m.mu.Unlock()
	for _, s := range states {
		s.cancel(ErrAborted)
	}
	for _, s := range states {
		select {
		case <-s.done:
		case <-ctx.Done():
			return &Error{Kind: Canceled, Cause: ctx.Err()}
		}
	}
	return nil
}

func (m *Manager) execute(ctx context.Context, req StartRequest, history []model.Message, memoryContext string) {
	runtime := &resolvedRuntime{memoryContext: memoryContext}
	var result agentloop.Result
	var err error
	assistantCommitted := false
	defer func() {
		if recover() != nil {
			err = &Error{Kind: Panic}
		} // Never retain panic values.
		if closeErr := runtime.close(); closeErr != nil {
			err = &Error{Kind: Cleanup, Cause: errors.Join(err, closeErr)}
		}
		// Final text is transient private data, persisted only after successful
		// cleanup and before terminal publication/releasing the Thread reservation.
		if err == nil && ctx.Err() == nil && m.deps.Conversations != nil {
			err = persistAssistant(ctx, m.deps.Conversations, req, result.FinalAnswer)
			assistantCommitted = err == nil
		}
		// Error inspection may call component-defined As/Unwrap. Do it outside
		// state locks and contain panics before terminal publication.
		finalCategory := category(err)
		m.mu.Lock()
		// ctx is our own cancelCtx; sample at terminal publication, after cleanup.
		ctxErr, ctxCause := ctx.Err(), context.Cause(ctx)
		s := m.sessions[req.SessionID]
		status := Completed
		if err != nil {
			status = Failed
		}
		// Once assistant append commits, later cancellation cannot unpublish it.
		if !assistantCommitted && (s.abortRequested || ctxErr != nil) {
			status = Aborted
			cause := ctxCause
			if s.abortRequested {
				cause = errors.Join(ErrAborted, context.Canceled, cause)
			}
			kind := Canceled
			if ctxErr == context.DeadlineExceeded {
				kind = Deadline
			}
			err = &Error{Kind: kind, Cause: errors.Join(cause, err)}
			finalCategory = kind
		}
		if !ValidTransition(s.snapshot.Status, status) {
			status = Failed
			err = &Error{Kind: Invalid, Cause: err}
			finalCategory = Invalid
		}
		s.snapshot.Status = status
		s.snapshot.FinishedAt = nowUTC()
		s.snapshot.Steps, s.snapshot.ToolCalls, s.snapshot.TruncatedToolResults = result.Steps, result.ToolCalls, result.TruncatedToolResults
		if result.StopReason != "" {
			s.snapshot.StopReason = result.StopReason
		}
		if err != nil {
			s.snapshot.ErrorCategory = finalCategory
			err = &Error{Kind: s.snapshot.ErrorCategory, Cause: err}
		}
		s.err = err
		delete(m.active, string(req.ThreadID))
		close(s.done)
		close(s.changed)
		m.mu.Unlock()
		s.cancel(nil)
	}()
	sink := sessionSink{manager: m, id: req.SessionID}
	if err = m.resolve(ctx, req, sink, runtime); err != nil {
		return
	}
	if err = ctx.Err(); err != nil {
		return
	}
	m.mu.Lock()
	s := m.sessions[req.SessionID]
	s.binding = cloneBinding(runtime.binding)
	s.snapshot.Computer = cloneComputerMetadata(s.binding.Computer)
	s.snapshot.Bound = true
	s.snapshot.Binding = BindingMetadata{BotID: s.binding.BotID, ProviderID: s.binding.ProviderID, ToolCount: len(s.binding.Tools), PermissionMode: s.binding.PermissionMode}
	s.snapshot.Status = Running
	m.mu.Unlock()
	runtime.loop.InitialHistory = history
	loopCtx := ctx
	if runtime.runCtx != nil {
		loopCtx = runtime.runCtx
	}
	result, err = runtime.loop.Run(loopCtx, req.Message)
	if err == nil && loopCtx.Err() != nil {
		err = loopCtx.Err()
	}
	if err == nil && runtime.syncWorkspace != nil {
		err = runtime.syncWorkspace(loopCtx)
	}
}

func (m *Manager) setEnvironment(id ID, env EnvironmentMetadata) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil {
		s.snapshot.Environment = cloneEnvironment(&env)
	}
}
