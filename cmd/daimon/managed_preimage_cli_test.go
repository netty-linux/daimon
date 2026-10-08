package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/managedworkspace"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

func TestManagedPreimageExecutableLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	binary := filepath.Join(dir, "daimon")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Env = []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + os.TempDir(), "TMP=" + os.TempDir(), "GOCACHE=" + filepath.Join(dir, "cache"), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local"}
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("offline build: %v %s", e, out)
	}
	base := filepath.Join(dir, "store")
	invoke := func(input string, args ...string) (string, string, error) {
		cmd := exec.CommandContext(ctx, binary, append([]string{"managed-workspace", "--base", base}, args...)...)
		cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot")}
		cmd.Stdin = strings.NewReader(input)
		var out, display bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &display
		e := cmd.Run()
		return out.String(), display.String(), e
	}
	if !managedworkspace.Supported() {
		_, display, e := invoke("", "apply", "--run", "id", "--plan", "p", "--enable-replace-file", "--enable-preimage-retention")
		if e == nil || !strings.Contains(display, "não suportado") {
			t.Fatal("platform allowed", e, display)
		}
		if _, e := os.Stat(base); !os.IsNotExist(e) {
			t.Fatal("platform created store")
		}
		return
	}
	source := filepath.Join(dir, "source")
	exportParent := filepath.Join(dir, "exports")
	for _, p := range []string{source, exportParent} {
		if os.Mkdir(p, 0700) != nil {
			t.Fatal("fixture")
		}
	}
	old := "API_KEY=preimage-cli-canary\npassword=preimage-cli-canary-password\naaa.bbb.ccc\n-----BEGIN PRIVATE KEY-----\npreimage-cli-private-canary\n-----END PRIVATE KEY-----\n"
	if os.WriteFile(filepath.Join(source, "config.txt"), []byte(old), 0640) != nil {
		t.Fatal("source")
	}
	create, _, e := invoke("", "create", "--source", source)
	if e != nil {
		t.Fatal(e)
	}
	runID := ""
	for _, line := range strings.Split(create, "\n") {
		if strings.HasPrefix(line, "Run ID: ") {
			runID = strings.TrimPrefix(line, "Run ID: ")
		}
	}
	if runID == "" {
		t.Fatal("run id")
	}
	other, _, e := invoke("", "create", "--source", source)
	if e != nil {
		t.Fatal(e)
	}
	plan := workspaceplan.Plan{Version: 1, Kind: "workspace_apply", Operations: []workspaceplan.Operation{{Type: "replace_file", Path: "config.txt", Content: "final\n", Precondition: workspaceplan.Precondition{SHA256: workspaceplan.Hash([]byte(old))}, Validation: workspaceplan.Validation{SHA256: workspaceplan.Hash([]byte("final\n"))}}}, Blockers: []string{}, Assumptions: []string{}}
	data, _ := json.Marshal(plan)
	planName := filepath.Join(dir, "plan.json")
	if os.WriteFile(planName, data, 0600) != nil {
		t.Fatal("plan")
	}
	apply, preview, e := invoke("y\n", "apply", "--run", runID, "--plan", planName, "--enable-replace-file", "--enable-preimage-retention")
	if e != nil || !strings.Contains(apply, "succeeded") || !strings.Contains(preview, "Retenção privada") {
		t.Fatal("apply", e, apply, preview)
	}
	check := func(text string) {
		for _, marker := range strings.Split(strings.TrimSpace(old), "\n") {
			if strings.Contains(text, marker) {
				t.Fatal("CLI content leak")
			}
		}
	}
	check(apply)
	check(preview)
	for _, command := range []string{"report", "inspect"} {
		out, display, e := invoke("", command, "--run", runID)
		if e != nil {
			t.Fatal(command, e)
		}
		check(out + display)
		if !strings.Contains(out, "verified") {
			t.Fatal("missing retention", command, out)
		}
	}
	listing, _, e := invoke("", "list")
	if e != nil {
		t.Fatal(e)
	}
	check(listing)
	if strings.Contains(listing, "captured_before_sha256") {
		t.Fatal("list details")
	}
	storage := filepath.Join(base, runID, "preimages")
	entries, e := os.ReadDir(storage)
	if e != nil || len(entries) != 2 {
		t.Fatal("private capture")
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bin") {
			captured, e := os.ReadFile(filepath.Join(storage, entry.Name()))
			if e != nil || string(captured) != old {
				t.Fatal("capture bytes")
			}
		}
	}
	dest := filepath.Join(exportParent, "evidence")
	out, display, e := invoke("y\n", "export-evidence", "--run", runID, "--destination", dest, "--source-check", source, "--enable-export-evidence")
	if e != nil {
		t.Fatal("export", e, out, display)
	}
	check(out + display)
	files, e := os.ReadDir(dest)
	if e != nil || len(files) != 6 {
		t.Fatal("export count")
	}
	for _, f := range files {
		b, e := os.ReadFile(filepath.Join(dest, f.Name()))
		if e != nil {
			t.Fatal(e)
		}
		check(string(b))
		if strings.Contains(string(b), "preimages/") {
			t.Fatal("export storage path")
		}
	}
	out, display, e = invoke("y\n", "discard", "--run", runID, "--enable-discard")
	if e != nil || !strings.Contains(out, "discarded") {
		t.Fatal("discard", e, out, display)
	}
	check(out + display)
	if _, e := os.Stat(filepath.Join(base, runID)); !os.IsNotExist(e) {
		t.Fatal("preimage survives run")
	}
	unchanged, e := os.ReadFile(filepath.Join(source, "config.txt"))
	if e != nil || string(unchanged) != old {
		t.Fatal("source changed")
	}
	otherID := ""
	for _, line := range strings.Split(other, "\n") {
		if strings.HasPrefix(line, "Run ID: ") {
			otherID = strings.TrimPrefix(line, "Run ID: ")
		}
	}
	out, _, e = invoke("", "inspect", "--run", otherID)
	if e != nil || !strings.Contains(out, `"state":"ready"`) {
		t.Fatal("other run changed")
	}
	t.Log("real CLI create/apply/report/inspect/list/export(6 files)/discard: source and other run intact; captured bytes exact; no content in operator outputs")
}
