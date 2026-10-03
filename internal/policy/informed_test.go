package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/tools"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestInformedReadingApproval(t *testing.T) {
	for _, tool := range []string{"read_file", "list_dir"} {
		for _, path := range []string{"dir/file with spaces", "a\"b\\c", "á雪\u202e\x1b\n\t", strings.Repeat("x", 500)} {
			raw, _ := json.Marshal(map[string]string{"path": path})
			original := string(raw)
			var out bytes.Buffer
			allowed, err := NewTerminalApproval(strings.NewReader("y\n"), &out).Approve(context.Background(), requestFor(tool, original))
			if err != nil || !allowed {
				t.Fatalf("allow=%v err=%v", allowed, err)
			}
			if !strings.Contains(out.String(), "Caminho relativo: "+strconv.QuoteToASCII(path)) || !strings.Contains(out.String(), "envia o resultado ao provider") {
				t.Fatal(out.String())
			}
			for _, control := range []rune{'\x1b', '\u202e', '\t'} {
				if strings.ContainsRune(out.String(), control) {
					t.Fatal("terminal control")
				}
			}
			if string(raw) != original {
				t.Fatal("mutated arguments")
			}
		}
	}
}

type readingProbe struct{ calls int }

func (*readingProbe) Name() string                 { return "read_file" }
func (*readingProbe) Description() string          { return "probe" }
func (*readingProbe) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (p *readingProbe) Execute(context.Context, json.RawMessage) (tools.ToolResult, error) {
	p.calls++
	return tools.ToolResult{Content: "CONTENT_SECRET"}, nil
}

func TestReadingFailedDisplayHasNoEffectsOrEventData(t *testing.T) {
	probe := &readingProbe{}
	registry := &tools.Registry{}
	if err := registry.Register(probe); err != nil {
		t.Fatal(err)
	}
	sink := &agentloop.MemoryEventSink{}
	writerCause := errors.New("WRITER_SECRET")
	auth := &Authorizer{Policy: DefaultCLIPolicy(), Approvals: NewTerminalApproval(noAnswer{t}, failingWriter{writerCause}), Sink: sink}
	script := model.NewScripted(model.ScriptStep{Response: model.ModelResponse{ToolCalls: []model.ToolCall{{ID: "ID_SECRET", Name: "read_file", Arguments: json.RawMessage(`{"path":"PATH_SECRET"}`)}}}})
	result, err := (agentloop.Loop{Model: script, Registry: registry, Budget: agentloop.DefaultBudget(), Authorizer: auth, Sink: sink}).Run(context.Background(), "start")
	if probe.calls != 0 || !errors.Is(err, ErrDisplay) || !errors.Is(err, writerCause) || result.StopReason != agentloop.StopReasonAuthorizationError {
		t.Fatal(result, err, probe.calls)
	}
	data, _ := json.Marshal(sink.Events())
	for _, secret := range []string{"PATH_SECRET", "ID_SECRET", "CONTENT_SECRET", "WRITER_SECRET", "read_file"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal(string(data))
		}
	}
}

type shortDisplay struct{}

func (shortDisplay) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestReadingDisplayBoundaryAndFailure(t *testing.T) {
	raw := func(n int) string {
		b, _ := json.Marshal(map[string]string{"path": strings.Repeat("x", n)})
		return string(b)
	}
	var sample bytes.Buffer
	_, err := NewTerminalApproval(strings.NewReader("n\n"), &sample).Approve(context.Background(), requestFor("read_file", raw(0)))
	if err != nil {
		t.Fatal(err)
	}
	boundary := maxReadDisplayBytes - sample.Len()
	for _, n := range []int{boundary, boundary + 1} {
		var out bytes.Buffer
		allowed, err := NewTerminalApproval(strings.NewReader("y\n"), &out).Approve(context.Background(), requestFor("read_file", raw(n)))
		if n == boundary {
			if !allowed || err != nil || out.Len() != maxReadDisplayBytes {
				t.Fatal(allowed, err, out.Len())
			}
		} else if allowed || !errors.Is(err, ErrDisplay) || out.Len() != 0 {
			t.Fatal(allowed, err, out.Len())
		}
	}
	allowed, err := NewTerminalApproval(noAnswer{t}, shortDisplay{}).Approve(context.Background(), requestFor("list_dir", raw(1)))
	if allowed || !errors.Is(err, ErrDisplay) {
		t.Fatal(allowed, err)
	}
	for _, answer := range []string{"", "n\n", "invalid\n"} {
		allowed, err := NewTerminalApproval(strings.NewReader(answer), io.Discard).Approve(context.Background(), requestFor("read_file", raw(1)))
		if allowed || err != nil {
			t.Fatal(allowed, err)
		}
	}
}
