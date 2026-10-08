package managedworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"sync/atomic"

	"github.com/netty-linux/daimon/internal/createcontract"
	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspacejournal"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

type preparedOperation struct {
	create  *createcontract.Proposal
	replace *editcontract.Proposal
}
type Proposal struct {
	run          *Run
	view         editcontract.Review
	plan         workspaceplan.Plan
	planBytes    []byte
	prepared     []preparedOperation
	journal      *workspacejournal.Journal
	journalSink  io.Writer
	closers      []closer
	attempted    *atomic.Bool
	runContext   context.Context
	retention    *RetentionSummary
	preimageHook func(string) error
}
type Permit struct {
	proposal        *Proposal
	approvalContext context.Context
	used            *atomic.Bool
	// Private deterministic failure seam used only by package tests.
	applyOne func(context.Context, int) error
}

func (p *Proposal) View() editcontract.Review {
	if p == nil {
		return editcontract.Review{}
	}
	return p.view
}

// Prepare validates every operation and builds the entire exact preview. Its
// metadata writes stay in this run; it makes no changes to output or source.
func (r *Run) Prepare(ctx context.Context, planBytes []byte) (result *Proposal, resultErr error) {
	return r.PrepareWithOptions(ctx, planBytes, ApplyOptions{})
}

func (r *Run) PrepareWithOptions(ctx context.Context, planBytes []byte, options ApplyOptions) (result *Proposal, resultErr error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if options.RetainPreimages && !options.ReplaceEnabled {
		return nil, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrState
	}
	plan, err := workspaceplan.Parse(planBytes, workspaceplan.DefaultLimits())
	if err != nil {
		return nil, err
	}
	if options.RetainPreimages {
		n := 0
		for _, op := range plan.Operations {
			if op.Type == "replace_file" {
				n++
			}
		}
		if n != 1 {
			return nil, ErrState
		}
	}
	if err := r.check(); err != nil {
		return nil, err
	}
	if r.active != nil {
		return nil, ErrState
	}
	active, err := exclusiveDirectory(r.root)
	if err != nil {
		return nil, err
	}
	r.active = active
	defer func() {
		if resultErr != nil && r.lock == nil {
			r.active.Close()
			r.active = nil
		}
	}()
	store, e := OpenStore(r.base)
	if e != nil {
		return nil, e
	}
	tomb, e := store.readTombstone(r.manifest.RunID, nil)
	store.Close()
	if e != nil || tomb != "" {
		return nil, ErrState
	}
	// Retain the exclusion through approval/application until Run.Close, even
	// on failure. A separate lifecycle process cannot discard an active run.
	if r.manifest.Status != "ready" {
		return nil, ErrState
	}
	_, _, _, _, snapshot, err := readSnapshot(ctx, r.output, true)
	if err != nil || snapshot != r.manifest.Snapshot {
		return nil, ErrImport
	}
	// Proposals retain their workspace handles, so reopen once per operation;
	// close them only after the batch finishes or is denied.
	p := &Proposal{run: r, plan: plan, planBytes: append([]byte(nil), planBytes...), runContext: ctx, attempted: new(atomic.Bool)}
	preview := fmt.Sprintf("Cópia gerenciada privada. A origem não será publicada.\nrun_id=%s\nsnapshot_sha256=%s\nplan_sha256=%s\nlimits: operations=2, creates=1, replaces=1, file_bytes=65536, total_bytes=131072, preview_bytes=1048576\n", r.manifest.RunID, snapshot, workspaceplan.Hash(planBytes))
	if options.RetainPreimages {
		p.retention = &RetentionSummary{Version: 1, Requested: true, State: "not_requested"}
		preview += "Retenção privada de pré-imagem solicitada: 1 replace_file, limite 65536 bytes. Conteúdo potencialmente sensível; não exportado nem publicado. Descarte remove com o run, sem garantia de apagamento seguro.\n"
	}
	metadata := make([]workspacejournal.Metadata, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		var prepared preparedOperation
		if op.Type == "create_file" {
			w, e := createcontract.Open(r.output)
			if e != nil {
				p.closePrepared()
				return nil, ErrPrivate
			}
			proposal, e := w.Prepare(ctx, op.Path, []byte(op.Content), createcontract.Limits{FinalBytes: 65536, Lines: 1000, PathBytes: 4096, PreviewBytes: MaxPreviewBytes})
			if e != nil {
				w.Close()
				p.closePrepared()
				return nil, ErrApply
			}
			prepared.create = proposal
			p.closers = append(p.closers, w)
			preview += proposal.View().Display
		} else {
			w, e := editcontract.Open(r.output)
			if e != nil {
				p.closePrepared()
				return nil, ErrPrivate
			}
			proposal, e := w.Prepare(ctx, op.Path, []byte(op.Content), editcontract.Limits{InputBytes: 65536, FinalBytes: 65536, Lines: 1000, PathBytes: 4096, PreviewBytes: MaxPreviewBytes})
			if e != nil {
				w.Close()
				p.closePrepared()
				return nil, ErrApply
			}
			if proposal.View().Original.SHA256 != op.Precondition.SHA256 {
				w.Close()
				p.closePrepared()
				return nil, ErrApply
			}
			prepared.replace = proposal
			p.closers = append(p.closers, w)
			if p.retention == nil {
				preview += proposal.View().Display
			} else {
				v := proposal.View()
				preview += fmt.Sprintf("Replace aprovado; alvo relativo=%s; original_sha256=%s; original_size_bytes=%d; mode=%04o; approved_after_sha256=%s; conteúdo original não exibido.\nConteúdo proposto (completo, escapes ASCII de Go): %s\n", strconv.QuoteToASCII(op.Path), v.Original.SHA256, v.Original.Bytes, v.Original.Mode, v.ProposedSHA256, strconv.QuoteToASCII(op.Content))
			}
		}
		if len(preview) > MaxPreviewBytes {
			p.closePrepared()
			return nil, ErrLimit
		}
		p.prepared = append(p.prepared, prepared)
		metadata = append(metadata, workspacejournal.Metadata{Type: op.Type, Path: op.Path, BeforeSHA256: op.Precondition.SHA256, AfterSHA256: op.Validation.SHA256})
	}
	// All paths, preconditions and complete previews were checked without writes.
	// Now claim this run once, including across processes; never remove the marker.
	if err := r.check(); err != nil {
		p.closePrepared()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		p.closePrepared()
		return nil, err
	}
	r.lock, err = r.root.OpenFile("attempt.lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		p.closePrepared()
		return nil, ErrState
	}
	if r.lock.Sync() != nil || r.syncDirectory(".") != nil {
		p.closePrepared()
		return nil, ErrArtifact
	}
	r.manifest.Retention = copyRetention(p.retention)
	r.manifest.Status = "prepared"
	if err := r.saveManifest(); err != nil {
		p.closePrepared()
		return nil, err
	}
	r.journalFile, err = r.root.OpenFile("artifacts/journal.jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		p.closePrepared()
		return nil, ErrArtifact
	}
	if r.journalFile.Sync() != nil || r.syncDirectory("artifacts") != nil {
		p.closePrepared()
		return nil, ErrArtifact
	}
	p.journalSink = r.journalFile
	p.journal, err = workspacejournal.New(p.journalSink, metadata)
	if err != nil {
		p.closePrepared()
		return nil, ErrArtifact
	}
	p.view = editcontract.Review{ID: workspaceplan.Hash([]byte(preview)), RunID: r.manifest.RunID, RootID: snapshot, Operation: "managed_batch", Display: preview}
	r.pending = p
	return p, nil
}

type closer interface{ Close() error }

func (p *Proposal) closePrepared() {
	for _, c := range p.closers {
		c.Close()
	}
	p.closers = nil
}

// Approve consumes the proposal even on denial, display failure or cancellation.
// Review fields are detached; changing them cannot retarget the private plan.
func (p *Proposal) Approve(ctx context.Context, reviewer editcontract.Reviewer) (*Permit, error) {
	if p == nil || p.attempted == nil || !p.attempted.CompareAndSwap(false, true) {
		return nil, editcontract.ErrUsed
	}
	r := p.run
	r.mu.Lock()
	if err := r.check(); err != nil {
		p.closePrepared()
		r.mu.Unlock()
		return nil, err
	}
	r.mu.Unlock()
	decision := editcontract.Deny
	var err error
	if p.runContext.Err() != nil {
		err = p.runContext.Err()
	} else if ctx.Err() != nil {
		err = ctx.Err()
	} else if _, ok := ctx.Deadline(); !ok {
		err = ErrState
	} else if reviewer == nil {
		err = editcontract.ErrApproval
	} else {
		deadline, _ := p.runContext.Deadline()
		reviewCtx, cancel := context.WithDeadline(ctx, deadline)
		stop := context.AfterFunc(p.runContext, cancel)
		decision, err = reviewer.Review(reviewCtx, p.view)
		if reviewCtx.Err() != nil {
			err = reviewCtx.Err()
		}
		stop()
		cancel()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if checkErr := r.check(); checkErr != nil {
		p.closePrepared()
		return nil, checkErr
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if p.runContext.Err() != nil {
		err = p.runContext.Err()
	}
	if err != nil || decision != editcontract.Allow {
		defer p.closePrepared()
		report := p.newReport("denied")
		for i, op := range p.plan.Operations {
			if p.journal.Append(op.Path, "denied") != nil {
				report.Status = "unknown"
				report.Operations[i].Status = "unknown"
				break
			}
			report.Operations[i].Status = "denied"
		}
		if finishErr := p.finish(report); finishErr != nil {
			return nil, finishErr
		}
		if err != nil {
			if p.runContext.Err() != nil {
				return nil, p.runContext.Err()
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, editcontract.ErrApproval
		}
		return nil, editcontract.ErrDenied
	}
	return &Permit{proposal: p, approvalContext: ctx, used: new(atomic.Bool)}, nil
}

type approvedCreate struct{}

func (approvedCreate) Review(context.Context, createcontract.Review) (createcontract.Decision, error) {
	return createcontract.Allow, nil
}

type approvedReplace struct{}

func (approvedReplace) Review(context.Context, editcontract.Review) (editcontract.Decision, error) {
	return editcontract.Allow, nil
}

func (p *Permit) Apply(ctx context.Context) (Report, error) {
	if p == nil || p.used == nil || !p.used.CompareAndSwap(false, true) {
		return Report{}, editcontract.ErrUsed
	}
	proposal := p.proposal
	deadline, _ := proposal.runContext.Deadline()
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	r := proposal.run
	r.mu.Lock()
	defer r.mu.Unlock()
	defer proposal.closePrepared()
	report := proposal.newReport("failed")
	if err := r.check(); err != nil {
		return report, err
	}
	if err := ctx.Err(); err != nil {
		return proposal.finishResult(report, err)
	}
	if err := p.approvalContext.Err(); err != nil {
		return proposal.finishResult(report, err)
	}
	if err := proposal.runContext.Err(); err != nil {
		return proposal.finishResult(report, err)
	}
	if _, ok := ctx.Deadline(); !ok {
		return proposal.finishResult(report, ErrState)
	}
	_, _, _, _, snapshot, err := readSnapshot(ctx, r.output, true)
	if err != nil || snapshot != r.manifest.Snapshot {
		if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return proposal.finishResult(report, err)
		}
		return proposal.finishResult(report, ErrApply)
	}
	// Full proposed content belongs only to this deliberate artifact, not journal.
	if err := r.writeNew("artifacts/approved-plan.json", proposal.planBytes); err != nil {
		return report, err
	}
	if proposal.retention != nil {
		binding, _ := json.Marshal(struct {
			Plan     string `json:"plan_sha256"`
			Approval string `json:"approval_sha256"`
			Run      string `json:"run_id"`
		}{workspaceplan.Hash(proposal.planBytes), proposal.view.ID, r.manifest.RunID})
		if err := proposal.preimageWrite(ctx, "artifacts/retention-approval.json", binding); err != nil {
			return proposal.finishResult(report, err)
		}
	}
	r.manifest.Status = "applying"
	if err := r.saveManifest(); err != nil {
		return report, err
	}
	for i, op := range proposal.plan.Operations {
		if err := ctx.Err(); err != nil {
			return proposal.finishResult(report, err)
		}
		if err := p.approvalContext.Err(); err != nil {
			return proposal.finishResult(report, err)
		}
		if err := proposal.runContext.Err(); err != nil {
			return proposal.finishResult(report, err)
		}
		if err := r.check(); err != nil {
			return proposal.finishResult(report, err)
		}
		if err := proposal.journal.Append(op.Path, "started"); err != nil {
			report.Status = "unknown"
			report.Operations[i].Status = "unknown"
			return proposal.finishResult(report, ErrArtifact)
		}
		report.Operations[i].Status = "unknown"
		if proposal.retention != nil && op.Type == "replace_file" {
			err = proposal.capturePreimage(ctx, i)
			if err != nil {
				proposal.retention.State = "failed"
				report.Status = "unknown"
				if i > 0 {
					report.Status = "partial"
				}
				_ = proposal.journal.Append(op.Path, "failed")
				return proposal.finishResult(report, errors.Join(ErrArtifact, err))
			}
		}
		if p.applyOne != nil {
			err = p.applyOne(ctx, i)
		} else {
			err = p.applyPrepared(ctx, i)
		}
		if err == nil {
			err = r.syncDirectory("output/" + path.Dir(op.Path))
		}
		if err == nil {
			data, e := r.readArtifact("output/"+op.Path, 65536)
			if e != nil || workspaceplan.Hash(data) != op.Validation.SHA256 {
				err = ErrApply
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = p.approvalContext.Err()
		}
		if err == nil {
			err = proposal.runContext.Err()
		}
		if err != nil {
			report.Status = "unknown"
			if i > 0 {
				report.Status = "partial"
			}
			cause := ErrApply
			if errors.Is(err, context.Canceled) {
				cause = errors.Join(ErrApply, context.Canceled)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				cause = errors.Join(ErrApply, context.DeadlineExceeded)
			}
			if proposal.journal.Append(op.Path, "failed") != nil {
				cause = errors.Join(cause, ErrArtifact)
			}
			return proposal.finishResult(report, cause)
		}
		if err := proposal.journal.Append(op.Path, "succeeded"); err != nil {
			report.Status = "unknown"
			if i > 0 {
				report.Status = "partial"
			}
			return proposal.finishResult(report, ErrArtifact)
		}
		report.Operations[i].Status = "succeeded"
		// A later pre-effect failure must still report earlier confirmed successes.
		report.Status = "partial"
		if err := errors.Join(ctx.Err(), p.approvalContext.Err(), proposal.runContext.Err()); err != nil {
			if i == len(proposal.plan.Operations)-1 {
				report.Status = "unknown"
			}
			return proposal.finishResult(report, err)
		}
	}
	report.Status = "succeeded"
	if proposal.retention != nil {
		report.Retention = copyRetention(proposal.retention)
		if err := r.verifyReport(report); err != nil {
			proposal.retention.State = "unknown"
			report.Status = "unknown"
			return proposal.finishResult(report, err)
		}
	}
	report, err = proposal.finishResult(report, nil)
	if err == nil {
		err = errors.Join(ctx.Err(), p.approvalContext.Err(), proposal.runContext.Err())
		if err != nil {
			report.Status = "unknown"
		}
	}
	return report, err
}

func (p *Permit) applyPrepared(ctx context.Context, i int) error {
	op := p.proposal.prepared[i]
	if op.create != nil {
		permit, err := op.create.Approve(ctx, approvedCreate{})
		if err != nil {
			return err
		}
		return permit.Apply(ctx)
	}
	permit, err := op.replace.Approve(ctx, approvedReplace{})
	if err != nil {
		return err
	}
	return permit.Apply(ctx)
}
func (p *Proposal) newReport(status string) Report {
	r := Report{Version: 1, Status: status, Operations: make([]OperationReport, 0, len(p.plan.Operations))}
	for _, op := range p.plan.Operations {
		r.Operations = append(r.Operations, OperationReport{Type: op.Type, Path: op.Path, BeforeSHA256: op.Precondition.SHA256, AfterSHA256: op.Validation.SHA256, Status: "not_started"})
	}
	return r
}
func (p *Proposal) finish(report Report) error {
	report.Retention = copyRetention(p.retention)
	p.run.manifest.Retention = copyRetention(p.retention)
	data, err := json.Marshal(report)
	if err != nil {
		return ErrArtifact
	}
	if err := p.run.writeNew("artifacts/report.json", append(data, '\n')); err != nil {
		return err
	}
	p.run.manifest.Status = report.Status
	return p.run.saveManifest()
}

// Preserve confirmed operations while refusing a complete-delivery claim when
// report/manifest persistence failed. Never discard a context or artifact failure.
func (p *Proposal) finishResult(report Report, cause error) (Report, error) {
	report.Retention = copyRetention(p.retention)
	if err := p.finish(report); err != nil {
		report.Status = "unknown"
		return report, errors.Join(cause, err)
	}
	return report, cause
}
