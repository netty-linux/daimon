package editcontract

import (
	"bufio"
	"context"
	"io"
	"strings"
)

// Terminal is an injected, synchronous reviewer, independent of CLI and loop.
// Nothing reads os.Stdin. A canceled blocked read may remain until input closes;
// discard this Terminal after cancellation. No subsequent prompt may reuse it.
type Terminal struct {
	input    *bufio.Reader
	output   io.Writer
	canceled bool
}

func NewTerminal(input io.Reader, output io.Writer) *Terminal {
	if input == nil || output == nil {
		return nil
	}
	return &Terminal{input: bufio.NewReader(input), output: output}
}
func (t *Terminal) Review(ctx context.Context, r Review) (Decision, error) {
	if t == nil || t.canceled || r.ID == "" || r.Display == "" {
		return Deny, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Deny, err
	}
	// The complete proposal must be written successfully before accepting input.
	for _, text := range []string{r.Display, "Approve this proposal once? [y/N]: "} {
		n, err := io.WriteString(t.output, text)
		if err != nil {
			return Deny, ErrApproval
		}
		if n != len(text) {
			return Deny, ErrApproval
		}
		if err := ctx.Err(); err != nil {
			return Deny, err
		}
	}
	type answer struct {
		text string
		err  error
	}
	done := make(chan answer, 1)
	go func() { line, err := t.input.ReadSlice('\n'); done <- answer{string(line), err} }()
	select {
	case <-ctx.Done():
		t.canceled = true
		return Deny, ctx.Err()
	case result := <-done:
		if err := ctx.Err(); err != nil {
			t.canceled = true
			return Deny, err
		}
		if result.err != nil {
			t.canceled = true
			return Deny, nil
		}
		switch strings.TrimSpace(result.text) {
		case "y", "Y", "yes", "Yes", "YES":
			return Allow, nil
		default:
			return Deny, nil
		}
	}
}
