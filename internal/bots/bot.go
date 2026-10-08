// Package bots defines reusable configuration, independent of agent execution.
package bots

import (
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sandbox"
)

type ID string
type PermissionMode string

const (
	PermissionAsk      PermissionMode = "ask"
	PermissionReadOnly PermissionMode = "read-only"
)

// Bot contains no credentials or transport configuration. Text fields are private
// configuration: callers must not put secrets in them or log complete Bots.
// Tools and PermissionMode are declarations, never authorization decisions.
type Bot struct {
	SandboxProfile  *sandbox.Profile  `json:"sandbox_profile,omitempty"`
	ComputerProfile *computer.Profile `json:"computer_profile,omitempty"`
	ID              ID                `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description,omitempty"`
	Instructions    string            `json:"instructions"`
	ProviderID      providers.ID      `json:"provider_id"`
	Model           string            `json:"model"`
	Tools           []string          `json:"tools"`
	PermissionMode  PermissionMode    `json:"permission_mode"`
}

// Clone preserves declaration order and the distinction between nil and empty.
func Clone(b Bot) Bot {
	if b.Tools != nil {
		b.Tools = append([]string{}, b.Tools...)
	}
	if b.ComputerProfile != nil {
		copy := *b.ComputerProfile
		b.ComputerProfile = &copy
	}
	if b.SandboxProfile != nil {
		copy := *b.SandboxProfile
		b.SandboxProfile = &copy
	}
	return b
}
