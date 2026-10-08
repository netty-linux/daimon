// Package memory owns explicit user-selected durable context, never execution authority.
package memory

import "time"

type ID string
type Scope string
type Kind string

const (
	Global            Scope = "global"
	Bot               Scope = "bot"
	Thread            Scope = "thread"
	Fact              Kind  = "fact"
	Preference        Kind  = "preference"
	Instruction       Kind  = "instruction"
	Note              Kind  = "note"
	Version                 = 1
	MaxContentBytes         = 16 * 1024
	MaxTags                 = 16
	MaxTagBytes             = 64
	MaxRecords              = 2048
	MaxFileBytes            = 32 * 1024 * 1024
	MaxContextBytes         = 32 * 1024
	MaxContextRecords       = 64
)

type Provenance struct {
	SourceType string `json:"source_type"`
}
type Memory struct {
	ID         ID         `json:"id"`
	Scope      Scope      `json:"scope"`
	ScopeID    string     `json:"scope_id"`
	Kind       Kind       `json:"kind"`
	Content    string     `json:"content"`
	Tags       []string   `json:"tags"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Provenance Provenance `json:"provenance"`
}

// Input excludes store-owned timestamps/provenance. Updates cannot retarget scope.
type Input struct {
	ID      ID       `json:"id"`
	Scope   Scope    `json:"scope"`
	ScopeID string   `json:"scope_id"`
	Kind    Kind     `json:"kind"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

func Clone(m Memory) Memory { m.Tags = append([]string{}, m.Tags...); return m }
