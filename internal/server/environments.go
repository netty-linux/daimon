package server

import (
	"errors"
	"github.com/netty-linux/daimon/internal/environments"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"net/http"
)

var errThreadEnvironment = errors.New("thread_has_environment")

func environmentFailure(w http.ResponseWriter, e error) {
	status, code := 500, "environment_unavailable"
	switch {
	case errors.Is(e, environments.ErrNotFound):
		status, code = 404, "environment_not_found"
	case errors.Is(e, environments.ErrExists):
		status, code = 409, "environment_exists"
	case errors.Is(e, environments.ErrBusy):
		status, code = 409, "environment_busy"
	case errors.Is(e, environments.ErrInvalid):
		status, code = 400, "invalid_environment"
	case errors.Is(e, environments.ErrLimit):
		status, code = 413, "store_limit"
	case errors.Is(e, environments.ErrVersion):
		status, code = 422, "unsupported_version"
	}
	var se *sessions.Error
	if errors.As(e, &se) {
		sessionFailure(w, e)
		return
	}
	failure(w, status, code)
}
func (s *Server) environment(w http.ResponseWriter, r *http.Request, id threads.ID) {
	if s.deps.Environments == nil {
		failure(w, 503, "environment_unavailable")
		return
	}
	if r.Method == "GET" {
		if _, e := s.deps.Threads.Get(id); e != nil {
			domainFailure(w, e, "thread")
			return
		}
		m, e := s.deps.Environments.Get(r.Context(), string(id))
		if e != nil {
			environmentFailure(w, e)
			return
		}
		respond(w, 200, m)
		return
	}
	if r.Method == "POST" {
		var empty struct{}
		if !decode(w, r, &empty) {
			return
		}
	}
	s.referenceMu.Lock()
	defer s.referenceMu.Unlock()
	guard, ok := s.deps.Sessions.(idleThreadGuard)
	if !ok {
		failure(w, 503, "environment_unavailable")
		return
	}
	var meta environments.Metadata
	err := guard.WithIdleThread(r.Context(), id, func() error {
		if _, e := s.deps.Threads.Get(id); e != nil {
			return e
		}
		var e error
		if r.Method == "POST" {
			meta, e = s.deps.Environments.Create(r.Context(), string(id))
		} else {
			e = s.deps.Environments.Delete(r.Context(), string(id))
		}
		return e
	})
	if err != nil {
		if errors.Is(err, threads.ErrNotFound) {
			domainFailure(w, err, "thread")
		} else {
			environmentFailure(w, err)
		}
		return
	}
	if r.Method == "DELETE" {
		w.WriteHeader(204)
	} else {
		respond(w, 201, meta)
	}
}
