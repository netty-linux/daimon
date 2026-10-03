//go:build !linux

package managedworkspace

import "os"

func evidenceRename(*os.File, string, string) error { return ErrUnsupported }
