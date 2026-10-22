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

O shell não alcança o proxy HTTP (`Failed to connect to proxy port 8080`).
As leituras remotas foram feitas pelo conector GitHub, sem contornar a política
de rede. Não houve instalação, login ou inferência paga.

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

Resultados executados, inventário e instruções serão registrados ao finalizar
este corte. O critério final solicitado (Bot em ciclo real Harness²) permanece
pendente; esta entrega não declara as fases seguintes concluídas.
