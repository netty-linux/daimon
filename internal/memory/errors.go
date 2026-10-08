package memory

import "errors"

var (
	ErrInvalid   = errors.New("memory: invalid record or query")
	ErrNotFound  = errors.New("memory: not found")
	ErrDuplicate = errors.New("memory: duplicate identity")
	ErrImmutable = errors.New("memory: immutable scope")
	ErrLimit     = errors.New("memory: size or capacity limit")
	ErrFormat    = errors.New("memory: invalid persisted format")
	ErrVersion   = errors.New("memory: unsupported version")
	ErrStore     = errors.New("memory: store unavailable")
	ErrCleanup   = errors.New("memory: cleanup failed")
)

// Public messages never interpolate caller content, paths or private causes.
type StoreError struct {
	Kind  error
	Cause error
}

func (e *StoreError) Error() string   { return e.Kind.Error() }
func (e *StoreError) Unwrap() []error { return []error{e.Kind, e.Cause} }
