package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/tools"
)

var errWorkspace = errors.New("invalid workspace arguments or directory")

func workspaceArguments(args []string) (string, []string, error) {
	if len(args) < 4 || args[1] != "--root" || strings.TrimSpace(args[2]) == "" {
		return "", nil, errWorkspace
	}
	root, tail := args[2], args[3:]
	var chat []string
	if tail[0] == "plan" {
		var message string
		validated, haveMessage := false, false
		for _, arg := range tail[1:] {
			if arg == "--validate-scope" {
				if validated {
					return "", nil, errWorkspace
				}
				validated = true
			} else {
				if haveMessage || strings.TrimSpace(arg) == "" || strings.HasPrefix(arg, "-") {
					return "", nil, errWorkspace
				}
				message, haveMessage = arg, true
			}
		}
		if !haveMessage {
			return "", nil, errWorkspace
		}
		chat = []string{"chat", "plan"}
		if validated {
			chat = append(chat, "--validate-scope")
		}
		chat = append(chat, message)
	} else if len(tail) == 2 && tail[0] == "--enable-replace-file" {
		if tail[1] == "plan" {
			return "", nil, errWorkspace
		}
		chat = []string{"chat", tail[0], tail[1]}
	} else if len(tail) == 2 && tail[0] == "--enable-create-file" {
		if tail[1] == "plan" {
			return "", nil, errWorkspace
		}
		chat = []string{"chat", "--workspace-create-file", tail[1]}
	} else if len(tail) == 1 {
		if tail[0] == "plan" {
			return "", nil, errWorkspace
		}
		chat = []string{"chat", tail[0]}
	} else {
		return "", nil, errWorkspace
	}
	message := chat[len(chat)-1]
	if strings.TrimSpace(message) == "" || strings.HasPrefix(message, "--") {
		return "", nil, errWorkspace
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		return "", nil, errWorkspace
	}
	if err := opened.Close(); err != nil {
		return "", nil, errWorkspace
	}
	return root, chat, nil
}

// Counts are local to one synchronous run, keyed only by fixed tool types.
type workspaceCounts struct {
	tools  [5]int
	writes int
}
type countedTool struct {
	tools.Tool
	attempts *int
	writes   *int
}

func (t *countedTool) Execute(ctx context.Context, raw json.RawMessage) (tools.ToolResult, error) {
	(*t.attempts)++
	result, err := t.Tool.Execute(ctx, raw)
	if (t.Name() == "replace_file" || t.Name() == "create_file") && err == nil && !result.IsError {
		(*t.writes)++
	}
	if t.Name() == "replace_file" || t.Name() == "create_file" {
		err = workspaceError(err)
	}
	return result, err
}

func printWorkspaceSummary(out io.Writer, result agentloop.Result, events []agentloop.Event, counts *workspaceCounts, duration time.Duration) error {
	var requested, granted, denied int
	for _, event := range events {
		switch event.Kind {
		case agentloop.ApprovalRequested:
			requested++
		case agentloop.ApprovalGranted:
			granted++
		case agentloop.ApprovalDenied:
			denied++
		}
	}
	_, err := fmt.Fprintf(out, "Resumo do workspace:\nMotivo de parada: %s\nPassos do modelo: %d\nChamadas de ferramenta: %d\nAprovações solicitadas: %d\nAprovações concedidas: %d\nAprovações negadas: %d\nExecuções: echo=%d list_dir=%d read_file=%d replace_file=%d create_file=%d\nEscritas confirmadas: %d\nResultados truncados: %d\nDuração em segundos: %.0f\n", result.StopReason, result.Steps, result.ToolCalls, requested, granted, denied, counts.tools[0], counts.tools[1], counts.tools[2], counts.tools[3], counts.tools[4], counts.writes, result.TruncatedToolResults, duration.Round(time.Second).Seconds())
	if err != nil {
		return errors.New("não foi possível exibir o resumo do workspace")
	}
	return nil
}
