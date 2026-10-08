package server

import (
	"errors"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	defer func() {
		if recover() != nil {
			failure(w, 500, "internal_error")
		}
	}()
	if !browserOriginAllowed(r) {
		failure(w, 403, "forbidden_origin")
		return
	}
	if s.static(w, r) {
		return
	}
	p := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(p) < 3 || p[0] != "api" || p[1] != "v1" || len(p) > 7 {
		failure(w, 404, "not_found")
		return
	}
	resource := p[2]
	id := ""
	action := ""
	if len(p) > 3 {
		id = p[3]
		if providers.ValidateID(providers.ID(id)) != nil {
			failure(w, 400, "invalid_request")
			return
		}
	}
	if len(p) > 4 {
		action = p[4]
	}
	if resource == "computers" && len(p) >= 5 {
		s.computerView(w, r, p)
		return
	}
	if len(p) > 6 {
		failure(w, 404, "not_found")
		return
	}
	stream := len(p) == 6 && p[2] == "sessions" && p[4] == "events" && p[5] == "stream"
	approvalDecision := len(p) == 6 && resource == "sessions" && action == "approvals"
	if len(p) == 6 && !stream && !approvalDecision {
		failure(w, 404, "not_found")
		return
	}
	allow := ""
	switch resource {
	case "sandboxes":
		if len(p) == 3 || len(p) == 4 {
			allow = "GET"
		}
	case "computers":
		if len(p) == 3 || len(p) == 4 {
			allow = "GET"
		}
	case "memories":
		if len(p) == 3 {
			allow = "GET, POST"
		} else if len(p) == 4 {
			allow = "GET, PUT, DELETE"
		}
	case "mcp":
		if len(p) == 4 && (id == "servers" || id == "tools") {
			allow = "GET"
		}
	case "health", "providers":
		if len(p) == 3 {
			allow = "GET"
		}
	case "bots", "threads":
		if len(p) == 3 {
			allow = "GET, POST"
		} else if len(p) == 4 {
			allow = "GET, PUT, DELETE"
		} else if resource == "threads" && len(p) == 5 && action == "messages" {
			allow = "GET"
		} else if resource == "threads" && len(p) == 5 && action == "session" {
			allow = "GET"
		} else if resource == "threads" && len(p) == 5 && action == "environment" {
			allow = "GET, POST, DELETE"
		}
	case "sessions":
		if len(p) == 3 {
			allow = "POST"
		} else if len(p) == 4 {
			allow = "GET"
		} else if action == "abort" {
			allow = "POST"
		} else if action == "events" {
			allow = "GET"
		} else if len(p) == 5 && action == "approval" {
			allow = "GET"
		}
	}
	if stream {
		allow = "GET"
	}
	if approvalDecision {
		allow = "POST"
	}
	if allow == "" {
		failure(w, 404, "not_found")
		return
	}
	allowed := false
	for _, method := range strings.Split(allow, ", ") {
		if r.Method == method {
			allowed = true
		}
	}
	if !allowed {
		w.Header().Set("Allow", allow)
		failure(w, 405, "method_not_allowed")
		return
	}
	if !(resource == "memories" && id == "" && r.Method == "GET") && action != "events" && action != "messages" && r.URL.RawQuery != "" {
		failure(w, 400, "invalid_request")
		return
	}
	body := r.Method == "PUT" || (r.Method == "POST" && action != "abort")
	if !body {
		r.Body = http.MaxBytesReader(w, r.Body, 1)
		data, err := io.ReadAll(r.Body)
		if err != nil || len(data) != 0 {
			failure(w, 400, "invalid_request")
			return
		}
	}
	if resource == "sandboxes" {
		s.sandboxes(w, r, id)
		return
	}
	if resource == "computers" {
		s.computers(w, r, id)
		return
	}
	if resource == "memories" {
		s.memories(w, r, id)
		return
	}
	if resource == "mcp" {
		if id == "servers" {
			s.mcpServers(w)
		} else {
			s.mcpTools(w)
		}
		return
	}
	if stream {
		s.stream(w, r, sessions.ID(id))
		return
	}
	if approvalDecision {
		if providers.ValidateID(providers.ID(p[5])) != nil {
			failure(w, 400, "invalid_request")
			return
		}
		s.approvalDecision(w, r, sessions.ID(id), sessions.ApprovalID(p[5]))
		return
	}
	if resource == "sessions" && action == "approval" {
		s.pendingApproval(w, r, sessions.ID(id))
		return
	}
	if resource == "threads" && action == "session" {
		s.activeSession(w, r, threads.ID(id))
		return
	}
	switch resource {
	case "health":
		respond(w, 200, map[string]string{"status": "ok"})
	case "providers":
		items := []map[string]string{}
		for _, provider := range s.providerIDs {
			items = append(items, map[string]string{"id": string(provider)})
		}
		respond(w, 200, map[string]any{"providers": items})
	case "bots":
		s.bots(w, r, bots.ID(id))
	case "threads":
		if action == "environment" {
			s.environment(w, r, threads.ID(id))
			return
		}
		if action == "messages" {
			s.messages(w, r, threads.ID(id))
			return
		}
		s.threads(w, r, threads.ID(id))
	case "sessions":
		s.session(w, r, sessions.ID(id), action)
	}
}

func (s *Server) bots(w http.ResponseWriter, r *http.Request, id bots.ID) {
	switch r.Method {
	case "GET":
		if id != "" {
			b, err := s.deps.Bots.Get(id)
			if err != nil {
				domainFailure(w, err, "bot")
				return
			}
			respond(w, 200, viewBot(b))
			return
		}
		list, err := s.deps.Bots.List()
		if err != nil {
			domainFailure(w, err, "bot")
			return
		}
		items := []botView{}
		for _, b := range list {
			items = append(items, viewBot(b))
		}
		respond(w, 200, map[string]any{"bots": items})
	case "POST", "PUT":
		var b bots.Bot
		if !decode(w, r, &b) {
			return
		}
		if r.Method == "PUT" && b.ID != id {
			failure(w, 400, "id_mismatch")
			return
		}
		var err error
		status := 201
		if r.Method == "POST" {
			err = s.deps.Bots.Create(b)
		} else {
			err = s.deps.Bots.Update(b)
			status = 200
		}
		if err != nil {
			domainFailure(w, err, "bot")
			return
		}
		respond(w, status, viewBot(b))
	case "DELETE":
		if err := s.deleteBot(r.Context(), id); err != nil {
			if errors.Is(err, errScopedMemory) {
				failure(w, 409, "bot_has_memories")
				return
			}
			domainFailure(w, err, "bot")
			return
		}
		w.WriteHeader(204)
	}
}
func (s *Server) threads(w http.ResponseWriter, r *http.Request, id threads.ID) {
	switch r.Method {
	case "GET":
		if id != "" {
			t, err := s.deps.Threads.Get(id)
			if err != nil {
				domainFailure(w, err, "thread")
				return
			}
			respond(w, 200, viewThread(t))
			return
		}
		list, err := s.deps.Threads.List()
		if err != nil {
			domainFailure(w, err, "thread")
			return
		}
		items := []threadView{}
		for _, t := range list {
			items = append(items, viewThread(t))
		}
		respond(w, 200, map[string]any{"threads": items})
	case "POST", "PUT":
		var t threads.Thread
		if !decode(w, r, &t) {
			return
		}
		if r.Method == "PUT" && t.ID != id {
			failure(w, 400, "id_mismatch")
			return
		}
		var err error
		status := 201
		if r.Method == "POST" {
			err = s.deps.Threads.Create(t)
		} else {
			err = s.deps.Threads.Update(t)
			status = 200
		}
		if err != nil {
			domainFailure(w, err, "thread")
			return
		}
		respond(w, status, viewThread(t))
	case "DELETE":
		if err := s.deleteThread(r.Context(), id); err != nil {
			if errors.Is(err, errThreadEnvironment) {
				failure(w, 409, "thread_has_environment")
				return
			}
			if errors.Is(err, errScopedMemory) {
				failure(w, 409, "thread_has_memories")
				return
			}
			if errors.Is(err, errThreadHasHistory) {
				failure(w, 409, "thread_has_history")
				return
			}
			var sessionErr *sessions.Error
			if errors.As(err, &sessionErr) {
				sessionFailure(w, err)
				return
			}
			domainFailure(w, err, "thread")
			return
		}
		w.WriteHeader(204)
	}
}

type startRequest struct {
	ID        sessions.ID `json:"id"`
	ThreadID  threads.ID  `json:"thread_id"`
	Message   string      `json:"message"`
	MessageID string      `json:"message_id"`
}

func (s *Server) session(w http.ResponseWriter, r *http.Request, id sessions.ID, action string) {
	if id == "" {
		var req startRequest
		if !decode(w, r, &req) {
			return
		}
		if len(req.Message) > MaxMessageBytes {
			failure(w, 400, "invalid_session")
			return
		}
		if r.Context().Err() != nil {
			failure(w, 503, "runtime_unavailable")
			return
		}
		snapshot, err := s.deps.Sessions.Start(s.deps.SessionContext, sessions.StartRequest{SessionID: req.ID, ThreadID: req.ThreadID, Message: req.Message, MessageID: conversations.ID(req.MessageID), EnableReplaceFile: s.deps.EnableReplaceFile, EnableCreateFile: s.deps.EnableCreateFile})
		if err != nil {
			sessionFailure(w, err)
			return
		}
		respond(w, 202, viewSession(snapshot))
		return
	}
	if action == "abort" {
		if err := s.deps.Sessions.Abort(id); err != nil {
			sessionFailure(w, err)
			return
		}
		respond(w, 202, map[string]string{"status": "abort_requested"})
		return
	}
	if action == "events" {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) > 1 || len(query["after"]) > 1 || (len(query) == 1 && query["after"] == nil) {
			failure(w, 400, "invalid_request")
			return
		}
		after := uint64(0)
		if values := query["after"]; len(values) > 0 {
			v := values[0]
			if v == "" || strings.Trim(v, "0123456789") != "" {
				failure(w, 400, "invalid_request")
				return
			}
			after, err = strconv.ParseUint(v, 10, 64)
			if err != nil {
				failure(w, 400, "invalid_request")
				return
			}
		}
		replay, err := s.deps.Sessions.EventsSince(id, after)
		if err != nil {
			sessionFailure(w, err)
			return
		}
		items := []eventView{}
		more := len(replay.Events) > MaxEvents
		events := replay.Events
		if more {
			events = events[:MaxEvents]
		}
		next := after
		for _, event := range events {
			e := event.Event
			items = append(items, eventView{event.Sequence, string(e.Kind), e.Step, e.ToolIndex, string(e.StopReason)})
			next = event.Sequence
		}
		respond(w, 200, map[string]any{"events": items, "first_available": replay.FirstAvailable, "last_sequence": replay.LastSequence, "gap": replay.Gap, "has_more": more, "next_after": next})
		return
	}
	snapshot, err := s.deps.Sessions.Get(id)
	if err != nil {
		sessionFailure(w, err)
		return
	}
	respond(w, 200, viewSession(snapshot))
}
