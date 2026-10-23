package harness2substrate

import "context"

// Gate declarations are requirements, NOT attestations. No caller-provided bool,
// unit-test result or fixture evidence can enable execution through this API.
type Gate struct {
	Requirement string
	State       string
}

func SecurityGates() []Gate {
	return []Gate{
		{"filesystem-isolation", "unverified"}, {"network-egress-policy", "unverified"},
		{"credential-separation", "unverified"}, {"interpreter-integrity", "unverified"},
		{"worker-integrity", "unverified"}, {"dependency-integrity", "unverified"},
		{"deployment-integrity", "unverified"}, {"process-tree-ownership", "unverified"},
		{"cpu-quota", "unverified"}, {"memory-quota", "unverified"},
		{"disk-quota", "unverified"}, {"time-quota", "unverified"},
		{"process-quota", "unverified"}, {"private-workspace", "unverified"},
		{"private-transcript", "unverified"}, {"supervision", "unverified"},
		{"cancellation-cleanup", "unverified"}, {"failure-recovery", "unverified"},
	}
}

// RequireRealExecution always refuses in C1, including after successful probes.
// Future OS evidence requires a separate reviewed authority, not an unlock flag.
func RequireRealExecution(ctx context.Context) error {
	if err := check(ctx); err != nil {
		return err
	}
	return ErrUnavailable
}
