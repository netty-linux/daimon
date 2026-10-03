//go:build !linux

package managedworkspace

import "os"

func supported() bool                                   { return false }
func openRead(*os.Root, string, bool) (*os.File, error) { return nil, ErrUnsupported }
func identityInfo(os.FileInfo) Identity                 { return Identity{} }
func identity(string) (Identity, error)                 { return Identity{}, ErrUnsupported }
func safeRegular(os.FileInfo) bool                      { return false }
func sourceRegular(os.FileInfo) bool                    { return false }
func checkAncestors(string) error                       { return ErrUnsupported }
func checkPrivate(string) error                         { return ErrUnsupported }
func checkLocalPath(string) error                       { return ErrUnsupported }
