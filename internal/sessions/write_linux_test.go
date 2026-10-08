//go:build linux

package sessions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/createcontract"
	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers"
)

type createReviewFunc func(context.Context, createcontract.Review) (createcontract.Decision, error)

func (f createReviewFunc) Review(ctx context.Context, r createcontract.Review) (createcontract.Decision, error) {
	return f(ctx, r)
}

type editReviewFunc func(context.Context, editcontract.Review) (editcontract.Decision, error)

func (f editReviewFunc) Review(ctx context.Context, r editcontract.Review) (editcontract.Decision, error) {
	return f(ctx, r)
}

func TestWriteExposureRequiresFullBoundReview(t *testing.T) {
	for _, replace := range []bool{false, true} {
		for _, allow := range []bool{false, true} {
			t.Run(fmtName(replace, allow), func(t *testing.T) {
				deps, options, bot, thread := fixture(t, nil)
				name := "create_file"
				if replace {
					name = "replace_file"
				}
				bot.bot.Tools = []string{name}
				target := filepath.Join(thread.thread.Workspace, "target.txt")
				if replace {
					if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				deps.Providers = &providers.Registry{}
				if err := deps.Providers.Register(providers.Factory{ID: "fixture", Build: func(providers.Config) (model.Model, error) {
					return model.NewScripted(
						model.ScriptStep{Response: model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "write-1", Name: name, Arguments: []byte(`{"path":"target.txt","content":"proposed"}`)}}}},
						model.ScriptStep{Response: model.ModelResponse{FinalText: "done"}},
					), nil
				}}); err != nil {
					t.Fatal(err)
				}
				reviewed := false
				decision := editcontract.Deny
				if allow {
					decision = editcontract.Allow
				}
				deps.Approvals = func(context.Context, ID) (Approvals, error) {
					return Approvals{
						Creations: createReviewFunc(func(_ context.Context, r createcontract.Review) (createcontract.Decision, error) {
							reviewed = true
							if r.Path != "target.txt" || !strings.Contains(r.Display, "proposed") || r.RootID == "" || r.RunID == "" {
								t.Error("incomplete/unbound preview")
							}
							return decision, nil
						}),
						Replacements: editReviewFunc(func(_ context.Context, r editcontract.Review) (editcontract.Decision, error) {
							reviewed = true
							if r.Path != "target.txt" || !strings.Contains(r.Display, "proposed") || r.RootID == "" || r.RunID == "" {
								t.Error("incomplete/unbound preview")
							}
							return decision, nil
						}),
					}, nil
				}
				m := manager(t, deps, options)
				if _, err := m.Start(testContext(t), StartRequest{SessionID: "session-a", ThreadID: "thread-a", Message: "propose one file", EnableReplaceFile: replace, EnableCreateFile: !replace}); err != nil {
					t.Fatal(err)
				}
				wait(t, m, "session-a")
				if !reviewed {
					t.Fatal("write skipped review")
				}
				data, err := os.ReadFile(target)
				if allow {
					if err != nil || string(data) != "proposed" {
						t.Fatal("approved write missing", err)
					}
				} else if replace {
					if err != nil || string(data) != "original" {
						t.Fatal("denied replacement changed target")
					}
				} else if !os.IsNotExist(err) {
					t.Fatal("denied create produced file")
				}
			})
		}
	}
}

func fmtName(replace, allow bool) string {
	if replace {
		if allow {
			return "replace-allow"
		}
		return "replace-deny"
	}
	if allow {
		return "create-allow"
	}
	return "create-deny"
}
