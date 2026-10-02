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
			if err := runWithContext(context.Background(), []string{"chat", "replace fixture"}, strings.NewReader(answer), &stdout, &stderr, chatEnvironment(server.URL, "local-smoke", "")); err != nil {
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
	err := runWithContext(context.Background(), []string{"chat", "two edits"}, strings.NewReader("y\ny\n"), io.Discard, &stderr, chatEnvironment(server.URL, "local-smoke", ""))
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
	if err := runWithContext(context.Background(), []string{"chat", "read then replace"}, strings.NewReader("y\ny\n"), io.Discard, &stderr, chatEnvironment(server.URL, "local-smoke", "")); err != nil {
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
