package managedworkspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

var ErrDiscard = errors.New("descarte recusado ou interrompido; consulte a auditoria privada")

const tombstoneDirectory = "_tombstones"

type DiscardProposal struct{ state *discardState }
type DiscardPermit struct {
	state *discardState
	used  *atomic.Bool
}
type discardState struct {
	mu                sync.Mutex
	store             *Store
	id                string
	root              *os.Root
	identity          Identity
	active            *os.File
	inventory         inventory
	summary           Summary
	ctx               context.Context
	approvalCtx       context.Context
	view              editcontract.Review
	attempted         atomic.Bool
	closed            atomic.Bool
	auditStarted      bool
	auditIdentity     Identity
	initialAuditHash  string
	initialAuditBytes int
	// Package-private fault seams exercise real writes/sync/removal independently.
	auditSync func(*os.File) error
	auditOpen func(*os.Root, string) (*os.File, error)
	remove    func(*os.Root, string) error
}

func (p *DiscardProposal) Close() error {
	if p == nil || p.state == nil {
		return ErrState
	}
	return p.state.close()
}
func (d *discardState) close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closeLocked()
}
func (d *discardState) closeLocked() error {
	if d.closed.CompareAndSwap(false, true) {
		if err := errors.Join(d.active.Close(), d.root.Close()); err != nil {
			return ErrArtifact
		}
	}
	return nil
}
func (p *DiscardProposal) View() editcontract.Review {
	if p == nil || p.state == nil {
		return editcontract.Review{}
	}
	return p.state.view
}

// Opt-in is checked before opening a run or constructing a preview.
func (s *Store) PrepareDiscard(ctx context.Context, id string, enabled bool) (*DiscardProposal, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if !enabled {
		return nil, ErrDiscard
	}
	if !validID(id) {
		return nil, ErrPrivate
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrState
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	if err := checkPrivate(filepath.Join(s.base, id)); err != nil {
		return nil, err
	}
	root, err := s.root.OpenRoot(id)
	if err != nil {
		return nil, ErrPrivate
	}
	active, err := exclusiveDirectory(root)
	if err != nil {
		root.Close()
		return nil, err
	}
	info, err := root.Stat(".")
	if err != nil {
		active.Close()
		root.Close()
		return nil, ErrPrivate
	}
	d := &discardState{store: s, id: id, root: root, active: active, identity: identityInfo(info), ctx: ctx, auditSync: (*os.File).Sync, remove: (*os.Root).Remove}
	d.auditOpen = func(root *os.Root, name string) (*os.File, error) {
		return root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	}
	ok := false
	defer func() {
		if !ok {
			d.close()
		}
	}()
	d.summary, err = s.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	// Interrupted/invalid evidence cannot authorize removal. No automatic repair.
	if d.summary.Reason != "verified" || d.summary.Staging || !(d.summary.State == "ready" || d.summary.State == "succeeded" || d.summary.State == "partial") {
		return nil, ErrDiscard
	}
	d.inventory, err = inventoryRun(ctx, s.base, id, root)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, ErrState
	}
	display := fmt.Sprintf("Descarte explícito de uma única cópia gerenciada.\nstore=%s\nrun_id=%s\nrun_relative=%s\nformat=1; declared=%s; verified=%s\nmanifest=%t; journal=%t; approved_plan=%t; attempt.lock=%t; staging=%t\nfiles=%d; directories=%d; total_size_bytes=%d\nstate_sha256=%s\nlimits: entries=%d; total_size_bytes=%d; preview_bytes=%d\nSomente a cópia gerenciada será removida. A origem não será removida nem modificada.\nO UID proprietário e administradores são confiáveis. Sem rollback, retomada ou publicação.\n", strconv.QuoteToASCII(s.base), id, id, d.summary.Declared, d.summary.State, d.summary.Manifest, d.summary.Journal, d.summary.ApprovedPlan, d.summary.Lock, d.summary.Staging, d.inventory.Files, d.inventory.Directories, d.inventory.Bytes, d.inventory.Hash, MaxRunEntries, MaxRunBytes, MaxPreviewBytes)
	if len(display) > MaxPreviewBytes {
		return nil, ErrLimit
	}
	deadline, _ := ctx.Deadline()
	display += "approval_deadline_utc=" + deadline.UTC().Format(time.RFC3339Nano) + "\n"
	if len(display) > MaxPreviewBytes {
		return nil, ErrLimit
	}
	d.view = editcontract.Review{ID: hex.EncodeToString(nonce), RunID: id, RootID: workspaceplan.Hash([]byte(s.base)), Operation: "discard", Path: id, Display: display, ProposedSHA256: workspaceplan.Hash([]byte(display))}
	if err = d.revalidate(ctx); err != nil {
		return nil, err
	}
	ok = true
	return &DiscardProposal{d}, nil
}
func (d *discardState) revalidate(ctx context.Context) error {
	if d.closed.Load() {
		return ErrState
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if d.approvalCtx != nil {
		if err := d.approvalCtx.Err(); err != nil {
			return err
		}
	}
	if err := d.store.check(); err != nil {
		return err
	}
	current, err := identity(filepath.Join(d.store.base, d.id))
	if err != nil || current != d.identity {
		return ErrPrivate
	}
	summary, err := d.store.inspect(ctx, d.id, d.auditStarted)
	if err != nil || !sameSummary(summary, d.summary) {
		return ErrDiscard
	}
	actual, err := inventoryRun(ctx, d.store.base, d.id, d.root)
	if err != nil {
		return err
	}
	if actual.Hash != d.inventory.Hash {
		return ErrDiscard
	}
	return nil
}
func (p *DiscardProposal) Approve(ctx context.Context, reviewer editcontract.Reviewer) (*DiscardPermit, error) {
	if p == nil || p.state == nil {
		return nil, ErrState
	}
	d := p.state
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.attempted.CompareAndSwap(false, true) {
		return nil, editcontract.ErrUsed
	}
	failed := true
	defer func() {
		if failed {
			d.closeLocked()
		}
	}()
	if reviewer == nil {
		return nil, editcontract.ErrApproval
	}
	if err := d.revalidate(ctx); err != nil {
		return nil, err
	}
	deadline, _ := d.ctx.Deadline()
	reviewCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	stop := context.AfterFunc(d.ctx, cancel)
	defer stop()
	decision, err := reviewer.Review(reviewCtx, d.view)
	if reviewCtx.Err() != nil {
		return nil, reviewCtx.Err()
	}
	if err != nil {
		return nil, editcontract.ErrApproval
	}
	if decision != editcontract.Allow {
		return nil, editcontract.ErrDenied
	}
	if err = d.revalidate(ctx); err != nil {
		return nil, err
	}
	failed = false
	d.approvalCtx = ctx
	return &DiscardPermit{d, new(atomic.Bool)}, nil
}

type tombstone struct {
	Version     int       `json:"version"`
	Sequence    int       `json:"sequence"`
	Timestamp   time.Time `json:"timestamp"`
	Store       string    `json:"store"`
	RunID       string    `json:"run_id"`
	Permit      string    `json:"permit"`
	StateHash   string    `json:"state_sha256"`
	PreviewHash string    `json:"preview_sha256"`
	Initial     string    `json:"initial"`
	Files       int       `json:"files"`
	Directories int       `json:"directories"`
	Bytes       int64     `json:"total_size_bytes"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason"`
}

func (d *discardState) auditRecord(sequence int, status, reason string) tombstone {
	return tombstone{1, sequence, time.Now().UTC(), d.store.base, d.id, d.view.ID, d.inventory.Hash, d.view.ProposedSHA256, d.summary.State, d.inventory.Files, d.inventory.Directories, d.inventory.Bytes, status, reason}
}
func (d *discardState) appendAudit(f *os.File, record tombstone) error {
	data, err := json.Marshal(record)
	if err != nil {
		return ErrArtifact
	}
	data = append(data, '\n')
	if record.Sequence == 1 {
		d.initialAuditHash = workspaceplan.Hash(data)
		d.initialAuditBytes = len(data)
	}
	n, err := f.Write(data)
	if err != nil || n != len(data) {
		return ErrArtifact
	}
	if err = d.auditSync(f); err != nil {
		return ErrArtifact
	}
	return nil
}
func (d *discardState) startAudit() (*os.File, error) {
	if err := d.store.check(); err != nil {
		return nil, err
	}
	if err := d.store.root.Mkdir(tombstoneDirectory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, ErrArtifact
	}
	if err := checkPrivate(filepath.Join(d.store.base, tombstoneDirectory)); err != nil {
		return nil, err
	}
	f, err := d.auditOpen(d.store.root, tombstoneDirectory+"/"+d.id+".jsonl")
	if err != nil {
		return nil, ErrArtifact
	}
	if err = d.appendAudit(f, d.auditRecord(1, "started", "none")); err != nil {
		f.Close()
		return nil, err
	}
	for _, path := range []string{tombstoneDirectory, "."} {
		dir, e := openRead(d.store.root, path, true)
		if e != nil {
			f.Close()
			return nil, ErrArtifact
		}
		e = d.auditSync(dir)
		dir.Close()
		if e != nil {
			f.Close()
			return nil, ErrArtifact
		}
	}
	return f, nil
}

func (d *discardState) checkAudit() error {
	if err := checkPrivate(filepath.Join(d.store.base, tombstoneDirectory)); err != nil {
		return err
	}
	info, err := d.store.root.Lstat(tombstoneDirectory + "/" + d.id + ".jsonl")
	if err != nil || !safeRegular(info) || identityInfo(info) != d.auditIdentity {
		return ErrArtifact
	}
	if info.Size() != int64(d.initialAuditBytes) {
		return ErrArtifact
	}
	f, err := openRead(d.store.root, tombstoneDirectory+"/"+d.id+".jsonl", false)
	if err != nil {
		return ErrArtifact
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || workspaceplan.Hash(data) != d.initialAuditHash {
		return ErrArtifact
	}
	return nil
}

// Consume before all metadata writes. After the first removal, any failure is
// conservatively unknown; a synced terminal tombstone is required for success.
func (p *DiscardPermit) Discard(ctx context.Context) (state string, resultErr error) {
	if p == nil || p.state == nil || p.used == nil || !p.used.CompareAndSwap(false, true) {
		return "invalid", editcontract.ErrUsed
	}
	d := p.state
	d.mu.Lock()
	defer d.mu.Unlock()
	started := false
	defer func() {
		if err := d.closeLocked(); err != nil {
			if started {
				state = "unknown_interrupted"
			}
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if err := d.revalidate(ctx); err != nil {
		return "invalid", err
	}
	audit, err := d.startAudit()
	if err != nil {
		return "invalid", err
	}
	defer func() {
		if err := audit.Close(); err != nil {
			if started {
				state = "unknown_interrupted"
			}
			resultErr = errors.Join(resultErr, ErrArtifact)
		}
	}()
	d.auditStarted = true
	info, e := audit.Stat()
	if e != nil {
		return "invalid", ErrArtifact
	}
	d.auditIdentity = identityInfo(info)
	if err = d.revalidate(ctx); err != nil {
		finalErr := d.appendAudit(audit, d.auditRecord(2, "cancelled_before_delete", "revalidation_failed"))
		return "invalid", errors.Join(err, finalErr)
	}
	finish := func(cause error) (string, error) {
		status := "failed"
		state := "invalid"
		if started {
			status = "unknown"
			state = "unknown_interrupted"
		}
		if e := d.appendAudit(audit, d.auditRecord(2, status, "removal_failed")); e != nil {
			return "unknown_interrupted", errors.Join(ErrDiscard, e, cause)
		}
		return state, errors.Join(ErrDiscard, cause)
	}
	for i := len(d.inventory.Entries) - 1; i >= 0; i-- {
		if err = ctx.Err(); err != nil {
			return finish(err)
		}
		if err = d.ctx.Err(); err != nil {
			return finish(err)
		}
		if err = d.approvalCtx.Err(); err != nil {
			return finish(err)
		}
		if err = d.store.check(); err != nil {
			return finish(err)
		}
		if err = d.checkAudit(); err != nil {
			return finish(err)
		}
		current, e := identity(filepath.Join(d.store.base, d.id))
		if e != nil || current != d.identity {
			return finish(ErrPrivate)
		}
		rootInfo, e := d.root.Stat(".")
		if e != nil || uint32(rootInfo.Mode()) != d.inventory.Root.Mode {
			return finish(ErrPrivate)
		}
		item := d.inventory.Entries[i]
		info, e := d.root.Lstat(item.Path)
		if e != nil || identityInfo(info) != item.Identity || uint32(info.Mode()) != item.Mode || info.IsDir() != item.Directory || info.Mode()&os.ModeSymlink != 0 {
			return finish(ErrPrivate)
		}
		if !item.Directory && (!safeRegular(info) || info.Size() != item.Size || info.ModTime().UnixNano() != item.Modified || changeInfo(info) != item.Changed) {
			return finish(ErrPrivate)
		}
		if !item.Directory {
			f, e := openRead(d.root, item.Path, false)
			if e != nil {
				return finish(ErrArtifact)
			}
			opened, e := f.Stat()
			if e != nil || !os.SameFile(info, opened) {
				f.Close()
				return finish(ErrPrivate)
			}
			data, e := io.ReadAll(io.LimitReader(f, MaxPreviewBytes+1))
			f.Close()
			if e != nil || workspaceplan.Hash(data) != item.Hash {
				return finish(ErrPrivate)
			}
			if e = ctx.Err(); e != nil {
				return finish(e)
			}
			if e = d.ctx.Err(); e != nil {
				return finish(e)
			}
		}
		if err = checkPrivate(filepath.Join(d.store.base, d.id, filepath.Dir(filepath.FromSlash(item.Path)))); err != nil {
			return finish(err)
		}
		started = true
		if err = d.remove(d.root, item.Path); err != nil {
			return finish(ErrArtifact)
		}
	}
	if err = ctx.Err(); err != nil {
		return finish(err)
	}
	if err = d.ctx.Err(); err != nil {
		return finish(err)
	}
	if err = d.approvalCtx.Err(); err != nil {
		return finish(err)
	}
	if err = d.store.check(); err != nil {
		return finish(err)
	}
	current, e := identity(filepath.Join(d.store.base, d.id))
	if e != nil || current != d.identity {
		return finish(ErrPrivate)
	}
	started = true
	if err = d.remove(d.store.root, d.id); err != nil {
		return finish(ErrArtifact)
	}
	dir, e := openRead(d.store.root, ".", true)
	if e != nil {
		return finish(ErrArtifact)
	}
	e = d.auditSync(dir)
	dir.Close()
	if e != nil {
		return finish(ErrArtifact)
	}
	if err = ctx.Err(); err != nil {
		return finish(err)
	}
	if err = d.ctx.Err(); err != nil {
		return finish(err)
	}
	if err = d.approvalCtx.Err(); err != nil {
		return finish(err)
	}
	if err = d.checkAudit(); err != nil {
		return finish(err)
	}
	if err = d.appendAudit(audit, d.auditRecord(2, "discarded", "none")); err != nil {
		return "unknown_interrupted", err
	}
	if err = ctx.Err(); err != nil {
		return "unknown_interrupted", err
	}
	if err = d.ctx.Err(); err != nil {
		return "unknown_interrupted", err
	}
	if err = d.approvalCtx.Err(); err != nil {
		return "unknown_interrupted", err
	}
	return "discarded", nil
}

// Compare optional metadata values, not the allocation address returned by inspect.
func sameSummary(a, b Summary) bool {
	ar, br := a.Retention, b.Retention
	a.Retention = nil
	b.Retention = nil
	if a != b {
		return false
	}
	if ar == nil || br == nil {
		return ar == nil && br == nil
	}
	return *ar == *br
}
