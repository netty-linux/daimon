package bots

import (
	"strings"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sandbox"
)

const (
	MaxIDBytes           = 64
	MaxNameBytes         = 256
	MaxDescriptionBytes  = 2048
	MaxInstructionsBytes = 8192
	MaxModelBytes        = 256
	MaxTools             = 32
	MaxToolNameBytes     = 64
)

// Validate is structural only. Provider/tool availability and authority are
// resolved by future assembly, not by this domain. Empty Tools is valid;
// nil is rejected so absence cannot become an implicit default capability set.
func Validate(b Bot) error {
	if b.SandboxProfile != nil && (sandbox.ValidateProfile(*b.SandboxProfile) != nil || b.ComputerProfile != nil && b.ComputerProfile.Enabled) {
		return &ValidationError{Field: "sandbox_profile"}
	}
	if b.ComputerProfile != nil && (computer.ValidateProfile(*b.ComputerProfile) != nil || b.ComputerProfile.Backend != computer.CUALocal) {
		return &ValidationError{Field: "computer_profile"}
	}
	if providers.ValidateID(providers.ID(b.ID)) != nil {
		return &ValidationError{Field: "id"}
	}
	if !text(b.Name, MaxNameBytes, true) {
		return &ValidationError{Field: "name"}
	}
	if !text(b.Description, MaxDescriptionBytes, false) {
		return &ValidationError{Field: "description"}
	}
	if !text(b.Instructions, MaxInstructionsBytes, true) {
		return &ValidationError{Field: "instructions"}
	}
	if providers.ValidateID(b.ProviderID) != nil {
		return &ValidationError{Field: "provider_id"}
	}
	if !text(b.Model, MaxModelBytes, true) {
		return &ValidationError{Field: "model"}
	}
	if b.Tools == nil || len(b.Tools) > MaxTools {
		return &ValidationError{Field: "tools"}
	}
	seen := make(map[string]bool, len(b.Tools))
	for _, name := range b.Tools {
		if !toolName(name) || seen[name] {
			return &ValidationError{Field: "tools"}
		}
		seen[name] = true
	}
	if b.PermissionMode != PermissionAsk && b.PermissionMode != PermissionReadOnly {
		return &ValidationError{Field: "permission_mode"}
	}
	return nil
}

func text(s string, max int, required bool) bool {
	return len(s) <= max && utf8.ValidString(s) && (!required || strings.TrimSpace(s) != "")
}

func toolName(s string) bool {
	if len(s) == 0 || len(s) > MaxToolNameBytes {
		return false
	}
	for i, c := range []byte(s) {
		if c >= 'a' && c <= 'z' {
			continue
		}
		if i > 0 && (c >= '0' && c <= '9' || c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}
