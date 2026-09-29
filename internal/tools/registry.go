package tools

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/netty-linux/daimon/internal/model"
)

var (
	ErrDuplicateTool = errors.New("duplicate tool name")
	ErrInvalidTool   = errors.New("invalid tool definition")
)

// Registry preserves registration order. Configure it before running a loop.
type Registry struct {
	byName       map[string]Tool
	descriptions []model.ToolDescription
}

func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return ErrInvalidTool
	}
	name := tool.Name()
	if strings.TrimSpace(name) == "" || !json.Valid(tool.InputSchema()) {
		return ErrInvalidTool
	}
	if _, exists := r.byName[name]; exists {
		return ErrDuplicateTool
	}
	if r.byName == nil {
		r.byName = make(map[string]Tool)
	}
	r.byName[name] = tool
	r.descriptions = append(r.descriptions, model.ToolDescription{
		Name: name, Description: tool.Description(), InputSchema: append(json.RawMessage(nil), tool.InputSchema()...),
	})
	return nil
}

func (r *Registry) Find(name string) (Tool, bool) {
	tool, ok := r.byName[name]
	return tool, ok
}

func (r *Registry) Descriptions() []model.ToolDescription {
	return model.CloneRequest(model.ModelRequest{Tools: r.descriptions}).Tools
}
