package computer_test

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/computer/mediatest"
	"github.com/netty-linux/daimon/internal/computer/socket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualCUAMediaProtocolAndInputAcknowledgement(t *testing.T) {
	fake := mediatest.New("server-only-secret")
	defer fake.Close()
	host := httptest.NewServer(fake)
	defer host.Close()
	backend, e := computer.NewCUAMedia(host.URL, "server-only-secret")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, input := range []bool{false, true} {
		s, e := backend.Open(ctx, computer.MediaRequest{Target: "desktop", Input: input})
		if e != nil {
			t.Fatal(e)
		}
		if s.Info().Input != input {
			t.Fatal("policy")
		}
		packet, e := s.Receive(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = computer.ParseVideo(packet.Data, s.Info().SessionID); e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(map[string]any{"type": "interactive_input", "payload": map[string]any{"session_id": s.Info().SessionID, "first_sequence": 17, "events": []any{map[string]string{"kind": "text_commit", "text": "Exact\n😀"}}}})
		if input {
			if e = s.Send(ctx, raw); e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 10; i++ {
				p, e := s.Receive(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if !p.Binary {
					var a struct {
						Type string `json:"type"`
					}
					json.Unmarshal(p.Data, &a)
					if a.Type != "interactive_input_acknowledgement" {
						t.Fatal("ack")
					}
					break
				}
			}
		} else if e = s.Send(ctx, raw); e == nil {
			t.Fatal("view-only input")
		}
		if e = s.Close(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if fake.Active() != 0 || fake.InputCount.Load() != 1 {
		t.Fatal("lifecycle/input count", fake.Active(), fake.InputCount.Load())
	}
}
func TestCUAMediaConfigOnlyExactLoopbackNoRedirectOrSecretsInError(t *testing.T) {
	for _, url := range []string{"http://localhost:3211", "http://example.com:3211", "http://0.0.0.0:3211", "http://127.0.0.1:3211/path", "http://u:secret@127.0.0.1:3211", "http://127.0.0.1:3211?token=secret", "https://127.0.0.1:3211"} {
		if _, e := computer.NewCUAMedia(url, ""); e == nil {
			t.Fatal(url)
		}
	}
	if _, e := computer.NewCUAMedia("http://127.0.0.1:3211", "secret\nInjected"); e == nil {
		t.Fatal("header injection")
	}
}

func TestCUAMediaNeverFollowsRedirectOrReturnsServerContent(t *testing.T) {
	var touched atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		touched.Store(true)
		w.Write([]byte("private-secret-body"))
	}))
	defer destination.Close()
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusInternalServerError} {
		host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", destination.URL)
			w.WriteHeader(status)
			w.Write([]byte(strings.Repeat("private-secret-body", 8192)))
		}))
		backend, e := computer.NewCUAMedia(host.URL, "private-secret-header")
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, e = backend.Open(ctx, computer.MediaRequest{Target: "desktop"})
		cancel()
		host.Close()
		if e != computer.ErrMedia || strings.Contains(e.Error(), "private") || touched.Load() {
			t.Fatal("redirect or error disclosure", e)
		}
	}
}
func TestVideoRejectsMalformedPacketsAndWrongSession(t *testing.T) {
	d := computer.VideoDescriptor{SessionID: "media-test", Codec: "bgra", Width: 2, Height: 2, GeometryEpoch: 1, CodecEpoch: 1, Sequence: 45, Keyframe: true}
	p, err := computer.VideoPacket(d, make([]byte, 16))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, e := computer.ParseVideo(p, "media-test"); e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{p[:3], p[:len(p)-1], append(append([]byte{}, p...), 0)} {
		if _, _, e := computer.ParseVideo(bad, "media-test"); e == nil {
			t.Fatal("malformed frame")
		}
	}
	if _, _, e := computer.ParseVideo(p, "different-session"); e == nil {
		t.Fatal("session confusion")
	}
	d.Width = 1921
	bad, _ := computer.VideoPacket(d, make([]byte, 16))
	if _, _, e := computer.ParseVideo(bad, "media-test"); e == nil {
		t.Fatal("geometry")
	}
}

func TestCUAMediaFailedHandshakePreservesUnconfirmedRevocation(t *testing.T) {
	daemon := mediatest.New("server-only-secret")
	defer daemon.Close()
	daemon.FailClose.Store(true)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/media" {
			daemon.ServeHTTP(w, r)
			return
		}
		conn, e := socket.Upgrade(w, r, "rcdp.v2")
		if e != nil {
			return
		}
		defer conn.Close()
		conn.Write(1, []byte(`{"type":"hello","payload":{"protocol":"invalid","selected_version":2}}`))
	}))
	defer host.Close()
	backend, e := computer.NewCUAMedia(host.URL, daemon.Token)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, e := backend.Open(ctx, computer.MediaRequest{Target: "desktop", Input: true})
	if e != computer.ErrMedia || s == nil || !s.Info().Input {
		t.Fatal("lost unconfirmed input session", e)
	}
	if s.Close(ctx) == nil {
		t.Fatal("failed revocation became success")
	}
	if daemon.Active() != 1 {
		t.Fatal("test did not create a remote session")
	}
}
