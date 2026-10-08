package bots

import "errors"

var (
	ErrInvalid   = errors.New("bots: invalid configuration")
	ErrDuplicate = errors.New("bots: duplicate ID")
	ErrNotFound  = errors.New("bots: bot not found")
	ErrStore     = errors.New("bots: store I/O failure")
	ErrFormat    = errors.New("bots: invalid store format")
	ErrVersion   = errors.New("bots: unsupported store version")
	ErrLimit     = errors.New("bots: store limit exceeded")
	ErrCleanup   = errors.New("bots: temporary cleanup failed")
)

// Field is a controlled schema field name, never caller-supplied text.
type ValidationError struct{ Field string }

func (e *ValidationError) Error() string { return "bots: invalid field " + e.Field }
func (*ValidationError) Unwrap() error   { return ErrInvalid }

// Error deliberately excludes path, stored text and underlying error messages.
// Unwrap preserves identity for inspection; callers must not log raw causes.
type StoreError struct {
	Kind  error
	Cause error
}

func (e *StoreError) Error() string {
	switch e.Kind {
	case ErrDuplicate:
		return "bots: duplicate ID"
	case ErrNotFound:
		return "bots: bot not found"
	case ErrFormat:
		return "bots: invalid store format"
	case ErrVersion:
		return "bots: unsupported store version"
	case ErrLimit:
		return "bots: store limit exceeded"
	case ErrCleanup:
		return "bots: temporary cleanup failed"
	default:
		return "bots: store I/O failure"
	}
}
func (e *StoreError) Unwrap() error        { return e.Cause }
func (e *StoreError) Is(target error) bool { return target == e.Kind }
