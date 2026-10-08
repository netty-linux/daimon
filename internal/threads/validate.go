package threads

import (
	"github.com/netty-linux/daimon/internal/providers"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxIDBytes        = 64
	MaxWorkspaceBytes = 4096
	MaxTitleBytes     = 256
)

func validateID(id ID) error {
	if providers.ValidateID(providers.ID(id)) != nil {
		return &ValidationError{Field: "id"}
	}
	return nil
}

// Validate performs no filesystem, registry or clock access. Workspace is an
// opaque reference: its exact spelling is retained, without path normalization.
func Validate(t Thread) error {
	if err := validateID(t.ID); err != nil {
		return err
	}
	if providers.ValidateID(providers.ID(t.BotID)) != nil {
		return &ValidationError{Field: "bot_id"}
	}
	if strings.TrimSpace(t.Workspace) == "" || len(t.Workspace) > MaxWorkspaceBytes || !utf8.ValidString(t.Workspace) || strings.ContainsRune(t.Workspace, 0) {
		return &ValidationError{Field: "workspace"}
	}
	if len(t.Title) > MaxTitleBytes || !utf8.ValidString(t.Title) {
		return &ValidationError{Field: "title"}
	}
	if !utcTime(t.CreatedAt) {
		return &ValidationError{Field: "created_at"}
	}
	if !utcTime(t.UpdatedAt) || t.UpdatedAt.Before(t.CreatedAt) {
		return &ValidationError{Field: "updated_at"}
	}
	return nil
}

func utcTime(t time.Time) bool {
	_, offset := t.Zone()
	return !t.IsZero() && offset == 0 && t.Year() >= 1 && t.Year() <= 9999
}
