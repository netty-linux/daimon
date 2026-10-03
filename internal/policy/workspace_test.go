package policy

import "testing"

func TestWorkspaceCapabilities(t *testing.T) {
	for _, mode := range []WorkspaceMode{WorkspaceNormal, WorkspacePlan, WorkspaceValidatedPlan, WorkspaceApply, 0, 99} {
		for _, create := range []bool{false, true} {
			for _, replace := range []bool{false, true} {
				for _, recovery := range []bool{false, true} {
					p := WorkspacePolicy{Mode: mode, Create: create, Replace: replace, Recovery: recovery}
					valid := mode >= WorkspaceNormal && mode <= WorkspaceApply && !recovery
					read := valid && mode != WorkspaceApply
					write := valid && mode == WorkspaceNormal
					for _, name := range []string{"read_file", "list_dir", "create_file", "replace_file", "apply_plan", "echo", "shell", "delete_file"} {
						c := p.Capability(name)
						want := false
						switch name {
						case "echo", "read_file", "list_dir":
							want = read
						case "create_file":
							want = write && create
						case "replace_file":
							want = write && replace
						case "apply_plan":
							want = false
						}
						if c.Enabled != want || c.RecoveryAllowed {
							t.Fatalf("incorrect capability: mode=%d name=%s", mode, name)
						}
						if want && name != "echo" && !c.Confirmation {
							t.Fatal("missing confirmation")
						}
						if want && (name == "create_file" || name == "replace_file" || name == "apply_plan") && !c.Revalidation {
							t.Fatal("missing revalidation")
						}
						if (mode == WorkspaceApply || !want) && p.Decide(name) != Deny {
							t.Fatal("free tool allowed")
						}
					}
				}
			}
		}
	}
}

func TestWorkspaceDefaultMatchesExistingPolicy(t *testing.T) {
	p := WorkspacePolicy{Mode: WorkspaceNormal}
	for _, name := range []string{"echo", "read_file", "list_dir", "create_file", "replace_file", "unknown"} {
		if p.Decide(name) != DefaultCLIPolicy().Decide(name) {
			t.Fatal("default changed", name)
		}
	}
}
