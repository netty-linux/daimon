package workspacefs

import "runtime"

// Isolation describes apply eligibility, not the legacy single-file executor.
// No caller-supplied flag can turn an unproven platform into a supported one.
type Isolation struct {
	Platform string
	Strong   bool
	Reason   string
}

func ApplyIsolation() Isolation {
	reason := "platform_not_supported"
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		reason = "external_namespace_mutation_uncontrolled"
	}
	return Isolation{Platform: runtime.GOOS, Strong: false, Reason: reason}
}
