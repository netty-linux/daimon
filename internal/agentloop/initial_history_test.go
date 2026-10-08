package agentloop

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
	"testing"
)

type historyModel struct {
	calls   int
	request model.ModelRequest
}

func (m *historyModel) Generate(_ context.Context, r model.ModelRequest) (model.ModelResponse, error) {
	m.calls++
	m.request = model.CloneRequest(r)
	r.Messages[0].Content = "mutated"
	return model.ModelResponse{FinalText: "answer"}, nil
}

type historyAllow struct{}

func (historyAllow) Authorize(context.Context, ToolAuthorizationRequest) (ToolDecision, error) {
	return ToolDecisionAllow, nil
}
func TestInitialHistoryValidationAndAccounting(t *testing.T) {
	for _, mode := range []string{"valid", "tool", "calls", "blank", "bytes", "count"} {
		t.Run(mode, func(t *testing.T) {
			m := &historyModel{}
			budget := DefaultBudget()
			history := []model.Message{{Role: model.RoleUser, Content: "earlier"}, {Role: model.RoleAssistant, Content: "previous"}}
			switch mode {
			case "tool":
				history[0].Role = model.RoleTool
			case "calls":
				history[0].ToolCalls = []model.ToolCall{{ID: "call", Name: "echo"}}
			case "blank":
				history[0].Content = " "
			case "bytes":
				budget.MaxHistoryBytes = 14
			case "count":
				budget.MaxHistoryMessages = 2
			}
			loop := Loop{Model: m, Registry: &tools.Registry{}, Authorizer: historyAllow{}, Budget: budget, InitialHistory: history}
			result, err := loop.Run(context.Background(), "new")
			if mode == "valid" {
				if err != nil || len(m.request.Messages) != 3 || history[0].Content != "earlier" || result.History[0].Content != "earlier" {
					t.Fatal("history/cloning")
				}
			} else {
				if err == nil || m.calls != 0 {
					t.Fatal("invalid context reached model")
				}
				if mode == "bytes" || mode == "count" {
					if !errors.Is(err, LimitError{Kind: LimitMaxHistoryBytes}) && !errors.Is(err, LimitError{Kind: LimitMaxHistoryMessages}) {
						t.Fatal("budget identity")
					}
				}
			}
		})
	}
}
