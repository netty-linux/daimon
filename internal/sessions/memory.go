package sessions

import (
	"context"
	"github.com/netty-linux/daimon/internal/memory"
	"github.com/netty-linux/daimon/internal/threads"
)

func (m *Manager) prepareMemory(ctx context.Context, req StartRequest) (text string, err error) {
	if m.deps.Memory == nil {
		return "", nil
	}
	defer func() {
		if recover() != nil {
			text = ""
			err = &Error{Kind: Panic}
		}
	}()
	thread, e := m.deps.Threads.Get(req.ThreadID)
	if e != nil || threads.Validate(thread) != nil || thread.ID != req.ThreadID {
		return "", &Error{Kind: ThreadResolution, Cause: e}
	}
	items, e := m.deps.Memory.List(ctx)
	if e != nil {
		return "", &Error{Kind: MemoryResolution, Cause: e}
	}
	query := memory.Query{BotID: string(thread.BotID), ThreadID: string(req.ThreadID), Text: req.Message, Limit: m.options.MaxMemoryRecords}
	eligible, e := memory.Retrieve(items, query)
	if e != nil {
		return "", &Error{Kind: MemoryResolution, Cause: e}
	}
	if len(eligible) == 0 {
		return "", ctx.Err()
	}
	available := m.options.Budget.MaxHistoryBytes - len(req.Message)
	if available < 0 || m.options.Budget.MaxFinalAnswerBytes > available {
		return "", &Error{Kind: MemoryResolution}
	}
	available -= m.options.Budget.MaxFinalAnswerBytes
	text, e = memory.Context(eligible, query, min(m.options.MaxMemoryContextBytes, available))
	if e != nil {
		return "", &Error{Kind: MemoryResolution, Cause: e}
	}
	return text, ctx.Err()
}
