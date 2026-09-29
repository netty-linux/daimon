package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDemo(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"demo"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Resposta final: DAIMON", "Passos do modelo: 2", "tool_completed", "final_answer", "loop_stopped"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, &out)
		}
	}
}
func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"demo", "extra"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatal("expected usage error")
		}
	}
}
