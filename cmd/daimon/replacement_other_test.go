//go:build !linux

package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/editcontract"
)

func TestReplacementChatUnsupportedPlatform(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("fixture.txt", []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, toolCallsResponse(toolCallJSON("edit", "replace_file", `"{\"path\":\"fixture.txt\",\"content\":\"new\"}"`)))
	}))
	defer server.Close()
	err := runWithContext(context.Background(), []string{"chat", "--enable-replace-file", "replace fixture"}, strings.NewReader("y\n"), io.Discard, io.Discard, chatEnvironment(server.URL, "offline", ""))
	if !errors.Is(err, editcontract.ErrUnsupported) {
		t.Fatal(err)
	}
	err = runWithContext(context.Background(), []string{"workspace", "--root", ".", "--enable-replace-file", "replace fixture"}, strings.NewReader("y\n"), io.Discard, io.Discard, chatEnvironment(server.URL, "offline", ""))
	if !errors.Is(err, editcontract.ErrUnsupported) {
		t.Fatal(err)
	}
	data, err := os.ReadFile("fixture.txt")
	if err != nil || string(data) != "original" {
		t.Fatal("unexpected write", err)
	}
}
