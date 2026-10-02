package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/createcontract"
)

// CreateFile owns one distinct new-file proposal attempt per instance/run.
type CreateFile struct {
	workspace *createcontract.Workspace
	limits    createcontract.Limits
	attempted bool
	proposal  *createcontract.Proposal
	permit    *createcontract.Permit
	arguments [32]byte
}

func NewCreateFile(root string, limits createcontract.Limits) (*CreateFile, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	w, err := createcontract.Open(root)
	if err != nil {
		return nil, err
	}
	return &CreateFile{workspace: w, limits: limits}, nil
}
func (t *CreateFile) Close() error {
	if t == nil || t.workspace == nil {
		return createcontract.ErrInvalid
	}
	t.permit.Invalidate()
	t.permit = nil
	return t.workspace.Close()
}
func (*CreateFile) Name() string { return "create_file" }
func (*CreateFile) Description() string {
	return "Create one new regular file with exact UTF-8 bytes after complete human-approved preview. Explicit workspace opt-in, Linux only, one proposal attempt per run. Target must be absent; safe parents must already exist. No overwrite, directories or symlinks. Permissions 0600; partial file may be visible during writing, failed cleanup reported. Controlled workspace required; no power-loss durability or rollback after success."
}
func (*CreateFile) InputSchema() json.RawMessage { return (&ReplaceFile{}).InputSchema() }
func (t *CreateFile) Prepare(ctx context.Context, raw json.RawMessage) error {
	if t == nil || t.workspace == nil {
		return createcontract.ErrInvalid
	}
	if t.attempted {
		return createcontract.ErrUsed
	}
	t.attempted = true
	if err := ctx.Err(); err != nil {
		return err
	}
	if !createcontract.Supported() {
		return createcontract.ErrUnsupported
	}
	if len(raw) > 6*(t.limits.PathBytes+t.limits.FinalBytes)+128 {
		return createcontract.ErrLimit
	}
	// Share only strict path/content JSON decoding, never replacement proposals.
	p, content, err := replacementArguments(raw)
	if err != nil {
		return err
	}
	proposal, err := t.workspace.Prepare(ctx, p, content, t.limits)
	if err != nil {
		return err
	}
	t.proposal = proposal
	t.arguments = sha256.Sum256(raw)
	return nil
}
func (t *CreateFile) Approve(ctx context.Context, reviewer createcontract.Reviewer) error {
	if t == nil || t.proposal == nil {
		return createcontract.ErrInvalid
	}
	p, err := t.proposal.Approve(ctx, reviewer)
	if err != nil {
		return err
	}
	t.permit = p
	return nil
}
func (t *CreateFile) Execute(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	if t == nil || t.permit == nil {
		return ToolResult{}, createcontract.ErrDenied
	}
	p := t.permit
	t.permit = nil
	if sha256.Sum256(raw) != t.arguments {
		p.Invalidate()
		return ToolResult{}, createcontract.ErrChanged
	}
	if err := p.Apply(ctx); err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Content: "file created"}, nil
}
