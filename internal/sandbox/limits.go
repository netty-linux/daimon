package sandbox

import "time"

const MaxRecords = 64
const MaxRegistryBytes = 256 * 1024
const MaxCommandOutput = 256 * 1024
const MaxCommandError = 16 * 1024

type Options struct {
	CreateTimeout, DeleteTimeout, Lifetime time.Duration
	MaxActive                              int
}

func DefaultOptions() Options {
	return Options{CreateTimeout: 120 * time.Second, DeleteTimeout: 15 * time.Second, Lifetime: 15 * time.Minute, MaxActive: 4}
}
func (o Options) Validate() error {
	if o.CreateTimeout <= 0 || o.CreateTimeout > 10*time.Minute || o.DeleteTimeout <= 0 || o.DeleteTimeout > time.Minute || o.Lifetime < time.Second || o.Lifetime > time.Hour || o.MaxActive < 1 || o.MaxActive > 4 {
		return errorOf(Invalid)
	}
	return nil
}
func limits(p Profile, lifetime time.Duration) ResourceLimits {
	r := ResourceLimits{CPU: 2, MemoryMiB: 4096, LifetimeSeconds: int(lifetime / time.Second)}
	if p.EffectivePlacement() == Cloud {
		r.CPU = 2
		r.MemoryMiB = 4096
		if p.Resources == "medium" {
			r.CPU = 4
			r.MemoryMiB = 8192
		}
		return r
	}
	if p.Resources == "small" {
		r.CPU = 1
		r.MemoryMiB = 2048
	}
	return r
}
