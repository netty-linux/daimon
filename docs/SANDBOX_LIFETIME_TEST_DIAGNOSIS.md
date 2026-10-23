# Sandbox lifetime: diagnóstico de teste Windows

Data: 2026-10-23. Base: `4c73fc1a5b94bacfbc2934e83bc7d67ff3ac7283`.
Comparação Harness² Phase B: `d4ccdfba417681c59e9a58b1a9508e9f87aa4e1e`.

## Reprodução e causa comprovada

Antes de alterar código, o teste original foi executado na main em branch local
de diagnóstico e na Phase B. `internal/sandbox` e `internal/computer` são
idênticos nas duas revisões. Não há dependência da integração Harness².

- Main, teste isolado com race, 30 repetições: PASS (34,653 s no pacote;
  aproximadamente 1,08–1,12 s por teste).
- Phase B, teste isolado com race, 30 repetições: PASS (34,635 s).
- Phase B, teste isolado sem race, 30 repetições: PASS (33,580 s).
- Main, suíte completa com race, original: FAIL em Create, linha 294,
  `context deadline exceeded`; teste durou 4,79 s.
- Main, suíte completa com race e overlay temporário de diagnóstico: FAIL
  no mesmo teste, `sandbox_canceled`; teste durou 3,93 s.

Na segunda execução completa, medições monotônicas por etapa registraram:

| Etapa | Tempo da etapa | Lifetime expirado ao sair |
| --- | --- | --- |
| Admissão + persistência inicial | 3,5327639 s | Sim |
| backend.Create | 6,9005 ms | Sim |
| Computer | 913,7 µs | Sim |
| RegisterComputer | 58,5 µs | Sim |
| Retorno após cleanup | 3,9252979 s desde o início | Sim |

A persistência inicial consumiu o prazo de um segundo antes do backend.
`saveJournal` realiza escrita, Sync, Close e rename reais. As medições não
isolam o custo de cada syscall nem demonstram a causa da latência de disco.
Não se atribui a causa exclusivamente ao race detector, antivírus ou CPU.
O teste isolado passa com race; a execução completa concorrente entre pacotes
reproduz a falha. Não houve relatório de data race nessas execuções.

O overlay modificou somente a cópia compilada de manager.go, fora do repositório,
para registrar duração e estado de expiração por etapa, sem IDs, conteúdo ou
caminhos de recursos. Ele não integra o commit nem as validações finais.

## Semântica preservada

`context.WithTimeout(ctx, Lifetime)` começa antes da admissão e persistência
da intenção. Portanto, o lifetime inclui o provisionamento; não começa quando
Create retorna. O orçamento `CreateTimeout` deriva desse contexto e limita
backend/Computer/persistência running ao menor prazo aplicável. Ele não estende
o lifetime. A intenção inicial usa o contexto do chamador; I/O síncrono não é
interrompido retroativamente quando o lifetime vence.

A fixture fake não verifica ctx quando não há gate: pode retornar um remote
depois da expiração. O Manager confere o lifetime e recusa o lease. O defer
cancela, fecha ready e faz cleanup com contexto separado e DeleteTimeout.
Esse orçamento não autoriza o uso do lease expirado. A falha observada é
fail-closed, não perda de cancelamento ou sucesso após expiração.

Após sucesso, o timer de cleanup e o timer de WithTimeout vencem no mesmo
instante. O primeiro pode chamar cancel antes do segundo, resultando em
context.Canceled em vez de DeadlineExceeded. Uma versão inicial do novo teste
exigia equivocadamente DeadlineExceeded nesse caso e falhou na repetição;
a asserção final exige o instante correto, contexto encerrado e revogação.
Nos caminhos de Create bloqueado, DeadlineExceeded continua sendo exigido.

## Correção limitada ao teste

`TestLifetimeExpiresAndRevokes` usa testing/synctest da biblioteca padrão:
I/O real continua sendo executado, mas não consome o relógio virtual do teste.
O teste avança deliberadamente o relógio até um nanossegundo antes do prazo e
confirma que o Computer ainda está disponível; no prazo exato, espera o callback
com synctest.Wait e exige contexto encerrado, estado deleted, cleanup complete,
catálogo revogado, backend vazio e journal persistido.

`TestCreateDeadlineCleansPartialResource` bloqueia a fixture por canais após
criar um recurso parcial. Exercita separadamente Lifetime e CreateTimeout;
confere o primeiro prazo exato, DeadlineExceeded, lease nil, revogação e limpeza
persistida. Não aumenta timeouts nem usa sleeps reais para sincronização.

Nenhuma alteração de produção, autorização, isolamento, API ou dependência.
Nenhum teste anterior foi removido ou desabilitado. Os PRs #24/#25 e a main
permanecem intactos. Não há implementação da Phase C ou merge nesta tarefa.

## Reprodução dos comandos

```powershell
go test -race -run '^TestLifetimeExpiresAndRevokes$' -count=30 -v ./internal/sandbox
go test -race -run '^(TestLifetimeExpiresAndRevokes|TestCreateDeadlineCleansPartialResource)$' -count=30 -v ./internal/sandbox
go test -race -count=1 ./...
go test -count=1 ./...
go vet ./...
gofmt -l .
git diff --check
```

Logs locais não versionados ficam em `D:/hyperframes-cache/temp/`, com prefixo
`daimon-lifetime-`. Os resultados finais de validação são registrados abaixo.

## Validações da correção

| Verificação | Resultado |
| --- | --- |
| Testes corrigidos com race, count=30 | PASS, 30 execuções de cada cenário; 125,512 s sob carga concorrente |
| Windows, suíte completa final com race | PASS; sandbox 47,791 s, cmd/daimon 206,257 s |
| Windows, suíte completa final sem race | PASS; sandbox 58,367 s, cmd/daimon 194,436 s |
| Windows, Phase A/B com race e worker real | PASS; bridge 5,693 s, incluindo E2E, bootstrap/Job, cancelamento e cleanup |
| Linux, suíte completa com race sobre Phase B + correção de teste | PASS; sandbox 7,337 s, bridge 2,828 s; managedworkspace/symlinks mantidos |
| Linux, Python real | PASS: 8 testes multiplataforma + 1 teste hard-limit |
| Linux, go vet ./... e demo offline | PASS; demo completed, 2 passos, 1 ferramenta |
| go vet ./... Windows | PASS |
| gofmt -l . e git diff --check | PASS, nenhuma saída |
| Controle negativo do teste de revogação | RED esperado: cleanup automático atrasado por overlay resulta em expiry cleanup incomplete |

A suíte Windows exploratória com a primeira versão das asserções também passou,
mas a evidência final acima foi produzida novamente depois do ajuste da asserção
de context.Canceled/DeadlineExceeded. A falha da asserção inicial no count=30
está preservada em `daimon-lifetime-fixed-race30.log`; a repetição final está em
`daimon-lifetime-fixed-race30-final.log`. Nenhuma falha foi convertida em PASS.

As suítes completas finais Windows rodaram sobre a branch de bugfix baseada na
main. Para a compatibilidade A/B e Linux, um git archive do SHA Phase B indicado
acima recebeu somente o mesmo manager_test.go corrigido (SHA-256 local
`cb1850ace51b8dda5722a3f3b8dce75b40b6c1600fd715d501be9f8f571d3de9`).
Essa cópia não é uma alteração de branch/PR, nem adiciona dependência Harness²
ao bugfix. As variáveis DAIMON_HARNESS2_TEST_PYTHON/SITE foram configuradas
explicitamente, impedindo os skips opcionais de worker.

Linux usa imagem de testes existente
`sha256:bbbdfac62f6851c8191f46b39d3404febe84a3fa80f9426dc6bf5bd9303d619a`,
Go 1.27.1, Python 3.12.10, rede negada, 4 CPUs, 4 GiB e limite de 512 processos.
A fonte é montada somente leitura e copiada para filesystem privado; TMPDIR
fica nesse filesystem e umask é 022. A venv da imagem é isolada da instalação
global e da venv Windows. Docker é apenas ambiente de testes, não novo backend
de isolamento do produto. UI não foi alterada nem revalidada nesta tarefa.

Logs finais: `daimon-lifetime-fixed-final-windows-race.log`,
`daimon-lifetime-fixed-final-windows-normal.log`,
`daimon-lifetime-phase-ab-windows-race.log` e
`daimon-lifetime-fixed-linux-race.log`.

## Condições para retomar a consolidação

A reprodução conhecida foi corrigida no teste; as repetições e suítes acima
passaram. Isso demonstra estabilidade nos cenários executados, não prova ausência
universal de defeitos ou prazo rígido para syscalls. Nenhum timeout de produção
foi alterado. O custo específico de cada syscall de persistência pode ser objeto
de perfilamento futuro, sem afrouxar a política fail-closed.

O bugfix deve receber revisão própria e aprovação dos workflows do PR separado.
Sua integração na main exige uma operação futura autorizada; esta tarefa não
faz merge. Depois disso, revalidar os PRs #24/#25 contra o estado atualizado,
na ordem A → B e com todos os gates de segurança existentes. A correção não
antecipa liberação de isolamento adversarial nem qualquer parte da Phase C.
