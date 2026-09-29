package agentloop

import (
	"testing"

	"github.com/netty-linux/daimon/internal/model"
)

func TestMessageBytes(t *testing.T) {
	msg := model.Message{
		Role:    "user",
		Content: "hello",
	}
	size := messageBytes(msg)
	if size != 5 {
		t.Errorf("expected 5 bytes, got %d", size)
	}
}

func TestMessageBytesWithToolCalls(t *testing.T) {
	msg := model.Message{
		Role:    "assistant",
		Content: "calling tool",
		ToolCalls: []model.ToolCall{
			{ID: "call1", Name: "echo", Arguments: "arg1"},
			{ID: "call2", Name: "read_file", Arguments: "arg2"},
		},
	}
	size := messageBytes(msg)
	// Content (12) + ToolCalls: ID+Name+Arguments para cada
	// call1: 5 + 4 + 4 = 13
	// call2: 5 + 9 + 4 = 18
	// Total: 12 + 13 + 18 = 43
	if size != 43 {
		t.Errorf("expected 43 bytes, got %d", size)
	}
}

func TestToolCallArgumentsBytes(t *testing.T) {
	calls := []model.ToolCall{
		{ID: "1", Name: "echo", Arguments: "hello"},
		{ID: "2", Name: "read_file", Arguments: "world"},
	}
	size := toolCallArgumentsBytes(calls)
	if size != 10 {
		t.Errorf("expected 10 bytes, got %d", size)
	}
}

func TestHistoryBytes(t *testing.T) {
	history := []model.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	size := historyBytes(history)
	if size != 7 {
		t.Errorf("expected 7 bytes, got %d", size)
	}
}

func TestCanFitInHistory(t *testing.T) {
	history := []model.Message{
		{Role: "user", Content: "hello"},
	}
	newMsg := model.Message{Role: "assistant", Content: "hi"}

	if !canFitInHistory(history, newMsg, 10, 100) {
		t.Error("should fit")
	}

	if canFitInHistory(history, newMsg, 1, 100) {
		t.Error("should not fit (max messages)")
	}

	if canFitInHistory(history, newMsg, 10, 5) {
		t.Error("should not fit (max bytes)")
	}
}

func TestReserveHistoryForResults(t *testing.T) {
	history := []model.Message{
		{Role: "user", Content: "hello"},
	}
	assistantMsg := model.Message{
		Role:    "assistant",
		Content: "calling",
		ToolCalls: []model.ToolCall{
			{ID: "1", Name: "echo", Arguments: "arg"},
		},
	}

	if !reserveHistoryForResults(history, assistantMsg, 1, 10, 1000, 64*1024) {
		t.Error("should reserve successfully")
	}

	// Testa limite de mensagens
	if reserveHistoryForResults(history, assistantMsg, 100, 5, 1000, 64*1024) {
		t.Error("should not reserve (too many messages)")
	}

	// Testa limite de bytes
	if reserveHistoryForResults(history, assistantMsg, 1, 10, 10, 64*1024) {
		t.Error("should not reserve (not enough bytes)")
	}
}
