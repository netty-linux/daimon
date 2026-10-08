// Package computer owns local computer capability admission and control leases.
// It does not own policy, human approval, agent execution, or model protocols.
package computer

import (
	"errors"
	"regexp"
)

type BackendID string

const CUALocal BackendID = "cua-local"
const CUACloud BackendID = "cua-cloud"

type Profile struct {
	Enabled     bool      `json:"enabled"`
	Backend     BackendID `json:"backend"`
	MCPServerID string    `json:"mcp_server_id"`
}

var serverID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var sessionID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func ValidateProfile(p Profile) error {
	if (p.Backend != CUALocal && (p.Backend != CUACloud || p.MCPServerID != "sandbox")) || !serverID.MatchString(p.MCPServerID) {
		return ErrConfig
	}
	return nil
}

type Class string

const (
	Observe   Class = "observe"
	Navigate  Class = "navigate"
	Input     Class = "input"
	System    Class = "system"
	Dangerous Class = "dangerous"
)

type Capability struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Class     Class  `json:"class"`
	Available bool   `json:"available"`
}
type Info struct {
	ID                  string       `json:"id"`
	Backend             BackendID    `json:"backend"`
	Status              string       `json:"status"`
	Capabilities        []Capability `json:"capabilities"`
	Busy                bool         `json:"busy"`
	ControllerSessionID string       `json:"controller_session_id,omitempty"`
}
type Metadata struct {
	ID            string    `json:"id"`
	Backend       BackendID `json:"backend"`
	CapabilityIDs []string  `json:"capability_ids"`
}

var (
	ErrConfig      = errors.New("computer: invalid configuration")
	ErrUnavailable = errors.New("computer: unavailable")
	ErrDenied      = errors.New("computer: capability denied")
	ErrBusy        = errors.New("computer: computer_busy")
	ErrClosed      = errors.New("computer: binding closed")
	ErrArguments   = errors.New("computer: invalid action arguments")
	ErrUnsupported = errors.New("computer: unsupported observation")
)
