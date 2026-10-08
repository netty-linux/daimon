package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/netty-linux/daimon/internal/model"
)

// Provider contains configuration only; each Generate owns its request state.
type Provider struct {
	systemInstruction string
	additionalContext string
	endpoint          string
	apiKey            string
	model             string
	maxResponseBytes  int64
	client            *http.Client
}

var _ model.Model = (*Provider)(nil)

func (p *Provider) Generate(ctx context.Context, input model.ModelRequest) (model.ModelResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ModelResponse{}, err
	}
	body, err := encodeRequestWithContext(p.model, input, p.systemInstruction, p.additionalContext)
	if err != nil {
		return model.ModelResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return model.ModelResponse{}, &RequestError{Reason: "HTTP request construction failed"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "daimon/0.3")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	response, err := p.client.Do(req)
	if err != nil {
		// Client.Do only returns a nonnil response with an error for redirect
		// policy failure, for which net/http has already closed the body.
		if ctx.Err() != nil {
			return model.ModelResponse{}, ctx.Err()
		}
		return model.ModelResponse{}, &TransportError{cause: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, p.maxResponseBytes+1))
	if ctx.Err() != nil {
		return model.ModelResponse{}, ctx.Err()
	}
	if int64(len(data)) > p.maxResponseBytes {
		return model.ModelResponse{}, &ResponseTooLargeError{Limit: p.maxResponseBytes, StatusCode: response.StatusCode}
	}
	if err != nil {
		return model.ModelResponse{}, &TransportError{cause: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return model.ModelResponse{}, p.httpError(response, data)
	}
	return decodeResponse(data)
}

func (p *Provider) httpError(response *http.Response, body []byte) *HTTPError {
	err := &HTTPError{
		StatusCode: response.StatusCode,
		RequestID:  p.metadata(response.Header.Get("x-request-id")),
		RetryAfter: p.metadata(response.Header.Get("Retry-After")),
	}
	// Error messages and raw bodies are intentionally discarded, even if JSON.
	var payload struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil {
		err.Code = p.metadata(payload.Error.Code)
		err.Type = p.metadata(payload.Error.Type)
	}
	return err
}

func (p *Provider) metadata(value string) string {
	// Normalize before redaction so removing control characters cannot recreate
	// a secret which was split across them in an untrusted metadata value.
	value = strings.Map(func(c rune) rune {
		if c >= 0x20 && c <= 0x7e {
			return c
		}
		return -1
	}, value)
	if p.apiKey != "" {
		value = strings.ReplaceAll(value, p.apiKey, "[REDACTED]")
	}
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}
