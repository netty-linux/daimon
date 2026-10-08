package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiagnosticOptInPrivacy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		root := t.TempDir()
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				respond(w, finalResponse("FILE_CONTENT_SECRET"))
			} else {
				encoded, _ := json.Marshal(scopeAnswer(goodScopeJSON))
				respond(w, `{"choices":[{"message":{"role":"assistant","content":`+string(encoded)+`}}]}`)
			}
		}))
		args := []string{"workspace", "--root", root}
		if enabled {
			args = append(args, "--diagnostic")
		}
		args = append(args, "plan", "--validate-scope", scopedRequest)
		var out, stderr bytes.Buffer
		err := runWithContext(context.Background(), args, strings.NewReader(""), &out, &stderr, chatEnvironment(server.URL, "offline", "API_SECRET"))
		server.Close()
		if err != nil || calls != 2 {
			t.Fatal("flow changed", err)
		}
		if enabled {
			for _, want := range []string{"validate_scope_template_recognized=true", "final_validation_requested=2", "final_validation_rejected=1", "final_validation_accepted=1", "recovery_model_requested=1"} {
				if !strings.Contains(stderr.String(), want) {
					t.Fatal("missing diagnostic", want)
				}
			}
		} else if stderr.Len() != 0 {
			t.Fatal("implicit diagnostic")
		}
		for _, secret := range []string{"API_SECRET", "FILE_CONTENT_SECRET", root, scopedRequest} {
			if strings.Contains(stderr.String(), secret) || strings.Contains(out.String(), secret) {
				t.Fatal("diagnostic/summary leakage")
			}
		}
	}
}
func TestDiagnosticMissingRequestBeforeProvider(t *testing.T) {
	root := t.TempDir()
	err := runWithContext(context.Background(), []string{"workspace", "--root", root, "--diagnostic", "plan"}, strings.NewReader(""), io.Discard, io.Discard, func(string) string { t.Fatal("provider accessed"); return "" })
	if err == nil {
		t.Fatal("missing request accepted")
	}
}
