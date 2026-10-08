package server

import (
	"errors"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"net/http"
)

type approvalManager interface {
	PendingApproval(sessions.ID) (*sessions.ApprovalPresentation, error)
	ResolveApproval(sessions.ID, sessions.ApprovalID, sessions.ApprovalDecision) error
	ActiveSession(threads.ID) (*sessions.Snapshot, error)
}

func (s *Server) pendingApproval(w http.ResponseWriter, r *http.Request, id sessions.ID) {
	m, ok := s.deps.Sessions.(approvalManager)
	if !ok {
		failure(w, 503, "runtime_unavailable")
		return
	}
	p, err := m.PendingApproval(id)
	if err != nil {
		sessionFailure(w, err)
		return
	}
	respond(w, 200, map[string]any{"approval": p})
}
func (s *Server) approvalDecision(w http.ResponseWriter, r *http.Request, id sessions.ID, approval sessions.ApprovalID) {
	// Browser decisions must carry their Origin. Header-free local CLI clients
	// remain supported; Fetch Metadata identifies browser requests missing Origin.
	if r.Header.Get("Sec-Fetch-Site") != "" && len(r.Header.Values("Origin")) != 1 {
		failure(w, 403, "forbidden_origin")
		return
	}
	m, ok := s.deps.Sessions.(approvalManager)
	if !ok {
		failure(w, 503, "runtime_unavailable")
		return
	}
	var req struct {
		Decision sessions.ApprovalDecision `json:"decision"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := m.ResolveApproval(id, approval, req.Decision); err != nil {
		switch {
		case errors.Is(err, sessions.ErrApprovalDecision):
			failure(w, 400, "invalid_approval_decision")
		case errors.Is(err, sessions.ErrApprovalNotFound):
			failure(w, 404, "approval_not_found")
		case errors.Is(err, sessions.ErrApprovalResolved):
			failure(w, 409, "approval_already_resolved")
		case errors.Is(err, sessions.ErrApprovalMismatch):
			failure(w, 409, "approval_session_mismatch")
		case errors.Is(err, sessions.ErrApprovalNotPending):
			failure(w, 409, "approval_not_pending")
		default:
			sessionFailure(w, err)
		}
		return
	}
	respond(w, 200, map[string]string{"status": "decision_accepted"})
}
func (s *Server) activeSession(w http.ResponseWriter, r *http.Request, id threads.ID) {
	if _, err := s.deps.Threads.Get(id); err != nil {
		domainFailure(w, err, "thread")
		return
	}
	m, ok := s.deps.Sessions.(approvalManager)
	if !ok {
		failure(w, 503, "runtime_unavailable")
		return
	}
	snapshot, err := m.ActiveSession(id)
	if err != nil {
		sessionFailure(w, err)
		return
	}
	var view *sessionView
	if snapshot != nil {
		projected := viewSession(*snapshot)
		view = &projected
	}
	respond(w, 200, map[string]any{"session": view})
}
