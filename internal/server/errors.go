package server

import (
	"errors"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"net/http"
)

func domainFailure(w http.ResponseWriter, err error, domain string) {
	status, code := 500, "internal_error"
	var invalid, missing, duplicate, limit, version error
	if domain == "bot" {
		invalid, missing, duplicate, limit, version = bots.ErrInvalid, bots.ErrNotFound, bots.ErrDuplicate, bots.ErrLimit, bots.ErrVersion
	} else {
		invalid, missing, duplicate, limit, version = threads.ErrInvalid, threads.ErrNotFound, threads.ErrDuplicate, threads.ErrLimit, threads.ErrVersion
	}
	switch {
	case errors.Is(err, invalid):
		status, code = 400, "invalid_"+domain
	case errors.Is(err, missing):
		status, code = 404, domain+"_not_found"
	case errors.Is(err, duplicate):
		status, code = 409, domain+"_exists"
	case errors.Is(err, limit):
		status, code = 413, "store_limit"
	case errors.Is(err, version):
		status, code = 422, "unsupported_version"
	case domain == "thread" && errors.Is(err, threads.ErrImmutable):
		status, code = 409, "thread_immutable"
	}
	failure(w, status, code)
}
func sessionFailure(w http.ResponseWriter, err error) {
	var e *sessions.Error
	status, code := 500, "internal_error"
	if errors.As(err, &e) {
		switch e.Kind {
		case sessions.Invalid:
			status, code = 400, "invalid_session"
		case sessions.NotFound:
			status, code = 404, "session_not_found"
		case sessions.Duplicate:
			status, code = 409, "session_exists"
		case sessions.ThreadBusy:
			status, code = 409, "thread_busy"
		case sessions.DuplicateMessage:
			status, code = 409, "message_exists"
		case sessions.MemoryResolution:
			status, code = 500, "memory_unavailable"
		case sessions.Persistence, sessions.HistoryResolution:
			status, code = 500, "conversation_unavailable"
		case sessions.ThreadResolution:
			status, code = 404, "thread_not_found"
		case sessions.NotRunning:
			status, code = 409, "session_finished"
		case sessions.Capacity:
			status, code = 429, "session_capacity"
		case sessions.Closed, sessions.Canceled:
			status, code = 503, "runtime_unavailable"
		}
	}
	failure(w, status, code)
}
