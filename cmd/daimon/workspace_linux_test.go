package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceVerticalApprovedReplacement(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("replacement requires Linux")
	}
	root := t.TempDir()
	t.Chdir(t.TempDir())
	if err := os.WriteFile("file.txt", []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		switch calls {
		case 1:
			if !bytes.Contains(body, []byte(`"name":"replace_file"`)) {
				t.Error("missing opt-in tool")
			}
			respond(w, toolCallsResponse(toolCallJSON("l", "list_dir", `"{\"path\":\".\"}"`)))
		case 2:
			respond(w, toolCallsResponse(toolCallJSON("r", "read_file", `"{\"path\":\"file.txt\"}"`)))
		case 3:
			respond(w, toolCallsResponse(toolCallJSON("w", "replace_file", `"{\"path\":\"file.txt\",\"content\":\"approved\"}"`)))
		default:
			if !bytes.Contains(body, []byte("file replaced")) {
				t.Error("missing receipt")
			}
			respond(w, finalResponse("approved file.txt original"))
		}
	}))
	defer server.Close()
	var out, preview bytes.Buffer
	err := runWithContext(context.Background(), []string{"workspace", "--root", root, "--enable-replace-file", "develop"}, strings.NewReader("y\ny\ny\n"), &out, &preview, chatEnvironment(server.URL, "offline", ""))
	if err != nil {
		t.Fatal(err)
	}
	inside, err := os.ReadFile(filepath.Join(root, "file.txt"))
	if err != nil || string(inside) != "approved" {
		t.Fatal("wrong replacement", err)
	}
	outside, err := os.ReadFile("file.txt")
	if err != nil || string(outside) != "outside" {
		t.Fatal("escaped root", err)
	}
	if !strings.Contains(preview.String(), "Proposed content (complete):") {
		t.Fatal("missing preview")
	}
	for _, expected := range []string{"Model steps: 4", "Approvals requested: 3", "Approvals granted: 3", "echo=0 list_dir=1 read_file=1 replace_file=1", "Completed writes: 1"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatal(out.String())
		}
	}
	for _, sensitive := range []string{root, "file.txt", "approved", "original"} {
		if strings.Contains(out.String(), sensitive) {
			t.Fatal("summary leak")
		}
	}
}
