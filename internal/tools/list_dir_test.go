package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newLister(t *testing.T, root string, maxEntries int, maxBytes int64) *ListDir {
	t.Helper()
	l, err := NewListDir(root, maxEntries, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})
	return l
}

func listLines(content string) []string {
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

func TestListDirContent(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"zed.txt", "alpha.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("alpha.md", filepath.Join(root, "link.md")); err != nil {
		t.Fatalf("symlink security test requires symlink support: %v", err)
	}
	l := newLister(t, root, 10, 1024)
	result, err := l.Execute(context.Background(), argsFor("."))
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	// Deterministic order and one kind label per entry; symlinks are labeled,
	// not followed.
	want := []string{"alpha.md\tfile", "folder\tdirectory", "link.md\tsymlink", "zed.txt\tfile"}
	if got := listLines(result.Content); len(got) != len(want) {
		t.Fatalf("listing=%q", result.Content)
	}
	for i, line := range listLines(result.Content) {
		if line != want[i] {
			t.Fatalf("line %d=%q want=%q", i, line, want[i])
		}
	}
}

func TestListDirBoundaries(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "allowed.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	l := newLister(t, root, 10, 1024)
	for _, tc := range []struct {
		name, path string
		wantErr    error
	}{
		{"absolute", filepath.Join(root, "folder"), ErrUnsafePath},
		{"parent", "../outside", ErrUnsafePath},
		{"nested traversal", "folder/../folder", ErrUnsafePath},
		{"backslash traversal", `..\outside`, ErrUnsafePath},
		{"drive", `C:\Users`, ErrUnsafePath},
		{"UNC", `\\server\share`, ErrUnsafePath},
		{"stream", "allowed.txt:secret", ErrUnsafePath},
		{"empty", "", ErrUnsafePath},
		{"file", "allowed.txt", ErrNotDirectory},
		{"missing", "missing-dir", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := l.Execute(context.Background(), argsFor(tc.path))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("%+v %v", result, err)
				}
				return
			}
			if err == nil || result.IsError {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
	for _, raw := range []string{`{}`, `{"path":null}`, `{"path":1}`, `{"path":".","extra":1}`, `null`, `{`, `{"path":"."} {}`, `{"path":".","path":"."}`} {
		if _, err := l.Execute(context.Background(), []byte(raw)); !errors.Is(err, ErrInvalidArguments) {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Execute(ctx, argsFor(".")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestListDirLimits(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	l := newLister(t, root, 2, 1024)
	if _, err := l.Execute(context.Background(), argsFor(".")); !errors.Is(err, ErrListTooLarge) {
		t.Fatal(err)
	}
	// Byte limit fails the whole listing instead of returning a silent cut.
	wide := newLister(t, root, 10, 8)
	if _, err := wide.Execute(context.Background(), argsFor(".")); !errors.Is(err, ErrListTooLarge) {
		t.Fatal(err)
	}
}

func TestListDirEntryBoundaryIsExact(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	exact := newLister(t, root, 2, 1024)
	if result, err := exact.Execute(context.Background(), argsFor(".")); err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	if err := os.WriteFile(filepath.Join(root, "c.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := exact.Execute(context.Background(), argsFor(".")); !errors.Is(err, ErrListTooLarge) {
		t.Fatalf("one extra entry must exceed the limit: %v", err)
	}
}

func TestListDirSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape-dir")); err != nil {
		t.Fatalf("symlink security test requires symlink support: %v", err)
	}
	// Listing the parent only labels the symlink; the external target must
	// stay unread through it.
	l := newLister(t, root, 10, 1024)
	result, err := l.Execute(context.Background(), argsFor("."))
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	if got := listLines(result.Content); len(got) != 1 || got[0] != "escape-dir\tsymlink" {
		t.Fatalf("listing=%q", result.Content)
	}
	if _, err := l.Execute(context.Background(), argsFor("escape-dir")); err == nil {
		t.Fatal("external symlink was followed")
	}
}

func TestListDirConstruction(t *testing.T) {
	for _, tc := range []struct {
		entries int
		bytes   int64
	}{
		{0, 10}, {-1, 10}, {10, 0}, {10, -1}, {10, 1<<63 - 1},
	} {
		if _, err := NewListDir(t.TempDir(), tc.entries, tc.bytes); err == nil {
			t.Fatal("accepted invalid limits")
		}
	}
	if _, err := NewListDir(filepath.Join(t.TempDir(), "missing"), 10, 10); err == nil {
		t.Fatal("accepted missing root")
	}
}

func TestListDirRegisters(t *testing.T) {
	l := newLister(t, t.TempDir(), 10, 1024)
	r := &Registry{}
	if err := r.Register(l); err != nil {
		t.Fatal(err)
	}
	tool, ok := r.Find("list_dir")
	if !ok {
		t.Fatal("missing list_dir")
	}
	if !json.Valid(tool.InputSchema()) {
		t.Fatal("invalid schema")
	}
}
