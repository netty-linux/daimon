package sessions

import (
	"context"
	"errors"
	"testing"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
)

func TestEventObservationBroadcastAndTerminal(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	deps, options, _, _ := fixture(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
		close(entered)
		select {
		case <-release:
			return model.ModelResponse{FinalText: "done"}, nil
		case <-ctx.Done():
			return model.ModelResponse{}, ctx.Err()
		}
	})
	m := manager(t, deps, options)
	start(t, m, "session", "thread")
	<-entered
	first, err := m.ObserveEvents("session")
	if err != nil || first.Terminal {
		t.Fatal(err)
	}
	second, err := m.ObserveEvents("session")
	if err != nil || first.Changed != second.Changed {
		t.Fatal("observers must share the generation")
	}
	// Emit after observation but before replay/wait: wakeup must remain observable.
	sessionSink{manager: m, id: "session"}.Record(context.Background(), agentloop.Event{Kind: agentloop.ModelResponded})
	replay, err := m.EventsSince("session", 0)
	if err != nil || len(replay.Events) == 0 {
		t.Fatal(err)
	}
	for _, observation := range []EventObservation{first, second} {
		select {
		case <-observation.Changed:
		default:
			t.Fatal("lost notification")
		}
	}
	next, err := m.ObserveEvents("session")
	if err != nil || next.Changed == first.Changed {
		t.Fatal("generation not renewed")
	}
	close(release)
	wait(t, m, "session")
	select {
	case <-next.Changed:
	default:
		t.Fatal("terminal notification missing")
	}
	terminal, err := m.ObserveEvents("session")
	if err != nil || !terminal.Terminal || terminal.Status != Completed {
		t.Fatal(terminal, err)
	}
	select {
	case <-terminal.Changed:
	default:
		t.Fatal("finalized generation remains open")
	}
	// Observation never consumes or changes ring contents.
	a, _ := m.EventsSince("session", 0)
	b, _ := m.EventsSince("session", 0)
	if len(a.Events) != len(b.Events) || a.LastSequence != b.LastSequence {
		t.Fatal("replay mutated")
	}
}

func TestObservationStartupFailureAndUnknown(t *testing.T) {
	deps, options, _, _ := fixture(t, func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
		return model.ModelResponse{FinalText: "done"}, nil
	})
	m := manager(t, deps, options)
	if _, err := m.ObserveEvents("unknown"); err == nil {
		t.Fatal("unknown accepted")
	}
	m.deps.Threads = &threadReader{err: errors.New("private missing thread")}
	start(t, m, "session", "missing")
	waitCtx := testContext(t)
	snapshot, err := m.Wait(waitCtx, "session")
	if err == nil || snapshot.Status != Failed {
		t.Fatal("startup failure")
	}
	observed, err := m.ObserveEvents("session")
	if err != nil || !observed.Terminal {
		t.Fatal(err)
	}
	replay, err := m.EventsSince("session", 0)
	if err != nil || len(replay.Events) != 0 {
		t.Fatal("startup invented events")
	}
}
