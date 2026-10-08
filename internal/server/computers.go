package server

import (
	"github.com/netty-linux/daimon/internal/computer"
	"net/http"
)

// Deliberate metadata projection. No handles, arguments, image, config or call API.
func (s *Server) computers(w http.ResponseWriter, r *http.Request, id string) {
	items := []computer.Info{}
	if s.deps.Computers != nil {
		var err error
		items, err = s.deps.Computers.Infos(r.Context())
		if err != nil {
			failure(w, 503, "computer_unavailable")
			return
		}
	}
	if id != "" {
		for _, info := range items {
			if info.ID == id {
				respond(w, 200, info)
				return
			}
		}
		failure(w, 404, "not_found")
		return
	}
	respond(w, 200, map[string]any{"computers": items})
}
