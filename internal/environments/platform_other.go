//go:build !linux

package environments

import "os"

func Supported() bool                     { return false }
func privateDirectory(i os.FileInfo) bool { return false }
func regular(i os.FileInfo) bool          { return false }
