package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise main(), argv parsing and the actual exit status, not only the CLI
// composition helper. Both compilation and execution use explicit environments
// with no inherited provider credentials. The provider is loopback-only.
func TestPlanScopeExecutableNoToolsFirstResponse(t *testing.T) {
	const invalid = "Sure! Could you let me know what you’d like me to analyze? If you have a specific file, module, test, or part of the codebase in mind, just point me to it and I’ll dive in."
	root := t.TempDir()
	for _, dir := range []string{"src", "docs"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"README.md": "Fixture", "src/config.txt": "mode=initial", "src/info.txt": "Info"}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0640); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(t.TempDir(), "daimon")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Env = []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + os.TempDir(), "TMP=" + os.TempDir(), "GOCACHE=" + filepath.Join(t.TempDir(), "cache"), "GOPROXY=off", "GOTOOLCHAIN=local", "GOSUMDB=off"}
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("offline build failed: %v; %s", err, output)
	}
	for _, tc := range []struct {
		name                                  string
		reads                                 int
		validate, invalidFirst, validRecovery bool
	}{
		{"A no tools invalid then valid", 0, true, true, true},
		{"no tools invalid twice", 0, true, true, false},
		{"B one tool invalid then valid", 1, true, true, true},
		{"one tool invalid twice", 1, true, true, false},
		{"one tool valid immediately", 1, true, false, false},
		{"C two tools invalid then valid", 2, true, true, true},
		{"D two tools invalid twice", 2, true, true, false},
		{"E two tools valid immediately", 2, true, false, false},
		{"H compatibility without flag", 2, false, true, false},
		{"D three tools invalid then valid", 3, true, true, true},
		{"E three tools invalid twice", 3, true, true, false},
		{"F three tools valid immediately", 3, true, false, false},
		{"I three tools compatibility without flag", 3, false, true, false},
		{"B five tools seven sections no envelope", 5, true, true, true},
		{"B five tools missing creation", 5, true, true, true},
		{"C five tools missing modification", 5, true, true, true},
		{"five tools invalid twice", 5, true, true, false},
		{"D five tools valid immediately", 5, true, false, false},
		{"G five tools compatibility without flag", 5, false, true, false},
		{"parser literal flag after request", 0, true, false, false},
		{"parser environment flag before request", 0, true, false, false},
		{"parser environment flag after request", 0, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalidText := invalid
			reads := tc.reads
			if reads == 1 {
				invalidText = "Para que eu possa ajudá-lo de forma precisa, poderia especificar qual aspecto do projeto você gostaria que eu analisasse?"
			}
			if reads == 2 {
				invalidText = "Para poder lhe fornecer uma análise útil, preciso saber qual parte do projeto você gostaria de examinar ou o que exatamente deseja analisar (por exemplo, estrutura de pastas, código fonte, documentação, dependências, etc.). Por favor, indique a área de foco."
			}
			if reads == 3 {
				invalidText = "**Diagnóstico** O workspace contém apenas um arquivo de documentação (`README.md`) e dois diretórios vazios (`docs/` e `src/`)..."
			}
			if reads == 5 {
				invalidText = "Diagnóstico\nFixture observada.\nObjetivo da mudança\nNenhuma alteração proposta.\nArquivos prováveis\nNenhum.\nAlteração proposta por arquivo\nNenhuma alteração proposta.\nRiscos e suposições\nNenhum.\nValidação proposta\nNenhuma.\nBloqueios ou informações faltantes\nNenhum."
				if strings.Contains(tc.name, "missing creation") {
					invalidText = scopeAnswer(strings.Replace(goodScopeJSON, `"create":["docs/NOTES.md"]`, `"create":[]`, 1))
				}
				if strings.Contains(tc.name, "missing modification") {
					invalidText = scopeAnswer(strings.Replace(goodScopeJSON, `"modify":["src/config.txt"]`, `"modify":[]`, 1))
				}
			}
			recoveries := 0
			if tc.validate && tc.invalidFirst {
				recoveries = 1
			}
			wantRequests := reads + 1 + recoveries
			wantCompleted := !tc.validate || !tc.invalidFirst || tc.validRecovery
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				step := int(calls.Add(1) - 1)
				var req struct {
					Messages []struct{ Role, Content string }
					Tools    []struct{ Function struct{ Name string } }
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("unexpected credential")
				}
				if len(req.Messages) < 2 || req.Messages[1].Role != "user" || req.Messages[1].Content != scopedRequest || len(req.Messages[1].Content) != 256 {
					t.Error("argv request was altered")
				}
				if step <= reads {
					if len(req.Tools) != 3 || strings.Contains(req.Messages[0].Content, scopeProtocol) != tc.validate {
						t.Error("scope contract not activated")
					}
					for _, tool := range req.Tools {
						if tool.Function.Name != "echo" && tool.Function.Name != "list_dir" && tool.Function.Name != "read_file" {
							t.Error("write/extra tool exposed")
						}
					}
				} else if step == reads+1 && recoveries == 1 {
					prior := 2 + 2*reads
					if len(req.Tools) != 0 || len(req.Messages) != prior+2 || req.Messages[prior].Content != invalidText || req.Messages[prior+1].Role != "user" || !strings.Contains(req.Messages[prior+1].Content, "Recuperação única") {
						t.Error("recovery history or schema invalid")
					}
				} else {
					t.Error("more than one recovery")
				}
				if reads == 5 {
					names := []string{"list_dir", "list_dir", "read_file", "read_file", "read_file"}
					paths := []string{".", "src", "src/config.txt", "README.md", "src/info.txt"}
					for i := 0; i < step && i < 5; i++ {
						index := 3 + 2*i
						if len(req.Messages) <= index || req.Messages[index].Role != "tool" {
							t.Error("missing approved receipt")
							continue
						}
						if i >= 2 && req.Messages[index].Content != files[paths[i]] {
							t.Error("incorrect read receipt")
						}
					}
					if step < 5 {
						argsJSON, _ := json.Marshal(map[string]string{"path": paths[step]})
						encodedArgs, _ := json.Marshal(string(argsJSON))
						respond(w, toolCallsResponse(toolCallJSON(fmt.Sprintf("five-%d", step), names[step], string(encodedArgs))))
						return
					}
				}
				if reads > 0 && reads != 5 && step == 0 {
					respond(w, toolCallsResponse(toolCallJSON("root-list", "list_dir", `"{\"path\":\".\"}"`)))
					return
				}
				if reads > 0 && reads != 5 && step >= 1 {
					if len(req.Messages) < 4 || req.Messages[2].Role != "assistant" || req.Messages[3].Role != "tool" || !strings.Contains(req.Messages[3].Content, "README.md\tfile") {
						t.Error("approved root listing missing from history")
					}
				}
				if reads == 3 && step == 1 {
					respond(w, toolCallsResponse(toolCallJSON("docs-list", "list_dir", `"{\"path\":\"docs\"}"`)))
					return
				}
				if reads == 3 && step >= 2 {
					if len(req.Messages) < 6 || req.Messages[4].Role != "assistant" || req.Messages[5].Role != "tool" || req.Messages[5].Content != "" {
						t.Error("approved empty docs listing missing from history")
					}
				}
				readStep := 1
				if reads == 3 {
					readStep = 2
				}
				if reads >= 2 && reads != 5 && step == readStep {
					respond(w, toolCallsResponse(toolCallJSON("read-readme", "read_file", `"{\"path\":\"README.md\"}"`)))
					return
				}
				if reads >= 2 && reads != 5 && step > readStep {
					prior := 2 + 2*readStep
					if len(req.Messages) < prior+2 || req.Messages[prior].Role != "assistant" || req.Messages[prior+1].Role != "tool" || req.Messages[prior+1].Content != files["README.md"] {
						t.Error("second approved tool receipt missing from history")
					}
				}
				answer := invalidText
				if !tc.invalidFirst || (step == reads+1 && tc.validRecovery) {
					answer = scopeAnswer(goodScopeJSON)
				}
				encoded, _ := json.Marshal(answer)
				respond(w, `{"choices":[{"message":{"role":"assistant","content":`+string(encoded)+`}}]}`)

			}))
			defer server.Close()
			args := []string{"workspace", "--root", root, "plan"}
			if tc.validate {
				args = append(args, "--validate-scope")
			}
			args = append(args, scopedRequest)
			if strings.Contains(tc.name, "flag after request") {
				args = []string{"workspace", "--root", root, "plan", scopedRequest, "--validate-scope"}
			}
			cmd := exec.CommandContext(ctx, binary, args...)
			if strings.Contains(tc.name, "parser environment") {
				if runtime.GOOS == "windows" {
					t.Skip("sh -c environment expansion is Linux-only; Windows literal argv covered")
				}
				command := `exec "$DAIMON_TEST_BIN" workspace --root "$DAIMON_TEST_ROOT" plan --validate-scope "$DAIMON_PLAN_REQUEST"`
				if strings.Contains(tc.name, "flag after request") {
					command = `exec "$DAIMON_TEST_BIN" workspace --root "$DAIMON_TEST_ROOT" plan "$DAIMON_PLAN_REQUEST" --validate-scope`
				}
				cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command)
			}
			if reads > 0 {
				cmd.Stdin = strings.NewReader(strings.Repeat("y\n", reads))
			}
			cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "DAIMON_BASE_URL=" + server.URL, "DAIMON_MODEL=offline"}
			if strings.Contains(tc.name, "parser environment") {
				cmd.Env = append(cmd.Env, "DAIMON_TEST_BIN="+binary, "DAIMON_TEST_ROOT="+root, "DAIMON_PLAN_REQUEST="+scopedRequest)
			}
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			err := cmd.Run()
			if wantCompleted && err != nil {
				t.Fatalf("valid recovery failed: %v; %s", err, stderr.String())
			}
			if !wantCompleted && err == nil {
				t.Fatal("invalid plan completed")
			}
			if int(calls.Load()) != wantRequests || (tc.validate && strings.Contains(out.String(), invalidText)) || strings.Contains(out.String(), scopeStart) || strings.Contains(stderr.String(), invalidText) || strings.Count(stderr.String(), "Permitir uma vez?") != reads {
				t.Fatalf("invalid response exposed, extra approval or recovery: requests=%d; stdout=%q; stderr=%q", calls.Load(), out.String(), stderr.String())
			}
			for _, want := range []string{fmt.Sprintf("Passos do modelo: %d", wantRequests), fmt.Sprintf("Chamadas de ferramenta: %d", reads), fmt.Sprintf("Aprovações solicitadas: %d", reads), "Escritas confirmadas: 0", "replace_file=0 create_file=0"} {
				if !strings.Contains(out.String(), want) {
					t.Fatal("unexpected counters", out.String())
				}
			}
			wantDisplays := 0
			if wantCompleted {
				wantDisplays = 1
			}
			if strings.Count(out.String(), "Plano proposto") != wantDisplays {
				t.Fatal("partial or missing plan")
			}
			if wantCompleted && !strings.Contains(out.String(), "Motivo de parada: completed") {
				t.Fatal("valid plan did not complete")
			}
			if !wantCompleted && (!strings.Contains(out.String(), "Motivo de parada: invalid_response") || !strings.Contains(stderr.String(), "Plano recusado")) {
				t.Fatal("failure was not controlled")
			}
			for path, content := range files {
				data, err := os.ReadFile(filepath.Join(root, path))
				if err != nil || string(data) != content {
					t.Fatal("fixture changed")
				}
			}
			docs, err := os.ReadDir(filepath.Join(root, "docs"))
			if err != nil || len(docs) != 0 {
				t.Fatal("docs changed")
			}
			if !tc.validate && !strings.Contains(out.String(), invalidText) {
				t.Fatal("unvalidated compatibility changed")
			}
			t.Logf("CLI executable: validation_enabled=%t; requests=%d; recovery=%d; tools=%d; approvals=%d; writes=0; fixture unchanged", tc.validate, wantRequests, recoveries, reads, reads)
			if strings.Contains(tc.name, "parser") {
				t.Log("validate_scope_template_recognized=true; request_bytes=256; provider received exact request")
			}
		})
	}
}
