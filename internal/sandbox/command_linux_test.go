//go:build linux

package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	switch os.Getenv("DAIMON_SANDBOX_FIXTURE") {
	case "argv":
		_ = json.NewEncoder(os.Stdout).Encode(os.Args[1:])
		os.Exit(0)
	case "flood":
		fmt.Fprint(os.Stdout, strings.Repeat("x", MaxCommandOutput+4096))
		os.Exit(0)
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("secret", MaxCommandError))
		os.Exit(0)
	case "block":
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestActualExecLiteralArgumentsAndOutputLimits(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	r := &execRunner{executable: exe, stateDir: t.TempDir(), env: []string{"DAIMON_SANDBOX_FIXTURE=argv"}}
	literal := `{"text":"$(host-command); ` + "`host-command`" + `"}`
	raw, code, e := r.Run(context.Background(), []string{"sb", "mcp", "local:guest", "spacesd", "call", "type_text", literal})
	if e != nil || code != 0 {
		t.Fatal(code, e)
	}
	var args []string
	if e = json.Unmarshal(raw, &args); e != nil || args[len(args)-1] != literal || args[0] != "--embedded" || args[1] != "--json" {
		t.Fatal(string(raw), e)
	}
	for _, mode := range []string{"flood", "stderr"} {
		r.env = []string{"DAIMON_SANDBOX_FIXTURE=" + mode}
		_, _, e = r.Run(context.Background(), nil)
		if Category(e) != Capacity || strings.Contains(e.Error(), "secret") {
			t.Fatal(mode, e)
		}
	}
}
func TestActualExecCancellationBounded(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	r := &execRunner{executable: exe, stateDir: t.TempDir(), env: []string{"DAIMON_SANDBOX_FIXTURE=block"}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	begin := time.Now()
	_, _, _ = r.Run(ctx, nil)
	if ctx.Err() == nil || time.Since(begin) > 3*time.Second {
		t.Fatal("unbounded process")
	}
}
