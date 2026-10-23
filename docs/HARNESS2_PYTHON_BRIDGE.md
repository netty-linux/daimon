# Phase B â€” ponte Python de validaÃ§Ã£o

Escopo: worker opcional e privado, com `health`, `capabilities` e
`validate_manifest`. NÃ£o hÃ¡ importaÃ§Ã£o deste pacote por `cmd`, `serve`, Sessions,
providers, tools, policy, Bot Store ou UI. Nenhuma Phase C foi implementada.

## DependÃªncia original e instalaÃ§Ã£o

HarnessÂ² original: https://github.com/google-research/harness2, revisÃ£o fixa
`1cfe254e8d25b15f37048c3730134686abe60335`, versÃ£o 0.1.0, Apache-2.0.
O ambiente local requer Python 3.12; a biblioteca upstream permite 3.12â€“3.13.
A restriÃ§Ã£o adicional mantÃ©m este worker/test runner reproduzÃ­vel em 3.12.

`python/harness2_bridge/pyproject.toml` e `uv.lock` fixam a origem Git e o commit.
Os requisitos de build tambÃ©m sÃ£o fixos: setuptools 80.9.0 e wheel 0.45.1.
NÃ£o sÃ£o instalados extras `bridge`/`render`, CLIs de agentes ou provedores.
O ambiente runtime contÃ©m somente harness2 e a biblioteca padrÃ£o Python.
O download/build inicial exige rede; as chamadas da ponte nÃ£o precisam de rede.
A ferramenta uv usada nesta validaÃ§Ã£o Ã© 0.12.17.

```powershell
uv sync --locked --project python/harness2_bridge --python 3.12
$env:DAIMON_HARNESS2_TEST_PYTHON = (Resolve-Path python/harness2_bridge/.venv/Scripts/python.exe).Path
$env:DAIMON_HARNESS2_TEST_SITE = (Resolve-Path python/harness2_bridge/.venv/Lib/site-packages).Path
& $env:DAIMON_HARNESS2_TEST_PYTHON -I -S -B python/harness2_bridge/test_worker.py
go test -count=1 ./internal/harness2bridge
go test -race -count=1 ./internal/harness2bridge
```

Nada Ã© instalado no Python global. `.venv`, bytecode e o checkout de auditoria
`.harness2-upstream` sÃ£o ignorados pelo Git. O checkout de auditoria nÃ£o Ã©
necessÃ¡rio para executar a ponte. Para conferir o inventÃ¡rio contra os objetos
Git originais, sem importar ou executar cÃ³digo do checkout:

```powershell
py -3.12 -I -S -B python/harness2_bridge/verify_pin.py .harness2-upstream
```

O inventÃ¡rio `upstream.lock.json` contÃ©m SHA-256 de todos os 39 arquivos Python
originais, calculados com `git show REVISION:path`, e da licenÃ§a completa,
preservada em `LICENSE.harness2`. HÃ¡ dois hashes explÃ­citos por fonte: bytes LF
originais e a transformaÃ§Ã£o exata LFâ†’CRLF usada pelo Git no Windows. NÃ£o hÃ¡
normalizaÃ§Ã£o arbitrÃ¡ria de cÃ³digo para fazÃª-lo passar. Arquivos ausentes,
fontes adicionais ou alteradas falham antes da importaÃ§Ã£o upstream. O worker
confere tambÃ©m `direct_url.json` e a licenÃ§a da distribuiÃ§Ã£o instalada.
Isso prova integridade em relaÃ§Ã£o ao inventÃ¡rio confiÃ¡vel; nÃ£o Ã© assinatura do
Google nem proteÃ§Ã£o contra um usuÃ¡rio que altere o worker e seu inventÃ¡rio.

Somente `harness2`, `harness2.core` e `harness2.core.tree` originais sÃ£o executados,
a partir de snapshots dos bytes verificados, evitando caches `.pyc` e extensÃµes
substitutas. Nenhum algoritmo Ã© substituÃ­do ou modificado. NÃ£o Ã© importado
`harness2.config` (que carrega `.env`), nem launchers, proposer, recursion,
selection, judge, subprocessos upstream ou modelos. Os demais fontes instalados
sÃ£o verificados, mas nÃ£o executados.

## API interna e protocolo v1

`internal/harness2bridge.New(Config)` recebe caminhos absolutos explÃ­citos do
Python da venv, worker fixo `worker.py`, site-packages e raiz privada de
scratch, alÃ©m de timeout positivo atÃ© 30 segundos. Caminhos/configuraÃ§Ã£o sÃ£o
inputs de deployment confiÃ¡vel; nunca vÃªm de manifesto, modelo ou navegador.
Um Client possui seu contexto, uma operaÃ§Ã£o em voo e `Close()` idempotente que
cancela operaÃ§Ãµes ativas/na fila e aguarda processos e limpeza. MÃºltiplos Clients
nÃ£o possuem quota global compartilhada. O aplicativo comum nÃ£o cria um Client.

Cada operaÃ§Ã£o inicia um worker novo, com `-I -S -B`, ambiente vazio, cwd temporÃ¡rio
novo e stdin/stdout privados. NÃ£o usa shell, PATH, HOME, dotenv, PYTHONPATH,
user site ou `.pth`. A entrada sÃ³ Ã© liberada depois do registro de ownership do
processo. NÃ£o hÃ¡ retry, restart automÃ¡tico ou serviÃ§o Python persistente.

Cada processo lÃª um documento JSON UTF-8 atÃ© EOF (mÃ¡ximo 96 KiB), retorna um
Ãºnico documento JSON seguido de newline e termina. Uma chamada = um processo:
`id: 1` Ã© correlacionado dentro desse processo, sem multiplexaÃ§Ã£o ou reutilizaÃ§Ã£o.
VersÃ£o e conjuntos de campos sÃ£o exatos; duplicatas, null, campos desconhecidos,
aliases por capitalizaÃ§Ã£o, valores concatenados e profundidade excessiva falham.
Python tambÃ©m recusa surrogates invÃ¡lidos, NaN/Infinity e booleanos como IDs.
Go compara a resposta ao schema fixo, aos valores/capacidades permitidos e Ã  revisÃ£o.

Request de health/capabilities:

```json
{"version":1,"id":1,"operation":"health","payload":{}}
```

`validate_manifest` usa `payload: {"manifest": <manifesto-v1>}`. A entrada passa
primeiro por `harness2.Decode` no Go (limite nativo de 64 KiB), preservando a
validaÃ§Ã£o autoritativa da Phase A. SuperfÃ­cies nÃ£o vazias alÃ©m de system prompt
sÃ£o recusadas antes do processo. Python valida a estrutura e chama a funÃ§Ã£o
**original** `harness2.core.tree.validate` com um `SurfaceSpec` DAIMON mÃ­nimo e
uma Ã¡rvore em memÃ³ria contendo apenas `systemprompt.md`. NÃ£o hÃ¡ materializaÃ§Ã£o
em disco, dereferenciamento de artefatos ou execuÃ§Ã£o de scripts.

Resposta de sucesso:

```json
{"version":1,"id":1,"operation":"health","ok":true,"code":"ok","result":{"revision":"1cfe254e8d25b15f37048c3730134686abe60335","operations":["health","capabilities","validate_manifest"],"manifest_valid":false,"tree_files":0,"execution":false,"os_sandbox":false}}
```

Na validaÃ§Ã£o bem-sucedida, `manifest_valid=true` e `tree_files=1`. Isso nÃ£o prova
que BotID existe, nÃ£o compara Bot Store, nÃ£o congela entrada de Sessions e nÃ£o
concede nenhuma permissÃ£o. VinculaÃ§Ã£o Ã  base continua responsabilidade dos
contratos Go da Phase A. ReferÃªncia a script ausente no prompt Ã© um exemplo de
rejeiÃ§Ã£o adicional produzida pelo validador upstream real, sem executar script.

Erros Python sÃ£o respostas fixas `invalid_request`, sem mensagens livres,
manifestos, paths ou tracebacks; exit status Ã© 2. Go recusa status nÃ£o zero como
`ErrWorker`, resposta malformada como `ErrProtocol`, excesso como `ErrLimit` e
falha de remoÃ§Ã£o/ownership como `ErrCleanup`. Timeout/cancelamento preservam
`context.DeadlineExceeded`/`context.Canceled`. Stderr Ã© descartado. ConteÃºdo
privado nÃ£o Ã© inserido em eventos, transcript, logs pÃºblicos ou erros Go.

## Lifecycle, recursos e limites de isolamento

O Go possui Start, kill, Wait, pipes e scratch. O tempo inclui fila, startup,
verificaÃ§Ã£o e operaÃ§Ã£o. Cancelamento mata o processo/grupo; `Close` aguarda todos
os calls. `WaitDelay` de um segundo limita pipes herdados. Toda operaÃ§Ã£o aguarda
o processo e tenta remover seu scratch; falha de cleanup Ã© explÃ­cita.

Windows: Job Object kill-on-close, limite de dois processos (redirector da venv
+ interpretador), 256 MiB de memÃ³ria comprometida total/por processo e cinco
segundos de CPU por processo. Se criaÃ§Ã£o/configuraÃ§Ã£o/associaÃ§Ã£o falha, o processo
Ã© morto e aguardado sem receber a requisiÃ§Ã£o. A associaÃ§Ã£o ocorre apÃ³s
CreateProcess: o bootstrap confiÃ¡vel pode iniciar antes do Job; nÃ£o se promete
confinamento desde a primeira instruÃ§Ã£o. Job nÃ£o restringe filesystem ou rede.

Linux: grupo de processos dedicado, SIGKILL no grupo em cancelamento/cleanup.
O worker configura limites hard/soft antes de carregar upstream: espaÃ§o virtual
256 MiB, CPU 5 s, tamanho de arquivo 0, 64 descritores e core dump desabilitado.
SÃ£o limites do processo, nÃ£o quotas globais de toda uma Ã¡rvore arbitrÃ¡ria.
Processos que escapassem de um grupo nÃ£o sÃ£o contidos por este mecanismo.
Outros sistemas operacionais falham na configuraÃ§Ã£o antes do spawn.

ApÃ³s imports verificados, um audit hook Python nega abertura de arquivos,
sockets, subprocessos, exec/spawn, vÃ¡rias mutaÃ§Ãµes de filesystem e carregamento
ctypes. Testes exercitam as negativas de escrita, socket e subprocesso. Isso Ã©
defesa adicional para **cÃ³digo fixo confiÃ¡vel**; um audit hook nÃ£o Ã© uma fronteira
de seguranÃ§a contra Python nativo/adversarial. NÃ£o hÃ¡ namespace, seccomp,
AppContainer, firewall de egress, ACL dedicada, quota de disco ou sandbox de
credenciais do usuÃ¡rio do SO. Python/stdlib/worker/inventÃ¡rio e processos do
mesmo usuÃ¡rio sÃ£o confiÃ¡veis. NÃ£o se promete resistÃªncia a adulteraÃ§Ã£o concorrente
do deployment, administradores, extensÃµes nativas ou vulnerabilidades do Python.

O worker nÃ£o recebe credenciais no ambiente e nÃ£o oferece operaÃ§Ã£o para ler
credenciais ou executar cÃ³digo. PorÃ©m, rodar sob o usuÃ¡rio do host nÃ£o elimina
sua autoridade de SO. `os_sandbox=false` e `execution=false` sÃ£o deliberados e
obrigatoriamente validados no Go. Esta ponte permite somente diagnÃ³stico e
validaÃ§Ã£o de dados com cÃ³digo auditado. O gate de isolamento completo registrado
na Phase A permanece aberto para qualquer engine, scripts, plugins, proposers ou
candidatos adversariais: **nÃ£o hÃ¡ entrada que os inicie nesta implementaÃ§Ã£o**.
NÃ£o se declara a execuÃ§Ã£o HarnessÂ² isolada pronta nem a Phase C liberada.

## ValidaÃ§Ã£o Windows/Linux

Os testes novos unitÃ¡rios de protocolo nÃ£o requerem Python. Testes reais da ponte
sÃ£o opt-in pelas duas variÃ¡veis `DAIMON_HARNESS2_TEST_*`; quando nÃ£o configuradas,
a dependÃªncia Python opcional nÃ£o Ã© imposta aos Bots convencionais. Na validaÃ§Ã£o
registrada abaixo elas foram configuradas: os testes reais nÃ£o foram pulados.
Fixtures de erro/lifecycle sÃ£o scripts controlados dos testes; o teste end-to-end
e a rejeiÃ§Ã£o semÃ¢ntica upstream usam a biblioteca original instalada, sem mocks.

A suÃ­te existente exige Linux para contratos de escrita/managed workspace,
permissÃµes Unix e symlinks. Nenhum teste anterior ou regra foi modificado.
Typecheck, 80 testes UI, build, vet e os testes reais da ponte sÃ£o suportados no
Windows. Os assets embutidos reconstruÃ­dos devem permanecer idÃªnticos.

Runner Linux opcional (build online; execuÃ§Ã£o offline, checkout somente leitura):

```powershell
docker build -t daimon-harness2-phase-b-test -f python/harness2_bridge/Dockerfile.test python/harness2_bridge
docker run --rm --network none --cpus 4 --memory 4g --pids-limit 512 --mount "type=bind,source=$((Get-Location).Path),target=/source,readonly" daimon-harness2-phase-b-test
```

O runner copia fontes para filesystem privado Linux, usa umask 022 e TMPDIR no
mesmo filesystem das fixtures. A venv Linux Ã© criada dentro da imagem, sem usar
a venv Windows montada. Docker aqui Ã© ambiente de teste, nÃ£o backend/sandbox do
produto. O Dockerfile fixa versÃµes de Go/Python/uv; tags de imagem e distribuiÃ§Ã£o
base ainda podem ser republicadas. Reprodutibilidade de dependÃªncia upstream vem
do SHA Git, inventÃ¡rio e uv.lock; nÃ£o se promete imagem byte-identical.

### Resultados desta implementaÃ§Ã£o

| CritÃ©rio/verificaÃ§Ã£o | EvidÃªncia e resultado |
| --- | --- |
| Branch separada | `feature/harness2-python-bridge` deriva de `7bb5053fab3e4fed6aff10f7ad06ff3320e8cf87`; branch Phase A permanece nesse SHA |
| Pin e licenÃ§a | `verify_pin.py`: PASS; 39 fontes originais + licenÃ§a Apache-2.0 contra objetos Git do SHA auditado |
| ComunicaÃ§Ã£o real | health, capabilities e manifesto percorreram Go â†’ Python 3.12 â†’ Go; PASS Windows e Linux |
| Validador original | ValidaÃ§Ã£o de prompt em memÃ³ria e rejeiÃ§Ã£o upstream de referÃªncia a script ausente; PASS, sem mocks upstream |
| Protocolo | Limites, duplicatas/case aliases/null/campos ausentes, versÃ£o, correlaÃ§Ã£o, respostas e entradas invÃ¡lidas; PASS |
| Lifecycle | Deadline, contexto cancelado, Close concorrente, crash, saÃ­da excessiva e scratch removido; PASS |
| Credenciais/efeitos | Ambiente sem secret de teste, ausÃªncia de import de config, audit hook nega escrita/socket/subprocesso; PASS |
| Recursos Windows | Job com memÃ³ria limitada: tentativa de alocaÃ§Ã£o de 400 MiB recusada; PASS |
| Recursos Linux | RLIMIT_AS/CPU/FSIZE conferidos e alocaÃ§Ã£o de 400 MiB recusada; PASS |
| Python multiplataforma | 8 testes PASS em Windows/Linux; mais 1 teste especÃ­fico Linux PASS (sem adaptar regras para Windows) |
| Go Windows â€” ponte final | `go test -race -count=1 ./internal/harness2bridge`: PASS, incluindo worker real e Job Object |
| Go Linux â€” suÃ­te completa | `go test -count=1 ./...`: PASS, inclusive contratos Linux/managedworkspace/symlinks |
| Go Linux â€” race completo | `go test -race -count=1 ./...`: PASS, sem alterar testes existentes |
| Vet e formato | `go vet ./...`: PASS Windows e Linux; `gofmt -l .`: sem saÃ­da |
| Demo offline | `go run ./cmd/daimon demo`: PASS Windows/Linux; completed, 2 passos/1 tool |
| UI | `npm ci`, typecheck, 80 testes em 15 arquivos e build: PASS; assets embutidos idÃªnticos, lockfile preservado |
| Diff | `git diff --check`: PASS; nenhuma alteraÃ§Ã£o em cÃ³digo/testes existentes do runtime |
| Isolamento adversarial | NÃƒO comprovado; sem sandbox de filesystem/rede do produto; gate mantido fechado para engine/candidatos |

As execuÃ§Ãµes completas iniciais no Windows **nÃ£o passaram integralmente**:
`TestLifetimeExpiresAndRevokes` falhou por prazo na suÃ­te normal e na race;
`TestCloudConcurrentReservationsBoundEffects` tambÃ©m falhou na race por prazo,
com arquivo em uso durante cleanup. Essas suÃ­tes estavam rodando em paralelo.
A repetiÃ§Ã£o direcionada dos dois testes com race passou sem alteraÃ§Ãµes.
NÃ£o se atribui a causa com certeza Ã  carga do host, e nÃ£o se apresenta aquela
execuÃ§Ã£o completa Windows como PASS. A validaÃ§Ã£o Linux completa e race passou.

A suÃ­te Linux completa usou uma cÃ³pia privada do cÃ³digo durante desenvolvimento;
as Ãºltimas mudanÃ§as locais na ponte e a separaÃ§Ã£o dos testes por plataforma
receberam adicionalmente vet/race/worker sobre uma cÃ³pia final nova. Nenhuma
suÃ­te antiga foi reduzida, reescrita ou desabilitada. Testes prÃ³prios de limites
Windows ficam em arquivo Go com build tag Windows; o teste Python de rlimit tem
arquivo separado que exige Linux explicitamente, sem contÃ¡-lo como PASS Windows.

Logs locais de desenvolvimento (nÃ£o versionados):
`D:/hyperframes-cache/temp/daimon-phase-b-go.log`,
`D:/hyperframes-cache/temp/daimon-phase-b-race.log`,
`D:/hyperframes-cache/temp/daimon-phase-b-linux.log`,
`D:/hyperframes-cache/temp/daimon-phase-b-linux-bridge-final.log` e
`D:/hyperframes-cache/temp/daimon-phase-b-docker-build.log`.

### InventÃ¡rio de alteraÃ§Ãµes

| Arquivo | MudanÃ§a |
| --- | --- |
| `.gitignore` | Ignora venv, checkout de auditoria e bytecode; mantÃ©m Dockerfile de teste versionÃ¡vel |
| `README.md` | Link e estado da ponte opcional |
| `docs/HARNESS2_INTEGRATION.md` | Distingue auditoria histÃ³rica A e estado B |
| `docs/HARNESS2_PYTHON_BRIDGE.md` | Novo: contrato, reproduÃ§Ã£o, riscos, resultados e inventÃ¡rio |
| `internal/harness2bridge/bridge.go` | Novo: cliente privado, protocolo, validaÃ§Ã£o nativa e lifecycle |
| `internal/harness2bridge/bridge_test.go` | Novo: protocolo, worker real, lifecycle e ambiente |
| `internal/harness2bridge/process_windows.go` | Novo: ownership e limites Job Object |
| `internal/harness2bridge/process_windows_test.go` | Novo: teste real do limite de memÃ³ria Windows |
| `internal/harness2bridge/process_linux.go` | Novo: ownership/kill do grupo |
| `internal/harness2bridge/process_other.go` | Novo: plataforma nÃ£o suportada falha fechada |
| `python/harness2_bridge/.dockerignore` | Novo: contexto Docker mÃ­nimo |
| `python/harness2_bridge/.gitattributes` | Novo: LF estÃ¡vel para fontes, locks e licenÃ§a |
| `python/harness2_bridge/Dockerfile.test` | Novo: ferramentas/dependÃªncia em ambiente Linux isolado |
| `python/harness2_bridge/LICENSE.harness2` | Novo: licenÃ§a upstream integral |
| `python/harness2_bridge/pyproject.toml` | Novo: dependÃªncia Git e requisitos de build fixos |
| `python/harness2_bridge/uv.lock` | Novo: resoluÃ§Ã£o reproduzÃ­vel da dependÃªncia |
| `python/harness2_bridge/upstream.lock.json` | Novo: inventÃ¡rio SHA-256 da revisÃ£o auditada |
| `python/harness2_bridge/verify_pin.py` | Novo: conferÃªncia independente contra objetos Git |
| `python/harness2_bridge/worker.py` | Novo: trÃªs operaÃ§Ãµes fixas e limites defensivos |
| `python/harness2_bridge/test_worker.py` | Novo: oito testes com a biblioteca real |
| `python/harness2_bridge/test_linux_limits.py` | Novo: teste Linux de limites hard |
| `python/harness2_bridge/test-linux.sh` | Novo: validaÃ§Ã£o offline completa em cÃ³pia Linux |

22 arquivos no corte: 3 existentes modificados e 19 novos. A primeira entrega
local foi feita sem commits ou publicaÃ§Ã£o. A consolidaÃ§Ã£o organiza commits
pequenos e permite apenas publicaÃ§Ã£o da branch B e PR draft contra a branch A,
conforme autorizaÃ§Ã£o posterior do usuÃ¡rio. NÃ£o hÃ¡ autorizaÃ§Ã£o para merge.
A branch da Phase A nÃ£o foi movida. Nenhum cÃ³digo de runtime
anterior, manifesto da Phase A, teste existente ou asset UI foi modificado.
InstalaÃ§Ãµes locais: venv Python 3.12 em `python/harness2_bridge/.venv`,
dependÃªncias UI em `ui/node_modules` e imagem Docker de testes. Docker Desktop
foi iniciado para permitir a validaÃ§Ã£o Linux. NÃ£o houve modelo pago, launcher
Codex/OpenCode, otimizaÃ§Ã£o, execuÃ§Ã£o de candidato ou avanÃ§o para Phase C.


## ConsolidaÃ§Ã£o para revisÃ£o draft â€” 2026-10-22

O escopo publicÃ¡vel Ã© a ponte opcional para cÃ³digo fixo confiÃ¡vel e validaÃ§Ã£o em
memÃ³ria. NÃ£o se declara homologado o gate de isolamento adversarial completo
registrado na Phase A. `execution=false`/`os_sandbox=false` permanecem obrigatÃ³rios;
nÃ£o existe operaÃ§Ã£o para iniciar engine, candidatos, modelos ou launchers externos.
A publicaÃ§Ã£o em draft nÃ£o libera Phase C nem altera a main.

### RevisÃ£o e correÃ§Ã£o

A leitura integral dos 22 arquivos encontrou uma falha em `gatedInput.Read`: o
copiador de stdin aguardava o sinal, mas nÃ£o consultava `allowed`. Assim, o
caminho de falha de containment poderia liberar o manifesto ao desbloquear o
copiador. `TestContainmentGateWithholdsRequest` reproduziu a falha antes da
correÃ§Ã£o. Agora a negativa retorna EOF sem entregar bytes; a liberaÃ§Ã£o positiva
continua entregando exatamente a requisiÃ§Ã£o. O teste passou com race no Windows
e Linux. NÃ£o foi alterado nenhum contrato da Phase A.

TambÃ©m foi acrescentado `TestActiveWorkerCancellationAndClose`: o worker
controlado dos testes confirma recebimento de stdin antes de o teste cancelar
ou chamar Close. Os dois caminhos verificam cancelamento, join e remoÃ§Ã£o do
scratch, com race nas duas plataformas. Na primeira versÃ£o desse teste, a
remoÃ§Ã£o prematura do marcador de readiness encontrou um handle ainda aberto
no Windows. A fixture passou a removÃª-lo apÃ³s o join do worker; nenhuma regra
ou teste anterior do produto foi modificado.

Os testes reais usam a biblioteca original; fixtures locais de erro e readiness
sÃ£o exclusivamente testes do transporte/lifecycle. Nenhum launcher Codex,
OpenCode, proposer, modelo pago ou otimizaÃ§Ã£o Ã© executado. A execuÃ§Ã£o Linux de
validaÃ§Ã£o tem `--network none`; os testes HTTP existentes usam servidores locais.

### RevalidaÃ§Ã£o da consolidaÃ§Ã£o

| VerificaÃ§Ã£o | Resultado |
| --- | --- |
| `verify_pin.py` contra checkout oficial limpo | PASS: SHA fixo, 39 fontes e licenÃ§a SHA-256 `cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30` |
| `uv sync --locked --project python/harness2_bridge --python 3.12` | PASS; somente ambiente isolado |
| Python Windows/Linux | PASS: 8 testes; Linux tem mais 1 teste hard-limit PASS |
| Ponte final Windows/Linux com race | PASS, incluindo controle de stdin e cancelamento de worker ativo |
| Go Windows completo, `go test -count=1 ./...` | PASS na consolidaÃ§Ã£o; os skips de plataforma existentes permanecem inalterados |
| Go Linux completo, `go test -count=1 ./...` | PASS, sem skips para contornar limitaÃ§Ãµes Windows |
| Go Linux completo com race | Resultado registrado apÃ³s a conclusÃ£o desta execuÃ§Ã£o |
| `go vet ./...`, `gofmt -l .` | PASS; nenhum arquivo sem formataÃ§Ã£o |
| UI typecheck / testes / build | PASS: 80 testes/15 arquivos; assets e package-lock idÃªnticos Ã  base |
| RevisÃ£o de arquivos/segredos | Somente os 22 arquivos do inventÃ¡rio; nenhum caminho de ambiente/cache/temporÃ¡rio ou formato reconhecÃ­vel de credencial encontrado |

As falhas de prazo Windows da implementaÃ§Ã£o inicial continuam registradas acima;
a execuÃ§Ã£o completa Windows desta consolidaÃ§Ã£o passou. NÃ£o se transforma o
resultado histÃ³rico em PASS. O Ãºnico teste RED proposital na revisÃ£o foi a
reproduÃ§Ã£o do defeito de stdin, corrigido antes da publicaÃ§Ã£o. A falha da nova
fixture de marcador tambÃ©m foi corrigida e revalidada separadamente.

Locks e inventÃ¡rio sÃ£o metadados reproduzÃ­veis deliberadamente versionados;
nenhuma dependÃªncia instalada, `.env`, virtualenv, cache, `.pyc`, node_modules,
log ou temporÃ¡rio integra os commits. A busca por formatos de credenciais nÃ£o
Ã© uma garantia universal de ausÃªncia de segredos; foi complementada pela leitura
integral dos arquivos. Os bytes e os caminhos do diff final sÃ£o conferidos antes
do push. O inventÃ¡rio/licenÃ§a upstream nÃ£o mudou nesta consolidaÃ§Ã£o.

EvidÃªncias locais desta revisÃ£o, nÃ£o versionadas:
`D:/hyperframes-cache/temp/daimon-phase-b-consolidation-windows.log`,
`D:/hyperframes-cache/temp/daimon-phase-b-consolidation-linux.log`,
`D:/hyperframes-cache/temp/daimon-phase-b-consolidation-bridge.log` e
`D:/hyperframes-cache/temp/daimon-phase-b-consolidation-linux-final.log`.
