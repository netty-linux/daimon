package sandbox

import (
	"context"
	"errors"
)

type ErrorKind string

const (
	ExecutableMissing ErrorKind = "cloud_executable_missing"
	SignedOut         ErrorKind = "cloud_signed_out"
	CloudUnavailable  ErrorKind = "cloud_unavailable"
	Invalid           ErrorKind = "invalid_sandbox"
	Unavailable       ErrorKind = "runtime_unavailable"
	NotConfigured     ErrorKind = "not_configured"
	NotFound          ErrorKind = "sandbox_not_found"
	Capacity          ErrorKind = "sandbox_capacity"
	Provision         ErrorKind = "sandbox_provision_failed"
	Cleanup           ErrorKind = "sandbox_cleanup_unresolved"
	Transport         ErrorKind = "sandbox_transport_failed"
	Permission        ErrorKind = "sandbox_permission_denied"
	DiskFull          ErrorKind = "sandbox_disk_full"
	Unsupported       ErrorKind = "sandbox_unsupported"
	Canceled          ErrorKind = "sandbox_canceled"
	Protocol          ErrorKind = "sandbox_invalid_response"
	Persistence       ErrorKind = "sandbox_registry_unavailable"
)

type Error struct {
	Kind       ErrorKind
	Cause      error
	Unresolved []ID
}

func (e *Error) Error() string        { return "sandbox: " + string(e.Kind) }
func (e *Error) Unwrap() error        { return e.Cause }
func (e *Error) Is(target error) bool { t, ok := target.(*Error); return ok && t.Kind == e.Kind }
func errorOf(k ErrorKind) *Error      { return &Error{Kind: k} }
func Category(e error) ErrorKind {
	var s *Error
	if errors.As(e, &s) {
		return s.Kind
	}
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return Canceled
	}
	if e != nil {
		return Provision
	}
	return ""
}
