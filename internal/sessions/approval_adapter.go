package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/createcontract"
	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/mcp"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"unicode/utf8"
)

// One adapter per synchronous Session authorizer. The current call never crosses
// a public boundary. Write reviewers receive their immutable contract display.
type webApprovalAdapter struct {
	computer  *computer.Binding
	manager   *Manager
	session   ID
	sink      agentloop.EventSink
	current   agentloop.ToolAuthorizationRequest
	requested agentloop.Event
}

func (a *webApprovalAdapter) Record(ctx context.Context, event agentloop.Event) {
	if event.Kind == agentloop.ApprovalRequested {
		a.requested = event
		return
	}
	a.sink.Record(ctx, event)
}

type webCallAuthorizer struct {
	adapter    *webApprovalAdapter
	authorizer agentloop.ToolAuthorizer
}

func (a webCallAuthorizer) Authorize(ctx context.Context, r agentloop.ToolAuthorizationRequest) (agentloop.ToolDecision, error) {
	r.Call.Arguments = append([]byte(nil), r.Call.Arguments...)
	a.adapter.current = r
	a.adapter.requested = agentloop.Event{}
	defer func() {
		a.adapter.current = agentloop.ToolAuthorizationRequest{}
		a.adapter.requested = agentloop.Event{}
	}()
	return a.authorizer.Authorize(ctx, r)
}
func (a *webApprovalAdapter) wait(ctx context.Context, p ApprovalPresentation) (bool, error) {
	if a.requested.Kind != agentloop.ApprovalRequested || a.current.Call.Name != p.Tool {
		return false, ErrApprovalPresentation
	}
	return a.manager.waitApproval(ctx, a.session, a.current.Call.ID, p, func() { a.sink.Record(ctx, a.requested) })
}
func (a *webApprovalAdapter) Approve(ctx context.Context, r agentloop.ToolAuthorizationRequest) (bool, error) {
	if r.Call.ID != a.current.Call.ID || r.Call.Name != a.current.Call.Name || !bytes.Equal(r.Call.Arguments, a.current.Call.Arguments) {
		return false, ErrApprovalPresentation
	}
	if a.computer != nil {
		if _, ok := a.computer.Tool(r.Call.Name); ok {
			p, err := a.computer.Presentation(r.Call.Name, r.Call.Arguments)
			if err != nil {
				return false, err
			}
			meta := a.computer.Metadata()
			namespace := meta.ID
			warning := "This action controls your real local desktop with your user permissions. Approved observations are sent to the model provider. Review the exact target and text. One-shot approval; cancellation cannot undo an action already performed. No sandbox guarantee."
			if strings.HasPrefix(meta.ID, "sandbox-") {
				namespace = "sandbox"
				warning = "This action controls this Session disposable Linux gVisor sandbox, not the host desktop. Approved observations go to the model provider. Guest data disappears on cleanup; outbound networking remains enabled. One-shot approval."
			}
			if meta.Backend == computer.CUACloud {
				warning = "CLOUD · Official CUA Fleet remote guest. " + warning
			}
			return a.wait(ctx, ApprovalPresentation{Tool: r.Call.Name, Kind: "computer", Target: p.Target, Preview: p.Preview, ServerID: namespace, ComputerID: meta.ID, Backend: string(meta.Backend), Classification: string(p.Action), Warning: warning})
		}
	}
	if mcp.IsName(r.Call.Name) {
		if a.manager.deps.MCP == nil {
			return false, ErrApprovalPresentation
		}
		_, classification, err := a.manager.deps.MCP.Lookup(r.Call.Name)
		if err != nil || classification != mcp.Read {
			return false, ErrApprovalPresentation
		}
		server := strings.SplitN(r.Call.Name, "__", 3)[1]
		return a.wait(ctx, ApprovalPresentation{Tool: r.Call.Name, Kind: "read", Target: r.Call.Name, ServerID: server, Classification: "read", Warning: "This external MCP tool receives model-generated arguments and runs with user permissions. Arguments are not displayed. Its approved result is sent to the configured model provider. Local classification is not a sandbox guarantee."})
	}
	if r.Call.Name != "read_file" && r.Call.Name != "list_dir" {
		return false, ErrApprovalPresentation
	}
	d := json.NewDecoder(bytes.NewReader(r.Call.Arguments))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return false, ErrApprovalPresentation
	}
	var path *string
	for d.More() {
		key, err := d.Token()
		if err != nil || key != "path" || path != nil {
			return false, ErrApprovalPresentation
		}
		if d.Decode(&path) != nil || path == nil {
			return false, ErrApprovalPresentation
		}
	}
	if _, err = d.Token(); err != nil || d.Decode(new(any)) != io.EOF || path == nil || !utf8.ValidString(*path) || !fs.ValidPath(*path) || strings.ContainsAny(*path, "\\:\x00") || len(*path) > 4096 {
		return false, ErrApprovalPresentation
	}
	return a.wait(ctx, ApprovalPresentation{Tool: r.Call.Name, Kind: "read", Target: strconv.QuoteToASCII(*path), Warning: "If approved, the read result will be sent to the configured model provider."})
}
func (a *webApprovalAdapter) Review(ctx context.Context, r editcontract.Review) (editcontract.Decision, error) {
	if a.current.Call.Name != "replace_file" || r.ID == "" || r.Display == "" || r.Path == "" {
		return editcontract.Deny, ErrApprovalPresentation
	}
	allow, err := a.wait(ctx, ApprovalPresentation{Tool: "replace_file", Kind: "write", Target: strconv.QuoteToASCII(r.Path), Preview: r.Display, Warning: "Replace one existing file. Review the complete contract preview. Approval applies once; cancellation after commit cannot undo the effect."})
	if allow && err == nil {
		return editcontract.Allow, nil
	}
	return editcontract.Deny, err
}

type webCreateReviewer struct{ adapter *webApprovalAdapter }

func (a webCreateReviewer) Review(ctx context.Context, r createcontract.Review) (createcontract.Decision, error) {
	if a.adapter.current.Call.Name != "create_file" || r.ID == "" || r.Display == "" || !r.TargetAbsent {
		return createcontract.Deny, ErrApprovalPresentation
	}
	allow, err := a.adapter.wait(ctx, ApprovalPresentation{Tool: "create_file", Kind: "write", Target: strconv.QuoteToASCII(r.Path), Preview: r.Display, Warning: "Create one absent file with permissions 0600. Review the complete contract preview. Content may be visible while writing; no overwrite is permitted."})
	if allow && err == nil {
		return createcontract.Allow, nil
	}
	return createcontract.Deny, err
}
