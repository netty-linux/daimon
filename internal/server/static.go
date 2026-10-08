package server

import (
	"bytes"
	"embed"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Built assets are committed: ordinary Go builds/tests need no Node installation.
// npm run build in ui/ replaces only this dedicated asset directory.
//
//go:embed ui
var webAssets embed.FS

func (s *Server) static(w http.ResponseWriter, r *http.Request) bool {
	name := ""
	if r.URL.Path == "/" {
		name = "ui/index.html"
	} else if strings.HasPrefix(r.URL.Path, "/assets/") {
		file := strings.TrimPrefix(r.URL.Path, "/assets/")
		if file == "" || file != path.Base(file) || strings.ContainsAny(file, "\\\x00") || file == "." || file == ".." {
			failure(w, 404, "not_found")
			return true
		}
		name = "ui/assets/" + file
	} else {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		failure(w, 405, "method_not_allowed")
		return true
	}
	data, err := webAssets.ReadFile(name)
	if err != nil {
		failure(w, 404, "not_found")
		return true
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if name == "ui/index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(data))
	return true
}

// Network admission also validates Host to limit browser DNS rebinding. Local
// CLI clients need no Origin. Browser requests must match the request authority;
// the loopback-only development proxy preserves both Host and Origin.
func (s *Server) localHTTP(w http.ResponseWriter, r *http.Request) {
	if !localAuthority(r.Host) || !browserOriginAllowed(r) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		failure(w, 403, "forbidden_origin")
		return
	}
	s.ServeHTTP(w, r)
}

func localAuthority(authority string) bool {
	u, err := url.Parse("http://" + authority)
	if err != nil || u.Host != authority || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func browserOriginAllowed(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	u, err := url.Parse(values[0])
	return err == nil && u.Scheme == "http" && u.Host == r.Host && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
