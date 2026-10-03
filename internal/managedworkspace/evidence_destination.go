package managedworkspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/workspaceplan"
)

type evidenceDestination struct {
	path, parent, name, source string
	root                       *os.Root
	parentID, sourceID         Identity
}

func strictEvidenceAbsolute(path string) bool {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\\\x00") {
		return false
	}
	for _, r := range path {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func overlaps(a, b string) bool {
	separator := string(os.PathSeparator)
	return a == b || strings.HasPrefix(a, strings.TrimSuffix(b, separator)+separator) || strings.HasPrefix(b, strings.TrimSuffix(a, separator)+separator)
}

func prepareEvidenceDestination(s *Store, r *Run, destination, source string) (*evidenceDestination, error) {
	if !strictEvidenceAbsolute(destination) || !strictEvidenceAbsolute(source) || workspaceplan.Hash([]byte(source)) != r.Manifest().SourceReference {
		return nil, ErrEvidence
	}
	parent, name := filepath.Dir(destination), filepath.Base(destination)
	// A private, existing parent is the managed export boundary, never an arbitrary tree.
	if !workspaceplan.ValidPath(name, workspaceplan.DefaultLimits()) || strings.Contains(name, "/") || strings.HasPrefix(name, ".") || name == evidenceAuditDirectory {
		return nil, ErrPrivate
	}
	if overlaps(parent, s.base) || overlaps(parent, source) {
		return nil, ErrPrivate
	}
	if err := checkLocalPath(source); err != nil {
		return nil, err
	}
	sourceID, err := identity(source)
	if err != nil {
		return nil, err
	}
	if err = checkPrivate(parent); err != nil {
		return nil, err
	}
	parentID, err := identity(parent)
	if err != nil {
		return nil, err
	}
	if parentID == sourceID || parentID == s.identity {
		return nil, ErrPrivate
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, ErrPrivate
	}
	d := &evidenceDestination{destination, parent, name, source, root, parentID, sourceID}
	if err = d.check(s); err != nil {
		root.Close()
		return nil, err
	}
	return d, nil
}
func (d *evidenceDestination) checkBoundary(s *Store) error {
	if err := s.check(); err != nil {
		return err
	}
	if err := checkPrivate(d.parent); err != nil {
		return err
	}
	parentID, err := identity(d.parent)
	if err != nil || parentID != d.parentID {
		return ErrPrivate
	}
	opened, err := d.root.Stat(".")
	if err != nil || identityInfo(opened) != d.parentID {
		return ErrPrivate
	}
	if err = checkLocalPath(d.source); err != nil {
		return err
	}
	sourceID, err := identity(d.source)
	if err != nil || sourceID != d.sourceID {
		return ErrPrivate
	}
	return nil
}
func (d *evidenceDestination) check(s *Store) error {
	if err := d.checkBoundary(s); err != nil {
		return err
	}
	if _, err := d.root.Lstat(d.name); !os.IsNotExist(err) {
		return ErrEvidence
	}
	return nil
}

// Open a completed package through the same private physical boundary.
func openEvidencePackage(ctx context.Context, destination string) (*os.Root, *os.Root, error) {
	if !Supported() {
		return nil, nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !strictEvidenceAbsolute(destination) || checkPrivate(destination) != nil || checkPrivate(filepath.Dir(destination)) != nil {
		return nil, nil, ErrPrivate
	}
	name := filepath.Base(destination)
	if !workspaceplan.ValidPath(name, workspaceplan.DefaultLimits()) || strings.HasPrefix(name, ".") {
		return nil, nil, ErrPrivate
	}
	parent, err := os.OpenRoot(filepath.Dir(destination))
	if err != nil {
		return nil, nil, ErrPrivate
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		parent.Close()
		return nil, nil, ErrPrivate
	}
	return root, parent, nil
}
