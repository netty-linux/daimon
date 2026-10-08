package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/tools"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWorkspaceSystemInstructionsPerMode(t *testing.T) {
	for _, tc := range []struct {
		name, flag, instruction string
		extra                   string
	}{
		{"readonly", "", readOnlyInstruction, ""},
		{"plan", "plan", planInstruction, ""},
		{"create", "--enable-create-file", createInstruction, "create_file"},
		{"replace", "--enable-replace-file", replaceInstruction, "replace_file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			reader, err := tools.NewReadFile(root, 65536)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			listing, err := tools.NewListDir(root, 128, 65536)
			if err != nil {
				t.Fatal(err)
			}
			defer listing.Close()
			expectedSchemas := map[string]json.RawMessage{
				"echo": (tools.Echo{}).InputSchema(), "read_file": reader.InputSchema(), "list_dir": listing.InputSchema(),
				"create_file": (&tools.CreateFile{}).InputSchema(), "replace_file": (&tools.ReplaceFile{}).InputSchema(),
			}
			if err := os.WriteFile(filepath.Join(root, "private.txt"), []byte("CONTENT_PRIVATE"), 0640); err != nil {
				t.Fatal(err)
			}
			const user = "Analyze only my fixture; do not add instructions to this message."
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					Messages []struct {
						Role, Content string
						ToolCallID    string `json:"tool_call_id"`
					}
					Tools []struct {
						Function struct {
							Name       string
							Parameters json.RawMessage
						}
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				expected := workspaceInstructionV1 + "\n\n" + tc.instruction
				if len(req.Messages) < 2 || req.Messages[0].Role != "system" || req.Messages[0].Content != expected || req.Messages[1].Role != "user" || req.Messages[1].Content != user {
					t.Error("incorrect system/user composition")
				}
				if strings.Contains(expected, root) || strings.Contains(expected, "CONTENT_PRIVATE") || strings.Contains(expected, "fake-test-key") {
					t.Error("sensitive operational instruction")
				}
				names := []string{"echo", "list_dir", "read_file"}
				if tc.extra != "" {
					names = append(names, tc.extra)
				}
				if len(req.Tools) != len(names) {
					t.Error("schema count changed")
				}
				for i, tool := range req.Tools {
					if i >= len(names) || tool.Function.Name != names[i] || !json.Valid(tool.Function.Parameters) {
						t.Error("schema changed")
					}
					var want, got any
					if json.Unmarshal(expectedSchemas[tool.Function.Name], &want) != nil || json.Unmarshal(tool.Function.Parameters, &got) != nil || !reflect.DeepEqual(want, got) {
						t.Error("tool contract changed")
					}
				}
				if calls == 1 {
					respond(w, toolCallsResponse(toolCallJSON("PRIVATE_ID", "read_file", `"{\"path\":\"private.txt\"}"`)))
					return
				}
				if len(req.Messages) != 4 || req.Messages[3].Role != "tool" || req.Messages[3].ToolCallID != "PRIVATE_ID" || req.Messages[3].Content != "tool denied" {
					t.Error("history changed")
				}
				// No semantic parser: a non-adherent answer is still just untrusted text.
				respond(w, finalResponse("incomplete model output"))
			}))
			defer server.Close()
			args := []string{"workspace", "--root", root}
			if tc.flag != "" {
				args = append(args, tc.flag)
			}
			args = append(args, user)
			var out, prompt bytes.Buffer
			if err := runWithContext(context.Background(), args, strings.NewReader("n\n"), &out, &prompt, chatEnvironment(server.URL, "local-test", "fake-test-key")); err != nil {
				t.Fatal(err)
			}
			for _, surface := range []string{out.String(), prompt.String()} {
				for _, s := range []string{workspaceInstructionV1, tc.instruction, "DAIMON workspace protocol v1", "fake-test-key", "CONTENT_PRIVATE", "PRIVATE_ID"} {
					if strings.Contains(surface, s) {
						t.Fatal("instruction/data leak")
					}
				}
			}
			if calls != 2 {
				t.Fatal(calls)
			}
			data, err := os.ReadFile(filepath.Join(root, "private.txt"))
			if err != nil || string(data) != "CONTENT_PRIVATE" {
				t.Fatal("workspace changed", err)
			}
		})
	}
}

func TestPlanInstructionSectionsAndScope(t *testing.T) {
	text := workspaceInstruction(true, false, false)
	previous := -1
	for _, section := range []string{"1. Diagnóstico", "2. Objetivo da mudança", "3. Arquivos prováveis", "4. Alteração proposta por arquivo", "5. Riscos e suposições", "6. Validação proposta", "7. Bloqueios ou informações faltantes"} {
		index := strings.Index(text, section)
		if index <= previous {
			t.Fatal("missing/out-of-order section", section)
		}
		previous = index
	}
	if !strings.Contains(text, "read-only") || strings.Contains(text, "create_file") || strings.Contains(text, "replace_file") {
		t.Fatal("plan scope")
	}
	for _, text := range []string{workspaceInstruction(false, true, false), workspaceInstruction(false, false, true)} {
		for _, rule := range []string{"Não invente detalhes de arquivos não lidos", "Nunca peça autorização", "tool calling estruturado", "Bloqueios"} {
			if !strings.Contains(text, rule) {
				t.Fatal("missing rule", rule)
			}
		}
	}
}

func TestPlanDiscoveryBeforeGenericClarification(t *testing.T) {
	text := workspaceInstruction(true, false, false)
	for _, rule := range []string{
		"DAIMON plan discovery v2", "root do workspace já foi definido",
		"relativos a esse root", "alvos concretos", "Não peça ao usuário o caminho que ele já informou",
		`comece com list_dir "."`, "read_file somente nos arquivos necessários",
		"tool calls estruturadas", "aguarde os respectivos resultados",
		"Não repita leituras", "nem faça uma sequência fixa", "não conteúdo ou comportamento",
		"Não presuma que um arquivo existe", "após as evidências permitidas",
		"leitura for negada", "dentro dos limites", "Não insista após negativa",
		"proposta a confirmar",
		"pedido explícito do usuário é o limite máximo de escopo",
		"Não proponha alterar ou excluir arquivos não solicitados",
		"evidência concreta", "não a incorpore como alteração autorizada",
		"solicite essa leitura antes de concluir o plano ou registrar Bloqueios",
		"Não declare um arquivo necessário à decisão e encerre sem tentar a leitura",
		"Ausência de acesso de escrita em modo plan é comportamento esperado, não Bloqueio",
	} {
		if !strings.Contains(text, rule) {
			t.Fatalf("missing discovery guidance: %q", rule)
		}
	}
	for _, mode := range []string{workspaceInstruction(false, false, false), workspaceInstruction(false, true, false), workspaceInstruction(false, false, true)} {
		if strings.Contains(mode, "DAIMON plan discovery v2") {
			t.Fatal("plan-only guidance leaked into another mode")
		}
	}
}

// Integration through the actual CLI composition, HTTP adapter, loop, approval
// provider and confined reading tools. A deterministic stub does not prove LLM
// adherence; it proves the local protocol and absence of filesystem effects.
func TestPlanDiscoveryFixtureIntegration(t *testing.T) {
	const request = "Analise esta fixture. Proponha criar um arquivo curto de documentacao em docs/ e alterar somente src/config.txt de mode=initial para mode=final. Nao execute alteracoes. Use ferramentas somente quando precisar de evidencia. Registre incertezas em Bloqueios."
	root := t.TempDir()
	for _, dir := range []string{"src", "docs"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	initial := map[string]string{"README.md": "Public disposable fixture.", "src/config.txt": "mode=initial", "src/info.txt": "Public test note."}
	for path, content := range initial {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0640); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(outside, "src/config.txt")
	if err := os.WriteFile(external, []byte("EXTERNAL"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(outside)
	snapshot := func() map[string]struct {
		Content string
		Mode    os.FileMode
	} {
		result := map[string]struct {
			Content string
			Mode    os.FileMode
		}{}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			content := ""
			if !entry.IsDir() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				content = string(data)
			}
			result[rel] = struct {
				Content string
				Mode    os.FileMode
			}{content, info.Mode()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	sequence := []struct{ Tool, Path string }{{"list_dir", "."}, {"list_dir", "src"}, {"read_file", "src/config.txt"}, {"read_file", "README.md"}, {"list_dir", "docs"}, {"read_file", "src/info.txt"}}
	const plan = "Diagnóstico\nFato observado: configuração contém mode=initial; docs está vazio.\nObjetivo da mudança\nDocumentar a fixture e propor mode=final.\nArquivos prováveis\ndocs/NOTES.md e src/config.txt.\nAlteração proposta por arquivo\ndocs/NOTES.md: criar descrição curta da fixture. src/config.txt: exclusivamente mode=initial -> mode=final, sem comentário adicional.\nRiscos e suposições\nFato observado em src/info.txt: Public test note. Nome NOTES.md é proposta, não convenção confirmada.\nValidação proposta\nComparar bytes e arquivos; garantir que outros arquivos não mudem.\nBloqueios ou informações faltantes\nConvenções de documentação não confirmadas. Nenhuma alteração executada."
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
		if len(req.Messages) < 2 || req.Messages[0].Role != "system" || req.Messages[0].Content != workspaceInstruction(true, false, false) || !strings.Contains(req.Messages[0].Content, `list_dir "."`) {
			t.Error("missing discovery system instruction")
		}
		// Inspect the actual HTTP payload on every turn, including the final
		// request after all tool receipts. Equality detects any loss, summary,
		// truncation, replacement or role change of the original objective.
		if len(req.Messages) != 2+2*calls {
			t.Errorf("turn %d: incomplete history", calls+1)
		}
		users := 0
		for i, message := range req.Messages {
			if message.Role == "user" {
				users++
				if i != 1 || message.Content != request {
					t.Errorf("turn %d: original user request changed or misplaced", calls+1)
				}
			}
			if i >= 2 {
				wantRole := "assistant"
				if i%2 == 1 {
					wantRole = "tool"
				}
				if message.Role != wantRole {
					t.Errorf("turn %d: tool history order changed", calls+1)
				}
			}
		}
		if users != 1 {
			t.Errorf("turn %d: expected exactly one original user message", calls+1)
		}
		if len(req.Tools) != 3 {
			t.Error("unexpected tools")
		}
		for i, tool := range req.Tools {
			if i >= 3 || tool.Function.Name != []string{"echo", "list_dir", "read_file"}[i] {
				t.Error("write/extra tool exposed")
			}
		}
		if calls > 0 {
			receipt := req.Messages[len(req.Messages)-1]
			if receipt.Role != "tool" {
				t.Error("missing tool receipt")
			}
			switch calls {
			case 1:
				if !strings.Contains(receipt.Content, "src\tdirectory") {
					t.Error("root listing mismatch")
				}
			case 2:
				if !strings.Contains(receipt.Content, "config.txt\tfile") {
					t.Error("relative src listing mismatch")
				}
			case 3:
				if receipt.Content != "mode=initial" {
					t.Error("read did not use explicit root")
				}
			case 4:
				if receipt.Content != initial["README.md"] {
					t.Error("readme mismatch")
				}
			case 5:
				if receipt.Content != "" {
					t.Error("docs not empty")
				}
			case 6:
				if receipt.Content != initial["src/info.txt"] {
					t.Error("required evidence was not read before final plan")
				}
			}
		}
		if calls < len(sequence) {
			action := sequence[calls]
			args, _ := json.Marshal(map[string]string{"path": action.Path})
			quoted, _ := json.Marshal(string(args))
			respond(w, toolCallsResponse(toolCallJSON(string(rune('a'+calls)), action.Tool, string(quoted))))
		} else {
			encoded, _ := json.Marshal(plan)
			respond(w, `{"choices":[{"message":{"role":"assistant","content":`+string(encoded)+`}}]}`)
		}
		calls++
	}))
	defer server.Close()
	var output, prompts bytes.Buffer
	err := runWithContext(context.Background(), []string{"workspace", "--root", root, "plan", request}, strings.NewReader(strings.Repeat("y\n", len(sequence))), &output, &prompts, planEnvironment(server.URL))
	if err != nil {
		t.Fatalf("local fixture failed: %v; cause: %v; requests: %d; output: %s", err, errors.Unwrap(err), calls, output.String())
	}
	if calls != 7 || strings.Count(prompts.String(), "Permitir uma vez?") != 6 {
		t.Fatal("unexpected request/approval count", calls, prompts.String())
	}
	parts := strings.SplitN(output.String(), "Resumo do workspace:", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], plan) {
		t.Fatal("missing deliberate plan")
	}
	filesAndChanges := strings.SplitN(strings.SplitN(parts[0], "Arquivos prováveis\n", 2)[1], "Riscos e suposições\n", 2)[0]
	if strings.Contains(filesAndChanges, "README.md") || strings.Contains(filesAndChanges, "src/info.txt") ||
		!strings.Contains(filesAndChanges, "docs/NOTES.md") ||
		!strings.Contains(filesAndChanges, "src/config.txt: exclusivamente mode=initial -> mode=final, sem comentário adicional.") {
		t.Fatal("plan expanded the restricted changes")
	}
	blocks := strings.SplitN(parts[0], "Bloqueios ou informações faltantes\n", 2)[1]
	if strings.Contains(blocks, "acesso de escrita") {
		t.Fatal("read-only mode incorrectly treated as a blocker")
	}
	for _, want := range []string{"Aprovações solicitadas: 6", "Aprovações concedidas: 6", "Execuções: echo=0 list_dir=3 read_file=3 replace_file=0 create_file=0", "Escritas confirmadas: 0"} {
		if !strings.Contains(parts[1], want) {
			t.Fatal("incorrect summary", parts[1])
		}
	}
	if strings.Contains(parts[1], "src/config.txt") || strings.Contains(prompts.String(), "mode=initial") {
		t.Fatal("private data leak")
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("fixture structure/bytes/permissions changed")
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "EXTERNAL" {
		t.Fatal("external file changed", err)
	}
	docs, err := os.ReadDir(filepath.Join(root, "docs"))
	if err != nil || len(docs) != 0 {
		t.Fatal("docs changed", err)
	}
	t.Log("list_dir '.' usado; list_dir=3; read_file=3; create_file=0; replace_file=0; docs vazia; config exatamente mode=initial; bytes, estrutura, permissões e externo inalterados")
	t.Log("pedido original integral, byte a byte, em role=user nos 7 requests HTTP; ordem system,user,(assistant,tool)* preservada após todas as leituras")
}

func TestWorkspaceInstructionDoesNotLeakInPublicError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, workspaceInstruction(true, false, false))
	}))
	defer server.Close()
	var out, prompt bytes.Buffer
	err := runWithContext(context.Background(), []string{"workspace", "--root", t.TempDir(), "plan", "analyze"}, strings.NewReader(""), &out, &prompt, planEnvironment(server.URL))
	if err == nil {
		t.Fatal("expected provider failure")
	}
	for _, surface := range []string{out.String(), prompt.String(), err.Error()} {
		if strings.Contains(surface, "DAIMON workspace protocol v1") || strings.Contains(surface, planInstruction) {
			t.Fatal("instruction leak")
		}
	}
}
