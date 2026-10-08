package server

import (
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sandbox"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"time"
)

// Explicit output projection prevents private domain/runtime fields from becoming
// HTTP fields merely because those types gain a field in a later phase.
type botView struct {
	SandboxProfile  *sandbox.Profile    `json:"sandbox_profile,omitempty"`
	ComputerProfile *computer.Profile   `json:"computer_profile,omitempty"`
	ID              bots.ID             `json:"id"`
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	ProviderID      providers.ID        `json:"provider_id"`
	Model           string              `json:"model"`
	Tools           []string            `json:"tools"`
	PermissionMode  bots.PermissionMode `json:"permission_mode"`
}

func viewBot(b bots.Bot) botView {
	return botView{b.SandboxProfile, b.ComputerProfile, b.ID, b.Name, b.Description, b.ProviderID, b.Model, b.Tools, b.PermissionMode}
}

type sessionView struct {
	PersistentWorkspace  *sessions.PersistentMetadata  `json:"persistent_workspace,omitempty"`
	Environment          *sessions.EnvironmentMetadata `json:"environment,omitempty"`
	Computer             *computer.Metadata            `json:"computer,omitempty"`
	ID                   sessions.ID                   `json:"id"`
	ThreadID             string                        `json:"thread_id"`
	Status               sessions.Status               `json:"status"`
	StartedAt            time.Time                     `json:"started_at"`
	FinishedAt           *time.Time                    `json:"finished_at"`
	LastEventSequence    uint64                        `json:"last_event_sequence"`
	StopReason           string                        `json:"stop_reason"`
	ErrorCategory        string                        `json:"error_category"`
	Steps                int                           `json:"steps"`
	ToolCalls            int                           `json:"tool_calls"`
	TruncatedToolResults int                           `json:"truncated_tool_results"`
}

func viewSession(s sessions.Snapshot) sessionView {
	var finished *time.Time
	if !s.FinishedAt.IsZero() {
		t := s.FinishedAt
		finished = &t
	}
	return sessionView{s.PersistentWorkspace, s.Environment, s.Computer, s.ID, string(s.ThreadID), s.Status, s.StartedAt, finished, s.LastEventSequence, string(s.StopReason), string(s.ErrorCategory), s.Steps, s.ToolCalls, s.TruncatedToolResults}
}

type eventView struct {
	Sequence   uint64 `json:"sequence"`
	Kind       string `json:"kind"`
	Step       int    `json:"step"`
	ToolIndex  int    `json:"tool_index"`
	StopReason string `json:"stop_reason"`
}

// Thread CRUD deliberately exposes the configured workspace reference. It never
// appears in session snapshots, events, health or errors.
type threadView struct {
	ID        threads.ID `json:"id"`
	BotID     bots.ID    `json:"bot_id"`
	Workspace string     `json:"workspace"`
	Title     string     `json:"title"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func viewThread(t threads.Thread) threadView {
	return threadView{t.ID, t.BotID, t.Workspace, t.Title, t.CreatedAt, t.UpdatedAt}
}
