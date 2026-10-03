package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/agentloop"
	"time"
)

func TestWorkspaceInvalidBeforeProvider(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"workspace"}, {"workspace", "message"}, {"workspace", "--root", dir},
		{"workspace", "--root", "", "message"}, {"workspace", "--root", filepath.Join(dir, "absent"), "message"},
		{"workspace", "--root", file, "message"}, {"workspace", "--root", dir, ""},
		{"workspace", "--root", dir, "--bad", "message"}, {"workspace", "--root", dir, "--enable-replace-file"},
		{"workspace", "--root", dir, "--enable-replace-file", "message", "extra"},
	} {
		var out bytes.Buffer
		err := runWithContext(context.Background(), args, strings.NewReader(""), &out, io.Discard, func(string) string { t.Fatal("provider configuration read"); return "" })
		if err == nil || strings.Contains(err.Error(), dir) || out.Len() != 0 {
			t.Fatal(args, err)
		}
	}
}

func TestWorkspaceReadRootAndSafeSummary(t *testing.T) {
	cwd, root := t.TempDir(), t.TempDir()
	t.Chdir(cwd)
	if err := os.WriteFile("outside-secret.txt", []byte("OUTSIDE_SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("INSIDE_SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"name":"replace_file"`)) {
			t.Error("writing schema exposed")
		}
		switch calls {
		case 1:
			respond(w, toolCallsResponse(toolCallJSON("private-list-id", "list_dir", `"{\"path\":\".\"}"`)))
		case 2:
			if !bytes.Contains(body, []byte("inside.txt")) || bytes.Contains(body, []byte("outside-secret.txt")) {
				t.Error("incorrect listing root")
			}
			respond(w, toolCallsResponse(toolCallJSON("private-read-id", "read_file", `"{\"path\":\"inside.txt\"}"`)))
		case 3:
			if !bytes.Contains(body, []byte("INSIDE_SECRET")) {
				t.Error("incorrect read root")
			}
			respond(w, toolCallsResponse(toolCallJSON("private-outside-id", "read_file", `"{\"path\":\"../outside-secret.txt\"}"`)))
		default:
			if bytes.Contains(body, []byte("OUTSIDE_SECRET")) {
				t.Error("escaped root")
			}
			respond(w, finalResponse("INSIDE_SECRET private-read-id"))
		}
	}))
	defer server.Close()
	var out bytes.Buffer
	err := runWithContext(context.Background(), []string{"workspace", "--root", root, "--enable-replace-file is only message text"}, strings.NewReader("y\ny\ny\n"), &out, io.Discard, chatEnvironment(server.URL, "offline", ""))
	// A message beginning with a flag is invalid; a flag in ordinary text must not opt in.
	if err == nil {
		t.Fatal("flag accepted as message")
	}
	err = runWithContext(context.Background(), []string{"workspace", "--root", root, "text mentions --enable-replace-file"}, strings.NewReader("y\ny\ny\n"), &out, io.Discard, chatEnvironment(server.URL, "offline", ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{root, "INSIDE_SECRET", "OUTSIDE_SECRET", "inside.txt", "private-read-id"} {
		if strings.Contains(out.String(), sensitive) {
			t.Fatal("summary leak")
		}
	}
	for _, expected := range []string{"Motivo de parada: completed", "Aprovações concedidas: 3", "list_dir=1 read_file=2 replace_file=0", "Escritas confirmadas: 0"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatal(out.String())
		}
	}
}

func TestWorkspaceSummaryUsesOnlyPublicCounters(t *testing.T) {
	var out bytes.Buffer
	err := printWorkspaceSummary(&out, agentloop.Result{FinalAnswer: "SECRET", StopReason: agentloop.StopReasonCanceled}, []agentloop.Event{{Kind: agentloop.ApprovalRequested}, {Kind: agentloop.ApprovalDenied}}, &workspaceCounts{}, time.Millisecond)
	if err != nil || strings.Contains(out.String(), "SECRET") || !strings.Contains(out.String(), "Aprovações negadas: 1") {
		t.Fatal(err, out.String())
	}
}
