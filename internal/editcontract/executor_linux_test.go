package editcontract

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func applyContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}
func approved(t *testing.T, w *Workspace, content []byte, l Limits) *Permit {
	t.Helper()
	ctx := applyContext(t)
	p, err := w.Prepare(ctx, "file.txt", content, l)
	if err != nil {
		t.Fatal(err)
	}
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	return permit
}
func assertTarget(t *testing.T, dir, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	if err != nil || string(data) != want {
		t.Fatalf("target=%q err=%v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".daimon-edit-") {
			t.Fatal("temporary left behind", entry.Name())
		}
	}
}

func TestApplyApprovedExactContentAndMode(t *testing.T) {
	w, dir := fixture(t)
	if err := os.Chmod(filepath.Join(dir, "file.txt"), 0640); err != nil {
		t.Fatal(err)
	}
	content := []byte("exact\r\n\x1b\\r\n")
	permit := approved(t, w, content, testLimits())
	copyPermit := *permit
	content[0] = 'X'
	if err := permit.Apply(applyContext(t)); err != nil {
		t.Fatal(err)
	}
	assertTarget(t, dir, "exact\r\n\x1b\\r\n")
	info, err := os.Stat(filepath.Join(dir, "file.txt"))
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatal(info, err)
	}
	if err := copyPermit.Apply(applyContext(t)); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
	if _, err := copyPermit.Consume(context.Background()); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
}

func TestApplyChangedRemovedSymlinkAndHardLink(t *testing.T) {
	for _, change := range []string{"content", "removed", "symlink", "hardlink"} {
		t.Run(change, func(t *testing.T) {
			w, dir := fixture(t)
			permit := approved(t, w, []byte("replacement"), testLimits())
			target := filepath.Join(dir, "file.txt")
			outside := t.TempDir()
			other := filepath.Join(outside, "other")
			if err := os.WriteFile(other, []byte("external"), 0600); err != nil {
				t.Fatal(err)
			}
			want := ErrChanged
			switch change {
			case "content":
				if err := os.WriteFile(target, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "removed":
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				want = ErrFile
			case "symlink":
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, target); err != nil {
					t.Fatalf("symlink permission required: %v", err)
				}
				want = ErrSymlink
			case "hardlink":
				if err := os.Link(target, filepath.Join(outside, "alias")); err != nil {
					t.Fatal(err)
				}
				want = ErrHardLink
			}
			if err := permit.Apply(applyContext(t)); !errors.Is(err, want) {
				t.Fatal(err)
			}
			if err := permit.Apply(applyContext(t)); !errors.Is(err, ErrUsed) {
				t.Fatal(err)
			}
			if change == "content" {
				assertTarget(t, dir, "changed")
			}
			if change == "hardlink" {
				assertTarget(t, dir, "original\r\n")
			}
			if change == "removed" {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatal("missing target recreated")
				}
			}
			data, err := os.ReadFile(other)
			if err != nil || string(data) != "external" {
				t.Fatal("external file modified", err)
			}
		})
	}
}

func TestPrepareRejectsExistingHardLink(t *testing.T) {
	w, dir := fixture(t)
	if err := os.Link(filepath.Join(dir, "file.txt"), filepath.Join(t.TempDir(), "alias")); err != nil {
		t.Fatal(err)
	}
	if p, err := w.Prepare(applyContext(t), "file.txt", []byte("new"), testLimits()); p != nil || !errors.Is(err, ErrHardLink) {
		t.Fatal(p, err)
	}
}

type faultyStage struct {
	*os.File
	write func([]byte) (int, error)
	chmod func(os.FileMode) error
	sync  func() error
	close func() error
}

func (f *faultyStage) Write(b []byte) (int, error) {
	if f.write != nil {
		return f.write(b)
	}
	return f.File.Write(b)
}
func (f *faultyStage) Chmod(mode os.FileMode) error {
	if f.chmod != nil {
		return f.chmod(mode)
	}
	return f.File.Chmod(mode)
}
func (f *faultyStage) Sync() error {
	if f.sync != nil {
		return f.sync()
	}
	return f.File.Sync()
}
func (f *faultyStage) Close() error {
	if f.close != nil {
		return f.close()
	}
	return f.File.Close()
}

func TestApplyStageFailuresCleanUp(t *testing.T) {
	for _, failure := range []string{"create", "partial-enospc", "chmod", "sync", "close", "rename", "short-write"} {
		t.Run(failure, func(t *testing.T) {
			w, dir := fixture(t)
			permit := approved(t, w, []byte("replacement"), testLimits())
			ops := realApplyOps()
			if failure == "create" {
				ops.create = func(*os.Root, string) (stageFile, error) {
					return nil, &os.PathError{Op: "create", Path: "sensitive", Err: syscall.EACCES}
				}
			} else {
				ops.create = func(r *os.Root, name string) (stageFile, error) {
					f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
					if err != nil {
						return nil, err
					}
					fault := &faultyStage{File: f}
					switch failure {
					case "partial-enospc":
						fault.write = func(b []byte) (int, error) {
							n, err := f.Write(b[:3])
							if err != nil {
								return n, err
							}
							return n, syscall.ENOSPC
						}
					case "chmod":
						fault.chmod = func(os.FileMode) error { return syscall.EACCES }
					case "sync":
						fault.sync = func() error { return syscall.ENOSPC }
					case "close":
						fault.close = func() error { f.Close(); return errors.New("sensitive close error") }
					case "short-write":
						fault.write = func(b []byte) (int, error) { return f.Write(b[:3]) }
					}
					return fault, nil
				}
			}
			if failure == "rename" {
				ops.rename = func(*os.Root, string, string) error { return syscall.EACCES }
			}
			err := permit.apply(applyContext(t), ops)
			if err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatal(err)
			}
			if failure == "partial-enospc" || failure == "sync" {
				if !errors.Is(err, ErrNoSpace) {
					t.Fatal(err)
				}
			}
			if failure == "create" || failure == "chmod" || failure == "rename" {
				if !errors.Is(err, fs.ErrPermission) {
					t.Fatal(err)
				}
			}
			assertTarget(t, dir, "original\r\n")
			if err := permit.Apply(applyContext(t)); !errors.Is(err, ErrUsed) {
				t.Fatal(err)
			}
		})
	}
}

func TestCancellationDuringStaging(t *testing.T) {
	w, dir := fixture(t)
	content := []byte(strings.Repeat("x", 65536))
	l := testLimits()
	l.FinalBytes = len(content)
	l.PreviewBytes = 200000
	permit := approved(t, w, content, l)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ops := realApplyOps()
	ops.create = func(r *os.Root, name string) (stageFile, error) {
		f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		return &faultyStage{File: f, write: func(b []byte) (int, error) { n, err := f.Write(b); cancel(); return n, err }}, nil
	}
	if err := permit.apply(ctx, ops); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertTarget(t, dir, "original\r\n")
	if err := permit.Apply(applyContext(t)); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
}

func TestChangeDuringStagingPreventsCommit(t *testing.T) {
	for _, change := range []string{"content", "removed", "symlink", "hardlink"} {
		t.Run(change, func(t *testing.T) {
			w, dir := fixture(t)
			permit := approved(t, w, []byte("replacement"), testLimits())
			ops := realApplyOps()
			target := filepath.Join(dir, "file.txt")
			ops.create = func(r *os.Root, name string) (stageFile, error) {
				f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return nil, err
				}
				return &faultyStage{File: f, sync: func() error {
					if err := f.Sync(); err != nil {
						return err
					}
					switch change {
					case "content":
						return os.WriteFile(target, []byte("changed late"), 0600)
					case "removed":
						return os.Remove(target)
					case "symlink":
						if err := os.Remove(target); err != nil {
							return err
						}
						return os.Symlink("other", target)
					case "hardlink":
						return os.Link(target, filepath.Join(dir, "alias"))
					}
					return nil
				}}, nil
			}
			if err := permit.apply(applyContext(t), ops); err == nil {
				t.Fatal("late change committed")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".daimon-edit-") {
					t.Fatal("temporary left behind")
				}
			}
			if change == "content" {
				assertTarget(t, dir, "changed late")
			}
			if change == "hardlink" {
				assertTarget(t, dir, "original\r\n")
			}
		})
	}
}

func TestApplyExactIndependentSizeLimitsAndDeadlines(t *testing.T) {
	for _, size := range []int{0, 1, 10, 4096} {
		t.Run(string(rune('a'+size%26)), func(t *testing.T) {
			w, dir := fixture(t)
			l := testLimits()
			l.InputBytes = len("original\r\n")
			l.FinalBytes = max(1, size)
			l.PreviewBytes = 20000
			content := []byte(strings.Repeat("x", size))
			permit := approved(t, w, content, l)
			if err := permit.Apply(applyContext(t)); err != nil {
				t.Fatal(err)
			}
			assertTarget(t, dir, string(content))
		})
	}
	w, dir := fixture(t)
	l := testLimits()
	l.FinalBytes = 1
	if p, err := w.Prepare(applyContext(t), "file.txt", []byte("xx"), l); p != nil || !errors.Is(err, ErrLimit) {
		t.Fatal(p, err)
	}
	permit := approved(t, w, []byte("new"), testLimits())
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	if err := permit.Apply(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	assertTarget(t, dir, "original\r\n")
	permit = approved(t, w, []byte("new"), testLimits())
	if err := permit.Apply(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := permit.Apply(applyContext(t)); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
}

func TestTamperedTemporaryCannotBeCommitted(t *testing.T) {
	w, dir := fixture(t)
	permit := approved(t, w, []byte("replacement"), testLimits())
	ops := realApplyOps()
	ops.create = func(r *os.Root, name string) (stageFile, error) {
		f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		return &faultyStage{File: f, sync: func() error {
			if err := f.Sync(); err != nil {
				return err
			}
			return r.WriteFile(name, []byte("tampered"), 0600)
		}}, nil
	}
	if err := permit.apply(applyContext(t), ops); !errors.Is(err, ErrChanged) {
		t.Fatal(err)
	}
	assertTarget(t, dir, "original\r\n")
}
