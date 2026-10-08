// Package providers constructs models without exposing provider selection to the runtime.
package providers

import (
	"errors"
	"net/http"
	"sort"

	"github.com/netty-linux/daimon/internal/model"
)

type ID string

const (
	OpenAI ID = "openai"
	Groq   ID = "groq"
)

var (
	ErrInvalidID      = errors.New("providers: invalid ID")
	ErrInvalidFactory = errors.New("providers: invalid factory")
	ErrDuplicate      = errors.New("providers: duplicate registration")
	ErrUnknown        = errors.New("providers: unknown provider")
)

// RegistryError never includes caller-supplied IDs or configuration values.
type RegistryError struct{ Cause error }

func (*RegistryError) Error() string   { return "providers: registry operation failed" }
func (e *RegistryError) Unwrap() error { return e.Cause }

// Config is the small construction contract shared by today's compatible adapters.
// It is not a universal schema for future protocols. Never log or persist it.
// HTTPClient is an optional caller-owned transport test/configuration seam.
type Config struct {
	BaseURL           string
	APIKey            string
	Model             string
	SystemInstruction string
	AdditionalContext string // Explicit bounded contextual data, separate from instructions/history.
	MaxResponseBytes  int64
	HTTPClient        *http.Client
}

// Factory contains construction behavior only, never configuration or credentials.
// A registered factory is copied; configure the registry before using it.
type Factory struct {
	ID    ID
	Build func(Config) (model.Model, error)
}

func (f Factory) New(cfg Config) (model.Model, error) {
	if !validID(f.ID) {
		return nil, &RegistryError{Cause: ErrInvalidID}
	}
	if f.Build == nil {
		return nil, &RegistryError{Cause: ErrInvalidFactory}
	}
	return f.Build(cfg)
}

// Registry is caller-owned and sequential. Registration and lookup perform no I/O.
// Concurrent mutation is unsupported; no global registry or implicit registration exists.
type Registry struct{ factories map[ID]Factory }

func (r *Registry) Register(f Factory) error {
	if !validID(f.ID) {
		return &RegistryError{Cause: ErrInvalidID}
	}
	if r == nil || f.Build == nil {
		return &RegistryError{Cause: ErrInvalidFactory}
	}
	if _, exists := r.factories[f.ID]; exists {
		return &RegistryError{Cause: ErrDuplicate}
	}
	if r.factories == nil {
		r.factories = make(map[ID]Factory)
	}
	r.factories[f.ID] = f
	return nil
}

func (r *Registry) Get(id ID) (Factory, error) {
	if !validID(id) {
		return Factory{}, &RegistryError{Cause: ErrInvalidID}
	}
	if r != nil {
		if f, ok := r.factories[id]; ok {
			return f, nil
		}
	}
	return Factory{}, &RegistryError{Cause: ErrUnknown}
}

// IDs returns an owned, lexically sorted snapshot.
func (r *Registry) IDs() []ID {
	ids := []ID{}
	if r != nil {
		for id := range r.factories {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func validID(id ID) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i, c := range []byte(id) {
		if c >= 'a' && c <= 'z' {
			continue
		}
		if i > 0 && (c >= '0' && c <= '9' || c == '-') {
			continue
		}
		return false
	}
	return true
}

// ValidateID checks structure only, without resolving a registered factory.
func ValidateID(id ID) error {
	if !validID(id) {
		return &RegistryError{Cause: ErrInvalidID}
	}
	return nil
}
