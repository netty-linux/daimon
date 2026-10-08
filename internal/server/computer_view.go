package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/computer/socket"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

func computerViewFailure(w http.ResponseWriter, e error) {
	code, status := "computer_media_unavailable", 503
	switch {
	case errors.Is(e, computer.ErrArguments):
		code, status = "invalid_request", 400
	case errors.Is(e, computer.ErrControl):
		code, status = "computer_control_invalid", 409
	case errors.Is(e, computer.ErrBusy):
		code, status = "computer_control_busy", 409
	case errors.Is(e, computer.ErrCapacity):
		code, status = "computer_view_capacity", 429
	case errors.Is(e, computer.ErrUnavailable):
		code, status = "computer_not_found", 404
	}
	failure(w, status, code)
}
func (s *Server) computerView(w http.ResponseWriter, r *http.Request, p []string) {
	method := ""
	route := ""
	id := p[3]
	if len(p) == 5 {
		switch p[4] {
		case "view":
			method, route = "GET", "state"
		case "views":
			method, route = "POST", "open"
		}
	}
	if len(p) == 6 && p[4] == "control" {
		switch p[5] {
		case "take", "release":
			method, route = "POST", p[5]
		}
	}
	if len(p) == 7 && p[4] == "views" {
		switch p[6] {
		case "media", "input":
			method, route = "GET", p[6]
		}
	}
	if route == "" {
		failure(w, 404, "not_found")
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		failure(w, 405, "method_not_allowed")
		return
	}
	if r.URL.RawQuery != "" {
		failure(w, 400, "invalid_request")
		return
	}
	// A same-origin fetch of read-only state does not carry Origin. Socket
	// upgrades and every mutation still require the explicit browser origin.
	stateFetch := route == "state" && r.Header.Get("Sec-Fetch-Site") == "same-origin"
	if !localAuthority(r.Host) || !browserOriginAllowed(r) || (!stateFetch && r.Header.Get("Sec-Fetch-Site") != "" && len(r.Header.Values("Origin")) != 1) {
		failure(w, 403, "forbidden_origin")
		return
	}
	select {
	case <-s.stopping:
		failure(w, 503, "runtime_unavailable")
		return
	default:
	}
	if s.deps.Computers == nil {
		failure(w, 503, "computer_media_unavailable")
		return
	}
	manager, e := s.deps.Computers.ForComputer(r.Context(), id)
	if e != nil {
		computerViewFailure(w, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	switch route {
	case "state":
		if !emptyComputerBody(w, r) {
			return
		}
		state, e := manager.ViewState(ctx, id)
		if e != nil {
			computerViewFailure(w, e)
			return
		}
		respond(w, 200, state)
	case "open":
		var req struct {
			Target string `json:"target"`
		}
		if !decode(w, r, &req) {
			return
		}
		_, ticket, e := manager.OpenView(ctx, id, req.Target)
		if e != nil {
			computerViewFailure(w, e)
			return
		}
		respond(w, 201, ticket)
	case "take", "release":
		var req struct {
			ViewID string `json:"view_id"`
			Token  string `json:"token"`
		}
		if !decode(w, r, &req) {
			return
		}
		if route == "take" {
			ticket, e := manager.TakeControl(ctx, id, req.ViewID, req.Token)
			if e != nil {
				computerViewFailure(w, e)
				return
			}
			respond(w, 200, ticket)
		} else {
			if e := manager.ReleaseControl(ctx, id, req.ViewID, req.Token); e != nil {
				computerViewFailure(w, e)
				return
			}
			respond(w, 200, map[string]string{"status": "released"})
		}
	case "media", "input":
		if !emptyComputerBody(w, r) {
			return
		}
		if len(r.Header.Values("Origin")) != 1 {
			failure(w, 403, "forbidden_origin")
			return
		}
		s.computerSocket(w, r, manager, id, p[5], route)
	}
}
func emptyComputerBody(w http.ResponseWriter, r *http.Request) bool {
	p, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	if e != nil || len(p) != 0 {
		failure(w, 400, "invalid_request")
		return false
	}
	return true
}
func socketCapability(r *http.Request, kind string) string {
	protocols := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")
	if len(protocols) != 2 {
		return ""
	}
	token := ""
	rcdp := false
	for _, p := range protocols {
		p = strings.TrimSpace(p)
		if p == "rcdp.v2" {
			rcdp = true
		} else if strings.HasPrefix(p, "daimon."+kind+".") {
			token = strings.TrimPrefix(p, "daimon."+kind+".")
		} else {
			return ""
		}
	}
	if !rcdp || len(token) != 32 {
		return ""
	}
	return token
}
func (s *Server) computerSocket(w http.ResponseWriter, r *http.Request, manager *computer.Manager, id, vid, kind string) {
	token := socketCapability(r, kind)
	if token == "" {
		failure(w, 403, "computer_control_invalid")
		return
	}
	// Validate the upgrade before consuming a one-attach capability.
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Version") != "13" {
		failure(w, 400, "invalid_request")
		return
	}
	s.mediaMu.Lock()
	select {
	case <-s.stopping:
		s.mediaMu.Unlock()
		failure(w, 503, "runtime_unavailable")
		return
	default:
	}
	s.mediaWG.Add(1)
	s.mediaMu.Unlock()
	defer s.mediaWG.Done()
	var view *computer.ViewSession
	var e error
	if kind == "media" {
		view, e = manager.AttachView(id, vid, token)
	} else {
		e = manager.AttachControl(id, vid, token)
	}
	if e != nil {
		computerViewFailure(w, e)
		return
	}
	conn, e := socket.Upgrade(w, r, "rcdp.v2")
	if e != nil {
		if view != nil {
			_ = view.Close(context.Background())
		} else {
			manager.DisconnectControl(id, vid, token)
		}
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		select {
		case <-s.stopping:
		case <-ctx.Done():
		}
		cancel()
		conn.Close()
	}()
	defer func() { cancel(); conn.Close(); workers.Wait() }()
	if view != nil {
		defer func() {
			closeCtx, end := context.WithTimeout(context.Background(), 10*time.Second)
			defer end()
			_ = view.Close(closeCtx)
		}()
	} else {
		defer manager.DisconnectControl(id, vid, token)
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if conn.Write(9, nil) != nil {
					cancel()
					conn.Close()
					return
				}
			}
		}
	}()
	if view == nil {
		for {
			conn.SetReadDeadline(time.Now().Add(15 * time.Second))
			op, p, e := conn.Read(computer.MaxMediaControl)
			if e != nil {
				return
			}
			if op == 10 {
				if manager.TouchControl(id, vid, token) != nil {
					return
				}
				continue
			}
			if op != 1 {
				return
			}
			result, e := manager.HumanInput(ctx, id, vid, token, p)
			if e != nil {
				_ = conn.Write(1, []byte(`{"type":"error","payload":{"code":"input_rejected"}}`))
				return
			}
			if conn.Write(1, result) != nil {
				return
			}
		}
	}
	info := view.Info()
	hello := []byte(`{"type":"hello","payload":{"protocol":"rcdp","selected_version":2,"capabilities":["desktop.v1"]}}`)
	if conn.Write(1, hello) != nil {
		return
	}
	opened, _ := json.Marshal(map[string]any{"type": "session_opened", "payload": map[string]any{"session_id": info.SessionID, "codec": info.Codec, "policy": "view_only", "target": map[string]string{"kind": "display", "display_id": "primary"}}})
	if conn.Write(1, opened) != nil {
		return
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		defer func() { cancel(); conn.Close() }()
		last := time.Time{}
		for {
			conn.SetReadDeadline(time.Now().Add(15 * time.Second))
			op, p, e := conn.Read(1024)
			if e != nil {
				return
			}
			if op == 10 {
				continue
			}
			if op != 1 || string(p) != `{"type":"request_keyframe"}` || time.Since(last) < time.Second {
				return
			}
			last = time.Now()
			if view.Keyframe(ctx) != nil {
				return
			}
		}
	}()
	var sequence, epoch uint64
	waiting := true
	for {
		packet, e := view.Receive(ctx)
		if e != nil {
			return
		}
		if !packet.Binary {
			continue
		}
		d, _, e := computer.ParseVideo(packet.Data, info.SessionID)
		if e != nil || d.Sequence <= sequence && sequence != 0 {
			return
		}
		if d.Codec != info.Codec {
			return
		}
		if d.CodecEpoch != epoch || sequence != 0 && d.Sequence != sequence+1 && d.Codec == "h264" {
			waiting = true
		}
		sequence = d.Sequence
		epoch = d.CodecEpoch
		if waiting && !d.Keyframe {
			if view.Keyframe(ctx) != nil {
				return
			}
			continue
		}
		waiting = false
		if conn.Write(2, packet.Data) != nil {
			return
		}
	}
}
