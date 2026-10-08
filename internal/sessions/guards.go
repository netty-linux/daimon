package sessions

import (
	"context"
	"encoding/json"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
)

// Recover only at the async component boundary, converting panics before they
// unwind the loop. No panic value/stack is retained in errors or events.
type guardModel struct{ model.Model }

func (m guardModel) Generate(ctx context.Context, req model.ModelRequest) (response model.ModelResponse, err error) {
	defer func() {
		if recover() != nil {
			response = model.ModelResponse{}
			err = &Error{Kind: Panic}
		}
	}()
	return m.Model.Generate(ctx, req)
}

type guardTool struct{ tools.Tool }

func (t guardTool) Execute(ctx context.Context, args json.RawMessage) (result tools.ToolResult, err error) {
	defer func() {
		if recover() != nil {
			result = tools.ToolResult{}
			err = &Error{Kind: Panic}
		}
	}()
	return t.Tool.Execute(ctx, args)
}

type guardAuthorizer struct{ agentloop.ToolAuthorizer }

func (a guardAuthorizer) Authorize(ctx context.Context, req agentloop.ToolAuthorizationRequest) (decision agentloop.ToolDecision, err error) {
	defer func() {
		if recover() != nil {
			decision = ""
			err = &Error{Kind: Panic}
		}
	}()
	return a.ToolAuthorizer.Authorize(ctx, req)
}
