//go:build !linux

package harness2isolation

import "context"

func inspectPlatform(context.Context) (Observations, error) {
	return Observations{}, ErrUnsupported
}
