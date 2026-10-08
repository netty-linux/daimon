package server

import (
	"encoding/json"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/sandbox"
	"net/http"
	"strings"
	"testing"
)

func TestSandboxReadonlyAPIUnavailableAndNoCreate(t *testing.T) {
	f := setup(t, finalModel, 16)
	r := request(f.server, http.MethodGet, "/api/v1/sandboxes", nil)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var v struct {
		Sandboxes []json.RawMessage `json:"sandboxes"`
		Runtime   struct {
			Available bool `json:"available"`
		}
	}
	if e := json.Unmarshal(r.Body.Bytes(), &v); e != nil || v.Sandboxes == nil || v.Runtime.Available {
		t.Fatal(r.Body.String())
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
		r = request(f.server, method, "/api/v1/sandboxes", map[string]string{"image": "arbitrary", "command": "host-command"})
		if r.Code != 405 {
			t.Fatal(method, r.Code, r.Body.String())
		}
	}
	r = request(f.server, http.MethodGet, "/api/v1/sandboxes/sb-"+strings.Repeat("a", 24), nil)
	if r.Code != 404 {
		t.Fatal(r.Code)
	}
}

func TestCloudProfileHTTPIsExplicitAndNoAuthOrRawAdmin(t *testing.T) {
	f := setup(t, finalModel, 16)
	b := f.bot
	p := sandbox.Profile{Backend: sandbox.CUACloud, Placement: sandbox.Cloud, Image: "linux", Runtime: "gvisor", Resources: "small", Network: "outbound"}
	b.SandboxProfile = &p
	raw, _ := json.Marshal(b)
	response := request(f.server, "PUT", "/api/v1/bots/bot", string(raw))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"placement":"cloud"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, bad := range []string{strings.Replace(string(raw), `"placement":"cloud",`, "", 1), strings.Replace(string(raw), `"resources":"small"`, `"resources":"arbitrary"`, 1), strings.Replace(string(raw), `"network":"outbound"`, `"network":"outbound","token":"secret"`, 1)} {
		if request(f.server, "PUT", "/api/v1/bots/bot", bad).Code != 400 {
			t.Fatal("unsafe cloud profile")
		}
	}
	b.SandboxProfile = nil
	b.ComputerProfile = &computer.Profile{Enabled: true, Backend: computer.CUACloud, MCPServerID: "sandbox"}
	raw, _ = json.Marshal(b)
	if request(f.server, "PUT", "/api/v1/bots/bot", string(raw)).Code != 400 {
		t.Fatal("host profile enabled cloud")
	}
	for _, route := range []string{"/api/v1/cloud/auth", "/api/v1/cloud/login", "/api/v1/cloud/pools", "/api/v1/sandboxes/create"} {
		if w := request(f.server, "POST", route, map[string]string{"token": "secret"}); w.Code != 404 && w.Code != 405 {
			t.Fatal(route, w.Code)
		}
	}
	response = request(f.server, "GET", "/api/v1/sandboxes", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"backend":"cua-cloud"`) || strings.Contains(response.Body.String(), "secret") {
		t.Fatal(response.Body.String())
	}
}
