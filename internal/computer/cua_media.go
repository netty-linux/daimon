package computer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/computer/socket"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// CUAMedia consumes the public StreamService protobuf gRPC-Web and RCDP v2.
// The external daemon is explicitly configured, never started or provisioned.
type CUAMedia struct {
	base    *url.URL
	token   string
	fleet   bool
	headers http.Header
	client  *http.Client
}

func NewCUAMedia(base, secret string) (*CUAMedia, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Port() == "" || u.RawPath != "" {
		return nil, ErrConfig
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() || u.Hostname() != ip.String() && u.Hostname() != "::1" {
		return nil, ErrConfig
	}
	if len(secret) > 4096 || strings.ContainsAny(secret, "\r\n\x00") || !utf8.ValidString(secret) {
		return nil, ErrConfig
	}
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, MaxConnsPerHost: 8, MaxIdleConnsPerHost: 4, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 16 * 1024}
	return &CUAMedia{base: u, token: secret, client: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *CUAMedia) rpc(ctx context.Context, method string, p []byte) ([]byte, error) {
	if method != "OpenMedia" && method != "CloseMedia" && method != "RequestKeyframe" {
		return nil, ErrMedia
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body := make([]byte, 5+len(p))
	binary.BigEndian.PutUint32(body[1:], uint32(len(p)))
	copy(body[5:], p)
	u := *c.base
	u.Path = strings.TrimSuffix(c.base.Path, "/") + "/cua.env.v1.StreamService/" + method
	r, e := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(body))
	if e != nil {
		return nil, ErrMedia
	}
	r.Header.Set("Content-Type", "application/grpc-web+proto")
	r.Header.Set("X-Grpc-Web", "1")
	for k, values := range c.headers {
		r.Header[k] = append([]string(nil), values...)
	}
	if c.token != "" {
		r.Header.Set("x-cua-env-authorization", "Bearer "+c.token)
	}
	res, e := c.client.Do(r)
	if e != nil {
		return nil, ErrMedia
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/grpc-web") {
		return nil, ErrMedia
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, 64*1024+1))
	if e != nil || len(data) > 64*1024 {
		return nil, ErrMedia
	}
	var message []byte
	seen := false
	status := res.Header.Get("Grpc-Status")
	for len(data) > 0 {
		if len(data) < 5 {
			return nil, ErrMedia
		}
		n := uint64(binary.BigEndian.Uint32(data[1:]))
		if n > uint64(len(data)-5) {
			return nil, ErrMedia
		}
		flag := data[0]
		part := data[5 : 5+n]
		data = data[5+n:]
		switch flag {
		case 0:
			if seen {
				return nil, ErrMedia
			}
			seen = true
			message = append([]byte{}, part...)
		case 128:
			if len(data) != 0 {
				return nil, ErrMedia
			}
			for _, line := range strings.Split(string(part), "\r\n") {
				if strings.HasPrefix(strings.ToLower(line), "grpc-status:") {
					status = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
				}
			}
		default:
			return nil, ErrMedia
		}
	}
	if !seen || status != "0" {
		return nil, ErrMedia
	}
	return message, nil
}

// Small protocol-specific protobuf helpers. Unknown fields are skipped; no
// generated CUA implementation, SDK, reflection or generic RPC proxy is used.
func pbInt(n int, v uint64) []byte {
	return append(binary.AppendUvarint(nil, uint64(n<<3)), binary.AppendUvarint(nil, v)...)
}
func pbBytes(n int, v []byte) []byte {
	b := binary.AppendUvarint(nil, uint64(n<<3|2))
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

type pbValue struct {
	integer uint64
	data    []byte
	wire    uint64
}

func pbParse(b []byte) (map[int]pbValue, error) {
	out := map[int]pbValue{}
	for count := 0; len(b) > 0; count++ {
		if count > 128 {
			return nil, ErrMedia
		}
		tag, n := binary.Uvarint(b)
		if n <= 0 || tag>>3 == 0 {
			return nil, ErrMedia
		}
		b = b[n:]
		v := pbValue{wire: tag & 7}
		field := int(tag >> 3)
		switch v.wire {
		case 0:
			x, k := binary.Uvarint(b)
			if k <= 0 {
				return nil, ErrMedia
			}
			v.integer = x
			b = b[k:]
		case 1:
			if len(b) < 8 {
				return nil, ErrMedia
			}
			v.data = b[:8]
			b = b[8:]
		case 2:
			l, k := binary.Uvarint(b)
			if k <= 0 || l > uint64(len(b)-k) {
				return nil, ErrMedia
			}
			v.data = b[k : k+int(l)]
			b = b[k+int(l):]
		case 5:
			if len(b) < 4 {
				return nil, ErrMedia
			}
			v.data = b[:4]
			b = b[4:]
		default:
			return nil, ErrMedia
		}
		if _, ok := out[field]; ok {
			return nil, ErrMedia
		}
		out[field] = v
	}
	return out, nil
}
func (c *CUAMedia) Open(ctx context.Context, r MediaRequest) (MediaSession, error) {
	if r.Target != "desktop" {
		return nil, ErrArguments
	}
	policy := uint64(1)
	if r.Input {
		policy = 3
	}
	req := pbBytes(1, pbBytes(1, []byte("primary")))
	req = append(req, pbBytes(2, []byte{1, 3, 2})...)
	req = append(req, pbInt(3, 30)...)
	req = append(req, pbInt(4, MaxDimension)...)
	req = append(req, pbInt(6, policy)...)
	req = append(req, pbInt(7, 1)...)
	req = append(req, pbBytes(8, pbInt(1, 30))...)
	p, e := c.rpc(ctx, "OpenMedia", req)
	if e != nil {
		return nil, e
	}
	f, e := pbParse(p)
	if e != nil {
		return nil, e
	}
	id, ticket := string(f[1].data), string(f[2].data)
	if id == "" || len(id) > 256 {
		return nil, ErrMedia
	}
	codec := map[uint64]string{1: "h264", 2: "bgra", 3: "png"}[f[6].integer]
	s := &cuaMediaSession{provider: c, info: MediaInfo{SessionID: id, Codec: codec, Input: r.Input}}
	// Preserve an unsuccessful close in the returned handle. The Manager must
	// never restore input authority after an unconfirmed remote revocation.
	failed := func() (MediaSession, error) {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if s.Close(closeCtx) != nil {
			return s, ErrMedia
		}
		return nil, ErrMedia
	}
	if len(ticket) < 16 || len(ticket) > 2048 || strings.Trim(ticket, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		return failed()
	}
	if f[13].integer != 2 || f[10].integer != policy || codec == "" || f[7].integer == 0 || f[7].integer > 30 || f[8].integer == 0 || f[8].integer > MaxDimension || len(f[14].data) != 0 {
		return failed()
	}
	u := *c.base
	u.Scheme = "ws"
	u.Path = strings.TrimSuffix(c.base.Path, "/") + "/media"
	u.RawQuery = ""
	var conn *socket.Conn
	if c.fleet {
		u.Scheme = "wss"
		conn, e = socket.DialFleet(ctx, &u, []string{"rcdp.v2", "cua.ticket." + ticket}, c.headers)
	} else {
		conn, e = socket.Dial(ctx, &u, []string{"rcdp.v2", "cua.ticket." + ticket})
	}
	if e != nil {
		return failed()
	}
	s.conn = conn
	// Validate the server-first handshake before exposing a session or control.
	for i := 0; i < 2; i++ {
		kind, p, e := s.read(ctx, 5*time.Second)
		var v struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if e != nil || kind != 1 || json.Unmarshal(p, &v) != nil {
			return failed()
		}
		if i == 0 {
			var h struct {
				Protocol     string   `json:"protocol"`
				Selected     int      `json:"selected_version"`
				Capabilities []string `json:"capabilities"`
			}
			if v.Type != "hello" || json.Unmarshal(v.Payload, &h) != nil || h.Protocol != "rcdp" || h.Selected != 2 {
				return failed()
			}
			if r.Input {
				found := false
				for _, cap := range h.Capabilities {
					if cap == "input.interactive.v2" {
						found = true
					}
				}
				if !found {
					return failed()
				}
			}
		} else {
			var h struct {
				ID     string `json:"session_id"`
				Policy string `json:"policy"`
				Codec  string `json:"codec"`
				Target struct {
					Kind string `json:"kind"`
				} `json:"target"`
			}
			wanted := "view_only"
			if r.Input {
				wanted = "allow_activation"
			}
			if v.Type != "session_opened" || json.Unmarshal(v.Payload, &h) != nil || h.ID != id || h.Policy != wanted || h.Codec != codec || h.Target.Kind != "display" {
				return failed()
			}
		}
	}
	return s, nil
}

type cuaMediaSession struct {
	provider *CUAMedia
	conn     *socket.Conn
	info     MediaInfo
	once     sync.Once
	closeErr error
}

func (s *cuaMediaSession) Info() MediaInfo { return s.info }
func (s *cuaMediaSession) read(ctx context.Context, timeout time.Duration) (byte, []byte, error) {
	if ctx.Err() != nil {
		return 0, nil, ctx.Err()
	}
	s.conn.SetReadDeadline(time.Now().Add(timeout))
	stop := context.AfterFunc(ctx, func() { s.conn.Close() })
	defer stop()
	for {
		kind, p, e := s.conn.Read(MaxMediaPacket)
		if e != nil {
			return 0, nil, ErrMedia
		}
		if kind == 10 {
			continue
		}
		if kind == 1 && len(p) > MaxMediaControl {
			return 0, nil, ErrMedia
		}
		return kind, p, nil
	}
}
func (s *cuaMediaSession) Receive(ctx context.Context) (MediaPacket, error) {
	kind, p, e := s.read(ctx, 60*time.Second)
	return MediaPacket{Binary: kind == 2, Data: p}, e
}
func (s *cuaMediaSession) Send(ctx context.Context, p []byte) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(p) > MaxMediaControl {
		return ErrArguments
	}
	var v struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(p, &v) != nil {
		return ErrArguments
	}
	if v.Type == "request_keyframe" {
		_, e := s.provider.rpc(ctx, "RequestKeyframe", pbBytes(1, []byte(s.info.SessionID)))
		return e
	}
	if !s.info.Input || v.Type != "interactive_input" {
		return ErrControl
	}
	if e := s.conn.Write(1, p); e != nil {
		return ErrMedia
	}
	return nil
}
func (s *cuaMediaSession) Close(ctx context.Context) error {
	s.once.Do(func() {
		if s.conn != nil {
			s.conn.Close()
		}
		_, s.closeErr = s.provider.rpc(ctx, "CloseMedia", pbBytes(1, []byte(s.info.SessionID)))
	})
	return s.closeErr
}

var fleetPath = regexp.MustCompile(`^/api/svc/[a-z0-9][a-z0-9-]{0,62}/[a-z0-9][a-z0-9-]{0,126}-(env|spacesd)$`)

// NewFleetMedia accepts only a private config obtained for an exact owned claim.
// Signed URLs, arbitrary origins and redirects are deliberately unsupported.
func NewFleetMedia(base string, credentials map[string]string) (*CUAMedia, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "https" || u.Host != "run.cua.ai" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !fleetPath.MatchString(u.Path) {
		return nil, ErrConfig
	}
	h := http.Header{}
	for k, v := range credentials {
		name := http.CanonicalHeaderKey(k)
		if name != "Authorization" && name != "X-Cua-Fleet-Claim" && name != "X-Cua-Env-Authorization" || h.Get(name) != "" || v == "" || len(v) > 8192 || strings.ContainsAny(v, "\r\n\x00") || !utf8.ValidString(v) {
			return nil, ErrConfig
		}
		if name != "X-Cua-Fleet-Claim" && !strings.HasPrefix(v, "Bearer ") {
			return nil, ErrConfig
		}
		h.Set(name, v)
	}
	if h.Get("Authorization") == "" || h.Get("X-Cua-Fleet-Claim") == "" {
		return nil, ErrConfig
	}
	tr := &http.Transport{Proxy: nil, DialContext: socket.DialPublicFleet, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, MaxConnsPerHost: 8, MaxIdleConnsPerHost: 4, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 16 * 1024}
	return &CUAMedia{base: u, fleet: true, headers: h, client: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
