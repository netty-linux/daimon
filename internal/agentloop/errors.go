package agentloop

import (
	"errors"
	"fmt"
)

var (
	ErrMaxSteps        = errors.New("maximum model steps reached")
	ErrInvalidResponse = errors.New("invalid model response")
	ErrInvalidConfig   = errors.New("invalid loop configuration")
)

type InvalidConfigError struct{ Err error }

func (e InvalidConfigError) Error() string        { return fmt.Sprintf("%v: %v", ErrInvalidConfig, e.Err) }
func (e InvalidConfigError) Unwrap() error        { return e.Err }
func (e InvalidConfigError) Is(target error) bool { return target == ErrInvalidConfig }

type ModelError struct {
	Step  int
	Cause error
}

func (e *ModelError) Error() string {
	return fmt.Sprintf("model failed at step %d: %v", e.Step, e.Cause)
}
func (e *ModelError) Unwrap() error { return e.Cause }

// The cause is never included in the message: it may hold sensitive detail.
type AuthorizationError struct {
	Step      int
	ToolIndex int
	Cause     error
}

func (e *AuthorizationError) Error() string {
	return fmt.Sprintf("authorization failed at step %d tool %d", e.Step, e.ToolIndex)
}
func (e *AuthorizationError) Unwrap() error { return e.Cause }

// A tool may report its own deadline before the supplied context expires.
// Keep that cause distinct from a caller deadline or a budget expiration.
type toolDeadlineError struct{ cause error }

func (e *toolDeadlineError) Error() string { return e.cause.Error() }
func (e *toolDeadlineError) Unwrap() error { return e.cause }
