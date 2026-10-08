package computer

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This deliberately small CUA argument subset excludes screenshot capture,
// output paths, browser scripts, batch operations and arbitrary driver options.
func actionFields(remote string) []string {
	switch remote {
	case "list_apps":
		return []string{}
	case "list_windows":
		return []string{"pid"}
	case "get_accessibility_tree":
		return []string{"pid", "window_id", "max_elements", "max_depth"}
	case "get_window_state":
		return []string{"pid", "window_id", "include_screenshot", "include_accessibility_tree", "max_elements", "max_depth"}
	case "click":
		return []string{"pid", "window_id", "element_token", "x", "y", "capture_id"}
	case "type_text":
		return []string{"pid", "window_id", "element_token", "text"}
	case "bring_to_front":
		return []string{"pid", "window_id"}
	default:
		return nil
	}
}
func validateAction(remote string, raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) > 64*1024 || !utf8.Valid(raw) || !supported(remote) {
		return nil, ErrArguments
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, ErrArguments
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		tok, err = d.Token()
		key, ok := tok.(string)
		if err != nil || !ok || fields[key] != nil {
			return nil, ErrArguments
		}
		allowed := false
		for _, k := range actionFields(remote) {
			allowed = allowed || k == key
		}
		var v json.RawMessage
		if !allowed || d.Decode(&v) != nil || string(v) == "null" {
			return nil, ErrArguments
		}
		fields[key] = v
		switch key {
		case "pid", "window_id", "max_elements", "max_depth":
			var n int64
			if json.Unmarshal(v, &n) != nil || n <= 0 || n > 2147483647 || (key == "max_elements" && n > 1000) || (key == "max_depth" && n > 64) {
				return nil, ErrArguments
			}
		case "x", "y":
			var n float64
			if json.Unmarshal(v, &n) != nil || math.IsInf(n, 0) || math.IsNaN(n) || n < 0 || n > 100000 {
				return nil, ErrArguments
			}
		case "include_screenshot", "include_accessibility_tree":
			var b bool
			if json.Unmarshal(v, &b) != nil || (key == "include_screenshot" && b) || (key == "include_accessibility_tree" && !b) {
				return nil, ErrArguments
			}
		default:
			var text string
			if json.Unmarshal(v, &text) != nil || !validUnicodeEscapes(v) || text == "" || !utf8.ValidString(text) || len(text) > 16384 || (key != "text" && len(text) > 256) {
				return nil, ErrArguments
			}
		}
	}
	if tok, err = d.Token(); err != nil || tok != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return nil, ErrArguments
	}
	if remote == "get_window_state" && string(fields["include_screenshot"]) != "false" {
		return nil, ErrArguments
	}
	targetRequired := remote != "list_apps" && remote != "list_windows"
	token := fields["element_token"] != nil
	if targetRequired && !token && (fields["pid"] == nil || fields["window_id"] == nil) {
		return nil, ErrArguments
	}
	if remote == "click" {
		coords := fields["x"] != nil || fields["y"] != nil
		if token == coords || (coords && (fields["x"] == nil || fields["y"] == nil)) {
			return nil, ErrArguments
		}
	}
	if remote == "type_text" && fields["text"] == nil {
		return nil, ErrArguments
	}
	return fields, nil
}

// Go's JSON decoder replaces unpaired UTF-16 surrogates; reject them so the
// reviewed text cannot differ from another runtime's interpretation of raw args.
func validUnicodeEscapes(raw []byte) bool {
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
func actionSchema(remote string) json.RawMessage {
	props := map[string]any{}
	for _, k := range actionFields(remote) {
		t := "string"
		switch k {
		case "pid", "window_id", "max_elements", "max_depth":
			t = "integer"
		case "x", "y":
			t = "number"
		case "include_screenshot", "include_accessibility_tree":
			t = "boolean"
		}
		prop := map[string]any{"type": t}
		if k == "include_screenshot" {
			prop["const"] = false
		}
		if k == "include_accessibility_tree" {
			prop["const"] = true
		}
		props[k] = prop
	}
	required := []string{}
	if remote == "get_window_state" {
		required = append(required, "pid", "window_id", "include_screenshot")
	}
	if remote == "get_accessibility_tree" || remote == "bring_to_front" {
		required = append(required, "pid", "window_id")
	}
	if remote == "type_text" {
		required = append(required, "text")
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false})
	return raw
}

type Presentation struct {
	Action          Class
	Target, Preview string
}

func describeAction(remote string, args json.RawMessage) (Presentation, error) {
	fields, err := validateAction(remote, args)
	if err != nil {
		return Presentation{}, err
	}
	keys := []string{}
	for key := range fields {
		if key != "text" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	lines := []string{remote + " on the user's local computer"}
	for _, key := range keys {
		value := string(fields[key])
		var text string
		if json.Unmarshal(fields[key], &text) == nil {
			value = strconv.QuoteToASCII(text)
		}
		lines = append(lines, key+": "+value)
	}
	p := Presentation{Action: Classify(remote), Target: strings.Join(lines, "\n")}
	if text, ok := fields["text"]; ok {
		var v string
		_ = json.Unmarshal(text, &v)
		p.Preview = "Exact text to type (complete ASCII escapes):\n" + strconv.QuoteToASCII(v)
	}
	return p, nil
}
