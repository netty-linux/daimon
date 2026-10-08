// Package mcp supplies bounded stdio tools. It owns no agent execution authority.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	ProtocolVersion   = "2025-11-25"
	CUALegacyProtocol = "2025-06-18"
	MaxConfigBytes    = 256 * 1024
	MaxServers        = 16
	MaxTools          = 64
	MaxTotalTools     = 256
	MaxSchemaBytes    = 32 * 1024
	MaxArgumentBytes  = 64 * 1024
	MaxResultBytes    = 64 * 1024
	MaxMessageBytes   = 1024 * 1024
)

var (
	ErrConfig      = errors.New("mcp: invalid configuration")
	ErrProtocol    = errors.New("mcp: protocol violation")
	ErrUnavailable = errors.New("mcp: unavailable")
	ErrLimit       = errors.New("mcp: size or capacity limit")
	ErrRemote      = errors.New("mcp: remote request failed")
	ErrUnsupported = errors.New("mcp: unsupported content or feature")
	ErrArguments   = errors.New("mcp: invalid arguments")
	ErrDenied      = errors.New("mcp: capability denied")
	serverName     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	toolName       = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

type Classification string

const (
	Read  Classification = "read"
	Write Classification = "write"
	Other Classification = "other"
)

type ToolConfig struct {
	Classification Classification `json:"classification"`
}
type ServerConfig struct {
	ComputerBackend string                `json:"computer_backend,omitempty"`
	ID              string                `json:"id"`
	Command         string                `json:"command"`
	Args            []string              `json:"args"`
	Enabled         bool                  `json:"enabled"`
	Tools           map[string]ToolConfig `json:"tools"`
}
type Config struct {
	Version int            `json:"version"`
	Servers []ServerConfig `json:"servers"`
}

// No normalization, hashing or lossy aliases. Native Bot name bounds stay intact.
func Name(server, tool string) (string, error) {
	name := "mcp__" + server + "__" + tool
	if !serverName.MatchString(server) || !toolName.MatchString(tool) || len(name) > 64 {
		return "", ErrConfig
	}
	return name, nil
}
func IsName(name string) bool {
	parts := strings.SplitN(name, "__", 3)
	if len(parts) != 3 || parts[0] != "mcp" {
		return false
	}
	canonical, err := Name(parts[1], parts[2])
	return err == nil && canonical == name
}
func Validate(c Config) error {
	if c.Version != 1 || c.Servers == nil || len(c.Servers) > MaxServers {
		return ErrConfig
	}
	seen := map[string]bool{}
	computerCount := 0
	for _, s := range c.Servers {
		if !serverName.MatchString(s.ID) || seen[s.ID] || !filepath.IsAbs(s.Command) || len(s.Command) > 4096 || strings.ContainsAny(s.Command, "\x00\r\n") || !utf8.ValidString(s.Command) || s.Args == nil || len(s.Args) > 32 || s.Tools == nil || len(s.Tools) > MaxTools {
			return ErrConfig
		}
		seen[s.ID] = true
		base := strings.TrimSuffix(strings.ToLower(filepath.Base(s.Command)), ".exe")
		if base == "cua-driver" && s.ComputerBackend == "" {
			return ErrConfig
		}
		switch base {
		case "sh", "bash", "dash", "zsh", "fish", "cmd", "powershell", "pwsh":
			return ErrConfig
		}
		if s.ComputerBackend != "" {
			computerCount++
			if s.ComputerBackend != "cua-local" || computerCount > 1 || base != "cua-driver" || len(s.Args) != 1 || s.Args[0] != "mcp" {
				return ErrConfig
			}
		}
		total := 0
		for _, arg := range s.Args {
			if len(arg) > 8192 || strings.ContainsRune(arg, 0) || !utf8.ValidString(arg) {
				return ErrConfig
			}
			total += len(arg)
		}
		if total > 32*1024 {
			return ErrConfig
		}
		for name, t := range s.Tools {
			if _, err := Name(s.ID, name); err != nil {
				return ErrConfig
			}
			if t.Classification != Read && t.Classification != Write && t.Classification != Other {
				return ErrConfig
			}
		}
	}
	return nil
}
func DecodeConfig(raw []byte) (Config, error) {
	var c Config
	if len(raw) > MaxConfigBytes {
		return c, ErrLimit
	}
	if strictJSON(raw) != nil {
		return c, ErrConfig
	}
	// Required keys and exact case are checked separately from Go's permissive decoder.
	object, err := object(raw)
	if err != nil || !keys(object, "version", "servers") {
		return c, ErrConfig
	}
	var servers []json.RawMessage
	if json.Unmarshal(object["servers"], &servers) != nil || servers == nil {
		return c, ErrConfig
	}
	for _, rawServer := range servers {
		fields, e := objectMap(rawServer)
		if backend, ok := fields["computer_backend"]; ok {
			var value string
			if json.Unmarshal(backend, &value) != nil || value != "cua-local" {
				return c, ErrConfig
			}
			delete(fields, "computer_backend")
		}
		if e != nil || !keys(fields, "id", "command", "args", "enabled", "tools") {
			return c, ErrConfig
		}
		declarations, e := objectMap(fields["tools"])
		if e != nil {
			return c, ErrConfig
		}
		for _, rawTool := range declarations {
			fields, e := objectMap(rawTool)
			if e != nil || !keys(fields, "classification") {
				return c, ErrConfig
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || Validate(c) != nil {
		return Config{}, ErrConfig
	}
	return c, nil
}
func LoadConfig(path string, optional bool) (Config, error) {
	empty := Config{Version: 1, Servers: []ServerConfig{}}
	info, err := os.Lstat(path)
	if optional && errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Config{}, ErrConfig
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, ErrConfig
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return Config{}, ErrConfig
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return Config{}, ErrConfig
	}
	return DecodeConfig(raw)
}

// Duplicate keys and excessive nesting are rejected before any protocol/config use.
func strictJSON(raw []byte) error {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	tokens := 0
	var walk func(int) error
	walk = func(depth int) error {
		tokens++
		if depth > 64 || tokens > 65536 {
			return ErrLimit
		}
		tok, err := d.Token()
		if err != nil {
			return ErrProtocol
		}
		delimiter, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return ErrProtocol
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return ErrProtocol
				}
				seen[s] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return ErrProtocol
		}
		_, err = d.Token()
		if err != nil {
			return ErrProtocol
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrProtocol
	}
	return nil
}
func objectMap(raw json.RawMessage) (map[string]json.RawMessage, error) { return object(raw) }
func object(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var v map[string]json.RawMessage
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &v) != nil || v == nil {
		return nil, ErrProtocol
	}
	return v, nil
}
func keys(v map[string]json.RawMessage, names ...string) bool {
	if len(v) != len(names) {
		return false
	}
	for _, name := range names {
		value, ok := v[name]
		if !ok || bytes.Equal(value, []byte("null")) {
			return false
		}
	}
	return true
}
