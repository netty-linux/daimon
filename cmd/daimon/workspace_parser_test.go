package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestWorkspacePlanArguments(t *testing.T) {
	root := t.TempDir()
	for _, request := range []string{scopedRequest, "  Análise: espaços, \"aspas\" e UTF-8.  "} {
		for _, tail := range [][]string{{"plan", "--validate-scope", request}, {"plan", request, "--validate-scope"}, {"plan", request}} {
			args := append([]string{"workspace", "--root", root}, tail...)
			gotRoot, got, err := workspaceArguments(args)
			want := []string{"chat", "plan"}
			if len(tail) == 3 {
				want = append(want, "--validate-scope")
			}
			want = append(want, request)
			if err != nil || gotRoot != root || !reflect.DeepEqual(got, want) {
				t.Fatalf("parsing failed: %v", err)
			}
			if request == scopedRequest && recognizePlanScope(got[len(got)-1]) == nil {
				t.Fatal("scope not recognized")
			}
		}
	}
	for _, tail := range [][]string{
		{"plan"}, {"plan", "--validate-scope"}, {"plan", ""}, {"plan", "   "},
		{"plan", "--validate-scope", ""}, {"plan", scopedRequest, "extra"},
		{"plan", "--validate-scope", scopedRequest, "extra"},
		{"plan", "--validate-scope", scopedRequest, "--validate-scope"},
		{"plan", "--unknown", scopedRequest}, {"plan", scopedRequest, "--unknown"},
		{"plan", "-x", scopedRequest},
	} {
		_, _, err := workspaceArguments(append([]string{"workspace", "--root", root}, tail...))
		if !errors.Is(err, errWorkspace) {
			t.Fatalf("invalid arguments accepted: %v", tail)
		}
	}
}
