package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/providers/openai"
)

// Only local, typed event counts and controlled categories are deliberately
// displayed. Prompts, tool results, paths, remote IDs and headers are excluded.
func printWorkspaceDiagnostic(out io.Writer, enabled, recognized bool, events []agentloop.Event, cause error) error {
	count := map[agentloop.EventKind]int{}
	for _, e := range events {
		count[e.Kind]++
	}
	reason := "disabled"
	if enabled {
		reason = "unsupported_pattern"
		if recognized {
			reason = "matched"
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Diagnóstico opt-in (sem prompt, conteúdo ou credenciais):\nvalidate_scope_enabled=%t\nvalidate_scope_template_recognized=%t\nvalidate_scope_match_reason=%s\nprovider_failure_class=%s\n", enabled, recognized, reason, openai.Classify(cause))
	for _, kind := range []agentloop.EventKind{agentloop.ModelRequested, agentloop.ApprovalRequested, agentloop.ApprovalGranted, agentloop.ApprovalDenied, agentloop.FinalValidationRequested, agentloop.FinalValidationAccepted, agentloop.FinalValidationRejected, agentloop.RecoveryRequested, agentloop.RecoveryModelRequested} {
		fmt.Fprintf(&b, "%s=%d\n", kind, count[kind])
	}
	text := b.String()
	n, err := io.WriteString(out, text)
	if err != nil || n != len(text) {
		return errors.New("não foi possível exibir o diagnóstico")
	}
	return nil
}
