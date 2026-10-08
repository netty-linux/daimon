package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedUIRoutes(t *testing.T) {
	f := setup(t, finalModel, 8)
	w := request(f.server, "GET", "/", nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), "DAIMON") || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("embedded index")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("CSP")
	}
	files, err := webAssets.ReadDir("ui/assets")
	if err != nil || len(files) == 0 {
		t.Fatal("committed assets missing")
	}
	for _, file := range files {
		w = request(f.server, "GET", "/assets/"+file.Name(), nil)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") || w.Header().Get("Content-Type") == "application/json" {
			t.Fatal("asset serving")
		}
	}
	expect(t, request(f.server, "GET", "/api/v1/health", nil), 200)
	for _, target := range []string{"/api", "/api/v1/missing", "/other", "/assets/../index.html", "/assets/%2e%2e/index.html", "/assets/..%5cindex.html", "/assets/", "/assets/missing.js"} {
		expect(t, request(f.server, "GET", target, nil), 404)
	}
	expect(t, request(f.server, "POST", "/", nil), 405)
}

func TestLocalBrowserAdmission(t *testing.T) {
	f := setup(t, finalModel, 8)
	for _, test := range []struct {
		host, origin, site string
		want               int
	}{
		{"127.0.0.1:3000", "", "", 200},
		{"127.0.0.1:3000", "http://127.0.0.1:3000", "same-origin", 200},
		{"localhost:5173", "http://localhost:5173", "same-origin", 200},
		{"[::1]:3000", "http://[::1]:3000", "same-origin", 200},
		{"attacker.example:3000", "", "", 403},
		{"127.0.0.1:3000", "http://attacker.example", "", 403},
		{"127.0.0.1:3000", "null", "", 403},
		{"127.0.0.1:3000", "http://127.0.0.1:3000", "cross-site", 403},
		{"127.0.0.1:3000", "http://127.0.0.1:4000", "same-site", 403},
	} {
		r := httptest.NewRequest("GET", "/api/v1/health", nil)
		r.Host = test.host
		if test.origin != "" {
			r.Header.Set("Origin", test.origin)
		}
		if test.site != "" {
			r.Header.Set("Sec-Fetch-Site", test.site)
		}
		w := httptest.NewRecorder()
		f.server.localHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("admission: got %d want %d", w.Code, test.want)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS changed")
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"id":"browser-session","thread_id":"thread","message":"task"}`))
	r.Host = "127.0.0.1:3000"
	r.Header.Set("Origin", "http://evil.example")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.server.localHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("foreign browser mutation accepted")
	}
	if _, err := f.manager.Get("browser-session"); err == nil {
		t.Fatal("rejected request started execution")
	}
}
