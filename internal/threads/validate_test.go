package threads

import (
	"errors"
	"github.com/netty-linux/daimon/internal/bots"
	"strings"
	"testing"
	"time"
)

func fixture(id ID) Thread {
	created := time.Date(2026, 10, 7, 12, 0, 0, 123, time.UTC)
	return Thread{ID: id, BotID: bots.ID("coder"), Workspace: "explicit/workspace", Title: "Private title", CreatedAt: created, UpdatedAt: created}
}

func TestValidate(t *testing.T) {
	if err := Validate(fixture("thread-a")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, field string
		change      func(*Thread)
	}{
		{"empty ID", "id", func(v *Thread) { v.ID = "" }},
		{"invalid ID", "id", func(v *Thread) { v.ID = "../bad" }},
		{"large ID", "id", func(v *Thread) { v.ID = ID(strings.Repeat("a", MaxIDBytes+1)) }},
		{"empty BotID", "bot_id", func(v *Thread) { v.BotID = "" }},
		{"invalid BotID", "bot_id", func(v *Thread) { v.BotID = "Bad bot" }},
		{"large BotID", "bot_id", func(v *Thread) { v.BotID = bots.ID(strings.Repeat("a", 65)) }},
		{"empty workspace", "workspace", func(v *Thread) { v.Workspace = " \n" }},
		{"workspace UTF8", "workspace", func(v *Thread) { v.Workspace = "\xff" }},
		{"workspace NUL", "workspace", func(v *Thread) { v.Workspace = "path\x00tail" }},
		{"large workspace", "workspace", func(v *Thread) { v.Workspace = strings.Repeat("a", MaxWorkspaceBytes+1) }},
		{"title UTF8", "title", func(v *Thread) { v.Title = "\xff" }},
		{"large title", "title", func(v *Thread) { v.Title = strings.Repeat("a", MaxTitleBytes+1) }},
		{"zero created", "created_at", func(v *Thread) { v.CreatedAt = time.Time{} }},
		{"zero updated", "updated_at", func(v *Thread) { v.UpdatedAt = time.Time{} }},
		{"reversed times", "updated_at", func(v *Thread) { v.UpdatedAt = v.CreatedAt.Add(-time.Nanosecond) }},
		{"nonUTC created", "created_at", func(v *Thread) { v.CreatedAt = v.CreatedAt.In(time.FixedZone("offset", 3600)) }},
		{"nonUTC updated", "updated_at", func(v *Thread) { v.UpdatedAt = v.UpdatedAt.In(time.FixedZone("offset", -3600)) }},
		{"unserializable year", "created_at", func(v *Thread) { v.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := fixture("thread-a")
			tt.change(&v)
			err := Validate(v)
			var typed *ValidationError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &typed) || typed.Field != tt.field {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), "Private title") || strings.Contains(err.Error(), "explicit/workspace") {
				t.Fatal("private data in error")
			}
		})
	}
}

func TestStructuralBoundariesAndNoResolution(t *testing.T) {
	v := fixture(ID(strings.Repeat("a", MaxIDBytes)))
	v.BotID = "not-installed"
	v.Workspace = strings.Repeat("é", MaxWorkspaceBytes/2)
	v.Title = strings.Repeat("é", MaxTitleBytes/2)
	if err := Validate(v); err != nil {
		t.Fatal(err)
	}
	v.Title += "a"
	if err := Validate(v); !errors.Is(err, ErrInvalid) {
		t.Fatal("title limits are bytes")
	}
	v.Title = ""
	for _, workspace := range []string{"/not-existing/fixture", `C:\not-existing\fixture`, "../unresolved", "relative/path", "  exact spelling  "} {
		v.Workspace = workspace
		before := v
		if err := Validate(v); err != nil || v != before {
			t.Fatalf("structural validation mutated/resolved workspace: %v", err)
		}
	}
	v.CreatedAt = v.CreatedAt.In(time.FixedZone("UTC alias", 0))
	if err := Validate(v); err != nil {
		t.Fatal("zero-offset representation should be accepted")
	}
}
