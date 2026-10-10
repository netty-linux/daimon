package computer

import "testing"

func TestSafeStartupReason(t *testing.T) {
	for _, value := range []string{"environment_failed", "handshake_failed", "discovery_failed", "discovery_too_large", "tool_limit_exceeded", "timeout", "cancelled", "protocol_error", "unknown"} {
		if safeStartupReason(value) != value {
			t.Fatal(value)
		}
	}
	for _, secret := range []string{`C:\private\driver`, "https://secret.invalid", "CUA_DRIVER_TOKEN=secret", "stderr secret"} {
		if safeStartupReason(secret) != "unknown" {
			t.Fatal("private metadata exposed")
		}
	}
}
