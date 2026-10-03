package main

import (
	"bytes"
	"context"
	"encoding/json"
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
