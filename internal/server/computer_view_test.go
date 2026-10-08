package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/computer/mediatest"
	"github.com/netty-linux/daimon/internal/computer/socket"
	"github.com/netty-linux/daimon/internal/tools"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type viewBackend struct{}

func (viewBackend) ID() computer.BackendID { return computer.CUALocal }
func (viewBackend) Probe(context.Context) (computer.Info, error) {
	return computer.Info{ID: "cua", Backend: computer.CUALocal, Status: "connected", Capabilities: []computer.Capability{}}, nil
}
func (viewBackend) Resolve(name string) (computer.Operation, error) {
	return viewOperation{name: name}, nil
}

type viewOperation struct{ name string }

func (o viewOperation) Definition() computer.Definition {
	return computer.Definition{Name: o.name, Schema: json.RawMessage(`{"type":"object"}`)}
}
func (viewOperation) Call(context.Context, json.RawMessage) (tools.ToolResult, error) {
	return tools.ToolResult{Content: "safe"}, nil
}
func TestComputerViewHTTPWebSocketAndOwnershipBoundary(t *testing.T) {
	f := setup(t, finalModel, 32)
	daemon := mediatest.New("never-expose-root-secret")
	defer daemon.Close()
	upstream := httptest.NewServer(daemon)
	defer upstream.Close()
	media, e := computer.NewCUAMedia(upstream.URL, daemon.Token)
	if e != nil {
		t.Fatal(e)
	}
	m, _ := computer.NewManager(viewBackend{})
	m.ConfigureMedia(media)
	f.server.deps.Computers = m
	defer m.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b, e := m.Open(ctx, "run", computer.Profile{Enabled: true, Backend: computer.CUALocal, MCPServerID: "cua"}, []string{}, false)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close(context.Background())
	host := httptest.NewServer(http.HandlerFunc(f.server.localHTTP))
	defer host.Close()
	post := func(path, body, origin string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest("POST", host.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		res, e := host.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return res.StatusCode, data
	}
	path := "/api/v1/computers/cua"
	for _, tc := range []struct {
		path, site string
		want       int
	}{
		{path + "/view", "same-origin", 200},
		{path + "/view", "cross-site", 403},
		{path + "/views/untrusted/media", "same-origin", 403},
		{path + "/views/untrusted/input", "same-origin", 403},
	} {
		req, _ := http.NewRequest("GET", host.URL+tc.path, nil)
		req.Header.Set("Sec-Fetch-Site", tc.site)
		res, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatalf("origin admission %s: %d", tc.path, res.StatusCode)
		}
	}
	if code, _ := post(path+"/views", `{"target":"desktop"}`, "http://evil.example"); code != 403 {
		t.Fatal("cross-origin viewer", code)
	}
	code, p := post(path+"/views", `{"target":"desktop"}`, host.URL)
	if code != 201 {
		t.Fatal(code, string(p))
	}
	var v computer.ViewTicket
	json.Unmarshal(p, &v)
	if bytes.Contains(p, []byte(daemon.Token)) || bytes.Contains(p, []byte("cua.ticket")) {
		t.Fatal("credential leak")
	}
	dial := func(ticket computer.ViewTicket, kind, token string) (*socket.Conn, error) {
		u, _ := url.Parse(host.URL + path + "/views/" + ticket.ID + "/" + kind)
		u.Scheme = "ws"
		return socket.Dial(ctx, u, []string{"rcdp.v2", "daimon." + kind + "." + token})
	}
	viewer, e := dial(v, "media", v.Token)
	if e != nil {
		t.Fatal(e)
	}
	defer viewer.Close()
	for i := 0; i < 3; i++ {
		op, p, e := viewer.Read(computer.MaxMediaPacket)
		if e != nil {
			t.Fatal(e)
		}
		if i < 2 && op != 1 || i == 2 && op != 2 {
			t.Fatal("media handshake/frame", op)
		}
		if bytes.Contains(p, []byte(daemon.Token)) {
			t.Fatal("secret in stream")
		}
	}
	takeBody, _ := json.Marshal(map[string]string{"view_id": v.ID, "token": v.Token})
	if code, _ := post(path+"/control/take", string(takeBody), "http://evil.example"); code != 403 {
		t.Fatal("cross origin input")
	}
	code, p = post(path+"/control/take", string(takeBody), host.URL)
	if code != 200 {
		t.Fatal(code, string(p))
	}
	var lease computer.ControlTicket
	json.Unmarshal(p, &lease)
	if code, _ := post(path+"/control/take", string(takeBody), host.URL); code != 409 {
		t.Fatal("duplicate takeover")
	}
	if _, e := dial(v, "input", v.Token); e == nil {
		t.Fatal("view token granted input")
	}
	control, e := dial(v, "input", lease.Token)
	if e != nil {
		t.Fatal(e)
	}
	defer control.Close()
	human := []byte(`{"type":"interactive_input","payload":{"first_sequence":1,"events":[{"kind":"text_commit","text":"exact-private-human-input"}]}}`)
	if control.Write(1, human) != nil {
		t.Fatal("send")
	}
	op, ack, e := control.Read(16384)
	if e != nil || op != 1 || !bytes.Contains(ack, []byte(`"delivered":true`)) {
		t.Fatal("ack", e, string(ack))
	}
	if bytes.Contains(ack, []byte("exact-private-human-input")) {
		t.Fatal("input leaked into receipt")
	}
	if daemon.InputCount.Load() != 1 {
		t.Fatal("dispatch")
	}
	releaseBody, _ := json.Marshal(map[string]string{"view_id": v.ID, "token": lease.Token})
	if code, _ := post(path+"/control/release", string(releaseBody), host.URL); code != 200 {
		t.Fatal("release", code)
	}
	if _, e := dial(v, "input", lease.Token); e == nil {
		t.Fatal("stale control")
	}
	viewer.Close()
	deadline := time.Now().Add(time.Second)
	for daemon.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if daemon.Active() != 0 {
		t.Fatal("media leak")
	}
}
func TestComputerViewMissingDoesNotChangeActionMetadata(t *testing.T) {
	f := setup(t, finalModel, 32)
	m, _ := computer.NewManager(viewBackend{})
	defer m.Close(context.Background())
	f.server.deps.Computers = m
	for _, path := range []string{"/api/v1/computers/cua/view", "/api/v1/computers/cua"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = "127.0.0.1:3000"
		w := httptest.NewRecorder()
		f.server.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(path, "/view") && !strings.Contains(w.Body.String(), `"available":false`) {
			t.Fatal("invented media")
		}
	}
}
