// Package socket implements the bounded RFC 6455 subset used by Computer media.
// No extensions, compression, redirects or arbitrary remote endpoints are used.
package socket

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
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

var ErrProtocol = errors.New("computer socket: invalid transport")

const MaxPacket = 16 * 1024 * 1024

type Conn struct {
	net.Conn
	reader  *bufio.Reader
	client  bool
	writeMu sync.Mutex
}

func accept(key string) string {
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h[:])
}
func token(header, want string) bool {
	for _, s := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return true
		}
	}
	return false
}
func Upgrade(w http.ResponseWriter, r *http.Request, protocol string) (*Conn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	decoded, e := base64.StdEncoding.DecodeString(key)
	if r.Method != "GET" || r.Header.Get("Sec-WebSocket-Version") != "13" || e != nil || len(decoded) != 16 || !token(r.Header.Get("Connection"), "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !token(r.Header.Get("Sec-WebSocket-Protocol"), protocol) {
		return nil, ErrProtocol
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, ErrProtocol
	}
	c, b, e := hj.Hijack()
	if e != nil {
		return nil, ErrProtocol
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	_, e = b.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept(key) + "\r\nSec-WebSocket-Protocol: " + protocol + "\r\n\r\n")
	if e == nil {
		e = b.Flush()
	}
	if e != nil {
		c.Close()
		return nil, ErrProtocol
	}
	c.SetDeadline(time.Time{})
	return &Conn{Conn: c, reader: b.Reader}, nil
}
func Dial(ctx context.Context, u *url.URL, protocols []string) (*Conn, error) {
	if u == nil || u.Scheme != "ws" || u.User != nil || u.Fragment != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, ErrProtocol
	}
	return dial(ctx, u, protocols, nil, (&net.Dialer{Timeout: 5 * time.Second}).DialContext)
}
func dial(ctx context.Context, u *url.URL, protocols []string, headers http.Header, connect func(context.Context, string, string) (net.Conn, error)) (*Conn, error) {
	address := u.Host
	if u.Scheme == "wss" && u.Port() == "" {
		address = net.JoinHostPort(u.Hostname(), "443")
	}
	c, e := connect(ctx, "tcp", address)
	if e != nil {
		return nil, ErrProtocol
	}
	success := false
	defer func() {
		if !success {
			c.Close()
		}
	}()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return nil, ErrProtocol
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return nil, ErrProtocol
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("Sec-WebSocket-Protocol", strings.Join(protocols, ", "))
	origin := "http://" + u.Host
	if u.Scheme == "wss" {
		origin = "https://" + u.Host
	}
	req.Header.Set("Origin", origin)
	for k, values := range headers {
		req.Header[k] = append([]string(nil), values...)
	}
	if e = req.Write(c); e != nil {
		return nil, ErrProtocol
	}
	limited := &io.LimitedReader{R: c, N: 32 * 1024}
	reader := bufio.NewReaderSize(limited, 4096)
	res, e := http.ReadResponse(reader, req)
	if e != nil {
		return nil, ErrProtocol
	}
	if res.StatusCode != 101 || !strings.EqualFold(res.Header.Get("Upgrade"), "websocket") || !token(res.Header.Get("Connection"), "upgrade") || res.Header.Get("Sec-WebSocket-Accept") != accept(key) || res.Header.Get("Sec-WebSocket-Extensions") != "" || res.Header.Get("Sec-WebSocket-Protocol") != "rcdp.v2" {
		res.Body.Close()
		return nil, ErrProtocol
	}
	c.SetDeadline(time.Time{})
	limited.N = 1 << 62
	success = true
	return &Conn{Conn: c, reader: reader, client: true}, nil
}

// Read returns complete messages, plus pong notifications for lease keepalive.
// Fragmentation is bounded across all fragments and control frames may interleave.
func (c *Conn) Read(limit int) (byte, []byte, error) {
	if limit <= 0 || limit > MaxPacket {
		return 0, nil, ErrProtocol
	}
	var data []byte
	var kind byte
	for fragments := 0; fragments < 1024; fragments++ {
		var h [2]byte
		if _, e := io.ReadFull(c.reader, h[:]); e != nil {
			return 0, nil, e
		}
		fin := h[0]&128 != 0
		op := h[0] & 15
		masked := h[1]&128 != 0
		if h[0]&112 != 0 || masked == c.client {
			return 0, nil, ErrProtocol
		}
		n := uint64(h[1] & 127)
		if n == 126 {
			var b [2]byte
			if _, e := io.ReadFull(c.reader, b[:]); e != nil {
				return 0, nil, e
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
			if n < 126 {
				return 0, nil, ErrProtocol
			}
		}
		if n == 127 {
			var b [8]byte
			if _, e := io.ReadFull(c.reader, b[:]); e != nil {
				return 0, nil, e
			}
			n = binary.BigEndian.Uint64(b[:])
			if n < 65536 || n>>63 != 0 {
				return 0, nil, ErrProtocol
			}
		}
		control := op >= 8
		if n > uint64(limit) || (!control && n > uint64(limit-len(data))) || (control && (!fin || n > 125)) {
			return 0, nil, ErrProtocol
		}
		var mask [4]byte
		if masked {
			if _, e := io.ReadFull(c.reader, mask[:]); e != nil {
				return 0, nil, e
			}
		}
		p := make([]byte, int(n))
		if _, e := io.ReadFull(c.reader, p); e != nil {
			return 0, nil, e
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i%4]
			}
		}
		if control {
			switch op {
			case 8:
				return 0, nil, io.EOF
			case 9:
				if e := c.Write(10, p); e != nil {
					return 0, nil, e
				}
				continue
			case 10:
				if kind == 0 {
					return 10, p, nil
				}
				continue
			default:
				return 0, nil, ErrProtocol
			}
		}
		if op == 1 || op == 2 {
			if kind != 0 {
				return 0, nil, ErrProtocol
			}
			kind = op
		} else if op != 0 || kind == 0 {
			return 0, nil, ErrProtocol
		}
		data = append(data, p...)
		if fin {
			if kind == 1 && !utf8.Valid(data) {
				return 0, nil, ErrProtocol
			}
			return kind, data, nil
		}
	}
	return 0, nil, ErrProtocol
}
func (c *Conn) Write(kind byte, p []byte) error {
	if len(p) > MaxPacket || (kind >= 8 && len(p) > 125) || (kind != 1 && kind != 2 && kind != 8 && kind != 9 && kind != 10) {
		return ErrProtocol
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	h := []byte{128 | kind, 0}
	if c.client {
		h[1] = 128
	}
	n := len(p)
	if n < 126 {
		h[1] |= byte(n)
	} else if n <= 65535 {
		h[1] |= 126
		h = append(h, byte(n>>8), byte(n))
	} else {
		h[1] |= 127
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		h = append(h, b[:]...)
	}
	if c.client {
		var mask [4]byte
		if _, e := rand.Read(mask[:]); e != nil {
			return ErrProtocol
		}
		h = append(h, mask[:]...)
		copyP := append([]byte(nil), p...)
		for i := range copyP {
			copyP[i] ^= mask[i%4]
		}
		p = copyP
	}
	if e := writeAll(c.Conn, h); e != nil {
		return e
	}
	return writeAll(c.Conn, p)
}
func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, e := w.Write(p)
		if e != nil {
			return e
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

// DialFleet admits only the official TLS Fleet gateway, never arbitrary remote hosts.
func DialFleet(ctx context.Context, u *url.URL, protocols []string, headers http.Header) (*Conn, error) {
	if u == nil || u.Scheme != "wss" || u.Host != "run.cua.ai" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !regexp.MustCompile(`^/api/svc/[a-z0-9][a-z0-9-]{0,62}/[a-z0-9][a-z0-9-]{0,126}-(env|spacesd)/media$`).MatchString(u.Path) {
		return nil, ErrProtocol
	}
	for k, v := range headers {
		if k != "Authorization" && k != "X-Cua-Fleet-Claim" && k != "X-Cua-Env-Authorization" || len(v) != 1 || len(v[0]) > 8192 || strings.ContainsAny(v[0], "\r\n\x00") {
			return nil, ErrProtocol
		}
	}
	connect := func(ctx context.Context, network, address string) (net.Conn, error) {
		raw, e := DialPublicFleet(ctx, network, address)
		if e != nil {
			return nil, e
		}
		c := tls.Client(raw, &tls.Config{ServerName: "run.cua.ai", MinVersion: tls.VersionTLS12})
		handshake, end := context.WithTimeout(ctx, 5*time.Second)
		defer end()
		if e = c.HandshakeContext(handshake); e != nil {
			raw.Close()
			return nil, ErrProtocol
		}
		return c, nil
	}
	return dial(ctx, u, protocols, headers, connect)
}

// DialPublicFleet resolves once and rejects non-public destinations before dialing.
func DialPublicFleet(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil || host != "run.cua.ai" || port != "443" {
		return nil, ErrProtocol
	}
	addresses, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil || len(addresses) == 0 {
		return nil, ErrProtocol
	}
	for _, a := range addresses {
		if !publicIP(a.IP) {
			return nil, ErrProtocol
		}
	}
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(addresses[0].IP.String(), port))
}
func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.To4() == nil {
		_, public, _ := net.ParseCIDR("2000::/3")
		if !public.Contains(ip) {
			return false
		}
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "2002::/16", "2001::/32"} {
		_, n, _ := net.ParseCIDR(cidr)
		if n.Contains(ip) {
			return false
		}
	}
	return true
}
