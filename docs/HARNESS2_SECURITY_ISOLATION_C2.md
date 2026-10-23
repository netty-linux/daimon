# Harness² C2 — inspeção de pré-requisitos; execução bloqueada

## Situação e base

**Entrega parcial, C2 bloqueada. Não existe executor de fixtures nesta entrega.**
Main foi atualizada por fast-forward para
`76a81cff335d54480f3e9ef42ca60b6459d34dfc`, checkout limpo; #26, #24, #25 e
#27 são ancestrais. Branch `feature/harness2-security-isolation-c2` criada dessa
base. Nenhuma branch anterior foi sobrescrita. A disponibilidade foi investigada
antes de alterar código; matriz e plano foram apresentados antes da implementação.

Host Windows; ambiente Linux descartável Docker Desktop/WSL2, kernel
`6.18.54.1-microsoft-standard-WSL2`. Runtimes anunciados pelo daemon:
`io.containerd.runc.v2`, `nvidia`, `runc`. Não há gVisor configurado. O container
de diagnóstico não tem runsc/bwrap nos caminhos pesquisados; cgroup v2 está
montado `ro,nosuid,nodev,noexec,relatime,nsdelegate`. A tentativa inofensiva
`unshare --user --map-root-user --net true` foi recusada com EPERM. Isso demonstra
a restrição daquele container, **não** ausência de suporte no kernel inteiro.
`nsdelegate` não equivale a uma delegação gravável para o supervisor.

Não foi instalado runtime global, reiniciado daemon, relaxado seccomp ou
concedido modo privileged. Runc não foi adotado como substituto silencioso de
gVisor. Não houve chamada Fleet, login, modelo pago ou Session real.

## Auditoria dos mecanismos existentes

Lidos AGENTS, README e documentos Harness², SANDBOX_COMPUTERS_V1,
CLOUD_COMPUTERS_V1 e managed-workspace; inspecionados sandbox CUA/command/limits,
regras de lifecycle e contratos de isolamento já revisados em A/B/C1.

| Recurso | Reutilização possível | Insuficiência para C2 |
| --- | --- | --- |
| SandboxManager | Padrão de intenção antes de provisionar, ownership exato, cleanup pendente e reconciliação | Domínio ligado ao Computer/Session, não executor de experimentos; não foi instanciado |
| CUA local gVisor | Runtime externo existente, quando instalado e explicitamente validado | Perfil outbound alcança serviços locais; disk flag não comprova quota; imagem não atestada externamente |
| Fleet | Contratos de ownership e lifecycle como referência | Cloud paga, rede outbound e garantias insuficientes para este escopo offline; não utilizada |
| Managed Workspaces | Limites de importação, cópias e validação de topologia | Mesmo UID confiável, sem namespace/chroot; NO_XDEV não isola código adversarial |
| Ponte B | Pin upstream, frames limitados, cancelamento do worker confiável | Audit hook, rlimits, process group e Job não garantem isolamento adversarial completo |
| C1 | Planos imutáveis, evidências estruturais e 18 requisitos | Fixtures não atestam SO; RequireRealExecution permanece ErrUnavailable |

Plano incremental: auditar plataforma e deployment; registrar pré-requisitos sem
autoridade de execução; validar a recusa e regressões; publicar diagnóstico e
lacunas. Implementar supervisor e fixtures somente depois de disponibilizar e
provar os controles obrigatórios, em nova revisão ainda dentro do escopo C2.
Não se entrega um executor especulativo que não possa ser testado.

## Modelo de ameaças

| Fronteira / ameaça | Controle exigido | Confiança e situação |
| --- | --- | --- |
| Host/kernel comprometido | Base de confiança administrada externamente | Confiáveis; escape de kernel/hipervisor fora de garantia desta inspeção |
| Supervisor substituído ou morto | Deployment protegido, ownership durável, watchdog externo, kill/join/reaping e recuperação | Ausentes; processo Go/CLI isolado não substitui supervisor externo |
| Runtime/interpretador/dependências alterados | Artefato imutável autenticado externamente e mounts somente leitura | Ausente; presença ou SHA local não autentica deployment |
| Fixture/candidato futuro hostil | Código sem autoridade, rede, credenciais ou mounts controláveis | Nenhum executado; nenhum input de modelo aceito |
| Leitura/escrita de arquivos do usuário | Raiz mínima, allowlist de mounts, identidade sem acesso ao host, traversal/symlinks recusados | Não comprovado; nenhum workspace real aberto ou montado para fixtures |
| Exfiltração de secrets | Credenciais separadas, ambiente limpo, sem arquivos/sockets de serviço | Não comprovado; diagnóstico não lê ambiente, secrets ou sockets |
| Localhost/DNS/metadata/serviços internos | Deny-by-default externo ao processo, sem rede compartilhada | Não comprovado; network none do runner de testes não homologa produto |
| Fork/CPU/memória/escrita sem limite | Cgroup delegado por execução, quotas agregadas, disco/tmpfs limitado e prazo externo | Não aplicado pela entrega; detectar cpu/memory/pids não comprova enforcement |
| Dispositivos/capabilities/namespace escape | Dispositivos mínimos, capabilities negadas, política de syscalls e runtime validado | Não comprovado para fixtures |

Os diretórios de código/testes montados no runner são somente para compilar e
testar código confiável do projeto. Eles **não** são a raiz mínima futura de
fixtures adversariais. Não há compartilhamento com transcripts ou stores de Sessions.

## Componente entregue e limites de integridade

`internal/harness2isolation.Inspect(ctx)` lê somente `/proc/self/status`,
metadados de `/sys/fs/cgroup` e presença de arquivo executável regular em
`/usr/local/bin/runsc` ou `/usr/bin/runsc`. Não pesquisa PATH nem executa esses
arquivos. Uma instalação em outro caminho não é descoberta; symlink não conta
como arquivo regular observado. O operador não pode fornecer caminhos por esta API.

As leituras têm teto de 64 KiB, fechamento explícito e verificações cooperativas
de contexto. Erros não expõem path, conteúdo bruto, UID/PID ou ambiente.
O snapshot tem cópia por valor, JSON próprio limitado e String/GoString privados.
Windows retorna ErrUnsupported sem inspeção Linux. Não há goroutine de trabalho,
launcher, opção de comando, credencial, policy, quota, mount ou método Run.

`cmd/harness2-isolation-audit` não aceita argumentos. Em Linux imprime JSON v1
com observações e `fixture_status=NOT_RUN`; termina sempre com exit 3. No
Windows/erro também recusa com exit 3, sem apresentar observações como sucesso.
Exit 0 **não é fornecido**, mesmo se os arquivos procurados existirem. A API
Inspect retornar nil significa apenas que os metadados foram lidos, nunca readiness.
O motivo é um supervisor validado ainda indisponível nesta implementação.

O diagnóstico confia em seu binário, kernel/procfs/sysfs e deployment administrado.
Não é um atestador resistente ao administrador ou mesmo UID hostil. Seccomp=2 não
prova qual filtro está ativo; UID não zero não prova separação de credenciais;
mount gravável não prova delegação; runtime presente não prova integridade.

## Mapeamento dos 18 gates

Nenhum gate foi modificado ou autoatestou verified. Estado de todos: **unverified**.

| Gate C1 | Evidência C2 / lacuna |
| --- | --- |
| filesystem-isolation | NOT RUN: raiz mínima e mounts de fixtures ausentes |
| network-egress-policy | NOT RUN: sem teste contra endpoints sintéticos isolados |
| credential-separation | NOT RUN: identidade e sentinelas de credenciais ainda não provisionadas |
| interpreter-integrity | NOT RUN: deployment externo não autenticado |
| worker-integrity | NOT RUN: inventário B não autentica deployment C2 |
| dependency-integrity | NOT RUN: sem autoridade externa de dependências C2 |
| deployment-integrity | NOT RUN: controle externo ausente |
| process-tree-ownership | NOT RUN: supervisor e reaping não implementados |
| cpu-quota | NOT RUN: controller observado não é quota testada |
| memory-quota | NOT RUN: idem, sem exaustão controlada |
| disk-quota | NOT RUN: sem filesystem temporário limitado por execução |
| time-quota | NOT RUN: deadline do diagnóstico não é prazo de fixture |
| process-quota | NOT RUN: sem teste de limite agregado |
| private-workspace | NOT RUN: sem workspace de fixture criado |
| private-transcript | NOT RUN: nenhuma trajetória/transcript C2 |
| supervision | NOT RUN: sem watchdog/ownership externo |
| cancellation-cleanup | NOT RUN: cancelamento da inspeção não prova término de descendentes |
| failure-recovery | NOT RUN: sem supervisor para matar e reconciliar |

## Fixtures, cleanup e recuperação

Todos os ensaios adversariais solicitados estão **NOT RUN**: leitura fora da
raiz; escrita fora do workspace; localhost/rede/DNS/metadata; ambiente,
arquivos e sockets de credenciais sintéticas; descendentes; exaustão CPU,
memória, disco e processos; cancelamento; morte forçada do supervisor;
cleanup/recuperação; symlinks, caminhos relativos e traversal.

Não foram criadas fixtures executáveis nem testes vazios que simulassem essas
provas. Falta o backend obrigatório; rodá-las no host ou num runtime alternativo
não revisado violaria a condição de execução isolada. Os testes unitários de
parser/snapshot não contam como PASS de isolamento. Nenhuma credencial verdadeira
ou serviço externo foi acessado. Não há recursos de fixture a limpar; o diagnóstico
fecha seus arquivos. Cancelamento não promete preempção de syscall de leitura.
Os containers confiáveis de validação são descartados com --rm.

Para implementar a próxima parcela C2: Linux descartável com runtime apropriado,
supervisor sob autoridade separada, cgroup delegado, mounts privados e quotas
reais de disco; depois fixtures com sentinelas/endpoints sintéticos, observação
externa das recusas, kill do supervisor e comprovação de remoção dos recursos
exatos. Não apagar recursos por prefixo nem usar process group como ownership.

## Validação

PASS de inspeção significa somente leitura/rejeição corretas. SKIP/NOT RUN não
satisfazem security gates. Não houve alteração de testes/timeouts existentes.
Falhas Windows históricas dos builds CLI continuam documentadas no relatório
C1; nenhuma repetição posterior as transforma retroativamente em PASS.

| Validação | Resultado |
| --- | --- |
| Disponibilidade para isolamento obrigatório | FAIL: sem gVisor configurado, cgroup somente leitura e criação de namespaces recusada no container avaliado |
| Unitários Windows do diagnóstico e C1, com race | PASS: erro de plataforma, cancelamento prévio, cópias, JSON limitado e gates preservados |
| Diagnóstico Windows compilado | PASS de recusa: exit 3, nenhuma inspeção/fixture Linux |
| Python real Windows | PASS: B 8 testes e C1 6; sem mocks |
| Integridade upstream | PASS: 39 fontes originais e Apache-2.0, revisão 1cfe254e8d25b15f37048c3730134686abe60335 |
| UI | PASS: typecheck, 80 testes/15 arquivos, build; assets idênticos |
| Go Windows completo sem race, -p 1 | PASS: CLI 174,565 s; A 0,951 s; B 4,096 s; inspeção 0,878 s; C1 1,524 s; sandbox 7,159 s |
| Go Windows completo com race, -p 1 | PASS: CLI 151,859 s; A 2,122 s; B 5,603 s; inspeção 1,713 s; C1 2,383 s; sandbox 7,655 s |
| Go Linux completo normal / race, UID 65534 | PASS / PASS: inspeção 0,006 / 1,058 s; B 2,099 / 2,778 s; C1 0,344 / 1,905 s; sandbox 4,065 / 6,030 s |
| Python Linux | PASS: B 8, C1 6 e limites Linux 1 |
| Vet Linux e demo offline | PASS: demo completed, 2 passos, 1 ferramenta |
| Vet Windows, gofmt e diff check | PASS: normal=0/race=0/vet=0; nenhuma formatação pendente |
| Diagnóstico Linux compilado | PASS de inspeção/recusa: JSON abaixo e exit 3; não é PASS de isolamento |
| Fixtures adversariais, quotas/cleanup/kill do supervisor | NOT RUN: infraestrutura obrigatória não disponível; sem fallback |
| Inspeção de kernel Linux no Windows | NOT RUN por plataforma; stub de recusa testado |

Observação real do runner Linux: cgroup_v2=true, cgroup_mount_read_only=true,
cpu_controller=true, memory_controller=true, pids_controller=true,
no_new_privileges=false, seccomp_filter=true, non_root_identity=true,
runtime_file_observed=false. O no_new_privileges=false também impede qualquer
alegação de que esse runner de regressão constitui o perfil C2. Status emitido:
`fixture_status=NOT_RUN`, motivo `validated-supervisor-unavailable`.

Comandos Windows (sem ensaio adversarial):

```powershell
$env:DAIMON_HARNESS2_TEST_PYTHON = (Resolve-Path python/harness2_bridge/.venv/Scripts/python.exe).Path
$env:DAIMON_HARNESS2_TEST_SITE = (Resolve-Path python/harness2_bridge/.venv/Lib/site-packages).Path
& $env:DAIMON_HARNESS2_TEST_PYTHON -I -S -B python/harness2_bridge/test_worker.py
& $env:DAIMON_HARNESS2_TEST_PYTHON -I -S -B python/harness2_native_substrate/test_substrate.py
py -3.12 -I -S -B python/harness2_bridge/verify_pin.py .harness2-upstream
go test -p 1 -count=1 ./...
go test -p 1 -race -count=1 ./...
go vet ./...
gofmt -l .
git diff --check
# Em ui: npm run typecheck; npm test; npm run build
```

Runner Linux de regressão confiável, com imagem B já instalada, sem novos extras
ou dependências globais. Código `7295b77`, Go 1.27.1 e Python 3.12.10; a venv B
existente também é usada no Windows. O runner novo chama a suíte B/C1 existente,
com ambas as variáveis do worker real, depois compila e executa o diagnóstico.
Só o exit 3 esperado permite considerar a inspeção recusada corretamente.
O script não executa fixtures e rejeita UID 0 antes dos testes.

```powershell
docker run --rm --env HOME=/work --env GOCACHE=/work/go-cache --network none --cpus 2 --memory 4g --pids-limit 512 --mount "type=bind,source=$((Get-Location).Path),target=/source,readonly" daimon-harness2-phase-b-test:latest bash -c 'chown 65534:65534 /work && exec setpriv --reuid=65534 --regid=65534 --clear-groups bash /source/scripts/harness2-isolation-audit-linux.sh'
```

Imagem local:
`sha256:bbbdfac62f6851c8191f46b39d3404febe84a3fa80f9426dc6bf5bd9303d619a`.
Os limites acima protegem o ambiente de validação confiável; não são uma
implementação de quotas por fixture. /work usa o overlay raiz do container,
preservando as guardas NO_XDEV e testes não-root existentes. Nenhuma exceção
a noexec/NO_XDEV foi criada. A fonte montada no host não recebe chown/escritas.

Diagnóstico separado de disponibilidade, executado antes do código, sem montar
arquivos do host: container read-only, network none, cap-drop ALL e
no-new-privileges; `command -v runsc`, `command -v bwrap`,
`findmnt -no OPTIONS /sys/fs/cgroup`, `unshare --user --map-root-user --net true`.
Resultado: runtimes ausentes, cgroup somente leitura, unshare EPERM. Isso é
evidência de indisponibilidade no ambiente, não um PASS das barreiras C2.

Logs locais não versionados em `D:/hyperframes-cache/temp/`:
`daimon-c2-prerequisites.log`, `daimon-c2-linux.log`,
`daimon-c2-windows-normal.log`, `daimon-c2-windows-race.log` e
`daimon-c2-windows-vet.log`. Nenhum ambiente, cache, dependência instalada,
arquivo temporário, credencial ou binário integra os commits.

## Critérios de entrada em C3

C2 ainda não concluída. Antes de propor C3: comprovar todos os controles
obrigatórios com testes reais de SO e deployment autenticado externamente;
revisar relatórios e limitações por plataforma; definir contrato de autoridade
separado e autorização humana explícita. Nem o relatório nem uma flag
isolation_verified podem habilitar execução. RequireRealExecution continua
ErrUnavailable. Não há Propose–Probe–Compose, Experience Library ou C3.
