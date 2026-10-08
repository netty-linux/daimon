package conversations

import "errors"

var (
	ErrInvalid   = errors.New("conversation: invalid message")
	ErrDuplicate = errors.New("conversation: duplicate identity")
	ErrLimit     = errors.New("conversation: limit exceeded")
	ErrFormat    = errors.New("conversation: invalid format")
	ErrVersion   = errors.New("conversation: unsupported version")
	ErrStore     = errors.New("conversation: persistence failure")
	ErrCleanup   = errors.New("conversation: cleanup failure")
)

type StoreError struct {
	Kind  error
	Cause error
}

func (e *StoreError) Error() string        { return "conversation: store operation failed" }
func (e *StoreError) Unwrap() error        { return e.Cause }
func (e *StoreError) Is(target error) bool { return target == e.Kind }
