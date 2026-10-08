package computer

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxViewers = 4
const MaxMediaPacket = 16 * 1024 * 1024
const MaxMediaControl = 16 * 1024
const MaxDimension = 1920

var ErrMedia = errors.New("computer: media unavailable")
var ErrHumanControl = errors.New("computer_controlled_by_human")
var ErrControl = errors.New("computer: invalid control lease")
var ErrCapacity = errors.New("computer: viewer capacity")

type MediaRequest struct {
	Target string
	Input  bool
}
type MediaInfo struct {
	SessionID string
	Codec     string
	Input     bool
}
type MediaPacket struct {
	Binary bool
	Data   []byte
}
type MediaProvider interface {
	Open(context.Context, MediaRequest) (MediaSession, error)
}
type MediaSession interface {
	Info() MediaInfo
	Receive(context.Context) (MediaPacket, error)
	Send(context.Context, []byte) error
	Close(context.Context) error
}
type VideoDescriptor struct {
	SessionID     string `json:"session_id"`
	Sequence      uint64 `json:"sequence"`
	GeometryEpoch uint64 `json:"geometry_epoch"`
	CodecEpoch    uint64 `json:"codec_epoch"`
	Width         int    `json:"width_px"`
	Height        int    `json:"height_px"`
	Timestamp     uint64 `json:"capture_timestamp_us"`
	Codec         string `json:"codec"`
	Keyframe      bool   `json:"keyframe"`
}

func VideoPacket(d VideoDescriptor, p []byte) ([]byte, error) {
	h, e := json.Marshal(struct {
		Direction string          `json:"direction"`
		Message   VideoDescriptor `json:"message"`
	}{"video", d})
	if e != nil {
		return nil, ErrMedia
	}
	if len(p) > MaxMediaPacket-len(h)-8 {
		return nil, ErrMedia
	}
	out := make([]byte, 8+len(h)+len(p))
	binary.BigEndian.PutUint32(out, uint32(len(h)))
	binary.BigEndian.PutUint32(out[4:], uint32(len(p)))
	copy(out[8:], h)
	copy(out[8+len(h):], p)
	return out, nil
}
func ParseVideo(p []byte, session string) (VideoDescriptor, []byte, error) {
	var h struct {
		Direction string          `json:"direction"`
		Message   VideoDescriptor `json:"message"`
	}
	if len(p) < 8 || len(p) > MaxMediaPacket {
		return h.Message, nil, ErrMedia
	}
	a, b := uint64(binary.BigEndian.Uint32(p)), uint64(binary.BigEndian.Uint32(p[4:]))
	if a > MaxMediaControl || a+b+8 != uint64(len(p)) {
		return h.Message, nil, ErrMedia
	}
	if !utf8.Valid(p[8:8+a]) || json.Unmarshal(p[8:8+a], &h) != nil {
		return h.Message, nil, ErrMedia
	}
	d := h.Message
	if h.Direction != "video" || d.SessionID != session || d.Width < 1 || d.Height < 1 || d.Width > MaxDimension || d.Height > MaxDimension || d.CodecEpoch == 0 || d.GeometryEpoch == 0 || d.Sequence > 9007199254740991 || d.Timestamp > 9007199254740991 {
		return d, nil, ErrMedia
	}
	bytes := p[8+a:]
	if len(bytes) == 0 {
		return d, nil, ErrMedia
	}
	switch d.Codec {
	case "h264":
	case "png":
		if len(bytes) < 24 || string(bytes[:8]) != "\x89PNG\r\n\x1a\n" || int(binary.BigEndian.Uint32(bytes[16:])) != d.Width || int(binary.BigEndian.Uint32(bytes[20:])) != d.Height {
			return d, nil, ErrMedia
		}
	case "bgra":
		if len(bytes) != d.Width*d.Height*4 {
			return d, nil, ErrMedia
		}
	default:
		return d, nil, ErrMedia
	}
	return d, bytes, nil
}

type InputEvent struct {
	Kind      string   `json:"kind"`
	Text      string   `json:"text,omitempty"`
	Key       string   `json:"key,omitempty"`
	State     string   `json:"state,omitempty"`
	Modifiers []string `json:"modifiers,omitempty"`
	Repeat    bool     `json:"repeat,omitempty"`
	Phase     string   `json:"phase,omitempty"`
	Button    *string  `json:"button,omitempty"`
	X         float64  `json:"x_normalized,omitempty"`
	Y         float64  `json:"y_normalized,omitempty"`
	DeltaX    float64  `json:"delta_x,omitempty"`
	DeltaY    float64  `json:"delta_y,omitempty"`
	Momentum  string   `json:"momentum_phase,omitempty"`
	Precise   bool     `json:"precise,omitempty"`
}
type InputBatch struct {
	FirstSequence uint64            `json:"first_sequence"`
	Events        []json.RawMessage `json:"events"`
}

// ValidateInput checks the documented RCDP event subset before a native effect.
// Original event bytes are forwarded only after exact-key validation.
func ValidateInput(raw []byte) (InputBatch, error) {
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	var batch InputBatch
	if len(raw) > MaxMediaControl || strictObject(raw, &envelope, "type", "payload") != nil || envelope.Type != "interactive_input" || strictObject(envelope.Payload, &batch, "first_sequence", "events") != nil || batch.FirstSequence < 1 || batch.FirstSequence > 9007199254740000 || len(batch.Events) < 1 || len(batch.Events) > 32 {
		return batch, ErrArguments
	}
	total := 0
	for _, rawEvent := range batch.Events {
		var v InputEvent
		var keys []string
		if json.Unmarshal(rawEvent, &v) != nil {
			return batch, ErrArguments
		}
		switch v.Kind {
		case "text_commit":
			keys = []string{"kind", "text"}
			if v.Text == "" || !utf8.ValidString(v.Text) || len(v.Text) > 4096 {
				return batch, ErrArguments
			}
			total += len(v.Text)
		case "key":
			keys = []string{"kind", "key", "state", "modifiers", "repeat"}
			if len(v.Key) < 1 || len(v.Key) > 32 || strings.Trim(v.Key, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" || (v.State != "down" && v.State != "up") {
				return batch, ErrArguments
			}
		case "pointer":
			keys = []string{"kind", "phase", "button", "x_normalized", "y_normalized", "modifiers"}
			if v.Phase != "move" && v.Phase != "down" && v.Phase != "up" && v.Phase != "cancel" {
				return batch, ErrArguments
			}
			if v.Button != nil && *v.Button != "left" && *v.Button != "right" && *v.Button != "middle" {
				return batch, ErrArguments
			}
			if (v.Phase == "down" || v.Phase == "up") && v.Button == nil {
				return batch, ErrArguments
			}
		case "scroll":
			keys = []string{"kind", "x_normalized", "y_normalized", "delta_x", "delta_y", "phase", "momentum_phase", "precise"}
			if v.Phase != "none" || v.Momentum != "none" || v.DeltaX < -2000 || v.DeltaX > 2000 || v.DeltaY < -2000 || v.DeltaY > 2000 {
				return batch, ErrArguments
			}
		default:
			return batch, ErrArguments
		}
		if v.X < 0 || v.X > 1 || v.Y < 0 || v.Y > 1 || len(v.Modifiers) > 4 {
			return batch, ErrArguments
		}
		seen := map[string]bool{}
		for _, m := range v.Modifiers {
			if seen[m] || (m != "control" && m != "shift" && m != "command" && m != "option") {
				return batch, ErrArguments
			}
			seen[m] = true
		}
		if strictObject(rawEvent, &v, keys...) != nil {
			return batch, ErrArguments
		}
	}
	if total > 4096 {
		return batch, ErrArguments
	}
	return batch, nil
}
func strictObject(raw []byte, out any, keys ...string) error {
	if !utf8.Valid(raw) || !validUnicodeEscapes(raw) {
		return ErrArguments
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return ErrArguments
	}
	seen := map[string]bool{}
	for d.More() {
		k, e := d.Token()
		s, ok := k.(string)
		if e != nil || !ok || !allowed[s] || seen[s] {
			return ErrArguments
		}
		seen[s] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || string(value) == "null" && s != "button" {
			return ErrArguments
		}
	}
	if _, e = d.Token(); e != nil {
		return ErrArguments
	}
	if len(seen) != len(keys) {
		return ErrArguments
	}
	if _, e = d.Token(); e == nil {
		return ErrArguments
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrArguments
	}
	return nil
}
func inputMessage(session string, b InputBatch) []byte {
	raw, _ := json.Marshal(struct {
		Type    string `json:"type"`
		Payload any    `json:"payload"`
	}{"interactive_input", struct {
		Session string            `json:"session_id"`
		First   uint64            `json:"first_sequence"`
		Events  []json.RawMessage `json:"events"`
	}{session, b.FirstSequence, b.Events}})
	return raw
}
func safeControl(kind string, payload any) []byte {
	p, _ := json.Marshal(struct {
		Type    string `json:"type"`
		Payload any    `json:"payload"`
	}{kind, payload})
	return p
}
func controlFailure(code string) []byte { return safeControl("error", map[string]string{"code": code}) }
func (m MediaInfo) String() string      { return fmt.Sprintf("Computer media (%s)", m.Codec) }
