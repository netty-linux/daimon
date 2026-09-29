package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/policy"
	"github.com/netty-linux/daimon/internal/providers/openai"
	"github.com/netty-linux/daimon/internal/tools"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := runWithContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	return runWithContext(context.Background(), args, strings.NewReader(""), out, io.Discard, os.Getenv)
}

const maxResponseBytes int64 = 2 * 1024 * 1024

// runWithContext is the composition root: stdin, stdout, stderr and the
// environment arrive injected so approval prompts and terminal answers are
// testable without touching real terminals.
func runWithContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) (err error) {
	sink := &agentloop.MemoryEventSink{}
	var selected model.Model
	var authorizer agentloop.ToolAuthorizer
	var message, apiKey string
	demo := len(args) == 1 && args[0] == "demo"
	switch {
	case demo:
		authorizer = agentloop.AllowAllAuthorizer{}
		selected = model.NewScripted(
			model.ScriptStep{Response: model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "echo-1", Name: "echo", Arguments: []byte(`{"text":"DAIMON"}`)}}}},
			model.ScriptStep{Response: model.ModelResponse{FinalText: "DAIMON"}},
		)
		message = "repita DAIMON"
	case len(args) == 2 && args[0] == "chat" && strings.TrimSpace(args[1]) != "":
		apiKey = getenv("DAIMON_API_KEY")
		// Redact even provider-echoed secrets or downstream writer errors.
		defer func() {
			if err != nil && apiKey != "" {
				err = &outputError{message: strings.ReplaceAll(err.Error(), apiKey, "[REDACTED]"), cause: err}
			}
		}()
		selected, err = openai.New(openai.Config{
			BaseURL: getenv("DAIMON_BASE_URL"), Model: getenv("DAIMON_MODEL"),
			APIKey: apiKey, MaxResponseBytes: maxResponseBytes,
		})
		if err != nil {
			return err
		}
		authorizer = &policy.Authorizer{
			Policy:    policy.DefaultCLIPolicy(),
			Approvals: policy.NewTerminalApproval(stdin, stderr),
			Sink:      sink,
		}
		message = args[1]
	default:
		return fmt.Errorf("usage: daimon demo | daimon chat \"mensagem\"")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, err := tools.NewReadFile(".", 64*1024)
	if err != nil {
		return err
	}
	defer reader.Close()
	lister, err := tools.NewListDir(".", 256, 32*1024)
	if err != nil {
		return err
	}
	defer lister.Close()
	registry := &tools.Registry{}
	for _, tool := range []tools.Tool{tools.Echo{}, lister, reader} {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	loop := agentloop.Loop{Model: selected, Registry: registry, Budget: agentloop.DefaultBudget(), Sink: sink, Authorizer: authorizer}
	result, err := loop.Run(ctx, message)
	if err != nil {
		return err
	}
	answer := result.FinalAnswer
	if apiKey != "" {
		answer = strings.ReplaceAll(answer, apiKey, "[REDACTED]")
	}
	if _, err := fmt.Fprintf(stdout, "Resposta final: %s\nPassos do modelo: %d\nTool calls: %d\nResultados truncados: %d\nStop reason: %s\n", answer, result.Steps, result.ToolCalls, result.TruncatedToolResults, result.StopReason); err != nil {
		return err
	}
	if !demo {
		return nil
	}
	if _, err := fmt.Fprintln(stdout, "Eventos:"); err != nil {
		return err
	}
	for _, event := range sink.Events() {
		if _, err := fmt.Fprintf(stdout, "  %s (step=%d, tool=%d, stop_reason=%s)\n", event.Kind, event.Step, event.ToolIndex, event.StopReason); err != nil {
			return err
		}
	}
	return nil
}

type outputError struct {
	message string
	cause   error
}

func (e *outputError) Error() string { return e.message }
func (e *outputError) Unwrap() error { return e.cause }
