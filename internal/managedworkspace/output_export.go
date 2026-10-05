package managedworkspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

var ErrOutputExport = errors.New("exportação de output recusada ou interrompida")

type outputExportError struct{ cause error }

func (e *outputExportError) Error() string { return ErrOutputExport.Error() }
func (e *outputExportError) Unwrap() error { return e.cause }

// OutputManifest describes exactly one deliberately content-bearing file.
// It is not an Evidence Export manifest and contains no file bytes.
type OutputManifest struct {
	Version     int       `json:"version"`
	ExportID    string    `json:"export_id"`
	RunID       string    `json:"run_id"`
	Path        string    `json:"output_relative_path"`
	SHA256      string    `json:"output_sha256"`
	Bytes       int       `json:"output_size_bytes"`
	Destination string    `json:"destination_name"`
	Approval    string    `json:"approval_sha256"`
	RunBinding  string    `json:"run_binding_sha256"`
	Created     time.Time `json:"created"`
	Integrity   string    `json:"integrity"`
	Warning     string    `json:"warning"`
}
type outputAudit struct {
	OutputManifest
	Sequence  int       `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason"`
}
type OutputResult struct {
	State        string
	Files, Bytes int
}
type OutputProposal struct{ state *outputState }
type OutputPermit struct {
	state *outputState
	used  *atomic.Bool
}
type outputState struct {
	mu                                     sync.Mutex
	store                                  *Store
	run                                    *Run
	parent, area                           *os.Root
	parentLock, runLock                    *os.File
	parentPath, areaPath, areaName, source string
	parentID, sourceID, areaID             Identity
	areaExists, closed                     bool
	ctx, approvalCtx                       context.Context
	attempted                              atomic.Bool
	manifest                               OutputManifest
	owner, manifestBytes, auditBytes       []byte
	view                                   editcontract.Review
	// Test-only deterministic failure points, never exposed to CLI/model.
	hook func(string) error
}

func validOutputName(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return s[0] != '-' && s[len(s)-1] != '-'
}

// PrepareOutput never writes the run, output, source or managed export area.
// sourceCheck is structural only; its bytes are never opened or hashed.
func (s *Store) PrepareOutput(ctx context.Context, id, path, destination, sourceCheck string, enabled bool) (*OutputProposal, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if !enabled {
		return nil, ErrOutputExport
	}
	if !validID(id) || !workspaceplan.ValidPath(path, workspaceplan.DefaultLimits()) || !validOutputName(destination) {
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
	d := &outputState{store: s, run: r, ctx: ctx, source: sourceCheck}
	ok := false
	defer func() {
		if !ok {
			d.closeLocked()
		}
	}()
	d.runLock, err = exclusiveDirectory(r.root)
	if err != nil {
		return nil, err
	}
	if !strictEvidenceAbsolute(sourceCheck) || workspaceplan.Hash([]byte(sourceCheck)) != r.Manifest().SourceReference || checkLocalPath(sourceCheck) != nil {
		return nil, ErrPrivate
	}
	d.sourceID, err = identity(sourceCheck)
	if err != nil {
		return nil, err
	}
	d.parentPath = filepath.Dir(s.base)
	if checkPrivate(d.parentPath) != nil {
		return nil, ErrPrivate
	}
	d.parentID, err = identity(d.parentPath)
	if err != nil {
		return nil, err
	}
	d.parent, err = os.OpenRoot(d.parentPath)
	if err != nil {
		return nil, ErrPrivate
	}
	d.parentLock, err = exclusiveDirectory(d.parent)
	if err != nil {
		return nil, err
	}
	d.areaName = ".daimon-output-exports-" + workspaceplan.Hash([]byte(fmt.Sprintf("%s:%d:%d", s.base, s.identity.Device, s.identity.Inode)))
	d.areaPath = filepath.Join(d.parentPath, d.areaName)
	if overlaps(d.areaPath, s.base) || overlaps(d.areaPath, sourceCheck) {
		return nil, ErrPrivate
	}
	d.owner, _ = json.Marshal(struct {
		Version  int      `json:"version"`
		Store    string   `json:"store_reference_sha256"`
		Identity Identity `json:"store_identity"`
	}{1, workspaceplan.Hash([]byte(s.base)), s.identity})
	d.area, err = d.parent.OpenRoot(d.areaName)
	if err == nil {
		d.areaExists = true
		if checkPrivate(d.areaPath) != nil {
			return nil, ErrPrivate
		}
		d.areaID, err = identity(d.areaPath)
		if err != nil {
			return nil, err
		}
		owner, e := d.areaRead("_owner.json", 4096)
		if e != nil || !bytes.Equal(owner, d.owner) {
			return nil, ErrPrivate
		}
	} else if !os.IsNotExist(err) {
		return nil, ErrPrivate
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, ErrState
	}
	exportID := hex.EncodeToString(nonce)
	bundle, err := s.buildEvidence(ctx, r, exportID, time.Now().UTC())
	if err != nil || bundle.manifest.RunState != "succeeded" {
		return nil, ErrOutputExport
	}
	data, err := r.captureRead("output/"+path, MaxFileBytes)
	if err != nil {
		return nil, ErrArtifact
	}
	var inventory evidenceInventory
	if json.Unmarshal(bundle.files[4].data, &inventory) != nil {
		return nil, ErrArtifact
	}
	match := false
	for _, f := range inventory.Entries {
		if f.Path == path && f.Type == "file" && f.Bytes == len(data) && f.SHA256 == workspaceplan.Hash(data) {
			match = true
		}
	}
	if !match {
		return nil, ErrArtifact
	}
	d.manifest = OutputManifest{Version: 1, ExportID: exportID, RunID: id, Path: path, SHA256: workspaceplan.Hash(data), Bytes: len(data), Destination: destination, RunBinding: bundle.binding, Created: bundle.manifest.Created, Integrity: "verified_bytes", Warning: "Conteúdo potencialmente sensível; sem garantia de detecção de segredos, criptografia ou apagamento seguro. Origem não modificada."}
	deadline, _ := ctx.Deadline()
	display := fmt.Sprintf("Output Export experimental, Linux amd64. Um arquivo; não é publicação na origem.\nrun_id=%s\noutput_relative_path=%s\noutput_sha256=%s\noutput_size_bytes=%d\ndestination_name=%s\nmanaged_area=%s\nexport_id=%s\nlimits: files=1, file_bytes=%d, manifest_bytes=4096, audit_bytes=16384\napproval_deadline_utc=%s\n%s\nUID proprietário e administradores confiáveis; sem preimage/patch export, rollback, replay ou limpeza automática.\n", id, strconv.QuoteToASCII(path), d.manifest.SHA256, len(data), strconv.QuoteToASCII(destination), strconv.QuoteToASCII(d.areaName), exportID, MaxFileBytes, deadline.UTC().Format(time.RFC3339Nano), d.manifest.Warning)
	if len(display) > MaxPreviewBytes {
		return nil, ErrLimit
	}
	binding := workspaceplan.Hash([]byte(display))
	d.manifest.Approval = binding
	d.manifestBytes, err = json.Marshal(d.manifest)
	if err != nil || len(d.manifestBytes) > 4096 {
		return nil, ErrLimit
	}
	d.view = editcontract.Review{ID: binding, RunID: id, Operation: "export-output", Path: path, Display: display, ProposedSHA256: d.manifest.SHA256, RootID: workspaceplan.Hash([]byte(s.base))}
	if err = d.revalidate(ctx, false); err != nil {
		return nil, err
	}
	ok = true
	return &OutputProposal{d}, nil
}

func (d *outputState) checkContext(ctx context.Context) error {
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
func (d *outputState) step(ctx context.Context, phase string) error {
	if err := d.checkContext(ctx); err != nil {
		return err
	}
	if d.hook != nil {
		if err := d.hook(phase); err != nil {
			return errors.Join(ErrArtifact, err)
		}
	}
	return d.checkContext(ctx)
}
func (d *outputState) areaRead(name string, limit int) ([]byte, error) {
	return readPrivateRegular(d.area, d.areaPath, name, limit)
}
func (d *outputState) boundary() error {
	if d.closed || d.store.check() != nil || checkPrivate(d.parentPath) != nil || checkLocalPath(d.source) != nil {
		return ErrPrivate
	}
	p, e := identity(d.parentPath)
	if e != nil || p != d.parentID {
		return ErrPrivate
	}
	i, e := d.parent.Stat(".")
	if e != nil || identityInfo(i) != d.parentID {
		return ErrPrivate
	}
	source, e := identity(d.source)
	if e != nil || source != d.sourceID {
		return ErrPrivate
	}
	if overlaps(d.areaPath, d.source) || overlaps(d.areaPath, d.store.base) {
		return ErrPrivate
	}
	if !d.areaExists {
		if _, e = d.parent.Lstat(d.areaName); !os.IsNotExist(e) {
			return ErrPrivate
		}
		return nil
	}
	if checkPrivate(d.areaPath) != nil {
		return ErrPrivate
	}
	a, e := identity(d.areaPath)
	if e != nil || a != d.areaID {
		return ErrPrivate
	}
	i, e = d.area.Stat(".")
	if e != nil || identityInfo(i) != d.areaID {
		return ErrPrivate
	}
	owner, e := d.areaRead("_owner.json", 4096)
	if e != nil || !bytes.Equal(owner, d.owner) {
		return ErrPrivate
	}
	return nil
}
func (d *outputState) revalidate(ctx context.Context, published bool) error {
	encoded, encodeErr := json.Marshal(d.manifest)
	if encodeErr != nil || !bytes.Equal(encoded, d.manifestBytes) || d.manifest.Approval != d.view.ID || workspaceplan.Hash([]byte(d.view.Display)) != d.view.ID {
		return ErrOutputExport
	}
	if err := d.checkContext(ctx); err != nil {
		return err
	}
	if err := d.boundary(); err != nil {
		return err
	}
	if d.areaExists && !published {
		if _, err := d.area.Lstat(d.manifest.Destination); !os.IsNotExist(err) {
			return ErrOutputExport
		}
	}
	bundle, err := d.store.buildEvidence(ctx, d.run, d.manifest.ExportID, d.manifest.Created)
	if err != nil || bundle.manifest.RunState != "succeeded" || bundle.binding != d.manifest.RunBinding {
		return ErrOutputExport
	}
	b, err := d.run.captureRead("output/"+d.manifest.Path, MaxFileBytes)
	if err != nil || len(b) != d.manifest.Bytes || workspaceplan.Hash(b) != d.manifest.SHA256 {
		return ErrArtifact
	}
	return d.checkContext(ctx)
}
func (p *OutputProposal) View() editcontract.Review {
	if p == nil || p.state == nil {
		return editcontract.Review{}
	}
	return p.state.view
}
func (p *OutputProposal) Close() error {
	if p == nil || p.state == nil {
		return ErrState
	}
	d := p.state
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closeLocked()
}
func (d *outputState) closeLocked() error {
	if d.closed {
		return nil
	}
	d.closed = true
	var errs []error
	if d.area != nil {
		errs = append(errs, d.area.Close())
	}
	if d.parentLock != nil {
		errs = append(errs, d.parentLock.Close())
	}
	if d.runLock != nil {
		errs = append(errs, d.runLock.Close())
	}
	if d.parent != nil {
		errs = append(errs, d.parent.Close())
	}
	if d.run != nil {
		errs = append(errs, d.run.Close())
	}
	if d.hook != nil {
		errs = append(errs, d.hook("handles_closed"))
	}
	if errors.Join(errs...) != nil {
		return ErrArtifact
	}
	return nil
}
func (p *OutputProposal) Approve(ctx context.Context, reviewer editcontract.Reviewer) (*OutputPermit, error) {
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
	if err := d.revalidate(ctx, false); err != nil {
		return nil, err
	}
	deadline, _ := d.ctx.Deadline()
	reviewCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	stop := context.AfterFunc(d.ctx, cancel)
	defer stop()
	decision, err := reviewer.Review(reviewCtx, d.view)
	if e := reviewCtx.Err(); e != nil {
		return nil, e
	}
	if err != nil {
		return nil, editcontract.ErrApproval
	}
	if decision != editcontract.Allow {
		return nil, editcontract.ErrDenied
	}
	if err = d.revalidate(ctx, false); err != nil {
		return nil, err
	}
	d.approvalCtx = ctx
	ok = true
	return &OutputPermit{d, new(atomic.Bool)}, nil
}

func (d *outputState) write(ctx context.Context, root *os.Root, name string, data []byte) error {
	if err := d.step(ctx, "open:"+name); err != nil {
		return err
	}
	f, err := openPreimageNew(root, name)
	if err != nil {
		return err
	}
	cause := d.step(ctx, "write:"+name)
	if cause == nil {
		var n int
		n, cause = f.Write(data)
		if cause == nil && n != len(data) {
			cause = ErrArtifact
		}
	}
	if cause == nil {
		cause = d.step(ctx, "sync:"+name)
		if cause == nil {
			cause = f.Sync()
		}
	}
	closed := f.Close()
	hook := d.step(ctx, "close:"+name)
	if cause != nil || closed != nil || hook != nil {
		return errors.Join(ErrArtifact, cause, hook)
	}
	return evidenceSyncDirectory(root, filepath.ToSlash(filepath.Dir(name)), (*os.File).Sync)
}
func (d *outputState) provision(ctx context.Context) error {
	if d.areaExists {
		return nil
	}
	if err := d.step(ctx, "provision"); err != nil {
		return err
	}
	if d.parent.Mkdir(d.areaName, 0700) != nil {
		return ErrArtifact
	}
	if checkPrivate(d.areaPath) != nil {
		return ErrPrivate
	}
	var err error
	d.area, err = d.parent.OpenRoot(d.areaName)
	if err != nil {
		return ErrArtifact
	}
	d.areaID, err = identity(d.areaPath)
	if err != nil {
		return err
	}
	d.areaExists = true
	if err = d.write(ctx, d.area, "_owner.json", d.owner); err != nil {
		return err
	}
	return evidenceSyncDirectory(d.parent, ".", (*os.File).Sync)
}
func (d *outputState) verifyPackage(root *os.Root, directory string) error {
	if checkPrivate(directory) != nil {
		return ErrPrivate
	}
	physical, err := identity(directory)
	if err != nil {
		return ErrPrivate
	}
	opened, err := root.Stat(".")
	if err != nil || identityInfo(opened) != physical {
		return ErrPrivate
	}
	f, err := openRead(root, ".", true)
	if err != nil {
		return ErrArtifact
	}
	entries, err := f.ReadDir(3)
	closed := f.Close()
	if err != nil || closed != nil || len(entries) != 2 {
		return ErrArtifact
	}
	m, e := readPrivateRegular(root, directory, "manifest.json", 4096)
	if e != nil || !bytes.Equal(m, d.manifestBytes) {
		return ErrArtifact
	}
	b, e := readPrivateRegular(root, directory, "output.bin", MaxFileBytes)
	if e != nil || len(b) != d.manifest.Bytes || workspaceplan.Hash(b) != d.manifest.SHA256 {
		return ErrArtifact
	}
	return nil
}
func (d *outputState) audit(ctx context.Context, f *os.File, sequence int, status, reason string) error {
	if err := d.step(ctx, "audit:"+status); err != nil {
		return err
	}
	record := outputAudit{d.manifest, sequence, time.Now().UTC(), status, reason}
	b, err := json.Marshal(record)
	if err != nil {
		return ErrArtifact
	}
	b = append(b, '\n')
	if len(d.auditBytes)+len(b) > 16384 {
		return ErrLimit
	}
	n, err := f.Write(b)
	if err != nil || n != len(b) {
		return ErrArtifact
	}
	if err = d.step(ctx, "sync_audit:"+status); err != nil {
		return err
	}
	if f.Sync() != nil {
		return ErrArtifact
	}
	d.auditBytes = append(d.auditBytes, b...)
	return d.checkContext(ctx)
}
func (d *outputState) checkAudit() error {
	b, e := d.areaRead("_audit/"+d.manifest.ExportID+".jsonl", 16384)
	if e != nil || !bytes.Equal(b, d.auditBytes) {
		return ErrArtifact
	}
	return nil
}

// Export publishes only into the approved private managed namespace. Failure
// preserves staging/audit; it never restores, deletes or edits the input run.
func (p *OutputPermit) Export(ctx context.Context) (result OutputResult, resultErr error) {
	result.State = "not_published"
	if p == nil || p.state == nil || p.used == nil || !p.used.CompareAndSwap(false, true) {
		return result, editcontract.ErrUsed
	}
	d := p.state
	d.mu.Lock()
	defer d.mu.Unlock()
	var audit, parent *os.File
	var staging *os.Root
	published := false
	defer func() {
		if e := d.checkContext(ctx); e != nil {
			resultErr = errors.Join(resultErr, e)
		}
		if audit != nil && resultErr != nil {
			_ = d.audit(context.WithoutCancel(ctx), audit, 3, "unknown_interrupted", "operation_failed")
		}
		for _, f := range []*os.File{audit, parent} {
			if f != nil {
				if f.Close() != nil {
					resultErr = errors.Join(resultErr, ErrArtifact)
				}
			}
		}
		if staging != nil && staging.Close() != nil {
			resultErr = errors.Join(resultErr, ErrArtifact)
		}
		if d.closeLocked() != nil {
			resultErr = errors.Join(resultErr, ErrArtifact)
		}
		if e := d.checkContext(ctx); e != nil {
			resultErr = errors.Join(resultErr, e)
		}
		if resultErr != nil {
			if published {
				result.State = "unknown_interrupted"
			} else {
				result.State = "not_published"
			}
			result.Files = 0
			resultErr = &outputExportError{resultErr}
		}
	}()
	if err := d.revalidate(ctx, false); err != nil {
		return result, err
	}
	if err := d.provision(ctx); err != nil {
		return result, err
	}
	if err := d.revalidate(ctx, false); err != nil {
		return result, err
	}
	if err := d.area.Mkdir("_audit", 0700); err != nil && !os.IsExist(err) {
		return result, ErrArtifact
	}
	if checkPrivate(filepath.Join(d.areaPath, "_audit")) != nil {
		return result, ErrPrivate
	}
	var err error
	audit, err = openPreimageNew(d.area, "_audit/"+d.manifest.ExportID+".jsonl")
	if err != nil {
		return result, err
	}
	if err = d.audit(ctx, audit, 1, "started", "none"); err != nil {
		return result, err
	}
	if err = d.step(ctx, "sync_audit_directory"); err != nil {
		return result, err
	}
	if err = evidenceSyncDirectory(d.area, "_audit", (*os.File).Sync); err != nil {
		return result, err
	}
	if err = evidenceSyncDirectory(d.area, ".", (*os.File).Sync); err != nil {
		return result, err
	}
	stage := ".staging-" + d.manifest.ExportID
	if d.area.Mkdir(stage, 0700) != nil {
		return result, ErrArtifact
	}
	if checkPrivate(filepath.Join(d.areaPath, stage)) != nil {
		return result, ErrPrivate
	}
	staging, err = d.area.OpenRoot(stage)
	if err != nil {
		return result, ErrArtifact
	}
	data, err := d.run.captureRead("output/"+d.manifest.Path, MaxFileBytes)
	if err != nil || len(data) != d.manifest.Bytes || workspaceplan.Hash(data) != d.manifest.SHA256 {
		return result, ErrArtifact
	}
	if err = d.write(ctx, staging, "output.bin", data); err != nil {
		return result, err
	}
	if err = d.write(ctx, staging, "manifest.json", d.manifestBytes); err != nil {
		return result, err
	}
	if err = d.step(ctx, "verify_staging"); err != nil {
		return result, err
	}
	if err = d.verifyPackage(staging, filepath.Join(d.areaPath, stage)); err != nil {
		return result, err
	}
	if err = d.revalidate(ctx, false); err != nil {
		return result, err
	}
	if err = d.checkAudit(); err != nil {
		return result, err
	}
	parent, err = openRead(d.area, ".", true)
	if err != nil {
		return result, ErrArtifact
	}
	if err = d.step(ctx, "before_publish"); err != nil {
		return result, err
	}
	// Revalidate after test/interruption boundary, immediately before rename.
	if err = d.revalidate(ctx, false); err != nil {
		return result, err
	}
	if err = d.verifyPackage(staging, filepath.Join(d.areaPath, stage)); err != nil {
		return result, err
	}
	if err = d.checkAudit(); err != nil {
		return result, err
	}
	if err = evidenceRename(parent, stage, d.manifest.Destination); err != nil {
		return result, err
	}
	published = true
	if err = d.step(ctx, "after_publish"); err != nil {
		return result, err
	}
	if err = d.step(ctx, "sync_published_parent"); err != nil {
		return result, err
	}
	if parent.Sync() != nil {
		return result, ErrArtifact
	}
	if err = d.revalidate(ctx, true); err != nil {
		return result, err
	}
	final, err := d.area.OpenRoot(d.manifest.Destination)
	if err != nil {
		return result, ErrArtifact
	}
	err = d.verifyPackage(final, filepath.Join(d.areaPath, d.manifest.Destination))
	closed := final.Close()
	if err != nil || closed != nil {
		return result, ErrArtifact
	}
	if err = d.checkAudit(); err != nil {
		return result, err
	}
	if err = d.audit(ctx, audit, 2, "exported", "none"); err != nil {
		return result, err
	}
	if err = d.step(ctx, "final_close"); err != nil {
		return result, err
	}
	result = OutputResult{"exported", 1, d.manifest.Bytes}
	return result, nil
}
