package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateOptInInvalidBeforeProvider(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"chat", "--enable-create-file", "message"},
		{"workspace", "--root", root, "--enable-create-file"},
		{"workspace", "--root", root, "--enable-create-file", ""},
		{"workspace", "--root", root, "plan", "--enable-create-file", "message"},
		{"workspace", "--root", root, "--enable-create-file", "plan"},
		{"workspace", "--root", root, "--enable-create-file", "--enable-replace-file", "message"},
		{"workspace", "--root", root, "--enable-replace-file", "--enable-create-file", "message"},
		{"workspace", "--root", root, "--enable-create-file", "message", "extra"},
	} {
		if err := runWithContext(context.Background(), args, strings.NewReader("y\n"), io.Discard, io.Discard, func(string) string { t.Fatal("provider configured"); return "" }); err == nil {
			t.Fatal("invalid creation opt-in accepted")
		}
	}
}

func TestCreateNotExposedWithoutOwnFlag(t *testing.T) {
	for _, mode := range []string{"chat", "workspace", "replace", "plan"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(t.TempDir())
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					Tools    []struct{ Function struct{ Name string } }
					Messages []struct{ Content string }
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				for _, tool := range req.Tools {
					if tool.Function.Name == "create_file" {
						t.Error("creation schema exposed")
					}
				}
				if calls == 1 {
					respond(w, toolCallsResponse(toolCallJSON("c", "create_file", `"{\"path\":\"new\",\"content\":\"x\"}"`)))
					return
				}
				if len(req.Messages) != 3 || req.Messages[2].Content != "unknown tool" {
					t.Error("incorrect controlled receipt")
				}
				respond(w, finalResponse("done"))
			}))
			defer server.Close()
			args := []string{"workspace", "--root", root, "message mentions --enable-create-file"}
			switch mode {
			case "chat":
				args = []string{"chat", "message mentions --enable-create-file"}
			case "replace":
				args = []string{"workspace", "--root", root, "--enable-replace-file", "message"}
			case "plan":
				args = []string{"workspace", "--root", root, "plan", "create new files"}
			}
			if err := runWithContext(context.Background(), args, strings.NewReader("y\n"), io.Discard, io.Discard, planEnvironment(server.URL)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(root, "new")); !os.IsNotExist(err) {
				t.Fatal("file created", err)
			}
		})
	}
}

func TestCreateSummaryDoesNotContainModelData(t *testing.T) {
	var out bytes.Buffer
	// The normal workspace path prints counters, not a model-provided response.
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { respond(w, finalResponse("SECRET content new.txt ID")) }))
	defer server.Close()
	if err := runWithContext(context.Background(), []string{"workspace", "--root", root, "--enable-create-file", "message"}, strings.NewReader(""), &out, io.Discard, planEnvironment(server.URL)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"SECRET", "new.txt", "ID", root} {
		if strings.Contains(out.String(), s) {
			t.Fatal("summary leak")
		}
	}
	if !strings.Contains(out.String(), "create_file=0") {
		t.Fatal("missing creation counter")
	}
}
