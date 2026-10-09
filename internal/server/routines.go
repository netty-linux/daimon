package server

import (
	"errors"
	"github.com/netty-linux/daimon/internal/routines"
	"net/http"
	"time"
)

var errRoutineReference = errors.New("server: resource has routines")

func (s *Server) routines(w http.ResponseWriter, r *http.Request, id string) {
	if s.deps.Routines == nil {
		failure(w, 503, "runtime_unavailable")
		return
	}
	if r.Method == "GET" {
		items, e := s.deps.Routines.List()
		if e != nil {
			failure(w, 500, "internal_error")
			return
		}
		respond(w, 200, map[string]any{"routines": items, "server_only": true})
		return
	}
	s.referenceMu.Lock()
	defer s.referenceMu.Unlock()
	var err error
	status := 204
	switch r.Method {
	case "POST":
		var in routines.Input
		if !decode(w, r, &in) {
			return
		}
		err = s.deps.Routines.Create(r.Context(), in, time.Now().UTC())
		status = 201
	case "PUT":
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Enabled == nil {
			failure(w, 400, "invalid_request")
			return
		}
		err = s.deps.Routines.Enable(r.Context(), id, *in.Enabled, time.Now().UTC())
	case "DELETE":
		err = s.deps.Routines.Delete(r.Context(), id)
	}
	if err != nil {
		failure(w, 409, "invalid_routine")
		return
	}
	if status == 204 {
		w.WriteHeader(204)
	} else {
		respond(w, status, map[string]string{"status": "created"})
	}
}
