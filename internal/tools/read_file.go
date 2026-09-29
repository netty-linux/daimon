package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrUnsafePath   = errors.New("path must remain inside workspace")
	ErrNotRegular   = errors.New("path is not a regular file")
	ErrFileTooLarge = errors.New("file exceeds size limit")
)

// ReadFile owns a traversal-resistant root handle. The caller must Close it.
type ReadFile struct {
	root     *os.Root
	maxBytes int64
}

func NewReadFile(workspace string, maxBytes int64) (*ReadFile, error) {
	if maxBytes <= 0 || maxBytes == 1<<63-1 {
		return nil, errors.New("invalid file size limit")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	return &ReadFile{root: root, maxBytes: maxBytes}, nil
}

func (r *ReadFile) Close() error      { return r.root.Close() }
func (*ReadFile) Name() string        { return "read_file" }
func (*ReadFile) Description() string { return "Read a bounded regular file inside the workspace." }
func (*ReadFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
}

func (r *ReadFile) Execute(ctx context.Context, arguments json.RawMessage) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	var args struct {
		Path *string `json:"path"`
	}
	if err := decodeObject(arguments, "path", &args); err != nil {
		return ToolResult{}, err
	}
	if args.Path == nil {
		return ToolResult{}, fmt.Errorf("%w: path is required", ErrInvalidArguments)
	}
	path := *args.Path
	// Reject traversal explicitly, including alternate Windows separators and ADS.
	if !filepath.IsLocal(path) || strings.ContainsAny(path, ":\x00") || strings.HasPrefix(path, "\\") {
		return ToolResult{}, ErrUnsafePath
	}
	for _, part := range strings.FieldsFunc(path, func(c rune) bool { return c == '/' || c == '\\' }) {
		if part == ".." {
			return ToolResult{}, ErrUnsafePath
		}
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	// Root resolves symlinks while enforcing confinement, without a check/open race.
	info, err := r.root.Stat(path)
	if err != nil {
		return ToolResult{}, errors.New("cannot access file inside workspace")
	}
	if !info.Mode().IsRegular() {
		return ToolResult{}, ErrNotRegular
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	f, err := r.root.Open(path)
	if err != nil {
		return ToolResult{}, errors.New("cannot open file inside workspace")
	}
	defer f.Close()
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	info, err = f.Stat()
	if err != nil {
		return ToolResult{}, errors.New("cannot inspect file")
	}
	if !info.Mode().IsRegular() {
		return ToolResult{}, ErrNotRegular
	}
	if info.Size() > r.maxBytes {
		return ToolResult{}, ErrFileTooLarge
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	data, err := io.ReadAll(io.LimitReader(f, r.maxBytes+1))
	if ctx.Err() != nil {
		return ToolResult{}, ctx.Err()
	}
	if err != nil {
		return ToolResult{}, errors.New("cannot read file")
	}
	if int64(len(data)) > r.maxBytes {
		return ToolResult{}, ErrFileTooLarge
	}
	return ToolResult{Content: string(data)}, nil
}
