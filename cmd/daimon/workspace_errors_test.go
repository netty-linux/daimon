package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/createcontract"
	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/policy"
	"github.com/netty-linux/daimon/internal/providers/openai"
	"strings"
	"testing"
)

func TestPublicWorkspaceErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		cause error
		want  string
	}{
		{createcontract.ErrConflict, "já existe"}, {editcontract.ErrChanged, "mudou"},
		{createcontract.ErrLimit, "Limite"}, {context.Canceled, "cancelada"},
		{policy.ErrDisplay, "aprovação integral"}, {editcontract.ErrApproval, "aprovação integral"},
		{createcontract.ErrDenied, "não concedida"},
		{errors.Join(createcontract.ErrCleanup, context.Canceled), "resíduo"},
		{errors.New("edit target changed SECRET /private ID"), "Não foi possível autorizar"},
	} {
		cause := fmt.Errorf("SECRET /private ID: %w", tc.cause)
		original := &agentloop.AuthorizationError{Step: 1, ToolIndex: 1, Cause: cause}
		got := workspaceError(original)
		if !strings.Contains(got.Error(), tc.want) || strings.Contains(got.Error(), "SECRET") || strings.Contains(got.Error(), "/private") || strings.Contains(got.Error(), "ID") {
			t.Fatal(got)
		}
		if !errors.Is(got, tc.cause) {
			t.Fatal("lost cause")
		}
		var auth *agentloop.AuthorizationError
		if !errors.As(got, &auth) || auth != original {
			t.Fatal("lost authorization error")
		}
	}
}

func TestPublicProviderClassificationPreservesCause(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 503, 400} {
		cause := &openai.HTTPError{StatusCode: status, RequestID: "SECRET", Code: "/private", Type: "Authorization: SECRET"}
		modelErr := &agentloop.ModelError{Step: 1, Cause: cause}
		got := workspaceError(modelErr)
		if !errors.Is(got, cause) || !strings.Contains(got.Error(), "rovider") || strings.Contains(got.Error(), "SECRET") || strings.Contains(got.Error(), "/private") {
			t.Fatal("unsafe provider error")
		}
		var preserved *agentloop.ModelError
		if !errors.As(got, &preserved) || preserved.Step != 1 {
			t.Fatal("lost model error")
		}
	}
	if workspaceProviderError(agentloop.ErrInvalidResponse) != "" {
		t.Fatal("local response classified as provider failure")
	}
}
