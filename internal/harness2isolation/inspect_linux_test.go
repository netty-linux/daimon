//go:build linux

package harness2isolation

import (
	"context"
	"errors"
	"testing"
)

func TestKernelMetadataParser(t *testing.T) {
	valid := []byte("Name:\tprivate\nUid:\t65534 65534 65534 65534\nNoNewPrivs:\t1\nSeccomp:\t2\n")
	o, err := parseStatus(valid)
	if err != nil || !o.NonRootIdentity || !o.NoNewPrivileges || !o.SeccompFilter {
		t.Fatal("metadata parse", err)
	}
	for _, bad := range []string{"", "Uid: 1 1 1 1\n", "Uid: x 1 1 1\nNoNewPrivs: 1\nSeccomp: 2", "Uid: 1 1 1 1\nNoNewPrivs: 9\nSeccomp: 2", string(valid) + "Uid: 0 0 0 0\n"} {
		if _, err := parseStatus([]byte(bad)); !errors.Is(err, ErrInspection) {
			t.Fatal("invalid metadata accepted")
		}
	}
}

func TestRealKernelInspection(t *testing.T) {
	s, err := Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Actual procfs/sysfs observations, not evidence of quota enforcement.
	t.Log(string(s.JSON()))
}
