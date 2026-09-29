package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEchoValidation(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		invalid   bool
	}{
		{`{"text":"DAIMON"}`, "DAIMON", false}, {`{"text":""}`, "", false},
		{`{}`, "", true}, {`{"text":null}`, "", true}, {`{"text":1}`, "", true},
		{`{"text":"x","extra":true}`, "", true}, {`{`, "", true},
		{`null`, "", true}, {`[]`, "", true}, {`{"text":"x"} {}`, "", true},
		{`{"text":"a","text":"b"}`, "", true},
		{`{"Text":"x"}`, "", true},
		{`{"text":"a","Text":"b"}`, "", true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			r, err := (Echo{}).Execute(context.Background(), json.RawMessage(tc.raw))
			if tc.invalid {
				if !errors.Is(err, ErrInvalidArguments) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || r.Content != tc.want || r.IsError {
				t.Fatal(r, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Echo{}).Execute(ctx, []byte(`{"text":"x"}`)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRegistry(t *testing.T) {
	r := &Registry{}
	if err := r.Register(Echo{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Echo{}); !errors.Is(err, ErrDuplicateTool) {
		t.Fatal(err)
	}
	reader := newReader(t, t.TempDir(), 20)
	if err := r.Register(reader); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Find("echo"); !ok {
		t.Fatal("missing echo")
	}
	if _, ok := r.Find("unknown"); ok {
		t.Fatal("unexpected tool")
	}
	for range 10 {
		d := r.Descriptions()
		if len(d) != 2 || d[0].Name != "echo" || d[1].Name != "read_file" {
			t.Fatal(d)
		}
		if !reflect.DeepEqual(d, r.Descriptions()) {
			t.Fatal("unstable descriptions")
		}
	}
	d := r.Descriptions()
	d[0].InputSchema[0] = 'x'
	d[0].Name = "changed"
	if r.Descriptions()[0].Name != "echo" || !json.Valid(r.Descriptions()[0].InputSchema) {
		t.Fatal("aliased descriptions")
	}
	if err := r.Register(nil); !errors.Is(err, ErrInvalidTool) {
		t.Fatal(err)
	}
}

func newReader(t *testing.T, root string, limit int64) *ReadFile {
	t.Helper()
	r, err := NewReadFile(root, limit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}
func argsFor(path string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"path": path})
	return raw
}

func TestReadFileBoundaries(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "allowed.txt"), []byte("DAIMON"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte("1234567"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	r := newReader(t, root, 6)
	for _, tc := range []struct {
		name, path string
		wantErr    error
		fails      bool
	}{
		{"allowed", "allowed.txt", nil, false},
		{"absolute", filepath.Join(root, "allowed.txt"), ErrUnsafePath, true},
		{"parent", "../outside.txt", ErrUnsafePath, true},
		{"nested traversal", "folder/../allowed.txt", ErrUnsafePath, true},
		{"backslash traversal", `..\outside.txt`, ErrUnsafePath, true},
		{"drive", `C:\secret.txt`, ErrUnsafePath, true},
		{"UNC", `\\server\share\file`, ErrUnsafePath, true},
		{"stream", "allowed.txt:secret", ErrUnsafePath, true},
		{"empty", "", ErrUnsafePath, true},
		{"large", "large.txt", ErrFileTooLarge, true},
		{"directory", "folder", ErrNotRegular, true},
		{"missing", "missing.txt", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := r.Execute(context.Background(), argsFor(tc.path))
			if tc.fails {
				if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
					t.Fatalf("%+v %v", result, err)
				}
				return
			}
			if err != nil || result.Content != "DAIMON" {
				t.Fatal(result, err)
			}
		})
	}
	for _, raw := range []string{`{}`, `{"path":null}`, `{"path":1}`, `{"path":"allowed.txt","extra":1}`, `null`, `{`} {
		if _, err := r.Execute(context.Background(), []byte(raw)); !errors.Is(err, ErrInvalidArguments) {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Execute(ctx, argsFor("allowed.txt")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReadFileSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	r := newReader(t, root, 20)
	for _, tc := range []struct {
		name, target, path string
		allowed            bool
	}{
		{"file escape", filepath.Join(outside, "secret.txt"), "escape.txt", false},
		{"directory escape", outside, "escape-dir", false},
		{"internal", "ok.txt", "internal.txt", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Symlink(tc.target, filepath.Join(root, tc.path)); err != nil {
				t.Fatalf("symlink security test requires symlink support: %v", err)
			}
			path := tc.path
			if tc.name == "directory escape" {
				path = filepath.Join(path, "secret.txt")
			}
			result, err := r.Execute(context.Background(), argsFor(path))
			if tc.allowed {
				if err != nil || result.Content != "ok" {
					t.Fatal(result, err)
				}
			} else if err == nil || result.Content != "" {
				t.Fatalf("escaped workspace: %+v %v", result, err)
			}
		})
	}
}

func TestReadFileConstruction(t *testing.T) {
	for _, limit := range []int64{0, -1, 1<<63 - 1} {
		if _, err := NewReadFile(t.TempDir(), limit); err == nil {
			t.Fatal("accepted invalid limit")
		}
	}
	if _, err := NewReadFile(filepath.Join(t.TempDir(), "missing"), 10); err == nil {
		t.Fatal("accepted missing root")
	}
}
