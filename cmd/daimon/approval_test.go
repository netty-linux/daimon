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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func respond(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, body)
}

func toolCallJSON(id, name, arguments string) string {
	return `{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` + arguments + `}}`
}

func toolCallsResponse(calls ...string) string {
	return `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[` + strings.Join(calls, ",") + `]}}]}`
}

func finalResponse(text string) string {
	return `{"choices":[{"message":{"role":"assistant","content":"` + text + `"}}]}`
}

func messageContents(t *testing.T, body []byte) []string {
	t.Helper()
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("request body: %v", err)
	}
	contents := make([]string, 0, len(request.Messages))
	for _, message := range request.Messages {
		contents = append(contents, message.Content)
	}
	return contents
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func chatRun(t *testing.T, stdin string, stderr io.Writer, url string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runWithContext(context.Background(), []string{"chat", "explore"}, strings.NewReader(stdin), &out, stderr, chatEnvironment(url+"/v1", "test-model", ""))
	return out.String(), err
}

// The full milestone flow: list_dir approved, listing reaches the provider,
// read_file approved, content reaches the provider, final answer. Everything
// offline, on a temporary workspace, with terminal input injected.
func TestChatVerticalApproval(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("alpha.txt", []byte("alpha content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("docs", 0700); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		switch calls.Add(1) {
		case 1:
			respond(w, toolCallsResponse(toolCallJSON("list-1", "list_dir", `"{\"path\":\".\"}"`)))
		case 2:
			contents := messageContents(t, body)
			if !containsString(contents, "alpha.txt\tfile") || !containsString(contents, "docs\tdirectory") {
				t.Errorf("listing receipt missing: %v", contents)
			}
			respond(w, toolCallsResponse(toolCallJSON("read-1", "read_file", `"{\"path\":\"alpha.txt\"}"`)))
		case 3:
			contents := messageContents(t, body)
			if !containsString(contents, "alpha content") {
				t.Errorf("file content missing: %v", contents)
			}
			respond(w, finalResponse("explorado"))
		default:
			t.Error("unexpected extra model call")
			w.WriteHeader(500)
		}
	}))
	defer s.Close()
	var stderr bytes.Buffer
	out, err := chatRun(t, "y\ny\n", &stderr, s.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Resposta final: explorado", "Chamadas de ferramenta: 2", "Motivo de parada: completed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	for _, want := range []string{"Ferramenta: list_dir", "Ferramenta: read_file", "Caminho relativo: \"alpha.txt\""} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %q in prompts: %s", want, stderr.String())
		}
	}
	if got := strings.Count(stderr.String(), "Permitir uma vez?"); got != 2 {
		t.Fatalf("prompts=%d: %s", got, stderr.String())
	}
	// File contents are never shown in the approval prompt.
	if strings.Contains(stderr.String(), "alpha content") || strings.Contains(stderr.String(), "alpha.txt\tfile") {
		t.Fatalf("prompt leaked results: %s", stderr.String())
	}
}

func TestChatApprovalDeniedByAnswer(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("secret.txt", []byte("internal secret"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{"n\n", "maybe\n", "\n"} {
		var calls atomic.Int32
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if bytes.Contains(body, []byte("internal secret")) {
				t.Error("file content sent to server")
			}
			switch calls.Add(1) {
			case 1:
				respond(w, toolCallsResponse(toolCallJSON("read-1", "read_file", `"{\"path\":\"secret.txt\"}"`)))
			case 2:
				if contents := messageContents(t, body); !containsString(contents, "tool denied") {
					t.Errorf("denial receipt missing: %v", contents)
				}
				respond(w, finalResponse("negado"))
			default:
				t.Error("unexpected extra model call")
				w.WriteHeader(500)
			}
		}))
		var stderr bytes.Buffer
		out, err := chatRun(t, answer, &stderr, s.URL)
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Resposta final: negado") || strings.Contains(out, "internal secret") {
			t.Fatalf("out=%s", out)
		}
		if !strings.Contains(stderr.String(), "Permitir uma vez?") {
			t.Fatalf("no prompt: %s", stderr.String())
		}
		if calls.Load() != 2 {
			t.Fatalf("calls=%d", calls.Load())
		}
	}
}

func TestChatTwoCallsNeedTwoApprovals(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("a.txt", []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("b.txt", []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		switch calls.Add(1) {
		case 1:
			respond(w, toolCallsResponse(toolCallJSON("read-1", "read_file", `"{\"path\":\"a.txt\"}"`)))
		case 2:
			if contents := messageContents(t, body); !containsString(contents, "first") {
				t.Errorf("first receipt missing: %v", contents)
			}
			respond(w, toolCallsResponse(toolCallJSON("read-2", "read_file", `"{\"path\":\"b.txt\"}"`)))
		case 3:
			if contents := messageContents(t, body); !containsString(contents, "second") {
				t.Errorf("second receipt missing: %v", contents)
			}
			respond(w, finalResponse("ambos"))
		default:
			t.Error("unexpected extra model call")
			w.WriteHeader(500)
		}
	}))
	defer s.Close()
	var stderr bytes.Buffer
	out, err := chatRun(t, "y\ny\n", &stderr, s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Resposta final: ambos") {
		t.Fatalf("out=%s", out)
	}
	if got := strings.Count(stderr.String(), "Permitir uma vez?"); got != 2 {
		t.Fatalf("prompts=%d: approvals must never be remembered", got)
	}
}

func TestChatUnknownToolNeverPrompts(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		switch calls.Add(1) {
		case 1:
			respond(w, toolCallsResponse(toolCallJSON("b-1", "bash", `"{\"cmd\":\"ls\"}"`)))
		case 2:
			if contents := messageContents(t, body); !containsString(contents, "unknown tool") {
				t.Errorf("receipt missing: %v", contents)
			}
			respond(w, finalResponse("sem bash"))
		default:
			t.Error("unexpected extra model call")
			w.WriteHeader(500)
		}
	}))
	defer s.Close()
	var stderr bytes.Buffer
	out, err := chatRun(t, "y\n", &stderr, s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Resposta final: sem bash") {
		t.Fatalf("out=%s", out)
	}
	if strings.Contains(stderr.String(), "DAIMON solicita leitura:") {
		t.Fatalf("unknown tool prompted: %s", stderr.String())
	}
}

func TestChatApprovalSanitizesTerminalEscape(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		switch calls.Add(1) {
		case 1:
			respond(w, toolCallsResponse(toolCallJSON("read-1", "read_file", `"{\"path\":\"a\\u001b[31m.txt\"}"`)))
		case 2:
			if contents := messageContents(t, body); !containsString(contents, "tool denied") {
				t.Errorf("receipt missing: %v", contents)
			}
			respond(w, finalResponse("escapado"))
		default:
			t.Error("unexpected extra model call")
			w.WriteHeader(500)
		}
	}))
	defer s.Close()
	var stderr bytes.Buffer
	out, err := chatRun(t, "", &stderr, s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Resposta final: escapado") {
		t.Fatalf("out=%s", out)
	}
	if strings.ContainsRune(stderr.String(), '\x1b') {
		t.Fatalf("escape byte reached the terminal: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Caminho relativo: \"a\\x1b[31m.txt\"") {
		t.Fatalf("sanitized path missing: %q", stderr.String())
	}
}

// promptSignal captures stderr and fires when an approval prompt appears.
type promptSignal struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	once sync.Once
	ch   chan struct{}
}

func newPromptSignal() *promptSignal { return &promptSignal{ch: make(chan struct{})} }

func (w *promptSignal) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), "Permitir uma vez?") {
		w.once.Do(func() { close(w.ch) })
	}
	return n, err
}

func (w *promptSignal) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestChatCancellationDuringApproval(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			respond(w, toolCallsResponse(toolCallJSON("read-1", "read_file", `"{\"path\":\"a.txt\"}"`)))
			return
		}
		t.Error("run continued after cancellation")
		w.WriteHeader(500)
	}))
	defer s.Close()
	stderr := newPromptSignal()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stderr.ch:
			cancel()
		case <-time.After(2 * time.Second):
		}
	}()
	err := runWithContext(ctx, []string{"chat", "explore"}, pr, io.Discard, stderr, chatEnvironment(s.URL+"/v1", "test-model", ""))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
