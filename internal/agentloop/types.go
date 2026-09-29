// Package agentloop coordinates a bounded, synchronous model/tool loop.
package agentloop

import (
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
)

type Loop struct {
	Model    model.Model
	Registry *tools.Registry
	MaxSteps int
	Sink     EventSink
}

type Result struct {
	FinalAnswer string
	History     []model.Message
	Steps       int
}
