# CUA Persistent Bots — auditoria de integração

Status em 2026-10-09: agendador nativo diário implementado para revisão; associação
Bot/Space e cópia de Memory/Volume continuam pendentes, sem implementação funcional.
Branch: `feature/cua-persistent-bots`, a partir de `main` em `66481c0`.
Nenhum merge ou push para main faz parte deste trabalho.

## Referência inspecionada

Clone temporário externo de https://github.com/trycua/cua em
`5e638b2113f7105b93e0cccf4477e4080995f717`.
Foram lidos os READMEs de libs/cua, TypeScript, Python, cua-driver,
samples/cua-bots-macos e samples/openkoalabot-example-ts, o manifesto Spaces,
os tipos SDK, o schema VolumeService e os comandos CLI correspondentes.
Não foi copiada implementação do sample ou do runtime CUA.

## APIs confirmadas e superfícies distintas

| Nome solicitado | Confirmado | CLI nessa revisão |
| --- | --- | --- |
| `Spaces.create` | SDK, SpaceCreateOptions | `cua spaces create linux --on local --name bot-... --runtime gvisor` |
| `Space.agentStart` | SDK | harness externo; não é AgentLoop do DAIMON |
| `persistentAgentCreate` | bindings TS/Swift | `cua agent create NAME --harness HARNESS --in SPACE` |
| `persistentAgentSend` | bindings TS/Swift | `cua agent tell NAME TEXT` |
| `routineAdd` | bindings TS/Swift | `cua agent routine add NAME --title TITLE --prompt PROMPT --daily HH:MM` |
| `volumeRead` | bindings TS/Swift | `cua --json volume cat PATH` |
| `volumeWrite` | bindings TS/Swift | `cua --json volume put PATH - --if-etag ETAG` ou `--create-only` |
| `volumeLs` | bindings TS/Swift | `cua --json volume ls PATH` |
| `cua daemon` | CLI | gerenciamento próprio do daemon |
| `cua-driver mcp` | Driver CLI | transporte já integrado ao ComputerManager |

`SpaceCreateOptions.reuse` existe no SDK, mas `cua spaces create --reuse`
não existe no parser CLI dessa revisão. Reuso em um adaptador CLI precisa de
associação persistida e verificação de identidade, não de uma flag inventada.
Os nomes camelCase acima são APIs do SDK, não subcomandos CLI.

Fontes exatas no clone: `libs/cua/crates/cua-cli/src/{host,drive_cmd,persistent_cmd,lib}.rs`,
`libs/cua/crates/cua-sdk/src/native/{spaces,persistent}.rs`,
`libs/cua/typescript/src/native/cua_sdk.ts`,
`libs/cua/spaces-contract/manifest.json` e
`libs/cua/proto/cua/env/v1/volume.proto`.

## Incompatibilidade que exige decisão antes de implementação

As rotinas persistentes do daemon entregam turns a agentes persistentes CUA,
que executam harnesses externos. O contrato SDK `AgentRunHandle` em
`libs/cua/crates/cua-spaces/src/client/model.rs` documenta
`approvals_are_enforced: false` nos backends distribuídos.
O README do sample confirma que regras são instruções e que permissões do
harness são autoaprovadas. Não há, nessa superfície auditada, um callback que
encaminhe cada ToolCall desses harnesses ao authorizer do DAIMON.

Portanto, usar `routineAdd` porque a API existe conflita com manter
Model.Generate/AgentLoop, ToolPolicy e aprovação de uso único como autoridade.
Marcadores `[[ask]]`, `[[login]]` e `[[handoff]]` não corrigem essa fronteira.
A alternativa aprovada é um agendador DAIMON que somente chama SessionManager.Start,
com os mesmos budgets, capabilities e aprovações, e funciona apenas enquanto
o servidor está aberto. Essa alternativa requer alterar a condição do prompt
que exige usar rotinas CUA sempre que estiverem disponíveis. O usuário autorizou
explicitamente esse desvio em 2026-10-09: aprovação por chamada é requisito central,
portanto disponibilidade de routineAdd não autoriza seu uso. Também proibiu
Space.agentStart, persistentAgentCreate/Send e qualquer harness externo para turns.

## Arquitetura proposta para revisão

CUA permanece backend de computador e armazenamento externo; Bot continua
configuração, Thread conversa, Session execução efêmera e Memory contexto manual.
ComputerManager mantém autoridade exclusiva de input e o plano de mídia existente.
Nenhum segundo loop ou modelo/harness externo é introduzido implicitamente.

Um adaptador CLI deve receber executável absoluto, ambiente operacional e conexão
explícitos. Nenhum pacote fora de cmd lê o ambiente atual. Deadline obrigatório,
stdout/stderr limitados, erros tipados, stdin fechado, sem shell/retry/setup/login.
`cua-driver` deve continuar no transporte MCP existente, sem segundo cliente.
Windows aceita `cua.exe` e `cua-driver.exe`; isso não habilita escrita nativa,
Sandbox ou Cloud no Windows. Os perfis existentes permanecem Linux-first.

A CLI usa daemon descoberto ou fallback embedded quando a conexão não é explícita.
O adaptador não pode herdar esse fallback silencioso: a configuração deve fixar
o destino e falhar fechado. Tokens são privados de processo e nunca argv,
metadata, resposta HTTP, evento, log ou arquivo do DAIMON.

Space por Bot precisa de identidade estável derivada do Bot ID, não do nome
editável. Criação/reuso precisa de ownership e reserva por Bot, registro de
intenção antes do efeito e reconciliação de falhas parciais. Bot sem associação
mantém os contratos atuais. Remover Bot não pode implicitamente apagar Space.
Criar uma Space pode baixar imagens/runtime no CUA; não se deve anunciar criação
sob demanda como livre de downloads sem um preflight comprovado. Cloud continua
exigindo opt-in de custo e não pode ganhar autorização por rotina.

## Volume e memória

`volume cat --json` retorna conteúdo, tamanho, etag e versão; `volume put`
aceita precondição `--if-etag` ou `--create-only`. Conflito não admite retry
incondicional. O Volume remoto é uma cópia, nunca nova autoridade do MemoryStore.

Há uma decisão de formato/escopo: MEMORY.md é um documento, mas MemoryStore
contém registros com ID, kind, tags, timestamps e escopos global/Bot/Thread.
Um arquivo compartilhado por Bot não pode agregar memórias privadas de outras
Threads. É necessário definir exportação determinística, limites, seleção de
escopos e uma proposta de importação com identidade/revisão antes de sync-back.
Uma edição de Markdown não pode virar extração automática de memória.

Os Permits de create/replace existentes autorizam bytes de um arquivo Linux
vinculado a um root; não autorizam mutação de registros Memory nem de Volume.
Importação precisa de revisão integral específica e decisão consumível única,
sem reaproveitar um Permit de filesystem para outro efeito. Nenhuma aprovação
permanente, flag de escrita Windows ou importação automática foi habilitada.

VolumeService separa montagem do Volume da escrita no store remoto. O proto
documenta montagem Windows como preview desabilitado por padrão. Não ativar
`CUA_VOLUME_WINDOWS_PREVIEW` automaticamente nem inferir suporte validado.

## Agendador nativo implementado

`internal/routines` possui store JSON v1 explícito em `<data-dir>/routines.json`,
com 32 registros totais, 4 ativos/Bot, prompt até 32 KiB, título até 256 bytes,
arquivo até 2 MiB, horário diário HH:MM e timezone IANA obrigatório (tzdata da
biblioteca padrão embutido, inclusive para Windows). Sem cron genérico, shell,
novas ferramentas, fila de execução ou segundo loop. Intervalo de 15 minutos
entre tentativas do mesmo Bot; último horário é persistido por registro para
preservar o limite ao reiniciar. Exclusão explícita de registros não promete
retenção perpétua de um ledger de tentativas.

Cada disparo usa SessionManager.Start com ScheduledBotID e IDs aleatórios de
Session/mensagem, o budget configurado e os opt-ins de exposição de escrita do
processo. ToolPolicy, aprovação de uso único, Linux-only para escrita, transcript,
Memory e ComputerManager são os existentes. O agendador não chama `Space.agentStart`,
`routineAdd` ou persistent agents. Computer continua exclusivamente via ferramentas
MCP aprovadas do runtime existente; Volume nunca vira comando ou autoridade.
Cloud paga não é aceita para rotinas: ainda não existe consentimento de custo
persistente apropriado. A resolução revalida esse bloqueio no Bot congelado.

Admissão atômica no Manager reserva o Bot durante o turno agendado. Sessions
ativas do mesmo Bot, incluindo startup/approval/cleanup, bloqueiam a rotina;
durante rotina ativa, outro turn manual do mesmo Bot também é recusado. Reservas
de mutação de metadata sem identidade conhecida bloqueiam rotinas conservadoramente.
Isso não altera a possibilidade de turns manuais paralelos em Threads diferentes
quando nenhuma rotina daquele Bot está ativa. Não há filesystem I/O sob lock do
Manager. Negar uma ferramenta não aborta automaticamente o loop.

WaitingApproval mantém a rotina devida aguardando e visível; não abre outro turno,
não resolve, reenvia ou contorna a pendência. Uma vez admitido um turno, pausar ou
excluir a rotina altera só agenda futura, nunca a decisão humana/Session em curso.
Falha de admissão após consumir o slot é explícita e não faz retry. O consumo
persiste antes de Start: crash nesse intervalo pode perder um disparo, mas não
repeti-lo automaticamente. Não há promessa de exactly-once/ACID entre stores.

O servidor possui o scheduler, com tick de 15 segundos. Shutdown cancela/join
antes de fechar Sessions. Reinício pula slots vencidos: rotinas só rodam com o
servidor do DAIMON aberto, sem catch-up, restauração de Sessions ou approvals.
Falha do armazenamento encerra o scheduler e o servidor de modo controlado.
Um processo possui o diretório; JSON estrito, arquivo regular/parent imediato
sem symlink observado, temporário 0600 + Sync/Close/rename e cleanup explícito.
Sem sandbox de filesystem, exclusão de escritores externos, diretório fsync,
atomicidade Windows ou garantia de desligamento forçado de componentes não cooperativos.

API: GET/POST `/api/v1/routines`, PUT `{enabled:boolean}` e DELETE
`/api/v1/routines/{id}`. GET é metadata deliberada (inclui título e correlação
de Session, não prompt, instruções, conteúdo Memory ou secrets). Create valida
Bot/Thread e vínculo; remoção Bot/Thread é bloqueada por qualquer rotina vinculada,
sem cascata. Endpoint preserva Host/Origin same-origin e JSON bounded estrito.
UI PT-BR na aba Atividade oferece criação diária, status, pausar/ativar, exclusão
confirmada e Abrir execução para o fluxo normal de aprovação. Chat, Computador
e Arquivos mantêm layout/autoridade. Prompts não vão para storage do browser.

## Baseline executada antes de alterações

Linux real via Docker `golang:1.27.1`, rede desabilitada, checkout read-only:

| Comando | Resultado |
| --- | --- |
| `gofmt -l .` | vazio |
| `go vet ./...` | passou |
| `go test -count=1 ./...` | passou, incluindo symlinks Linux |
| `go test -race -count=1 ./...` | passou |
| `go run ./cmd/daimon demo` | completed; resposta DAIMON, 2 passos, 1 tool call |

Docker estava fechado na primeira tentativa; a tentativa retornou erro de conexão.
Após iniciar o Docker já instalado, a baseline acima executou integralmente.
O CI do commit base também concluiu com sucesso.
Essa baseline foi executada antes da implementação. Validação posterior é
registrada no relatório de implementação abaixo.

## Opt-ins e limites de validação

Nenhuma chamada CUA, Space real, Fleet pago, instalação, login ou download CUA
foi executado. Clonar fontes para leitura não verifica compatibilidade binária.
Windows real, montagem de Volume, gVisor real, Fleet, durabilidade remota e
encaminhamento de aprovações de harness externo não foram validados.
Não há flags ou comandos para associação de Spaces/Memory/Volume não implementadas.
Não usar exemplos de API desta auditoria como garantia de integração funcional.

Arquivos locais preexistentes preservados: `.zcodeignore` e `IDEA.md`.
Decisões pendentes: contrato de exportação/importação de Memory e preflight de
criação de Spaces sem downloads implícitos. O recorte atual implementa o próximo
passo autorizado (agendador), sem anunciar integração persistente CUA completa.

## Relatório da implementação nativa (2026-10-09)

Diff por pacote:

- `internal/routines`: store privado, schedule diário, slots/intervalo persistidos,
  driver do SessionManager, metadata sem prompt e testes offline/concorrrência.
- `internal/sessions`: ScheduledBotID interno, admissão atômica por Bot,
  consulta segura de status, bloqueio de cloud em turn agendado. Start comum
  preserva resolução assíncrona. Nenhuma alteração em agentloop/model/policy/tools.
- `internal/server`: CRUD de rotinas, validação JSON e referências para remoção.
- `cmd/daimon`: store explícito e ownership/stop/join do scheduler no serve.
- `ui`: painel em Atividade, PT-BR, recuperação da execução/approval existente,
  testes de controles/metadata/escaping e assets embutidos regenerados.
- `docs`, README e AGENTS: decisão aprovada, escopo, limites e evidência de execução.

Resultados finais, em Linux real Docker `golang:1.27.1` sem rede:

| Comando | Resultado |
| --- | --- |
| `gofmt -w .` | passou, sem alterações necessárias |
| `gofmt -l .` | vazio |
| `go vet ./...` | passou |
| `go test -count=1 ./...` | passou |
| `go test -race -count=1 ./...` | passou |
| `go run ./cmd/daimon demo` | completed, DAIMON, 2 passos, 1 tool call |
| `go build -o /tmp/daimon ./cmd/daimon` | passou |

Resultados UI em Windows com fixtures locais, sem CUA real/credenciais:

| Comando | Resultado |
| --- | --- |
| `npm test` | 72 testes em 15 arquivos passaram |
| `npm run typecheck` | passou (executado pelo build) |
| `npm run build` | passou, assets versionados atualizados |
| `DAIMON_SMOKE_CHANNEL=msedge node scripts/smoke.mjs` | passou, incluindo criar/pausar/reload/excluir rotina |
| `DAIMON_SMOKE_CHANNEL=msedge node scripts/approval-smoke.mjs` | passou, preview integral, allow/deny, reload, duas abas, abort/shutdown |
| `git diff --check` | passou |

Testes de regressão específicos incluem: rotina real com model fake local e
read_file exigindo decisão Allow/Deny, nenhuma nova Session durante pendência,
reserva do Bot contra outro turn, admissão de metadata, resolução assíncrona de
Start normal, limites 4/32, intervalo após restart, Tick concorrente, slots
consumidos em falha, cancelamento, JSON corrupto/versionado, symlinks reais Linux
e modo 0600. Teste AST impede imports de execução CUA/processo e os identificadores
agentStart/routineAdd/persistentAgentCreate/Send no código do agendador.

Retenção: o Manager mantém no máximo 128 Sessions no serve e descarta somente
terminais finalizadas, da mais antiga à mais recente, ao admitir um novo Start
no limite. Sem terminal elegível, retorna erro tipado de capacidade sem alterar
as Sessions existentes. IDs admitidos não são reutilizados; somente tombstones
de identidade sobrevivem ao descarte, sem dados/eventos/recursos da execução.
Não há retries, retomada durável, catch-up offline ou garantia exactly-once.
Na inicialização, uma rotina ativa vencida registra a última ocorrência perdida
com horário previsto e detecção, sem Session ou alteração de LastAttemptAt.
Retém-se somente a perda mais recente de cada rotina (até 32 registros), sem
contador de execução ou consumo de autorização; pausadas não geram perda.
O store grava versão 2; versão 1 continua legível e é convertida na próxima
gravação válida. Versões desconhecidas e metadados incompletos falham fechado.
Criar/pausar/excluir agenda não autoriza effects nem decide approvals. Horários
dependem do relógio/timezone do servidor. CUA real, cua-driver Windows real,
Space/Volume e Fleet permanecem não validados; nenhum smoke pago foi executado.


## Smoke CUA real no Windows — bloqueio de discovery

Preparação executada após as validações offline, no Windows, com o DAIMON em
`57bd5c22a1e1dbad1dee30a452cfe0daad9dc2c5` e CUA Driver oficial 0.34.0 x86_64.
O ZIP foi conferido contra SHA256SUMS oficial
(`F96CC1632BC88E6F268EAB745277C1FC302BB0F7D123E04373E35AFECAD6439F`);
os três executáveis possuem assinatura Authenticode válida de Cua AI, Inc.
O driver foi instalado fora do repositório, com telemetria desativada.

Foi iniciado um daemon CUA em modo standard, sem bypass de aprovação, e um
servidor DAIMON de smoke na porta 3001, com dados separados do servidor existente.
O mcp.json usa executável absoluto, args exatos ["mcp"], computer_backend
"cua-local" e classificação read para as quatro observações suportadas.
Health e API de rotinas respondem, mas Computer informa startup_failed e MCP
informa unavailable, sem capabilities disponíveis.

O diagnóstico executou somente initialize e tools/list; nenhuma tools/call.
O driver aceitou 2025-06-18, anunciou 59 ferramentas com nomes válidos e schemas
object de até 4476 bytes. O catálogo padrão inclui descrições acima do limite
existente de 1024 bytes de internal/mcp/tools.go: list_apps retorna 1478 bytes;
get_window_state, 3636 bytes; click, 2866 bytes. Discovery rejeita integralmente
o catálogo, inclusive por ferramentas que não foram selecionadas no Bot.
Nenhum limite, contrato, política ou código foi alterado para contornar a falha.

Não foram criados Bot, Thread ou rotina de smoke. Observação real, aprovação
pela UI, ordem approval/action, bloqueio de novo disparo por pendência e reinício
com slot vencido não foram executados. Portanto, o smoke end-to-end com CUA real
no Windows permanece **bloqueado e não validado**. A instalação e o handshake
real não equivalem à validação de uma ação aprovada. Space, Volume e Fleet
continuam não executados nesta rodada.

### Restrição oficial do catálogo encontrada — não aplicada

A versão 0.34.0 oferece a variável CUA_DRIVER_POLICY_FILE e política YAML com
allow.tools, deny.tools e allow.rules. A implementação determina a elegibilidade
para tools/list pela política; ferramentas negadas ou sem caminho de allow são
ocultadas. O teste tools_list_hides_policy_denied_tools_and_calls_stay_denied
confirma que uma ferramenta explicitamente negada não é anunciada e continua
negada na invocação. Exemplo de lista restrita, ainda não aplicado:

```yaml
allow:
  tools: [list_windows]
```

```powershell
$env:CUA_DRIVER_POLICY_FILE = 'C:\Users\01 Bigode\AppData\Local\DAIMON\smoke-cua-windows\policy.yaml'
& 'C:\Users\01 Bigode\AppData\Local\DAIMON\tools\cua-driver\0.34.0\cua-driver-rs-0.34.0-windows-x86_64\cua-driver.exe' serve
```

Esse arquivo não foi criado e o comando acima não foi executado. O daemon
existente não foi reconfigurado. Não foi encontrada uma flag direta de lista
no subcomando mcp. A ajuda também apresenta --capability-manifest e
--permission-mode bounded, mas essas opções são contratos de autorização;
não foram usadas nem demonstradas como solução deste bloqueio de discovery.

DAIMON não encaminha CUA_DRIVER_* ao subprocesso MCP e mcp.json não aceita env;
a configuração acima pertence ao lançamento manual do daemon CUA, não ao
mcp.json nem às permissões DAIMON. Uma política CUA não substitui ToolPolicy,
budget ou aprovação individual. O catálogo filtrado não foi verificado no
binário instalado neste Windows; sua compatibilidade permanece pendente.
O código oficial também informa que erro ao carregar política pode manter o
catálogo visível; não inferir filtragem bem-sucedida apenas da variável definida.

Fontes oficiais, fixadas na versão instalada:
- [policy.rs: variável e filtragem do catálogo](https://github.com/trycua/cua/blob/cua-driver-rs-v0.34.0/libs/cua-driver/rust/crates/cua-driver-core/src/policy.rs#L110).
- [policy_tools_list_test.rs: teste de ferramentas ocultas e invocação negada](https://github.com/trycua/cua/blob/cua-driver-rs-v0.34.0/libs/cua-driver/rust/crates/cua-driver/tests/policy_tools_list_test.rs).
- [cli.rs: opções de lançamento e manifesto](https://github.com/trycua/cua/blob/cua-driver-rs-v0.34.0/libs/cua-driver/rust/crates/cua-driver/src/cli.rs).


## Causa controlada de falha de inicialização — 2026-10-10

A causa agora é registrada no estado MCP e projetada pelas rotas GET existentes de metadados do Computer, com `status: startup_failed` e `reason` fixo. A UI apresenta uma frase fixa em PT-BR; não apresenta o valor bruto. `executable_missing`, conectado, desativado e indisponibilidade após conexão preservam a semântica anterior e não recebem reason de startup. Não há action API nova.

A classificação usa `errors.Is` nas sentinelas existentes e a etapa local conhecida de criação. Não analisa Error(), mensagens do servidor, stderr ou valores do mcp.json. Somente a categoria é retida no Manager; o erro original não entra no status.

| Categoria | Evidência e comportamento |
| --- | --- |
| `environment_failed` | Resolver de ambiente retorna erro que preserva `mcp.ErrConfig`. Erro livre do resolver fica unknown. |
| `handshake_failed` | `ErrRemote` ou `ErrUnsupported` durante initialize/notificação de inicialização. |
| `discovery_failed` | `ErrRemote` ou `ErrUnsupported` durante tools/list. |
| `discovery_too_large` | Categoria reconhecida pelo contrato/UI, mas não emitida: não existe tipo específico para limite de descrição/frame. `ErrLimit` também cobre schema, nomes duplicados, quantidade e paginação, portanto permanece unknown na descoberta. |
| `tool_limit_exceeded` | Comparação existente com MaxTotalTools no Manager, classificada a partir de ErrLimit e etapa local de contagem. MaxTools dentro de discover não é distinguível por tipo e fica unknown. |
| `timeout` | `context.DeadlineExceeded`, inclusive quando envolto. |
| `cancelled` | `context.Canceled`, inclusive quando envolto. Cancelamento antes do loop continua retornando erro ao chamador, sem novo status artificial. |
| `protocol_error` | `mcp.ErrProtocol`. |
| `unknown` | Erro livre, ErrUnavailable, ErrConfig fora do resolver, ErrLimit ambíguo ou qualquer erro sem evidência tipada suficiente. |

Não foram criados novos tipos de erro nem modificados limites. Os testes offline aceitam descrição genérica de 1.024 bytes e rejeitam 1.025 bytes com o mesmo ErrLimit anterior; não há exceção CUA. A assinatura pública de NewClient e o fluxo de criação permanecem; um helper privado comunica somente a etapa local ao Manager.

Validação e contagens: [relatório desta rodada](MCP_STARTUP_REASONS.md). Descoberta pelo DAIMON, leitura de janela, aprovação, rotinas com computador, Space, Volume, Windows com cua-driver.exe e Fleet continuam sem validação CUA real nesta rodada. Registrar a categoria não corrige nem contorna a falha de descoberta anterior.
