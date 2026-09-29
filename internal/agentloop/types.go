package agentloop

import "github.com/netty-linux/daimon/internal/model"

// Result é o resultado de uma execução do agent loop.
type Result struct {
	FinalAnswer         string
	History             []model.Message
	Steps               int
	ToolCalls           int
	TruncatedToolResults int
	StopReason          StopReason
}

// Options configura a execução do agent loop.
type Options struct {
	Model    model.Model
	Tools    []Tool
	MaxSteps int
	Budget   Budget
}
