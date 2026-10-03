//go:build linux

package createcontract

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type reviewFunc func(context.Context, Review) (Decision, error)

func (f reviewFunc) Review(ctx context.Context, r Review) (Decision, error) { return f(ctx, r) }
func setup(t *testing.T) (*Workspace, string, context.Context) {
	t.Helper()
	dir := t.TempDir()
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return w, dir, ctx
}
func limits() Limits {
	return Limits{FinalBytes: 65536, Lines: 1000, PathBytes: 4096, PreviewBytes: 1024 * 1024}
}
func approved(t *testing.T, w *Workspace, ctx context.Context, p string, data []byte) *Permit {
	t.Helper()
	proposal, err := w.Prepare(ctx, p, data, limits())
	if err != nil {
		t.Fatal(err)
	}
	permit, err := proposal.Approve(ctx, reviewFunc(func(context.Context, Review) (Decision, error) { return Allow, nil }))
	if err != nil {
		t.Fatal(err)
	}
	return permit
}
func noFile(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("unexpected residual file", err)
	}
}

func TestExactCreationAndOneUse(t *testing.T) {
	w, dir, ctx := setup(t)
	if err := os.Mkdir(filepath.Join(dir, "parent"), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("héllo\r\n\\n\x1b")
	original := append([]byte(nil), data...)
	p, err := w.Prepare(ctx, "parent/new.txt", data, limits())
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	view := p.View()
	if !view.TargetAbsent || view.RootID == "" || !strings.Contains(view.Display, strconv.QuoteToASCII(string(original))) || !strings.Contains(view.Display, "AUSENTE") {
		t.Fatal("incomplete binding")
	}
	for _, r := range view.Display {
		if (r < 32 && r != '\n') || r == '\x1b' {
			t.Fatal("unsafe preview")
		}
	}
	permit, err := p.Approve(ctx, NewTerminal(strings.NewReader("y\n"), io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	copyPermit := *permit
	if err := permit.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(dir, "parent/new.txt"))
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatal("inexact bytes", err)
	}
	info, err := os.Stat(filepath.Join(dir, "parent/new.txt"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe permissions", err)
	}
	if err := copyPermit.Apply(ctx); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
	if _, err := p.Approve(ctx, reviewFunc(func(context.Context, Review) (Decision, error) { t.Fatal("approval reused"); return Allow, nil })); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
}

func TestUnsafeAndExistingTargets(t *testing.T) {
	w, dir, ctx := setup(t)
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(dir, "symlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("directory", filepath.Join(dir, "linked-parent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "file"), filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"file", "directory", "symlink", "hardlink", "linked-parent/new", "missing/new", "../escape", "/absolute", "x\x00y", "x:stream", "\\server\\x", "a/../b", "a//b", "./file", "CON", "NUL.txt", "x.", "x "} {
		if proposal, err := w.Prepare(ctx, p, []byte("new"), limits()); proposal != nil || err == nil {
			t.Fatal("unsafe proposal accepted", p)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil || string(data) != "existing" {
		t.Fatal("existing file changed", err)
	}
}

func TestConflictsBeforeApprovalAndApply(t *testing.T) {
	for _, when := range []string{"before-approval", "during-approval", "before-apply", "exclusive-race", "parent-changed"} {
		t.Run(when, func(t *testing.T) {
			w, dir, ctx := setup(t)
			if err := os.Mkdir(filepath.Join(dir, "parent"), 0700); err != nil {
				t.Fatal(err)
			}
			p, err := w.Prepare(ctx, "parent/new", []byte("approved"), limits())
			if err != nil {
				t.Fatal(err)
			}
			compete := func() {
				if err := os.WriteFile(filepath.Join(dir, "parent/new"), []byte("competitor"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if when == "before-approval" {
				compete()
			}
			permit, err := p.Approve(ctx, reviewFunc(func(context.Context, Review) (Decision, error) {
				if when == "during-approval" {
					compete()
				}
				return Allow, nil
			}))
			if when == "before-approval" || when == "during-approval" {
				if !errors.Is(err, ErrConflict) || permit != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ops := realOps()
			if when == "before-apply" {
				compete()
			}
			if when == "exclusive-race" {
				real := ops.create
				ops.create = func(r *os.Root, p string) (createFile, error) { compete(); return real(r, p) }
			}
			if when == "parent-changed" {
				if err := os.Rename(filepath.Join(dir, "parent"), filepath.Join(dir, "old-parent")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(dir, "parent"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			err = permit.apply(ctx, ops)
			if err == nil {
				t.Fatal("conflict accepted")
			}
			if when == "parent-changed" {
				noFile(t, filepath.Join(dir, "parent/new"))
				noFile(t, filepath.Join(dir, "old-parent/new"))
			} else {
				data, err := os.ReadFile(filepath.Join(dir, "parent/new"))
				if err != nil || string(data) != "competitor" {
					t.Fatal("competitor changed", err)
				}
			}
			if err := permit.Apply(ctx); !errors.Is(err, ErrUsed) {
				t.Fatal(err)
			}
		})
	}
}

type faultyFile struct {
	*os.File
	write func([]byte) (int, error)
	sync  func() error
	close func() error
	chmod func(os.FileMode) error
}

func (f *faultyFile) Write(b []byte) (int, error) {
	if f.write != nil {
		return f.write(b)
	}
	return f.File.Write(b)
}
func (f *faultyFile) Sync() error {
	if f.sync != nil {
		return f.sync()
	}
	return f.File.Sync()
}
func (f *faultyFile) Close() error {
	if f.close != nil {
		return f.close()
	}
	return f.File.Close()
}
func (f *faultyFile) Chmod(m os.FileMode) error {
	if f.chmod != nil {
		return f.chmod(m)
	}
	return f.File.Chmod(m)
}

func TestInjectedFailuresAndCancellation(t *testing.T) {
	for _, scenario := range []string{"permission", "exclusive", "disk", "short", "chmod", "sync", "close", "cleanup", "cancel", "tamper", "other-inode"} {
		t.Run(scenario, func(t *testing.T) {
			w, dir, ctx := setup(t)
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			permit := approved(t, w, ctx, "new", bytes.Repeat([]byte("x"), 65536))
			ops := realOps()
			real := ops.create
			ops.create = func(r *os.Root, p string) (createFile, error) {
				if scenario == "permission" {
					return nil, &os.PathError{Op: "secret", Path: "SECRET", Err: fs.ErrPermission}
				}
				if scenario == "exclusive" {
					return nil, fs.ErrExist
				}
				f, err := real(r, p)
				if err != nil {
					return nil, err
				}
				file := f.(*os.File)
				fault := &faultyFile{File: file}
				switch scenario {
				case "disk", "cleanup":
					fault.write = func(b []byte) (int, error) { n, _ := file.Write(b[:3]); return n, syscall.ENOSPC }
				case "short":
					fault.write = func(b []byte) (int, error) { return file.Write(b[:3]) }
				case "chmod":
					fault.chmod = func(os.FileMode) error { return fs.ErrPermission }
				case "sync":
					fault.sync = func() error { return syscall.ENOSPC }
				case "close":
					fault.close = func() error { file.Close(); return errors.New("SECRET") }
				case "cancel":
					fault.write = func(b []byte) (int, error) { n, err := file.Write(b); cancel(); return n, err }
				case "tamper":
					fault.sync = func() error { _, err := file.WriteAt([]byte("changed"), 0); return err }
				case "other-inode":
					fault.sync = func() error {
						if err := r.Remove(p); err != nil {
							return err
						}
						return r.WriteFile(p, []byte("competitor"), 0600)
					}
				}
				return fault, nil
			}
			if scenario == "cleanup" {
				ops.remove = func(*os.Root, string) error { return errors.New("SECRET") }
			}
			err := permit.apply(runCtx, ops)
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatal("false success or unsafe error", err)
			}
			if scenario == "disk" || scenario == "cleanup" || scenario == "sync" {
				if !errors.Is(err, ErrNoSpace) {
					t.Fatal(err)
				}
			}
			if scenario == "permission" || scenario == "chmod" {
				if !errors.Is(err, fs.ErrPermission) {
					t.Fatal(err)
				}
			}
			if scenario == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if scenario == "cleanup" || scenario == "other-inode" {
				if !errors.Is(err, ErrCleanup) {
					t.Fatal(err)
				}
				if scenario == "other-inode" {
					data, e := os.ReadFile(filepath.Join(dir, "new"))
					if e != nil || string(data) != "competitor" {
						t.Fatal("competitor removed", e)
					}
				}
			} else {
				noFile(t, filepath.Join(dir, "new"))
			}
			if err := permit.Apply(ctx); !errors.Is(err, ErrUsed) {
				t.Fatal(err)
			}
		})
	}
}

func TestExactLimitsAndExplicitPermissions(t *testing.T) {
	w, dir, ctx := setup(t)
	maximum := bytes.Repeat([]byte("x"), limits().FinalBytes)
	fullPermit := approved(t, w, ctx, "maximum", maximum)
	if err := fullPermit.Apply(ctx); err != nil {
		t.Fatal("exact final-byte limit", err)
	}
	full, readErr := os.ReadFile(filepath.Join(dir, "maximum"))
	if readErr != nil || !bytes.Equal(full, maximum) {
		t.Fatal("maximum content changed", readErr)
	}
	for i, mask := range []int{0, 0777} {
		old := syscall.Umask(mask)
		p := []string{"empty", "byte"}[i]
		data := []byte{}
		if i == 1 {
			data = []byte("x")
		}
		permit := approved(t, w, ctx, p, data)
		err := permit.Apply(ctx)
		syscall.Umask(old)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(dir, p))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("umask changed final mode", err)
		}
	}
	l := limits()
	l.FinalBytes = 10
	l.PathBytes = 3
	l.Lines = 2
	p, err := w.Prepare(ctx, "new", []byte("1234567890"), l)
	if err != nil {
		t.Fatal(err)
	}
	l.PreviewBytes = len(p.View().Display)
	for i := 0; i < 3; i++ {
		p, err = w.Prepare(ctx, "new", []byte("1234567890"), l)
		if err != nil {
			t.Fatal(err)
		}
		l.PreviewBytes = len(p.View().Display)
	}
	if _, err := w.Prepare(ctx, "new", []byte("1234567890"), l); err != nil {
		t.Fatal("exact preview limit", err)
	}
	l.PreviewBytes--
	if _, err := w.Prepare(ctx, "new", []byte("1234567890"), l); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	l = limits()
	l.FinalBytes = 10
	l.PathBytes = 3
	l.Lines = 2
	for _, sample := range []struct{ p, c string }{{"long", "x"}, {"new", "12345678901"}, {"new", "a\nb\nc"}} {
		if _, err := w.Prepare(ctx, sample.p, []byte(sample.c), l); !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
	}
	permit := approved(t, w, ctx, "no-deadline", []byte("x"))
	if err := permit.Apply(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	noFile(t, filepath.Join(dir, "no-deadline"))
	expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	permit = approved(t, w, ctx, "expired", []byte("x"))
	if err := permit.Apply(expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	noFile(t, filepath.Join(dir, "expired"))
}

type displayFunc func([]byte) (int, error)

func (f displayFunc) Write(b []byte) (int, error) { return f(b) }
func TestApprovalFailuresConsumeProposal(t *testing.T) {
	for _, scenario := range []string{"deny", "eof", "invalid", "display", "partial", "cancel", "bad-decision"} {
		t.Run(scenario, func(t *testing.T) {
			w, dir, ctx := setup(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			p, err := w.Prepare(ctx, "new", []byte("secret"), limits())
			if err != nil {
				t.Fatal(err)
			}
			answer := "y\n"
			if scenario == "deny" {
				answer = "n\n"
			}
			if scenario == "eof" {
				answer = ""
			}
			if scenario == "invalid" {
				answer = "maybe\n"
			}
			writer := displayFunc(func(b []byte) (int, error) {
				if scenario == "display" {
					return 0, errors.New("secret")
				}
				if scenario == "partial" {
					return len(b) - 1, nil
				}
				if scenario == "cancel" {
					cancel()
				}
				return len(b), nil
			})
			var reviewer Reviewer = NewTerminal(strings.NewReader(answer), writer)
			if scenario == "bad-decision" {
				reviewer = reviewFunc(func(context.Context, Review) (Decision, error) { return Decision(99), nil })
			}
			if permit, err := p.Approve(ctx, reviewer); permit != nil || err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal(permit, err)
			}
			if _, err := p.Approve(context.Background(), reviewer); !errors.Is(err, ErrUsed) {
				t.Fatal(err)
			}
			noFile(t, filepath.Join(dir, "new"))
		})
	}
}
