//go:build linux

package server

import (
	"context"
	"github.com/netty-linux/daimon/internal/environments"
	"os"
	"strings"
	"testing"
)

func TestEnvironmentHTTPExplicitMetadataAndDeletionGuards(t *testing.T) {
	f := setup(t, finalModel, 64)
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	store, e := environments.NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	f.server.deps.Environments = store
	for _, body := range []string{`{"workspace":"/private"}`, `{"confirm":true}`, `null`, `{} {}`} {
		if r := request(f.server, "POST", "/api/v1/threads/thread/environment", body); r.Code != 400 {
			t.Fatal(r.Code, r.Body)
		}
	}
	if r := request(f.server, "GET", "/api/v1/threads/thread/environment", nil); r.Code != 404 {
		t.Fatal(r.Code)
	}
	r := request(f.server, "POST", "/api/v1/threads/thread/environment", `{}`)
	if r.Code != 201 || strings.Contains(r.Body.String(), "workspace") || strings.Contains(r.Body.String(), "entries") {
		t.Fatal(r.Code, r.Body)
	}
	if r = request(f.server, "DELETE", "/api/v1/threads/thread", nil); r.Code != 409 || !strings.Contains(r.Body.String(), "thread_has_environment") {
		t.Fatal(r.Code, r.Body)
	}
	reservation, e := store.Acquire(context.Background(), "thread")
	if e != nil {
		t.Fatal(e)
	}
	if r = request(f.server, "DELETE", "/api/v1/threads/thread/environment", nil); r.Code != 409 {
		t.Fatal(r.Code, r.Body)
	}
	reservation.Close()
	if r = request(f.server, "GET", "/api/v1/threads/thread/environment?path=x", nil); r.Code != 400 {
		t.Fatal(r.Code)
	}
	if r = request(f.server, "DELETE", "/api/v1/threads/thread/environment", nil); r.Code != 204 {
		t.Fatal(r.Code, r.Body)
	}
	if r = request(f.server, "DELETE", "/api/v1/threads/thread", nil); r.Code != 204 {
		t.Fatal(r.Code, r.Body)
	}
}
