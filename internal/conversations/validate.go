package conversations

import (
	"github.com/netty-linux/daimon/internal/providers"
	"strings"
	"unicode/utf8"
)

func Validate(m Message) error {
	if providers.ValidateID(providers.ID(m.ID)) != nil || providers.ValidateID(providers.ID(m.ThreadID)) != nil || providers.ValidateID(providers.ID(m.SessionID)) != nil {
		return ErrInvalid
	}
	max := MaxUserBytes
	if m.Role == Assistant {
		max = MaxAssistantBytes
	} else if m.Role != User {
		return ErrInvalid
	}
	_, offset := m.CreatedAt.Zone()
	if !utf8.ValidString(m.Content) || strings.TrimSpace(m.Content) == "" || len(m.Content) > max || m.CreatedAt.IsZero() || offset != 0 || m.CreatedAt.Year() < 1 || m.CreatedAt.Year() > 9999 {
		return ErrInvalid
	}
	return nil
}
