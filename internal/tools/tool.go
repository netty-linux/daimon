package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type ToolResult struct {
	Content string
	IsError bool
}

// Tool implementations must honor ctx and return ordinary failures as errors.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Execute(ctx context.Context, arguments json.RawMessage) (ToolResult, error)
}

var ErrInvalidArguments = errors.New("invalid tool arguments")

// decodeObject rejects nonobjects, unknown fields, duplicate fields and extra values.
func decodeObject(raw json.RawMessage, field string, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return ErrInvalidArguments
	}
	seen := make(map[string]bool)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return ErrInvalidArguments
		}
		name, ok := key.(string)
		if !ok || name != field || seen[name] {
			return ErrInvalidArguments
		}
		seen[name] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return ErrInvalidArguments
		}
	}
	if _, err := d.Token(); err != nil {
		return ErrInvalidArguments
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return ErrInvalidArguments
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid object fields", ErrInvalidArguments)
	}
	return nil
}
