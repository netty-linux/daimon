//go:build linux

package sessions

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebWritesReuseCompleteContracts(t *testing.T) {
	for _, tool := range []string{"replace_file", "create_file"} {
		for _, mode := range []string{"allow", "deny", "abort", "mutate", "no-capability"} {
			t.Run(tool+"-"+mode, func(t *testing.T) {
				deps, options, bot, thread := fixture(t, func(_ context.Context, r model.ModelRequest) (model.ModelResponse, error) {
					if r.Messages[len(r.Messages)-1].Role == model.RoleUser {
						return model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "exact-write", Name: tool, Arguments: []byte(`{"path":"target.txt","content":"proposed\n<safe>"}`)}}}, nil
					}
					return model.ModelResponse{FinalText: "done"}, nil
				})
				deps.WebApprovals = true
				bot.bot.Tools = []string{tool}
				target := filepath.Join(thread.thread.Workspace, "target.txt")
				if tool == "replace_file" {
					if err := os.WriteFile(target, []byte("original\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				m := manager(t, deps, options)
				req := StartRequest{SessionID: "write", ThreadID: "thread", Message: "propose", EnableReplaceFile: tool == "replace_file" && mode != "no-capability", EnableCreateFile: tool == "create_file" && mode != "no-capability"}
				if _, err := m.Start(testContext(t), req); err != nil {
					t.Fatal(err)
				}
				if mode == "no-capability" {
					snap, err := m.Wait(testContext(t), "write")
					if err == nil || snap.ErrorCategory != ToolResolution {
						t.Fatal("browser created capability")
					}
					return
				}
				p := awaitApproval(t, m, "write", "")
				if p.Kind != "write" || p.Target != `"target.txt"` || !strings.Contains(p.Preview, `proposed\n<safe>`) || !strings.Contains(p.Preview, "Limites:") {
					t.Fatal("incomplete preview")
				}
				if tool == "replace_file" && !strings.Contains(p.Preview, "original") {
					t.Fatal("missing original")
				}
				if tool == "create_file" && !strings.Contains(p.Preview, "AUSENTE") {
					t.Fatal("absence omitted")
				}
				if mode == "mutate" {
					if err := os.WriteFile(target, []byte("external"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "abort" {
					if err := m.Abort("write"); err != nil {
						t.Fatal(err)
					}
				} else {
					decision := ApprovalAllow
					if mode == "deny" {
						decision = ApprovalDeny
					}
					if err := m.ResolveApproval("write", p.ID, decision); err != nil {
						t.Fatal(err)
					}
				}
				snap, err := m.Wait(testContext(t), "write")
				if mode == "mutate" && err == nil {
					t.Fatal("changed target authorized")
				}
				if mode == "abort" && snap.Status != Aborted {
					t.Fatal("abort")
				}
				data, readErr := os.ReadFile(target)
				switch mode {
				case "allow":
					if readErr != nil || string(data) != "proposed\n<safe>" {
						t.Fatal("approved bytes")
					}
				case "mutate":
					if readErr != nil || string(data) != "external" {
						t.Fatal("conflicting file overwritten")
					}
				default:
					if tool == "replace_file" {
						if readErr != nil || string(data) != "original\n" {
							t.Fatal("denied/aborted replacement")
						}
					} else if !os.IsNotExist(readErr) {
						t.Fatal("denied/aborted creation")
					}
				}
				late := m.ResolveApproval("write", p.ID, ApprovalAllow)
				if !errors.Is(late, ErrApprovalResolved) && !errors.Is(late, ErrApprovalNotPending) {
					t.Fatal("write permit replay")
				}
				entries, err := os.ReadDir(thread.thread.Workspace)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if entry.Name() != "target.txt" {
						t.Fatal("temporary leaked")
					}
				}
			})
		}
	}
}
