# Phase B — ponte Python de validação

Escopo: worker opcional e privado, com `health`, `capabilities` e
`validate_manifest`. Não há importação deste pacote por `cmd`, `serve`, Sessions,
providers, tools, policy, Bot Store ou UI. Nenhuma Phase C foi implementada.

## Dependência original e instalação

Harness² original: https://github.com/google-research/harness2, revisão fixa
`1cfe254e8d25b15f37048c3730134686abe60335`, versão 0.1.0, Apache-2.0.
O ambiente local requer Python 3.12; a biblioteca upstream permite 3.12–3.13.
A restrição adicional mantém este worker/test runner reproduzível em 3.12.

`python/harness2_bridge/pyproject.toml` e `uv.lock` fixam a origem Git e o commit.
Os requisitos de build também são fixos: setuptools 80.9.0 e wheel 0.45.1.
Não são instalados extras `bridge`/`render`, CLIs de agentes ou provedores.
O ambiente runtime contém somente harness2 e a biblioteca padrão Python.
O download/build inicial exige rede; as chamadas da ponte não precisam de rede.
A ferramenta uv usada nesta validação é 0.12.17.

```powershell
uv sync --locked --project python/harness2_bridge --python 3.12
$env:DAIMON_HARNESS2_TEST_PYTHON = (Resolve-Path python/harness2_bridge/.venv/Scripts/python.exe).Path
$env:DAIMON_HARNESS2_TEST_SITE = (Resolve-Path python/harness2_bridge/.venv/Lib/site-packages).Path
& $env:DAIMON_HARNESS2_TEST_PYTHON -I -S -B python/harness2_bridge/test_worker.py
go test -count=1 ./internal/harness2bridge
go test -race -count=1 ./internal/harness2bridge
```

Nada é instalado no Python global. `.venv`, bytecode e o checkout de auditoria
`.harness2-upstream` são ignorados pelo Git. O checkout de auditoria não é
necessário para executar a ponte. Para conferir o inventário contra os objetos
Git originais, sem importar ou executar código do checkout:

```powershell
py -3.12 -I -S -B python/harness2_bridge/verify_pin.py .harness2-upstream
```

O inventário `upstream.lock.json` contém SHA-256 de todos os 39 arquivos Python
originais, calculados com `git show REVISION:path`, e da licença completa,
preservada em `LICENSE.harness2`. Há dois hashes explícitos por fonte: bytes LF
originais e a transformação exata LF→CRLF usada pelo Git no Windows. Não há
normalização arbitrária de código para fazê-lo passar. Arquivos ausentes,
fontes adicionais ou alteradas falham antes da importação upstream. O worker
confere também `direct_url.json` e a licença da distribuição instalada.
Isso prova integridade em relação ao inventário confiável; não é assinatura do
Google nem proteção contra um usuário que altere o worker e seu inventário.

Somente `harness2`, `harness2.core` e `harness2.core.tree` originais são executados,
a partir de snapshots dos bytes verificados, evitando caches `.pyc` e extensões
substitutas. Nenhum algoritmo é substituído ou modificado. Não é importado
`harness2.config` (que carrega `.env`), nem launchers, proposer, recursion,
selection, judge, subprocessos upstream ou modelos. Os demais fontes instalados
são verificados, mas não executados.

## API interna e protocolo v1

`internal/harness2bridge.New(Config)` recebe caminhos absolutos explícitos do
Python da venv, worker fixo `worker.py`, site-packages e raiz privada de
scratch, além de timeout positivo até 30 segundos. Caminhos/configuração são
inputs de deployment confiável; nunca vêm de manifesto, modelo ou navegador.
Um Client possui seu contexto, uma operação em voo e `Close()` idempotente que
cancela operações ativas/na fila e aguarda processos e limpeza. Múltiplos Clients
não possuem quota global compartilhada. O aplicativo comum não cria um Client.

Cada operação inicia um worker novo, com `-I -S -B`, ambiente vazio, cwd temporário
novo e stdin/stdout privados. Não usa shell, PATH, HOME, dotenv, PYTHONPATH,
user site ou `.pth`. A entrada só é liberada depois do registro de ownership do
processo. Não há retry, restart automático ou serviço Python persistente.

Cada processo lê um documento JSON UTF-8 até EOF (máximo 96 KiB), retorna um
único documento JSON seguido de newline e termina. Uma chamada = um processo:
`id: 1` é correlacionado dentro desse processo, sem multiplexação ou reutilização.
Versão e conjuntos de campos são exatos; duplicatas, null, campos desconhecidos,
aliases por capitalização, valores concatenados e profundidade excessiva falham.
Python também recusa surrogates inválidos, NaN/Infinity e booleanos como IDs.
Go compara a resposta ao schema fixo, aos valores/capacidades permitidos e à revisão.

Request de health/capabilities:

```json
{"version":1,"id":1,"operation":"health","payload":{}}
```

`validate_manifest` usa `payload: {"manifest": <manifesto-v1>}`. A entrada passa
primeiro por `harness2.Decode` no Go (limite nativo de 64 KiB), preservando a
validação autoritativa da Phase A. Superfícies não vazias além de system prompt
são recusadas antes do processo. Python valida a estrutura e chama a função
**original** `harness2.core.tree.validate` com um `SurfaceSpec` DAIMON mínimo e
uma árvore em memória contendo apenas `systemprompt.md`. Não há materialização
em disco, dereferenciamento de artefatos ou execução de scripts.

Resposta de sucesso:

```json
{"version":1,"id":1,"operation":"health","ok":true,"code":"ok","result":{"revision":"1cfe254e8d25b15f37048c3730134686abe60335","operations":["health","capabilities","validate_manifest"],"manifest_valid":false,"tree_files":0,"execution":false,"os_sandbox":false}}
```

Na validação bem-sucedida, `manifest_valid=true` e `tree_files=1`. Isso não prova
que BotID existe, não compara Bot Store, não congela entrada de Sessions e não
concede nenhuma permissão. Vinculação à base continua responsabilidade dos
contratos Go da Phase A. Referência a script ausente no prompt é um exemplo de
rejeição adicional produzida pelo validador upstream real, sem executar script.

Erros Python são respostas fixas `invalid_request`, sem mensagens livres,
manifestos, paths ou tracebacks; exit status é 2. Go recusa status não zero como
`ErrWorker`, resposta malformada como `ErrProtocol`, excesso como `ErrLimit` e
falha de remoção/ownership como `ErrCleanup`. Timeout/cancelamento preservam
`context.DeadlineExceeded`/`context.Canceled`. Stderr é descartado. Conteúdo
privado não é inserido em eventos, transcript, logs públicos ou erros Go.

## Lifecycle, recursos e limites de isolamento

O Go possui Start, kill, Wait, pipes e scratch. O tempo inclui fila, startup,
verificação e operação. Cancelamento mata o processo/grupo; `Close` aguarda todos
os calls. `WaitDelay` de um segundo limita pipes herdados. Toda operação aguarda
o processo e tenta remover seu scratch; falha de cleanup é explícita.

Windows: Job Object kill-on-close, limite de dois processos (redirector da venv
+ interpretador), 256 MiB de memória comprometida total/por processo e cinco
segundos de CPU em modo usuário por processo. Se criação/configuração/associação falha, o processo
é morto e aguardado sem receber a requisição. Após a revisão técnica final,
CreateProcess usa CREATE_SUSPENDED: o thread primário só é retomado depois da
associação ao Job. A retomada valida o processo dono do handle de thread e
recusa enumeração ambígua. A liberação termina o Job, retém handles dos membros
atuais (até dois) e aguarda seu estado final e ausência de membros ativos, por
até um segundo, antes de fechar o Job. Falha é ErrCleanup. Isso cobre o
redirector e o interpretador confiáveis da venv; não é supervisor genérico de
árvore adversarial. Job não restringe filesystem ou rede.

Linux: grupo de processos dedicado, SIGKILL no grupo em cancelamento/cleanup.
O worker configura limites hard/soft antes de carregar upstream: espaço virtual
256 MiB, CPU 5 s, tamanho de arquivo 0, 64 descritores e core dump desabilitado.
São limites do processo, não quotas globais de toda uma árvore arbitrária.
Processos que escapassem de um grupo não são contidos por este mecanismo.
Outros sistemas operacionais falham na configuração antes do spawn.

Após imports verificados, um audit hook Python nega abertura de arquivos,
sockets, subprocessos, exec/spawn, várias mutações de filesystem e carregamento
ctypes. Testes exercitam as negativas de escrita, socket e subprocesso. Isso é
defesa adicional para **código fixo confiável**; um audit hook não é uma fronteira
de segurança contra Python nativo/adversarial. Não há namespace, seccomp,
AppContainer, firewall de egress, ACL dedicada, quota de disco ou sandbox de
credenciais do usuário do SO. Python/stdlib/worker/inventário e processos do
mesmo usuário são confiáveis. Não se promete resistência a adulteração concorrente
do deployment, administradores, extensões nativas ou vulnerabilidades do Python.

O worker não recebe credenciais no ambiente e não oferece operação para ler
credenciais ou executar código. Porém, rodar sob o usuário do host não elimina
sua autoridade de SO. `os_sandbox=false` e `execution=false` são deliberados e
obrigatoriamente validados no Go. Esta ponte permite somente diagnóstico e
validação de dados com código auditado. O gate de isolamento completo registrado
na Phase A permanece aberto para qualquer engine, scripts, plugins, proposers ou
candidatos adversariais: **não há entrada que os inicie nesta implementação**.
Não se declara a execução Harness² isolada pronta nem a Phase C liberada.

## Validação Windows/Linux

Os testes novos unitários de protocolo não requerem Python. Testes reais da ponte
são opt-in pelas duas variáveis `DAIMON_HARNESS2_TEST_*`; quando não configuradas,
a dependência Python opcional não é imposta aos Bots convencionais. Na validação
registrada abaixo elas foram configuradas: os testes reais não foram pulados.
Fixtures de erro/lifecycle são scripts controlados dos testes; o teste end-to-end
e a rejeição semântica upstream usam a biblioteca original instalada, sem mocks.

A suíte existente exige Linux para contratos de escrita/managed workspace,
permissões Unix e symlinks. Nenhum teste anterior ou regra foi modificado.
Typecheck, 80 testes UI, build, vet e os testes reais da ponte são suportados no
Windows. Os assets embutidos reconstruídos devem permanecer idênticos.

Runner Linux opcional (build online; execução offline, checkout somente leitura):

```powershell
docker build -t daimon-harness2-phase-b-test -f python/harness2_bridge/Dockerfile.test python/harness2_bridge
docker run --rm --network none --cpus 4 --memory 4g --pids-limit 512 --mount "type=bind,source=$((Get-Location).Path),target=/source,readonly" daimon-harness2-phase-b-test
```

O runner copia fontes para filesystem privado Linux, usa umask 022 e TMPDIR no
mesmo filesystem das fixtures. A venv Linux é criada dentro da imagem, sem usar
a venv Windows montada. Docker aqui é ambiente de teste, não backend/sandbox do
produto. O Dockerfile fixa versões de Go/Python/uv; tags de imagem e distribuição
base ainda podem ser republicadas. Reprodutibilidade de dependência upstream vem
do SHA Git, inventário e uv.lock; não se promete imagem byte-identical.

### Resultados desta implementação

| Critério/verificação | Evidência e resultado |
| --- | --- |
| Branch separada | `feature/harness2-python-bridge` deriva de `7bb5053fab3e4fed6aff10f7ad06ff3320e8cf87`; branch Phase A permanece nesse SHA |
| Pin e licença | `verify_pin.py`: PASS; 39 fontes originais + licença Apache-2.0 contra objetos Git do SHA auditado |
| Comunicação real | health, capabilities e manifesto percorreram Go → Python 3.12 → Go; PASS Windows e Linux |
| Validador original | Validação de prompt em memória e rejeição upstream de referência a script ausente; PASS, sem mocks upstream |
| Protocolo | Limites, duplicatas/case aliases/null/campos ausentes, versão, correlação, respostas e entradas inválidas; PASS |
| Lifecycle | Deadline, contexto cancelado, Close concorrente, crash, saída excessiva e scratch removido; PASS |
| Credenciais/efeitos | Ambiente sem secret de teste, ausência de import de config, audit hook nega escrita/socket/subprocesso; PASS |
| Recursos Windows | Job com memória limitada: tentativa de alocação de 400 MiB recusada; PASS |
| Recursos Linux | RLIMIT_AS/CPU/FSIZE conferidos e alocação de 400 MiB recusada; PASS |
| Python multiplataforma | 8 testes PASS em Windows/Linux; mais 1 teste específico Linux PASS (sem adaptar regras para Windows) |
| Go Windows — ponte final | `go test -race -count=1 ./internal/harness2bridge`: PASS, incluindo worker real e Job Object |
| Go Linux — suíte completa | `go test -count=1 ./...`: PASS, inclusive contratos Linux/managedworkspace/symlinks |
| Go Linux — race completo | `go test -race -count=1 ./...`: PASS, sem alterar testes existentes |
| Vet e formato | `go vet ./...`: PASS Windows e Linux; `gofmt -l .`: sem saída |
| Demo offline | `go run ./cmd/daimon demo`: PASS Windows/Linux; completed, 2 passos/1 tool |
| UI | `npm ci`, typecheck, 80 testes em 15 arquivos e build: PASS; assets embutidos idênticos, lockfile preservado |
| Diff | `git diff --check`: PASS; nenhuma alteração em código/testes existentes do runtime |
| Isolamento adversarial | NÃO comprovado; sem sandbox de filesystem/rede do produto; gate mantido fechado para engine/candidatos |

As execuções completas iniciais no Windows **não passaram integralmente**:
`TestLifetimeExpiresAndRevokes` falhou por prazo na suíte normal e na race;
`TestCloudConcurrentReservationsBoundEffects` também falhou na race por prazo,
com arquivo em uso durante cleanup. Essas suítes estavam rodando em paralelo.
A repetição direcionada dos dois testes com race passou sem alterações.
Não se atribui a causa com certeza à carga do host, e não se apresenta aquela
execução completa Windows como PASS. A validação Linux completa e race passou.

A suíte Linux completa usou uma cópia privada do código durante desenvolvimento;
as últimas mudanças locais na ponte e a separação dos testes por plataforma
receberam adicionalmente vet/race/worker sobre uma cópia final nova. Nenhuma
suíte antiga foi reduzida, reescrita ou desabilitada. Testes próprios de limites
Windows ficam em arquivo Go com build tag Windows; o teste Python de rlimit tem
arquivo separado que exige Linux explicitamente, sem contá-lo como PASS Windows.

Logs locais de desenvolvimento (não versionados):
`D:/hyperframes-cache/temp/daimon-phase-b-go.log`,
`D:/hyperframes-cache/temp/daimon-phase-b-race.log`,
`D:/hyperframes-cache/temp/daimon-phase-b-linux.log`,
`D:/hyperframes-cache/temp/daimon-phase-b-linux-bridge-final.log` e
`D:/hyperframes-cache/temp/daimon-phase-b-docker-build.log`.

### Inventário de alterações

| Arquivo | Mudança |
| --- | --- |
| `.gitignore` | Ignora venv, checkout de auditoria e bytecode; mantém Dockerfile de teste versionável |
| `README.md` | Link e estado da ponte opcional |
| `docs/HARNESS2_INTEGRATION.md` | Distingue auditoria histórica A e estado B |
| `docs/HARNESS2_PYTHON_BRIDGE.md` | Novo: contrato, reprodução, riscos, resultados e inventário |
| `internal/harness2bridge/bridge.go` | Novo: cliente privado, protocolo, validação nativa e lifecycle |
| `internal/harness2bridge/bridge_test.go` | Novo: protocolo, worker real, lifecycle e ambiente |
| `internal/harness2bridge/process_windows.go` | Novo: ownership e limites Job Object |
| `internal/harness2bridge/process_windows_test.go` | Novo: teste real do limite de memória Windows |
| `internal/harness2bridge/process_linux.go` | Novo: ownership/kill do grupo |
| `internal/harness2bridge/process_other.go` | Novo: plataforma não suportada falha fechada |
| `python/harness2_bridge/.dockerignore` | Novo: contexto Docker mínimo |
| `python/harness2_bridge/.gitattributes` | Novo: LF estável para fontes, locks e licença |
| `python/harness2_bridge/Dockerfile.test` | Novo: ferramentas/dependência em ambiente Linux isolado |
| `python/harness2_bridge/LICENSE.harness2` | Novo: licença upstream integral |
| `python/harness2_bridge/pyproject.toml` | Novo: dependência Git e requisitos de build fixos |
| `python/harness2_bridge/uv.lock` | Novo: resolução reproduzível da dependência |
| `python/harness2_bridge/upstream.lock.json` | Novo: inventário SHA-256 da revisão auditada |
| `python/harness2_bridge/verify_pin.py` | Novo: conferência independente contra objetos Git |
| `python/harness2_bridge/worker.py` | Novo: três operações fixas e limites defensivos |
| `python/harness2_bridge/test_worker.py` | Novo: oito testes com a biblioteca real |
| `python/harness2_bridge/test_linux_limits.py` | Novo: teste Linux de limites hard |
| `python/harness2_bridge/test-linux.sh` | Novo: validação offline completa em cópia Linux |

22 arquivos no corte: 3 existentes modificados e 19 novos. A primeira entrega
local foi feita sem commits ou publicação. A consolidação organiza commits
pequenos e permite apenas publicação da branch B e PR draft contra a branch A,
conforme autorização posterior do usuário. Não há autorização para merge.
A branch da Phase A não foi movida. Nenhum código de runtime
anterior, manifesto da Phase A, teste existente ou asset UI foi modificado.
Instalações locais: venv Python 3.12 em `python/harness2_bridge/.venv`,
dependências UI em `ui/node_modules` e imagem Docker de testes. Docker Desktop
foi iniciado para permitir a validação Linux. Não houve modelo pago, launcher
Codex/OpenCode, otimização, execução de candidato ou avanço para Phase C.


## Consolidação para revisão draft — 2026-10-22

O escopo publicável é a ponte opcional para código fixo confiável e validação em
memória. Não se declara homologado o gate de isolamento adversarial completo
registrado na Phase A. `execution=false`/`os_sandbox=false` permanecem obrigatórios;
não existe operação para iniciar engine, candidatos, modelos ou launchers externos.
A publicação em draft não libera Phase C nem altera a main.

### Revisão e correção

A leitura integral dos 22 arquivos encontrou uma falha em `gatedInput.Read`: o
copiador de stdin aguardava o sinal, mas não consultava `allowed`. Assim, o
caminho de falha de containment poderia liberar o manifesto ao desbloquear o
copiador. `TestContainmentGateWithholdsRequest` reproduziu a falha antes da
correção. Agora a negativa retorna EOF sem entregar bytes; a liberação positiva
continua entregando exatamente a requisição. O teste passou com race no Windows
e Linux. Não foi alterado nenhum contrato da Phase A.

Também foi acrescentado `TestActiveWorkerCancellationAndClose`: o worker
controlado dos testes confirma recebimento de stdin antes de o teste cancelar
ou chamar Close. Os dois caminhos verificam cancelamento, join e remoção do
scratch, com race nas duas plataformas. Na primeira versão desse teste, a
remoção prematura do marcador de readiness encontrou um handle ainda aberto
no Windows. A fixture passou a removê-lo após o join do worker; nenhuma regra
ou teste anterior do produto foi modificado.

Os testes reais usam a biblioteca original; fixtures locais de erro e readiness
são exclusivamente testes do transporte/lifecycle. Nenhum launcher Codex,
OpenCode, proposer, modelo pago ou otimização é executado. A execução Linux de
validação tem `--network none`; os testes HTTP existentes usam servidores locais.

### Revalidação da consolidação

| Verificação | Resultado |
| --- | --- |
| `verify_pin.py` contra checkout oficial limpo | PASS: SHA fixo, 39 fontes e licença SHA-256 `cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30` |
| `uv sync --locked --project python/harness2_bridge --python 3.12` | PASS; somente ambiente isolado |
| Python Windows/Linux | PASS: 8 testes; Linux tem mais 1 teste hard-limit PASS |
| Ponte final Windows/Linux com race | PASS, incluindo controle de stdin e cancelamento de worker ativo |
| Go Windows completo, `go test -count=1 ./...` | PASS na consolidação; os skips de plataforma existentes permanecem inalterados |
| Go Linux completo, `go test -count=1 ./...` | PASS, sem skips para contornar limitações Windows |
| Go Linux completo com race | Resultado registrado após a conclusão desta execução |
| `go vet ./...`, `gofmt -l .` | PASS; nenhum arquivo sem formatação |
| UI typecheck / testes / build | PASS: 80 testes/15 arquivos; assets e package-lock idênticos à base |
| Revisão de arquivos/segredos | Somente os 22 arquivos do inventário; nenhum caminho de ambiente/cache/temporário ou formato reconhecível de credencial encontrado |

As falhas de prazo Windows da implementação inicial continuam registradas acima;
a execução completa Windows desta consolidação passou. Não se transforma o
resultado histórico em PASS. O único teste RED proposital na revisão foi a
reprodução do defeito de stdin, corrigido antes da publicação. A falha da nova
fixture de marcador também foi corrigida e revalidada separadamente.

Locks e inventário são metadados reproduzíveis deliberadamente versionados;
nenhuma dependência instalada, `.env`, virtualenv, cache, `.pyc`, node_modules,
log ou temporário integra os commits. A busca por formatos de credenciais não
é uma garantia universal de ausência de segredos; foi complementada pela leitura
integral dos arquivos. Os bytes e os caminhos do diff final são conferidos antes
do push. O inventário/licença upstream não mudou nesta consolidação.

Evidências locais desta revisão, não versionadas:
`D:/hyperframes-cache/temp/daimon-phase-b-consolidation-windows.log`,
`D:/hyperframes-cache/temp/daimon-phase-b-consolidation-linux.log`,
`D:/hyperframes-cache/temp/daimon-phase-b-consolidation-bridge.log` e
`D:/hyperframes-cache/temp/daimon-phase-b-consolidation-linux-final.log`.


## Revisão técnica final dos PRs #24 e #25

A revisão posterior identificou execução de bootstrap anterior ao Job e falta
de espera pelo estado terminal do interpretador filho no Windows. A janela de
stdin já corrigida anteriormente não resolvia essas duas condições. Foram
adicionados testes RED/GREEN, startup suspenso, encerramento/espera explícita
dos membros do Job e preservação de contexto durante falhas de inicialização.

O relatório [HARNESS2_FINAL_REVIEW.md](HARNESS2_FINAL_REVIEW.md) classifica os
achados por fase, descreve evidências, propostas mínimas e limitações restantes.
O texto desta documentação também teve sua codificação UTF-8 corrigida; isso
não altera protocolo, dados, hashes upstream ou os contratos da Phase A.


A revisão final toca sete arquivos em relação ao SHA consolidado anterior e
acrescenta o teste Linux de descendentes e o relatório de revisão. Assim, o diff
acumulado do PR #25 passa de 22 para 24 arquivos; o inventário de 22 acima é o
registro da entrega inicial. A correção de código está no commit
`4d89d1caa2cac61e42f6364d3b664310f68b6dba`. O relatório distingue os testes finais
PASS da falha observada na suíte Windows completa. Nenhuma garantia de sandbox
adversarial foi acrescentada.

## Consolidação de #26 → #24 → #25 — 2026-10-23

A autorização posterior do operador permite a consolidação sequencial, condicionada
à revisão, ausência de bloqueadores e checks aprovados. Os registros anteriores
preservam as falhas e o escopo de autorização existentes naquelas execuções.

- #26 integrado por merge commit `5e5490b425f91127763d5b9b9191cc90fb0c324e`.
  A correção é exclusivamente de testes/documentação; mantém os deadlines e o
  runtime fail-closed. Nova repetição dos três cenários com race/count=30 passou.
- A branch A recebeu main por merge `39e7888ad71c6c271049d650414893e8967242db`,
  preservando os commits originais. #24 foi integrado por merge commit
  `79582205ba077f4a7e14ed527dfb271e4c131218`, após todos os checks aprovados.
- #25 foi retargetado para main. A branch B recebeu main por merge
  `26976610d9ad96cd29ba822f545816034f5b245e`, preservando os seis commits
  originais e a correção Windows. Seu diff contra main contém somente os 24
  arquivos da Phase B, incluindo esta documentação. Contratos A e runtime
  sandbox/computer/Sessions/policy não diferem da main já integrada.

As validações abaixo executaram o código de `2697661`; o commit posterior
somente acrescenta este registro. Os checks do HEAD final devem concluir antes
da integração de #25. O SHA do merge final é rastreável no
[PR #25](https://github.com/netty-linux/daimon/pull/25).

| Validação desta consolidação | Resultado |
| --- | --- |
| Regressão após #26 e após #24, normal/race | PASS: sandbox, lifetime/cleanup e contratos A |
| Windows, Go completo sem race | PASS; sandbox 36,917 s, ponte real 8,893 s |
| Windows, Go completo com race | PASS; sandbox 39,189 s, ponte real 7,009 s |
| Linux, Go completo sem race | PASS; sandbox 4,280 s, ponte real 2,892 s; managedworkspace/symlinks preservados |
| Linux, Go completo com race | PASS; sandbox 6,390 s, ponte real 3,411 s; managedworkspace/symlinks preservados |
| Python real Windows/Linux | PASS: 8 testes em cada plataforma; mais 1 teste hard-limit Linux |
| E2E, cancelamento, falha de startup e cleanup | PASS nos pacotes completos da ponte, com variáveis DAIMON_HARNESS2_TEST_PYTHON/SITE explicitamente configuradas |
| Integridade e procedência | PASS: 39 fontes e licença Apache-2.0, contra objetos Git do checkout oficial limpo na revisão fixada |
| go vet, gofmt e git diff --check | PASS; nenhuma formatação pendente |
| UI local | PASS: typecheck, 80 testes/15 arquivos e build; assets sem alterações; UI é idêntica à main |
| Demo Linux offline | PASS: completed, 2 passos, 1 ferramenta |
| Revisão do diff | Somente Phase B; nenhum ambiente/cache/temporário/dependência instalada ou formato reconhecível de credencial encontrado |

Windows usa Go 1.27.1, CGO habilitado, Python 3.12.10 e uv 0.12.17. Linux usa
o runner versionado com fonte somente leitura, cópia privada, umask 022, TMPDIR
no mesmo filesystem e `--network none --cpus 4 --memory 4g --pids-limit 512`.
Imagem local de teste:
`sha256:bbbdfac62f6851c8191f46b39d3404febe84a3fa80f9426dc6bf5bd9303d619a`.
Nenhum launcher upstream, candidato, modelo ou API paga foi executado.
Testes específicos Linux não foram adaptados ou relaxados para Windows.

Os workflows push/PR da A atualizada passaram (runs 39961843214/39961847502),
assim como os workflows das mains após #26/#24 (39961683170/39962468355).
Os workflows da B em `2697661` passaram (39962622968/39962629171).
O CI continua sem matriz Windows/Python real: essas evidências são locais,
complementares aos checks Go/Linux e UI do GitHub, não implícitas no CI verde.

Logs locais não versionados:
`D:/hyperframes-cache/temp/daimon-final-consolidation-windows-normal.log`,
`D:/hyperframes-cache/temp/daimon-final-consolidation-windows-race.log` e
`D:/hyperframes-cache/temp/daimon-final-consolidation-linux.log`.

A falha histórica de lifetime permanece documentada; as execuções completas
desta consolidação passaram após a estabilização do teste em #26. Não houve
alteração de semântica de produção, supressão de cancelamento ou aumento de
timeouts. A revisão de segurança anterior continua válida para o código fixo
confiável. Os gates C1/C2/C3 de filesystem/rede/credenciais, integridade externa
do runtime e supervisão/quota/reaping de árvores adversariais permanecem abertos.
Phase C, recursão, Experience Library e conexão automática a Sessions continuam
fora desta consolidação.
