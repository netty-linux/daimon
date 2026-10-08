package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type runnerFunc func(context.Context, []string) ([]byte, int, error)

func (f runnerFunc) Run(ctx context.Context, a []string) ([]byte, int, error) { return f(ctx, a) }
func TestCLIExitCodesAndNoRawError(t *testing.T) {
	for code, kind := range map[int]ErrorKind{1: Provision, 2: Invalid, 3: NotFound, 4: Unsupported, 5: Transport, 6: Permission, 7: DiskFull, 130: Canceled} {
		c := &CUA{runner: runnerFunc(func(context.Context, []string) ([]byte, int, error) {
			return []byte("SECRET stderr and endpoint"), code, nil
		})}
		_, e := c.run(context.Background(), "sb", "info")
		if !errors.Is(e, errorOf(kind)) || strings.Contains(e.Error(), "SECRET") {
			t.Fatal(code, e)
		}
	}
}
func TestCLIExplicitEnvironmentNoSecretsOrShell(t *testing.T) {
	dir := t.TempDir()
	for _, env := range [][]string{{"DAIMON_API_KEY=secret"}, {"OPENAI_API_KEY=secret"}, {"CUA_HOME=/arbitrary"}, {"PATH=ok\nsecret"}} {
		if _, e := NewCUA(filepath.Join(dir, "cua"), dir, env); e == nil {
			t.Fatal(env)
		}
	}
	for _, exe := range []string{"sh", "cmd.exe", "powershell.exe", "pwsh"} {
		if _, e := NewCUA(filepath.Join(dir, exe), dir, nil); e == nil {
			t.Fatal(exe)
		}
	}
	c, e := NewCUA(filepath.Join(dir, "cua"), dir, []string{"PATH=/bin"})
	if e != nil {
		t.Fatal(e)
	}
	r := c.runner.(*execRunner)
	if len(r.env) != 3 || r.env[1] != "CUA_HOME="+dir || r.env[2] != "CUA_NO_DAEMON_AUTOSTART=1" {
		t.Fatal(r.env)
	}
}
func TestCLIGetDeleteExactOwnedRefAndJSON(t *testing.T) {
	ref := "local:daimon-" + strings.Repeat("a", 24)
	c := &CUA{}
	var calls [][]string
	c.runner = runnerFunc(func(ctx context.Context, args []string) ([]byte, int, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[1] == "rm" {
			raw, e := json.Marshal(struct {
				Deleted string `json:"deleted"`
				Missing bool   `json:"missing"`
			}{ref, false})
			return raw, 0, e
		}
		return []byte(`{"id":"` + ref + `","name":"` + strings.TrimPrefix(ref, "local:") + `","runtime":"gvisor","location":"local","kind":"container","state":"ready","image":null}`), 0, nil
	})
	if e := c.Delete(context.Background(), ref); e != nil {
		t.Fatal(e)
	}
	if len(calls) != 2 || strings.Join(calls[1], " ") != "sb rm "+ref+" --local --force" {
		t.Fatal(calls)
	}
	before := len(calls)
	for _, bad := range []string{"cloud:guest", "local:unknown", ref + ";evil"} {
		if e := c.Delete(context.Background(), bad); e == nil {
			t.Fatal(bad)
		}
	}
	if len(calls) != before {
		t.Fatal("invalid ref reached process")
	}
}
func TestStrictJSONAndBoundedOutput(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":1} {}`, `{"x":"\ud800"}`, `{"x":"\udc00"}`, strings.Repeat("[", 20) + "0" + strings.Repeat("]", 20)} {
		if strictJSON([]byte(raw)) == nil {
			t.Fatal(raw)
		}
	}
	b := &boundedOutput{limit: 3}
	_, _ = b.Write([]byte("abcdef"))
	if !b.overflow || string(b.data) != "abc" {
		t.Fatal(b)
	}
}
