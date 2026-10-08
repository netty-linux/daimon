package providers

import (
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/providers/groq"
	"github.com/netty-linux/daimon/internal/providers/openai"
)

// CompatibleFactory permits explicit endpoint configurations under a stable ID.
// The existing openai adapter remains the sole owner of HTTP and protocol checks.
func CompatibleFactory(id ID) Factory {
	return Factory{ID: id, Build: func(cfg Config) (model.Model, error) {
		return openai.New(openai.Config{
			BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.Model,
			SystemInstruction: cfg.SystemInstruction, AdditionalContext: cfg.AdditionalContext,
			MaxResponseBytes: cfg.MaxResponseBytes, HTTPClient: cfg.HTTPClient,
		})
	}}
}

// GroqFactory preserves the wrapper's fixed endpoint and default model.
// Unsupported common fields are rejected rather than silently ignored.
func GroqFactory() Factory {
	return Factory{ID: Groq, Build: func(cfg Config) (model.Model, error) {
		if cfg.BaseURL != "" {
			return nil, &openai.ConfigError{Field: "BaseURL"}
		}
		return groq.New(groq.Config{APIKey: cfg.APIKey, Model: cfg.Model,
			MaxResponseBytes: cfg.MaxResponseBytes, HTTPClient: cfg.HTTPClient, SystemInstruction: cfg.SystemInstruction, AdditionalContext: cfg.AdditionalContext})
	}}
}
