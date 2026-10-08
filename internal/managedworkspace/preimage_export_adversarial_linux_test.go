//go:build linux && amd64

package managedworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspaceplan"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPreimageExportCaptureAdversaries(t *testing.T) {
	for _, kind := range []string{"missing", "truncated", "metadata", "null", "duplicate", "hash", "size", "path", "operation", "cross-run", "cross-operation", "symlink", "hardlink", "fifo", "journal", "approval"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, base, s, id, _ := preimageExportFixture(t)
			r, e := Open(base, id)
			if e != nil {
				t.Fatal(e)
			}
			op := r.Manifest().Retention.OperationID
			r.Close()
			dir := filepath.Join(base, id)
			bin := filepath.Join(dir, "preimages", op+".bin")
			meta := filepath.Join(dir, "preimages", op+".json")
			switch kind {
			case "missing":
				e = os.Remove(bin)
			case "truncated":
				e = os.WriteFile(bin, []byte("x"), 0600)
			case "metadata":
				e = os.WriteFile(meta, []byte("{"), 0600)
			case "null":
				e = os.WriteFile(meta, []byte("null"), 0600)
			case "duplicate":
				b := read(t, meta)
				b = append([]byte(`{"run_id":"fake",`), b[1:]...)
				e = os.WriteFile(meta, b, 0600)
			case "symlink", "hardlink", "fifo":
				e = os.Remove(bin)
				if e == nil {
					switch kind {
					case "symlink":
						e = os.Symlink(filepath.Join(source, "config.txt"), bin)
					case "hardlink":
						e = os.Link(filepath.Join(source, "config.txt"), bin)
					case "fifo":
						e = syscall.Mkfifo(bin, 0600)
					}
				}
			case "journal":
				e = os.WriteFile(filepath.Join(dir, "artifacts/preimage-journal.jsonl"), []byte("{}\n"), 0600)
			case "approval":
				e = os.WriteFile(filepath.Join(dir, "artifacts/retention-approval.json"), []byte("{}\n"), 0600)
			default:
				b := read(t, meta)
				m, e2 := decodePreimage(b)
				if e2 != nil {
					t.Fatal(e2)
				}
				switch kind {
				case "hash":
					m.Captured = strings.Repeat("a", 64)
				case "size":
					m.Size = 65537
				case "path":
					m.Path = "../escape"
				case "operation", "cross-operation":
					m.OperationID = strings.Repeat("b", 64)
				case "cross-run":
					m.RunID = strings.Repeat("c", 32)
				}
				b, e = json.Marshal(m)
				if e == nil {
					e = os.WriteFile(meta, b, 0600)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			if p, e := s.PreparePreimage(ctx, id, op, "review", source, true); e == nil {
				p.Close()
				t.Fatal("adulterated capture accepted")
			}
		})
	}
}
func TestPreimageExportOperationSelectionAndLegacy(t *testing.T) {
	ctx, source, _, s, id, _ := preimageExportFixture(t)
	for _, op := range []string{"config.txt", "../x", "/source", strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("f", 64), "*", ""} {
		if p, e := s.PreparePreimage(ctx, id, op, "review", source, true); e == nil {
			p.Close()
			t.Fatal("invalid selection")
		}
	}
	old, e := Create(ctx, s.base, source)
	if e != nil {
		t.Fatal(e)
	}
	otherID := old.ID()
	old.Close()
	if p, e := s.PreparePreimage(ctx, otherID, strings.Repeat("a", 64), "review", source, true); e == nil {
		p.Close()
		t.Fatal("ready eligible")
	}
	_, legacySource, legacyBase, legacy, plan := preimageFixture(t, "initial\n")
	p, e := legacy.Prepare(ctx, plan)
	if e != nil {
		t.Fatal(e)
	}
	pm, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pm.Apply(ctx); e != nil {
		t.Fatal(e)
	}
	legacyID := legacy.ID()
	legacy.Close()
	ls, e := OpenStore(legacyBase)
	if e != nil {
		t.Fatal(e)
	}
	defer ls.Close()
	if p, e := ls.PreparePreimage(ctx, legacyID, strings.Repeat("a", 64), "review", legacySource, true); e == nil {
		p.Close()
		t.Fatal("legacy eligible")
	}
}
func TestPreimageExportCaptureLimits(t *testing.T) {
	for _, size := range []int{0, 65536, 65537} {
		t.Run(string(rune(size+65)), func(t *testing.T) {
			if size > 65536 {
				ctx, source, base, _, _ := preimageFixture(t, "initial\n")
				if e := os.WriteFile(filepath.Join(source, "too-large.txt"), []byte(strings.Repeat("x", size)), 0600); e != nil {
					t.Fatal(e)
				}
				if r, e := Create(ctx, base, source); e == nil {
					r.Close()
					t.Fatal("oversize import")
				}
				return
			}
			ctx, source, base, r, b := preimageFixture(t, strings.Repeat("x", size))
			if e := os.Chmod(filepath.Dir(base), 0700); e != nil {
				t.Fatal(e)
			}
			p, e := r.PrepareWithOptions(ctx, b, ApplyOptions{true, true})
			if e != nil {
				t.Fatal(e)
			}
			pm, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = pm.Apply(ctx); e != nil {
				t.Fatal(e)
			}
			id := r.ID()
			op := r.Manifest().Retention.OperationID
			r.Close()
			s, e := OpenStore(base)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			ep, e := s.PreparePreimage(ctx, id, op, "review", source, true)
			if e != nil {
				t.Fatal(e)
			}
			permit, e := ep.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			result, e := permit.Export(ctx)
			if e != nil || result.Bytes != size {
				t.Fatal(result, e)
			}
			copy := *permit
			if _, e = copy.Export(context.Background()); !errors.Is(e, editcontract.ErrUsed) {
				t.Fatal("copied permit reused")
			}
		})
	}
}
func TestPreimageExportCaptureChangeAfterApproval(t *testing.T) {
	ctx, source, base, s, id, _ := preimageExportFixture(t)
	p := preimageExportPrepare(t, ctx, s, id, source)
	permit, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	op := p.state.preimage.OperationID
	p.state.hook = func(phase string) error {
		if phase == "before_publish" {
			return os.WriteFile(filepath.Join(base, id, "preimages", op+".bin"), []byte("changed"), 0600)
		}
		return nil
	}
	result, e := permit.Export(ctx)
	if e == nil || result.State != "not_published" {
		t.Fatal(result, e)
	}
	if _, e = permit.Export(ctx); !errors.Is(e, editcontract.ErrUsed) {
		t.Fatal(e)
	}
}
func TestPreimageExportCreateOperationBlocked(t *testing.T) {
	ctx, source, base, r, _ := preimageFixture(t, "initial\n")
	plan := workspaceplan.Plan{Version: 1, Kind: "workspace_apply", Operations: []workspaceplan.Operation{{Type: "create_file", Path: "new.txt", Content: "new", Precondition: workspaceplan.Precondition{Absent: boolPointer(true)}, Validation: workspaceplan.Validation{SHA256: workspaceplan.Hash([]byte("new"))}}}, Blockers: []string{}, Assumptions: []string{}}
	b, e := json.Marshal(plan)
	if e != nil {
		t.Fatal(e)
	}
	p, e := r.Prepare(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	pm, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pm.Apply(ctx); e != nil {
		t.Fatal(e)
	}
	id := r.ID()
	r.Close()
	s, e := OpenStore(base)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if p, e := s.PreparePreimage(ctx, id, preimageID(id, workspaceplan.Hash(b), 0), "review", source, true); e == nil {
		p.Close()
		t.Fatal("create operation eligible")
	}
}
func boolPointer(v bool) *bool { return &v }

func TestPreimageExportRenameRejectsExistingDestination(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"stage", "destination"} {
		if e := os.Mkdir(filepath.Join(dir, name), 0700); e != nil {
			t.Fatal(e)
		}
		if name == "stage" {
			if e := os.WriteFile(filepath.Join(dir, name, "sentinel"), []byte(name), 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	// An ordinary rename could replace this empty directory. The test must
	// exercise NOREPLACE, rather than rely on ENOTEMPTY for protection.
	before, e := os.Stat(filepath.Join(dir, "destination"))
	if e != nil {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	parent, e := openRead(root, ".", true)
	if e != nil {
		t.Fatal(e)
	}
	defer parent.Close()
	if e := evidenceRename(parent, "stage", "destination"); e == nil {
		t.Fatal("rename overwrote existing destination")
	}
	after, e := os.Stat(filepath.Join(dir, "destination"))
	if e != nil || !os.SameFile(before, after) {
		t.Fatal("destination replaced")
	}
	if string(read(t, filepath.Join(dir, "stage", "sentinel"))) != "stage" {
		t.Fatal("rename failure changed staging")
	}
}

func TestPreimageExportApprovalExplicitContract(t *testing.T) {
	ctx, source, _, s, id, _ := preimageExportFixture(t)
	p := preimageExportPrepare(t, ctx, s, id, source)
	for _, field := range []string{"capability=preimage_export\n", "run_integrity_state=verified\n", "preimage_format_version=1\n", "single_use=true\n"} {
		if !strings.Contains(p.View().Display, field) {
			t.Errorf("approval digest omits %q", field)
		}
	}
	if p.View().ID != workspaceplan.Hash([]byte(p.View().Display)) {
		t.Fatal("digest differs from approved display")
	}
}

func TestPreimageExportForeignCaptureBeforeProvision(t *testing.T) {
	for _, kind := range []string{"foreign-operation", "copied-capture"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, base, s, id, _ := preimageExportFixture(t)
			_, _, foreignBase, _, foreignID, _ := preimageExportFixture(t)
			foreign, e := Open(foreignBase, foreignID)
			if e != nil {
				t.Fatal(e)
			}
			foreignOp := foreign.Manifest().Retention.OperationID
			foreign.Close()
			r, e := Open(base, id)
			if e != nil {
				t.Fatal(e)
			}
			op := r.Manifest().Retention.OperationID
			r.Close()
			selected := foreignOp
			if kind == "copied-capture" {
				selected = op
				for _, ext := range []string{".json", ".bin"} {
					data := read(t, filepath.Join(foreignBase, foreignID, "preimages", foreignOp+ext))
					if e := os.WriteFile(filepath.Join(base, id, "preimages", op+ext), data, 0600); e != nil {
						t.Fatal(e)
					}
				}
			}
			if p, e := s.PreparePreimage(ctx, id, selected, "review", source, true); e == nil {
				p.Close()
				t.Fatal("foreign capture accepted")
			}
			entries, e := os.ReadDir(filepath.Dir(base))
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".daimon-preimage-exports-") {
					t.Fatal("provisioned before rejecting foreign capture")
				}
			}
		})
	}
}
