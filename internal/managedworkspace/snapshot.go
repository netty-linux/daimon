package managedworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/netty-linux/daimon/internal/workspaceplan"
)

type snapshotEntry struct {
	Path      string
	Directory bool
	Data      []byte
}

// A snapshot is bounded and versioned per entry, not an atomic snapshot of an
// externally mutable source. Source is opened read-only and never written.
func readSnapshot(ctx context.Context, directory string, private bool) ([]snapshotEntry, int, int, int, string, error) {
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, 0, 0, 0, "", ErrImport
	}
	defer r.Close()
	initial, err := r.Stat(".")
	if err != nil {
		return nil, 0, 0, 0, "", ErrImport
	}
	var entries []snapshotEntry
	files, dirs, total := 0, 0, 0
	var walk func(string, int) error
	walk = func(parent string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > MaxDepth {
			return ErrLimit
		}
		f, err := openRead(r, parent, true)
		if err != nil {
			return ErrImport
		}
		items, err := f.ReadDir(MaxFiles + MaxDirectories + 1)
		f.Close()
		if err != nil && err != io.EOF {
			return ErrImport
		}
		if len(items) > MaxFiles+MaxDirectories {
			return ErrLimit
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name() < items[j].Name() })
		for _, item := range items {
			p := item.Name()
			if parent != "." {
				p = parent + "/" + p
			}
			if !workspaceplan.ValidPath(p, workspaceplan.DefaultLimits()) || strings.Count(p, "/")+1 > MaxDepth {
				return ErrImport
			}
			i, err := r.Lstat(p)
			if err != nil {
				return ErrImport
			}
			if i.Mode()&os.ModeSymlink != 0 {
				return ErrImport
			}
			if i.IsDir() {
				dirs++
				if dirs > MaxDirectories {
					return ErrLimit
				}
				if private {
					if err := checkPrivate(directory + string(os.PathSeparator) + strings.ReplaceAll(p, "/", string(os.PathSeparator))); err != nil {
						return err
					}
				}
				entries = append(entries, snapshotEntry{Path: p, Directory: true})
				if err := walk(p, depth+1); err != nil {
					return err
				}
				continue
			}
			if !sourceRegular(i) || (private && !safeRegular(i)) {
				return ErrImport
			}
			files++
			if files > MaxFiles || i.Size() > MaxFileBytes || i.Size() < 0 {
				return ErrLimit
			}
			f, err := openRead(r, p, false)
			if err != nil {
				return ErrImport
			}
			opened, statErr := f.Stat()
			if statErr != nil || !os.SameFile(i, opened) || !sourceRegular(opened) {
				f.Close()
				return ErrImport
			}
			data, readErr := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
			after, statErr := f.Stat()
			f.Close()
			current, pathErr := r.Lstat(p)
			if readErr != nil || statErr != nil || pathErr != nil || !os.SameFile(i, after) || !os.SameFile(after, current) || !sourceRegular(current) || i.Size() != after.Size() || i.ModTime() != after.ModTime() {
				return ErrImport
			}
			if len(data) > MaxFileBytes || len(data) > MaxTotalBytes-total {
				return ErrLimit
			}
			total += len(data)
			entries = append(entries, snapshotEntry{Path: p, Data: data})
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return nil, 0, 0, 0, "", err
	}
	current, err := os.Lstat(directory)
	if err != nil || !os.SameFile(initial, current) {
		return nil, 0, 0, 0, "", ErrImport
	}
	h := sha256.New()
	// Paths use a portable ASCII subset; framed lengths avoid concatenation ambiguity.
	for _, e := range entries {
		kind := "file"
		if e.Directory {
			kind = "directory"
		}
		fmt.Fprintf(h, "v1:%d:%s:%s:%d:%s\n", len(e.Path), e.Path, kind, len(e.Data), workspaceplan.Hash(e.Data))
	}
	return entries, files, dirs, total, hex.EncodeToString(h.Sum(nil)), nil
}
