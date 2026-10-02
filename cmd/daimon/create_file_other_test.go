//go:build !linux

package main

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/createcontract"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateWorkspaceUnsupported(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, toolCallsResponse(toolCallJSON("c", "create_file", `"{\"path\":\"new\",\"content\":\"x\"}"`)))
	}))
	defer server.Close()
	err := runWithContext(context.Background(), []string{"workspace", "--root", root, "--enable-create-file", "create"}, strings.NewReader("y\n"), io.Discard, io.Discard, planEnvironment(server.URL))
	if !errors.Is(err, createcontract.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "new")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
