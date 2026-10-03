package managedworkspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

type EvidenceProposal struct{ state *evidenceState }
type EvidencePermit struct {
	state *evidenceState
	used  *atomic.Bool
}
type EvidenceResult struct {
	State string `json:"state"`
	Files int    `json:"files"`
	Bytes int    `json:"bytes"`
}
type evidenceState struct {
	mu               sync.Mutex
	store            *Store
	run              *Run
	active           *os.File
	destination      *evidenceDestination
	bundle           evidenceBundle
	ctx, approvalCtx context.Context
	view             editcontract.Review
	attempted        atomic.Bool
	closed           bool
	auditHash        string
	auditIdentity    Identity
	auditSequence    int
	// Fault seams only for tests, never supplied by CLI/model.
	sync       func(*os.File) error
	openAudit  func(*os.Root, string) (*os.File, error)
	closeAudit func(*os.File) error
	rename     func(*os.File, string, string) error
}

func (p *EvidenceProposal) View() editcontract.Review {
	if p == nil || p.state == nil {
		return editcontract.Review{}
	}
	return p.state.view
}
func (p *EvidenceProposal) Close() error {
	if p == nil || p.state == nil {
		return ErrState
	}
	d := p.state
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closeLocked()
}
func (d *evidenceState) closeLocked() error {
	if d.closed {
		return nil
	}
	d.closed = true
	var errs []error
	if d.active != nil {
		errs = append(errs, d.active.Close())
	}
	if d.destination != nil {
		errs = append(errs, d.destination.root.Close())
	}
	if d.run != nil {
		errs = append(errs, d.run.Close())
	}
	if errors.Join(errs...) != nil {
		return ErrArtifact
	}
	return nil
}

// Prepare reads and pins evidence only. No directories, audit or lock files are created.
func (s *Store) PrepareEvidence(ctx context.Context, id, destination, sourceCheck string, enabled bool) (*EvidenceProposal, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if !enabled {
		return nil, ErrEvidence
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
	r, err := Open(s.base, id)
	if err != nil {
		return nil, err
	}
	d := &evidenceState{store: s, run: r, ctx: ctx, sync: (*os.File).Sync, closeAudit: (*os.File).Close, rename: evidenceRename}
	ok := false
	defer func() {
		if !ok {
			d.closeLocked()
		}
	}()
	d.active, err = exclusiveDirectory(r.root)
	if err != nil {
		return nil, err
	}
	d.destination, err = prepareEvidenceDestination(s, r, destination, sourceCheck)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, ErrState
	}
	idExport := hex.EncodeToString(nonce)
	d.bundle, err = s.buildEvidence(ctx, r, idExport, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	display := fmt.Sprintf("Exportação manual de evidências; sem conteúdo de arquivos ou patch.\noperation=export-evidence\nstore=%s\nrun_id=%s\nrun_state=%s\ndestination=%s\nsource_check=%s\nexport_id=%s\ncontains_file_content=false; contains_patch_content=false; secret_free_guarantee=not_applicable\n", strconv.QuoteToASCII(s.base), id, d.bundle.manifest.RunState, strconv.QuoteToASCII(destination), strconv.QuoteToASCII(sourceCheck), idExport)
	for _, f := range d.bundle.files {
		display += fmt.Sprintf("artifact=%s; bytes=%d; sha256=%s\n", f.path, len(f.data), workspaceplan.Hash(f.data))
	}
	display += fmt.Sprintf("files=6; bytes=%d; limits: files=6, file_bytes=%d, total_bytes=%d, preview_bytes=%d\napproval_deadline_utc=%s\n", d.bundle.total, MaxEvidenceFileBytes, MaxEvidenceBytes, MaxPreviewBytes, deadline.UTC().Format(time.RFC3339Nano))
	for _, warning := range d.bundle.manifest.Warnings {
		display += warning + "\n"
	}
	display += "Destino novo, pai privado validado; sem overwrite ou merge. Auditoria externa e staging residual são preservados em falha.\n"
	if len(display) > MaxPreviewBytes {
		return nil, ErrLimit
	}
	d.view = editcontract.Review{ID: idExport, RunID: id, RootID: workspaceplan.Hash([]byte(s.base)), Operation: "export-evidence", Path: destination, Display: display, ProposedSHA256: workspaceplan.Hash([]byte(display))}
	d.openAudit = func(root *os.Root, name string) (*os.File, error) {
		return root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	}
	if err = d.revalidate(ctx); err != nil {
		return nil, err
	}
	ok = true
	return &EvidenceProposal{d}, nil
}
func (d *evidenceState) contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if d.approvalCtx != nil {
		return d.approvalCtx.Err()
	}
	return nil
}
func (d *evidenceState) revalidate(ctx context.Context) error {
	if d.closed {
		return ErrState
	}
	if err := d.contextError(ctx); err != nil {
		return err
	}
	if err := d.destination.check(d.store); err != nil {
		return err
	}
	return d.revalidateRun(ctx)
}
func (d *evidenceState) revalidateRun(ctx context.Context) error {
	actual, err := d.store.buildEvidence(ctx, d.run, d.bundle.manifest.ExportID, d.bundle.manifest.Created)
	if err != nil {
		return err
	}
	if actual.binding != d.bundle.binding || len(actual.files) != len(d.bundle.files) {
		return ErrEvidence
	}
	for i, f := range actual.files {
		if f.path != d.bundle.files[i].path || !bytes.Equal(f.data, d.bundle.files[i].data) {
			return ErrEvidence
		}
	}
	return d.contextError(ctx)
}
func (p *EvidenceProposal) Approve(ctx context.Context, reviewer editcontract.Reviewer) (*EvidencePermit, error) {
	if p == nil || p.state == nil {
		return nil, ErrState
	}
	d := p.state
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.attempted.CompareAndSwap(false, true) {
		return nil, editcontract.ErrUsed
	}
	ok := false
	defer func() {
		if !ok {
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
	d.approvalCtx = ctx
	ok = true
	return &EvidencePermit{d, new(atomic.Bool)}, nil
}

func evidenceSyncDirectory(root *os.Root, name string, syncFile func(*os.File) error) error {
	f, err := openRead(root, name, true)
	if err != nil {
		return ErrArtifact
	}
	err = syncFile(f)
	closed := f.Close()
	if err != nil || closed != nil {
		return ErrArtifact
	}
	return nil
}
func writeEvidenceFile(root *os.Root, name string, data []byte, syncFile func(*os.File) error) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrArtifact
	}
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = syncFile(f)
	}
	closed := f.Close()
	if err != nil || closed != nil {
		return ErrArtifact
	}
	return nil
}

// Export consumes approval before writes; no cleanup of failed staging/audits.
func (p *EvidencePermit) Export(ctx context.Context) (result EvidenceResult, resultErr error) {
	result.State = "invalid"
	if p == nil || p.state == nil || p.used == nil || !p.used.CompareAndSwap(false, true) {
		return result, editcontract.ErrUsed
	}
	d := p.state
	d.mu.Lock()
	defer d.mu.Unlock()
	started := false
	var audit *os.File
	defer func() {
		if err := d.closeLocked(); err != nil {
			if started {
				result.State = "unknown_interrupted"
			}
			resultErr = errors.Join(resultErr, err)
		}
		if err := d.contextError(ctx); err != nil {
			if started {
				result.State = "unknown_interrupted"
			}
			resultErr = errors.Join(resultErr, err)
		}
		if audit != nil {
			if resultErr != nil {
				result.State = "unknown_interrupted"
				resultErr = errors.Join(resultErr, d.appendEvidenceAudit(audit, d.auditSequence+1, "unknown_interrupted"))
			}
			if err := d.closeAudit(audit); err != nil {
				result.State = "unknown_interrupted"
				resultErr = errors.Join(resultErr, ErrArtifact)
			}
		}
		if err := d.contextError(ctx); err != nil && !errors.Is(resultErr, err) {
			if started {
				result.State = "unknown_interrupted"
			}
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if err := d.revalidate(ctx); err != nil {
		return result, err
	}
	started = true
	result.State = "unknown_interrupted"
	var err error
	audit, err = d.startEvidenceAudit()
	if err != nil {
		return result, err
	}
	if err = d.revalidate(ctx); err != nil {
		return result, err
	}
	stage := ".daimon-evidence-staging-" + d.bundle.manifest.ExportID
	if err = d.destination.root.Mkdir(stage, 0700); err != nil {
		return result, ErrArtifact
	}
	if err = checkPrivate(filepath.Join(d.destination.parent, stage)); err != nil {
		return result, err
	}
	staging, err := d.destination.root.OpenRoot(stage)
	if err != nil {
		return result, ErrArtifact
	}
	defer func() {
		if e := staging.Close(); e != nil {
			result.State = "unknown_interrupted"
			resultErr = errors.Join(resultErr, ErrArtifact)
		}
	}()
	for _, f := range d.bundle.files {
		if err = d.contextError(ctx); err != nil {
			return result, err
		}
		if err = d.destination.check(d.store); err != nil {
			return result, err
		}
		if err = writeEvidenceFile(staging, f.path, f.data, d.sync); err != nil {
			return result, err
		}
	}
	if err = evidenceSyncDirectory(staging, ".", d.sync); err != nil {
		return result, err
	}
	if err = validateEvidenceBundle(staging, d.bundle.manifest, workspaceplan.Hash(d.bundle.files[5].data)); err != nil {
		return result, err
	}
	if err = d.revalidate(ctx); err != nil {
		return result, err
	}
	if err = d.checkEvidenceAudit(audit); err != nil {
		return result, err
	}
	parent, err := openRead(d.destination.root, ".", true)
	if err != nil {
		return result, ErrArtifact
	}
	defer func() {
		if e := parent.Close(); e != nil {
			result.State = "unknown_interrupted"
			resultErr = errors.Join(resultErr, ErrArtifact)
		}
	}()
	if err = d.rename(parent, stage, d.destination.name); err != nil {
		return result, err
	}
	if err = d.sync(parent); err != nil {
		return result, ErrArtifact
	}
	if err = d.destination.checkBoundary(d.store); err != nil {
		return result, err
	}
	if err = d.contextError(ctx); err != nil {
		return result, err
	}
	finalRoot, err := d.destination.root.OpenRoot(d.destination.name)
	if err != nil {
		return result, ErrArtifact
	}
	err = validateEvidenceBundle(finalRoot, d.bundle.manifest, workspaceplan.Hash(d.bundle.files[5].data))
	closed := finalRoot.Close()
	if err != nil || closed != nil {
		return result, ErrArtifact
	}
	if err = d.revalidateRun(ctx); err != nil {
		return result, err
	}
	if err = d.checkEvidenceAudit(audit); err != nil {
		return result, err
	}
	if err = d.appendEvidenceAudit(audit, 2, "exported"); err != nil {
		return result, err
	}
	if err = d.contextError(ctx); err != nil {
		return result, err
	}
	result = EvidenceResult{"exported", 6, d.bundle.total}
	return result, nil
}
