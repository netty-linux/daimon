package openai

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err   error
		class FailureClass
	}{
		{&ConfigError{Field: "BaseURL", Missing: true}, FailureMissingConfiguration},
		{&ConfigError{Field: "BaseURL"}, FailureURL}, {&ConfigError{Field: "Model"}, FailureConfiguration},
		{&TransportError{cause: x509.UnknownAuthorityError{}}, FailureTLS},
		{&TransportError{cause: &net.DNSError{IsTimeout: true}}, FailureTimeout},
		{&TransportError{cause: errors.New("SECRET /private")}, FailureTransport},
		{context.Canceled, FailureCanceled}, {context.DeadlineExceeded, FailureTimeout},
		{&HTTPError{StatusCode: 401}, FailureAuthentication}, {&HTTPError{StatusCode: 403}, FailureAuthentication},
		{&HTTPError{StatusCode: 404}, FailureNotFound}, {&HTTPError{StatusCode: 429}, FailureRateLimit},
		{&HTTPError{StatusCode: 503}, FailureServer}, {&HTTPError{StatusCode: 400}, FailureHTTP},
		{&ProtocolError{Reason: "SECRET"}, FailurePayload}, {&JSONError{}, FailurePayload},
		{&RequestError{Reason: "SECRET"}, FailurePayload}, {&ResponseTooLargeError{}, FailurePayload},
		{errors.New("x509: SECRET timeout 429"), FailureUnknown},
	} {
		wrapped := fmt.Errorf("SECRET: %w", tc.err)
		if Classify(wrapped) != tc.class || !errors.Is(wrapped, tc.err) {
			t.Fatalf("classification mismatch: want %s", tc.class)
		}
	}
}
