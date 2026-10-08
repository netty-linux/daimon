//go:build linux

package environments

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	s, e := NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestWorkspaceValidation(t *testing.T) {
	cases := []Workspace{{{Path: "../escape"}}, {{Path: "/absolute"}}, {{Path: "A"}, {Path: "a"}}, {{Path: "a/b"}}, {{Path: "file", Data: make([]byte, MaxFileBytes+1)}}, {{Path: "dir", Directory: true, Data: []byte("x")}}}
	for _, w := range cases {
		if _, e := Describe(w); e == nil {
			t.Fatal(w)
		}
	}
	w := Workspace{{Path: ".git", Directory: true}, {Path: ".git/config", Data: []byte{0, 255, 1}}, {Path: "empty", Directory: true}}
	m, e := Describe(w)
	if e != nil || m.Files != 1 || m.Bytes != 3 {
		t.Fatal(m, e)
	}
	copy := Clone(w)
	copy[1].Data[0] = 9
	if w[1].Data[0] != 0 {
		t.Fatal("aliased")
	}
}
func TestRevisionsCopiesRestartAndConflicts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	created, e := s.Create(ctx, "thread")
	if e != nil || created.Revision != 0 {
		t.Fatal(created, e)
	}
	if _, e = s.Create(ctx, "thread"); !errors.Is(e, ErrExists) {
		t.Fatal(e)
	}
	r, e := s.Acquire(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Acquire(ctx, "thread"); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	if e = s.Delete(ctx, "thread"); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	// Mutating exposed metadata must not change private reservation authority.
	r.Metadata.ID = "env-000000000000000000000000"
	r.Metadata.Revision = 999
	w := Workspace{{Path: "A", Data: []byte("A")}}
	m, committed, e := r.Commit(ctx, w)
	if e != nil || !committed || m.Revision != 1 {
		t.Fatal(m, committed, e)
	}
	if _, _, e = r.Commit(ctx, w); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	r.Close()
	r, e = s.Acquire(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(r.Workspace(), w) {
		t.Fatal(r.Workspace())
	}
	copy := r.Workspace()
	copy[0].Data[0] = 'X'
	if r.Workspace()[0].Data[0] != 'A' {
		t.Fatal("copy")
	}
	w = append(w, Entry{Path: "B", Data: []byte("B")})
	if _, ok, e := r.Commit(ctx, w); e != nil || !ok {
		t.Fatal(ok, e)
	}
	r.Close()
	dir := s.directory
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	r, e = reopened.Acquire(ctx, "thread")
	if e != nil || !reflect.DeepEqual(r.Workspace(), w) {
		t.Fatal(e)
	}
	r.Close()
	if e = reopened.Delete(ctx, "thread"); e != nil {
		t.Fatal(e)
	}
	if _, e = reopened.Get(ctx, "thread"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestRejectedAndCanceledCommitKeepsPrevious(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.Create(ctx, "thread")
	r, e := s.Acquire(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	for _, w := range []Workspace{{{Path: "bad", Data: make([]byte, MaxFileBytes+1)}}, {{Path: "../bad"}}} {
		if _, ok, e := r.Commit(ctx, w); e == nil || ok {
			t.Fatal(ok, e)
		}
		m, e := s.Get(ctx, "thread")
		if e != nil || m.Revision != 0 {
			t.Fatal(m, e)
		}
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok, e := r.Commit(c, Workspace{}); !errors.Is(e, context.Canceled) || ok {
		t.Fatal(ok, e)
	}
}
func TestRecoveryDiscardsOnlyExactIntent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m, e := s.Create(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	if e = writeJSON(ctx, s.root, m.ID+"/pending.json", intent{1, m.ID, 1}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.materialize(ctx, m.ID+"/stage", Workspace{{Path: "partial", Data: []byte("new")}}); e != nil {
		t.Fatal(e)
	}
	if e = s.recoverOne(m.ID); e != nil {
		t.Fatal(e)
	}
	current, e := s.Get(ctx, "thread")
	if e != nil || current.Revision != 0 {
		t.Fatal(e)
	}
	if _, e = s.root.Lstat(m.ID + "/stage"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	if e = writeJSON(ctx, s.root, m.ID+"/pending.json", intent{1, "env-000000000000000000000000", 1}); e != nil {
		t.Fatal(e)
	}
	if e = s.recoverOne(m.ID); !errors.Is(e, ErrStorage) {
		t.Fatal(e)
	}
	if _, e = s.root.Lstat(m.ID + "/current.json"); e != nil {
		t.Fatal("deleted foreign intent", e)
	}
}
func TestWorkspaceLinksSpecialAndCorruption(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"symlink", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			outside := filepath.Join(t.TempDir(), "outside")
			if e := os.WriteFile(outside, []byte("private"), 0600); e != nil {
				t.Fatal(e)
			}
			target := filepath.Join(dir, "bad")
			var e error
			switch kind {
			case "symlink":
				e = os.Symlink(outside, target)
			case "hardlink":
				e = os.Link(outside, target)
			case "fifo":
				e = syscall.Mkfifo(target, 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = ReadWorkspace(ctx, dir); e == nil {
				t.Fatal("accepted", kind)
			}
		})
	}
	s := testStore(t)
	m, e := s.Create(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(s.directory, m.ID, "current.json")
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, []byte(strings.Replace(string(b), `"version":1`, `"version":2`, 1)), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, "thread"); !errors.Is(e, ErrVersion) {
		t.Fatal(e)
	}
}
func TestConcurrentReadersSeeCompleteRevision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.Create(ctx, "thread")
	r, e := s.Acquire(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				m, e := s.Get(ctx, "thread")
				if e != nil || m.Revision > 1 || m.Revision == 1 && m.Bytes != 1 {
					t.Errorf("metadata %v %v", m, e)
				}
			}
		}()
	}
	if _, ok, e := r.Commit(ctx, Workspace{{Path: "a", Data: []byte("A")}}); e != nil || !ok {
		t.Fatal(e)
	}
	wg.Wait()
}
func TestStrictMetadataAndCapacity(t *testing.T) {
	for _, raw := range []string{`{"version":1,"version":1}`, `{"version":null}`, `{"Version":1}`, `{} {}`} {
		var m Metadata
		if decode([]byte(raw), &m) == nil {
			t.Fatal(raw)
		}
	}
	s := testStore(t)
	for i := 0; i < MaxEnvironments; i++ {
		name := "thread-" + strings.Repeat("a", i+1)
		if _, e := s.Create(context.Background(), name); e != nil {
			t.Fatal(i, e)
		}
	}
	if _, e := s.Create(context.Background(), "overflow"); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
}

func TestPrivatePermissionsAndUnknownArtifacts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	old := syscall.Umask(0777)
	defer syscall.Umask(old)
	m, e := s.Create(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.Acquire(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	_, ok, e := r.Commit(ctx, Workspace{{Path: "dir", Directory: true}, {Path: "dir/file", Data: []byte("private")}})
	r.Close()
	if e != nil || !ok {
		t.Fatal(e)
	}
	current, e := s.Get(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(s.directory, m.ID, revisionName(current.Revision), "workspace", "dir", "file")
	if e = os.Chmod(file, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, "thread"); e == nil {
		t.Fatal("accepted uncontrolled mode")
	}
	if e = os.Chmod(file, 0600); e != nil {
		t.Fatal(e)
	}
	syscall.Umask(old)
	if e = write(ctx, s.root, m.ID+"/unknown", []byte("unknown")); e != nil {
		t.Fatal(e)
	}
	if e = s.recover(); e == nil {
		t.Fatal("unknown artifact accepted")
	}
	if _, e = s.root.Lstat(m.ID + "/unknown"); e != nil {
		t.Fatal("unknown artifact deleted", e)
	}
}

func TestCommitDoesNotAdoptOrDeleteUnregisteredStage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m, e := s.Create(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.Acquire(ctx, "thread")
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if e = s.makeDir(m.ID + "/stage"); e != nil {
		t.Fatal(e)
	}
	if e = write(ctx, s.root, m.ID+"/stage/unowned", []byte("keep")); e != nil {
		t.Fatal(e)
	}
	if _, ok, e := r.Commit(ctx, Workspace{}); e == nil || ok {
		t.Fatal(e, ok)
	}
	if _, e = s.root.Lstat(m.ID + "/stage/unowned"); e != nil {
		t.Fatal("unregistered stage deleted", e)
	}
	if _, e = s.root.Lstat(m.ID + "/pending.json"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("foreign stage adopted", e)
	}
}
