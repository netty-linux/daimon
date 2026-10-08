// Package conversations owns private, immutable user/assistant transcripts.
// It contains no execution, provider configuration, tools or semantic memory.
package conversations

import (
	"github.com/netty-linux/daimon/internal/threads"
	"time"
)

type ID string
type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
)
const (
	Version           = 1
	MaxMessages       = 1024
	MaxFileBytes      = 16 * 1024 * 1024
	MaxUserBytes      = 32 * 1024
	MaxAssistantBytes = 256 * 1024
)

type Message struct {
	ID        ID         `json:"id"`
	ThreadID  threads.ID `json:"thread_id"`
	Sequence  uint64     `json:"sequence"`
	Role      Role       `json:"role"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	SessionID string     `json:"session_id"`
}
