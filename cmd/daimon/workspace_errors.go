package main

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/createcontract"
	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/policy"
	"github.com/netty-linux/daimon/internal/providers/openai"
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
		} else if providerMessage := workspaceProviderError(err); providerMessage != "" {
			message = providerMessage
		}
	}
	return &outputError{message: message, cause: err}
}

func workspaceProviderError(err error) string {
	switch openai.Classify(err) {
	case openai.FailureMissingConfiguration:
		return "Configuração do provider ausente."
	case openai.FailureConfiguration:
		return "Configuração do provider inválida."
	case openai.FailureURL:
		return "Endpoint do provider inválido."
	case openai.FailureTLS:
		return "Falha ao validar a conexão TLS com o provider."
	case openai.FailureTimeout:
		return "Tempo de espera do provider excedido."
	case openai.FailureTransport:
		return "Falha de transporte na conexão com o provider."
	case openai.FailureAuthentication:
		return "Provider recusou a autenticação ou o acesso (HTTP 401/403)."
	case openai.FailureNotFound:
		return "Endpoint ou modelo não encontrado pelo provider (HTTP 404)."
	case openai.FailureRateLimit:
		return "Limite de requisições do provider atingido (HTTP 429)."
	case openai.FailureServer:
		return "Falha no serviço do provider (HTTP 5xx)."
	case openai.FailureHTTP:
		return "Provider recusou a requisição HTTP."
	case openai.FailurePayload:
		return "Requisição ou resposta incompatível com o protocolo do provider."
	default:
		return ""
	}
}
