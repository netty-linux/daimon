package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/netty-linux/daimon/internal/computer"
)

type resource struct {
	cancel   context.CancelFunc
	ready    chan struct{}
	computer *computer.Manager
	cleanup  sync.Mutex
	timer    *time.Timer
}
type Manager struct {
	mu        sync.Mutex
	backend   Backend
	cloud     Backend
	catalog   *computer.Manager
	path      string
	options   Options
	records   map[ID]record
	resources map[ID]*resource
	closed    bool
	wg        sync.WaitGroup
}
type Lease struct {
	manager  *Manager
	id       ID
	Computer *computer.Manager
	Context  context.Context
}

func (l *Lease) ID() ID                          { return l.id }
func (l *Lease) Close(ctx context.Context) error { return l.manager.cleanup(ctx, l.id) }
func NewManager(path string, backend Backend, catalog *computer.Manager, opts Options) (*Manager, error) {
	return NewManagerWithCloud(path, backend, nil, catalog, opts)
}
func NewManagerWithCloud(path string, backend, cloud Backend, catalog *computer.Manager, opts Options) (*Manager, error) {
	if !filepath.IsAbs(path) || backend == nil || catalog == nil || opts.Validate() != nil {
		return nil, errorOf(Invalid)
	}
	records, e := loadJournal(path)
	if e != nil {
		return nil, e
	}
	m := &Manager{backend: backend, cloud: cloud, catalog: catalog, path: path, options: opts, records: records, resources: map[ID]*resource{}}
	for id := range records {
		ready := make(chan struct{})
		close(ready)
		m.resources[id] = &resource{cancel: func() {}, ready: ready}
	}
	return m, nil
}
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Info{}
	for _, r := range m.records {
		out = append(out, r.Info)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}
func (m *Manager) Get(id ID) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok {
		return Info{}, errorOf(NotFound)
	}
	return r.Info, nil
}
func (m *Manager) Probe(ctx context.Context) RuntimeInfo {
	if p, ok := m.backend.(RuntimeProbe); ok {
		r, e := p.Probe(ctx)
		if e == nil {
			return r
		}
		return RuntimeInfo{Backend: CUALocal, Runtime: "gvisor", Reason: Category(e)}
	}
	return RuntimeInfo{Backend: CUALocal, Runtime: "gvisor", Reason: Unavailable}
}
func (m *Manager) change(ctx context.Context, id ID, fn func(*record)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok {
		return errorOf(NotFound)
	}
	previous := r.Info.Status
	fn(&r)
	if r.Info.Status != previous && !ValidTransition(previous, r.Info.Status) {
		return errorOf(Invalid)
	}
	if validateRecord(r) != nil {
		return errorOf(Persistence)
	}
	m.records[id] = r
	return saveJournal(ctx, m.path, m.records)
}
func (m *Manager) Create(ctx context.Context, session string, p Profile) (lease *Lease, err error) {
	if ctx == nil || !sessionPattern.MatchString(session) || ValidateProfile(p) != nil {
		return nil, errorOf(Invalid)
	}
	var random [12]byte
	if _, e := rand.Read(random[:]); e != nil {
		return nil, errorOf(Provision)
	}
	id := ID("sb-" + hex.EncodeToString(random[:]))
	life, cancel := context.WithTimeout(ctx, m.options.Lifetime)
	res := &resource{cancel: cancel, ready: make(chan struct{})}
	r := record{Info: Info{ID: id, Backend: p.Backend, Kind: Container, Status: Creating, Placement: p.EffectivePlacement(), Image: ImageSpec{Alias: "linux"}, Runtime: "gvisor", Browser: p.Browser, Resources: limits(p, m.options.Lifetime), Network: "outbound", OwnerSession: session, CreatedAt: time.Now().UTC(), Cleanup: "pending"}, Name: ownedName(id), Ref: string(p.EffectivePlacement()) + ":" + ownedName(id), Profile: p}
	m.mu.Lock()
	active, cloudActive := 0, 0
	for _, existing := range m.records {
		if existing.Info.OwnerSession == session {
			m.mu.Unlock()
			cancel()
			return nil, errorOf(Invalid)
		}
		if existing.Info.Cleanup != "complete" {
			active++
			if existing.Info.Placement == Cloud {
				cloudActive++
			}
		}
	}
	if p.EffectivePlacement() == Cloud && cloudActive >= 2 || m.closed || active >= m.options.MaxActive || len(m.records) >= MaxRecords {
		m.mu.Unlock()
		cancel()
		return nil, errorOf(Capacity)
	}
	m.records[id] = r
	m.resources[id] = res
	if e := saveJournal(ctx, m.path, m.records); e != nil {
		delete(m.records, id)
		delete(m.resources, id)
		m.mu.Unlock()
		cancel()
		return nil, e
	}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() {
		close(res.ready)
		if err != nil {
			cancel()
			cleanupCtx, end := context.WithTimeout(context.Background(), m.options.DeleteTimeout)
			defer end()
			cleanupErr := m.cleanup(cleanupCtx, id)
			err = errors.Join(err, cleanupErr)
		}
	}()
	createCtx, end := context.WithTimeout(life, m.options.CreateTimeout)
	defer end()
	backend := m.backendFor(p.EffectivePlacement())
	remote, e := backend.Create(createCtx, CreateRequest{Name: r.Name, Profile: p, Limits: r.Info.Resources, ReadyTimeout: m.options.CreateTimeout})
	if e != nil {
		_ = m.change(context.Background(), id, func(r *record) { r.Info.Status = Failed; r.Info.ErrorCategory = Category(e) })
		return nil, e
	}
	if remote.Ref != r.Ref || remote.Name != r.Name || remote.Runtime != "gvisor" || !remote.Ready || !validExpiry(remote.ExpiresAt) {
		return nil, errorOf(Protocol)
	}
	stopAt := deadlineOf(life)
	if p.EffectivePlacement() == Cloud && remote.ExpiresAt != "" {
		expires, _ := time.Parse(time.RFC3339, remote.ExpiresAt)
		if !expires.After(time.Now()) {
			return nil, errorOf(Protocol)
		}
		if expires.Before(stopAt) {
			stopAt = expires
		}
	}
	provider, ok := backend.(ComputerProvider)
	if !ok {
		return nil, errorOf(Unavailable)
	}
	child, e := provider.Computer(createCtx, remote, computerID(id))
	if e != nil {
		return nil, e
	}
	res.computer = child
	if e = m.catalog.RegisterComputer(computerID(id), child); e != nil {
		return nil, errorOf(Unavailable)
	}
	if e = life.Err(); e != nil {
		return nil, &Error{Kind: Canceled, Cause: e}
	}
	if e = m.change(createCtx, id, func(r *record) {
		r.Info.Status = Running
		r.Info.ComputerID = computerID(id)
		r.Info.Image.Resolved = remote.Image
		r.Info.ExpiresAt = remote.ExpiresAt
	}); e != nil {
		return nil, e
	}
	res.timer = time.AfterFunc(time.Until(stopAt), func() {
		cleanupCtx, end := context.WithTimeout(context.Background(), m.options.DeleteTimeout)
		defer end()
		_ = m.cleanup(cleanupCtx, id)
	})
	return &Lease{manager: m, id: id, Computer: child, Context: life}, nil
}
func deadlineOf(ctx context.Context) time.Time { d, _ := ctx.Deadline(); return d }
func (m *Manager) cleanup(ctx context.Context, id ID) error {
	m.mu.Lock()
	r, ok := m.records[id]
	res := m.resources[id]
	m.mu.Unlock()
	if !ok {
		return errorOf(NotFound)
	}
	if r.Info.Cleanup == "complete" {
		return nil
	}
	if res != nil {
		res.cancel()
		select {
		case <-res.ready:
		case <-ctx.Done():
			return ctx.Err()
		}
		res.cleanup.Lock()
		defer res.cleanup.Unlock()
		if res.timer != nil {
			res.timer.Stop()
		}
	}
	m.mu.Lock()
	r = m.records[id]
	m.mu.Unlock()
	if r.Info.Cleanup == "complete" {
		return nil
	}
	var resourceErr error
	if res != nil && res.computer != nil {
		resourceErr = errors.Join(res.computer.StopMedia(ctx), res.computer.Close(ctx))
		m.catalog.UnregisterComputer(computerID(id), res.computer)
	}
	if e := m.change(ctx, id, func(r *record) { r.Info.Status = Deleting; r.Info.Cleanup = "pending" }); e != nil {
		return &Error{Kind: Cleanup, Cause: errors.Join(e, resourceErr), Unresolved: []ID{id}}
	}
	backend := m.backendFor(r.Info.Placement)
	remote, e := backend.Get(ctx, r.Ref)
	if e == nil && (remote.Ref != r.Ref || remote.Name != r.Name || remote.Runtime != "gvisor") {
		e = errorOf(Protocol)
	}
	if e == nil {
		e = backend.Delete(ctx, r.Ref)
	}
	if errors.Is(e, errorOf(NotFound)) {
		e = nil
	}
	if e != nil {
		persistErr := m.change(context.Background(), id, func(r *record) { r.Info.Status = Failed; r.Info.Cleanup = "unresolved"; r.Info.ErrorCategory = Cleanup })
		return &Error{Kind: Cleanup, Cause: errors.Join(e, persistErr, resourceErr), Unresolved: []ID{id}}
	}
	persistErr := m.change(context.Background(), id, func(r *record) {
		r.Info.Status = Deleted
		r.Info.Cleanup = "complete"
		if r.Info.ErrorCategory == Cleanup {
			r.Info.ErrorCategory = ""
		}
	})
	return errors.Join(resourceErr, persistErr)
}

// Reconcile only examines exact registry-owned local refs. Unknown CUA resources
// are never listed/adopted/deleted based on a name prefix.
func (m *Manager) Reconcile(ctx context.Context) error {
	var result error
	for _, i := range m.List() {
		if i.Cleanup == "complete" {
			continue
		}
		if e := m.change(ctx, i.ID, func(r *record) { r.Info.Orphan = true }); e != nil {
			return e
		}
		cleanupCtx, end := context.WithTimeout(ctx, m.options.DeleteTimeout)
		e := m.cleanup(cleanupCtx, i.ID)
		end()
		result = errors.Join(result, e)
	}
	return result
}
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	for _, r := range m.resources {
		r.cancel()
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		ids := []ID{}
		for _, i := range m.List() {
			if i.Cleanup != "complete" {
				ids = append(ids, i.ID)
			}
		}
		return &Error{Kind: Cleanup, Cause: ctx.Err(), Unresolved: ids}
	}
	var result error
	for _, i := range m.List() {
		if i.Cleanup == "complete" {
			continue
		}
		cleanupCtx, end := context.WithTimeout(ctx, m.options.DeleteTimeout)
		e := m.cleanup(cleanupCtx, i.ID)
		end()
		result = errors.Join(result, e)
	}
	if m.cloud != nil {
		result = errors.Join(result, m.cloud.Close(ctx))
	}
	return errors.Join(result, m.backend.Close(ctx))
}

func (m *Manager) backendFor(p Placement) Backend {
	if p == Cloud {
		if m.cloud != nil {
			return m.cloud
		}
		return UnavailableBackend{}
	}
	return m.backend
}
func (m *Manager) Backends(ctx context.Context) []RuntimeInfo {
	out := []RuntimeInfo{m.Probe(ctx)}
	cloud := RuntimeInfo{Backend: CUACloud, Runtime: "gvisor", Reason: NotConfigured}
	if p, ok := m.cloud.(RuntimeProbe); ok {
		info, e := p.Probe(ctx)
		cloud = info
		cloud.Backend = CUACloud
		cloud.Runtime = "gvisor"
		if e != nil {
			cloud.Available = false
			cloud.Reason = Category(e)
		}
	}
	return append(out, cloud)
}
