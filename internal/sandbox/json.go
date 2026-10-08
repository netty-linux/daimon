package sandbox

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func exactSchema(raw []byte, t reflect.Type) bool {
	if t == reflect.TypeFor[time.Time]() {
		return true
	}
	if t.Kind() == reflect.Slice {
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) != nil || list == nil {
			return false
		}
		for _, v := range list {
			if !exactSchema(v, t.Elem()) {
				return false
			}
		}
		return true
	}
	if t.Kind() != reflect.Struct {
		return true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return false
	}
	known := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.Split(field.Tag.Get("json"), ",")
		name := tag[0]
		known[name] = true
		v, ok := object[name]
		if !ok {
			if len(tag) > 1 && tag[1] == "omitempty" {
				continue
			}
			return false
		}
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) || !exactSchema(v, field.Type) {
			return false
		}
	}
	for key := range object {
		if !known[key] {
			return false
		}
	}
	return true
}

// StrictJSON rejects duplicate keys, ambiguous Unicode, null configuration and
// trailing data. Depth and total input are bounded before domain decoding.
func strictJSON(raw []byte) error {
	if len(raw) > MaxCommandOutput || !utf8.Valid(raw) || !validJSONEscapes(raw) {
		return errorOf(Protocol)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 16 {
			return errorOf(Protocol)
		}
		t, e := d.Token()
		if e != nil {
			return errorOf(Protocol)
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					s, ok := k.(string)
					if e != nil || !ok || seen[s] {
						return errorOf(Protocol)
					}
					seen[s] = true
					if e = visit(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim('}') {
					return errorOf(Protocol)
				}
			case '[':
				for d.More() {
					if e = visit(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(']') {
					return errorOf(Protocol)
				}
			default:
				return errorOf(Protocol)
			}
		}
		return nil
	}
	if e := visit(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errorOf(Protocol)
	}
	return nil
}
func DecodeProfile(raw []byte) (Profile, error) {
	var p Profile
	if len(raw) > 1024 || strictJSON(raw) != nil {
		return p, errorOf(Invalid)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || (len(fields) != 6 && len(fields) != 7) {
		return p, errorOf(Invalid)
	}
	for _, k := range []string{"backend", "image", "runtime", "browser", "resources", "network"} {
		if len(fields[k]) == 0 || bytes.Equal(bytes.TrimSpace(fields[k]), []byte("null")) {
			return p, errorOf(Invalid)
		}
	}
	if v, ok := fields["placement"]; ok && (bytes.Equal(bytes.TrimSpace(v), []byte("null")) || bytes.Equal(bytes.TrimSpace(v), []byte(`""`))) {
		return p, errorOf(Invalid)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || ValidateProfile(p) != nil {
		return p, errorOf(Invalid)
	}
	return p, nil
}

// Go JSON replaces lone UTF-16 surrogates; reject them before interpretation.
func validJSONEscapes(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != 92 {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		v, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if v >= 0xDC00 && v <= 0xDFFF {
			return false
		}
		if v >= 0xD800 && v <= 0xDBFF {
			if i+6 >= len(raw) || raw[i+1] != 92 || raw[i+2] != 'u' {
				return false
			}
			w, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || w < 0xDC00 || w > 0xDFFF {
				return false
			}
			i += 6
		}
	}
	return !inString
}
