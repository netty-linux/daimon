package server

import (
	"github.com/netty-linux/daimon/internal/sandbox"
	"net/http"
)

func (s *Server) sandboxes(w http.ResponseWriter, r *http.Request, id string) {
	if id != "" {
		if sandbox.ValidateID(sandbox.ID(id)) != nil {
			failure(w, 400, "invalid_sandbox")
			return
		}
		if s.deps.Sandboxes == nil {
			failure(w, 404, "sandbox_not_found")
			return
		}
		info, e := s.deps.Sandboxes.Get(sandbox.ID(id))
		if e != nil {
			failure(w, 404, "sandbox_not_found")
			return
		}
		respond(w, 200, info)
		return
	}
	items := []sandbox.Info{}
	runtime := sandbox.RuntimeInfo{Backend: sandbox.CUALocal, Runtime: "gvisor", Reason: sandbox.NotConfigured}
	if s.deps.Sandboxes != nil {
		items = s.deps.Sandboxes.List()
		runtime = s.deps.Sandboxes.Probe(r.Context())
	}
	backends := []sandbox.RuntimeInfo{runtime, {Backend: sandbox.CUACloud, Runtime: "gvisor", Reason: sandbox.NotConfigured}}
	if s.deps.Sandboxes != nil {
		backends = s.deps.Sandboxes.Backends(r.Context())
	}
	respond(w, 200, struct {
		Sandboxes []sandbox.Info        `json:"sandboxes"`
		Runtime   sandbox.RuntimeInfo   `json:"runtime"`
		Backends  []sandbox.RuntimeInfo `json:"backends"`
	}{items, runtime, backends})
}
