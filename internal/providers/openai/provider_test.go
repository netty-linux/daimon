package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/model"
)

const textResponse = `{"choices":[{"message":{"role":"assistant","content":"DAIMON"},"finish_reason":"stop"}]}`

func userRequest() model.ModelRequest {
	return model.ModelRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}
}
func newProvider(t *testing.T, server *httptest.Server, key string, limit int64) *Provider {
	t.Helper()
	p, err := New(Config{BaseURL: server.URL, APIKey: key, Model: "test-model", MaxResponseBytes: limit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.client.CloseIdleConnections)
	return p
}
func TestConfig(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, textResponse) }))
	defer s.Close()
	for _, tc := range []struct {
		name, base, model, key string
		limit                  int64
		invalid                bool
	}{
		{"valid", s.URL, "m", "", 1000, false},
		{"HTTPS", "https://provider.example/v1", "m", "key", 1000, false},
		{"localhost", "http://localhost:11434/v1", "m", "", 1000, false},
		{"IPv4 loopback", "http://127.100.2.3/v1", "m", "", 1000, false},
		{"IPv6 loopback", "http://[::1]:11434/v1", "m", "", 1000, false},
		{"empty URL", "", "m", "", 1000, true},
		{"empty model", s.URL, " ", "", 1000, true},
		{"zero limit", s.URL, "m", "", 0, true},
		{"negative limit", s.URL, "m", "", -1, true},
		{"overflow limit", s.URL, "m", "", 1<<63 - 1, true},
		{"no scheme", "localhost/v1", "m", "", 1000, true},
		{"scheme", "ftp://localhost/v1", "m", "", 1000, true},
		{"no host", "https:///v1", "m", "", 1000, true},
		{"no hostname", "https://:443/v1", "m", "", 1000, true},
		{"userinfo", "https://user:secret@provider.example/v1", "m", "", 1000, true},
		{"username", "https://user@provider.example/v1", "m", "", 1000, true},
		{"query", s.URL + "?secret=yes", "m", "", 1000, true},
		{"empty query", s.URL + "?", "m", "", 1000, true},
		{"fragment", s.URL + "#secret", "m", "", 1000, true},
		{"empty fragment", s.URL + "#", "m", "", 1000, true},
		{"remote HTTP", "http://provider.example/v1", "m", "", 1000, true},
		{"private HTTP", "http://192.168.1.1/v1", "m", "", 1000, true},
		{"localhost suffix", "http://localhost.example/v1", "m", "", 1000, true},
		{"remote IPv6", "http://[2001:db8::1]/v1", "m", "", 1000, true},
		{"port", "http://localhost:99999/v1", "m", "", 1000, true},
		{"empty port", "http://localhost:/v1", "m", "", 1000, true},
		{"invalid URL", "http://localhost/%xx", "m", "", 1000, true},
		{"header injection", s.URL, "m", "secret\r\nX-Secret: value", 1000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(Config{BaseURL: tc.base, Model: tc.model, APIKey: tc.key, MaxResponseBytes: tc.limit})
			if tc.invalid {
				var config *ConfigError
				if !errors.As(err, &config) || strings.Contains(err.Error(), "secret") {
					t.Fatalf("%T %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p.client.CloseIdleConnections()
			if p.client == http.DefaultClient || p.client.Timeout != 0 {
				t.Fatal("unsafe default client")
			}
			var _ model.Model = p
		})
	}
}

func TestRequestHTTP(t *testing.T) {
	for _, key := range []string{"", "sk-test-private-key"} {
		t.Run(fmt.Sprintf("key=%t", key != ""), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				for name, want := range map[string]string{"Content-Type": "application/json", "Accept": "application/json", "User-Agent": "daimon/0.3"} {
					if r.Header.Get(name) != want {
						t.Errorf("header %s", name)
					}
				}
				wantAuth := ""
				if key != "" {
					wantAuth = "Bearer " + key
				}
				if r.Header.Get("Authorization") != wantAuth {
					t.Error("authorization mismatch")
				}
				if key == "" {
					if _, ok := r.Header["Authorization"]; ok {
						t.Error("empty authorization header sent")
					}
				}
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if string(payload["model"]) != `"test-model"` || string(payload["stream"]) != "false" {
					t.Error("model/stream missing")
				}
				if _, ok := payload["tools"]; ok {
					t.Error("empty tools must be omitted")
				}
				io.WriteString(w, textResponse)
			}))
			defer s.Close()
			cfg := Config{BaseURL: s.URL + "/v1/", Model: "test-model", APIKey: key, MaxResponseBytes: 1024}
			p, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer p.client.CloseIdleConnections()
			got, err := p.Generate(context.Background(), userRequest())
			if err != nil || got.FinalText != "DAIMON" || calls.Load() != 1 {
				t.Fatal(got, err, calls.Load())
			}
		})
	}
}

func TestHTTPStatusesAndSanitizedMetadata(t *testing.T) {
	const key = "sk-sensitive-test-key"
	for _, status := range []int{400, 401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("x-request-id", "req-"+key+strings.Repeat("x", 200))
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(status)
				fmt.Fprintf(w, `{"error":{"code":"%s","type":"rate_limit","message":"%s"}}`, key, key)
			}))
			defer s.Close()
			p := newProvider(t, s, key, 2048)
			_, err := p.Generate(context.Background(), userRequest())
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != status || httpErr.RetryAfter != "120" || httpErr.Type != "rate_limit" {
				t.Fatalf("%T %v", err, err)
			}
			if !strings.HasPrefix(httpErr.RequestID, "req-[REDACTED]") || len(httpErr.RequestID) > 128 {
				t.Fatal("request id not bounded/redacted")
			}
			if strings.Contains(fmt.Sprintf("%+v", httpErr), key) || strings.Contains(httpErr.Code, key) || strings.Contains(httpErr.RequestID, key) || calls.Load() != 1 {
				t.Fatal("secret leaked or request retried")
			}
		})
	}
	for _, body := range []string{"", "not JSON", `{"error":{"message":"` + key + `"}}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500); io.WriteString(w, body) }))
		p := newProvider(t, s, key, 2048)
		_, err := p.Generate(context.Background(), userRequest())
		s.Close()
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != 500 || strings.Contains(err.Error(), key) {
			t.Fatal(err)
		}
	}
}

func TestResponseLimit(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		for _, oversized := range []bool{false, true} {
			t.Run(fmt.Sprintf("chunked=%t/over=%t", chunked, oversized), func(t *testing.T) {
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if chunked {
						w.(http.Flusher).Flush()
					}
					io.WriteString(w, textResponse)
					if oversized {
						io.WriteString(w, " ")
					}
				}))
				defer s.Close()
				p := newProvider(t, s, "", int64(len(textResponse)))
				got, err := p.Generate(context.Background(), userRequest())
				var tooLarge *ResponseTooLargeError
				if oversized {
					if !errors.As(err, &tooLarge) || tooLarge.Limit != int64(len(textResponse)) {
						t.Fatal(err)
					}
				} else if err != nil || got.FinalText != "DAIMON" {
					t.Fatal(got, err)
				}
			})
		}
	}
}

func TestDefaultClientRefusesRedirect(t *testing.T) {
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { leaked.Add(1); io.WriteString(w, textResponse) }))
	defer destination.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, status) }))
		p := newProvider(t, s, "sk-not-forwarded", 2048)
		_, err := p.Generate(context.Background(), userRequest())
		s.Close()
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != status || leaked.Load() != 0 {
			t.Fatal(err, leaked.Load())
		}
	}
}

func TestContextBeforeAndDuringRequest(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); io.WriteString(w, textResponse) }))
	p := newProvider(t, s, "", 1024)
	if _, err := p.Generate(ctx, userRequest()); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatal(err)
	}
	s.Close()
	for _, bodyStarted := range []bool{false, true} {
		t.Run(fmt.Sprintf("body=%t", bodyStarted), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				io.Copy(io.Discard, r.Body)
				if bodyStarted {
					w.(http.Flusher).Flush()
					io.WriteString(w, `{"choices":`)
					w.(http.Flusher).Flush()
				}
				cancel()
				<-r.Context().Done()
			}))
			defer s.Close()
			p := newProvider(t, s, "", 1024)
			_, err := p.Generate(ctx, userRequest())
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("server context not canceled")
			}
		})
	}
}

func TestDeadline(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer s.Close()
	p := newProvider(t, s, "", 1024)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.Generate(ctx, userRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestTransportCauseAndTLSClient(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, textResponse) }))
	const key = "sk-private-network-error"
	p, err := New(Config{BaseURL: s.URL + "/" + key, APIKey: key, Model: "m", MaxResponseBytes: 1024, HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer p.client.CloseIdleConnections()
	if _, err = p.Generate(context.Background(), userRequest()); err != nil {
		t.Fatal(err)
	}
	s.Close()
	_, err = p.Generate(context.Background(), userRequest())
	var transport *TransportError
	var network *url.Error
	if !errors.As(err, &transport) || !errors.As(err, &network) || !errors.Is(err, network.Err) {
		t.Fatalf("cause lost: %T %v", err, err)
	}
	if strings.Contains(err.Error(), s.URL) {
		t.Fatal("URL exposed")
	}
	if strings.Contains(err.Error(), key) { t.Fatal("key exposed in transport error") }
}

// The wrapper observes real httptest traffic rather than replacing HTTP responses.
type observingTransport struct {
	base           http.RoundTripper
	wantContext    context.Context
	contextMatched bool
	bodies         []*observedBody
}
type observedBody struct {
	io.ReadCloser
	closed bool
	bytes  int
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytes += n
	return n, err
}
func (b *observedBody) Close() error { b.closed = true; return b.ReadCloser.Close() }
func (o *observingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	o.contextMatched = req.Context() == o.wantContext
	res, err := o.base.RoundTrip(req)
	if res != nil {
		body := &observedBody{ReadCloser: res.Body}
		res.Body = body
		o.bodies = append(o.bodies, body)
	}
	return res, err
}
func TestBodyClosedAndBoundedOnEveryResponsePath(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"success", textResponse, 200}, {"JSON", "{", 200}, {"protocol", `{}`, 200}, {"HTTP", "failure", 500}, {"oversize", strings.Repeat("x", 2048), 200}, {"oversize HTTP", strings.Repeat("x", 2048), 500}, {"read error", "x", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.name == "read error" { w.Header().Set("Content-Length", "500") }
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observer := &observingTransport{base: s.Client().Transport, wantContext: ctx}
			p, err := New(Config{BaseURL: s.URL, Model: "m", MaxResponseBytes: 1024, HTTPClient: &http.Client{Transport: observer}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Client().CloseIdleConnections()
			_, generateErr := p.Generate(ctx, userRequest())
			if tc.name == "read error" {
				var transport *TransportError
				if !errors.As(generateErr, &transport) || !errors.Is(generateErr, io.ErrUnexpectedEOF) { t.Fatal(generateErr) }
			}
			if !observer.contextMatched || len(observer.bodies) != 1 || !observer.bodies[0].closed || observer.bodies[0].bytes > 1025 {
				t.Fatal("context, close, or read bound violated")
			}
		})
	}
}
