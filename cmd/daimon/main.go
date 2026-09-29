package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"daimon/internal/agentloop"
	"daimon/internal/model"
	"daimon/internal/tools"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) != 1 || args[0] != "demo" {
		return fmt.Errorf("usage: daimon demo")
	}
	reader, err := tools.NewReadFile(".", 64*1024)
	if err != nil {
		return err
	}
	defer reader.Close()
	registry := &tools.Registry{}
	for _, tool := range []tools.Tool{tools.Echo{}, reader} {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	m := model.NewScripted(
		model.ScriptStep{Response: model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "echo-1", Name: "echo", Arguments: []byte(`{"text":"DAIMON"}`)}}}},
		model.ScriptStep{Response: model.ModelResponse{FinalText: "DAIMON"}},
	)
	sink := &agentloop.MemoryEventSink{}
	loop := agentloop.Loop{Model: m, Registry: registry, MaxSteps: 4, Sink: sink}
	result, err := loop.Run(context.Background(), "repita DAIMON")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Resposta final: %s\nPassos do modelo: %d\nEventos:\n", result.FinalAnswer, result.Steps); err != nil {
		return err
	}
	for _, event := range sink.Events() {
		if _, err := fmt.Fprintf(out, "  %s (step=%d, tool=%d)\n", event.Kind, event.Step, event.ToolIndex); err != nil {
			return err
		}
	}
	return nil
}
