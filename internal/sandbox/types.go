// Package sandbox owns disposable environment lifecycle, not Computer semantics.
package sandbox

import (
	"context"
	"regexp"
	"time"

	"github.com/netty-linux/daimon/internal/computer"
)

type ID string
type BackendID string
type Kind string
type Status string
type Placement string

const (
	CUALocal  BackendID = "cua-local"
	CUACloud  BackendID = "cua-cloud"
	Cloud     Placement = "cloud"
	Container Kind      = "container"
	Local     Placement = "local"
	Creating  Status    = "creating"
	Running   Status    = "running"
	Deleting  Status    = "deleting"
	Deleted   Status    = "deleted"
	Failed    Status    = "failed"
)

type ResourceLimits struct {
	CPU             int `json:"cpu"`
	MemoryMiB       int `json:"memory_mib"`
	LifetimeSeconds int `json:"lifetime_seconds"`
}
type ImageSpec struct {
	Alias    string `json:"alias"`
	Resolved string `json:"resolved,omitempty"`
}

// Profile contains only approved aliases/presets, never executable configuration.
type Profile struct {
	Placement Placement `json:"placement,omitempty"`
	Backend   BackendID `json:"backend"`
	Image     string    `json:"image"`
	Runtime   string    `json:"runtime"`
	Browser   bool      `json:"browser"`
	Resources string    `json:"resources"`
	Network   string    `json:"network"`
}
type Info struct {
	ExpiresAt     string         `json:"expires_at,omitempty"`
	ID            ID             `json:"id"`
	Backend       BackendID      `json:"backend"`
	Kind          Kind           `json:"kind"`
	Status        Status         `json:"status"`
	Placement     Placement      `json:"placement"`
	Image         ImageSpec      `json:"image"`
	Runtime       string         `json:"runtime"`
	Browser       bool           `json:"browser"`
	Resources     ResourceLimits `json:"resources"`
	Network       string         `json:"network"`
	OwnerSession  string         `json:"owner_session"`
	ComputerID    string         `json:"computer_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	Cleanup       string         `json:"cleanup"`
	Orphan        bool           `json:"orphan"`
	ErrorCategory ErrorKind      `json:"error_category,omitempty"`
}
type RuntimeInfo struct {
	Backend   BackendID `json:"backend"`
	Runtime   string    `json:"runtime"`
	Available bool      `json:"available"`
	Reason    ErrorKind `json:"reason,omitempty"`
}
type CreateRequest struct {
	Name         string
	Profile      Profile
	Limits       ResourceLimits
	ReadyTimeout time.Duration
}

// Remote is private runtime metadata. Endpoints never enter persisted/public Info.
type Remote struct {
	Ref, Name, Runtime, Image string
	ExpiresAt                 string
	Ready                     bool
}
type Backend interface {
	Create(context.Context, CreateRequest) (Remote, error)
	Get(context.Context, string) (Remote, error)
	Delete(context.Context, string) error
	Close(context.Context) error
}
type ComputerProvider interface {
	Computer(context.Context, Remote, string) (*computer.Manager, error)
}
type RuntimeProbe interface {
	Probe(context.Context) (RuntimeInfo, error)
}

var idPattern = regexp.MustCompile(`^sb-[a-f0-9]{24}$`)
var sessionPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func ValidateID(id ID) error {
	if !idPattern.MatchString(string(id)) {
		return errorOf(Invalid)
	}
	return nil
}
func ownedName(id ID) string  { return "daimon-" + string(id)[3:] }
func computerID(id ID) string { return "sandbox-" + string(id)[3:] }
func ValidTransition(from, to Status) bool {
	switch from {
	case Creating:
		return to == Running || to == Failed || to == Deleting
	case Running:
		return to == Deleting || to == Failed
	case Failed:
		return to == Deleting
	case Deleting:
		return to == Deleted || to == Failed
	}
	return false
}

// EffectivePlacement preserves legacy local profiles, never infers cloud.
func (p Profile) EffectivePlacement() Placement {
	if p.Placement == "" && p.Backend == CUALocal {
		return Local
	}
	return p.Placement
}
func ValidateProfile(p Profile) error {
	valid := p.Backend == CUALocal && p.EffectivePlacement() == Local && (p.Resources == "standard" || p.Resources == "small")
	valid = valid || p.Backend == CUACloud && p.Placement == Cloud && (p.Resources == "small" || p.Resources == "medium")
	if !valid || p.Image != "linux" || p.Runtime != "gvisor" || p.Network != "outbound" {
		return errorOf(Invalid)
	}
	return nil
}
func validExpiry(value string) bool {
	if value == "" {
		return true
	}
	t, e := time.Parse(time.RFC3339, value)
	return e == nil && t.Format(time.RFC3339Nano) == value && t.Location().String() == "UTC"
}
