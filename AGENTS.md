# DAIMON

Visão: fundação experimental de um Sovereign Personal Agent sob controle do usuário.
Escopo atual: Reliable Agent Loop + Execution Budget + OpenAI-compatible
non-streaming + Human Approval + Workspace Read-Only, em Go, com Scripted,
echo, read_file e list_dir.

## Arquitetura

- Biblioteca padrão, interfaces pequenas e fluxo explícito, síncrono.
- Preserve Model.Generate, Tool.Execute, Registry e EventSink tipado.
- Não adicione emissor global nem entregue Registry/implementações ao modelo.
- Copie requests e chamadas para proteger o histórico de mutações externas.
- Ferramentas sequenciais, sem retries ou concorrência.
- Configuração explícita: Loop.Budget; DefaultBudget somente quando solicitado pelo chamador.
- Preserve errors.Is para ErrInvalidConfig, ErrInvalidResponse, ErrMaxSteps e contextos;
  ModelError deve preservar passo e causa. LimitError deve distinguir Kind.

## Invariantes do budget

- Todos os campos são positivos. Valide antes de chamar o modelo; zero nunca é ilimitado.
- Uma chamada ao modelo = um passo; nunca ultrapasse MaxSteps.
- Pré-valide o lote inteiro antes de efeitos: IDs/nomes não brancos, IDs únicos em toda
  execução, quantidade por passo/total, bytes por argumento e capacidade de histórico.
- Conte Content, ToolCallID, IDs, nomes e json.RawMessage por bytes, não tokens ou runes.
- Reserve assistant e todos os recibos: cada recibo inclui len(ID) + MaxToolResultBytes.
  Aritmética não pode aceitar um lote por overflow.
- Verifique capacidade para a mensagem inicial e para a resposta final antes de adicioná-las.
- Rejeite texto com calls, resposta final branca, acima do limite ou que não caiba integralmente.
- Normalize resultados para UTF-8 e limite em fronteira válida. Inclua marcador completo:
  original_bytes quando couber, [truncated] ou ~ para limites mínimos. Conte toda alteração.
- ToolCalls conta tentativas iniciadas, incluindo falhas controladas/interrupções.
  Lotes rejeitados e chamadas não iniciadas não contam.
- Cancelamento pode deixar calls sem recibo; preserve histórico aceito e pare novos efeitos.
- Use timeouts cooperativos com causa identificável, respeitando deadlines externos.
  Verifique antes/depois das chamadas; sucesso após expiração não é sucesso do loop.
- StopReason deve existir em todo retorno e no único loop_stopped correspondente.
- Evento não contém mensagens, nomes/IDs do modelo, argumentos, resultados ou texto de erro.

## Invariantes de autorização

- Loop.Authorizer é obrigatório; nil é ErrInvalidConfig antes da primeira chamada ao modelo.
- Autorização é fail-closed: nenhuma chamada executa sem decisão Allow explícita.
- O lote inteiro é autorizado antes do primeiro Execute, em passagem única por chamada,
  sem execução nem efeitos; o lote rejeitado pelo budget nunca chega ao Authorize.
  Eventos de aprovação podem ser emitidos durante essa passagem; nenhum efeito de
  ferramenta ocorre antes de todas as decisões.
- ToolPolicy e ApprovalProvider são conceitos fora do loop: um authorizer composto
  implementa ToolAuthorizer e resolve política e aprovação antes de responder.
  O loop não conhece terminal, política ou motivos.
- Política estática nega por padrão; fallback não configurado nega (zero value).
  Decisão de política inválida falha fechado, sem consultar aprovação.
- ApprovalProvider: uma aprovação vale para uma única call; não persiste decisão,
  não oferece "sempre permitir". Padrão é No; EOF e entrada inválida negam sem
  falhar o run. Cancelamento interrompe a aprovação e retorna o erro de contexto.
- Argumentos exibidos são sanitizados: controles e formatação Unicode não chegam
  ao terminal; conteúdo de arquivo nunca é exibido. Prompts vão ao stderr injetado;
  nada lê os.Stdin dentro da política.
- Authorize recebe runCtx e continua governado por MaxRunDuration e deadlines externos.
- ToolAuthorizationRequest recebe os argumentos copiados defensivamente.
- Somente allow e deny são decisões válidas; qualquer outro valor falha fechado.
- Erros do authorizer impedem todo Execute daquele lote; erros de contexto voltam
  diretamente; erros não de contexto viram AuthorizationError, cuja mensagem só
  nomeia passo e ferramenta e preserva a causa por Unwrap.
- Permitido direto: ToolAllowed → ToolRequested → ToolCompleted/ToolFailed.
- Aprovado: ApprovalRequested → ApprovalGranted → ToolAllowed → ToolRequested →
  ToolCompleted.
- Negado: (ApprovalRequested → ApprovalDenied quando passa por aprovação) →
  ToolDenied → recibo controlado correlato, sem execução.
- Desconhecido ou JSON inválido: ToolRequested → ToolFailed, sem evento de autorização.
- Eventos nunca contêm nomes, IDs, argumentos, motivos de decisão ou resultados.
- A política padrão do CLI permite echo, exige aprovação para list_dir e read_file
  e nega qualquer outra ferramenta; não existe opção global de aprovar tudo.
- Não adicionar aprovação permanente, wildcards de permissão, configuração de
  política em arquivo ou novas ferramentas neste corte.

## Segurança e escopo

- internal/editcontract prepara propostas imutáveis e aprovação de uso único
  somente leitura, sem executor/Tool/integração ao loop. Vincule caminho, versão
  original, bytes propostos e limites; preview ASCII reversível completo obrigatório.
  Rejeite symlinks observados em qualquer componente; revalide antes/depois da
  decisão e no consumo. Falha/cancelamento gasta a tentativa/capacidade, sem retry.
  Inspeções não provam exclusão de corridas, ABA, hard links ou atomicidade de escrita.

- Ferramentas desconhecidas, JSON inválido e erros normais viram resultados controlados.
- Não usar panic, log.Fatal ou os.Exit no loop.
- read_file e list_dir permanecem confinados com os.Root, somente caminhos
  relativos, limites de bytes, validação estrita e bloqueio de traversal/symlink
  externo/diretórios; list_dir é não recursivo, ordenado, rotula symlink sem
  seguir e falha integralmente ao exceder limites, sem corte silencioso.
- Testes de symlink devem executar; falta de permissão é falha explícita.
- Não adicionar prematuramente banco, memória longa/vetorial, gateway, múltiplos providers,
  subagentes, MCP, servidor HTTP, Telegram/Discord, TUI/web, event sourcing completo,
  shell, escrita/edição de arquivos, streaming ou abstrações especulativas.
- Não anunciar garantias de sandbox ou limite rígido de memória: o budget limita dados aceitos.

## Provider HTTP

- Providers implementam model.Model sem modificar os contratos do loop.
- HTTP e structs privadas do protocolo pertencem a internal/providers; agentloop e model
  não importam providers. Somente cmd/daimon escolhe Scripted ou o adapter.
- Secrets entram pela configuração explícita. Somente cmd lê DAIMON_BASE_URL,
  DAIMON_MODEL, DAIMON_GROQ_MODEL e DAIMON_API_KEY; nunca imprimir/gravar chave ou aceitá-la em argumento CLI.
- Smokes reais opt-in usam Groq por padrão via cmd/daimon smoke. internal/providers/groq
  configura o adapter OpenAI-compatible existente; não duplica transporte/protocolo.
  Testes automáticos e CI continuam offline, sem credenciais; não há fallback.
- Provider traduz mensagens e descrições; não executa ferramentas, não faz retry,
  não implementa streaming/SSE, Responses API, fallback ou SDK externo.
- Preserve argumentos como string/raw bytes, inclusive JSON inválido, para recuperação
  pelo loop. Valide presença/tipos do envelope e schema JSON antes de enviar.
- HTTPS remoto; HTTP somente localhost, 127.0.0.0/8 e ::1. Sem userinfo/query/fragment.
- Cliente padrão recusa redirects. Cliente injetado tem política do chamador;
  não configure timeout próprio que concorra com o Budget nem substitua o contexto recebido.
- Limite body antes de decodificar, incluindo bodies de erro, e feche-o em todos os caminhos.
- Error() não inclui chave, URL, corpo ou mensagem livre do servidor. Preserve causa de
  transporte por Unwrap e identidade de erros de contexto. Metadados precisam ser limitados.
- Demo permanece offline. Testes de provider/chat usam httptest.Server; nenhum teste toca
  a internet e o CI não usa credenciais ou provedores externos.

## Processo e validação

Antes de modificar: inspecione o estado, resuma o comportamento, liste arquivos,
semântica e invariantes afetadas. Preserve os testes da versão anterior, migrando
apenas a configuração necessária. Cubra limites e falhas com testes sem rede.

Execute em Linux, incluindo os testes de symlink:
```sh
gofmt -w .
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go run ./cmd/daimon demo
```
gofmt -l não pode listar arquivos. CI executa formatação, vet, testes e race detector
a cada push e pull request. Não substitua execução real por comandos simulados.
