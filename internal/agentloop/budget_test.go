package agentloop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestDefaultBudgetValid(t *testing.T) {
	if err := DefaultBudget().Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestBudgetValidation(t *testing.T) {
	b := DefaultBudget()
	counts := []struct {
		name  string
		field *int
		kind  LimitKind
	}{
		{"steps", &b.MaxSteps, LimitMaxSteps},
		{"calls per step", &b.MaxToolCallsPerStep, LimitMaxToolCallsPerStep},
		{"total calls", &b.MaxTotalToolCalls, LimitMaxTotalToolCalls},
		{"user bytes", &b.MaxUserMessageBytes, LimitMaxUserMessageBytes},
		{"final bytes", &b.MaxFinalAnswerBytes, LimitMaxFinalAnswerBytes},
		{"argument bytes", &b.MaxToolArgumentBytes, LimitMaxToolArgumentBytes},
		{"result bytes", &b.MaxToolResultBytes, LimitMaxToolResultBytes},
		{"history messages", &b.MaxHistoryMessages, LimitMaxHistoryMessages},
		{"history bytes", &b.MaxHistoryBytes, LimitMaxHistoryBytes},
	}
	for _, field := range counts {
		original := *field.field
		for _, value := range []int{0, -1} {
			*field.field = value
			assertInvalidBudget(t, b, field.kind)
		}
		*field.field = original
	}
	durations := []struct {
		field *time.Duration
		kind  LimitKind
	}{
		{&b.MaxRunDuration, LimitMaxRunDuration},
		{&b.MaxModelCallDuration, LimitMaxModelCallDuration},
		{&b.MaxToolCallDuration, LimitMaxToolCallDuration},
	}
	for _, field := range durations {
		original := *field.field
		for _, value := range []time.Duration{0, -1} {
			*field.field = value
			assertInvalidBudget(t, b, field.kind)
		}
		*field.field = original
	}
	if err := (Budget{}).Validate(); err == nil {
		t.Fatal("zero budget accepted")
	}
}
func assertInvalidBudget(t *testing.T, b Budget, kind LimitKind) {
	t.Helper()
	err := b.Validate()
	var config InvalidConfigError
	var limit LimitError
	if !errors.Is(err, ErrInvalidConfig) || !errors.As(err, &config) || !errors.As(err, &limit) || limit.Kind != kind {
		t.Fatalf("%s: %v", kind, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("invalid duration mistaken for expired deadline")
	}
	l, m, sink := setup(t, final("never"))
	l.Budget = b
	r, err := l.Run(context.Background(), "user")
	if !errors.Is(err, ErrInvalidConfig) || len(m.Requests()) != 0 || len(r.History) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	assertStopped(t, sink, r, StopReasonInvalidConfig)
}
func TestTruncateResult(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		limit       int
		changed     bool
	}{
		{"exact", "1234567890", 10, false},
		{"empty", "", 1, false},
		{"one over", "12345678901", 10, true},
		{"normal marker", strings.Repeat("x", 100), 50, true},
		{"utf8 boundary", strings.Repeat("é界🙂", 20), 40, true},
		{"minimum", "abc", 1, true},
		{"short marker", strings.Repeat("a", 20), 11, true},
		{"invalid below limit", "a\xffb", 100, true},
		{"invalid at limit", "\xff", 1, true},
		{"invalid above limit", strings.Repeat("\xffa", 50), 40, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := DefaultBudget()
			b.MaxToolResultBytes = tc.limit
			got, changed := b.truncateResult(tc.input)
			if changed != tc.changed || len(got) > tc.limit || !utf8.ValidString(got) {
				t.Fatalf("%q changed=%v", got, changed)
			}
			if !changed && got != tc.input {
				t.Fatal("unchanged result changed")
			}
			if changed && !strings.Contains(got, "truncated") && !strings.HasSuffix(got, "~") {
				t.Fatalf("missing complete marker: %q", got)
			}
			if tc.name == "normal marker" && !strings.HasSuffix(got, "\n[truncated: original_bytes=100]") {
				t.Fatal(got)
			}
		})
	}
}
func TestLimitErrorMatching(t *testing.T) {
	err := LimitError{Kind: LimitMaxSteps, Limit: 3, Actual: 3}
	var got LimitError
	if !errors.Is(err, ErrMaxSteps) || !errors.Is(err, LimitError{}) || !errors.Is(err, LimitError{Kind: LimitMaxSteps}) || !errors.As(err, &got) || got != err {
		t.Fatal(err)
	}
	if errors.Is(err, LimitError{Kind: LimitMaxHistoryBytes}) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("matched different limit")
	}
	for _, kind := range []LimitKind{LimitMaxRunDuration, LimitMaxModelCallDuration, LimitMaxToolCallDuration} {
		if !errors.Is(LimitError{Kind: kind, Limit: 1, Actual: 1}, context.DeadlineExceeded) {
			t.Fatal(kind)
		}
	}
}
func TestTimeoutHelpersPreserveParent(t *testing.T) {
	b := DefaultBudget()
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer cancel()
	want, _ := parent.Deadline()
	// Own deadlines deliberately longer than the parent.
	b.MaxRunDuration = 2 * time.Hour
	b.MaxModelCallDuration = 2 * time.Hour
	b.MaxToolCallDuration = 2 * time.Hour
	for _, apply := range []func(context.Context) (context.Context, context.CancelFunc){b.ApplyRunTimeout, b.ApplyModelTimeout, b.ApplyToolTimeout} {
		child, cleanup := apply(parent)
		got, ok := child.Deadline()
		if !ok || !got.Equal(want) {
			t.Fatalf("deadline=%v want=%v", got, want)
		}
		cleanup()
	}
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancelExpired()
	child, cleanup := b.ApplyRunTimeout(expired)
	defer cleanup()
	if !errors.Is(child.Err(), context.DeadlineExceeded) {
		t.Fatal(child.Err())
	}
}
