package main

import (
	"context"
	"github.com/netty-linux/daimon/internal/editcontract"
	"io"
)

type preimageReviewer struct {
	input   io.Reader
	display io.Writer
}

func (t *preimageReviewer) Review(ctx context.Context, r editcontract.Review) (editcontract.Decision, error) {
	if t == nil {
		return editcontract.Deny, editcontract.ErrApproval
	}
	return reviewDisclosure(ctx, r, t.input, t.display, "EXPORTAR PREIMAGE ")
}
