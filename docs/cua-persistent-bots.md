# CUA Persistent Bots — auditoria de integração

Status em 2026-10-09: auditoria e baseline concluídas; integração ainda não implementada.
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
A alternativa proposta é um agendador DAIMON que somente chama SessionManager.Start,
com os mesmos budgets, capabilities e aprovações, e funciona apenas enquanto
o servidor está aberto. Essa alternativa requer alterar a condição do prompt
que exige usar rotinas CUA sempre que estiverem disponíveis. Decisão pendente.

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

## Rotinas propostas

Se aprovada a alternativa nativa: store explícito/versionado/limitado, no máximo
4 rotinas ativas/Bot e 32 totais, intervalo mínimo 15 minutos, horário diário
com timezone explícito e sem catch-up silencioso. Reserva de execução e exclusão
devem compartilhar admission com Bot/Thread e SessionManager.
Uma pendência de aprovação usa WaitingApproval existente, sem segundo canal.
Shutdown cancela/join o scheduler antes de fechar Sessions; restart não restaura
execuções nem approvals. UI PT-BR mostra estado, associação e ativar/pausar,
preservando Chat/Computador/Arquivos. Esse desenho ainda não foi implementado.

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
Mudanças desta auditoria são somente documentação: sem alteração de UI/API/runtime,
sem necessidade de regenerar assets ou repetir testes de código já aprovado.

## Opt-ins e limites de validação

Nenhuma chamada CUA, Space real, Fleet pago, instalação, login ou download CUA
foi executado. Clonar fontes para leitura não verifica compatibilidade binária.
Windows real, montagem de Volume, gVisor real, Fleet, durabilidade remota e
encaminhamento de aprovações de harness externo não foram validados.
Não há novas flags, endpoints ou comandos DAIMON para features não implementadas.
Não usar exemplos de API desta auditoria como garantia de integração funcional.

Arquivos locais preexistentes preservados: `.zcodeignore` e `IDEA.md`.
Decisões pendentes: agendador nativo em vez de rotinas do daemon; contrato de
exportação/importação de Memory; preflight de criação sem downloads implícitos.
