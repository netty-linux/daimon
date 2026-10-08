package managedworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

var ErrPreimageExport = errors.New("exportação de pré-imagem recusada ou interrompida")

type PreimageManifest struct {
	Version        int       `json:"version"`
	ExportID       string    `json:"export_id"`
	RunID          string    `json:"run_id"`
	OperationID    string    `json:"operation_id"`
	OperationIndex int       `json:"operation_index"`
	Path           string    `json:"relative_path"`
	Expected       string    `json:"expected_before_sha256"`
	Captured       string    `json:"captured_before_sha256"`
	After          string    `json:"approved_after_sha256"`
	Size           int       `json:"preimage_size_bytes"`
	Destination    string    `json:"destination_name"`
	Approval       string    `json:"approval_sha256"`
	RunBinding     string    `json:"run_binding_sha256"`
	Created        time.Time `json:"created"`
	Integrity      string    `json:"integrity"`
	Warning        string    `json:"warning"`
}

type PreimageProposal struct{ proposal *OutputProposal }
type PreimagePermit struct{ permit *OutputPermit }
type preimageExportError struct{ cause error }

func (e *preimageExportError) Error() string        { return ErrPreimageExport.Error() }
func (e *preimageExportError) Unwrap() error        { return e.cause }
func (e *preimageExportError) Is(target error) bool { return target == ErrPreimageExport }
func preimageExportFailure(err error) error {
	if err == nil {
		return nil
	}
	return &preimageExportError{err}
}

func (s *Store) PreparePreimage(ctx context.Context, id, operation, destination, sourceCheck string, enabled bool) (*PreimageProposal, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if !enabled {
		return nil, ErrPreimageExport
	}
	if !validHash(operation) {
		return nil, ErrPreimageExport
	}
	p, e := s.prepareContentExport(ctx, id, operation, destination, sourceCheck, true, true)
	if e != nil {
		return nil, preimageExportFailure(e)
	}
	return &PreimageProposal{p}, nil
}
func (p *PreimageProposal) View() editcontract.Review {
	if p == nil || p.proposal == nil {
		return editcontract.Review{}
	}
	return p.proposal.View()
}
func (p *PreimageProposal) Close() error {
	if p == nil || p.proposal == nil {
		return ErrState
	}
	return p.proposal.Close()
}
func (p *PreimageProposal) Approve(ctx context.Context, r editcontract.Reviewer) (*PreimagePermit, error) {
	if p == nil || p.proposal == nil {
		return nil, ErrState
	}
	v, e := p.proposal.Approve(ctx, r)
	if e != nil {
		return nil, preimageExportFailure(e)
	}
	return &PreimagePermit{v}, nil
}
func (p *PreimagePermit) Export(ctx context.Context) (OutputResult, error) {
	if p == nil || p.permit == nil {
		return OutputResult{State: "not_published"}, editcontract.ErrUsed
	}
	r, e := p.permit.Export(ctx)
	return r, preimageExportFailure(e)
}

// Selection is exclusively the validated retention operation ID. Storage names
// are runtime-derived, never paths accepted from a caller.
func (d *outputState) selectPreimage(operation string) ([]byte, error) {
	s := d.run.Manifest().Retention
	if !validHash(operation) || s == nil || s.State != "verified" || s.Count != 1 || s.OperationID != operation || !retentionValid(s) {
		return nil, ErrArtifact
	}
	raw, e := d.run.captureRead("preimages/"+operation+".json", 8192)
	if e != nil {
		return nil, ErrArtifact
	}
	m, e := decodePreimage(raw)
	if e != nil || m.State != "verified" || m.RunID != d.run.ID() || m.OperationID != operation || m.StorageID != operation || m.Captured != m.Expected || m.Size < 0 || m.Size > MaxFileBytes {
		return nil, ErrArtifact
	}
	if d.preimage != nil && *d.preimage != m {
		return nil, ErrArtifact
	}
	b, e := d.run.captureRead("preimages/"+operation+".bin", MaxFileBytes)
	if e != nil || len(b) != m.Size || workspaceplan.Hash(b) != m.Captured {
		return nil, ErrArtifact
	}
	// buildEvidence already verifies approved plan, report, retention and journals.
	d.preimage = &m
	return b, nil
}
func (d *outputState) contentName() string {
	if d.preimage != nil {
		return "preimage.bin"
	}
	return "output.bin"
}
func (d *outputState) readContent() ([]byte, error) {
	if d.preimage != nil {
		return d.selectPreimage(d.preimage.OperationID)
	}
	return d.run.captureRead("output/"+d.manifest.Path, MaxFileBytes)
}
func (d *outputState) encodedManifest() ([]byte, error) {
	if d.preimage == nil {
		return json.Marshal(d.manifest)
	}
	m := d.preimage
	if d.manifest.Path != m.Path || d.manifest.SHA256 != m.Captured || d.manifest.Bytes != m.Size {
		return nil, ErrArtifact
	}
	return json.Marshal(PreimageManifest{1, d.manifest.ExportID, d.manifest.RunID, m.OperationID, m.Index, m.Path, m.Expected, m.Captured, m.After, m.Size, d.manifest.Destination, d.manifest.Approval, d.manifest.RunBinding, d.manifest.Created, d.manifest.Integrity, d.manifest.Warning})
}
func (d *outputState) preimageDisplay() string {
	m := d.preimage
	return fmt.Sprintf("capability=preimage_export\nrun_integrity_state=verified\npreimage_format_version=%d\nsingle_use=true\noperation_id=%s\noperation_index=%d\nrelative_path=%q\nexpected_before_sha256=%s\ncaptured_before_sha256=%s\napproved_after_sha256=%s\npreimage_size_bytes=%d\n", m.Version, m.OperationID, m.Index, m.Path, m.Expected, m.Captured, m.After, m.Size)
}
