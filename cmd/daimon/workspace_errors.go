package main

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/createcontract"
	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/policy"
)

// Only typed causes select a public explanation. Cleanup takes precedence:
// even cancellation may leave a residue when cleanup fails.
func workspaceError(err error) error {
	if err == nil {
		return nil
	}
	message := "Não foi possível concluir a operação; consulte os contadores de escritas confirmadas."
	switch {
	case errors.Is(err, errWorkspace):
		message = "Argumentos ou diretório de workspace inválidos. Uso de plan: workspace --root \"diretório\" plan [--validate-scope] \"pedido\" ou plan \"pedido\" --validate-scope. Forneça um único pedido não vazio; a única flag de plan é --validate-scope, no máximo uma vez."
	case errors.Is(err, errPlanScope):
		message = "Plano recusado: o contrato de escopo não foi validado após a recuperação única."
	case errors.Is(err, createcontract.ErrCleanup), errors.Is(err, editcontract.ErrCleanup):
		message = "Falha de limpeza; pode haver resíduo. Consulte as escritas confirmadas."
	case errors.Is(err, context.Canceled):
		message = "Operação cancelada; consulte as escritas confirmadas."
	case errors.Is(err, context.DeadlineExceeded):
		message = "Prazo da operação excedido; consulte as escritas confirmadas."
	case errors.Is(err, createcontract.ErrConflict):
		message = "O alvo já existe; criação recusada."
	case errors.Is(err, createcontract.ErrChanged), errors.Is(err, editcontract.ErrChanged):
		message = "O alvo mudou desde a preparação; aprovação invalidada."
	case errors.Is(err, createcontract.ErrLimit), errors.Is(err, editcontract.ErrLimit):
		message = "Limite da proposta excedido; aprovação impedida."
	case errors.Is(err, policy.ErrDisplay), errors.Is(err, createcontract.ErrApproval), errors.Is(err, editcontract.ErrApproval):
		message = "Não foi possível apresentar ou concluir a aprovação integral; operação não autorizada."
	case errors.Is(err, createcontract.ErrDenied), errors.Is(err, editcontract.ErrDenied):
		message = "Aprovação não concedida."
	case errors.Is(err, createcontract.ErrWrite), errors.Is(err, editcontract.ErrStage), errors.Is(err, editcontract.ErrCommit):
		message = "Falha na operação de arquivo; consulte as escritas confirmadas."
	default:
		var auth *agentloop.AuthorizationError
		if errors.As(err, &auth) {
			message = "Não foi possível autorizar a operação."
		}
	}
	return &outputError{message: message, cause: err}
}
