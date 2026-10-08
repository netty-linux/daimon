package sessions

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/createcontract"
	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/mcp"
	"github.com/netty-linux/daimon/internal/policy"
	"github.com/netty-linux/daimon/internal/threads"
	"github.com/netty-linux/daimon/internal/tools"
	"github.com/netty-linux/daimon/internal/workspacefs"
	"strings"
	"time"
)

type resolvedRuntime struct {
	syncWorkspace func(context.Context) error
	runCtx        context.Context
	memoryContext string
	loop          agentloop.Loop
	binding       Binding
	closers       []func() error
}

func closeResource(close func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = &Error{Kind: Panic}
		}
	}()
	return close()
}

func (r *resolvedRuntime) close() error {
	var err error
	for i := len(r.closers) - 1; i >= 0; i-- {
		err = errors.Join(err, closeResource(r.closers[i]))
	}
	r.closers = nil
	return err
}

// Fixed declarative catalog. No heuristic, wildcard, dynamic tools or bypass.
type capability uint8

const (
	read capability = iota + 1
	write
	other
)

func classify(name string) capability {
	switch name {
	case "read_file", "list_dir":
		return read
	case "replace_file", "create_file":
		return write
	case "echo":
		return other
	default:
		return 0
	}
}

func (m *Manager) resolve(ctx context.Context, req StartRequest, sink agentloop.EventSink, r *resolvedRuntime) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	thread, err := m.deps.Threads.Get(req.ThreadID)
	if err != nil {
		return &Error{Kind: ThreadResolution, Cause: err}
	}
	if err := threads.Validate(thread); err != nil || thread.ID != req.ThreadID {
		return &Error{Kind: ThreadResolution, Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	bot, err := m.deps.Bots.Get(thread.BotID)
	if err != nil {
		return &Error{Kind: BotResolution, Cause: err}
	}
	bot = bots.Clone(bot)
	if err := bots.Validate(bot); err != nil || bot.ID != thread.BotID {
		return &Error{Kind: BotResolution, Cause: err}
	}
	r.binding = Binding{BotID: bot.ID, ProviderID: bot.ProviderID, Model: bot.Model, Instructions: bot.Instructions, Tools: append([]string{}, bot.Tools...), PermissionMode: bot.PermissionMode}
	needsApproval := false
	external := map[string]tools.Tool{}
	var computerBinding *computer.Binding
	computerManager := m.deps.Computers
	computerProfile := bot.ComputerProfile
	persistent, err := m.reserveEnvironment(ctx, req, bot.SandboxProfile != nil, r)
	if err != nil {
		return err
	}
	if bot.SandboxProfile != nil {
		if m.deps.Sandboxes == nil || !m.deps.WebApprovals {
			return &Error{Kind: SandboxResolution}
		}
		profile := *bot.SandboxProfile
		r.binding.SandboxProfile = &profile
		environment := EnvironmentMetadata{Mode: "sandbox", State: "creating", Runtime: "gvisor", Cleanup: "pending", Placement: profile.EffectivePlacement(), Backend: profile.Backend, Resources: profile.Resources}
		m.setEnvironment(req.SessionID, environment)
		sink.Record(ctx, agentloop.Event{Kind: agentloop.SandboxCreating})
		lease, e := m.deps.Sandboxes.Create(ctx, string(req.SessionID), profile)
		if e != nil {
			environment.State = "failed"
			for _, info := range m.deps.Sandboxes.List() {
				if info.OwnerSession == string(req.SessionID) {
					environment.SandboxID = string(info.ID)
					environment.Cleanup = info.Cleanup
					break
				}
			}
			m.setEnvironment(req.SessionID, environment)
			sink.Record(ctx, agentloop.Event{Kind: agentloop.SandboxFailed})
			return &Error{Kind: SandboxResolution, Cause: e}
		}
		environment.State = "ready"
		if info, e := m.deps.Sandboxes.Get(lease.ID()); e == nil {
			environment.ExpiresAt = info.ExpiresAt
		}
		environment.SandboxID = string(lease.ID())
		m.setEnvironment(req.SessionID, environment)
		sink.Record(ctx, agentloop.Event{Kind: agentloop.SandboxReady})
		computerManager = lease.Computer
		r.runCtx = lease.Context
		ctx = lease.Context
		guestProfile := computer.Profile{Enabled: true, Backend: computer.BackendID(profile.Backend), MCPServerID: "sandbox"}
		computerProfile = &guestProfile
		r.closers = append(r.closers, func() error {
			environment.State = "cleaning_up"
			m.setEnvironment(req.SessionID, environment)
			sink.Record(context.Background(), agentloop.Event{Kind: agentloop.SandboxCleanupStarted})
			closeCtx, end := context.WithTimeout(context.Background(), 15*time.Second)
			defer end()
			e := lease.Close(closeCtx)
			environment.State = "deleted"
			environment.Cleanup = "complete"
			kind := agentloop.SandboxCleanupCompleted
			info, infoErr := m.deps.Sandboxes.Get(lease.ID())
			if infoErr == nil {
				environment.Cleanup = info.Cleanup
			}
			if e != nil {
				environment.State = "failed"
				if infoErr != nil {
					environment.Cleanup = "unresolved"
				}
				kind = agentloop.SandboxFailed
			}
			m.setEnvironment(req.SessionID, environment)
			sink.Record(context.Background(), agentloop.Event{Kind: kind})
			return e
		})
		if persistent != nil {
			if err := m.hydrateEnvironment(ctx, req, sink, r, lease, persistent); err != nil {
				return err
			}
		}
	}
	if computerProfile != nil && computerProfile.Enabled {
		if computerManager == nil || !m.deps.WebApprovals {
			return &Error{Kind: ComputerResolution}
		}
		computerBinding, err = computerManager.Open(ctx, string(req.SessionID), *computerProfile, bot.Tools, bot.PermissionMode == bots.PermissionReadOnly)
		if err != nil {
			kind := ComputerResolution
			if errors.Is(err, computer.ErrBusy) {
				kind = ComputerBusy
			}
			return &Error{Kind: kind, Cause: err}
		}
		meta := computerBinding.Metadata()
		r.binding.Computer = &meta
		if r.syncWorkspace != nil {
			syncWorkspace := r.syncWorkspace
			r.syncWorkspace = func(ctx context.Context) error {
				// Revoke agent/human input before taking the final guest snapshot.
				// The Sandbox remains alive until the existing later cleanup.
				closeCtx, end := context.WithTimeout(ctx, 5*time.Second)
				defer end()
				if e := computerBinding.Close(closeCtx); e != nil {
					return &Error{Kind: Cleanup, Cause: e}
				}
				return syncWorkspace(ctx)
			}
		}
		r.closers = append(r.closers, func() error {
			closeCtx, end := context.WithTimeout(context.Background(), 5*time.Second)
			defer end()
			return computerBinding.Close(closeCtx)
		})
	}
	for _, name := range bot.Tools {
		kind := classify(name)
		if computerBinding != nil && strings.HasPrefix(name, "mcp__"+computerProfile.MCPServerID+"__") {
			tool, ok := computerBinding.Tool(name)
			if !ok {
				return &Error{Kind: ToolResolution}
			}
			external[name] = tool
			kind = write
			if computer.Classify(strings.TrimPrefix(name, "mcp__"+computerProfile.MCPServerID+"__")) == computer.Observe {
				kind = read
			}
			needsApproval = true
			continue
		}
		if mcp.IsName(name) && m.deps.MCP != nil {
			tool, classification, err := m.deps.MCP.Lookup(name)
			if err != nil || tool == nil || tool.Name() != name || classification != mcp.Read {
				return &Error{Kind: ToolResolution, Cause: err}
			}
			external[name], kind = tool, read
		}
		if kind == 0 || (bot.PermissionMode == bots.PermissionReadOnly && kind != read) {
			return &Error{Kind: ToolResolution}
		}
		if kind == write && (runtime.GOOS != "linux" || (name == "replace_file" && !req.EnableReplaceFile) || (name == "create_file" && !req.EnableCreateFile)) {
			return &Error{Kind: ToolResolution}
		}
		needsApproval = needsApproval || kind != other
	}
	// Absolute runtime roots avoid interpreting persisted relative references via cwd.
	// The stored Thread is never modified. Existing boundary resolves/checks root.
	if !filepath.IsAbs(thread.Workspace) {
		return &Error{Kind: WorkspaceResolution}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, bound, err := workspacefs.Open(thread.Workspace)
	if err != nil {
		return &Error{Kind: WorkspaceResolution, Cause: err}
	}
	r.closers = append(r.closers, root.Close)
	canonical := root.Name()
	factory, exists := m.factories[bot.ProviderID]
	if !exists {
		return &Error{Kind: ProviderResolution}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg, err := m.deps.Config(ctx, bot.ProviderID)
	if err != nil {
		return &Error{Kind: ConfigResolution, Cause: err}
	}
	if (cfg.Model != "" && cfg.Model != bot.Model) || (cfg.SystemInstruction != "" && cfg.SystemInstruction != bot.Instructions) || cfg.MaxResponseBytes <= 0 || cfg.MaxResponseBytes == 1<<63-1 {
		return &Error{Kind: ConfigResolution}
	}
	if m.deps.Memory != nil && cfg.AdditionalContext != "" {
		return &Error{Kind: ConfigResolution}
	}
	cfg.Model, cfg.SystemInstruction = bot.Model, bot.Instructions
	if r.memoryContext != "" {
		cfg.AdditionalContext = r.memoryContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	selected, err := factory.New(cfg)
	if err != nil {
		return &Error{Kind: ConfigResolution, Cause: err}
	}
	if selected == nil {
		return &Error{Kind: ConfigResolution}
	}
	var approvals Approvals
	var web *webApprovalAdapter
	if needsApproval {
		if m.deps.WebApprovals {
			web = &webApprovalAdapter{manager: m, session: req.SessionID, sink: sink, computer: computerBinding}
			approvals = Approvals{Reads: web, Replacements: web, Creations: webCreateReviewer{web}}
		} else if m.deps.Approvals == nil {
			return &Error{Kind: ApprovalResolution}
		} else {
			approvals, err = m.deps.Approvals(ctx, req.SessionID)
			if err != nil {
				return &Error{Kind: ApprovalResolution, Cause: err}
			}
		}
	}
	registry := &tools.Registry{}
	authorizer := &policy.Authorizer{Policy: sessionPolicy{native: policy.WorkspacePolicy{Mode: policy.WorkspaceNormal, Create: req.EnableCreateFile, Replace: req.EnableReplaceFile}, external: external}, Approvals: approvals.Reads, Sink: sink}
	if web != nil {
		authorizer.Sink = web
	}
	for _, name := range bot.Tools {
		if err := ctx.Err(); err != nil {
			return err
		}
		var tool tools.Tool
		switch name {
		case "echo":
			tool = tools.Echo{}
		case "read_file":
			if approvals.Reads == nil {
				return &Error{Kind: ApprovalResolution}
			}
			reader, err := tools.NewReadFile(canonical, 64*1024)
			if err != nil {
				return &Error{Kind: WorkspaceResolution, Cause: err}
			}
			r.closers = append(r.closers, reader.Close)
			tool = reader
		case "list_dir":
			if approvals.Reads == nil {
				return &Error{Kind: ApprovalResolution}
			}
			lister, err := tools.NewListDir(canonical, 256, 32*1024)
			if err != nil {
				return &Error{Kind: WorkspaceResolution, Cause: err}
			}
			r.closers = append(r.closers, lister.Close)
			tool = lister
		case "replace_file":
			if approvals.Replacements == nil {
				return &Error{Kind: ApprovalResolution}
			}
			replacer, err := tools.NewReplaceFile(canonical, editcontract.Limits{InputBytes: 64 * 1024, FinalBytes: 64 * 1024, Lines: 1000, PathBytes: 4096, PreviewBytes: 1024 * 1024})
			if err != nil {
				return &Error{Kind: WorkspaceResolution, Cause: err}
			}
			r.closers = append(r.closers, replacer.Close)
			tool = replacer
			authorizer.Replacements, authorizer.EditReviews = replacer, approvals.Replacements
		case "create_file":
			if approvals.Creations == nil {
				return &Error{Kind: ApprovalResolution}
			}
			creator, err := tools.NewCreateFile(canonical, createcontract.Limits{FinalBytes: 64 * 1024, Lines: 1000, PathBytes: 4096, PreviewBytes: 1024 * 1024})
			if err != nil {
				return &Error{Kind: WorkspaceResolution, Cause: err}
			}
			r.closers = append(r.closers, creator.Close)
			tool = creator
			authorizer.Creations, authorizer.CreateReviews = creator, approvals.Creations
		default:
			tool = external[name]
		}
		if err := registry.Register(guardTool{Tool: tool}); err != nil {
			return &Error{Kind: ToolResolution, Cause: err}
		}
	}
	if err := bound.Check(); err != nil {
		return &Error{Kind: WorkspaceResolution, Cause: err}
	}
	var selectedAuthorizer agentloop.ToolAuthorizer = authorizer
	if web != nil {
		selectedAuthorizer = webCallAuthorizer{web, authorizer}
	}
	r.loop = agentloop.Loop{Model: guardModel{Model: selected}, Registry: registry, Authorizer: guardAuthorizer{selectedAuthorizer}, Budget: m.options.Budget, Sink: sink}
	r.loop.Budget.MaxHistoryBytes -= len(r.memoryContext)
	return ctx.Err()
}

// Exact per-run intersection. Discovery and remote annotations cannot authorize.
type sessionPolicy struct {
	native   policy.WorkspacePolicy
	external map[string]tools.Tool
}

func (p sessionPolicy) Decide(name string) policy.Decision {
	if _, ok := p.external[name]; ok {
		return policy.RequireApproval
	}
	return p.native.Decide(name)
}
