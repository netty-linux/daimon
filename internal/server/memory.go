package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/memory"
	"github.com/netty-linux/daimon/internal/threads"
	"net/http"
	"net/url"
	"strconv"
)

var errScopedMemory = errors.New("memory: scoped records prevent deletion")

func memoryFailure(w http.ResponseWriter, err error) {
	status, code := 500, "memory_unavailable"
	switch {
	case errors.Is(err, memory.ErrInvalid):
		status, code = 400, "invalid_memory"
	case errors.Is(err, memory.ErrNotFound):
		status, code = 404, "memory_not_found"
	case errors.Is(err, memory.ErrDuplicate):
		status, code = 409, "memory_exists"
	case errors.Is(err, memory.ErrImmutable):
		status, code = 409, "memory_immutable"
	case errors.Is(err, memory.ErrLimit):
		status, code = 413, "store_limit"
	case errors.Is(err, memory.ErrVersion):
		status, code = 422, "unsupported_version"
	}
	failure(w, status, code)
}
func (s *Server) requireNoScopedMemory(ctx context.Context, scope memory.Scope, id string) error {
	if s.deps.Memory == nil {
		return nil
	}
	items, err := s.deps.Memory.List(ctx)
	if err != nil {
		return err
	}
	for _, m := range items {
		if m.Scope == scope && m.ScopeID == id {
			return errScopedMemory
		}
	}
	return nil
}
func (s *Server) deleteBot(ctx context.Context, id bots.ID) error {
	s.referenceMu.Lock()
	defer s.referenceMu.Unlock()
	if err := s.requireNoScopedMemory(ctx, memory.Bot, string(id)); err != nil {
		return err
	}
	return s.deps.Bots.Delete(id)
}
func (s *Server) memoryReference(ctx context.Context, input memory.Input) error {
	switch input.Scope {
	case memory.Bot:
		_, err := s.deps.Bots.Get(bots.ID(input.ScopeID))
		return err
	case memory.Thread:
		_, err := s.deps.Threads.Get(threads.ID(input.ScopeID))
		return err
	}
	return ctx.Err()
}
func (s *Server) memories(w http.ResponseWriter, r *http.Request, id string) {
	if s.deps.Memory == nil {
		failure(w, 503, "runtime_unavailable")
		return
	}
	switch r.Method {
	case "GET":
		if id != "" {
			record, err := s.deps.Memory.Get(r.Context(), memory.ID(id))
			if err != nil {
				memoryFailure(w, err)
				return
			}
			respond(w, 200, record)
			return
		}
		s.memoryList(w, r)
	case "POST", "PUT":
		var input memory.Input
		if !decode(w, r, &input) {
			return
		}
		if r.Method == "PUT" && string(input.ID) != id {
			failure(w, 400, "id_mismatch")
			return
		}
		s.referenceMu.Lock()
		defer s.referenceMu.Unlock()
		if err := s.memoryReference(r.Context(), input); err != nil {
			if input.Scope == memory.Bot {
				domainFailure(w, err, "bot")
			} else if input.Scope == memory.Thread {
				domainFailure(w, err, "thread")
			} else {
				memoryFailure(w, err)
			}
			return
		}
		var record memory.Memory
		var err error
		status := 201
		if r.Method == "POST" {
			record, err = s.deps.Memory.Create(r.Context(), input)
		} else {
			record, err = s.deps.Memory.Update(r.Context(), input)
			status = 200
		}
		if err != nil {
			memoryFailure(w, err)
			return
		}
		respond(w, status, record)
	case "DELETE":
		s.referenceMu.Lock()
		defer s.referenceMu.Unlock()
		if err := s.deps.Memory.Delete(r.Context(), memory.ID(id)); err != nil {
			memoryFailure(w, err)
			return
		}
		w.WriteHeader(204)
	}
}
func (s *Server) memoryList(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		failure(w, 400, "invalid_request")
		return
	}
	for key, values := range q {
		if (key != "scope" && key != "scope_id" && key != "after" && key != "limit") || len(values) != 1 || values[0] == "" {
			failure(w, 400, "invalid_request")
			return
		}
	}
	scope := memory.Scope(q.Get("scope"))
	scopeID := q.Get("scope_id")
	if (scope == "" && scopeID != "") || (scope != "" && memory.ValidateScope(scope, scopeID) != nil) {
		failure(w, 400, "invalid_request")
		return
	}
	after := q.Get("after")
	if after != "" && memory.ValidateID(after) != nil {
		failure(w, 400, "invalid_request")
		return
	}
	limit := 40
	if value := q.Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			failure(w, 400, "invalid_request")
			return
		}
	}
	items, err := s.deps.Memory.List(r.Context())
	if err != nil {
		memoryFailure(w, err)
		return
	}
	page := []memory.Memory{}
	size := 256
	next := after
	more := false
	for _, record := range items {
		if string(record.ID) <= after || (scope != "" && (record.Scope != scope || record.ScopeID != scopeID)) {
			continue
		}
		raw, err := json.Marshal(record)
		if err != nil {
			failure(w, 500, "internal_error")
			return
		}
		if len(page) == limit || len(raw)+1 > MaxResponseBytes-size {
			more = true
			break
		}
		page = append(page, record)
		size += len(raw) + 1
		next = string(record.ID)
	}
	respond(w, 200, map[string]any{"memories": page, "next_after": next, "has_more": more})
}
