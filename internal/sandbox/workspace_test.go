package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/environments"
	"strings"
	"testing"
)

type fakeWorkspaceRPC struct {
	w                             environments.Workspace
	uploadID                      string
	uploadPath                    string
	upload                        []byte
	badLinks, badHash, failUpload bool
	helperCalls                   int
}

func (f *fakeWorkspaceRPC) WorkspaceRPC(ctx context.Context, service, method string, p []byte) ([][]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	fields, e := wire(p)
	if e != nil {
		return nil, e
	}
	if service == "ProcessService" {
		if method != "StartProcess" {
			return nil, errWorkspaceWire
		}
		cfg, e := wire(only(fields, 1).data)
		command, _ := textField(cfg, 1)
		if e != nil || command != "/usr/bin/python3" || len(cfg[2]) != 3 || string(cfg[2][0].data) != "-I" || string(cfg[2][1].data) != "-c" || string(cfg[2][2].data) != workspaceInspector {
			return nil, errWorkspaceWire
		}
		kill, _ := number(fields, 7)
		if kill != 1 {
			return nil, errWorkspaceWire
		}
		f.helperCalls++
		i := inspection{Version: 1, Entries: []inspectedFile{}}
		for n, entry := range f.w {
			links := uint64(1)
			if f.badLinks {
				links = 2
			}
			i.Entries = append(i.Entries, inspectedFile{Path: entry.Path, Directory: entry.Directory, Size: uint64(len(entry.Data)), Inode: uint64(n + 1), Device: 1, Links: links, Mtime: 1, Ctime: 1})
		}
		b, _ := json.Marshal(i)
		start := wb(1, wb(1, wi(1, 123)))
		data := wb(1, wb(2, wb(2, b)))
		end := wb(1, wb(3, wi(1, 0)))
		return [][]byte{start, data, end}, nil
	}
	if service != "FilesystemService" {
		return nil, errWorkspaceWire
	}
	switch method {
	case "MakeDir":
		path, _ := textField(fields, 1)
		if path != guestWorkspace {
			f.w = append(f.w, environments.Entry{Path: strings.TrimPrefix(path, guestWorkspace+"/"), Directory: true})
		}
		return [][]byte{{}}, nil
	case "BeginUpload":
		header, e := wire(only(fields, 1).data)
		if e != nil {
			return nil, e
		}
		f.uploadPath, _ = textField(header, 1)
		if !strings.HasPrefix(f.uploadPath, guestWorkspace+"/") {
			return nil, errWorkspaceWire
		}
		mode, _ := number(header, 2)
		if mode != 2 {
			return nil, errWorkspaceWire
		}
		f.uploadID = "upload-fixed"
		f.upload = nil
		p = ws(1, f.uploadID)
		p = append(p, wi(3, 8192)...)
		return [][]byte{p}, nil
	case "UploadChunk":
		if f.failUpload {
			return nil, errors.New("private transfer")
		}
		id, _ := textField(fields, 1)
		offset, _ := number(fields, 2)
		if id != f.uploadID || offset != uint64(len(f.upload)) {
			return nil, errWorkspaceWire
		}
		f.upload = append(f.upload, only(fields, 3).data...)
		return [][]byte{wi(1, uint64(len(f.upload)))}, nil
	case "CommitUpload":
		f.w = append(f.w, environments.Entry{Path: strings.TrimPrefix(f.uploadPath, guestWorkspace+"/"), Data: append([]byte{}, f.upload...)})
		return [][]byte{ws(2, environments.Hash(f.upload))}, nil
	case "AbortUpload":
		f.upload = nil
		return [][]byte{{}}, nil
	case "ReadFile":
		path, _ := textField(fields, 1)
		for _, entry := range f.w {
			if guestWorkspace+"/"+entry.Path == path {
				header := ws(2, path)
				header = append(header, wi(3, 1)...)
				header = append(header, wi(4, uint64(len(entry.Data)))...)
				chunk := wb(2, wb(2, entry.Data))
				end := wi(1, uint64(len(entry.Data)))
				hash := environments.Hash(entry.Data)
				if f.badHash {
					hash = "invalid"
				}
				end = append(end, ws(2, hash)...)
				return [][]byte{wb(1, header), chunk, wb(3, end)}, nil
			}
		}
		return nil, errWorkspaceWire
	}
	return nil, errWorkspaceWire
}
func TestWorkspaceOfficialFilesystemRoundTrip(t *testing.T) {
	fake := &fakeWorkspaceRPC{}
	transfer := &workspaceTransfer{rpc: fake}
	w := environments.Workspace{{Path: "dir", Directory: true}, {Path: "dir/a", Data: []byte{0, 255, 'A'}}, {Path: "empty", Data: []byte{}}}
	if e := transfer.Hydrate(context.Background(), w); e != nil {
		t.Fatal(e)
	}
	out, e := transfer.Export(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	a, _ := environments.Describe(w)
	b, _ := environments.Describe(out)
	if a.SHA256 != b.SHA256 || fake.helperCalls < 3 {
		t.Fatal(a, b, fake.helperCalls)
	}
}
func TestWorkspaceRejectsHardLinksHashTraversalAndCanceled(t *testing.T) {
	for _, kind := range []string{"links", "hash", "path", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			fake := &fakeWorkspaceRPC{w: environments.Workspace{{Path: "safe", Data: []byte("A")}}, badLinks: kind == "links", badHash: kind == "hash"}
			if kind == "path" {
				fake.w[0].Path = "../outside"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancel" {
				cancel()
			}
			if _, e := (&workspaceTransfer{rpc: fake}).Export(ctx); e == nil {
				t.Fatal("accepted", kind)
			} else if kind == "cancel" && !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
		})
	}
}
func TestWorkspaceFailedUploadAbortsPartial(t *testing.T) {
	fake := &fakeWorkspaceRPC{failUpload: true}
	e := (&workspaceTransfer{rpc: fake}).Hydrate(context.Background(), environments.Workspace{{Path: "a", Data: []byte("A")}})
	if e == nil || fake.upload != nil || len(fake.w) != 0 {
		t.Fatal(e, fake)
	}
}
