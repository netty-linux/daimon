package sessions

import (
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/threads"
	"testing"
)

type blockingScheduledThread struct {
	gate   chan struct{}
	reader *threadReader
}

func (r *blockingScheduledThread) Get(id threads.ID) (threads.Thread, error) {
	<-r.gate
	return r.reader.Get(id)
}
func TestNormalStartKeepsAsynchronousThreadResolution(t *testing.T) {
	deps, opts, _, reader := fixture(t, nil)
	gate := make(chan struct{})
	deps.Threads = &blockingScheduledThread{gate, reader}
	m := manager(t, deps, opts)
	ctx := testContext(t)
	defer close(gate)
	result := make(chan error, 1)
	go func() {
		_, e := m.Start(ctx, StartRequest{SessionID: "normal", ThreadID: "thread", Message: "task"})
		result <- e
	}()
	select {
	case e := <-result:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("normal Start blocked on Thread resolution")
	}
}

func TestScheduledAdmissionRespectsIdleMetadataReservation(t *testing.T) {
	deps, opts, _, _ := fixture(t, nil)
	m := manager(t, deps, opts)
	ctx := testContext(t)
	if e := m.WithIdleThread(ctx, "reserved", func() error {
		if m.ScheduledStatus(bots.ID("coder")) != Running {
			t.Fatal("reservation bypass")
		}
		if _, e := m.Start(ctx, StartRequest{SessionID: "routine", ThreadID: "other", ScheduledBotID: "coder", Message: "task"}); e == nil {
			t.Fatal("admitted through reserved metadata")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
