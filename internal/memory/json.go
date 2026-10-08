package memory

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

func strictJSON(data []byte) bool {
	if !utf8.Valid(data) || !validStringEscapes(data) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	allowed := map[string]bool{"version": true, "memories": true, "id": true, "scope": true, "scope_id": true, "kind": true, "content": true, "tags": true, "created_at": true, "updated_at": true, "provenance": true, "source_type": true}
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 5 {
			return false
		}
		token, err := d.Token()
		if err != nil || token == nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				t, e := d.Token()
				key, ok := t.(string)
				if e != nil || !ok || !allowed[key] || seen[key] {
					return false
				}
				seen[key] = true
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
func exactObject(raw []byte, required []string, optional ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	allowed := map[string]bool{}
	for _, k := range required {
		allowed[k] = true
		if _, ok := fields[k]; !ok {
			return false
		}
	}
	for _, k := range optional {
		allowed[k] = true
	}
	for k := range fields {
		if !allowed[k] {
			return false
		}
	}
	return true
}

// DecodeInput validates exact spelling, presence, duplicates and Unicode before decoding.
func DecodeInput(data []byte) (Input, error) {
	var input Input
	if !strictJSON(data) || !exactObject(data, []string{"id", "scope", "scope_id", "kind", "content"}, "tags") {
		return input, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || ValidateInput(input) != nil {
		return Input{}, ErrInvalid
	}
	return input, nil
}
