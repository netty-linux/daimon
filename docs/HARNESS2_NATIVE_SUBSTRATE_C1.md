# Harness² — Native Substrate Foundation C1

Revisão posterior do PR #27: [achados, regressões e critérios C2](HARNESS2_C1_FINAL_REVIEW.md).

Base: main `bedb0866988b0e72dae4f533a630c856de88efb5`, com #26, #24 e #25
integrados nessa ordem. Checkout e HEAD remoto foram conferidos antes da nova
branch `feature/harness2-native-substrate-c1`. Upstream permanece na revisão
`1cfe254e8d25b15f37048c3730134686abe60335`, Apache-2.0.

## Auditoria e pontos de integração

Foram consultados AGENTS.md, README, os documentos de integração/ponte e os
contratos de Bots, Sessions, providers, tools, sandbox e policy/approvals.
`sessions.Manager.resolve` atualmente resolve Bot/Thread dos stores e monta
Binding, tools, provider e recursos. C1 não altera esse caminho nem chama Start.
Uma integração futura precisará de entrada explicitamente congelada e privada,
mantendo policy.Authorizer, aprovação individual, budgets e ownership existentes.
Snapshots de Session e SSE não são um canal apropriado de evidência experimental.

No upstream foram inspecionados `substrate/base.py`, `core/tree.py` e
`core/spec.py`. Este último importa config; `substrate/__init__.py` importa os
launchers. Esses dois módulos não são executados. C1 não implementa
BenchmarkAdapter, BenchSpec, launcher, engine ou algoritmo de otimização.

Plano anterior à implementação: reutilizar snapshot/candidato da A no Go,
provas fechadas em memória, gates indisponíveis e conformidade Python separada,
sem novas operações RPC. Não foram criadas abstrações paralelas de Session,
provider, store, sandbox ou autorização.

## Arquitetura e fluxo

`internal/harness2substrate.Prepare(ctx, manifestJSON, baselineBot)` chama Decode,
Freeze e Candidate da A. Os oito campos permanecem no manifesto. BotID, hash da
base completa, revision e forma do parent hash são validados pelos mesmos
contratos; a existência histórica do parent não é comprovada sem um futuro store.
As sete superfícies além de system_prompt são recusadas quando não vazias.

Plan guarda somente o snapshot privado da A. Manifest, Candidate e Tree devolvem
cópias independentes. Tree contém uma única chave lógica `systemprompt.md`;
não é um caminho a ser aberto. Candidate exige novamente a baseline e falha
se seu fingerprint mudou. Nada grava ou promove um Bot.

Fluxo disponível:

1. Go recebe bytes limitados e baseline explícita; prepara Plan em memória.
2. Go executa uma das duas fixtures estruturais ou devolve erro controlado.
3. Opcionalmente, o chamador usa o cliente B existente para validar o manifesto
   no worker original: Go → Python → Go. C1 não faz isso implicitamente.
4. Testes Python separados conferem a interface Substrate original e o preview.

A ponte B mantém suas três operações e seu schema v1 integralmente inalterados.
Os testes C1 usam essa API real nos casos compartilhados ASCII, Unicode e
referência a script ausente. No último caso, Prepare/Probe estruturais aceitam
texto inerte, mas o validador upstream real o rejeita. Isso não autoriza execução.

## Mapeamento upstream

| Contrato original | C1 |
| --- | --- |
| Substrate.name / surface() | NativeSubstrate Python retorna SurfaceSpec original, frozen, com identidade C1 e limites da superfície B |
| core.tree.validate | Função original sobre cópia da árvore de um único prompt, com allow_subagents=False e limite adicional de 8192 bytes |
| Install(staging_dir, fingerprint, extras) | Tipo original carregado e conferido; nenhum Install é fabricado para uma árvore apenas em memória |
| materialise(tree_dir, spec, ctx) | Recusa ExecutionUnavailable antes de acessar qualquer path ou ctx de instalação |
| execute(task_ref, install, run_ids, timeout) | Recusa ExecutionUnavailable; sem resultados/run_dirs fictícios |
| read_trajectory(...) | Recusa ExecutionUnavailable; não há trajetória de Session ou rollout C1 |
| preflight() | Recusa ExecutionUnavailable; não anuncia readiness de execução |
| BenchmarkAdapter | Não implementado; as fixtures Go não simulam solver, benchmark, juiz ou métricas reais |

`python/harness2_native_substrate/substrate.py` reutiliza a verificação B de todos
os fontes/licença/commit instalado. Carrega novamente os bytes de base.py e
confere seu hash antes de compilar o snapshot original. Um namespace de pacote
inerte permite importar esse módulo sem executar __init__ com launchers. Não há
substituição de Substrate/Install/SurfaceSpec por mocks, cópia ou alteração de
algoritmos upstream. Preview é uma representação C1 adicional, frozen e privada,
validada pela função original; não se apresenta como materialise bem-sucedido.

Python é opcional, restrito a 3.12 com -I -S -B e caminhos de deployment confiáveis.
O loader é destinado a um processo de conformidade dedicado; não oferece reload
concorrente ou serviço Python persistente e altera namespaces em sys.modules.
O loader/worker/stdlib/inventário continuam confiáveis; não há atestação externa
de todo o deployment. C1 não transforma esse loader em sandbox.

## Provas e evidências

`Probe(ctx, plan, baseline, fixtureID, outputLimit)` aceita somente:

- identity-v1: associação nativa ao Bot base e hash dos bytes do prompt.
- prompt-roundtrip-v1: adicionalmente compara a cópia Candidate com a árvore
  lógica do snapshot, sem executar ou interpretar as instruções.

Não há callbacks de solver, caminhos, tarefas livres, comandos ou modelos como
parâmetros. Produção importa apenas contexto, hashing/JSON, erros, tempo, Bots
e contratos A. Um teste arquitetural impede introdução acidental de imports de
efeitos ou runtime. Isso comprova o desenho do código, não isolamento de SO.

Limites: manifesto JSON 64 KiB; prompt 8192 bytes UTF-8; evidência no máximo
2048 bytes (limite do chamador deve ser positivo e não superior ao teto).
Toda prova deriva um deadline de no máximo um segundo, preservando o prazo menor
do chamador. Há verificações antes, durante o hashing limitado e depois da
serialização. Cancelamento/erro não devolve evidência parcial. O prazo é
cooperativo; não é quota de CPU de SO ou garantia sobre syscalls arbitrárias.

Evidence guarda JSON canônico privado por valor e JSON() entrega cópia dos bytes.
Schema v1: version, fixture, baseline_sha256, candidate_sha256, prompt_sha256,
prompt_bytes, checks, structural_only=true, performance_evaluated=false,
real_execution=false. Não há timestamps, duração observada, texto do prompt,
resultado de modelo, trajetória ou score. checks conta as asserções da fixture,
não avalia qualidade. SHA do candidato é o snapshot nativo A, não o fingerprint
de um Install upstream inexistente. Hash não é assinatura e pode revelar
informação por ataques de dicionário; evidências não devem ir a logs/eventos.

O resultado é determinístico para entradas equivalentes e a mesma versão da
fixture. As representações String de Plan/Evidence omitem conteúdo e hashes.
Não há comparação científica, indicação de ganho ou promoção automática.

## Gates e C2

SecurityGates retorna cópias de 18 requisitos, todos unverified:
filesystem, egress, separação de credenciais, integridade de interpretador,
worker, dependências e deployment, ownership da árvore, quotas de CPU, memória,
disco, tempo e processos, workspace privado, transcript privado, supervisão,
cancelamento/cleanup e recuperação após falhas.

RequireRealExecution sempre recusa, mesmo depois de provas bem-sucedidas ou se
o chamador modificar a cópia dos gates. Não existe flag, bool ou método para
liberar execução com autoatestação. Os testes de unidade não satisfazem gates.
O adaptador Python também recusa todas as operações que exigiriam execução.

Para C2 serão necessários escopo explícito e evidência real de SO: deployment
imutável/atestado externamente, credenciais separadas, filesystem/mounts mínimos,
rede negada por política externa, quotas agregadas e supervisor com ownership,
kill/join/reaping, cleanup e recuperação após crash. Depois disso, integração
explícita de candidato congelado ao runtime Go, workspace/transcript privados e
testes mantendo autorização humana. Nada disso é habilitado por esta entrega.

## Reprodução e resultados

Ambiente Python é a venv isolada da B, sem instalação global ou novos extras.

```powershell
$env:DAIMON_HARNESS2_TEST_PYTHON = (Resolve-Path python/harness2_bridge/.venv/Scripts/python.exe).Path
$env:DAIMON_HARNESS2_TEST_SITE = (Resolve-Path python/harness2_bridge/.venv/Lib/site-packages).Path
py -3.12 -I -S -B python/harness2_bridge/verify_pin.py .harness2-upstream
& $env:DAIMON_HARNESS2_TEST_PYTHON -I -S -B python/harness2_native_substrate/test_substrate.py
go test -race -count=1 -v ./internal/harness2substrate
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
gofmt -l .
git diff --check
```

O runner Linux B foi estendido somente com a chamada dos testes Python C1.
Ele continua offline, com fonte somente leitura, cópia privada, umask 022,
TMPDIR no mesmo filesystem e as duas variáveis de worker real configuradas.
Sem ambas as variáveis, os subtestes opcionais reais Go registram SKIP;
configuração parcial C1 é FAIL. Python C1 exige a variável SITE e não pula testes.
Os comandos de validação desta entrega configuram ambas explicitamente.

| Verificação | Estado final local |
| --- | --- |
| Go C1 com race, incluindo casos reais da B | PASS |
| Python C1 contra contratos originais | PASS: 5 testes Windows e Linux, incluindo tamper |
| Integridade upstream | PASS: 39 fontes + Apache-2.0 |
| Go Windows completo normal / race | PASS / PASS; C1 2,514 s / 3,830 s; ponte B 7,145 s / 6,483 s |
| Go Linux completo normal / race, UID 65534 | PASS / PASS; C1 0,794 s / 2,075 s; ponte B 1,646 s / 2,677 s |
| Python B e limites Linux | PASS: 8 testes Windows/Linux e mais 1 teste Linux |
| Vet Windows/Linux, formatação e diff | PASS |
| UI typecheck, testes e build | PASS: 80 testes/15 arquivos; assets sem alterações |
| Demo Linux offline | PASS: completed, 2 passos, 1 ferramenta |
| Primeira tentativa Linux em tmpfs padrão | FAIL de ambiente: noexec impediu iniciar binários Go |
| Segunda tentativa Linux em tmpfs exec | FAIL de ambiente: managedworkspace e CLI recusam mount separado por NO_XDEV |
| Worker real C1/B nas execuções locais finais | PASS, sem SKIP: ambas as variáveis configuradas |
| Integrações Python opcionais no CI Go existente | SKIP quando as variáveis não estão configuradas; não comprova o E2E local |
| Casos exclusivos Linux na execução Windows | NOT RUN por build tags/plataforma; executados no Linux |
| Isolamento adversarial, Sessions reais, modelos/launchers, C2 | NOT RUN por escopo; execução indisponível e gates unverified |

Os dois FAILs Linux não foram convertidos em PASS. A primeira montagem foi
confirmada por findmnt como noexec. Após permitir binários, a segunda tentativa
atingiu a guarda existente de `managedworkspace.checkLocalPath`: openat2 com
NO_XDEV exige que a base esteja no filesystem raiz do namespace, não só junto do
source/TMPDIR. Nenhuma guarda ou teste foi alterado. O runner final usa o /work
original no overlay do container, ajusta seu proprietário e troca para UID 65534
antes de executar a suíte. O teste de conteúdo ilegível executa como não-root;
seu SKIP condicionado a root não é necessário nesse ambiente.

Comando Linux final (imagem B já construída com dependências fixadas):

```powershell
docker run --rm --env HOME=/work --env GOCACHE=/work/go-cache --network none --cpus 4 --memory 4g --pids-limit 512 --mount "type=bind,source=$((Get-Location).Path),target=/source,readonly" daimon-harness2-phase-b-test:latest bash -c 'chown 65534:65534 /work && exec setpriv --reuid=65534 --regid=65534 --clear-groups bash /source/python/harness2_bridge/test-linux.sh'
```

Imagem usada: `sha256:bbbdfac62f6851c8191f46b39d3404febe84a3fa80f9426dc6bf5bd9303d619a`.
Go 1.27.1, Python 3.12.10, uv 0.12.17. Chown afeta somente /work privado do
container descartável; não altera fonte montada ou permissões no host.
Esse ambiente de testes não constitui um backend de isolamento do produto.

Logs locais, não versionados, em `D:/hyperframes-cache/temp/`:
`daimon-c1-windows-normal.log`, `daimon-c1-windows-race.log`,
`daimon-c1-linux.log` (noexec), `daimon-c1-linux-final.log` (NO_XDEV) e
`daimon-c1-linux-overlay.log` (suítes finais PASS). Os testes existentes foram
preservados; o runner encerrou antes de race nas duas tentativas FAIL.

Não há alegação de homologação de isolamento baseada nas fixtures. Modelos,
launchers inseguros, Sessions reais e C2 não foram executados. Os checks do PR
draft cobrem Go/Linux e UI; Python e E2E reais foram comprovados localmente,
não pelo CI sem essas dependências opcionais. Não há autorização de merge.
