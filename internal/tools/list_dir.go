package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	ErrNotDirectory = errors.New("path is not a directory")
	ErrListTooLarge = errors.New("listing exceeds size limit")
)

// ListDir lists a single directory inside the workspace. Listing is
// non-recursive, sorted by name, and refuses to follow symlinks out of the
// workspace. The caller must Close it.
type ListDir struct {
	root       *os.Root
	maxEntries int
	maxBytes   int64
}

func NewListDir(workspace string, maxEntries int, maxBytes int64) (*ListDir, error) {
	if maxEntries <= 0 || maxBytes <= 0 || maxBytes == 1<<63-1 {
		return nil, errors.New("invalid listing limits")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	return &ListDir{root: root, maxEntries: maxEntries, maxBytes: maxBytes}, nil
}

func (l *ListDir) Close() error { return l.root.Close() }
func (*ListDir) Name() string   { return "list_dir" }
func (*ListDir) Description() string {
	return "List one directory inside the workspace, non-recursively."
}
func (*ListDir) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
}

func (l *ListDir) Execute(ctx context.Context, arguments json.RawMessage) (ToolResult, error) {
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
	// Same explicit traversal rejection as read_file, including alternate
	// Windows separators and ADS.
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
	// Root resolves symlinks while enforcing confinement: a path component
	// that escapes the workspace fails instead of being followed.
	info, err := l.root.Stat(path)
	if err != nil {
		return ToolResult{}, errors.New("cannot access directory inside workspace")
	}
	if !info.IsDir() {
		return ToolResult{}, ErrNotDirectory
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	f, err := l.root.Open(path)
	if err != nil {
		return ToolResult{}, errors.New("cannot open directory inside workspace")
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return ToolResult{}, errors.New("cannot inspect directory")
	}
	if !info.IsDir() {
		return ToolResult{}, ErrNotDirectory
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	// ReadDir loads at most maxEntries+1 names: a single extra entry proves
	// the listing is too large without ever loading the whole directory.
	entries, err := f.ReadDir(l.maxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return ToolResult{}, errors.New("cannot read directory")
	}
	if len(entries) > l.maxEntries {
		return ToolResult{}, ErrListTooLarge
	}
	if ctx.Err() != nil {
		return ToolResult{}, ctx.Err()
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var builder strings.Builder
	for _, entry := range entries {
		kind := "file"
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			kind = "symlink"
		case entry.IsDir():
			kind = "directory"
		}
		builder.WriteString(entry.Name())
		builder.WriteByte('\t')
		builder.WriteString(kind)
		builder.WriteByte('\n')
		if int64(builder.Len()) > l.maxBytes {
			return ToolResult{}, ErrListTooLarge
		}
	}
	return ToolResult{Content: builder.String()}, nil
}
