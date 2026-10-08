package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/sessions"
)

const (
	MaxStreams         = 64
	MaxSSEPayloadBytes = 4096
	SSEHeartbeat       = 15 * time.Second
	SSEWriteTimeout    = 5 * time.Second
)

// Options remain internal: configured once before serving, never during requests.
// Short heartbeat/write intervals and payload limits can be injected by tests.
type sseOptions struct {
	heartbeat, writeTimeout time.Duration
	payloadBytes            int
}
type eventObserver interface {
	ObserveEvents(sessions.ID) (sessions.EventObservation, error)
}

func streamCursor(r *http.Request) (uint64, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) > 1 || len(q["after"]) > 1 || (len(q) == 1 && q["after"] == nil) {
		return 0, false
	}
	var value string
	if values, exists := r.Header[http.CanonicalHeaderKey("Last-Event-ID")]; exists {
		if len(values) != 1 {
			return 0, false
		}
		value = values[0]
	} else if values := q["after"]; len(values) == 1 {
		value = values[0]
	} else {
		return 0, true
	}
	if len(value) == 0 || len(value) > 20 || strings.Trim(value, "0123456789") != "" {
		return 0, false
	}
	cursor, err := strconv.ParseUint(value, 10, 64)
	return cursor, err == nil
}

// Whitelist semantic names; unknown strings cannot become event headers/payloads.
func eventName(kind agentloop.EventKind) (string, bool) {
	switch kind {
	case agentloop.EnvironmentHydrating, agentloop.EnvironmentReady, agentloop.EnvironmentSaving, agentloop.EnvironmentSaved, agentloop.EnvironmentFailed:
		return string(kind), true
	case agentloop.SandboxCreating, agentloop.SandboxReady, agentloop.SandboxCleanupStarted, agentloop.SandboxCleanupCompleted, agentloop.SandboxFailed:
		return string(kind), true
	case agentloop.LoopStarted, agentloop.ModelRequested, agentloop.ModelResponded,
		agentloop.ToolAllowed, agentloop.ToolDenied, agentloop.ToolRequested, agentloop.ToolCompleted, agentloop.ToolFailed,
		agentloop.ApprovalRequested, agentloop.ApprovalGranted, agentloop.ApprovalDenied,
		agentloop.FinalAnswer, agentloop.FinalValidationRequested, agentloop.FinalValidationAccepted, agentloop.FinalValidationRejected,
		agentloop.RecoveryRequested, agentloop.RecoveryModelRequested, agentloop.LoopStopped:
		return string(kind), true
	}
	return "", false
}
func safeStop(reason agentloop.StopReason) bool {
	switch reason {
	case "", agentloop.StopReasonCompleted, agentloop.StopReasonInvalidConfig, agentloop.StopReasonAuthorizationError, agentloop.StopReasonInvalidResponse, agentloop.StopReasonModelError,
		agentloop.StopReasonCanceled, agentloop.StopReasonExternalDeadline, agentloop.StopReasonRunTimeout, agentloop.StopReasonModelTimeout, agentloop.StopReasonToolTimeout,
		agentloop.StopReasonMaxSteps, agentloop.StopReasonMaxToolCalls, agentloop.StopReasonUserMessageLimit, agentloop.StopReasonArgumentLimit, agentloop.StopReasonFinalAnswerLimit, agentloop.StopReasonHistoryLimit:
		return true
	}
	return false
}

type streamWriter struct {
	w          http.ResponseWriter
	controller *http.ResponseController
	options    sseOptions
}

func (w streamWriter) write(frame string) bool {
	if w.controller.SetWriteDeadline(time.Now().Add(w.options.writeTimeout)) != nil {
		return false
	}
	n, err := w.w.Write([]byte(frame))
	if err != nil || n != len(frame) || w.controller.Flush() != nil {
		return false
	}
	// A ResponseController deadline cannot be extended once exceeded. Clear a
	// successful write's deadline BEFORE idle waiting (heartbeat interval is longer).
	return w.controller.SetWriteDeadline(time.Time{}) == nil
}
func (w streamWriter) event(name string, sequence *uint64, value any) bool {
	data, err := json.Marshal(value)
	if err != nil || len(data) > w.options.payloadBytes {
		return false
	}
	frame := "event: " + name + "\ndata: " + string(data) + "\n\n"
	if sequence != nil {
		frame = "id: " + strconv.FormatUint(*sequence, 10) + "\n" + frame
	}
	return w.write(frame)
}
func (w streamWriter) transportError() {
	_ = w.event("transport_error", nil, struct {
		Code string `json:"code"`
	}{"internal_error"})
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request, id sessions.ID) {
	after, ok := streamCursor(r)
	if !ok {
		failure(w, 400, "invalid_request")
		return
	}
	observer, ok := s.deps.Sessions.(eventObserver)
	if !ok {
		failure(w, 500, "internal_error")
		return
	}
	// Check existence before committing stream status/headers.
	observation, err := observer.ObserveEvents(id)
	if err != nil {
		sessionFailure(w, err)
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		failure(w, 500, "stream_unsupported")
		return
	}
	controller := http.NewResponseController(w)
	if controller.SetWriteDeadline(time.Time{}) != nil {
		failure(w, 500, "stream_unsupported")
		return
	}
	defer controller.SetWriteDeadline(time.Time{})
	select {
	case <-s.stopping:
		failure(w, 503, "runtime_unavailable")
		return
	default:
	}
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		failure(w, 429, "stream_capacity")
		return
	}
	writer := streamWriter{w, controller, s.sse}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Contain dependency panics here: never append a JSON HTTP error to SSE frames.
	defer func() {
		if recover() != nil {
			writer.transportError()
		}
	}()
	if !writer.write("retry: 3000\n\n") {
		return
	}
	heartbeat := time.NewTicker(s.sse.heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stopping:
			return
		default:
		}
		// Capture generation before replay. Final observation means replay is immutable.
		replay, err := s.deps.Sessions.EventsSince(id, after)
		if err != nil {
			writer.transportError()
			return
		}
		if replay.Gap {
			gap := struct {
				RequestedAfter uint64 `json:"requested_after"`
				Oldest         uint64 `json:"oldest_available"`
				Latest         uint64 `json:"latest_available"`
			}{after, replay.FirstAvailable, replay.LastSequence}
			if !writer.event("replay_gap", nil, gap) {
				return
			}
		}
		for _, event := range replay.Events {
			select {
			case <-r.Context().Done():
				return
			case <-s.stopping:
				return
			default:
			}
			e := event.Event
			name, valid := eventName(e.Kind)
			if !valid || !safeStop(e.StopReason) || event.Sequence <= after {
				writer.transportError()
				return
			}
			payload := eventView{event.Sequence, name, e.Step, e.ToolIndex, string(e.StopReason)}
			data, marshalErr := json.Marshal(payload)
			if marshalErr != nil || len(data) > s.sse.payloadBytes {
				writer.transportError()
				return
			}
			// Each frame is flushed; only a successful write advances this client's cursor.
			if !writer.event(name, &event.Sequence, payload) {
				return
			}
			after = event.Sequence
		}
		if observation.Terminal {
			switch observation.Status {
			case sessions.Completed, sessions.Failed, sessions.Aborted:
			default:
				writer.transportError()
				return
			}
			_ = writer.event("stream_end", nil, struct {
				Status sessions.Status `json:"status"`
				Last   uint64          `json:"last_sequence"`
			}{observation.Status, replay.LastSequence})
			return
		}
	waiting:
		for {
			select {
			case <-r.Context().Done():
				return
			case <-s.stopping:
				return
			case <-observation.Changed:
				break waiting
			case <-heartbeat.C:
				// Heartbeats do not query replay or change cursor/sequence.
				if !writer.write(": keep-alive\n\n") {
					return
				}
			}
		}
		observation, err = observer.ObserveEvents(id)
		if err != nil {
			writer.transportError()
			return
		}
	}
}
