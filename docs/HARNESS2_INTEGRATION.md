# Harness² — integração incremental

## Estado e auditoria de 2026-10-22

Entrega atual: contratos nativos da Phase A. **Não há ciclo de otimização,
processo Python, promoção de Bot, endpoint ou interface de Otimização executável.**
As fases B–G dependem dos gates descritos abaixo. Não confundir candidato validado
com execução isolada, melhoria científica ou configuração aprovada.

HEAD inicial: `4c73fc1a5b94bacfbc2934e83bc7d67ff3ac7283`, checkout limpo,
branch inicial `work`. `origin/main` local e GET remoto
`repos/netty-linux/daimon/branches/main` confirmaram o mesmo SHA.
Trabalho em `feature/harness2-contracts`; main não é alterada.

No sandbox inicial, o shell não alcançou o proxy HTTP (`Failed to connect to
proxy port 8080`). As primeiras leituras remotas foram feitas pelo conector
GitHub. Após autorização automática de execução fora do sandbox de comandos,
`git ls-remote origin HEAD refs/heads/main` também confirmou o SHA acima usando
o proxy configurado. Não houve instalação, login ou inferência paga.

Fontes locais: AGENTS.md, README.md, DAIMON_ARCHITECTURE_V2, SESSION_RUNTIME,
WEB_APPROVAL_V1, MEMORY_V1, MCP_V1, SANDBOX_COMPUTERS_V1,
CLOUD_COMPUTERS_V1, PERSISTENT_ENVIRONMENTS_V1 e PRODUCT_UI_V2.
Código inspecionado: bots/validação/store, sessions/types/resolve, providers/registry,
memory/JSON, server/routes, App, ProductTabs, catálogos e package.json da UI.

PRs remotos mesclados e histórico local consultados: #23 (Pen frontend),
#22 (rotinas/CUA persistente), #21 (preimage export), #20 (output export),
#19 (preimage retention). #22 oferece admissão por Bot, retenção limitada e
ownership; não transforma rotinas em experimentos. #23 fornece design system
PT-BR e controles acessíveis. Exports gerenciados são operações explícitas de
workspace; não constituem sandbox para código proposto por IA.

## Auditoria upstream

Repositório: https://github.com/google-research/harness2
Revisão auditada: `1cfe254e8d25b15f37048c3730134686abe60335` (main remota).
Licença Apache-2.0; Python `>=3.12,<3.14`, versão 0.1.0, dependências base vazias.
Extras: render (openpyxl/python-pptx), bridge (litellm 1.95.0,
fastapi 0.139.2, google-auth 2.56.3). Esta revisão é uma referência de auditoria,
não uma dependência instalada ou certificada.

Arquivos lidos: README, pyproject.toml, config.py, substrate/base.py,
substrate/codex.py, substrate/opencode.py, core/spec.py, tree.py, recursion.py,
proposer.py, selection.py, library.py e judge.py.

- `Substrate` exige `surface`, `materialise(tree_dir, spec, ctx) -> Install`,
  `execute(task_ref, install, run_ids, timeout)`, `read_trajectory` e `preflight`.
  `Install` contém staging_dir, fingerprint e extras; não concede autoridade.
- `BenchmarkAdapter` exige spec/substrate, tasks/domain/task_prompt/task_files,
  execute idempotente por run_id, delivered, trajectory, deliverable_files/text,
  base_harness, validate_tree e score. `SubstrateLike` é apenas name/surface.
- `ChannelSpec` admite **claude_cli e opencode** para proposer/selector;
  os dois campos channel/model devem ser definidos juntos. OpenCode exige
  provider/model; Claude CLI exige ID sem prefixo. Isso não é o transporte
  OpenAI-compatible do DAIMON. Judge é específico do benchmark e separado.
- `run_parallel` e `run_sequential` implementam Propose–Probe–Compose; tokens
  internos `agg`/`seq`. São 4k rollouts e 3k estágios, incluindo bases partilhadas.
  Paralelo propõe k INV + k DOM e k composições independentes; sequencial carrega
  harness/evidências anteriores. Propostas inválidas recebem uma nova tentativa,
  depois herdam a configuração atual. Não reimplementar esse algoritmo em Go.
- `selection.run_selection` escolhe artefatos anonimizados; `reflect_adoption`
  reflete depois da adoção. `Library.add` retém árvore adotada e lições. Esse
  armazenamento não é `internal/memory`. Judge roda depois da seleção; falha
  é None, não zero. Não alimentar seleção com vereditos de avaliação.
- SurfaceSpec parametriza caminhos/limites. OpenCode tem systemprompt.md,
  guardrails.md, LongTermMEMORY.md, tool_descriptions/tool_guidance.md,
  skills, sub_agents, scripts e plugin. Codex usa AGENTS.md, .agents/skills
  e hooks. Manifesto nativo não é uma cópia automática dessas árvores.
- `CodexSubstrate.exec_argv` usa
  `--dangerously-bypass-approvals-and-sandbox`; proposer Claude usa
  `--dangerously-skip-permissions`. Config importa .env e consulta ambiente/cwd.
  Esses launchers e importações não podem ser tratados como processos confiáveis
  ou herdar ambiente/credenciais do servidor DAIMON.

O artigo https://arxiv.org/abs/2610.14224 é referenciado pelo README upstream;
não foi obtido nem validado separadamente nesta execução.

## Arquitetura e gates

Go permanece autoridade de lifecycle, Sessions, provider, tools, policy,
aprovações, memória manual e sandbox. Python deverá reutilizar core.recursion,
selection e library originais. Bridge deverá validar toda proposta e executar
somente por runtime Go, mantendo Bot ativo e transcript separados.

O runtime atual congela Binding, mas não fornece uma entrada de candidato
separada da resolução normal de Bot. Snapshots/eventos omitem deliberadamente
conteúdo e tool trajectories. Será necessário um canal privado, limitado e
opt-in de evidências; não ampliar SSE nem reutilizar transcript como trace.
Sessions normais também podem fazer sync de Environment; candidatos não podem
compartilhar reservas, revisões ou workspaces ativos.

MCP stdio controla ownership/cleanup, mas não é sandbox. O sandbox gVisor atual
tem rede outbound, acesso a serviços locais e nenhuma quota de disco/egress
verificada. Não oferece o conjunto de garantias exigido para proposer, scripts
ou plugins adversariais. Não iniciar engine com defaults upstream; ausência de
isolamento comprovado deve produzir erro controlado antes de qualquer processo.

Antes da Phase B: definir e testar protocolo limitado, versão/hash da dependência,
health sem modelos, ambiente mínimo sem .env do usuário, processo possuído com
encerramento/join, isolamento de filesystem/rede/credenciais e limites efetivos.
Antes da C: entrada congelada em Sessions sem editar Bot Store, workspace e
transcript privados, provider e ferramentas sob a política existente.
Antes da D: budgets globais para proposer/selector/judge e rollouts, cancelamento
que drene threads upstream, biblioteca privada e seleção sem promoção automática.
Antes da E/F: persistência transacional de intenção, crash/cleanup, revisão humana
vinculada a hashes e baseline atual, rollback explícito e APIs sem grants.

## Contratos deste corte

Planejado antes da alteração de código: criar apenas `internal/harness2` com
manifesto v1 das oito superfícies, parser estrito limitado, associação BotID,
fingerprint da configuração base, snapshot privado por valor e materialização de
um Bot candidato **em memória**, sem alterar stores, Sessions, tools ou frontend.
Somente system prompt é materializável neste corte. As outras sete superfícies
são dados preparatórios explicitamente recusados pela materialização quando não
vazias. Guardrails de IA nunca têm autoridade sobre policy.

Não adicionar factories, endpoints ou adapters Python vazios. Não conectar o
pacote ao serve: Bots convencionais não precisam de Python. Não gravar conteúdo
privado em logs; fingerprints também são metadata privada, não prova de segurança
nem autenticação. Testes usarão apenas fixtures e nenhum modelo remoto.

## Validação e continuidade

O critério final solicitado (Bot em ciclo real Harness²) permanece pendente;
esta entrega não declara as fases seguintes concluídas.

### Uso interno e limites do manifesto

O pacote não abre arquivos, lê ambiente, cria subprocessos ou chama providers.
Uso em código interno que já possui um `bots.Bot` válido:

```go
manifest, err := harness2.Baseline(bot)
if err != nil { return err }
manifest.Components.SystemPrompt = "Instruções candidatas"
snapshot, err := harness2.Freeze(manifest, bot)
if err != nil { return err }
candidate, err := snapshot.Candidate(bot)
if err != nil { return err }
// candidate é um valor privado. Não salvar no Bot Store nem executar antes
// dos gates de isolamento/lifecycle. bot continua com suas instruções originais.
```

`Decode` aceita um único documento UTF-8 até 64 KiB, versão 1, campos exatos
obrigatórios (incluindo as oito superfícies), sem null/duplicatas/case aliases,
surrogates inválidos ou valores concatenados. Quatro textos têm limite individual
de 8192 bytes; cada coleção aceita 16 referências com ID restrito e SHA-256
hexadecimal lowercase. Referências não são caminhos, comandos ou download URLs;
não são dereferenciadas. O limite codificado inclui escapes JSON.

`BotID` e `base_bot_sha256` fixam a associação e todos os campos do Bot base,
incluindo provider/modelo/permissões/tools/Computer/Sandbox. A existência do Bot
é verificada pelo valor explícito fornecido, sem lookup implícito. A mudança de
qualquer campo da base exige novo candidato e nova revisão. A serialização de
Bot pertence ao schema atual; uma mudança futura exige revisão do contrato de hash.

`revision` é positivo. Revisão 1 não tem parent; posteriores exigem parent hash.
Isso valida somente a forma da linhagem: não há store que prove que o pai existe,
compare monotonicidade ou autorize promoção. Hash é integridade determinística,
não assinatura, autorização ou garantia de melhoria. Não publicar hashes de
conteúdo privado indiscriminadamente (podem permitir ataques de dicionário).

Snapshot guarda bytes canônicos privados por valor; `Manifest()` retorna cópia
profunda, `Candidate()` clona o Bot. Nenhum estado global ou pointer de autoridade
é entregue ao candidato. Zero value falha fechado. Chamador possui os inputs e
não pode modificá-los concorrentemente durante Freeze/Candidate; snapshots prontos
suportam leitores concorrentes. Textos podem conter segredos: não há secret scanner,
criptografia nem promessa de imunidade de modelos a prompt injection.

| Superfície | Contrato atual | Materialização |
| --- | --- | --- |
| System prompt | Texto privado obrigatório | Somente cópia de Bot em memória |
| Guardrails | Texto privado | Não vazio retorna ErrUnsupported; nunca altera policy |
| Memory | Contexto experimental separado da Memory manual | Não vazio retorna ErrUnsupported |
| Tool guidance | Texto privado | Não vazio retorna ErrUnsupported |
| Subagents | Referências ID/hash | Coleção não vazia retorna ErrUnsupported |
| Skills | Referências ID/hash | Coleção não vazia retorna ErrUnsupported |
| Scripts | Referências ID/hash | Coleção não vazia retorna ErrUnsupported |
| Plugins | Referências ID/hash | Coleção não vazia retorna ErrUnsupported |

### Inventário

Criados: `docs/HARNESS2_INTEGRATION.md`, `internal/harness2/manifest.go`,
`internal/harness2/json.go`, `internal/harness2/manifest_test.go`.
Modificado: `README.md` (link e status experimental).
Nenhum algoritmo upstream foi copiado, reescrito ou executado: nesta etapa os
contratos reais orientaram o desenho; reuso executável fica para B–D.

### Cobertura de segurança e pendências

Unitários exercitam mudança de provider/modelo/tools/permissão/Computer/Sandbox,
prompt com instruções maliciosas sem alteração de autoridade, isolamento de
valores/slices, snapshot concorrente, Unicode, limites antes da aceitação,
hashes/revisões e rejeição das sete superfícies não materializáveis. Fuzz target
exercita decoder/roundtrip. Não há processo/modelo na fixture.

Não comprovados nesta etapa: contenção real de processo, cross-workspace de um
executor, bloqueio de escrita/rede/exfiltração, cancelamento de engine, limites
efetivos de CPU/RAM/disco, limpeza após crash, promoção/rollback e comparação de
evidências científicas. Recusar campos de credenciais/rede/workspace no manifesto
não é um teste de isolamento do sistema operacional.

Pendentes: dependência Python instalada e fixada, DaimonSubstrate e
DaimonBenchmarkAdapter reais, protocolo Go/Python, health checks, execução
isolada por Sessions, engine recursiva original, biblioteca de experiências
privada com retenção, budgets globais, lifecycle persistente, revisão humana,
rollback, HTTP/UI de Otimização e testes de integração/segurança correspondentes.
Nenhum botão ou endpoint fictício foi adicionado.
