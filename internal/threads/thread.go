// Package threads defines persisted conversation metadata, never active execution.
package threads

import (
	"github.com/netty-linux/daimon/internal/bots"
	"time"
)

type ID string

// Thread references an unresolved Bot and workspace. It contains no history,
// credentials, provider snapshot, permissions or session state.
// All fields are values; assigning a Thread produces an independent copy.
type Thread struct {
	ID        ID        `json:"id"`
	BotID     bots.ID   `json:"bot_id"`
	Workspace string    `json:"workspace"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
