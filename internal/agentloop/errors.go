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

type ModelError struct {
	Step  int
	Cause error
}

func (e *ModelError) Error() string {
	return fmt.Sprintf("model failed at step %d: %v", e.Step, e.Cause)
}
func (e *ModelError) Unwrap() error { return e.Cause }
