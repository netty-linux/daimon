package computer

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/tools"
)

// Backend supplies availability and exact discovered operations. Manager owns
// admission, binding lifetime and exclusive access to the physical computer.
type Backend interface {
	ID() BackendID
	Probe(context.Context) (Info, error)
	Resolve(string) (Operation, error)
}
type Operation interface {
	Definition() Definition
	Call(context.Context, json.RawMessage) (tools.ToolResult, error)
}
type Definition struct {
	Name, Description string
	Schema            json.RawMessage
}
