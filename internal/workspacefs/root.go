// Package workspacefs binds an opened root to its canonical pathname and inode.
// Revalidation detects namespace changes; it cannot exclude external mutations
// between checks and syscalls. It is not an isolation boundary against peers
// with filesystem mutation privileges.
package workspacefs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

var ErrRoot = errors.New("cannot bind canonical workspace root")
var ErrChanged = errors.New("canonical workspace root changed")

type Binding struct {
	canonical     string
	info          os.FileInfo
	root          *os.Root
	rootID, runID string
}

func (b *Binding) RootID() string {
	if b == nil {
		return ""
	}
	return b.rootID
}
func (b *Binding) RunID() string {
	if b == nil {
		return ""
	}
	return b.runID
}

func Open(directory string) (*os.Root, *Binding, error) {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, nil, ErrRoot
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, nil, ErrRoot
	}
	before, err := os.Lstat(canonical)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, ErrRoot
	}
	r, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, nil, ErrRoot
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		r.Close()
		return nil, nil, ErrRoot
	}
	h := sha256.Sum256([]byte(canonical))
	b := &Binding{canonical: canonical, info: before, root: r, rootID: hex.EncodeToString(h[:]), runID: hex.EncodeToString(nonce)}
	if err := b.Check(); err != nil {
		r.Close()
		return nil, nil, err
	}
	return r, b, nil
}

func (b *Binding) Check() error {
	if b == nil || b.root == nil {
		return ErrRoot
	}
	// All canonical ancestors must still be physical directories, never aliases.
	for name := b.canonical; ; name = filepath.Dir(name) {
		i, err := os.Lstat(name)
		if err != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
			return ErrChanged
		}
		parent := filepath.Dir(name)
		if parent == name {
			break
		}
	}
	current, err := os.Lstat(b.canonical)
	if err != nil || !os.SameFile(b.info, current) {
		return ErrChanged
	}
	opened, err := b.root.Stat(".")
	if errors.Is(err, os.ErrClosed) {
		return ErrRoot
	}
	if err != nil || !os.SameFile(b.info, opened) {
		return ErrChanged
	}
	return nil
}
