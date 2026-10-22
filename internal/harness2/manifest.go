// Package harness2 defines native candidate contracts. It executes no Python,
// model, tool or filesystem operation and is not wired into ordinary Sessions.
package harness2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/providers"
)

const (
	Version          = 1
	MaxManifestBytes = 64 * 1024
	MaxArtifacts     = 16 // Per surface; references only, never paths or commands.
)

var (
	ErrInvalid     = errors.New("harness2: invalid manifest")
	ErrVersion     = errors.New("harness2: unsupported version")
	ErrLimit       = errors.New("harness2: manifest limit exceeded")
	ErrConflict    = errors.New("harness2: baseline conflict")
	ErrUnsupported = errors.New("harness2: surface not executable")
)

// Artifact is an inert reference to future private content-addressed storage.
// Validation neither fetches content nor certifies that it exists or is safe.
type Artifact struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// Components names all eight upstream surfaces without importing their launchers.
// Guardrails is proposed text, never a policy, approval or permission grant.
// Memory is experimental context, never a record in internal/memory.
type Components struct {
	SystemPrompt string     `json:"system_prompt"`
	Guardrails   string     `json:"guardrails"`
	Memory       string     `json:"memory"`
	ToolGuidance string     `json:"tool_guidance"`
	Subagents    []Artifact `json:"subagents"`
	Skills       []Artifact `json:"skills"`
	Scripts      []Artifact `json:"scripts"`
	Plugins      []Artifact `json:"plugins"`
}

// Manifest is private candidate data, not authority. Do not log it or expose it
// through public events. Revision/parent are structural, not an authenticated log.
type Manifest struct {
	Version       int        `json:"version"`
	BotID         bots.ID    `json:"bot_id"`
	BaseBotSHA256 string     `json:"base_bot_sha256"`
	Revision      uint64     `json:"revision"`
	ParentSHA256  string     `json:"parent_sha256"`
	Components    Components `json:"components"`
}

// Baseline constructs a first candidate from an explicitly supplied valid Bot.
// The hash binds all Bot fields, including provider, tools and sandbox profiles.
func Baseline(bot bots.Bot) (Manifest, error) {
	hash, err := BotFingerprint(bot)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{Version: Version, BotID: bot.ID, BaseBotSHA256: hash, Revision: 1,
		Components: Components{SystemPrompt: bot.Instructions, Subagents: []Artifact{},
			Skills: []Artifact{}, Scripts: []Artifact{}, Plugins: []Artifact{}}}, nil
}

// BotFingerprint is deterministic for the current Bot schema. It is private
// metadata, not a MAC, secret detector or cross-schema identity guarantee.
func BotFingerprint(bot bots.Bot) (string, error) {
	if bots.Validate(bot) != nil {
		return "", ErrInvalid
	}
	data, err := json.Marshal(bot)
	if err != nil {
		return "", ErrInvalid
	}
	return digest(data), nil
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func Validate(m Manifest) error {
	if m.Version != Version {
		return ErrVersion
	}
	if providers.ValidateID(providers.ID(m.BotID)) != nil || !validHash(m.BaseBotSHA256) || m.Revision == 0 {
		return ErrInvalid
	}
	if m.Revision == 1 && m.ParentSHA256 != "" || m.Revision > 1 && !validHash(m.ParentSHA256) {
		return ErrInvalid
	}
	c := m.Components
	if strings.TrimSpace(c.SystemPrompt) == "" {
		return ErrInvalid
	}
	for _, s := range []string{c.SystemPrompt, c.Guardrails, c.Memory, c.ToolGuidance} {
		if !utf8.ValidString(s) {
			return ErrInvalid
		}
		if len(s) > bots.MaxInstructionsBytes {
			return ErrLimit
		}
	}
	for _, list := range [][]Artifact{c.Subagents, c.Skills, c.Scripts, c.Plugins} {
		if list == nil {
			return ErrInvalid
		}
		if len(list) > MaxArtifacts {
			return ErrLimit
		}
		seen := map[string]bool{}
		for _, a := range list {
			if providers.ValidateID(providers.ID(a.ID)) != nil || !validHash(a.SHA256) || seen[a.ID] {
				return ErrInvalid
			}
			seen[a.ID] = true
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		return ErrInvalid
	}
	if len(data) > MaxManifestBytes {
		return ErrLimit
	}
	return nil
}

// Snapshot owns canonical encoded bytes as an immutable string. The zero value
// fails closed. Accessors return independent values for deliberate private use.
type Snapshot struct {
	encoded string
	sha256  string
}

func Freeze(m Manifest, baseline bots.Bot) (Snapshot, error) {
	if err := Validate(m); err != nil {
		return Snapshot{}, err
	}
	if err := matches(m, baseline); err != nil {
		return Snapshot{}, err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return Snapshot{}, ErrInvalid
	}
	return Snapshot{encoded: string(data), sha256: digest(data)}, nil
}

func (s Snapshot) String() string { return "harness2: private snapshot" }
func (s Snapshot) SHA256() string { return s.sha256 }

func (s Snapshot) Manifest() (Manifest, error) {
	if s.encoded == "" {
		return Manifest{}, ErrInvalid
	}
	return Decode([]byte(s.encoded))
}

func matches(m Manifest, bot bots.Bot) error {
	hash, err := BotFingerprint(bot)
	if err != nil {
		return err
	}
	if m.BotID != bot.ID || m.BaseBotSHA256 != hash {
		return ErrConflict
	}
	return nil
}

// Candidate returns a new Bot value with only its system prompt replaced. It
// does not start a Session, persist/promote a Bot, or establish isolation.
// All other surfaces must be exactly empty until their execution gates exist.
func (s Snapshot) Candidate(baseline bots.Bot) (bots.Bot, error) {
	m, err := s.Manifest()
	if err != nil {
		return bots.Bot{}, err
	}
	if err := matches(m, baseline); err != nil {
		return bots.Bot{}, err
	}
	c := m.Components
	if c.Guardrails != "" || c.Memory != "" || c.ToolGuidance != "" ||
		len(c.Subagents) != 0 || len(c.Skills) != 0 || len(c.Scripts) != 0 || len(c.Plugins) != 0 {
		return bots.Bot{}, ErrUnsupported
	}
	bot := bots.Clone(baseline)
	bot.Instructions = c.SystemPrompt
	if bots.Validate(bot) != nil {
		return bots.Bot{}, ErrInvalid
	}
	return bot, nil
}
