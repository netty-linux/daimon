package socket

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestFleetDestinationRestrictionsBeforeNetwork(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "224.0.0.1", "::1", "fe80::1", "fc00::1", "fec0::1", "::ffff:127.0.0.1", "2001:db8::1", "2002:7f00:1::1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Fatal("unsafe address", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "2606:4700::1111"} {
		if !publicIP(net.ParseIP(ip)) {
			t.Fatal(ip)
		}
	}
	for _, address := range []string{"127.0.0.1:443", "run.cua.ai:80", "run.cua.ai.evil:443", "localhost:443"} {
		if c, e := DialPublicFleet(context.Background(), "tcp", address); e == nil {
			c.Close()
			t.Fatal(address)
		}
	}
	for _, raw := range []string{"ws://run.cua.ai/api/svc/a/b-env/media", "wss://localhost/api/svc/a/b-env/media", "wss://run.cua.ai/api/svc/a/b-env/media?token=x", "wss://run.cua.ai/api/svc/a/../media", "wss://run.cua.ai/api/svc/a/b-env/media#x"} {
		u, _ := url.Parse(raw)
		if c, e := DialFleet(context.Background(), u, []string{"rcdp.v2"}, nil); e == nil {
			c.Close()
			t.Fatal(raw)
		}
	}
	u, _ := url.Parse("wss://run.cua.ai/api/svc/a/b-env/media")
	if c, e := DialFleet(context.Background(), u, []string{"rcdp.v2"}, http.Header{"Host": []string{"evil"}}); e == nil {
		c.Close()
		t.Fatal("header override")
	}
}
