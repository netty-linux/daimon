package openai

import "fmt"

// Diagnostics contain only controlled text, never configuration values or bodies.
type ConfigError struct {
	Field   string
	Missing bool
}

func (e *ConfigError) Error() string {
	return "openai-compatible: invalid configuration field " + e.Field
}

type RequestError struct{ Reason string }

func (e *RequestError) Error() string { return "openai-compatible: invalid request: " + e.Reason }

// Error deliberately omits cause text. Unwrap preserves network/context identity
// for errors.Is/As; callers must not log the raw cause, which may contain a URL.
type TransportError struct{ cause error }

func (*TransportError) Error() string   { return "openai-compatible: HTTP transport failed" }
func (e *TransportError) Unwrap() error { return e.cause }

type HTTPError struct {
	StatusCode int
	RequestID  string
	Code       string
	Type       string
	RetryAfter string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("openai-compatible: HTTP status %d", e.StatusCode)
}

type ResponseTooLargeError struct {
	Limit      int64
	StatusCode int
}

func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("openai-compatible: response exceeds %d bytes", e.Limit)
}

type JSONError struct{}

func (*JSONError) Error() string { return "openai-compatible: invalid response JSON" }

type ProtocolError struct{ Reason string }

func (e *ProtocolError) Error() string {
	return "openai-compatible: invalid response protocol: " + e.Reason
}
