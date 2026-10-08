package openai

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
)

// FailureClass contains controlled categories only, never remote metadata.
type FailureClass string

const (
	FailureUnknown              FailureClass = "unknown"
	FailureConfiguration        FailureClass = "configuration"
	FailureMissingConfiguration FailureClass = "configuration_missing"
	FailureURL                  FailureClass = "invalid_url"
	FailureTLS                  FailureClass = "tls"
	FailureTimeout              FailureClass = "timeout"
	FailureCanceled             FailureClass = "canceled"
	FailureTransport            FailureClass = "transport"
	FailureAuthentication       FailureClass = "authentication"
	FailureNotFound             FailureClass = "endpoint_or_model_not_found"
	FailureRateLimit            FailureClass = "rate_limit"
	FailureServer               FailureClass = "server"
	FailureHTTP                 FailureClass = "http"
	FailurePayload              FailureClass = "incompatible_payload"
)

// Classify follows typed causes. It neither mutates the error chain nor infers
// a model failure from arbitrary error text or a server's free-form metadata.
func Classify(err error) FailureClass {
	if err == nil {
		return FailureUnknown
	}
	if errors.Is(err, context.Canceled) {
		return FailureCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureTimeout
	}
	var cfg *ConfigError
	if errors.As(err, &cfg) {
		if cfg.Missing {
			return FailureMissingConfiguration
		}
		if cfg.Field == "BaseURL" {
			return FailureURL
		}
		return FailureConfiguration
	}
	var transport *TransportError
	if errors.As(err, &transport) {
		var unknown x509.UnknownAuthorityError
		var invalid x509.CertificateInvalidError
		var host x509.HostnameError
		var tlsVerify *tls.CertificateVerificationError
		var record tls.RecordHeaderError
		if errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &host) || errors.As(err, &tlsVerify) || errors.As(err, &record) {
			return FailureTLS
		}
		var timed net.Error
		if errors.As(err, &timed) && timed.Timeout() {
			return FailureTimeout
		}
		return FailureTransport
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.StatusCode == 401 || httpErr.StatusCode == 403:
			return FailureAuthentication
		case httpErr.StatusCode == 404:
			return FailureNotFound
		case httpErr.StatusCode == 429:
			return FailureRateLimit
		case httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599:
			return FailureServer
		default:
			return FailureHTTP
		}
	}
	var request *RequestError
	var protocol *ProtocolError
	var jsonErr *JSONError
	var size *ResponseTooLargeError
	if errors.As(err, &request) || errors.As(err, &protocol) || errors.As(err, &jsonErr) || errors.As(err, &size) {
		return FailurePayload
	}
	return FailureUnknown
}
