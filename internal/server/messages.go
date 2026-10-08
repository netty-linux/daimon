package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/environments"
	"github.com/netty-linux/daimon/internal/memory"
	"github.com/netty-linux/daimon/internal/threads"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var errThreadHasHistory = errors.New("server: thread has history")

type idleThreadGuard interface {
	WithIdleThread(context.Context, threads.ID, func() error) error
}

func (s *Server) deleteThread(ctx context.Context, id threads.ID) error {
	s.referenceMu.Lock()
	defer s.referenceMu.Unlock()
	if err := s.requireNoScopedMemory(ctx, memory.Thread, string(id)); err != nil {
		return err
	}
	if s.deps.Conversations == nil && s.deps.Environments == nil {
		return s.deps.Threads.Delete(id)
	}
	guard, ok := s.deps.Sessions.(idleThreadGuard)
	if !ok {
		return ErrConfig
	}
	return guard.WithIdleThread(ctx, id, func() error {
		if s.deps.Environments != nil {
			if _, err := s.deps.Environments.Get(ctx, string(id)); err == nil {
				return errThreadEnvironment
			} else if !errors.Is(err, environments.ErrNotFound) {
				return err
			}
		}
		if s.deps.Conversations == nil {
			return s.deps.Threads.Delete(id)
		}
		items, err := s.deps.Conversations.List(ctx, id)
		if err != nil {
			return err
		}
		if len(items) != 0 {
			return errThreadHasHistory
		}
		return s.deps.Threads.Delete(id)
	})
}
func (s *Server) messages(w http.ResponseWriter, r *http.Request, id threads.ID) {
	if _, err := s.deps.Threads.Get(id); err != nil {
		domainFailure(w, err, "thread")
		return
	}
	if s.deps.Conversations == nil {
		failure(w, 503, "runtime_unavailable")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) > 2 {
		failure(w, 400, "invalid_request")
		return
	}
	for key, values := range q {
		if (key != "after" && key != "limit") || len(values) != 1 || values[0] == "" || strings.Trim(values[0], "0123456789") != "" {
			failure(w, 400, "invalid_request")
			return
		}
	}
	after := uint64(0)
	limit := uint64(40)
	if v := q.Get("after"); v != "" {
		after, err = strconv.ParseUint(v, 10, 64)
		if err != nil {
			failure(w, 400, "invalid_request")
			return
		}
	}
	if v := q.Get("limit"); v != "" {
		limit, err = strconv.ParseUint(v, 10, 64)
		if err != nil || limit < 1 || limit > 100 {
			failure(w, 400, "invalid_request")
			return
		}
	}
	items, err := s.deps.Conversations.List(r.Context(), id)
	if err != nil {
		if errors.Is(err, conversations.ErrVersion) {
			failure(w, 422, "unsupported_version")
		} else {
			failure(w, 500, "conversation_unavailable")
		}
		return
	}
	page := []conversations.Message{}
	next := after
	size := 256
	more := false
	for _, m := range items {
		if m.Sequence <= after {
			continue
		}
		data, encodeErr := json.Marshal(m)
		if encodeErr != nil {
			failure(w, 500, "internal_error")
			return
		}
		if uint64(len(page)) == limit || len(data)+1 > MaxResponseBytes-size {
			more = true
			break
		}
		page = append(page, m)
		size += len(data) + 1
		next = m.Sequence
	}
	respond(w, 200, map[string]any{"messages": page, "next_after": next, "has_more": more})
}
