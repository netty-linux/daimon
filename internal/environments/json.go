package environments

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

func decode(raw []byte, out any) error {
	if len(raw) > 128*1024 || !utf8.Valid(raw) {
		return ErrStorage
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 8 {
			return ErrStorage
		}
		t, e := d.Token()
		if e != nil || t == nil {
			return ErrStorage
		}
		if v, ok := t.(json.Delim); ok {
			switch v {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					s, ok := k.(string)
					if e != nil || !ok || seen[s] {
						return ErrStorage
					}
					seen[s] = true
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim('}') {
					return ErrStorage
				}
			case '[':
				for d.More() {
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(']') {
					return ErrStorage
				}
			default:
				return ErrStorage
			}
		}
		return nil
	}
	if walk(0) != nil {
		return ErrStorage
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrStorage
	}
	if !schema(raw, reflect.TypeOf(out).Elem()) {
		return ErrStorage
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrStorage
	}
	return nil
}
func schema(raw []byte, t reflect.Type) bool {
	if t == reflect.TypeFor[time.Time]() {
		return true
	}
	if t.Kind() == reflect.Slice {
		var a []json.RawMessage
		if json.Unmarshal(raw, &a) != nil || a == nil {
			return false
		}
		for _, b := range a {
			if !schema(b, t.Elem()) {
				return false
			}
		}
		return true
	}
	if t.Kind() != reflect.Struct {
		return true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return false
	}
	known := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		known[name] = true
		b, ok := obj[name]
		if !ok || bytes.Equal(bytes.TrimSpace(b), []byte("null")) || !schema(b, f.Type) {
			return false
		}
	}
	for k := range obj {
		if !known[k] {
			return false
		}
	}
	return true
}
