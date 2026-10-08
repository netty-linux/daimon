package computer

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/tools"
	"strings"
	"sync/atomic"
	"testing"
)

type countingHost struct{ probes atomic.Int32 }

func (h *countingHost) ID() BackendID { return CUALocal }
func (h *countingHost) Probe(context.Context) (Info, error) {
	h.probes.Add(1)
	return Info{ID: "cua", Backend: CUALocal, Status: "connected"}, nil
}
func (h *countingHost) Resolve(string) (Operation, error) { return nil, ErrDenied }
func TestSandboxCatalogClosedPathNeverConsultsHost(t *testing.T) {
	host := &countingHost{}
	catalog, _ := NewManager(host)
	id := "sandbox-" + strings.Repeat("a", 24)
	if _, e := catalog.ForComputer(context.Background(), id); e == nil || host.probes.Load() != 0 {
		t.Fatal("host fallback on missing guest")
	}
	if _, e := catalog.ViewState(context.Background(), id); e == nil || host.probes.Load() != 0 {
		t.Fatal("catalog guest view consulted host")
	}
	var guestCalls atomic.Int32
	adapter, e := NewSandboxAdapter(id, func(context.Context, string, json.RawMessage) (tools.ToolResult, error) {
		guestCalls.Add(1)
		return tools.ToolResult{Content: "guest"}, nil
	}, "list_apps")
	if e != nil {
		t.Fatal(e)
	}
	child, _ := NewManager(adapter)
	if e = catalog.RegisterComputer(id, child); e != nil {
		t.Fatal(e)
	}
	selected, e := catalog.ForComputer(context.Background(), id)
	if e != nil || selected != child || host.probes.Load() != 0 {
		t.Fatal(e)
	}
	if _, e = adapter.Resolve("mcp__cua__list_apps"); e == nil {
		t.Fatal("guest can address host")
	}
	catalog.UnregisterComputer(id, child)
	if _, e = catalog.ForComputer(context.Background(), id); e == nil || host.probes.Load() != 0 || guestCalls.Load() != 0 {
		t.Fatal("stale guest fell back")
	}
}
