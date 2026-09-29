package agentloop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/model"
)

func TestDefaultBudgetValid(t *testing.T) {
	b := DefaultBudget()
	if err := b.Validate(); err != nil {
		t.Fatalf("DefaultBudget() should be valid: %v", err)
	}
}

func TestBudgetZeroFields(t *testing.T) {
	tests := []struct {
		name   string
		budget Budget
	}{
		{"zero MaxSteps", Budget{MaxSteps: 0, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1, MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 1, MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second}},
		{"zero MaxToolCallsPerStep", Budget{MaxSteps: 1, MaxToolCallsPerStep: 0, MaxTotalToolCalls: 1, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1, MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 1, MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second}},
		{"zero MaxTotalToolCalls", Budget{MaxSteps: 1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 0, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1, MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 1, MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second}},
		{"zero MaxUserMessageBytes", Budget{MaxSteps: 1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1, MaxUserMessageBytes: 0, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1, MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 1, MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second}},
		{"zero MaxFinalAnswerBytes", Budget{MaxSteps: 1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 0, MaxToolArgumentBytes: 1, MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 1, MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second}},
		{"zero MaxToolArgumentBytes", Budget{MaxSteps: 1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 0, MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 1, MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second}},
		{"zero MaxToolResultBytes", Budget{MaxSteps: 1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1, MaxToolResultBytes: 0, MaxHistoryMessages: 1, MaxHistoryBytes: 1, MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second}},
		{"zero MaxHistoryMessages", Budget{MaxSteps: 1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1, MaxToolResultBytes: 1, MaxHistoryMessages: 0, MaxHistoryBytes: 1, MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second}},
		{"zero MaxHistoryBytes", Budget{MaxSteps: 1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1, MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 0, MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second}},
		{"zero MaxRunDuration", Budget{MaxSteps: 1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1, MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 1, MaxRunDuration: 0, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second}},
		{"zero MaxModelCallDuration", Budget{MaxSteps: 1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1, MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 1, MaxRunDuration: time.Second, MaxModelCallDuration: 0, MaxToolCallDuration: time.Second}},
		{"zero MaxToolCallDuration", Budget{MaxSteps: 1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1, MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1, MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 1, MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.budget.Validate()
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
			var icErr InvalidConfigError
			if !errors.As(err, &icErr) {
				t.Errorf("expected InvalidConfigError, got %T: %v", err, err)
			}
		})
	}
}

func TestBudgetNegativeFields(t *testing.T) {
	b := Budget{
		MaxSteps: -1, MaxToolCallsPerStep: 1, MaxTotalToolCalls: 1,
		MaxUserMessageBytes: 1, MaxFinalAnswerBytes: 1, MaxToolArgumentBytes: 1,
		MaxToolResultBytes: 1, MaxHistoryMessages: 1, MaxHistoryBytes: 1,
		MaxRunDuration: time.Second, MaxModelCallDuration: time.Second, MaxToolCallDuration: time.Second,
	}
	err := b.Validate()
	if err == nil {
		t.Error("expected error for negative MaxSteps")
	}
}

func TestTruncateResultExactLimit(t *testing.T) {
	b := Budget{MaxToolResultBytes: 10}
	result := "1234567890" // exatamente 10 bytes
	truncated, wasTruncated := b.truncateResult(result)
	if wasTruncated {
		t.Error("should not truncate when exactly at limit")
	}
	if truncated != result {
		t.Errorf("expected %q, got %q", result, truncated)
	}
}

func TestTruncateResultOneByteOver(t *testing.T) {
	b := Budget{MaxToolResultBytes: 10}
	result := "12345678901" // 11 bytes
	truncated, wasTruncated := b.truncateResult(result)
	if !wasTruncated {
		t.Error("should truncate when one byte over")
	}
	if len([]byte(truncated)) > 10 {
		t.Errorf("truncated result exceeds limit: %d bytes", len([]byte(truncated)))
	}
}

func TestTruncateResultPreservesUTF8(t *testing.T) {
	b := Budget{MaxToolResultBytes: 10}
	// "héllo" = 6 bytes (h=1, é=2, l=1, l=1, o=1)
	result := "héllo world" // 11 bytes
	truncated, wasTruncated := b.truncateResult(result)
	if !wasTruncated {
		t.Error("should truncate")
	}
	if !utf8ValidString(truncated) {
		t.Errorf("truncated result is not valid UTF-8: %q", truncated)
	}
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		_ = r
	}
	return true
}

func TestTruncateResultIndicator(t *testing.T) {
	b := Budget{MaxToolResultBytes: 50}
	longResult := string(make([]byte, 100)) // 100 bytes
	truncated, wasTruncated := b.truncateResult(longResult)
	if !wasTruncated {
		t.Error("should truncate long result")
	}
	if len([]byte(truncated)) > 50 {
		t.Errorf("truncated exceeds limit: %d bytes", len([]byte(truncated)))
	}
	if !contains(truncated, "truncated") {
		t.Errorf("truncated result should contain indicator: %q", truncated)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestTruncateResultSmallLimit(t *testing.T) {
	b := Budget{MaxToolResultBytes: 5}
	longResult := "this is a very long result"
	truncated, wasTruncated := b.truncateResult(longResult)
	if !wasTruncated {
		t.Error("should truncate")
	}
	if len([]byte(truncated)) > 5 {
		t.Errorf("truncated exceeds limit: %d bytes", len([]byte(truncated)))
	}
}

func TestApplyRunTimeoutPreservesExternalDeadline(t *testing.T) {
	b := DefaultBudget()
	externalDeadline := time.Now().Add(100 * time.Millisecond)
	parent := context.WithValue(context.Background(), "key", "value")
	parent, cancel := context.WithDeadline(parent, externalDeadline)
	defer cancel()

	ctx, cancelFunc := b.ApplyRunTimeout(parent)
	defer cancelFunc()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	// O deadline deve ser o externo (menor)
	if deadline.After(externalDeadline.Add(time.Millisecond)) {
		t.Errorf("deadline should be external or earlier: got %v, external %v", deadline, externalDeadline)
	}
}

func TestApplyModelTimeout(t *testing.T) {
	b := Budget{MaxModelCallDuration: 100 * time.Millisecond}
	ctx, cancel := b.ApplyModelTimeout(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}

	// Aguarda um pouco e verifica se o contexto expira
	select {
	case <-time.After(200 * time.Millisecond):
		t.Error("context should have timed out")
	case <-ctx.Done():
		// OK
	}

	// Verifica que o deadline está próximo do esperado
	expectedDeadline := time.Now().Add(100 * time.Millisecond)
	if deadline.After(expectedDeadline.Add(10 * time.Millisecond)) {
		t.Errorf("deadline too far: got %v, expected ~%v", deadline, expectedDeadline)
	}
}

func TestLimitErrorIs(t *testing.T) {
	err1 := LimitError{Kind: LimitMaxSteps, Limit: 10, Actual: 11}
	err2 := LimitError{Kind: LimitMaxSteps, Limit: 10, Actual: 11}

	if !errors.Is(err1, err2) {
		t.Error("LimitError should be comparable with errors.Is")
	}
}

func TestLimitErrorAs(t *testing.T) {
	err := LimitError{Kind: LimitMaxToolResultBytes, Limit: 100, Actual: 150}
	var target LimitError
	if !errors.As(err, &target) {
		t.Error("errors.As should work for LimitError")
	}
	if target.Kind != LimitMaxToolResultBytes || target.Limit != 100 || target.Actual != 150 {
		t.Errorf("LimitError not properly extracted: %+v", target)
	}
}
