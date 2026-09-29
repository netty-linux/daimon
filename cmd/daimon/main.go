package main

import (
	"context"
	"fmt"
	"os"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: daimon <command>")
		fmt.Println("Commands:")
		fmt.Println("  demo    Run a demonstration of the agent loop")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "demo":
		runDemo()
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runDemo() {
	// Registry de ferramentas
	registry := tools.NewRegistry()
	registry.Register(tools.NewEcho())
	registry.Register(tools.NewReadFile())

	// Modelo scripted para demo
	scriptedModel := model.NewScriptedModel([]model.ScriptedResponse{
		{
			Content: "Vou listar o conteúdo do arquivo.",
			ToolCalls: []model.ToolCall{
				{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: `{"path":"README.md"}`,
				},
			},
		},
		{
			Content: "DAIMON",
		},
	})

	// Cria loop com budget padrão
	loop, err := agentloop.NewLoop(agentloop.Options{
		Model:  scriptedModel,
		Tools:  registry.Tools(),
		Budget: agentloop.DefaultBudget(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create loop: %v\n", err)
		os.Exit(1)
	}

	// Executa
	result, err := loop.Run(context.Background(), "Leia o arquivo README.md e me diga o que está escrito.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Loop error: %v\n", err)
	}

	fmt.Println("=== DAIMON Agent Demo ===")
	fmt.Printf("Final Answer: %s\n", result.FinalAnswer)
	fmt.Printf("Steps: %d\n", result.Steps)
	fmt.Printf("Tool Calls: %d\n", result.ToolCalls)
	fmt.Printf("Truncated Tool Results: %d\n", result.TruncatedToolResults)
	fmt.Printf("Stop Reason: %s\n", result.StopReason)
}
