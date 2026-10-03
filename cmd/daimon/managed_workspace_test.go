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
	for _, tail := range [][]string{{"create", "--source", "src"}, {"apply", "--run", "id", "--plan", "p"}, {"apply", "--plan", "p", "--run", "id"}, {"report", "--run", "id"}} {
		if _, _, _, err := managedArguments(append([]string{"--base", "store"}, tail...)); err != nil {
			t.Fatal(err)
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
	t.Log("real CLI: one approval; private output changed; source unchanged; report succeeded; second apply denied; no provider configuration")
}
