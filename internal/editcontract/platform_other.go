//go:build !linux

package editcontract

import "os"

func replacementSupported() bool                 { return false }
func samePlatformMetadata(a, b os.FileInfo) bool { return true }

// Read-only preparation is portable. Apply is disabled before any write on
// platforms without validated link-count and atomic-rename support.
func checkLinks(os.FileInfo) error { return nil }
