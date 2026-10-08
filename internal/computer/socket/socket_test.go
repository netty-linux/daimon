package socket

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestActualUpgradeMaskedClientUnmaskedServerAndPing(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := Upgrade(w, r, "rcdp.v2")
		if e != nil {
			t.Error(e)
			return
		}
		defer c.Close()
		op, p, e := c.Read(1024)
		if e != nil || op != 1 || string(p) != "exact" {
			t.Error("request", e)
			return
		}
		if c.Write(9, []byte("ping")) != nil {
			return
		}
		op, p, e = c.Read(1024)
		if e != nil || op != 10 || string(p) != "ping" {
			t.Error("pong", e)
			return
		}
		_ = c.Write(2, []byte{1, 2, 3})
	}))
	defer host.Close()
	u, _ := url.Parse(host.URL)
	u.Scheme = "ws"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, e := Dial(ctx, u, []string{"rcdp.v2"})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = c.Write(1, []byte("exact")); e != nil {
		t.Fatal(e)
	}
	op, p, e := c.Read(1024)
	if e != nil || op != 2 || len(p) != 3 {
		t.Fatal(op, p, e)
	}
}
func TestBoundsMaskRSVAndFragmentation(t *testing.T) {
	for _, head := range [][]byte{{0x81, 0x00}, {0xc1, 0x80}, {0x89, 0xfe, 0, 126}, {0x82, 0xff, 0, 0, 0, 0, 1, 0, 0, 0}} {
		a, b := net.Pipe()
		c := &Conn{Conn: a, reader: bufio.NewReader(a)}
		done := make(chan struct{})
		go func() { defer close(done); defer b.Close(); b.Write(head) }()
		if _, _, e := c.Read(1024); e == nil {
			t.Fatal("unsafe frame")
		}
		a.Close()
		<-done
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := &Conn{Conn: a, reader: bufio.NewReader(a)}
	go func() {
		for _, h := range [][]byte{{1, 0x82, 1, 2, 3, 4, 'a' ^ 1, 'b' ^ 2}, {0x80, 0x81, 1, 2, 3, 4, 'c' ^ 1}} {
			b.Write(h)
		}
	}()
	op, p, e := c.Read(3)
	if e != nil || op != 1 || string(p) != "abc" {
		t.Fatal(op, string(p), e)
	}
}
