package editcontract

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"syscall"
)

var (
	ErrUnsupported = errors.New("atomic edit replacement is supported only on Linux")
	ErrStage       = errors.New("cannot stage edit replacement")
	ErrCommit      = errors.New("cannot commit edit replacement")
	ErrCleanup     = errors.New("cannot remove edit temporary file")
	ErrNoSpace     = errors.New("insufficient space for edit replacement")
)

func Supported() bool { return replacementSupported() }

// These private seams allow deterministic disk/permission faults without a
// global filesystem mock or a dependency. Production always uses os.Root.
type stageFile interface {
	io.WriteCloser
	Chmod(os.FileMode) error
	Sync() error
	Stat() (os.FileInfo, error)
}
type applyOps struct {
	create func(*os.Root, string) (stageFile, error)
	rename func(*os.Root, string, string) error
}

func realApplyOps() applyOps {
	return applyOps{
		create: func(r *os.Root, name string) (stageFile, error) {
			return r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		},
		rename: func(r *os.Root, old, new string) error { return r.Rename(old, new) },
	}
}
func safeFailure(kind, errorCause error) error {
	if errors.Is(errorCause, fs.ErrPermission) {
		return errors.Join(kind, fs.ErrPermission)
	}
	if errors.Is(errorCause, syscall.ENOSPC) {
		return errors.Join(kind, ErrNoSpace)
	}
	return kind
}

// Apply irreversibly consumes a permit, even on unsupported platform, invalid
// context, failure or cancellation. A deadline is mandatory (the loop supplies
// run/tool budget deadlines). Rename is the commit point: cancellation observed
// after a successful rename cannot undo it. No target is ever opened for write.
func (p *Permit) Apply(ctx context.Context) error { return p.apply(ctx, realApplyOps()) }

func (p *Permit) apply(ctx context.Context, ops applyOps) (err error) {
	if p == nil || p.state == nil {
		return ErrInvalid
	}
	if !p.state.used.CompareAndSwap(false, true) {
		return ErrUsed
	}
	s := p.state.proposal
	checkContext := func() error {
		if err := p.state.approvalCtx.Err(); err != nil {
			return err
		}
		return ctx.Err()
	}
	if err := checkContext(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrInvalid
	}
	if !Supported() {
		return ErrUnsupported
	}
	if !s.review.Limits.valid() || len(s.proposed) > s.review.Limits.FinalBytes || digest(s.proposed) != s.review.ProposedSHA256 {
		return ErrLimit
	}
	if s.info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return ErrFile
	}
	if err := s.revalidate(ctx); err != nil {
		return err
	}
	if err := checkContext(); err != nil {
		return err
	}
	parent, err := s.w.root.OpenRoot(path.Dir(s.review.Path))
	if err != nil {
		return ErrFile
	}
	defer parent.Close()
	base := path.Base(s.review.Path)
	// Pin the parent; revalidate both the original path and the pinned target.
	checkTarget := func() error {
		if err := checkContext(); err != nil {
			return err
		}
		if err := s.revalidate(ctx); err != nil {
			return err
		}
		data, info, err := (&Workspace{root: parent}).snapshot(ctx, base, s.review.Limits.InputBytes)
		if err != nil {
			return err
		}
		if !sameVersion(s.info, info) || version(info, data) != s.review.Original {
			return ErrChanged
		}
		return checkContext()
	}
	if err := checkTarget(); err != nil {
		return err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return ErrStage
	}
	tempName := ".daimon-edit-" + hex.EncodeToString(nonce) + ".tmp"
	if err := checkContext(); err != nil {
		return err
	}
	f, err := ops.create(parent, tempName)
	if err != nil {
		return safeFailure(ErrStage, err)
	}
	closed, committed := false, false
	defer func() {
		if !closed {
			if closeErr := f.Close(); closeErr != nil {
				err = errors.Join(err, ErrStage)
			}
		}
		if !committed {
			if removeErr := parent.Remove(tempName); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				err = errors.Join(err, ErrCleanup)
			}
		}
	}()
	// Chunk checks make cancellation cooperative during staging.
	for offset := 0; offset < len(s.proposed); {
		if err := checkContext(); err != nil {
			return err
		}
		end := offset + min(32*1024, len(s.proposed)-offset)
		n, writeErr := f.Write(s.proposed[offset:end])
		if err := checkContext(); err != nil {
			return err
		}
		if writeErr != nil {
			return safeFailure(ErrStage, writeErr)
		}
		if n != end-offset {
			return ErrStage
		}
		offset = end
	}
	if err := checkContext(); err != nil {
		return err
	}
	// Preserve ordinary rwx permissions; ACLs, ownership and special bits are
	// not copied. Reject privilege bits instead of silently changing them.
	if err := f.Chmod(s.info.Mode().Perm()); err != nil {
		return safeFailure(ErrStage, err)
	}
	if err := checkContext(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return safeFailure(ErrStage, err)
	}
	stagedInfo, err := f.Stat()
	if err != nil {
		return ErrStage
	}
	closed = true
	if err := f.Close(); err != nil {
		return safeFailure(ErrStage, err)
	}
	if err := checkContext(); err != nil {
		return err
	}
	// Verify the staging inode and exact bytes as well as the original target.
	stagedData, currentStage, err := (&Workspace{root: parent}).snapshot(ctx, tempName, s.review.Limits.FinalBytes)
	if err != nil {
		return err
	}
	if !sameVersion(stagedInfo, currentStage) || digest(stagedData) != s.review.ProposedSHA256 {
		return ErrChanged
	}
	// Last revalidation immediately precedes the atomic commit, after staging.
	if err := checkTarget(); err != nil {
		return err
	}
	if err := ops.rename(parent, tempName, base); err != nil {
		return safeFailure(ErrCommit, err)
	}
	committed = true
	return nil
}
