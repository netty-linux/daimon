// Package mediatest is an offline CUA protocol fixture. It never reads a desktop
// or injects native input; frames are generated color samples for tests only.
package mediatest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/computer/socket"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Fixture struct {
	Token      string
	FailOpen   atomic.Bool
	FailClose  atomic.Bool
	InputCount atomic.Int32
	Opens      atomic.Int32
	mu         sync.Mutex
	sessions   map[string]*session
	inputs     []json.RawMessage
}
type session struct {
	id, ticket string
	input      bool
	cancel     context.CancelFunc
	ctx        context.Context
}

func New(token string) *Fixture { return &Fixture{Token: token, sessions: map[string]*session{}} }
func (f *Fixture) Inputs() []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := make([]json.RawMessage, len(f.inputs))
	for i := range v {
		v[i] = append(json.RawMessage(nil), f.inputs[i]...)
	}
	return v
}
func (f *Fixture) Active() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sessions) }
func (f *Fixture) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		s.cancel()
	}
	f.sessions = map[string]*session{}
}
func number(n int, v uint64) []byte {
	return append(binary.AppendUvarint(nil, uint64(n<<3)), binary.AppendUvarint(nil, v)...)
}
func field(n int, p []byte) []byte {
	b := binary.AppendUvarint(nil, uint64(n<<3|2))
	b = binary.AppendUvarint(b, uint64(len(p)))
	return append(b, p...)
}
func readFields(p []byte) (map[int][]byte, map[int]uint64, bool) {
	a, b := map[int][]byte{}, map[int]uint64{}
	for len(p) > 0 {
		tag, k := binary.Uvarint(p)
		if k <= 0 {
			return nil, nil, false
		}
		p = p[k:]
		n := int(tag >> 3)
		switch tag & 7 {
		case 0:
			v, k := binary.Uvarint(p)
			if k <= 0 {
				return nil, nil, false
			}
			b[n] = v
			p = p[k:]
		case 2:
			l, k := binary.Uvarint(p)
			if k <= 0 || l > uint64(len(p)-k) {
				return nil, nil, false
			}
			a[n] = p[k : k+int(l)]
			p = p[k+int(l):]
		default:
			return nil, nil, false
		}
	}
	return a, b, true
}
func reply(w http.ResponseWriter, p []byte) {
	w.Header().Set("Content-Type", "application/grpc-web+proto")
	h := make([]byte, 5)
	binary.BigEndian.PutUint32(h[1:], uint32(len(p)))
	w.Write(h)
	w.Write(p)
	tail := []byte("grpc-status: 0\r\n")
	h[0] = 128
	binary.BigEndian.PutUint32(h[1:], uint32(len(tail)))
	w.Write(h)
	w.Write(tail)
}
func (f *Fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/media" {
		f.media(w, r)
		return
	}
	if r.Method != "POST" || (f.Token != "" && r.Header.Get("x-cua-env-authorization") != "Bearer "+f.Token) {
		w.WriteHeader(403)
		return
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 16385))
	if e != nil || len(raw) > 16384 || len(raw) < 5 || raw[0] != 0 || int(binary.BigEndian.Uint32(raw[1:])) != len(raw)-5 {
		w.WriteHeader(400)
		return
	}
	a, b, ok := readFields(raw[5:])
	if !ok {
		w.WriteHeader(400)
		return
	}
	switch r.URL.Path {
	case "/cua.env.v1.StreamService/OpenMedia":
		target, _, valid := readFields(a[1])
		if f.FailOpen.Load() || !valid || string(target[1]) != "primary" || b[4] != 1920 || b[3] != 30 || (b[6] != 1 && b[6] != 3) || len(a[10]) != 0 {
			w.WriteHeader(503)
			return
		}
		var random [24]byte
		if _, e = rand.Read(random[:]); e != nil {
			w.WriteHeader(500)
			return
		}
		ticket := base64.RawURLEncoding.EncodeToString(random[:])
		id := fmt.Sprintf("media-%d", f.Opens.Add(1))
		ctx, cancel := context.WithCancel(context.Background())
		s := &session{id: id, ticket: ticket, input: b[6] == 3, ctx: ctx, cancel: cancel}
		f.mu.Lock()
		f.sessions[id] = s
		f.mu.Unlock()
		p := field(1, []byte(id))
		p = append(p, field(2, []byte(ticket))...)
		p = append(p, number(6, 2)...)
		p = append(p, number(7, 30)...)
		p = append(p, number(8, 1920)...)
		p = append(p, number(10, b[6])...)
		p = append(p, number(13, 2)...)
		reply(w, p)
	case "/cua.env.v1.StreamService/CloseMedia":
		if f.FailClose.Load() {
			w.WriteHeader(503)
			return
		}
		f.mu.Lock()
		if s := f.sessions[string(a[1])]; s != nil {
			s.cancel()
			delete(f.sessions, s.id)
		}
		f.mu.Unlock()
		reply(w, nil)
	case "/cua.env.v1.StreamService/RequestKeyframe":
		reply(w, nil)
	default:
		w.WriteHeader(404)
	}
}
func (f *Fixture) media(w http.ResponseWriter, r *http.Request) {
	var s *session
	f.mu.Lock()
	for _, candidate := range f.sessions {
		if strings.Contains(r.Header.Get("Sec-WebSocket-Protocol"), "cua.ticket."+candidate.ticket) {
			s = candidate
			break
		}
	}
	f.mu.Unlock()
	if s == nil {
		w.WriteHeader(401)
		return
	}
	c, e := socket.Upgrade(w, r, "rcdp.v2")
	if e != nil {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		c.Close()
	}()
	defer close(done)
	write := func(kind string, p any) error {
		b, _ := json.Marshal(map[string]any{"type": kind, "payload": p})
		return c.Write(1, b)
	}
	caps := []string{"desktop.v1", "input.interactive.v2"}
	policy := "view_only"
	if s.input {
		policy = "allow_activation"
	}
	if write("hello", map[string]any{"protocol": "rcdp", "selected_version": 2, "capabilities": caps}) != nil {
		return
	}
	if write("session_opened", map[string]any{"session_id": s.id, "policy": policy, "codec": "bgra", "target": map[string]string{"kind": "display", "display_id": "primary"}}) != nil {
		return
	}
	frameDone := make(chan struct{})
	go func() {
		defer close(frameDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		sequence := uint64(10)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sequence++
				pixels := make([]byte, 64*36*4)
				for i := 0; i < len(pixels); i += 4 {
					pixels[i] = byte(sequence * 7)
					pixels[i+1] = 140
					pixels[i+2] = 70
					pixels[i+3] = 255
				}
				d := computer.VideoDescriptor{SessionID: s.id, Sequence: sequence, GeometryEpoch: 1, CodecEpoch: 1, Width: 64, Height: 36, Timestamp: sequence * 100000, Codec: "bgra", Keyframe: true}
				p, _ := computer.VideoPacket(d, pixels)
				if c.Write(2, p) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); c.Close(); <-frameDone }()
	next := uint64(0)
	for {
		kind, p, e := c.Read(computer.MaxMediaControl)
		if e != nil {
			return
		}
		if kind == 10 {
			continue
		}
		if kind != 1 {
			return
		}
		var envelope struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(p, &envelope) != nil {
			return
		}
		if envelope.Type == "request_keyframe" {
			continue
		}
		if envelope.Type != "interactive_input" || !s.input {
			_ = write("error", map[string]string{"code": "view_only"})
			return
		}
		var input struct {
			Session string            `json:"session_id"`
			First   uint64            `json:"first_sequence"`
			Events  []json.RawMessage `json:"events"`
		}
		if json.Unmarshal(envelope.Payload, &input) != nil || input.Session != s.id || next != 0 && next != input.First {
			return
		}
		batch, _ := json.Marshal(map[string]any{"type": "interactive_input", "payload": map[string]any{"first_sequence": input.First, "events": input.Events}})
		if _, e := computer.ValidateInput(batch); e != nil {
			return
		}
		next = input.First + uint64(len(input.Events))
		f.mu.Lock()
		for _, event := range input.Events {
			f.inputs = append(f.inputs, append(json.RawMessage(nil), event...))
			if len(f.inputs) > 256 {
				f.inputs = f.inputs[len(f.inputs)-256:]
			}
			f.InputCount.Add(1)
		}
		f.mu.Unlock()
		if write("interactive_input_acknowledgement", map[string]any{"session_id": s.id, "through_sequence": next - 1, "delivered": true, "error": nil}) != nil {
			return
		}
	}
}
