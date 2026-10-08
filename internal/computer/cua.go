package computer

import (
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/mcp"
	"github.com/netty-linux/daimon/internal/tools"
	"strings"
)

type CUASource interface {
	ComputerSources() []mcp.ComputerSource
	ComputerTool(string) (mcp.ComputerTransport, mcp.Classification, error)
	Tools() []mcp.ToolMetadata
}
type CUA struct {
	source CUASource
	server string
}

func NewCUA(source CUASource) (*CUA, error) {
	if source == nil {
		return nil, ErrConfig
	}
	sources := source.ComputerSources()
	if len(sources) != 1 || sources[0].Backend != string(CUALocal) || !serverID.MatchString(sources[0].ID) {
		return nil, ErrConfig
	}
	return &CUA{source, sources[0].ID}, nil
}
func (c *CUA) ID() BackendID { return CUALocal }
func (c *CUA) Probe(ctx context.Context) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	info := Info{ID: c.server, Backend: CUALocal, Status: "unavailable", Capabilities: []Capability{}}
	for _, s := range c.source.ComputerSources() {
		if s.ID == c.server && s.Backend == string(CUALocal) {
			info.Status = s.Status
		}
	}
	for _, t := range c.source.Tools() {
		if t.ServerID != c.server {
			continue
		}
		remote := strings.TrimPrefix(t.Name, "mcp__"+c.server+"__")
		class := Classify(remote)
		expected := mcp.Write
		if class == Observe {
			expected = mcp.Read
		}
		info.Capabilities = append(info.Capabilities, Capability{remote, t.Name, class, info.Status == "connected" && t.Available && t.Classification == expected && supported(remote)})
	}
	return info, nil
}
func (c *CUA) Resolve(name string) (Operation, error) {
	if !strings.HasPrefix(name, "mcp__"+c.server+"__") || !mcp.IsName(name) {
		return nil, ErrDenied
	}
	remote := strings.TrimPrefix(name, "mcp__"+c.server+"__")
	class := Classify(remote)
	if !supported(remote) || class == Dangerous {
		return nil, ErrDenied
	}
	t, kind, err := c.source.ComputerTool(name)
	if err != nil {
		return nil, ErrUnavailable
	}
	expected := mcp.Write
	if class == Observe {
		expected = mcp.Read
	}
	if kind != expected {
		return nil, ErrDenied
	}
	return cuaOperation{t}, nil
}

type cuaOperation struct{ transport mcp.ComputerTransport }

func (o cuaOperation) Definition() Definition {
	d := o.transport.Definition()
	return Definition{d.Name, "Local computer action. Individual human approval required. Text observations only.", d.Schema}
}
func (o cuaOperation) Call(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	r, err := o.transport.Call(ctx, args, safeText)
	if err != nil {
		return tools.ToolResult{}, err
	}
	return r, nil
}

// Applied separately to every text block before concatenation/receipt creation.
func safeText(text string) bool {
	if strings.Contains(strings.ToLower(text), "data:image/") {
		return false
	}
	var value any
	return json.Unmarshal([]byte(text), &value) != nil || !imageValue(value)
}

func imageValue(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			switch strings.ToLower(key) {
			case "screenshot", "image", "base64", "image_base64", "screenshot_base64", "image_url":
				if item != nil {
					if text, ok := item.(string); !ok || text != "" {
						return true
					}
				}
			}
			if imageValue(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if imageValue(item) {
				return true
			}
		}
	}
	return false
}
