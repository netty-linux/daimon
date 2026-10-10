package server

import (
	"context"
	"github.com/netty-linux/daimon/internal/routines"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRoutineHTTPStrictnessPrivacyAndReferences(t *testing.T) {
	f := setup(t, nil, 32)
	store, e := routines.Open(filepath.Join(t.TempDir(), "routines.json"))
	if e != nil {
		t.Fatal(e)
	}
	scheduler, e := routines.New(store, f.manager, f.threads, f.bots, false, false)
	if e != nil {
		t.Fatal(e)
	}
	f.server.deps.Routines = scheduler
	in := routines.Input{ID: "routine", BotID: "bot", ThreadID: "thread", Title: "Review", Prompt: "private-routine-prompt", DailyAt: "09:00", Timezone: "UTC", Enabled: true}
	if r := request(f.server, "POST", "/api/v1/routines", in); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	if r := request(f.server, "GET", "/api/v1/routines", nil); r.Code != 200 || strings.Contains(r.Body.String(), in.Prompt) || !strings.Contains(r.Body.String(), `"server_only":true`) {
		t.Fatal("leak", r.Code, r.Body)
	}
	if err := scheduler.Initialize(context.Background(), time.Now().UTC().Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if r := request(f.server, "GET", "/api/v1/routines", nil); r.Code != 200 || !strings.Contains(r.Body.String(), `"state":"missed"`) || !strings.Contains(r.Body.String(), `"scheduled_at"`) || !strings.Contains(r.Body.String(), `"detected_at"`) || strings.Contains(r.Body.String(), in.Prompt) {
		t.Fatal("missed metadata missing/private", r.Body)
	}
	for _, path := range []string{"/api/v1/bots/bot", "/api/v1/threads/thread"} {
		if r := request(f.server, "DELETE", path, nil); r.Code != 409 || !strings.Contains(r.Body.String(), "resource_has_routines") {
			t.Fatal("reference delete", r.Code)
		}
	}
	if r := request(f.server, "PUT", "/api/v1/routines/routine", map[string]bool{"enabled": false}); r.Code != 204 {
		t.Fatal(r.Code)
	}
	if r := request(f.server, "PUT", "/api/v1/routines/routine", map[string]any{}); r.Code != 400 {
		t.Fatal("missing enabled")
	}
	if r := request(f.server, "PUT", "/api/v1/routines/routine", map[string]any{"enabled": true, "approve_all": true}); r.Code != 400 {
		t.Fatal("unknown field")
	}
	if r := request(f.server, "GET", "/api/v1/routines?secret=one", nil); r.Code != 400 {
		t.Fatal("query")
	}
	if e := scheduler.Delete(context.Background(), in.ID); e != nil {
		t.Fatal(e)
	}
	if r := request(f.server, "DELETE", "/api/v1/bots/bot", nil); r.Code != 204 {
		t.Fatal("stale reference")
	}
}
