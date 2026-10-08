package computer

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

func DecodeProfile(raw []byte) (Profile, error) {
	var p Profile
	if len(raw) > 256 || !utf8.Valid(raw) {
		return p, ErrConfig
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return p, ErrConfig
	}
	seen := map[string]bool{}
	for d.More() {
		tok, err = d.Token()
		key, ok := tok.(string)
		if err != nil || !ok || seen[key] {
			return p, ErrConfig
		}
		seen[key] = true
		var v json.RawMessage
		if d.Decode(&v) != nil || string(v) == "null" {
			return p, ErrConfig
		}
		switch key {
		case "enabled":
			err = json.Unmarshal(v, &p.Enabled)
		case "backend":
			err = json.Unmarshal(v, &p.Backend)
		case "mcp_server_id":
			err = json.Unmarshal(v, &p.MCPServerID)
		default:
			return p, ErrConfig
		}
		if err != nil {
			return p, ErrConfig
		}
	}
	if tok, err = d.Token(); err != nil || tok != json.Delim('}') || len(seen) != 3 || d.Decode(new(any)) != io.EOF || (ValidateProfile(p) != nil || p.Backend != CUALocal) {
		return p, ErrConfig
	}
	return p, nil
}
