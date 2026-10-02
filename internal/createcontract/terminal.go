package createcontract

import (
	"context"
	"github.com/netty-linux/daimon/internal/editcontract"
	"io"
)

// Reuse only the complete-display, explicit one-shot terminal interaction.
// Creation proposals and permits remain distinct from replacement contracts.
type Terminal struct{ terminal *editcontract.Terminal }

func NewTerminal(input io.Reader, output io.Writer) *Terminal {
	return &Terminal{terminal: editcontract.NewTerminal(input, output)}
}
func (t *Terminal) Review(ctx context.Context, r Review) (Decision, error) {
	if t == nil || t.terminal == nil || !r.TargetAbsent {
		return Deny, ErrInvalid
	}
	d, err := t.terminal.Review(ctx, editcontract.Review{ID: r.ID, Display: r.Display})
	return d, err
}
