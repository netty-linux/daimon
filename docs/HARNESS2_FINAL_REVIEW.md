# Revisão técnica final — DAIMON × Harness²

Data: 2026-10-23. PRs: [#24](https://github.com/netty-linux/daimon/pull/24)
e [#25](https://github.com/netty-linux/daimon/pull/25).

## Revisões inspecionadas e conclusão

- PR #24: `7bb5053fab3e4fed6aff10f7ad06ff3320e8cf87`, base main
  `4c73fc1a5b94bacfbc2934e83bc7d67ff3ac7283`.
- PR #25 antes desta revisão: `607f76053f7ae6b3106c33db95a5417aa63c99db`,
  base na branch da Phase A. Ambos estavam abertos como draft e com CI existente
  verde no início da revisão.
- O escopo aprovado para avaliação é o worker fixo confiável de health,
  capabilities e validate_manifest. Não é execução isolada de candidatos.
- Não foi encontrado bloqueador no contrato da Phase A. Dois defeitos de
  lifecycle Windows pertencentes à Phase B foram reproduzidos e corrigidos.
  A homologação para código adversarial da Phase C continua bloqueada.
- Código da Phase A, main, Session runtime, providers, tools, policy e testes
  anteriores foram preservados. As correções estão somente na branch da ponte.

## Achados classificados

| ID | Classificação | Evidência / consequência | Correção ou proposta mínima |
| --- | --- | --- | --- |
| B1 | **Bloqueador para a Phase B — corrigido** | CREATE_SUSPENDED não era usado. Um teste segurou a janela Start→containment e o Python criou seu marcador antes do Job. stdin bloqueado não impede execução do bootstrap nem aplica retroativamente os limites. | Criar o processo suspenso, associar ao Job e só então retomar o thread primário. Recusar enumeração ambígua/erro e conferir o proprietário do handle de thread antes de ResumeThread. Teste RED antes e GREEN depois. |
| B2 | **Bloqueador para a Phase B — corrigido** | Release fechava o Job e retornava antes de o handle do interpretador filho sinalizar fim. O teste observou WAIT_TIMEOUT em três execuções após release. Isso não prova que o processo ainda consumia CPU, mas prova que seu encerramento final não havia sido aguardado. | Capturar handles SYNCHRONIZE dos membros atuais (até dois), terminar o Job e aguardar handles sinalizados e ActiveProcesses=0, com orçamento de cleanup de um segundo. Somente depois fechar o Job. Falha de cleanup continua explícita. |
| C1 | **Bloqueador somente para execução adversarial na Phase C — aberto** | Nenhuma fronteira de SO restringe filesystem/rede/credenciais do UID do host. Audit hooks e ambiente vazio não são sandbox. Linux aplica rlimits após começar o interpretador/stdlib confiáveis. | Antes de qualquer engine/candidato: perfil externo de isolamento iniciado antes do interpretador, runtime somente leitura, mounts mínimos, credenciais separadas, rede negada e quotas agregadas verificadas. Não implementado aqui. |
| C2 | **Bloqueador somente para execução adversarial na Phase C — aberto** | New valida existência/tipo/caminhos absolutos, mas não autentica Python, stdlib, worker ou o inventário carregado pelo próprio worker. Configuração/deployment e processos do mesmo usuário são confiáveis. | Para deployment adversarial: artefato imutável de runtime+worker+inventário, verificado por uma autoridade externa ao worker e protegido contra substituição, além de isolamento do SO. Hash dentro de um worker modificável não resolve a confiança. Não implementado. |
| C3 | **Bloqueador somente para execução adversarial na Phase C — aberto** | Setpgid prepara o grupo antes do exec no Linux; SIGKILL alcança filhos comuns. setsid/árvores arbitrárias e reaping de netos não são garantidos. Os rlimits não são orçamento agregado de uma árvore. | Cgroup/supervisor próprio e política de reaping/quota para toda a árvore, sem mudanças globais de subreaper no servidor Go nesta fase. O teste atual distingue término de reaping e não anuncia garantia falsa. |
| N1 | **Melhoria não bloqueante — defeito corrigido** | Deadline curto durante Start/containment retornava ErrWorker. TestStartupDeadlinePreservesContext reproduziu a perda de errors.Is(context.DeadlineExceeded). A chamada já falhava fechada. | Preservar ctx.Err nesses caminhos depois de matar/aguardar o processo. Se a contenção informar ErrCleanup, mantê-lo junto do contexto. |
| N2 | **Melhoria não bloqueante — proposta** | O CI existente só tem Go/Linux e UI; as variáveis dos testes reais da ponte não são configuradas. CI verde não comprova E2E Python nem comportamento Windows. | Job dedicado com Python/uv fixos e matriz Windows/Linux, sem transformar Python em requisito dos Bots convencionais. Não foi acrescentado novo workflow nesta revisão. Evidência real local está abaixo. |
| N3 | **Melhoria não bloqueante — corrigida** | Trechos do relatório anterior estavam com codificação UTF-8 interpretada como CP1252. | Corrigida somente a codificação do texto e atualizadas as garantias de lifecycle. Protocolo e hashes upstream não foram modificados. |

## Ordem de inicialização e ownership

### Windows

A criação usa CREATE_SUSPENDED. O processo ainda existe como objeto do SO,
mas o thread primário não executa o redirector da venv nem o worker antes do Job.
O código configura limites, associa o processo e identifica seu único thread
inicial com Toolhelp. OpenThread permite suspender/retomar e consultar o dono;
GetProcessIdOfThread confere o PID enquanto o handle está retido, evitando retomar
um ID de thread reciclado após cancelamento. ResumeThread precisa retornar
suspend count anterior igual a um. Ambiguidade/erro resulta em falha fechada.

A entrada privada só é liberada depois disso. Falhas antes da retomada deixam o
processo suspenso até sua morte/join. O teste de falha remove a comprovação do modo suspenso registrada no cmd após
Start; verifica recusa de retomada, término do processo próprio e ausência de
efeitos do worker. Não usa um PID já recolhido para simular a falha.

A liberação usa TerminateJobObject. A lista de membros é limitada pelo teto fixo
de dois processos: o redirector e o interpretador esperados na Phase B. Handles
capturados são usados apenas para esperar; terminação sempre se dirige ao Job,
nunca a um PID obtido de snapshot. Falhas de consulta/espera/fechamento resultam
em ErrCleanup; não são convertidas em sucesso. A liberação é idempotente.

Isso não é um supervisor de árvore adversarial: enumeração e terminação não são
uma operação atômica para código que cria/substitui filhos continuamente. Esse
comportamento não é disponibilizado pelo worker fixo e fica no gate da Phase C.
O teste com a venv real espera o handle do interpretador antes de aceitar release.

### Linux

SysProcAttr.Setpgid instala o grupo antes do exec. O teste cria um filho local
controlado, confere PGID igual ao PID do worker e verifica que o SIGKILL do grupo
impede sua continuação. Wait faz join do filho direto do Go. Um neto pode virar
zumbi e depender do reaper do sistema/container: o teste aceita desaparecimento
ou estado Z como prova de término, explicitamente **não** como prova de reaping.
O worker de produção não oferece fork/spawn nem executa subprocessos upstream.
Não há uso de cgroups, PID namespace ou subreaper no produto.

## Limites efetivos e confiança

| Recurso | Windows | Linux | Limitação relevante |
| --- | --- | --- | --- |
| Memória | 256 MiB de memória comprometida por processo e total do Job | RLIMIT_AS 256 MiB | AS não é RSS; não é limite de memória do servidor Go. Não anunciar o mesmo mecanismo nas duas plataformas. |
| CPU | 5 s de CPU em modo usuário por processo | RLIMIT_CPU 5 s por processo | Não é quota agregada da árvore. Windows não configura limite total de CPU de kernel pelo flag usado. |
| Processos | No máximo dois ativos simultaneamente no Job | Grupo de processos; sem RLIMIT_NPROC | Teto simultâneo não é número total de spawns durante a vida de uma árvore. O worker fixo nega spawn. |
| Filesystem | Sem ACL/AppContainer dedicados | Sem mount namespace/chroot; RLIMIT_FSIZE=0 e CORE=0 | FSIZE=0 não impede leituras, diretórios, unlink ou exfiltração por rede. Não é quota de disco/sandbox. |
| Rede | Sem firewall do produto | Sem firewall do produto | Audit hook nega sockets após imports; não protege contra runtime adversarial. --network none pertence ao runner de testes. |
| Descritores | Sem quota específica além do Job | RLIMIT_NOFILE=64 | Ambiente e stdout/stderr privados não substituem limites externos de SO. |
| Tempo | Contexto/timeout da chamada até 30 s | Igual | Cleanup pode adicionar WaitDelay de 1 s e, no Windows, orçamento de espera do Job de 1 s. Não há promessa de deadline rígido para syscall de kernel não interrompível. |
| Entrada/saída | Manifesto nativo 64 KiB; frame 96 KiB | Igual | Stderr descartado, stdout limitado; processo sem retries. |

O limite Windows entra antes do bootstrap Python. Não se promete cobrança
retroativa de toda alocação do SO durante CreateProcess: a [associação ao Job](https://learn.microsoft.com/en-us/windows/win32/api/jobapi2/nf-jobapi2-assignprocesstojobobject)
não reexamina operações de memória anteriores à associação. Os testes de
recursos conferem a configuração e a recusa de uma alocação de 400 MiB; não
medem precisão de CPU ou contabilização de kernel sob uma carga adversarial.

Os paths vêm de configuração Go explícita confiável, sem lookup pelo manifesto,
PATH, HOME ou browser. Caminhos absolutos e os.Stat não provam imutabilidade;
symlinks/junctions e ancestrais mutáveis não constituem uma fronteira de segurança.
Não se recusou arbitrariamente o symlink legítimo de Python em venv Linux.

Os 39 fontes upstream e a licença são verificados contra o inventário fixo antes
de compilar os três módulos originais usados. O worker compila snapshots dos
bytes já verificados, sem usar .pyc/extensões para substituí-los. A origem Git,
revisão e licença foram reconferidas. Isso verifica os fontes em relação ao
inventário confiável; não autentica o interpretador/stdlib/worker/inventário.
O manifesto não contém caminhos executáveis ou instruções para dereferenciar IDs.

## Entradas inválidas, dados sensíveis e compatibilidade A

Nenhuma alteração foi feita em internal/harness2. Decode/Validate/Freeze/Candidate
continuam com manifesto v1, campos exatos, limites por byte, Unicode estrito,
rejeição de null/duplicatas/aliases, snapshot por valor, binding ao hash do Bot
base e recusa das sete superfícies não materializáveis. O candidato segue somente
em memória e não altera Bot Store, Sessions ou permissões. Os testes originais
passaram com race; cobertura de statements permaneceu em 91,7% no Windows.

A ponte chama a validação Go antes do spawn, valida a resposta com revisão,
operação, ID e capacidades fixas e só entrega uma árvore de prompt em memória
a core.tree.validate original. Não há eval do prompt, execução de script,
Codex/OpenCode, modelo pago, engine ou API de otimização. Não houve avanço de fase.

Os testes de protocolo/Unicode/limites/tamper/error handling passaram. Erros
públicos são sentinelas ou contexto; detalhes de paths, exceções, manifestos,
credenciais e stderr não são devolvidos. A rejeição upstream de script ausente
não ecoa o nome privado no JSON de erro. Requisições não vão para SSE,
transcript, Memory ou logs do servidor. Isso não é secret scanning universal;
manifesto e hashes privados não devem ser logados deliberadamente pelo chamador.

## Evidências de testes

As execuções Python reais foram habilitadas explicitamente pelas variáveis
DAIMON_HARNESS2_TEST_PYTHON e DAIMON_HARNESS2_TEST_SITE. Não foram puladas para
simular sucesso. As fixtures de bootstrap/descendentes são programas de teste
locais controlados, não launchers de agentes externos. Nenhum teste existente
foi removido ou flexibilizado.

| Validação | Resultado |
| --- | --- |
| Bootstrap Windows, teste antes da correção | RED: Python executou antes de containment |
| Join Windows, três repetições antes da correção | RED: handle do interpretador retornou WAIT_TIMEOUT após release |
| Startup deadline antes da correção | RED: ErrWorker em vez de context.DeadlineExceeded |
| Bootstrap + join + cancelamento ativo após correção, race/repetição | PASS |
| Ponte Windows completa com race, cópia final | PASS |
| Startup com executável inválido e retomada recusada de processo suspenso | PASS; nenhum worker executado e scratch limpo |
| Grupo/filho comum Linux e ponte com race, cópia final | PASS; sem alegar reaping de netos |
| Phase A original com race/coverage | PASS; 91,7% statements no Windows |
| Python | PASS: 8 testes multiplataforma + 1 teste específico Linux |
| Go Linux completo normal e race, runner offline | PASS; demo também completou em dois passos/uma ferramenta |
| Go Windows completo | **FAIL** em TestLifetimeExpiresAndRevokes (sandbox/manager_test.go:294): prazo em Create |
| Repetição direcionada sandbox com race | PASS; código desse pacote e seus testes são idênticos à base |
| UI typecheck / 80 testes em 15 arquivos / build | PASS; assets sem alterações |
| go vet e gofmt | PASS |

A falha Windows é de teste existente em pacote não modificado; a mesma falha já
havia ocorrido em execução anterior à revisão. Não se atribui sua causa com
certeza à carga do host, nem se reescreve a execução completa Windows como PASS.
A classificação dos defeitos corrigidos se apoia nos testes RED/GREEN específicos,
não em inferência a partir desse timeout. O teste Linux não foi relaxado para Windows.

Logs locais de revisão, não versionados:
- D:/hyperframes-cache/temp/daimon-harness2-final-review-linux.log
- D:/hyperframes-cache/temp/daimon-harness2-final-review-linux-focused.log
- D:/hyperframes-cache/temp/daimon-harness2-final-review-windows-bridge.log
- D:/hyperframes-cache/temp/daimon-harness2-final-review-windows-all.log

## Referências primárias e escopo restante

CREATE_SUSPENDED mantém o thread primário suspenso até ResumeThread; a ordem de
startup adotada segue a [documentação de criação Win32](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags).
A consulta de estado/IDs usa [QueryInformationJobObject](https://learn.microsoft.com/en-us/windows/win32/api/jobapi2/nf-jobapi2-queryinformationjobobject).
A diferença entre CPU em modo usuário por processo e limite por Job segue
[JOBOBJECT_BASIC_LIMIT_INFORMATION](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_basic_limit_information).

Não foram implementados os itens C1/C2/C3 nem um novo workflow para N2. Eles
exigem um escopo explícito futuro. Não há autorização para merge/main/Phase C.


## Entrega da revisão

Correção de código e testes: `4d89d1caa2cac61e42f6364d3b664310f68b6dba`.

Arquivos tocados em relação ao SHA inicialmente revisado do PR #25:

- `internal/harness2bridge/bridge.go`
- `internal/harness2bridge/bridge_test.go`
- `internal/harness2bridge/process_windows.go`
- `internal/harness2bridge/process_windows_test.go`
- `internal/harness2bridge/process_linux_test.go` (novo)
- `docs/HARNESS2_PYTHON_BRIDGE.md`
- `docs/HARNESS2_FINAL_REVIEW.md` (novo)

O pacote alterado foi retestado com race sobre a cópia final após os ajustes;
a suíte completa Linux rodou durante a revisão e passou normal/race. A última
suíte completa Windows falhou no teste de sandbox descrito acima; isso não foi
omitido por um rerun. Nenhuma mudança de código depois dessas validações alterou
a semântica testada. Não foram incluídos logs, caches, ambientes Python ou
credenciais. Os PRs permanecem draft; sem merge ou alteração da main/Phase A.
