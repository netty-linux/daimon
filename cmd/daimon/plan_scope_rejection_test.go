package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopeFinalRejectionFlows(t *testing.T) {
	cases := []struct {
		name, first, second string
		recovery            bool
	}{
		{"empty first", "", "", false},
		{"fenced first", "```json\n" + scopeAnswer(goodScopeJSON) + "\n```", scopeAnswer(goodScopeJSON), true},
		{"duplicate heading", strings.Replace(scopeAnswer(goodScopeJSON), "Diagnóstico", "Diagnóstico\nDiagnóstico", 1), scopeAnswer(goodScopeJSON), true},
		{"out of order", strings.Replace(scopeAnswer(goodScopeJSON), "Diagnóstico", "Validação proposta", 1), scopeAnswer(goodScopeJSON), true},
		{"empty recovery", "PRIVATE_INVALID_FINAL", "", true},
		{"fenced recovery", "PRIVATE_INVALID_FINAL", "```\n" + scopeAnswer(goodScopeJSON) + "\n```", true},
		{"duplicate JSON recovery", "PRIVATE_INVALID_FINAL", scopeAnswer(strings.Replace(goodScopeJSON, `"delete":[]`, `"delete":[],"delete":[]`, 1)), true},
		{"out of scope recovery", "PRIVATE_INVALID_FINAL", scopeAnswer(strings.Replace(goodScopeJSON, "docs/NOTES.md", "README.md", 1)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "sentinel.txt")
			if err := os.WriteFile(file, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Tools    []json.RawMessage
					Messages []struct{ Role, Content string }
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("credential unexpectedly supplied")
				}
				if calls > 0 && len(req.Tools) != 0 {
					t.Error("recovery received tools")
				}
				if len(req.Messages) < 2 || req.Messages[1].Content != scopedRequest {
					t.Error("original request lost")
				}
				answer := tc.first
				if calls > 0 {
					answer = tc.second
				}
				calls++
				if calls > 2 {
					t.Error("more than one recovery")
				}
				encoded, _ := json.Marshal(answer)
				respond(w, `{"choices":[{"message":{"role":"assistant","content":`+string(encoded)+`}}]}`)
			}))
			defer server.Close()
			var out, stderr bytes.Buffer
			err := runWithContext(context.Background(), []string{"workspace", "--root", root, "plan", "--validate-scope", scopedRequest}, strings.NewReader(""), &out, &stderr, planEnvironment(server.URL))
			wantCalls := 1
			if tc.recovery {
				wantCalls = 2
			}
			valid := tc.recovery && tc.second == scopeAnswer(goodScopeJSON)
			if calls != wantCalls || (err == nil) != valid {
				t.Fatalf("unexpected outcome: calls=%d err=%v", calls, err)
			}
			if !valid && strings.Contains(out.String(), "Plano proposto") {
				t.Fatal("invalid plan displayed")
			}
			if strings.Contains(out.String()+stderr.String(), "PRIVATE_INVALID_FINAL") || strings.Contains(stderr.String(), "Permitir uma vez?") {
				t.Fatal("invalid output or additional approval")
			}
			data, readErr := os.ReadFile(file)
			entries, listErr := os.ReadDir(root)
			if readErr != nil || listErr != nil || string(data) != "unchanged" || len(entries) != 1 {
				t.Fatal("fixture changed")
			}
			if !strings.Contains(out.String(), "Escritas confirmadas: 0") {
				t.Fatal("write counter changed")
			}
			t.Logf("requests=%d recovery=%d recovery_tools=0 writes=0", calls, calls-1)
		})
	}
}
