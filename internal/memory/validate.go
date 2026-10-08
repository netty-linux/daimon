package memory

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var tagPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func ValidateID(id string) error {
	if !idPattern.MatchString(id) {
		return ErrInvalid
	}
	return nil
}
func ValidateScope(scope Scope, id string) error {
	switch scope {
	case Global:
		if id != "" {
			return ErrInvalid
		}
	case Bot, Thread:
		if ValidateID(id) != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func validTags(tags []string) bool {
	if len(tags) > MaxTags {
		return false
	}
	seen := map[string]bool{}
	for _, tag := range tags {
		if !tagPattern.MatchString(tag) || len(tag) > MaxTagBytes || seen[tag] {
			return false
		}
		seen[tag] = true
	}
	return true
}
func ValidateInput(m Input) error {
	if ValidateID(string(m.ID)) != nil || ValidateScope(m.Scope, m.ScopeID) != nil || !validTags(m.Tags) {
		return ErrInvalid
	}
	switch m.Kind {
	case Fact, Preference, Instruction, Note:
	default:
		return ErrInvalid
	}
	if !utf8.ValidString(m.Content) || strings.TrimSpace(m.Content) == "" || len(m.Content) > MaxContentBytes {
		return ErrInvalid
	}
	return nil
}
func utc(t time.Time) bool {
	_, offset := t.Zone()
	return !t.IsZero() && offset == 0 && t.Year() >= 1 && t.Year() <= 9999
}
func Validate(m Memory) error {
	if ValidateInput(Input{m.ID, m.Scope, m.ScopeID, m.Kind, m.Content, m.Tags}) != nil || !utc(m.CreatedAt) || !utc(m.UpdatedAt) || m.UpdatedAt.Before(m.CreatedAt) || m.Provenance.SourceType != "manual" {
		return ErrInvalid
	}
	return nil
}
