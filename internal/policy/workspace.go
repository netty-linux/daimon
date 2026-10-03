package policy

// WorkspaceMode has no permissive zero value. Apply never exposes model tools.
type WorkspaceMode uint8

const (
	WorkspaceNormal WorkspaceMode = iota + 1
	WorkspacePlan
	WorkspaceValidatedPlan
	WorkspaceApply
)

// WorkspacePolicy is an explicit, ephemeral session configuration, not a grant.
// Each enabled operation still requires the corresponding approval contract.
type WorkspacePolicy struct {
	Mode                      WorkspaceMode
	Create, Replace, Recovery bool
}

type Capability struct {
	Enabled, Confirmation, Revalidation, ModelTool, RecoveryAllowed bool
}

func (p WorkspacePolicy) Capability(name string) Capability {
	if p.Recovery || p.Mode < WorkspaceNormal || p.Mode > WorkspaceApply {
		return Capability{}
	}
	model := p.Mode != WorkspaceApply
	switch name {
	case "echo":
		return Capability{Enabled: model, ModelTool: model}
	case "list_dir", "read_file":
		return Capability{Enabled: model, ModelTool: model, Confirmation: model}
	case "create_file", "replace_file":
		// Apply remains unavailable until physical namespace isolation is proven.
		write := p.Mode == WorkspaceNormal
		enabled := write && ((name == "create_file" && p.Create) || (name == "replace_file" && p.Replace))
		return Capability{Enabled: enabled, Confirmation: enabled, Revalidation: enabled, ModelTool: enabled && model}
	case "apply_plan":
		return Capability{}
	default:
		return Capability{}
	}
}

func (p WorkspacePolicy) Decide(name string) Decision {
	c := p.Capability(name)
	if !c.Enabled || !c.ModelTool {
		return Deny
	}
	if c.Confirmation {
		return RequireApproval
	}
	return Allow
}
