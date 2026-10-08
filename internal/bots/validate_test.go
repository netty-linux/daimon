package bots

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/providers"
)

func fixture(id ID) Bot {
	return Bot{ID: id, Name: "Daimon Coder", Instructions: "Private guidance", ProviderID: providers.Groq,
		Model: "openai/gpt-oss-20b", Tools: []string{"read_file", "list_dir"}, PermissionMode: PermissionAsk}
}

func TestValidate(t *testing.T) {
	if err := Validate(fixture("coder")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, field string
		mutate      func(*Bot)
	}{
		{"empty ID", "id", func(b *Bot) { b.ID = "" }},
		{"blank ID", "id", func(b *Bot) { b.ID = " " }},
		{"malformed ID", "id", func(b *Bot) { b.ID = "../Coder" }},
		{"large ID", "id", func(b *Bot) { b.ID = ID(strings.Repeat("a", MaxIDBytes+1)) }},
		{"empty name", "name", func(b *Bot) { b.Name = " \n" }},
		{"large name", "name", func(b *Bot) { b.Name = strings.Repeat("a", MaxNameBytes+1) }},
		{"name UTF8", "name", func(b *Bot) { b.Name = "\xff" }},
		{"large description", "description", func(b *Bot) { b.Description = strings.Repeat("a", MaxDescriptionBytes+1) }},
		{"description UTF8", "description", func(b *Bot) { b.Description = "\xff" }},
		{"empty instructions", "instructions", func(b *Bot) { b.Instructions = "\t" }},
		{"large instructions", "instructions", func(b *Bot) { b.Instructions = strings.Repeat("a", MaxInstructionsBytes+1) }},
		{"instructions UTF8", "instructions", func(b *Bot) { b.Instructions = "\xff" }},
		{"empty provider", "provider_id", func(b *Bot) { b.ProviderID = "" }},
		{"invalid provider", "provider_id", func(b *Bot) { b.ProviderID = "Groq" }},
		{"large provider", "provider_id", func(b *Bot) { b.ProviderID = providers.ID(strings.Repeat("a", 65)) }},
		{"empty model", "model", func(b *Bot) { b.Model = " " }},
		{"large model", "model", func(b *Bot) { b.Model = strings.Repeat("a", MaxModelBytes+1) }},
		{"model UTF8", "model", func(b *Bot) { b.Model = "\xff" }},
		{"absent tools", "tools", func(b *Bot) { b.Tools = nil }},
		{"duplicate tools", "tools", func(b *Bot) { b.Tools = []string{"echo", "echo"} }},
		{"wildcard", "tools", func(b *Bot) { b.Tools = []string{"*"} }},
		{"malformed tool", "tools", func(b *Bot) { b.Tools = []string{"read file"} }},
		{"empty tool", "tools", func(b *Bot) { b.Tools = []string{""} }},
		{"large tool", "tools", func(b *Bot) { b.Tools = []string{strings.Repeat("a", MaxToolNameBytes+1)} }},
		{"tool UTF8", "tools", func(b *Bot) { b.Tools = []string{"é"} }},
		{"many tools", "tools", func(b *Bot) { b.Tools = make([]string, MaxTools+1) }},
		{"zero permission", "permission_mode", func(b *Bot) { b.PermissionMode = "" }},
		{"unknown permission", "permission_mode", func(b *Bot) { b.PermissionMode = "yolo" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := fixture("coder")
			tt.mutate(&b)
			err := Validate(b)
			var ve *ValidationError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &ve) || ve.Field != tt.field {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), "Private guidance") {
				t.Fatal("private content in error")
			}
		})
	}
}

func TestValidationBoundariesAndDeclarations(t *testing.T) {
	b := fixture(ID(strings.Repeat("a", MaxIDBytes)))
	b.Name = strings.Repeat("é", MaxNameBytes/2)
	b.Description = strings.Repeat("a", MaxDescriptionBytes)
	b.Instructions = strings.Repeat("a", MaxInstructionsBytes)
	b.Model = strings.Repeat("a", MaxModelBytes)
	b.ProviderID = "not-installed"
	b.Tools = []string{}
	b.PermissionMode = PermissionReadOnly
	if err := Validate(b); err != nil {
		t.Fatal(err)
	}
	b.Name += "é"
	if err := Validate(b); !errors.Is(err, ErrInvalid) {
		t.Fatal("limits must count bytes")
	}
	b.Name = "valid"
	for i := 0; i < MaxTools; i++ {
		b.Tools = append(b.Tools, fmt.Sprintf("future_tool_%d", i))
	}
	if err := Validate(b); err != nil {
		t.Fatal(err)
	}
	before := append([]string{}, b.Tools...)
	if err := Validate(b); err != nil {
		t.Fatal(err)
	}
	if strings.Join(before, ",") != strings.Join(b.Tools, ",") {
		t.Fatal("validation reordered tools")
	}
}

func TestClone(t *testing.T) {
	b := fixture("coder")
	c := Clone(b)
	c.Tools[0] = "changed"
	if b.Tools[0] != "read_file" {
		t.Fatal("shared tools")
	}
	b.Tools = []string{}
	if Clone(b).Tools == nil {
		t.Fatal("empty tools became absent")
	}
}
