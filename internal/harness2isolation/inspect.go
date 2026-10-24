// Package harness2isolation inspects prerequisites only. It owns no fixture
// executor, runtime launcher, Session authority or isolation attestation.
package harness2isolation

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrConfig      = errors.New("harness2isolation: invalid configuration")
	ErrInspection  = errors.New("harness2isolation: inspection failed")
	ErrUnsupported = errors.New("harness2isolation: Linux required")
)

// Observations are local metadata, NOT proof of working isolation. In particular
// a controller's presence does not prove delegation or enforcement, and an
// executable file's presence does not authenticate its deployment.
type Observations struct {
	CgroupV2            bool `json:"cgroup_v2"`
	CgroupMountReadOnly bool `json:"cgroup_mount_read_only"`
	CPUController       bool `json:"cpu_controller"`
	MemoryController    bool `json:"memory_controller"`
	PIDsController      bool `json:"pids_controller"`
	NoNewPrivileges     bool `json:"no_new_privileges"`
	SeccompFilter       bool `json:"seccomp_filter"`
	NonRootIdentity     bool `json:"non_root_identity"`
	RuntimeFileObserved bool `json:"runtime_file_observed"`
}

type Snapshot struct{ observations Observations }

func (Snapshot) String() string               { return "harness2isolation: prerequisite observations only" }
func (s Snapshot) GoString() string           { return s.String() }
func (s Snapshot) Observations() Observations { return s.observations }

// JSON is bounded and contains no paths, environment values, credentials,
// process IDs, runtime hashes or claim that any of the C1 gates is verified.
func (s Snapshot) JSON() []byte {
	data, _ := json.Marshal(struct {
		Version      int          `json:"version"`
		Status       string       `json:"fixture_status"`
		Reason       string       `json:"reason"`
		Observations Observations `json:"observations"`
	}{1, "NOT_RUN", "validated-supervisor-unavailable", s.observations})
	return data
}

// Inspect reads fixed kernel/deployment metadata. No caller can choose a path,
// executable, command, mount, policy, credentials or quota. There is no execution
// method: an inspection success must never be interpreted as admission.
func Inspect(ctx context.Context) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, ErrConfig
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	o, err := inspectPlatform(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{observations: o}, nil
}
