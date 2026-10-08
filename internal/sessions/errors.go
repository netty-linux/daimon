package sessions

import "errors"

type Kind string

const (
	EnvironmentResolution Kind = "environment_resolution"
	EnvironmentHydration  Kind = "environment_hydration"
	EnvironmentSync       Kind = "environment_sync"
	EnvironmentConflict   Kind = "environment_conflict"
	SandboxResolution     Kind = "sandbox_resolution"
	ComputerResolution    Kind = "computer_resolution"
	ComputerBusy          Kind = "computer_busy"
	Invalid               Kind = "invalid_config"
	NotFound              Kind = "not_found"
	Duplicate             Kind = "duplicate_session"
	ThreadBusy            Kind = "thread_busy"
	NotRunning            Kind = "not_running"
	Closed                Kind = "manager_closed"
	Capacity              Kind = "session_capacity"
	ThreadResolution      Kind = "thread_resolution"
	BotResolution         Kind = "bot_resolution"
	ProviderResolution    Kind = "provider_resolution"
	ConfigResolution      Kind = "config_resolution"
	ToolResolution        Kind = "tool_resolution"
	WorkspaceResolution   Kind = "workspace_resolution"
	ApprovalResolution    Kind = "approval_resolution"
	Execution             Kind = "execution"
	Cleanup               Kind = "cleanup"
	Panic                 Kind = "component_panic"
	Canceled              Kind = "canceled"
	Deadline              Kind = "deadline"
	Persistence           Kind = "conversation_persistence"
	DuplicateMessage      Kind = "duplicate_message"
	MemoryResolution      Kind = "memory_resolution"
	HistoryResolution     Kind = "history_resolution"
)

var ErrAborted = errors.New("sessions: abort requested")

// Error messages contain controlled categories only. Unwrap may contain private
// causes: inspect identities but do not log underlying errors or complete structs.
type Error struct {
	Kind  Kind
	Cause error
}

func (e *Error) Error() string {
	switch e.Kind {
	case EnvironmentResolution, EnvironmentHydration, EnvironmentSync, EnvironmentConflict, Invalid, NotFound, Duplicate, ThreadBusy, NotRunning, Closed, Capacity,
		ThreadResolution, BotResolution, ProviderResolution, ConfigResolution,
		ToolResolution, WorkspaceResolution, ApprovalResolution, Execution,
		Cleanup, Panic, Canceled, Deadline, Persistence, DuplicateMessage, HistoryResolution, MemoryResolution, ComputerResolution, ComputerBusy, SandboxResolution:
		return "sessions: " + string(e.Kind)
	default:
		return "sessions: failure"
	}
}
func (e *Error) Unwrap() error { return e.Cause }
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && other.Kind == e.Kind
}

func category(err error) (kind Kind) {
	defer func() {
		if recover() != nil {
			kind = Panic
		}
	}()
	var typed *Error
	if errors.As(err, &typed) {
		switch typed.Kind {
		case EnvironmentResolution, EnvironmentHydration, EnvironmentSync, EnvironmentConflict, Invalid, NotFound, Duplicate, ThreadBusy, NotRunning, Closed, Capacity,
			ThreadResolution, BotResolution, ProviderResolution, ConfigResolution,
			ToolResolution, WorkspaceResolution, ApprovalResolution, Execution,
			Cleanup, Panic, Canceled, Deadline, Persistence, DuplicateMessage, HistoryResolution, MemoryResolution, ComputerResolution, ComputerBusy, SandboxResolution:
			return typed.Kind
		}
	}
	return Execution
}
