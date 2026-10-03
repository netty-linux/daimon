// Package workspaceplan parses bounded, versioned, deterministic artifacts.
// It neither calls a model nor performs filesystem operations.
package workspaceplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalid = errors.New("invalid structured workspace plan")
	ErrLimit   = errors.New("structured workspace plan exceeds limits")
	ErrBlocked = errors.New("structured workspace plan has blockers")
)

type Limits struct{ PlanBytes, Operations, Creates, Replaces, FileBytes, TotalBytes, PathBytes, PathDepth int }

func DefaultLimits() Limits { return Limits{256 * 1024, 2, 1, 1, 64 * 1024, 128 * 1024, 4096, 16} }
func (l Limits) valid() bool {
	return l.PlanBytes > 0 && l.PlanBytes <= 1024*1024 && l.Operations > 0 && l.Operations <= 3 && l.Creates > 0 && l.Creates <= 1 && l.Replaces > 0 && l.Replaces <= 1 && l.FileBytes > 0 && l.FileBytes <= 64*1024 && l.TotalBytes > 0 && l.TotalBytes <= 128*1024 && l.PathBytes > 0 && l.PathBytes <= 4096 && l.PathDepth > 0 && l.PathDepth <= 16
}

type Precondition struct {
	Absent *bool  `json:"absent,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}
type Validation struct {
	SHA256 string `json:"sha256"`
}
type Operation struct {
	Type         string       `json:"type"`
	Path         string       `json:"path"`
	Content      string       `json:"content"`
	Precondition Precondition `json:"precondition"`
	Validation   Validation   `json:"validation"`
}
type Plan struct {
	Version     int         `json:"version"`
	Kind        string      `json:"kind"`
	Operations  []Operation `json:"operations"`
	Blockers    []string    `json:"blockers"`
	Assumptions []string    `json:"assumptions"`
}

func Hash(content []byte) string { h := sha256.Sum256(content); return hex.EncodeToString(h[:]) }
func hashValid(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Paths deliberately use a portable ASCII subset. Exact case is retained.
func ValidPath(p string, l Limits) bool {
	if !l.valid() || len(p) > l.PathBytes || !fs.ValidPath(p) || p == "." || strings.Count(p, "/")+1 > l.PathDepth {
		return false
	}
	for _, c := range p {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-/", c)) {
			return false
		}
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasSuffix(part, ".") {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
			return false
		}
		if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9' {
			return false
		}
	}
	return true
}

func Parse(data []byte, l Limits) (Plan, error) {
	var p Plan
	if !l.valid() {
		return p, ErrInvalid
	}
	if len(data) > l.PlanBytes {
		return p, ErrLimit
	}
	if !utf8.Valid(data) || !uniqueKeys(data) {
		return p, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF {
		return Plan{}, ErrInvalid
	}
	if p.Version != 1 || p.Kind != "workspace_apply" || p.Operations == nil || len(p.Operations) == 0 || p.Blockers == nil || p.Assumptions == nil {
		return Plan{}, ErrInvalid
	}
	if len(p.Operations) > l.Operations || len(p.Blockers) > 4 || len(p.Assumptions) > 4 {
		return Plan{}, ErrLimit
	}
	if len(p.Blockers) != 0 {
		return Plan{}, ErrBlocked
	}
	for _, a := range p.Assumptions {
		if len(a) > 256 {
			return Plan{}, ErrLimit
		}
	}
	seen := map[string]bool{}
	total, creates, replaces := 0, 0, 0
	for _, op := range p.Operations {
		// ASCII case-insensitive duplicate rejection also protects case-folding filesystems.
		key := strings.ToLower(op.Path)
		if !ValidPath(op.Path, l) || seen[key] || !utf8.ValidString(op.Content) {
			return Plan{}, ErrInvalid
		}
		seen[key] = true
		for other := range seen {
			if other != key && (strings.HasPrefix(key, other+"/") || strings.HasPrefix(other, key+"/")) {
				return Plan{}, ErrInvalid
			}
		}
		if len(op.Content) > l.FileBytes || len(op.Content) > l.TotalBytes-total {
			return Plan{}, ErrLimit
		}
		total += len(op.Content)
		if !hashValid(op.Validation.SHA256) || op.Validation.SHA256 != Hash([]byte(op.Content)) {
			return Plan{}, ErrInvalid
		}
		switch op.Type {
		case "create_file":
			creates++
			if op.Precondition.Absent == nil || !*op.Precondition.Absent || op.Precondition.SHA256 != "" {
				return Plan{}, ErrInvalid
			}
		case "replace_file":
			replaces++
			if op.Precondition.Absent != nil || !hashValid(op.Precondition.SHA256) {
				return Plan{}, ErrInvalid
			}
		default:
			return Plan{}, ErrInvalid
		}
	}
	if creates > l.Creates || replaces > l.Replaces {
		return Plan{}, ErrLimit
	}
	return p, nil
}

// A bounded token pass rejects duplicate keys, nulls, wrong key spelling and
// excessive nesting before decoding. Schema-specific types remain strict below.
func uniqueKeys(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	allowed := map[string]bool{"version": true, "kind": true, "operations": true, "blockers": true, "assumptions": true, "type": true, "path": true, "content": true, "precondition": true, "validation": true, "absent": true, "sha256": true}
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 5 {
			return false
		}
		t, err := d.Token()
		if err != nil || t == nil {
			return false
		}
		c, ok := t.(json.Delim)
		if !ok {
			return true
		}
		if c != '{' && c != '[' {
			return false
		}
		keys := map[string]bool{}
		count := 0
		for d.More() {
			count++
			if count > 16 {
				return false
			}
			if c == '{' {
				k, e := d.Token()
				s, ok := k.(string)
				if e != nil || !ok || !allowed[s] || keys[s] {
					return false
				}
				keys[s] = true
			}
			if !walk(depth + 1) {
				return false
			}
		}
		if c == '{' && depth == 0 {
			for _, k := range []string{"version", "kind", "operations", "blockers", "assumptions"} {
				if !keys[k] {
					return false
				}
			}
		}
		if c == '{' && depth == 2 {
			for _, k := range []string{"type", "path", "content", "precondition", "validation"} {
				if !keys[k] {
					return false
				}
			}
		}
		end, e := d.Token()
		if e != nil || end != map[json.Delim]json.Delim{'{': '}', '[': ']'}[c] {
			return false
		}
		return true
	}
	return walk(0) && func() bool { _, err := d.Token(); return err == io.EOF }()
}
