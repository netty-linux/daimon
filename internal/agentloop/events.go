package agentloop

import "context"

type EventKind string

const (
	LoopStarted    EventKind = "loop_started"
	ModelRequested EventKind = "model_requested"
	ModelResponded EventKind = "model_responded"
	ToolAllowed    EventKind = "tool_allowed"
	ToolDenied     EventKind = "tool_denied"
	ToolRequested  EventKind = "tool_requested"
	ToolCompleted  EventKind = "tool_completed"
	ToolFailed     EventKind = "tool_failed"
	FinalAnswer    EventKind = "final_answer"
	LoopStopped    EventKind = "loop_stopped"
)

// Event intentionally excludes names, IDs, arguments, output and error text.
// ToolIndex is one-based within its model step; zero means no tool.
type Event struct {
	Kind       EventKind
	Step       int
	ToolIndex  int
	StopReason StopReason // Populated only for loop_stopped.
}

// Record should be a prompt, local operation, including on canceled contexts.
type EventSink interface{ Record(context.Context, Event) }
type NoopEventSink struct{}

func (NoopEventSink) Record(context.Context, Event) {}

// MemoryEventSink is intended for sequential use.
type MemoryEventSink struct{ events []Event }

func (s *MemoryEventSink) Record(_ context.Context, event Event) { s.events = append(s.events, event) }
func (s *MemoryEventSink) Events() []Event                       { return append([]Event(nil), s.events...) }
