package main

import (
	"bufio"
	"context"
	"io"
	"strings"

	"github.com/netty-linux/daimon/internal/editcontract"
)

// Deliberate disclosure approval, separate from generic y/N write approval.
// One reviewer is used once; canceled reads are never reused.
type outputReviewer struct {
	input   io.Reader
	display io.Writer
}

func (t *outputReviewer) Review(ctx context.Context, r editcontract.Review) (editcontract.Decision, error) {
	if t == nil || t.input == nil || t.display == nil || r.ID == "" || r.Display == "" {
		return editcontract.Deny, editcontract.ErrApproval
	}
	if err := ctx.Err(); err != nil {
		return editcontract.Deny, err
	}
	phrase := "EXPORTAR " + r.ID
	for _, s := range []string{r.Display, "Para divulgar este conteúdo uma vez, digite exatamente " + phrase + " (qualquer outra resposta nega): "} {
		n, err := io.WriteString(t.display, s)
		if err != nil || n != len(s) {
			return editcontract.Deny, editcontract.ErrApproval
		}
		if err := ctx.Err(); err != nil {
			return editcontract.Deny, err
		}
	}
	type answer struct {
		s   string
		err error
	}
	done := make(chan answer, 1)
	go func() {
		line, err := bufio.NewReaderSize(t.input, 4096).ReadSlice('\n')
		done <- answer{string(line), err}
	}()
	select {
	case <-ctx.Done():
		return editcontract.Deny, ctx.Err()
	case a := <-done:
		if err := ctx.Err(); err != nil {
			return editcontract.Deny, err
		}
		if a.err != nil || strings.TrimSuffix(strings.TrimSuffix(a.s, "\n"), "\r") != phrase {
			return editcontract.Deny, nil
		}
		return editcontract.Allow, nil
	}
}
