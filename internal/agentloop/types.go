// Package agentloop coordinates a bounded, synchronous model/tool loop.
package agentloop

import (
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
)

type Loop struct {
	Model      model.Model
	Registry   *tools.Registry
	Budget     Budget
	Sink       EventSink
	Authorizer ToolAuthorizer
	// ValidateFinal is optional and deterministic. A nonempty recovery message
	// requests one additional, tool-free model step within the existing budget.
	// The loop enforces at most one recovery, regardless of validator behavior.
	ValidateFinal func(string) (recovery string, err error)
}

type Result struct {
	FinalAnswer string
	History     []model.Message
	Steps       int
	// ToolCalls counts attempted calls, including controlled failures and
	// interrupted executions, but excludes rejected batches.
	ToolCalls            int
	TruncatedToolResults int
	StopReason           StopReason
}
