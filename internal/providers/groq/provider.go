// Package groq configures Groq on the existing Chat Completions adapter.
package groq

import (
	"net/http"
	"strings"

	"github.com/netty-linux/daimon/internal/providers/openai"
)

const BaseURL = "https://api.groq.com/openai/v1"
const DefaultModel = "openai/gpt-oss-20b"

type Config struct {
	APIKey           string
	Model            string
	MaxResponseBytes int64
	HTTPClient       *http.Client
}

// New keeps HTTP, protocol validation and typed errors in the shared adapter.
func New(cfg Config) (*openai.Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, &openai.ConfigError{Field: "APIKey"}
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	return openai.New(openai.Config{BaseURL: BaseURL, APIKey: cfg.APIKey,
		Model: cfg.Model, MaxResponseBytes: cfg.MaxResponseBytes, HTTPClient: cfg.HTTPClient})
}
