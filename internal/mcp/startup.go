package mcp

import (
	"context"
	"errors"
)

// startupReason uses existing error identities and the local failure phase only.
// ErrLimit during discovery is ambiguous (frame/schema/description/count) and
// remains unknown. No message is inspected or exposed.
func startupReason(err error, phase string) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, ErrProtocol) {
		return "protocol_error"
	}
	if phase == "environment" && errors.Is(err, ErrConfig) {
		return "environment_failed"
	}
	if phase == "tool_count" && errors.Is(err, ErrLimit) {
		return "tool_limit_exceeded"
	}
	if errors.Is(err, ErrRemote) || errors.Is(err, ErrUnsupported) {
		if phase == "handshake" {
			return "handshake_failed"
		}
		if phase == "discovery" {
			return "discovery_failed"
		}
	}
	return "unknown"
}
