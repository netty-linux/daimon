//go:build linux

package harness2isolation

import (
	"context"
	"io"
	"os"
	"strings"
	"syscall"
)

const maxMetadataBytes = 64 * 1024

func metadata(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrInspection
	}
	b, readErr := io.ReadAll(io.LimitReader(f, maxMetadataBytes+1))
	closeErr := f.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if readErr != nil || closeErr != nil || len(b) > maxMetadataBytes {
		return nil, ErrInspection
	}
	return b, nil
}

func parseStatus(data []byte) (Observations, error) {
	var o Observations
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, _ := strings.Cut(line, ":")
		if key != "Uid" && key != "NoNewPrivs" && key != "Seccomp" {
			continue
		}
		if seen[key] {
			return Observations{}, ErrInspection
		}
		seen[key] = true
		values := strings.Fields(value)
		switch key {
		case "Uid":
			if len(values) != 4 {
				return Observations{}, ErrInspection
			}
			o.NonRootIdentity = true
			for _, v := range values {
				if len(v) == 0 {
					return Observations{}, ErrInspection
				}
				for _, c := range v {
					if c < '0' || c > '9' {
						return Observations{}, ErrInspection
					}
				}
				if strings.TrimLeft(v, "0") == "" {
					o.NonRootIdentity = false
				}
			}
		case "NoNewPrivs":
			if len(values) != 1 || (values[0] != "0" && values[0] != "1") {
				return Observations{}, ErrInspection
			}
			o.NoNewPrivileges = values[0] == "1"
		case "Seccomp":
			if len(values) != 1 || (values[0] != "0" && values[0] != "1" && values[0] != "2") {
				return Observations{}, ErrInspection
			}
			o.SeccompFilter = values[0] == "2"
		}
	}
	if len(seen) != 3 {
		return Observations{}, ErrInspection
	}
	return o, nil
}

func inspectPlatform(ctx context.Context) (Observations, error) {
	status, err := metadata(ctx, "/proc/self/status")
	if err != nil {
		return Observations{}, err
	}
	o, err := parseStatus(status)
	if err != nil {
		return Observations{}, err
	}
	var fs syscall.Statfs_t
	if syscall.Statfs("/sys/fs/cgroup", &fs) != nil {
		return Observations{}, ErrInspection
	}
	o.CgroupV2 = fs.Type == 0x63677270      // CGROUP2_SUPER_MAGIC, not enforcement.
	o.CgroupMountReadOnly = fs.Flags&1 != 0 // ST_RDONLY; false does NOT prove delegation.
	if o.CgroupV2 {
		controllers, err := metadata(ctx, "/sys/fs/cgroup/cgroup.controllers")
		if err != nil {
			return Observations{}, err
		}
		for _, c := range strings.Fields(string(controllers)) {
			switch c {
			case "cpu":
				o.CPUController = true
			case "memory":
				o.MemoryController = true
			case "pids":
				o.PIDsController = true
			}
		}
	}
	for _, p := range []string{"/usr/local/bin/runsc", "/usr/bin/runsc"} {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return Observations{}, ErrInspection
		}
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			o.RuntimeFileObserved = true
		}
	}
	return o, nil
}
