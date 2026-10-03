package main

import (
	"context"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/policy"
)

const planInstruction = `Modo de planejamento read-only. Use somente echo, list_dir e read_file; leituras exigem aprovação humana. Não execute alterações nem solicite ferramentas de escrita. Produza como resposta final um plano humano legível contendo: resumo do diagnóstico; objetivo de mudança; arquivos que provavelmente precisariam mudar; alteração pretendida por arquivo, sem executar; riscos e suposições; plano de validação; bloqueios ou informações faltantes. O plano não concede permissão de escrita e nunca será executado automaticamente.`

// Plan uses the same deliberate, informed reading approval as workspace.
// The original request remains intact for authorization and execution.
type planApproval struct{ terminal *policy.TerminalApproval }

func (p *planApproval) Approve(ctx context.Context, request agentloop.ToolAuthorizationRequest) (bool, error) {
	return p.terminal.Approve(ctx, request)
}
