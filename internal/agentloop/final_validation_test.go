package agentloop

import (
	"context"
	"errors"
	"testing"
)

func TestFinalValidationRecoveryBudget(t *testing.T) {
	for _, tc := range []struct {
		name           string
		steps, history int
		want           error
		requests       int
	}{
		{"within budget", 3, 10, nil, 2},
		{"no remaining model step", 1, 10, ErrMaxSteps, 1},
		{"history reservation", 3, 2, LimitError{Kind: LimitMaxHistoryMessages}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, m, sink := setup(t, final("invalid"), final("valid"), final("must not run"))
			l.Budget.MaxSteps = tc.steps
			l.Budget.MaxHistoryMessages = tc.history
			l.ValidateFinal = func(text string) (string, error) {
				if text == "valid" {
					return "", nil
				}
				return "correct scope", nil
			}
			r, err := l.Run(context.Background(), "original objective")
			if !errors.Is(err, tc.want) || len(m.Requests()) != tc.requests {
				t.Fatal("budget bypass", r, err)
			}
			if tc.requests == 2 {
				req := m.Requests()[1]
				if len(req.Tools) != 0 || len(req.Messages) != 3 || req.Messages[0].Content != "original objective" || req.Messages[1].Content != "invalid" || req.Messages[2].Content != "correct scope" {
					t.Fatal("lost history or tool-free recovery")
				}
			}
			if r.ToolCalls != 0 {
				t.Fatal("unexpected effects")
			}
			stops := 0
			for _, event := range sink.Events() {
				if event.Kind == LoopStopped {
					stops++
					if event.StopReason != r.StopReason {
						t.Fatal("stop mismatch")
					}
				}
			}
			if stops != 1 {
				t.Fatal("duplicate stop")
			}
		})
	}
}

func TestFinalValidationAtMostOneRecovery(t *testing.T) {
	l, m, _ := setup(t, final("invalid"), final("invalid"), final("must not run"))
	l.ValidateFinal = func(string) (string, error) { return "correct scope", nil }
	r, err := l.Run(context.Background(), "objective")
	if !errors.Is(err, ErrInvalidResponse) || len(m.Requests()) != 2 || r.FinalAnswer != "" {
		t.Fatal("unbounded recovery", err)
	}
}

func TestFinalValidationCancellation(t *testing.T) {
	l, m, _ := setup(t, final("invalid"), final("must not run"))
	ctx, cancel := context.WithCancel(context.Background())
	l.ValidateFinal = func(string) (string, error) { cancel(); return "correct scope", nil }
	r, err := l.Run(ctx, "objective")
	if !errors.Is(err, context.Canceled) || len(m.Requests()) != 1 || r.FinalAnswer != "" || len(r.History) != 1 {
		t.Fatal("cancellation bypass", err)
	}
}

func TestFinalValidationRecoveryByteLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Budget)
		kind   LimitKind
	}{
		{"recovery input", func(b *Budget) { b.MaxUserMessageBytes = 4 }, LimitMaxUserMessageBytes},
		{"combined history", func(b *Budget) { b.MaxHistoryBytes = 8 }, LimitMaxHistoryBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, m, _ := setup(t, final("bad"), final("must not run"))
			tc.change(&l.Budget)
			l.ValidateFinal = func(string) (string, error) { return "correct scope", nil }
			r, err := l.Run(context.Background(), "x")
			if !errors.Is(err, LimitError{Kind: tc.kind}) || len(m.Requests()) != 1 || len(r.History) != 1 || r.FinalAnswer != "" {
				t.Fatal("recovery byte limit bypass", err)
			}
		})
	}
}

func TestFinalValidationTypedEvents(t *testing.T) {
	l, _, sink := setup(t, final("SECRET invalid"), final("SECRET valid"))
	l.ValidateFinal = func(text string) (string, error) {
		if text == "SECRET valid" {
			return "", nil
		}
		return "SECRET correction", nil
	}
	if _, err := l.Run(context.Background(), "SECRET prompt"); err != nil {
		t.Fatal(err)
	}
	var got []EventKind
	for _, e := range sink.Events() {
		switch e.Kind {
		case FinalValidationRequested, FinalValidationAccepted, FinalValidationRejected, RecoveryRequested, RecoveryModelRequested:
			got = append(got, e.Kind)
		}
	}
	want := []EventKind{FinalValidationRequested, FinalValidationRejected, RecoveryRequested, RecoveryModelRequested, FinalValidationRequested, FinalValidationAccepted}
	if len(got) != len(want) {
		t.Fatal("wrong diagnostic event count")
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal("wrong diagnostic order")
		}
	}
}
