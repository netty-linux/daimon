//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/createcontract"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCreateBatchFailsBeforeEffects(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, toolCallsResponse(
			toolCallJSON("c1", "create_file", `"{\"path\":\"one\",\"content\":\"x\"}"`),
			toolCallJSON("c2", "create_file", `"{\"path\":\"two\",\"content\":\"x\"}"`),
		))
	}))
	defer server.Close()
	var preview bytes.Buffer
	err := runWithContext(context.Background(), []string{"workspace", "--root", root, "--enable-create-file", "two creations"}, strings.NewReader("y\ny\n"), io.Discard, &preview, planEnvironment(server.URL))
	if !errors.Is(err, createcontract.ErrUsed) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("batch effects", err)
	}
	if strings.Count(preview.String(), "Aprovar esta proposta uma vez?") != 1 {
		t.Fatal("unexpected second prompt")
	}
}

func TestCreateVerticalOffline(t *testing.T) {
	for _, scenario := range []string{"allow", "deny", "eof", "invalid", "display", "partial", "cancel", "timeout", "limit", "conflict", "reuse"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(t.TempDir())
			if err := os.Mkdir(filepath.Join(root, "parent"), 0700); err != nil {
				t.Fatal(err)
			}
			content := "PRIVATE_CONTENT\r\n\\n\x1bé"
			if scenario == "limit" {
				content = strings.Repeat("x\n", 1001)
			}
			args, _ := json.Marshal(map[string]string{"path": "parent/new.txt", "content": content})
			encoded, _ := json.Marshal(string(args))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "timeout" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					Tools    []struct{ Function struct{ Name string } }
					Messages []struct{ Role, Content string }
				}
				body, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(body, &req); err != nil {
					t.Error(err)
				}
				found := false
				for _, tool := range req.Tools {
					if tool.Function.Name == "replace_file" {
						t.Error("unrequested replacement schema")
					}
					if tool.Function.Name == "create_file" {
						found = true
					}
				}
				if !found || len(req.Tools) != 4 {
					t.Error("incorrect creation schema")
				}
				if calls == 1 || (scenario == "reuse" && calls == 2) {
					id := "PRIVATE_ID"
					if calls == 2 {
						id = "PRIVATE_ID_2"
					}
					respond(w, toolCallsResponse(toolCallJSON(id, "create_file", string(encoded))))
					return
				}
				want := "tool denied"
				if scenario == "allow" {
					want = "file created"
				}
				if len(req.Messages) != 4 || req.Messages[3].Content != want {
					t.Error("incorrect receipt")
				}
				if bytes.Contains(body, []byte("Alvo: AUSENTE")) || bytes.Contains(body, []byte("Vínculo do workspace:")) {
					t.Error("runtime leaked preview to provider")
				}
				respond(w, finalResponse("PRIVATE_CONTENT parent/new.txt PRIVATE_ID"))
			}))
			defer server.Close()
			answer := "y\n"
			if scenario == "deny" {
				answer = "n\n"
			}
			if scenario == "invalid" {
				answer = "maybe\n"
			}
			if scenario == "eof" {
				answer = ""
			}
			var input io.Reader = strings.NewReader(answer)
			if scenario == "timeout" {
				reader, writer := io.Pipe()
				defer reader.Close()
				defer writer.Close()
				input = reader
			}
			var out, preview bytes.Buffer
			changed := false
			display := planTestWriter(func(b []byte) (int, error) {
				if scenario == "display" {
					return 0, errors.New("PRIVATE_CONTENT")
				}
				if scenario == "partial" {
					return len(b) - 1, nil
				}
				if strings.Contains(string(b), "Aprovar esta proposta uma vez?") && !changed {
					changed = true
					if scenario == "cancel" {
						cancel()
					}
					if scenario == "conflict" {
						if err := os.WriteFile(filepath.Join(root, "parent/new.txt"), []byte("competitor"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				return preview.Write(b)
			})
			err := runWithContext(ctx, []string{"workspace", "--root", root, "--enable-create-file", "create a file"}, input, &out, display, planEnvironment(server.URL))
			success := scenario == "allow" || scenario == "deny" || scenario == "eof" || scenario == "invalid"
			if success && err != nil {
				t.Fatal(err)
			}
			if !success && err == nil {
				t.Fatal("expected controlled failure")
			}
			if err != nil && (strings.Contains(err.Error(), "PRIVATE_CONTENT") || strings.Contains(err.Error(), root)) {
				t.Fatal("sensitive error")
			}
			for _, value := range []string{"PRIVATE_CONTENT", "PRIVATE_ID", "parent/new.txt", root} {
				if strings.Contains(out.String(), value) {
					t.Fatal("public summary leak")
				}
			}
			if scenario == "allow" || scenario == "reuse" {
				data, err := os.ReadFile(filepath.Join(root, "parent/new.txt"))
				if err != nil || string(data) != content {
					t.Fatal("wrong created bytes", err)
				}
				info, err := os.Stat(filepath.Join(root, "parent/new.txt"))
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("wrong permissions", err)
				}
				if !strings.Contains(preview.String(), strconv.QuoteToASCII(content)) || !strings.Contains(preview.String(), "AUSENTE na preparação") {
					t.Fatal("incomplete preview")
				}
				if !strings.Contains(out.String(), "Escritas confirmadas: 1") {
					t.Fatal(out.String())
				}
			} else if scenario == "conflict" {
				data, err := os.ReadFile(filepath.Join(root, "parent/new.txt"))
				if err != nil || string(data) != "competitor" {
					t.Fatal("competitor modified", err)
				}
			} else {
				if _, err := os.Lstat(filepath.Join(root, "parent/new.txt")); !os.IsNotExist(err) {
					t.Fatal("unexpected file", err)
				}
			}
			if _, err := os.Lstat("parent/new.txt"); !os.IsNotExist(err) {
				t.Fatal("created outside root", err)
			}
			if scenario == "reuse" && strings.Count(preview.String(), "Aprovar esta proposta uma vez?") != 1 {
				t.Fatal("approval reused")
			}
		})
	}
}
