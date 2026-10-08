package threads

import "errors"

var (
	ErrInvalid   = errors.New("threads: invalid configuration")
	ErrDuplicate = errors.New("threads: duplicate ID")
	ErrNotFound  = errors.New("threads: thread not found")
	ErrStore     = errors.New("threads: store I/O failure")
	ErrFormat    = errors.New("threads: invalid store format")
	ErrVersion   = errors.New("threads: unsupported store version")
	ErrLimit     = errors.New("threads: store limit exceeded")
	ErrCleanup   = errors.New("threads: temporary cleanup failed")
	ErrImmutable = errors.New("threads: immutable association or creation time")
)

// Field is a controlled schema field name, never caller-supplied text.
type ValidationError struct{ Field string }

func (e *ValidationError) Error() string { return "threads: invalid field " + e.Field }
func (*ValidationError) Unwrap() error   { return ErrInvalid }

// Error deliberately excludes path, stored text and underlying error messages.
// Unwrap preserves identity for inspection; callers must not log raw causes.
type StoreError struct {
	Kind  error
	Cause error
}

func (e *StoreError) Error() string {
	switch e.Kind {
	case ErrImmutable:
		return "threads: immutable association or creation time"
	case ErrDuplicate:
		return "threads: duplicate ID"
	case ErrNotFound:
		return "threads: thread not found"
	case ErrFormat:
		return "threads: invalid store format"
	case ErrVersion:
		return "threads: unsupported store version"
	case ErrLimit:
		return "threads: store limit exceeded"
	case ErrCleanup:
		return "threads: temporary cleanup failed"
	default:
		return "threads: store I/O failure"
	}
}
func (e *StoreError) Unwrap() error        { return e.Cause }
func (e *StoreError) Is(target error) bool { return target == e.Kind }
