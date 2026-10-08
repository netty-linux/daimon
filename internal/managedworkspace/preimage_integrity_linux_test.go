//go:build linux && amd64

package managedworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

func TestPreimageMetadataTamperingAndIdentity(t *testing.T) {
	ctx, _, _, r, b := preimageFixture(t, "old")
	p := preimagePrepare(t, ctx, r, b)
	permit, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = permit.Apply(ctx); e != nil {
		t.Fatal(e)
	}
	id := preimageID(r.ID(), workspaceplan.Hash(b), 0)
	name := filepath.Join(r.directory, "preimages", id+".json")
	original := read(t, name)
	for _, field := range []string{"run_id", "operation_id", "operation_index", "relative_path", "expected_before_sha256", "captured_before_sha256", "approved_after_sha256", "preimage_size_bytes", "storage_id", "plan_sha256", "approval_sha256", "journal_sequence", "preimage_format_version", "integrity_state"} {
		t.Run(field, func(t *testing.T) {
			var m map[string]any
			if json.Unmarshal(original, &m) != nil {
				t.Fatal("fixture metadata")
			}
			switch field {
			case "operation_index":
				m[field] = 1
			case "journal_sequence":
				m[field] = 3
			case "preimage_format_version", "preimage_size_bytes":
				m[field] = 9
			case "run_id":
				m[field] = strings.Repeat("b", 32)
			case "relative_path":
				m[field] = "other.txt"
			case "integrity_state":
				m[field] = "persisted"
			default:
				m[field] = strings.Repeat("a", 64)
			}
			changed, _ := json.Marshal(m)
			if os.WriteFile(name, changed, 0600) != nil {
				t.Fatal("tamper")
			}
			if _, e := r.Report(); e == nil {
				t.Fatal("tampered report accepted", field)
			}
			if os.WriteFile(name, original, 0600) != nil {
				t.Fatal("restore fixture")
			}
		})
	}
	for _, invalid := range [][]byte{[]byte("null"), []byte(`{"run_id":null}`), append(original, []byte(" {}")...), []byte(`{"version":1,"version":1}`)} {
		if _, e := decodePreimage(invalid); e == nil {
			t.Fatal("malformed metadata")
		}
	}

	for _, field := range []string{"operation_index", "preimage_size_bytes", "journal_sequence"} {
		var fields map[string]json.RawMessage
		json.Unmarshal(original, &fields)
		delete(fields, field)
		missing, _ := json.Marshal(fields)
		if _, e := decodePreimage(missing); e == nil {
			t.Fatal("required metadata omitted", field)
		}
	}
	bin := filepath.Join(r.directory, "preimages", id+".bin")
	if e := os.Rename(bin, bin+"-old"); e != nil {
		t.Fatal(e)
	}
	if os.WriteFile(bin, []byte("old"), 0600) != nil {
		t.Fatal("substitute")
	}
	if _, e := r.Report(); e == nil {
		t.Fatal("same bytes different identity accepted")
	}
}

func TestPreimageCancellationDenialAndJournal(t *testing.T) {
	for _, phase := range []string{"capture", "close:preimages/", "directory_close:preimages/", "before_replace", "journal_sync"} {
		t.Run("cancel "+phase, func(t *testing.T) {
			parent, _, _, r, b := preimageFixture(t, "old")
			ctx, cancel := context.WithCancel(parent)
			defer cancel()
			p := preimagePrepare(t, ctx, r, b)
			p.preimageHook = func(s string) error {
				if strings.HasPrefix(s, phase) {
					cancel()
				}
				return nil
			}
			permit, e := p.Approve(ctx, allow)
			if e != nil {
				t.Fatal(e)
			}
			report, e := permit.Apply(ctx)
			if e == nil || !errors.Is(e, context.Canceled) || report.Status == "succeeded" {
				t.Fatal(report, e)
			}
			if string(read(t, filepath.Join(r.output, "config.txt"))) != "old" {
				t.Fatal("write after cancel")
			}
		})
	}
	for _, kind := range []string{"deny", "display", "journal"} {
		t.Run(kind, func(t *testing.T) {
			ctx, _, _, r, b := preimageFixture(t, "old")
			p := preimagePrepare(t, ctx, r, b)
			if kind == "journal" {
				permit, e := p.Approve(ctx, allow)
				if e != nil {
					t.Fatal(e)
				}
				r.journalFile.Close()
				report, e := permit.Apply(ctx)
				if e == nil || report.Status == "succeeded" {
					t.Fatal("journal ignored")
				}
			} else {
				reviewer := reviewFunc(func(context.Context, editcontract.Review) (editcontract.Decision, error) {
					if kind == "display" {
						return editcontract.Deny, errors.New("synthetic display")
					}
					return editcontract.Deny, nil
				})
				if _, e := p.Approve(ctx, reviewer); e == nil {
					t.Fatal("approval allowed")
				}
			}
			if _, e := os.Stat(filepath.Join(r.directory, "preimages")); !os.IsNotExist(e) {
				t.Fatal("unauthorized capture")
			}
			if string(read(t, filepath.Join(r.output, "config.txt"))) != "old" {
				t.Fatal("unauthorized replace")
			}
		})
	}
}

func TestPreimagePostCaptureReplaceFailureHonest(t *testing.T) {
	ctx, _, _, r, b := preimageFixture(t, "old")
	p := preimagePrepare(t, ctx, r, b)
	permit, e := p.Approve(ctx, allow)
	if e != nil {
		t.Fatal(e)
	}
	permit.applyOne = func(context.Context, int) error { return errors.New("synthetic replace close failure") }
	report, e := permit.Apply(ctx)
	if e == nil || report.Status != "unknown" || report.Retention == nil || report.Retention.State != "verified" {
		t.Fatal(report, e)
	}
	if string(read(t, filepath.Join(r.output, "config.txt"))) != "old" {
		t.Fatal("rollback or unexpected write")
	}
	if _, e := r.Report(); e != nil {
		t.Fatal("honest retained evidence", e)
	}
}

func TestPreimageCrossRunAndDuplicateJournal(t *testing.T) {
	ctx, _, _, first, plan := preimageFixture(t, "old")
	_, _, _, second, secondPlan := preimageFixture(t, "old")
	for _, item := range []struct {
		r    *Run
		plan []byte
	}{{first, plan}, {second, secondPlan}} {
		p := preimagePrepare(t, ctx, item.r, item.plan)
		permit, err := p.Approve(ctx, allow)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := permit.Apply(ctx); err != nil {
			t.Fatal(err)
		}
	}
	foreign, err := decodePreimage(read(t, filepath.Join(second.directory, "artifacts/preimage-journal.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.verifyPreimage(foreign, plan, foreign.ApprovalSHA256); err == nil {
		t.Fatal("foreign run capture accepted")
	}
	localBytes := read(t, filepath.Join(first.directory, "artifacts/preimage-journal.jsonl"))
	local, err := decodePreimage(localBytes)
	if err != nil {
		t.Fatal(err)
	}
	local.Index = 1
	local.JournalSequence = 3
	if err := first.verifyPreimage(local, plan, local.ApprovalSHA256); err == nil {
		t.Fatal("capture reused for another operation")
	}
	duplicated := append(append([]byte{}, localBytes...), localBytes...)
	if _, err := decodePreimage(duplicated); err == nil {
		t.Fatal("duplicate operation journal accepted")
	}
	journal := filepath.Join(first.directory, "artifacts/preimage-journal.jsonl")
	if err := os.WriteFile(journal, duplicated, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Report(); err == nil {
		t.Fatal("duplicate journal yielded integral report")
	}
}

func TestPreimageIncompleteSummaryRejectsUnboundFields(t *testing.T) {
	ctx, _, base, r, plan := preimageFixture(t, "old")
	p := preimagePrepare(t, ctx, r, plan)
	deny := reviewFunc(func(context.Context, editcontract.Review) (editcontract.Decision, error) {
		return editcontract.Deny, nil
	})
	if _, err := p.Approve(ctx, deny); !errors.Is(err, editcontract.ErrDenied) {
		t.Fatal(err)
	}
	id, original := r.ID(), r.Manifest()
	r.Close()
	store, err := OpenStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	canary := "API_KEY=review-metadata-canary-" + workspaceplan.Hash([]byte(t.TempDir()))
	for _, state := range []string{"not_requested", "capturing", "captured", "persisted", "failed", "unknown"} {
		for _, field := range []string{"operation_id", "preimage_size_bytes"} {
			t.Run(state+"/"+field, func(t *testing.T) {
				m := original
				m.Retention = copyRetention(original.Retention)
				m.Retention.State = state
				if field == "operation_id" {
					m.Retention.OperationID = canary
				} else {
					m.Retention.Size = MaxFileBytes + 1
				}
				data, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(base, id, "manifest.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				summary, err := store.Inspect(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if summary.State != "invalid" || summary.ManifestVerified || summary.Retention != nil {
					t.Fatal("unbound incomplete retention metadata exposed", state, field)
				}
				encoded, _ := json.Marshal(summary)
				if strings.Contains(string(encoded), canary) {
					t.Fatal("metadata content escaped to inspect")
				}
			})
		}
	}
}
