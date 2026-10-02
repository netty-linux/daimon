package createcontract

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"syscall"
)

var (
	ErrUnsupported = errors.New("file creation is supported only on Linux")
	ErrWrite       = errors.New("cannot complete approved file creation")
	ErrCleanup     = errors.New("cannot clean incomplete created file")
	ErrNoSpace     = errors.New("insufficient space for file creation")
)

type createFile interface {
	io.ReadWriteCloser
	io.Seeker
	Chmod(os.FileMode) error
	Sync() error
	Stat() (os.FileInfo, error)
}
type applyOps struct {
	create func(*os.Root, string) (createFile, error)
	remove func(*os.Root, string) error
}

func realOps() applyOps {
	return applyOps{
		create: func(r *os.Root, p string) (createFile, error) {
			return r.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		},
		remove: func(r *os.Root, p string) error { return r.Remove(p) },
	}
}
func safeFailure(kind, cause error) error {
	if errors.Is(cause, fs.ErrExist) {
		return ErrConflict
	}
	if errors.Is(cause, fs.ErrPermission) {
		return errors.Join(kind, fs.ErrPermission)
	}
	if errors.Is(cause, syscall.ENOSPC) {
		return errors.Join(kind, ErrNoSpace)
	}
	return kind
}
func (p *Permit) Apply(ctx context.Context) error { return p.apply(ctx, realOps()) }
func (p *Permit) apply(ctx context.Context, ops applyOps) (err error) {
	if p == nil || p.state == nil {
		return ErrInvalid
	}
	if !p.state.used.CompareAndSwap(false, true) {
		return ErrUsed
	}
	s := p.state.proposal
	check := func() error {
		if err := p.state.approvalCtx.Err(); err != nil {
			return err
		}
		return ctx.Err()
	}
	if err := check(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrInvalid
	}
	if !Supported() {
		return ErrUnsupported
	}
	if s.review.Limits.Validate() != nil || len(s.content) > s.review.Limits.FinalBytes || digest(s.content) != s.review.ProposedSHA256 {
		return ErrLimit
	}
	if err := s.revalidate(ctx, true); err != nil {
		return err
	}
	parent, err := s.w.root.OpenRoot(path.Dir(s.review.Path))
	if err != nil {
		return ErrFile
	}
	defer parent.Close()
	parentInfo, err := parent.Stat(".")
	if err != nil || !os.SameFile(parentInfo, s.parents[len(s.parents)-1]) {
		return ErrChanged
	}
	if err := s.revalidate(ctx, true); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	base := path.Base(s.review.Path)
	f, err := ops.create(parent, base)
	if err != nil {
		return safeFailure(ErrWrite, err)
	}
	identity, statErr := f.Stat()
	closed, committed := false, false
	defer func() {
		if !closed {
			if closeErr := f.Close(); closeErr != nil {
				err = errors.Join(err, ErrWrite)
			}
		}
		if !committed {
			current, inspectErr := parent.Lstat(base)
			if errors.Is(inspectErr, fs.ErrNotExist) {
				return
			}
			// Do not delete a different inode, symlink or externally linked file.
			if statErr != nil || inspectErr != nil || !current.Mode().IsRegular() || !os.SameFile(current, identity) || !singleLink(current) {
				err = errors.Join(err, ErrCleanup)
				return
			}
			if removeErr := ops.remove(parent, base); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				err = errors.Join(err, ErrCleanup)
			}
		}
	}()
	if statErr != nil || !identity.Mode().IsRegular() || !singleLink(identity) {
		return ErrWrite
	}
	// Force the documented owner-only permissions regardless of umask.
	if err := f.Chmod(0600); err != nil {
		return safeFailure(ErrWrite, err)
	}
	for offset := 0; offset < len(s.content); {
		if err := check(); err != nil {
			return err
		}
		end := offset + min(32*1024, len(s.content)-offset)
		n, writeErr := f.Write(s.content[offset:end])
		if err := check(); err != nil {
			return err
		}
		if writeErr != nil {
			return safeFailure(ErrWrite, writeErr)
		}
		if n != end-offset {
			return ErrWrite
		}
		offset = end
	}
	if err := check(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return safeFailure(ErrWrite, err)
	}
	if err := check(); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ErrWrite
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(s.review.Limits.FinalBytes)+1))
	if err != nil {
		return ErrWrite
	}
	finalInfo, err := f.Stat()
	if err != nil {
		return ErrWrite
	}
	if !finalInfo.Mode().IsRegular() || !singleLink(finalInfo) || finalInfo.Mode().Perm() != 0600 || digest(data) != s.review.ProposedSHA256 || int64(len(data)) != finalInfo.Size() {
		return ErrChanged
	}
	closed = true
	if err := f.Close(); err != nil {
		return safeFailure(ErrWrite, err)
	}
	if err := s.revalidate(ctx, false); err != nil {
		return err
	}
	current, err := parent.Lstat(base)
	if err != nil || !current.Mode().IsRegular() || !sameCreatedFile(current, finalInfo) {
		return ErrChanged
	}
	if err := check(); err != nil {
		return err
	}
	// Success confirms the validated, synced and closed file; no rename/overwrite.
	committed = true
	return nil
}
