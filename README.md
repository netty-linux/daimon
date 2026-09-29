# DAIMON

DAIMON é a fundação experimental de um **Sovereign Personal Agent** em Go:
execução sob controle do usuário e contratos independentes de provedor.
**Ainda não é um agente pessoal pronto.**

O corte atual reúne Reliable Agent Loop e Execution Budget:
mensagem → modelo → ferramentas opcionais → resultados → modelo → resposta final.
Usa somente a biblioteca padrão, um modelo programável e as ferramentas
`echo` e `read_file`. Não requer credenciais nem APIs externas.

## Execução e validação

Requisito: Go 1.27.1 ou posterior. Na raiz do projeto:

```sh
go run ./cmd/daimon demo
gofmt -w .
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
```

`gofmt -l .` não deve listar arquivos. O demo recebe “repita DAIMON”, executa
`echo({"text":"DAIMON"})` e retorna `DAIMON` em dois passos, com nove eventos.
Registra também `read_file`, limitado a 64 KiB e ao diretório atual.
A CLI aceita apenas `demo`; argumentos inválidos e falhas retornam código não zero.

No ambiente Windows inicial, o SDK foi instalado em
`%LOCALAPPDATA%\DAIMON\toolchains\go1.27.1\go`:

```powershell
$env:PATH = "$env:LOCALAPPDATA\DAIMON\toolchains\go1.27.1\go\bin;$env:PATH"
go run ./cmd/daimon demo
```

Os testes de symlink precisam de permissão para criá-los. No Windows, sem modo de
desenvolvedor ou privilégio apropriado, falham explicitamente. Para executar a
suíte inteira em Linux com Docker Desktop (download inicial requer rede):

```powershell
docker pull golang:1.27.1
docker run --rm --network none --mount "type=bind,source=$((Get-Location).Path),target=/workspace,readonly" -w /workspace golang:1.27.1 bash -c 'go vet ./... && go test -count=1 ./... && go test -race -count=1 ./... && go run ./cmd/daimon demo'
```

## Arquitetura

- `cmd/daimon`: monta Registry, modelo programável, Budget e sink; imprime o demo.
- `internal/model`: mensagens, descrições de ferramentas, `Model.Generate` e `Scripted`.
- `internal/tools`: `Tool.Execute`, Registry determinístico, echo e read_file.
- `internal/agentloop`: loop síncrono, Budget, limites de histórico, erros e eventos tipados.

O modelo recebe `ModelRequest` com cópias do histórico e descrições de ferramentas;
não recebe Registry nem implementações. `ToolCall.Arguments` é `json.RawMessage`.
O Registry preserva a ordem de registro e rejeita duplicatas.
Configure os componentes antes de executar; não há suporte a uso concorrente.

O loop recebe explicitamente `Budget: agentloop.DefaultBudget()`.
A configuração anterior `Loop.MaxSteps` foi substituída por `Loop.Budget.MaxSteps`.
Não há default implícito para um budget vazio.

## Execution Budget

| Campo | Padrão |
|---|---|
| MaxSteps | 8 chamadas ao modelo |
| MaxToolCallsPerStep | 4 |
| MaxTotalToolCalls | 16 |
| MaxUserMessageBytes | 32 KiB |
| MaxFinalAnswerBytes | 256 KiB |
| MaxToolArgumentBytes | 64 KiB por chamada |
| MaxToolResultBytes | 64 KiB por resultado |
| MaxHistoryMessages | 128 |
| MaxHistoryBytes | 2 MiB |
| MaxRunDuration | 5 minutos |
| MaxModelCallDuration | 2 minutos |
| MaxToolCallDuration | 30 segundos |

Todos os campos precisam ser positivos. Zero e valores negativos são inválidos.
Budget inválido falha antes da primeira chamada ao modelo.

Uma chamada ao modelo é um passo, inclusive em falha. Chamadas de ferramenta são
sequenciais e pertencem ao passo que as solicitou. `Result.ToolCalls` conta
tentativas iniciadas, incluindo ferramenta desconhecida, argumentos inválidos,
erros controlados e chamadas interrompidas. Lotes rejeitados não consomem chamadas.

IDs e nomes precisam ser não vazios/não brancos. IDs são únicos durante toda a
execução. O lote inteiro é validado antes da primeira ferramenta: quantidade por
passo, total, argumentos individuais e capacidade de histórico. Falha em qualquer
limite impede todas as ferramentas daquele lote.

O histórico conta bytes usando `len`: Content, ToolCallID e IDs, nomes e argumentos
das calls. Conta bytes do protocolo, não memória da struct, JSON serializado ou tokens.
Antes do lote, reserva-se a mensagem assistant e um recibo por call contendo
`len(ID) + MaxToolResultBytes`. A reserva é conservadora mesmo para ferramentas que
normalmente retornam pouco. A aritmética de capacidade evita overflow; valores de
diagnóstico saturam no maior int da plataforma.

A mensagem inicial também precisa caber no histórico. Uma resposta final deve ser
não branca, sem calls, respeitar seu limite e caber integralmente no histórico.
Texto junto de calls é inválido. Respostas finais grandes falham sem truncamento
e sem serem adicionadas ao histórico.

Resultados de ferramentas, inclusive erros controlados, são normalizados para UTF-8
válido e limitados em bytes. Toda alteração recebe um marcador:
`\n[truncated: original_bytes=N]`; se não couber, `[truncated]`; para limites menores
que esse marcador, `~`. O marcador está incluído no limite. Original_bytes mede a
entrada antes da normalização. `TruncatedToolResults` conta resultados modificados,
inclusive normalização de bytes inválidos abaixo do limite.

## Cancelamento, erros e encerramento

Timeouts usam `context.WithTimeoutCause`, mantendo deadlines anteriores do pai.
O contexto é verificado antes e depois de cada chamada, inclusive se o componente
retornar sucesso depois de expirar. Não há goroutine para interromper chamadas.

O cancelamento/deadline externo já observado tem precedência. Caso contrário,
a causa herdada identifica timeout do budget total, do modelo ou da ferramenta.
Timeouts do budget retornam `LimitError` e preservam
`errors.Is(err, context.DeadlineExceeded)`. Erros de contexto retornados diretamente
por ferramentas também encerram: cancelamento gera `canceled`; deadline próprio da
ferramenta gera `tool_timeout`, mesmo com contexto recebido ainda ativo.

Falhas normais do modelo retornam `*ModelError`, preservando passo e causa.
Ferramenta desconhecida, JSON inválido e erro normal de ferramenta retornam uma
mensagem tool com `IsError`, permitindo recuperação pelo modelo.
`ErrInvalidConfig`, `ErrInvalidResponse` e `ErrMaxSteps` continuam verificáveis com
`errors.Is`. `LimitError` expõe Kind, Limit e Actual por `errors.As`.
`errors.Is(err, LimitError{Kind: ...})` distingue o limite; Kind vazio aceita qualquer
limite. Em timeout, Actual registra a duração limite alcançada.

Todo retorno define `Result.StopReason` e emite exatamente um `loop_stopped` com
a mesma razão, inclusive configuração inválida e mensagem inicial rejeitada:

- `completed`, `invalid_config`, `invalid_response`, `model_error`;
- `canceled`, `external_deadline`, `run_timeout`, `model_timeout`, `tool_timeout`;
- `max_steps`, `max_tool_calls`, `user_message_limit`, `argument_limit`,
  `final_answer_limit`, `history_limit`.

O resultado preserva o histórico já aceito, também em falhas. Interrupção durante
um lote pode deixar calls sem recibo; não se inventam resultados nem se executam
as chamadas restantes. A reserva garante espaço, não conclusão após cancelamento.

## Observabilidade e ferramentas

EventSink tem implementações Noop e Memory, sem emissor global. O demo emite:

```text
loop_started → model_requested → model_responded → tool_requested
→ tool_completed → model_requested → model_responded → final_answer → loop_stopped
```

Erros controlados usam `tool_failed`. Interrupção pode deixar requested sem conclusão.
Eventos contêm somente tipo, passo, posição de ferramenta e razão de encerramento;
não incluem mensagens, nomes/IDs fornecidos pelo modelo, argumentos, outputs ou erros.
O sink deve ser local, rápido e aceitar encerramento com contexto cancelado.
O histórico e os requests, ao contrário, contêm dados potencialmente sensíveis.

`echo` exige um objeto com text string. `read_file` exige path relativo e usa
`os.Root` para confinar a resolução de symlinks ao workspace; symlinks internos são
permitidos. Bloqueia caminhos absolutos, traversal, diretórios, arquivos não regulares
e arquivos acima do limite. Ambos rejeitam campos desconhecidos/duplicados,
campos obrigatórios ausentes, null e tipos inválidos. O proprietário de ReadFile
deve chamar Close.

## CI e testes

`.github/workflows/ci.yml` roda em Ubuntu 24.04 a cada push e pull request, usando
o Go de go.mod. Verifica formatação (falha se houver arquivos listados), vet,
testes e race detector, sem cache de resultados dos testes. Tem timeout de
15 minutos, permissões de leitura e Actions fixadas por SHA.
O check `Go / Linux` pode ser exigido nas regras de proteção do repositório.

Os testes preservam os cenários da v0.1 e acrescentam budget inválido, limites
exatos/excedidos, pré-validação de lotes, reserva com IDs, overflow, UTF-8, marcadores,
erros tipados, StopReason e timeouts de execução/modelo/ferramenta. Testes de deadline
aguardam ctx.Done; não dependem de corrida contra sleeps arbitrários. Não usam rede.

## Limites e próximo passo

Esta fundação não oferece sandbox de processo, limite rígido de alocação de memória,
tokenização, sumarização, persistência ou provider real. Um componente pode alocar
uma resposta grande antes de devolvê-la; o budget limita o que o loop aceita e armazena.
Cancelamento é cooperativo e não interrompe um componente que ignora o contexto.
Valores padrão são pontos de partida experimentais, não garantias de produção.

os.Root não impede hard links nem transforma um workspace hostil em sandbox.
Implementações fornecidas devem respeitar os contratos; não há recuperação de panics
de código arbitrário nem suporte a interfaces contendo ponteiros nil tipados.

Não existem banco, memória vetorial, gateway, subagentes, MCP, servidor, UI,
streaming, shell ou escrita/edição de arquivos. Nenhuma API é necessária para validar
o loop. Próximo corte possível, após revisão: um único provider compatível com OpenAI,
respeitando o budget. Não implementado nesta entrega.
