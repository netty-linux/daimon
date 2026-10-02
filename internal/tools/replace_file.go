package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/editcontract"
)

// ReplaceFile has one proposal attempt per instance/run. The authorizer prepares
// and approves it before Execute; Execute alone cannot obtain write permission.
// It is synchronous and owns its workspace. The caller must Close it.
type ReplaceFile struct {
	workspace *editcontract.Workspace
	limits    editcontract.Limits
	attempted bool
	proposal  *editcontract.Proposal
	permit    *editcontract.Permit
	arguments [32]byte
}

func NewReplaceFile(workspace string, limits editcontract.Limits) (*ReplaceFile, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	w, err := editcontract.Open(workspace)
	if err != nil {
		return nil, err
	}
	return &ReplaceFile{workspace: w, limits: limits}, nil
}
func (t *ReplaceFile) Close() error {
	if t == nil || t.workspace == nil {
		return editcontract.ErrInvalid
	}
	t.permit.Invalidate()
	t.permit = nil
	return t.workspace.Close()
}
func (*ReplaceFile) Name() string { return "replace_file" }
func (*ReplaceFile) Description() string {
	return "Replace one existing regular file with exact UTF-8 content, after complete human-approved preview. One proposal attempt per run; Linux only. No creation, deletion or symlinks/hard links."
}
func (*ReplaceFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)
}

func replacementArguments(raw json.RawMessage) (string, []byte, error) {
	if !utf8.Valid(raw) {
		return "", nil, ErrInvalidArguments
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return "", nil, ErrInvalidArguments
	}
	seen := map[string]bool{}
	var path, content *string
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return "", nil, ErrInvalidArguments
		}
		name, ok := key.(string)
		if !ok || seen[name] || (name != "path" && name != "content") {
			return "", nil, ErrInvalidArguments
		}
		seen[name] = true
		var value *string
		if err := d.Decode(&value); err != nil || value == nil {
			return "", nil, ErrInvalidArguments
		}
		if name == "path" {
			path = value
		} else {
			content = value
		}
	}
	if _, err := d.Token(); err != nil {
		return "", nil, ErrInvalidArguments
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF || path == nil || content == nil {
		return "", nil, ErrInvalidArguments
	}
	return *path, []byte(*content), nil
}

func (t *ReplaceFile) Prepare(ctx context.Context, raw json.RawMessage) error {
	if t == nil || t.workspace == nil {
		return editcontract.ErrInvalid
	}
	if t.attempted {
		return editcontract.ErrUsed
	}
	t.attempted = true
	if err := ctx.Err(); err != nil {
		return err
	}
	if !editcontract.Supported() {
		return editcontract.ErrUnsupported
	}
	// Even bypassing the loop cannot force unbounded argument decoding.
	if len(raw) > 6*(t.limits.PathBytes+t.limits.FinalBytes)+128 {
		return editcontract.ErrLimit
	}
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
func (t *ReplaceFile) Approve(ctx context.Context, reviewer editcontract.Reviewer) error {
	if t == nil || t.proposal == nil {
		return editcontract.ErrInvalid
	}
	permit, err := t.proposal.Approve(ctx, reviewer)
	if err != nil {
		return err
	}
	t.permit = permit
	return nil
}
func (t *ReplaceFile) Execute(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	if t == nil || t.permit == nil {
		return ToolResult{}, editcontract.ErrDenied
	}
	permit := t.permit
	t.permit = nil
	if sha256.Sum256(raw) != t.arguments {
		permit.Invalidate()
		return ToolResult{}, editcontract.ErrChanged
	}
	if err := permit.Apply(ctx); err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Content: "file replaced"}, nil
}
