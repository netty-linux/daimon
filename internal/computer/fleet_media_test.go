package computer

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFleetMediaStrictOriginPathAndHeaders(t *testing.T) {
	const base = "https://run.cua.ai/api/svc/pool-a/guest-one-env"
	headers := map[string]string{"authorization": "Bearer private", "x-cua-fleet-claim": "opaque-claim"}
	m, e := NewFleetMedia(base, headers)
	if e != nil {
		t.Fatal(e)
	}
	headers["authorization"] = "Bearer mutated"
	if m.headers.Get("Authorization") != "Bearer private" {
		t.Fatal("mutable credentials")
	}
	for _, bad := range []string{"http://run.cua.ai/api/svc/a/b-spacesd", "https://127.0.0.1/api/svc/a/b-spacesd", "https://run.cua.ai.evil/api/svc/a/b-spacesd", base + "?token=secret", base + "#x", "https://user@run.cua.ai/api/svc/a/b-spacesd", "https://run.cua.ai:444/api/svc/a/b-spacesd", "https://run.cua.ai/api/svc/../guest-spacesd", "https://run.cua.ai/api/svc/a/%2e%2e-spacesd", "https://run.cua.ai/api/svc/a/guest-driver"} {
		if _, e = NewFleetMedia(bad, headers); e == nil {
			t.Fatal(bad)
		}
	}
	for _, bad := range []map[string]string{{"authorization": "Bearer x"}, {"authorization": "Bearer x\r\nHost: evil", "x-cua-fleet-claim": "x"}, {"authorization": "Bearer x", "x-cua-fleet-claim": "x", "Cookie": "secret"}} {
		if _, e = NewFleetMedia(base, bad); e == nil {
			t.Fatal("bad headers accepted")
		}
	}
}
func TestFleetRPCUsesTLSPrivateHeadersAndPrefixNoRedirect(t *testing.T) {
	calls := 0
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/svc/pool-a/guest-one-env/cua.env.v1.StreamService/CloseMedia" || r.Header.Get("Authorization") != "Bearer private" || r.Header.Get("X-Cua-Fleet-Claim") != "claim" {
			t.Error("incorrect scope")
		}
		w.Header().Set("Location", "http://127.0.0.1/private")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer fixture.Close()
	m, e := NewFleetMedia("https://run.cua.ai/api/svc/pool-a/guest-one-env", map[string]string{"authorization": "Bearer private", "x-cua-fleet-claim": "claim"})
	if e != nil {
		t.Fatal(e)
	}
	// Fixture-only transport routes the official host to an offline TLS server.
	client := fixture.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(fixture.URL, "https://"))
	}
	m.client.Transport = transport
	if _, e = m.rpc(context.Background(), "CloseMedia", nil); e == nil || calls != 1 {
		t.Fatal("redirect accepted", calls, e)
	}
}
