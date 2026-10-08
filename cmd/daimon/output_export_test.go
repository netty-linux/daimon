package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

func TestOutputExportArgumentsAndApproval(t *testing.T) {
	good := []string{"--base", "store", "export-output", "--run", "id", "--path", "file.txt", "--destination", "review", "--source-check", "/source", "--enable-output-export"}
	if _, _, _, err := managedArguments(good); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{good[:len(good)-1], append(append([]string{}, good...), "--enable-output-export"), append(append([]string{}, good...), "--shell", "x"), append(append([]string{}, good...), "--path", "other")} {
		if _, _, _, e := managedArguments(args); e == nil {
			t.Fatal("invalid args allowed")
		}
	}
	view := editcontract.Review{ID: strings.Repeat("a", 64), Display: "metadata only\n"}
	for _, input := range []string{"", "y\n", "EXPORTAR wrong\n", "EXPORTAR " + view.ID + "\n"} {
		var display bytes.Buffer
		r := &outputReviewer{strings.NewReader(input), &display}
		decision, e := r.Review(context.Background(), view)
		if e != nil {
			t.Fatal(e)
		}
		expected := editcontract.Deny
		if input == "EXPORTAR "+view.ID+"\n" {
			expected = editcontract.Allow
		}
		if decision != expected {
			t.Fatal("decision")
		}
	}
	r := &outputReviewer{strings.NewReader("EXPORTAR " + view.ID + "\n"), shortOutputWriter{}}
	if _, e := r.Review(context.Background(), view); e == nil {
		t.Fatal("display failure accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := (&outputReviewer{strings.NewReader(""), &bytes.Buffer{}}).Review(ctx, view); e != context.Canceled {
		t.Fatal(e)
	}
}

type shortOutputWriter struct{}

func (shortOutputWriter) Write(p []byte) (int, error) { return 0, io.ErrShortWrite }

type exportPhraseWriter struct {
	buf   bytes.Buffer
	input io.Writer
	sent  bool
}

func (w *exportPhraseWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	match := regexp.MustCompile("EXPORTAR ([a-f0-9]{64})").FindStringSubmatch(w.buf.String())
	if len(match) == 2 && !w.sent {
		w.sent = true
		if _, err := io.WriteString(w.input, "EXPORTAR "+match[1]+"\n"); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func TestOutputExportExecutableLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(dir, "daimon")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Env = []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + os.TempDir(), "TMP=" + os.TempDir(), "GOCACHE=" + filepath.Join(dir, "cache"), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local"}
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build %v %s", e, out)
	}
	base := filepath.Join(dir, "store")
	source := filepath.Join(dir, "source")
	invoke := func(approve bool, input string, args ...string) (string, string, error) {
		cmd := exec.CommandContext(ctx, binary, append([]string{"managed-workspace", "--base", base}, args...)...)
		cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot")}
		var out, display bytes.Buffer
		cmd.Stdout = &out
		if approve {
			pipe, e := cmd.StdinPipe()
			if e != nil {
				t.Fatal(e)
			}
			w := &exportPhraseWriter{input: pipe}
			cmd.Stderr = w
			e = cmd.Run()
			pipe.Close()
			return out.String(), w.buf.String(), e
		}
		cmd.Stdin = strings.NewReader(input)
		cmd.Stderr = &display
		e := cmd.Run()
		return out.String(), display.String(), e
	}
	args := []string{"export-output", "--run", "00000000000000000000000000000000", "--path", "config.txt", "--destination", "review", "--source-check", source, "--enable-output-export"}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		_, display, e := invoke(false, "", args...)
		if e == nil || !strings.Contains(display, "não suportado") {
			t.Fatal("platform", e, display)
		}
		if _, e = os.Stat(base); !os.IsNotExist(e) {
			t.Fatal("provisioned")
		}
		return
	}
	if os.Mkdir(source, 0700) != nil || os.WriteFile(filepath.Join(source, "config.txt"), []byte("initial\n"), 0600) != nil {
		t.Fatal("fixture")
	}
	create, _, e := invoke(false, "", "create", "--source", source)
	if e != nil {
		t.Fatal(e)
	}
	id := strings.TrimSpace(strings.TrimPrefix(create, "Run ID: "))
	if i := strings.Index(id, "\n"); i >= 0 {
		id = id[:i]
	}
	// Parse only the documented run-id line, rather than assume all CLI output.
	for _, line := range strings.Split(create, "\n") {
		if strings.HasPrefix(line, "Run ID: ") {
			id = strings.TrimPrefix(line, "Run ID: ")
		}
	}
	canary := "API_KEY=output-cli-" + workspaceplan.Hash([]byte(dir)) + "\npassword=output-password\nJWT-like-token=aaa.bbb.ccc\n-----BEGIN PRIVATE KEY-----\noutput-cli-private-canary\n-----END PRIVATE KEY-----\n"
	plan := workspaceplan.Plan{Version: 1, Kind: "workspace_apply", Operations: []workspaceplan.Operation{{Type: "replace_file", Path: "config.txt", Content: canary, Precondition: workspaceplan.Precondition{SHA256: workspaceplan.Hash([]byte("initial\n"))}, Validation: workspaceplan.Validation{SHA256: workspaceplan.Hash([]byte(canary))}}}, Blockers: []string{}, Assumptions: []string{}}
	b, _ := json.Marshal(plan)
	planFile := filepath.Join(dir, "plan.json")
	if os.WriteFile(planFile, b, 0600) != nil {
		t.Fatal("plan")
	}
	if _, _, e = invoke(false, "y\n", "apply", "--run", id, "--plan", planFile); e != nil {
		t.Fatal("apply", e)
	}
	control, _, e := invoke(false, "", "create", "--source", source)
	if e != nil {
		t.Fatal(e)
	}
	other := ""
	for _, line := range strings.Split(control, "\n") {
		if strings.HasPrefix(line, "Run ID: ") {
			other = strings.TrimPrefix(line, "Run ID: ")
		}
	}
	args[2] = id
	check := func(text string) {
		for _, m := range strings.Split(strings.TrimSpace(canary), "\n") {
			if strings.Contains(text, m) {
				t.Fatal("operator leak")
			}
		}
	}
	denied, display, e := invoke(false, "y\n", args...)
	check(denied + display)
	if e == nil {
		t.Fatal("y accepted")
	}
	out, display, e := invoke(true, "", args...)
	check(out + display)
	if e != nil || !strings.Contains(out, "exported") {
		t.Fatal("export", e, out, display)
	}
	area := ""
	for _, line := range strings.Split(display, "\n") {
		if strings.HasPrefix(line, "managed_area=") {
			quoted := strings.TrimPrefix(line, "managed_area=")
			if json.Unmarshal([]byte(quoted), &area) != nil {
				t.Fatal("area")
			}
		}
	}
	dest := filepath.Join(dir, area, "review")
	data, e := os.ReadFile(filepath.Join(dest, "output.bin"))
	if e != nil || string(data) != canary {
		t.Fatal("bytes", e)
	}
	for _, command := range []string{"report", "inspect"} {
		out, display, e = invoke(false, "", command, "--run", id)
		check(out + display)
		if e != nil {
			t.Fatal(command, e)
		}
	}
	out, display, e = invoke(false, "", "list")
	check(out + display)
	if e != nil {
		t.Fatal(e)
	}
	evidenceParent := filepath.Join(dir, "evidence")
	if os.Mkdir(evidenceParent, 0700) != nil {
		t.Fatal("evidence")
	}
	out, display, e = invoke(false, "y\n", "export-evidence", "--run", id, "--destination", filepath.Join(evidenceParent, "package"), "--source-check", source, "--enable-export-evidence")
	check(out + display)
	if e != nil {
		t.Fatal("evidence", e)
	}
	files, e := os.ReadDir(filepath.Join(evidenceParent, "package"))
	if e != nil || len(files) != 6 {
		t.Fatal("six files")
	}
	for _, f := range files {
		b, e := os.ReadFile(filepath.Join(evidenceParent, "package", f.Name()))
		if e != nil {
			t.Fatal(e)
		}
		check(string(b))
	}
	out, display, e = invoke(false, "y\n", "discard", "--run", id, "--enable-discard")
	check(out + display)
	if e != nil {
		t.Fatal("discard", e)
	}
	data, e = os.ReadFile(filepath.Join(dest, "output.bin"))
	if e != nil || string(data) != canary {
		t.Fatal("export survives")
	}
	data, e = os.ReadFile(filepath.Join(source, "config.txt"))
	if e != nil || string(data) != "initial\n" {
		t.Fatal("source")
	}
	out, _, e = invoke(false, "", "inspect", "--run", other)
	if e != nil || !strings.Contains(out, `"state":"ready"`) {
		t.Fatal("control")
	}
	t.Log("real CLI output export: reinforced approval, exact content, source/control intact, six metadata-only evidence files, discard preserves export")
}
