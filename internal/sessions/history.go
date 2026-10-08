package sessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/threads"
)

func userMessageID(req StartRequest) conversations.ID {
	if req.MessageID != "" {
		return req.MessageID
	}
	sum := sha256.Sum256([]byte(req.SessionID))
	return conversations.ID("message-" + hex.EncodeToString(sum[:16]))
}
func prepareConversation(ctx context.Context, store ConversationStore, req StartRequest, b agentloop.Budget) (history []model.Message, err error) {
	defer func() {
		if recover() != nil {
			err = &Error{Kind: Panic}
			history = nil
		}
	}()
	messages, err := store.List(ctx, req.ThreadID)
	if err != nil {
		return nil, &Error{Kind: HistoryResolution, Cause: err}
	}
	// Reserve the new user and one full final response, leaving loop limits to
	// account for any intra-session tool calls/receipts as before.
	remainingBytes := b.MaxHistoryBytes - len(req.Message)
	if remainingBytes < 0 || b.MaxHistoryMessages < 2 || b.MaxFinalAnswerBytes > remainingBytes {
		return nil, &Error{Kind: HistoryResolution, Cause: agentloop.ErrInvalidConfig}
	}
	remainingBytes -= b.MaxFinalAnswerBytes
	remainingCount := b.MaxHistoryMessages - 2
	start := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if conversations.Validate(m) != nil || m.ThreadID != req.ThreadID || m.Sequence != uint64(i)+1 {
			return nil, &Error{Kind: HistoryResolution}
		}
		limit := b.MaxUserMessageBytes
		if m.Role == conversations.Assistant {
			limit = b.MaxFinalAnswerBytes
		}
		if len(m.Content) > limit || remainingCount == 0 || len(m.Content) > remainingBytes {
			break
		}
		start = i
		remainingBytes -= len(m.Content)
		remainingCount--
	}
	for _, m := range messages[start:] {
		history = append(history, model.Message{Role: model.Role(m.Role), Content: m.Content})
	}
	err = store.Append(ctx, conversations.Message{ID: userMessageID(req), ThreadID: req.ThreadID, Role: conversations.User, Content: req.Message, CreatedAt: nowUTC(), SessionID: string(req.SessionID)})
	if err != nil {
		kind := Persistence
		if errors.Is(err, conversations.ErrDuplicate) {
			kind = DuplicateMessage
		}
		return nil, &Error{Kind: kind, Cause: err}
	}
	return history, nil
}

func (m *Manager) prepareStart(ctx context.Context, req StartRequest, memoryBytes int) (history []model.Message, err error) {
	defer func() {
		if recover() != nil {
			err = &Error{Kind: Panic}
			history = nil
		}
	}()
	thread, readErr := m.deps.Threads.Get(req.ThreadID)
	if readErr != nil || thread.ID != req.ThreadID {
		return nil, &Error{Kind: ThreadResolution, Cause: readErr}
	}
	budget := m.options.Budget
	budget.MaxHistoryBytes -= memoryBytes
	return prepareConversation(ctx, m.deps.Conversations, req, budget)
}
func persistAssistant(ctx context.Context, store ConversationStore, req StartRequest, text string) (err error) {
	defer func() {
		if recover() != nil {
			err = &Error{Kind: Panic}
		}
	}()
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return &Error{Kind: Persistence}
	}
	message := conversations.Message{ID: conversations.ID("assistant-" + hex.EncodeToString(nonce)), ThreadID: req.ThreadID, Role: conversations.Assistant, Content: text, CreatedAt: nowUTC(), SessionID: string(req.SessionID)}
	if err = store.Append(ctx, message); err != nil {
		return &Error{Kind: Persistence, Cause: err}
	}
	return nil
}

// WithIdleThread reserves admission while a caller checks/deletes empty metadata.
// The callback runs without Manager locks; Start sees ThreadBusy until release.
func (m *Manager) WithIdleThread(ctx context.Context, id threads.ID, action func() error) error {
	if m == nil || ctx == nil || action == nil || providers.ValidateID(providers.ID(id)) != nil {
		return &Error{Kind: Invalid}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return &Error{Kind: Closed}
	}
	if _, busy := m.active[string(id)]; busy {
		m.mu.Unlock()
		return &Error{Kind: ThreadBusy}
	}
	m.active[string(id)] = ""
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.active, string(id)); m.mu.Unlock() }()
	return action()
}
