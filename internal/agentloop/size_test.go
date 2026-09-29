package agentloop

import (
	"errors"
	"testing"

	"github.com/netty-linux/daimon/internal/model"
)

func TestMessageAndHistoryBytes(t *testing.T) {
	msg := model.Message{Content: "é", ToolCallID: "result-id", ToolCalls: []model.ToolCall{call("call", "echo", `{"text":"🙂"}`)}}
	want := len("é") + len("result-id") + len("call") + len("echo") + len(`{"text":"🙂"}`)
	if messageBytes(msg) != want {
		t.Fatal(messageBytes(msg), want)
	}
	if historyBytes([]model.Message{msg, {Content: "abc"}}) != want+3 {
		t.Fatal("history byte count")
	}
	// Malformed JSON still occupies bytes and cannot bypass the argument budget.
	if messageBytes(model.Message{ToolCalls: []model.ToolCall{call("id", "echo", "{")}}) != 7 {
		t.Fatal("invalid JSON not counted")
	}
}
func TestHistoryReservation(t *testing.T) {
	history := []model.Message{{Content: "user"}}
	calls := []model.ToolCall{call("long-id", "echo", `{}`), call("other-id", "echo", `{}`)}
	assistant := model.Message{ToolCalls: calls}
	b := DefaultBudget()
	b.MaxToolResultBytes = 10
	exact := historyBytes(history) + messageBytes(assistant) + 2*10 + len("long-id") + len("other-id")
	b.MaxHistoryMessages = 4
	b.MaxHistoryBytes = exact
	if err := historyLimit(history, assistant, calls, b); err != nil {
		t.Fatal(err)
	}
	b.MaxHistoryBytes--
	var limit LimitError
	if err := historyLimit(history, assistant, calls, b); !errors.As(err, &limit) || limit.Kind != LimitMaxHistoryBytes || limit.Actual != int64(exact) {
		t.Fatal(err)
	}
	b.MaxHistoryBytes = exact
	b.MaxHistoryMessages = 3
	if err := historyLimit(history, assistant, calls, b); !errors.As(err, &limit) || limit.Kind != LimitMaxHistoryMessages || limit.Actual != 4 {
		t.Fatal(err)
	}
}
func TestReservationCannotOverflow(t *testing.T) {
	b := DefaultBudget()
	b.MaxHistoryBytes = maxInt
	b.MaxToolResultBytes = maxInt
	calls := []model.ToolCall{call("a", "echo", `{}`), call("b", "echo", `{}`)}
	var limit LimitError
	if err := historyLimit(nil, model.Message{ToolCalls: calls}, calls, b); !errors.As(err, &limit) || limit.Kind != LimitMaxHistoryBytes || limit.Actual != int64(maxInt) {
		t.Fatal(err)
	}
	if sizeSum(maxInt, 1) != maxInt {
		t.Fatal("overflow did not saturate")
	}
}
