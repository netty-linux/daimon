package managedworkspace

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/workspaceplan"
)

const MaxRunEntries = 128
const MaxRunBytes = 4 * 1024 * 1024

type inventoryEntry struct {
	Path      string
	Directory bool
	Identity  Identity
	Size      int64
	Modified  int64
	Mode      uint32
	Changed   string
	Hash      string
}
type inventoryVersion struct {
	Identity       Identity
	Size, Modified int64
	Mode           uint32
	Changed        string
}

func entryVersion(info os.FileInfo) inventoryVersion {
	return inventoryVersion{identityInfo(info), info.Size(), info.ModTime().UnixNano(), uint32(info.Mode()), changeInfo(info)}
}

type inventory struct {
	Root               inventoryVersion
	Entries            []inventoryEntry
	Files, Directories int
	Bytes              int64
	Hash               string
}

// Each component is opened beneath a pinned root. Private topology is also
// checked with the existing openat2 no-symlink/no-mount policy. No RemoveAll.
func inventoryRun(ctx context.Context, base, id string, root *os.Root) (inventory, error) {
	var result inventory
	rootInfo, err := root.Stat(".")
	if err != nil {
		return inventory{}, ErrPrivate
	}
	result.Root = entryVersion(rootInfo)
	var visit func(string, int) error
	visit = func(parent string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > MaxDepth+2 {
			return ErrLimit
		}
		if err := checkPrivate(filepath.Join(base, id, filepath.FromSlash(parent))); err != nil {
			return err
		}
		dir, err := openRead(root, parent, true)
		if err != nil {
			return ErrPrivate
		}
		entries, err := dir.ReadDir(MaxRunEntries + 1)
		dir.Close()
		if err != nil && err != io.EOF {
			return ErrArtifact
		}
		if len(entries) > MaxRunEntries {
			return ErrLimit
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := entry.Name()
			if !utf8.ValidString(name) || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
				return ErrPrivate
			}
			path := name
			if parent != "." {
				path = parent + "/" + name
			}
			if len(path) > 4096 || len(result.Entries) >= MaxRunEntries {
				return ErrLimit
			}
			info, err := root.Lstat(path)
			if err != nil {
				return ErrArtifact
			}
			item := inventoryEntry{Path: path, Directory: info.IsDir(), Identity: identityInfo(info), Size: info.Size(), Modified: info.ModTime().UnixNano()}
			item.Mode = uint32(info.Mode())
			item.Changed = changeInfo(info)
			if info.IsDir() {
				if err := checkPrivate(filepath.Join(base, id, filepath.FromSlash(path))); err != nil {
					return err
				}
				result.Directories++
			} else {
				if !safeRegular(info) || info.Size() < 0 || info.Size() > MaxPreviewBytes {
					return ErrPrivate
				}
				f, err := openRead(root, path, false)
				if err != nil {
					return ErrArtifact
				}
				opened, e := f.Stat()
				if e != nil || !os.SameFile(info, opened) {
					f.Close()
					return ErrPrivate
				}
				data, e := io.ReadAll(io.LimitReader(f, MaxPreviewBytes+1))
				after, e2 := f.Stat()
				f.Close()
				if e != nil || e2 != nil || int64(len(data)) != info.Size() || entryVersion(after) != entryVersion(info) {
					return ErrArtifact
				}
				item.Hash = workspaceplan.Hash(data)
				result.Bytes += int64(len(data))
				result.Files++
				if result.Bytes > MaxRunBytes {
					return ErrLimit
				}
			}
			result.Entries = append(result.Entries, item)
			if item.Directory {
				if err := visit(path, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(".", 0); err != nil {
		return inventory{}, err
	}
	after, err := root.Stat(".")
	if err != nil || entryVersion(after) != result.Root {
		return inventory{}, ErrPrivate
	}
	data, _ := json.Marshal(struct {
		Root    inventoryVersion
		Entries []inventoryEntry
	}{result.Root, result.Entries})
	result.Hash = workspaceplan.Hash(data)
	return result, nil
}
