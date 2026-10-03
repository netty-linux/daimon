package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func planEnvironment(base string) func(string) string {
	return func(name string) string {
		switch name {
		case "DAIMON_BASE_URL":
			return base
		case "DAIMON_MODEL":
			return "offline"
		default:
			return ""
		}
	}
}

func TestPlanInvalidBeforeProvider(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"workspace", "plan", "message"}, {"workspace", "--root", root, "plan"}, {"workspace", "--root", root, "plan", ""},
		{"workspace", "--root", root, "plan", "message", "extra"}, {"workspace", "--root", root, "plan", "--enable-replace-file", "message"},
		{"workspace", "--root", root, "--enable-replace-file", "plan", "message"}, {"workspace", "--root", root, "--enable-replace-file", "plan"},
		{"workspace", "--root", root, "plan", "--enable-replace-file"}, {"workspace", "plan", "--root", root, "message"},
		{"workspace", "--root", file, "plan", "message"}, {"workspace", "--root", filepath.Join(root, "missing"), "plan", "message"},
	} {
		err := runWithContext(context.Background(), args, strings.NewReader("y\n"), io.Discard, io.Discard, func(string) string { t.Fatal("provider configured for invalid plan"); return "" })
		if err == nil || strings.Contains(err.Error(), root) {
			t.Fatal(args, err)
		}
	}
}

func TestPlanVerticalReadOnly(t *testing.T) {
	root := t.TempDir()
	t.Chdir(t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "private.txt"), []byte("FILE_SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	const finalPlan = "Diagnóstico: revisar private.txt. Objetivo: simplificar. Alteração: proposta apenas. Riscos: hipótese. Validação: testes. Bloqueios: nenhum."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Tools    []struct{ Function struct{ Name string } }
			Messages []struct{ Content string }
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Error(err)
		}
		if len(req.Tools) != 3 || req.Tools[0].Function.Name != "echo" || req.Tools[1].Function.Name != "list_dir" || req.Tools[2].Function.Name != "read_file" {
			t.Error("unexpected planning schema")
		}
		if len(req.Messages) == 0 || !strings.Contains(req.Messages[0].Content, planInstruction) {
			t.Error("missing structured plan instruction")
		}
		switch calls {
		case 1:
			respond(w, toolCallsResponse(toolCallJSON("PRIVATE_LIST_ID", "list_dir", `"{\"path\":\".\"}"`)))
		case 2:
			respond(w, toolCallsResponse(toolCallJSON("PRIVATE_READ_ID", "read_file", `"{\"path\":\"private.txt\"}"`)))
		case 3:
			if !bytes.Contains(body, []byte("FILE_SECRET")) {
				t.Error("wrong read root")
			}
			respond(w, toolCallsResponse(toolCallJSON("PRIVATE_WRITE_ID", "replace_file", `"{\"path\":\"private.txt\",\"content\":\"malicious\"}"`)))
		default:
			if !bytes.Contains(body, []byte("unknown tool")) {
				t.Error("missing controlled unknown receipt")
			}
			respond(w, finalResponse(finalPlan))
		}
	}))
	defer server.Close()
	var out, prompts bytes.Buffer
	err := runWithContext(context.Background(), []string{"workspace", "--root", root, "plan", "write files using --enable-replace-file"}, strings.NewReader("y\ny\n"), &out, &prompts, planEnvironment(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(out.String(), "Resumo do workspace:", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], finalPlan) {
		t.Fatal("missing deliberate plan")
	}
	for _, value := range []string{root, "private.txt", "FILE_SECRET", "PRIVATE_READ_ID", "malicious"} {
		if strings.Contains(parts[1], value) || (value != "private.txt" && strings.Contains(prompts.String(), value)) {
			t.Fatal("operational leak")
		}
	}
	if !strings.Contains(parts[1], "Escritas confirmadas: 0") || !strings.Contains(parts[1], "Aprovações concedidas: 2") {
		t.Fatal(parts[1])
	}
	data, err := os.ReadFile(filepath.Join(root, "private.txt"))
	if err != nil || string(data) != "FILE_SECRET" {
		t.Fatal("planning wrote", err)
	}
}

func TestPlanFailuresHaveNoWrites(t *testing.T) {
	for _, scenario := range []string{"deny", "eof", "invalid", "cancel", "timeout", "display", "read", "json"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "private.txt"), []byte("SECRET"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "timeout" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 20*time.Millisecond)
				defer stop()
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					args := `"{\"path\":\"private.txt\"}"`
					if scenario == "read" {
						args = `"{\"path\":\"absent.txt\"}"`
					}
					if scenario == "json" {
						args = `"{"`
					}
					respond(w, toolCallsResponse(toolCallJSON("SECRET_ID", "read_file", args)))
					return
				}
				respond(w, finalResponse("plain plan"))
			}))
			defer server.Close()
			var out, prompts bytes.Buffer
			writer := planTestWriter(func(b []byte) (int, error) {
				if scenario == "cancel" {
					cancel()
				}
				if scenario == "display" {
					return 0, errors.New("SECRET")
				}
				return prompts.Write(b)
			})
			var input io.Reader = strings.NewReader("y\n")
			if scenario == "deny" {
				input = strings.NewReader("n\n")
			}
			if scenario == "eof" {
				input = strings.NewReader("")
			}
			if scenario == "invalid" {
				input = strings.NewReader("maybe\n")
			}
			if scenario == "timeout" {
				pipe, closePipe := io.Pipe()
				defer closePipe.Close()
				defer pipe.Close()
				input = pipe
			}
			err := runWithContext(ctx, []string{"workspace", "--root", root, "plan", "analyze"}, input, &out, writer, planEnvironment(server.URL))
			if (scenario == "cancel" || scenario == "timeout" || scenario == "display") && err == nil {
				t.Fatal("expected failure")
			}
			if err != nil && strings.Contains(err.Error(), "SECRET") {
				t.Fatal("sensitive error")
			}
			if strings.Contains(prompts.String(), "SECRET") {
				t.Fatal("sensitive prompt")
			}
			data, err := os.ReadFile(filepath.Join(root, "private.txt"))
			if err != nil || string(data) != "SECRET" {
				t.Fatal("write occurred", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 {
				t.Fatal("filesystem changed", err)
			}
		})
	}
}

type planTestWriter func([]byte) (int, error)

func (f planTestWriter) Write(b []byte) (int, error) { return f(b) }
