//go:build !linux

package createcontract

import "os"

func Supported() bool             { return false }
func singleLink(os.FileInfo) bool { return false }

func sameCreatedFile(os.FileInfo, os.FileInfo) bool { return false }
