package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/agentloop"
)

const scopedRequest = "Analise esta fixture. Proponha criar um arquivo curto de documentacao em docs/ e alterar somente src/config.txt de mode=initial para mode=final. Nao execute alteracoes. Use ferramentas somente quando precisar de evidencia. Registre incertezas em Bloqueios."

const scopedHumanPlan = "Diagnóstico\nConfiguração observada.\nObjetivo da mudança\nDocumentar a fixture e trocar o valor solicitado.\nArquivos prováveis\ndocs/NOTES.md e src/config.txt.\nAlteração proposta por arquivo\nDocumento curto e troca mode=initial para mode=final.\nRiscos e suposições\nNome da documentação é proposta.\nValidação proposta\nComparar arquivos, sem executar.\nBloqueios ou informações faltantes\nNenhum.\n"

const goodScopeJSON = `{"create":["docs/NOTES.md"],"modify":["src/config.txt"],"delete":[],"operations":[{"path":"src/config.txt","old":"mode=initial","new":"mode=final"}],"blockers":[]}`

func scopeAnswer(block string) string {
	return scopedHumanPlan + scopeStart + "\n" + block + "\n" + scopeEnd
}

func TestPlanScopeExactRequestRecognition(t *testing.T) {
	scope := recognizePlanScope(scopedRequest)
	if scope == nil || scope.directory != "docs" || scope.file != "src/config.txt" ||
		scope.old != "mode=initial" || scope.new != "mode=final" {
		t.Fatal("exact manual request did not activate the expected scope")
	}
	if !scope.valid(scopeAnswer(goodScopeJSON)) ||
		scope.valid(scopeAnswer(strings.Replace(goodScopeJSON, `"delete":[]`, `"delete":["src/info.txt"]`, 1))) {
		t.Fatal("exact manual scope did not enforce zero deletions")
	}
	t.Log("validate_scope_template_recognized=true; directory=docs; modify=src/config.txt; old=mode=initial; new=mode=final; deletions=0")
}

func TestPlanScopeValidation(t *testing.T) {
	s := recognizePlanScope(scopedRequest)
	if s == nil || !s.valid(scopeAnswer(goodScopeJSON)) {
		t.Fatal("recognized valid scope rejected")
	}
	for _, answer := range []string{
		"Objetivo não definido", scopeAnswer("{"), scopeAnswer(goodScopeJSON) + "trailing",
		scopeAnswer(strings.Replace(goodScopeJSON, `"docs/NOTES.md"`, `"README.md"`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"docs/NOTES.md"`, `"docs/../NOTES.md"`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"docs/NOTES.md"`, `"/docs/NOTES.md"`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"modify":["src/config.txt"]`, `"modify":["src/config.txt","README.md"]`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"delete":[]`, `"delete":["src/info.txt"]`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"mode=final"`, `"mode=final # comment"`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"blockers":[]`, `"blockers":["sem acesso de escrita"]`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"create":["docs/NOTES.md"]`, `"create":[]`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"delete":[]`, `"delete":null`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"delete":[]`, `"extra":true,"delete":[]`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"delete":[]`, `"delete":["README.md"],"delete":[]`, 1)),
		scopeAnswer(strings.Replace(goodScopeJSON, `"path":"src/config.txt"`, `"Path":"src/config.txt"`, 1)),
		strings.Replace(scopeAnswer(goodScopeJSON), "Objetivo da mudança", "Other", 1),
	} {
		if s.valid(answer) {
			t.Fatal("incompatible scope accepted")
		}
	}
	for _, request := range []string{"Analise", scopedRequest + " Também altere README.md.", strings.Replace(scopedRequest, "docs/", "../docs/", 1)} {
		if recognizePlanScope(request) != nil {
			t.Fatal("unsupported request guessed")
		}
	}
}

func TestPlanScopeIntegration(t *testing.T) {
	for _, tc := range []struct {
		name            string
		opt, recognized bool
		answers         []string
		toolInRecovery  bool
		wantErr         bool
		invalidWire     bool
		failOnRecovery  bool
	}{
		{name: "valid first", opt: true, recognized: true, answers: []string{scopeAnswer(goodScopeJSON)}},
		{name: "invalid then valid", opt: true, recognized: true, answers: []string{"Objetivo não definido", scopeAnswer(goodScopeJSON)}},
		{name: "invalid twice", opt: true, recognized: true, answers: []string{"Objetivo não definido", "Ainda inválido"}, wantErr: true},
		{name: "unrecognized unchanged", opt: true, answers: []string{"Plano livre"}},
		{name: "opt in absent", recognized: true, answers: []string{"Plano livre"}},
		{name: "recovery tools refused", opt: true, recognized: true, answers: []string{"Inválido", "unused"}, toolInRecovery: true, wantErr: true},
		{name: "malformed initial response", opt: true, recognized: true, answers: []string{"unused"}, invalidWire: true, wantErr: true},
		{name: "malformed recovery response", opt: true, recognized: true, answers: []string{"Inválido", "unused"}, invalidWire: true, failOnRecovery: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"src", "docs"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{"README.md": "Fixture", "src/config.txt": "mode=initial", "src/info.txt": "Info"}
			for p, content := range files {
				if err := os.WriteFile(filepath.Join(root, p), []byte(content), 0640); err != nil {
					t.Fatal(err)
				}
			}
			request := scopedRequest
			if !tc.recognized {
				request = "Analise a fixture."
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				if len(req.Messages) < 2 || req.Messages[1].Role != "user" || req.Messages[1].Content != request {
					t.Error("original request lost")
				}
				recovery := calls == 3
				if recovery {
					if len(req.Tools) != 0 || len(req.Messages) != 8 || req.Messages[6].Role != "assistant" || req.Messages[6].Content != tc.answers[0] || req.Messages[7].Role != "user" || !strings.Contains(req.Messages[7].Content, "Recuperação única") {
						t.Error("recovery lost history or exposed tools")
					}
				} else {
					if len(req.Tools) != 3 {
						t.Error("read-only schema changed")
					}
					for _, tool := range req.Tools {
						if tool.Function.Name != "echo" && tool.Function.Name != "read_file" && tool.Function.Name != "list_dir" {
							t.Error("new tool exposed")
						}
					}
				}
				if strings.Contains(req.Messages[0].Content, scopeProtocol) != (tc.opt && tc.recognized) {
					t.Error("contract opt-in mismatch")
				}
				switch calls {
				case 0:
					respond(w, toolCallsResponse(toolCallJSON("list", "list_dir", `"{\"path\":\".\"}"`)))
				case 1:
					respond(w, toolCallsResponse(toolCallJSON("read", "read_file", `"{\"path\":\"src/config.txt\"}"`)))
				default:
					index := calls - 2
					if index >= len(tc.answers) {
						t.Error("extra recovery request")
						index = len(tc.answers) - 1
					}
					if tc.invalidWire && (recovery || !tc.failOnRecovery) {
						respond(w, "{PRIVATE_INVALID_BODY")
					} else if tc.toolInRecovery && recovery {
						respond(w, toolCallsResponse(toolCallJSON("extra", "read_file", `"{\"path\":\"README.md\"}"`)))
					} else {
						encoded, _ := json.Marshal(tc.answers[index])
						respond(w, `{"choices":[{"message":{"role":"assistant","content":`+string(encoded)+`}}]}`)
					}
				}
				calls++
			}))
			defer server.Close()
			args := []string{"workspace", "--root", root, "plan"}
			if tc.opt {
				args = append(args, "--validate-scope")
			}
			args = append(args, request)
			var out, prompts bytes.Buffer
			err := runWithContext(context.Background(), args, strings.NewReader("y\ny\n"), &out, &prompts, planEnvironment(server.URL))
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected result: %v", err)
			}
			var modelErr *agentloop.ModelError
			if tc.invalidWire && !errors.As(err, &modelErr) {
				t.Fatal("lost model error")
			}
			if tc.wantErr && !tc.invalidWire && !errors.Is(err, agentloop.ErrInvalidResponse) {
				t.Fatal("lost typed failure", err)
			}
			if err != nil && (strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "mode=initial")) {
				t.Fatal("public error exposed workspace data")
			}
			if strings.Contains(out.String(), "PRIVATE_INVALID_BODY") || (err != nil && strings.Contains(err.Error(), "PRIVATE_INVALID_BODY")) {
				t.Fatal("raw HTTP response exposed")
			}
			if calls != 2+len(tc.answers) || strings.Count(prompts.String(), "Permitir uma vez?") != 2 {
				t.Fatal("unexpected requests/approvals", calls)
			}
			if !strings.Contains(out.String(), "replace_file=0 create_file=0") || !strings.Contains(out.String(), "Escritas confirmadas: 0") {
				t.Fatal("write counters changed")
			}
			if tc.wantErr && strings.Contains(out.String(), "Plano proposto") {
				t.Fatal("invalid plan displayed")
			}
			wantDisplay := tc.answers[len(tc.answers)-1]
			if tc.opt && tc.recognized && !tc.wantErr {
				wantDisplay, _, _ = splitScopeAnswer(wantDisplay)
				if strings.Contains(out.String(), scopeStart) || strings.Contains(out.String(), `"operations"`) {
					t.Fatal("internal scope block displayed")
				}
			}
			if !tc.wantErr && !strings.Contains(out.String(), wantDisplay) {
				t.Fatal("valid plan not displayed")
			}
			if tc.opt && tc.recognized && strings.Contains(out.String(), "Objetivo não definido") {
				t.Fatal("invalid first plan leaked")
			}
			summary := strings.SplitN(out.String(), "Resumo do workspace:\n", 2)
			if len(summary) != 2 || strings.Contains(summary[1], "src/") || strings.Contains(summary[1], "docs/") || strings.Contains(summary[1], "mode=initial") {
				t.Fatal("public summary exposed workspace data")
			}
			for p, content := range files {
				data, err := os.ReadFile(filepath.Join(root, p))
				if err != nil || string(data) != content {
					t.Fatal("fixture changed")
				}
			}
			docs, err := os.ReadDir(filepath.Join(root, "docs"))
			if err != nil || len(docs) != 0 {
				t.Fatal("docs changed")
			}
			t.Logf("requests=%d; recovery=%d; approvals=2; writes=0; fixture unchanged", calls, len(tc.answers)-1)
		})
	}
}

func TestPlanScopeBoundaryCases(t *testing.T) {
	s := recognizePlanScope(scopedRequest)
	exact := goodScopeJSON + strings.Repeat(" ", maxScopeBytes-len(goodScopeJSON))
	if !s.valid(scopeAnswer(exact)) || s.valid(scopeAnswer(exact+" ")) {
		t.Fatal("scope byte boundary incorrect")
	}
	for _, p := range []string{
		"/docs/NOTES.md", "../NOTES.md", "docs/../NOTES.md", "docs//NOTES.md", `docs\.\NOTES.md`, `docs\..\NOTES.md`,
		"C:/docs/NOTES.md", "//server/docs/NOTES.md", "docs/NOTES.md\x00", "docs/ＮＯＴＥＳ.md", "docs-evil/NOTES.md",
		"docs/NUL", "docs/CON.txt", "docs/com1.md", "docs/LPT9", "docs/NOTES.md.",
	} {
		block := strings.Replace(goodScopeJSON, `"docs/NOTES.md"`, string(mustJSON(t, p)), 1)
		if s.valid(scopeAnswer(block)) {
			t.Errorf("unsafe or unrelated proposed path accepted: %q", p)
		}
	}
	for _, block := range []string{
		strings.Replace(goodScopeJSON, `"modify":["src/config.txt"]`, `"modify":["src/config.txt.bak"]`, 1),
		strings.Replace(goodScopeJSON, `"blockers":[]`, `"blockers":["read_failed","read_failed"]`, 1),
		strings.Replace(goodScopeJSON, `"create":["docs/NOTES.md"]`, `"create":[null]`, 1),
		strings.Replace(goodScopeJSON, `"operations":[{`, `"operations":[null,{`, 1),
		goodScopeJSON + " {}", "[]", "null",
		goodScopeJSON + strings.Repeat(" ", maxScopeBytes),
	} {
		if s.valid(scopeAnswer(block)) {
			t.Fatal("invalid JSON/size contract accepted")
		}
	}
	for _, answer := range []string{
		scopedHumanPlan + scopeStart + "\n" + scopeEnd,
		scopedHumanPlan + scopeStart + goodScopeJSON + scopeEnd,
		scopedHumanPlan + "quoted " + scopeStart + "\n" + goodScopeJSON + "\n" + scopeEnd,
		"```text\n" + scopeAnswer(goodScopeJSON),
		strings.Replace(scopeAnswer(goodScopeJSON), "Objetivo da mudança", "Diagnóstico\nObjetivo da mudança", 1),
	} {
		if s.valid(answer) {
			t.Fatal("ambiguous or incomplete envelope accepted")
		}
	}
	for _, p := range []string{"docs/NUL", "docs/file.", "../docs", `docs\sub`, "docs/ｅvil"} {
		request := strings.Replace(scopedRequest, "docs/", p+"/", 1)
		if recognizePlanScope(request) != nil {
			t.Fatal("ambiguous request recognized")
		}
	}
}

func mustJSON(t *testing.T, value string) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestValidatedPlanInvalidArguments(t *testing.T) {
	root := t.TempDir()
	for _, tail := range [][]string{
		{"plan", "--validate-scope"}, {"plan", "--validate-scope", ""}, {"plan", "--validate-scope", " "},
		{"plan", "--validate-scope", "pedido", "extra"}, {"--validate-scope", "plan", "pedido"},
		{"plan", "pedido", "--validate-scope", "--validate-scope"}, {"plan", "--validate-scope", "--enable-create-file"},
	} {
		args := append([]string{"workspace", "--root", root}, tail...)
		err := runWithContext(context.Background(), args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { t.Fatal("invalid args configured provider"); return "" })
		if !errors.Is(err, errWorkspace) || !strings.Contains(err.Error(), "Uso de plan:") || strings.Contains(err.Error(), root) {
			t.Fatal("unsafe or unhelpful CLI rejection", err)
		}
	}
}
