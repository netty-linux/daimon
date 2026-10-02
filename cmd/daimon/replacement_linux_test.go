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
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/editcontract"
)

type editDisplayWriter func([]byte) (int, error)

func (f editDisplayWriter) Write(b []byte) (int, error) { return f(b) }

func TestReplacementChatFailsClosed(t *testing.T) {
	for _, scenario := range []string{"default", "eof", "invalid", "cancel", "display", "partial", "changed", "removed", "symlink", "hardlink", "limit", "reuse"} {
		t.Run(scenario, func(t *testing.T) {
			t.Chdir(t.TempDir())
			const original = "PRIVATE_ORIGINAL"
			content := "PRIVATE_PROPOSAL"
			if scenario == "limit" {
				content = strings.Repeat("x\n", 1001)
			}
			if err := os.WriteFile("fixture.txt", []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("other.txt", []byte("other"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			args, _ := json.Marshal(map[string]string{"path": "fixture.txt", "content": content})
			encoded, _ := json.Marshal(string(args))
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 || (scenario == "reuse" && calls == 2) {
					var req struct {
						Tools []struct{ Function struct{ Name string } }
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					found := false
					for _, tool := range req.Tools {
						found = found || tool.Function.Name == "replace_file"
					}
					if found != (scenario != "default") {
						t.Error("incorrect opt-in exposure")
					}
					id := "edit1"
					if calls == 2 {
						id = "edit2"
					}
					respond(w, toolCallsResponse(toolCallJSON(id, "replace_file", string(encoded))))
					return
				}
				var req struct {
					Messages []struct{ Role, Content string }
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				for _, m := range req.Messages {
					if m.Role == "tool" && (strings.Contains(m.Content, original) || strings.Contains(m.Content, "PRIVATE_PROPOSAL")) {
						t.Error("sensitive receipt")
					}
				}
				respond(w, finalResponse("done"))
			}))
			defer server.Close()
			var stdout, preview bytes.Buffer
			mutated := false
			writer := editDisplayWriter(func(b []byte) (int, error) {
				if scenario == "display" {
					return 0, errors.New("PRIVATE_PROPOSAL")
				}
				if scenario == "partial" {
					return len(b) - 1, nil
				}
				if !mutated && strings.Contains(string(b), "Approve this proposal once?") {
					mutated = true
					var err error
					switch scenario {
					case "cancel":
						cancel()
					case "changed":
						err = os.WriteFile("fixture.txt", []byte("changed"), 0600)
					case "removed":
						err = os.Remove("fixture.txt")
					case "symlink", "hardlink":
						err = os.Remove("fixture.txt")
						if err == nil {
							if scenario == "symlink" {
								err = os.Symlink("other.txt", "fixture.txt")
							} else {
								err = os.Link("other.txt", "fixture.txt")
							}
						}
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				return preview.Write(b)
			})
			answer := "y\n"
			if scenario == "eof" {
				answer = ""
			}
			if scenario == "invalid" {
				answer = "maybe\n"
			}
			cli := []string{"chat", "--enable-replace-file", "replace fixture"}
			if scenario == "default" {
				cli = []string{"chat", "replace fixture"}
			}
			err := runWithContext(ctx, cli, strings.NewReader(answer), &stdout, writer, chatEnvironment(server.URL, "offline", ""))
			denied := scenario == "default" || scenario == "eof" || scenario == "invalid"
			if denied && (err != nil || calls != 2) {
				t.Fatal(err, calls)
			}
			if !denied && err == nil {
				t.Fatal("expected controlled failure")
			}
			if err != nil && (strings.Contains(err.Error(), original) || strings.Contains(err.Error(), "PRIVATE_PROPOSAL")) {
				t.Fatal("sensitive error")
			}
			if strings.Contains(stdout.String(), original) || strings.Contains(stdout.String(), "PRIVATE_PROPOSAL") {
				t.Fatal("sensitive routine output")
			}
			if (scenario == "default" || scenario == "limit") && preview.Len() != 0 {
				t.Fatal("unexpected preview")
			}
			want := original
			switch scenario {
			case "changed":
				want = "changed"
			case "symlink", "hardlink":
				want = "other"
			case "reuse":
				want = content
			}
			data, readErr := os.ReadFile("fixture.txt")
			if scenario == "removed" {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal(readErr)
				}
			} else if readErr != nil || string(data) != want {
				t.Fatal("unexpected write", readErr)
			}
		})
	}
}

func TestLocalReplacementChatSmoke(t *testing.T) {
	for _, answer := range []string{"y\n", "n\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.WriteFile("fixture.txt", []byte("original"), 0640); err != nil {
				t.Fatal(err)
			}
			calls := 0
			content := "approved\r\n\\r\x1b"
			args, _ := json.Marshal(map[string]string{"path": "fixture.txt", "content": content})
			encoded, _ := json.Marshal(string(args))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if calls == 1 {
					respond(w, toolCallsResponse(toolCallJSON("edit1", "replace_file", string(encoded))))
					return
				}
				if bytes.Contains(body, []byte("original")) || bytes.Contains(body, []byte("Proposed content (complete):")) || bytes.Contains(body, []byte("sha256=")) {
					t.Error("runtime added private preview data to provider request")
				}
				var request struct {
					Messages []struct {
						Content    string
						ToolCallID string `json:"tool_call_id"`
					}
				}
				if err := json.Unmarshal(body, &request); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				want := "tool denied"
				if answer == "y\n" {
					want = "file replaced"
				}
				if len(request.Messages) != 3 || request.Messages[2].Content != want || request.Messages[2].ToolCallID != "edit1" {
					t.Error("incorrect edit receipt")
				}
				respond(w, finalResponse("done"))
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			if err := runWithContext(context.Background(), []string{"chat", "--enable-replace-file", "replace fixture"}, strings.NewReader(answer), &stdout, &stderr, chatEnvironment(server.URL, "local-smoke", "")); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile("fixture.txt")
			if err != nil {
				t.Fatal(err)
			}
			want := "original"
			if answer == "y\n" {
				want = content
			}
			if string(data) != want {
				t.Fatal("incorrect resulting file", string(data))
			}
			if !strings.Contains(stderr.String(), "Proposed content (complete):") || !strings.Contains(stdout.String(), "Stop reason: completed") || calls != 2 {
				t.Fatal(stdout.String(), stderr.String(), calls)
			}
		})
	}
}

func TestTwoReplacementCallsCannotExecuteBatch(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, p := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(p, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, toolCallsResponse(toolCallJSON("e1", "replace_file", `"{\"path\":\"one.txt\",\"content\":\"new\"}"`), toolCallJSON("e2", "replace_file", `"{\"path\":\"two.txt\",\"content\":\"new\"}"`)))
	}))
	defer server.Close()
	var stderr bytes.Buffer
	err := runWithContext(context.Background(), []string{"chat", "--enable-replace-file", "two edits"}, strings.NewReader("y\ny\n"), io.Discard, &stderr, chatEnvironment(server.URL, "local-smoke", ""))
	var authorization *agentloop.AuthorizationError
	if !errors.As(err, &authorization) || !errors.Is(err, editcontract.ErrUsed) {
		t.Fatal(err)
	}
	for _, p := range []string{"one.txt", "two.txt"} {
		data, err := os.ReadFile(p)
		if err != nil || string(data) != "old" {
			t.Fatal("batch wrote", p, err)
		}
	}
	if strings.Count(stderr.String(), "Approve this proposal once?") != 1 {
		t.Fatal("unexpected second approval")
	}
}

func TestReadAndReplaceShareApprovalInput(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("file.txt", []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			respond(w, toolCallsResponse(toolCallJSON("read", "read_file", `"{\"path\":\"file.txt\"}"`), toolCallJSON("edit", "replace_file", `"{\"path\":\"file.txt\",\"content\":\"new\"}"`)))
			return
		}
		respond(w, finalResponse("done"))
	}))
	defer server.Close()
	var stderr bytes.Buffer
	if err := runWithContext(context.Background(), []string{"chat", "--enable-replace-file", "read then replace"}, strings.NewReader("y\ny\n"), io.Discard, &stderr, chatEnvironment(server.URL, "local-smoke", "")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("file.txt")
	if err != nil || string(data) != "new" {
		t.Fatal(data, err)
	}
	if calls != 2 || !strings.Contains(stderr.String(), "Permitir uma vez?") || !strings.Contains(stderr.String(), "Approve this proposal once?") {
		t.Fatal(stderr.String(), calls)
	}
}
