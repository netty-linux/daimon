package harness2

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

// Decode accepts one complete v1 manifest. No unknown/case-aliased/duplicate
// fields, missing components, nulls, malformed Unicode or trailing values.
func Decode(data []byte) (Manifest, error) {
	if len(data) > MaxManifestBytes {
		return Manifest{}, ErrLimit
	}
	if !utf8.Valid(data) || !validStringEscapes(data) || !uniqueJSON(data) {
		return Manifest{}, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || !exact(fields, "version", "bot_id", "base_bot_sha256", "revision", "parent_sha256", "components") {
		return Manifest{}, ErrInvalid
	}
	var components map[string]json.RawMessage
	if json.Unmarshal(fields["components"], &components) != nil || !exact(components, "system_prompt", "guardrails", "memory", "tool_guidance", "subagents", "skills", "scripts", "plugins") {
		return Manifest{}, ErrInvalid
	}
	for _, name := range []string{"subagents", "skills", "scripts", "plugins"} {
		var artifacts []map[string]json.RawMessage
		if json.Unmarshal(components[name], &artifacts) != nil {
			return Manifest{}, ErrInvalid
		}
		for _, a := range artifacts {
			if !exact(a, "id", "sha256") {
				return Manifest{}, ErrInvalid
			}
		}
	}
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil {
		return Manifest{}, ErrInvalid
	}
	if err := Validate(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func exact(fields map[string]json.RawMessage, keys ...string) bool {
	if len(fields) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := fields[k]; !ok {
			return false
		}
	}
	return true
}

func uniqueJSON(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 4 {
			return false
		}
		t, err := d.Token()
		if err != nil || t == nil {
			return false
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				t, err := d.Token()
				k, ok := t.(string)
				if err != nil || !ok || seen[k] {
					return false
				}
				seen[k] = true
				if !walk(depth + 1) {
					return false
				}
			}
		case '[':
			for d.More() {
				if !walk(depth + 1) {
					return false
				}
			}
		default:
			return false
		}
		_, err = d.Token()
		return err == nil
	}
	return walk(0) && d.Decode(new(any)) == io.EOF
}

// Same exact-Unicode rule as native Memory: encoding/json otherwise repairs
// unpaired UTF-16 surrogates and changes the candidate bytes silently.
func validStringEscapes(data []byte) bool {
	quoted := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		value, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value < 0xd800 || value > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return !quoted
}
