package policy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"github.com/netty-linux/daimon/internal/agentloop"
)

const maxValueRunes = 120

// TerminalApproval asks the human operator once per call. Prompt text goes to
// out; answers come from a single buffered reader over in, so queued input
// survives between consecutive approvals. The default answer is No: EOF,
// read errors and any non-affirmative line deny without failing the run.
// Cancellation interrupts the pending prompt and returns the context error.
type TerminalApproval struct {
	in  *bufio.Reader
	out io.Writer
}

func NewTerminalApproval(in io.Reader, out io.Writer) *TerminalApproval {
	return &TerminalApproval{in: bufio.NewReader(in), out: out}
}

func (t *TerminalApproval) Approve(ctx context.Context, request agentloop.ToolAuthorizationRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// A prompt that cannot be displayed fails closed: the error returns
	// before any answer is read or accepted.
	if err := t.print(request); err != nil {
		return false, err
	}
	line, err := t.readLine(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		// EOF and read errors deny; they are not authorization failures.
		return false, nil
	}
	switch strings.TrimSpace(line) {
	case "y", "Y", "yes", "YES", "Yes":
		return true, nil
	default:
		return false, nil
	}
}

func (t *TerminalApproval) print(request agentloop.ToolAuthorizationRequest) error {
	if _, err := fmt.Fprintf(t.out, "DAIMON solicita:\n\n  Ferramenta: %s\n", sanitize(request.Call.Name, maxValueRunes)); err != nil {
		return err
	}
	for _, field := range argumentFields(request.Call.Arguments) {
		if _, err := fmt.Fprintf(t.out, "  %s\n", field); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(t.out, "\nPermitir uma vez? [y/N]: ")
	return err
}

// readLine races the buffered read against cancellation. The abandoned
// goroutine keeps the reader busy until the run ends; the CLI is synchronous
// and single-shot, so one bounded goroutine per canceled approval is safe.
func (t *TerminalApproval) readLine(ctx context.Context) (string, error) {
	type line struct {
		text string
		err  error
	}
	done := make(chan line, 1)
	go func() {
		text, err := t.in.ReadString('\n')
		done <- line{text, err}
	}()
	select {
	case <-ctx.Done():
		return "", context.Cause(ctx)
	case result := <-done:
		return result.text, result.err
	}
}

// argumentFields renders one display line per argument, deterministically
// ordered, with control and formatting characters replaced so nothing can
// shape the terminal. File contents are never shown: only what the model asked for.
func argumentFields(arguments json.RawMessage) []string {
	var fields map[string]any
	if err := json.Unmarshal(arguments, &fields); err != nil || fields == nil {
		return []string{"  Argumentos: " + sanitize(string(arguments), maxValueRunes)}
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		value := fields[name]
		text, ok := value.(string)
		if !ok {
			compact, err := json.Marshal(value)
			if err != nil {
				text = fmt.Sprint(value)
			} else {
				text = string(compact)
			}
		}
		lines = append(lines, fmt.Sprintf("  %s: %s", label(name), sanitize(text, maxValueRunes)))
	}
	return lines
}

func label(name string) string {
	switch name {
	case "path":
		return "Caminho"
	case "text":
		return "Texto"
	default:
		return sanitize(name, maxValueRunes)
	}
}

// sanitize replaces control and formatting characters, including ANSI
// escape introducers and bidirectional overrides, with '?' and caps length.
func sanitize(text string, maxRunes int) string {
	var builder strings.Builder
	runes := 0
	for _, r := range text {
		if runes >= maxRunes {
			builder.WriteRune('…')
			return builder.String()
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			r = '?'
		}
		builder.WriteRune(r)
		runes++
	}
	return builder.String()
}
