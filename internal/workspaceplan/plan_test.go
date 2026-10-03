package workspaceplan

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func fixture() Plan {
	absent := true
	return Plan{Version: 1, Kind: "workspace_apply", Blockers: []string{}, Assumptions: []string{}, Operations: []Operation{
		{Type: "create_file", Path: "docs/NOTES.md", Content: "Fixture\n", Precondition: Precondition{Absent: &absent}, Validation: Validation{SHA256: Hash([]byte("Fixture\n"))}},
		{Type: "replace_file", Path: "src/config.txt", Content: "mode=final\n", Precondition: Precondition{SHA256: Hash([]byte("mode=initial\n"))}, Validation: Validation{SHA256: Hash([]byte("mode=final\n"))}},
	}}
}
func encoded(t *testing.T, p Plan) []byte {
	t.Helper()
	b, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestPlanValidAndBoundaries(t *testing.T) {
	l := DefaultLimits()
	p := fixture()
	if _, e := Parse(encoded(t, p), l); e != nil {
		t.Fatal(e)
	}
	l.PlanBytes = len(encoded(t, p))
	if _, e := Parse(encoded(t, p), l); e != nil {
		t.Fatal(e)
	}
	l.PlanBytes--
	if _, e := Parse(encoded(t, p), l); !errors.Is(e, ErrLimit) {
		t.Fatal("plan byte limit")
	}
	l = DefaultLimits()
	p.Operations[0].Content = strings.Repeat("x", l.FileBytes)
	p.Operations[0].Validation.SHA256 = Hash([]byte(p.Operations[0].Content))
	if _, e := Parse(encoded(t, p), l); e != nil {
		t.Fatal(e)
	}
	p.Operations[0].Content += "x"
	p.Operations[0].Validation.SHA256 = Hash([]byte(p.Operations[0].Content))
	if _, e := Parse(encoded(t, p), l); !errors.Is(e, ErrLimit) {
		t.Fatal("file byte limit")
	}
	l = DefaultLimits()
	l.TotalBytes = 1
	if _, e := Parse(encoded(t, fixture()), l); !errors.Is(e, ErrLimit) {
		t.Fatal("total limit")
	}
}
func TestInvalidPlans(t *testing.T) {
	base := string(encoded(t, fixture()))
	for _, data := range []string{
		"```json\n" + base + "\n```", base + base, "prefix " + base, base + " residual", "[]", "null", "{",
		strings.Replace(base, `"version":1`, `"version":2`, 1), strings.Replace(base, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(base, `"kind":`, `"Kind":`, 1), strings.Replace(base, `"kind":`, `"extra":true,"kind":`, 1),
		strings.Replace(base, `"create_file"`, `"delete_file"`, 1), strings.Replace(base, `"content":"Fixture\n",`, "", 1),
		strings.Replace(base, `"absent":true`, `"absent":false`, 1), strings.Replace(base, `"absent":true`, `"absent":null`, 1),
		strings.Replace(base, `"blockers":[]`, `"blockers":null`, 1), strings.Replace(base, `"blockers":[]`, `"blockers":["unknown"]`, 1),
		strings.Replace(base, `"precondition":{"absent":true}`, `"precondition":{}`, 1),
	} {
		if _, e := Parse([]byte(data), DefaultLimits()); e == nil {
			t.Fatal("invalid plan accepted")
		}
	}
	for _, path := range []string{"../escape", "/absolute", "docs/../escape", "docs//a", `docs\a`, ".", "CON", "docs/x.", "docs/\x00x", "docs/é", "src/config.txt"} {
		p := fixture()
		p.Operations[0].Path = path
		if _, e := Parse(encoded(t, p), DefaultLimits()); e == nil {
			t.Fatal("invalid/conflicting path", path)
		}
	}
	for _, mutate := range []func(*Plan){
		func(p *Plan) { p.Operations = nil }, func(p *Plan) { p.Operations = append(p.Operations, p.Operations[0]) },
		func(p *Plan) { p.Operations[0].Validation.SHA256 = strings.Repeat("0", 64) },
		func(p *Plan) { p.Operations[1].Precondition.SHA256 = "" },
		func(p *Plan) { p.Operations[1].Path = "DOCS/notes.md" },
		func(p *Plan) { p.Operations[0].Path = "src" },
	} {
		p := fixture()
		mutate(&p)
		if _, e := Parse(encoded(t, p), DefaultLimits()); e == nil {
			t.Fatal("invalid operation accepted")
		}
	}
}
func TestPathDepthLimit(t *testing.T) {
	l := DefaultLimits()
	p := strings.Repeat("a/", l.PathDepth-1) + "file"
	if !ValidPath(p, l) || ValidPath("a/"+p, l) {
		t.Fatal("depth boundary")
	}
	l = Limits{}
	if ValidPath("file", l) {
		t.Fatal("zero limits allowed")
	}
}

func TestCriticalLimitBoundaries(t *testing.T) {
	p := fixture()
	l := DefaultLimits()
	total := len(p.Operations[0].Content) + len(p.Operations[1].Content)
	l.TotalBytes = total
	if _, e := Parse(encoded(t, p), l); e != nil {
		t.Fatal(e)
	}
	l.TotalBytes--
	if _, e := Parse(encoded(t, p), l); !errors.Is(e, ErrLimit) {
		t.Fatal("total boundary")
	}
	l = DefaultLimits()
	l.Operations = 1
	if _, e := Parse(encoded(t, p), l); !errors.Is(e, ErrLimit) {
		t.Fatal("operation budget")
	}
	l = DefaultLimits()
	p = fixture()
	p.Operations[1] = p.Operations[0]
	p.Operations[1].Path = "docs/SECOND.md"
	if _, e := Parse(encoded(t, p), l); !errors.Is(e, ErrLimit) {
		t.Fatal("creation count")
	}
	p = fixture()
	p.Operations[0] = p.Operations[1]
	p.Operations[0].Path = "src/other.txt"
	if _, e := Parse(encoded(t, p), l); !errors.Is(e, ErrLimit) {
		t.Fatal("replacement count")
	}
	l.PathBytes = 4
	if !ValidPath("file", l) || ValidPath("files", l) {
		t.Fatal("path byte boundary")
	}
	p = fixture()
	p.Operations = p.Operations[:1]
	p.Operations[0].Content = ""
	p.Operations[0].Validation.SHA256 = Hash(nil)
	if _, e := Parse(encoded(t, p), DefaultLimits()); e != nil {
		t.Fatal("explicit empty file refused", e)
	}
}
