package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/workspaceplan"
)

func TestManagedArguments(t *testing.T) {
	for _, tail := range [][]string{{"create", "--source", "src"}, {"apply", "--run", "id", "--plan", "p"}, {"apply", "--plan", "p", "--run", "id"}, {"report", "--run", "id"}, {"list"}, {"inspect", "--run", "id"}, {"discard", "--run", "id", "--enable-discard"}, {"discard", "--enable-discard", "--run", "id"}} {
		if _, _, _, err := managedArguments(append([]string{"--base", "store"}, tail...)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tail := range [][]string{{"discard", "--run", "id"}, {"discard", "--run", "id", "--enable-discard", "--enable-discard"}, {"list", "--run", "id"}, {"inspect"}, {"inspect", "--run", "id", "--unknown"}, {"discard", "--enable-discard"}} {
		if _, _, _, err := managedArguments(append([]string{"--base", "store"}, tail...)); err == nil {
			t.Fatal("invalid lifecycle args accepted", tail)
		}
	}
	for _, args := range [][]string{nil, {"--base", "store"}, {"--base", "store", "apply"}, {"--base", "store", "apply", "--run", "id"}, {"--base", "store", "report", "--run", "id", "--run", "id"}, {"--base", "store", "create", "--source", ""}, {"--base", "store", "create", "--root", "src"}, {"--base", "store", "delete", "--run", "id"}} {
		if _, _, _, err := managedArguments(args); err == nil {
			t.Fatal("invalid args accepted", args)
		}
	}
}

func TestSharedRootApplyStillBlocked(t *testing.T) {
	root := t.TempDir()
	var out, display bytes.Buffer
	err := runWithContext(context.Background(), []string{"workspace", "--root", root, "apply-plan", "plan.json"}, strings.NewReader("y\n"), &out, &display, func(string) string { t.Fatal("blocked apply accessed provider environment"); return "" })
	if err == nil {
		t.Fatal("shared root apply allowed")
	}
	files, e := os.ReadDir(root)
	if e != nil || len(files) != 0 {
		t.Fatal("blocked apply wrote")
	}
}

// Real executable with an explicit credential-free environment. Linux exercises
// create/apply/report; Windows verifies unsupported rejection without effects.
func TestManagedCLIExecutable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	base := filepath.Join(t.TempDir(), "store")
	source := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "config.txt"), []byte("initial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "daimon")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Env = []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + os.TempDir(), "TMP=" + os.TempDir(), "GOCACHE=" + filepath.Join(t.TempDir(), "cache"), "GOPROXY=off", "GOTOOLCHAIN=local", "GOSUMDB=off"}
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("offline build: %v %s", err, output)
	}
	invoke := func(input string, args ...string) (string, string, error) {
		command := exec.CommandContext(ctx, binary, args...)
		command.Env = []string{}
		command.Stdin = strings.NewReader(input)
		var out, display bytes.Buffer
		command.Stdout = &out
		command.Stderr = &display
		err := command.Run()
		return out.String(), display.String(), err
	}
	out, display, err := invoke("", "managed-workspace", "--base", base, "create", "--source", source)
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		if err == nil || !strings.Contains(display, "não suportado") {
			t.Fatal("unsupported platform enabled", out, display, err)
		}
		if _, e := os.Stat(base); !os.IsNotExist(e) {
			t.Fatal("unsupported command wrote")
		}
		for _, tail := range [][]string{{"list"}, {"inspect", "--run", strings.Repeat("a", 32)}, {"discard", "--run", strings.Repeat("a", 32), "--enable-discard"}} {
			args := append([]string{"managed-workspace", "--base", base}, tail...)
			out, display, err = invoke("y\n", args...)
			if err == nil || !strings.Contains(display, "não suportado") {
				t.Fatal("unsupported lifecycle enabled", out, display, err)
			}
			if _, e := os.Stat(base); !os.IsNotExist(e) {
				t.Fatal("unsupported lifecycle provisioned")
			}
		}
		return
	}
	if err != nil {
		t.Fatal(out, display, err)
	}
	id := strings.Split(strings.Split(out, "Run ID: ")[1], "\n")[0]
	planFile := filepath.Join(t.TempDir(), "plan.json")
	// SHA-256 values are deterministic fixtures, never credentials.
	plan := `{"version":1,"kind":"workspace_apply","operations":[{"type":"replace_file","path":"config.txt","content":"final\n","precondition":{"sha256":"` + workspaceplan.Hash([]byte("initial\n")) + `"},"validation":{"sha256":"` + workspaceplan.Hash([]byte("final\n")) + `"}}],"blockers":[],"assumptions":[]}`
	if err := os.WriteFile(planFile, []byte(plan), 0600); err != nil {
		t.Fatal(err)
	}
	out, display, err = invoke("y\n", "managed-workspace", "--base", base, "apply", "--run", id, "--plan", planFile)
	if err != nil || !strings.Contains(out, "Estado: succeeded") || !strings.Contains(display, "config.txt") || !strings.Contains(display, "final") || strings.Count(display, "Aprovar esta proposta uma vez?") != 1 {
		t.Fatal(out, display, err)
	}
	if strings.Contains(out, "config.txt") || strings.Contains(out, "final") {
		t.Fatal("public summary leaked")
	}
	original, e := os.ReadFile(filepath.Join(source, "config.txt"))
	if e != nil || string(original) != "initial\n" {
		t.Fatal("source changed")
	}
	final, e := os.ReadFile(filepath.Join(base, id, "output", "config.txt"))
	if e != nil || string(final) != "final\n" {
		t.Fatal("managed bytes differ")
	}
	out, display, err = invoke("", "managed-workspace", "--base", base, "report", "--run", id)
	if err != nil || !strings.Contains(out, "Estado: succeeded") {
		t.Fatal(out, display, err)
	}
	if _, _, err := invoke("y\n", "managed-workspace", "--base", base, "apply", "--run", id, "--plan", planFile); err == nil {
		t.Fatal("CLI reused approval/run")
	}
	out, display, err = invoke("", "managed-workspace", "--base", base, "create", "--source", source)
	if err != nil {
		t.Fatal(out, display, err)
	}
	otherID := strings.Split(strings.Split(out, "Run ID: ")[1], "\n")[0]
	for _, command := range []string{"list", "inspect"} {
		args := []string{"managed-workspace", "--base", base, command}
		if command == "inspect" {
			args = append(args, "--run", id)
		}
		out, display, err = invoke("", args...)
		if err != nil || !strings.Contains(out, `"state":"succeeded"`) || strings.Contains(out, "config.txt") || strings.Contains(out, "initial\\n") {
			t.Fatal(out, display, err)
		}
	}
	if _, display, err = invoke("y\n", "managed-workspace", "--base", base, "discard", "--run", id); err == nil || strings.Contains(display, "Aprovar esta proposta") {
		t.Fatal("absent opt-in approved")
	}
	if _, _, err = invoke("n\n", "managed-workspace", "--base", base, "discard", "--run", id, "--enable-discard"); err == nil {
		t.Fatal("denial accepted")
	}
	if _, e = os.Stat(filepath.Join(base, id)); e != nil {
		t.Fatal("denial removed run")
	}
	out, display, err = invoke("y\n", "managed-workspace", "--base", base, "discard", "--run", id, "--enable-discard")
	if err != nil || !strings.Contains(out, "Estado do descarte: discarded") || strings.Count(display, "Aprovar esta proposta uma vez?") != 1 || strings.Contains(display, "final\\n") {
		t.Fatal(out, display, err)
	}
	if _, e = os.Stat(filepath.Join(base, id)); !os.IsNotExist(e) {
		t.Fatal("discard retained run", e)
	}
	if _, e = os.Stat(filepath.Join(base, "_tombstones", id+".jsonl")); e != nil {
		t.Fatal("audit absent", e)
	}
	for _, command := range []string{"list", "inspect"} {
		args := []string{"managed-workspace", "--base", base, command}
		if command == "inspect" {
			args = append(args, "--run", id)
		}
		out, display, err = invoke("", args...)
		if err != nil || !strings.Contains(out, `"state":"discarded"`) {
			t.Fatal(out, display, err)
		}
	}
	out, display, err = invoke("", "managed-workspace", "--base", base, "inspect", "--run", otherID)
	if err != nil || !strings.Contains(out, `"state":"ready"`) {
		t.Fatal("other run affected", out, display, err)
	}
	original, e = os.ReadFile(filepath.Join(source, "config.txt"))
	if e != nil || string(original) != "initial\n" {
		t.Fatal("source modified after discard")
	}
	t.Log("real CLI create/apply/report/list/inspect/deny/discard: source unchanged; other run ready; terminal audit verified; no provider configuration")
}
