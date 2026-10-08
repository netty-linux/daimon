package bots

import (
	"encoding/json"
	"github.com/netty-linux/daimon/internal/computer"
	"os"
	"testing"
)

func TestOptionalComputerProfileCompatibleStrictAndCopied(t *testing.T) {
	store := newStore(t)
	b := fixture("old")
	if err := store.Create(b); err != nil {
		t.Fatal(err)
	}
	old, _ := store.Get(b.ID)
	if old.ComputerProfile != nil {
		t.Fatal("old bot changed")
	}
	b.ComputerProfile = &computer.Profile{Enabled: true, Backend: computer.CUALocal, MCPServerID: "cua"}
	if err := store.Update(b); err != nil {
		t.Fatal(err)
	}
	b.ComputerProfile.Enabled = false
	got, _ := store.Get(b.ID)
	if !got.ComputerProfile.Enabled {
		t.Fatal("caller alias")
	}
	clone := Clone(got)
	clone.ComputerProfile.MCPServerID = "mutated"
	if got.ComputerProfile.MCPServerID != "cua" {
		t.Fatal("clone alias")
	}
	raw, _ := os.ReadFile(store.path)
	for _, bad := range []string{`{"enabled":true,"backend":"cua-local"}`, `{"Enabled":true,"backend":"cua-local","mcp_server_id":"cua"}`, `{"enabled":true,"enabled":false,"backend":"cua-local","mcp_server_id":"cua"}`, `null`} {
		// Store files are indented: rewrite their profile through a JSON projection.
		var document map[string]json.RawMessage
		_ = json.Unmarshal(raw, &document)
		var bots []map[string]json.RawMessage
		_ = json.Unmarshal(document["bots"], &bots)
		bots[0]["computer_profile"] = json.RawMessage(bad)
		value, _ := json.Marshal(bots)
		document["bots"] = value
		encoded, _ := json.Marshal(document)
		candidate := string(encoded)
		if err := os.WriteFile(store.path, []byte(candidate), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.List(); err == nil {
			t.Fatal("ambiguous profile", bad)
		}
	}
}
