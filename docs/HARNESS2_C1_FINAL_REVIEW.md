# Revisão final C1 — PR #27

Auditoria de 2026-10-23, base `bedb0866988b0e72dae4f533a630c856de88efb5`,
HEAD inicial `7efa9c21347d41ad8eacc347baf9e1cd85d203ed`.
Escopo: todo o diff C1, contratos A utilizados, loader/worker B, interface e
validador originais, testes, documentos e workflow. Não houve execução de
candidatos, modelos, launchers, Sessions ou API paga. Nenhum merge foi realizado.

## Resultado técnico e classificação

Foi comprovada e corrigida uma exposição acidental na formatação de diagnóstico:
`fmt.Sprintf("%#v", plan/evidence)` ignorava String e mostrava campos privados.
Regressão RED reproduziu o vazamento sem imprimir seu conteúdo; GoString agora
devolve a mesma etiqueta privada. A correção não cria uma fronteira contra
reflection/código Go hostil nem altera os acessos privados explícitos.
Commits da revisão: `2003b68` (cobertura) e `f4571ec` (redação de diagnóstico).

| Achado | Classificação | Evidência / tratamento |
| --- | --- | --- |
| Formatação %#v expunha prompt/JSON privado | Bloqueador C1 corrigido | GoString em Plan/Evidence; regressão RED/GREEN, sem alterar snapshot A ou runtime |
| Faltava exercitar expiração durante o probe, além de cancelamento e deadline já vencido | Melhoria não bloqueante C1 | Novos testes com timers reais de context e relógio virtual synctest cobrem prazo interno e pai menor, ausência de evidência parcial e recuperação |
| Cobertura de ownership não exercitava os dois perfis ponteiro do Bot, nem limite exato da evidência | Melhoria não bloqueante C1 | Mutações em ComputerProfile/SandboxProfile retornados não alteram a baseline; mutação da baseline invalida o binding; limite exato aceita e um byte menor recusa sem parcial |
| Preview Python precisava de observação explícita de ausência de I/O | Melhoria não bloqueante C1 | Audit hook de teste observa eventos reais sem substituir o validador; preview e recusas não abrem arquivos, sockets ou subprocessos; SurfaceSpec original é frozen |
| CI existente cobre Go/Linux e UI, mas não Windows/Python/E2E real | Melhoria não bloqueante C1 | Evidências locais são separadas dos checks remotos; ampliar matriz é trabalho posterior, não uma alegação de cobertura do CI atual |
| Builds offline da CLI falharam nas duas primeiras suítes Windows sob cargas concorrentes | Melhoria não bloqueante C1 / limitação em testes existentes | Testes e CLI não foram alterados por C1, e CLI não importa harness2; registrar falhas e repetir serialmente, sem afirmar causa raiz ou estabilidade sob qualquer carga |
| Deployment Python, stdlib, worker e inventário continuam confiáveis; paths podem mudar por escritor externo | Bloqueador C2 | Verificação dos bytes upstream não atesta interpretador/stdlib/deployment nem exclui corridas de filesystem; exigir deployment imutável e autoridade externa antes de execução |
| Não há isolamento adversarial de filesystem, rede ou credenciais | Bloqueador C2 | C1 Go opera em memória; loader Python lê deployment confiável. Audit hook/Job/rlimits não constituem sandbox de SO |
| Ownership, quotas agregadas, reaping e recuperação adversarial não são garantidos | Bloqueador C2 | B usa Job Windows e process group Linux para worker confiável; Linux permite evasão por código adversarial. C1 não cria processos ou instalações |
| Evidências contêm hashes correlacionáveis, sujeitos a dicionário, sem autenticidade criptográfica | Bloqueador C2 para divulgação/uso como autorização | API privada deliberada; sem logs, SSE, store ou promoção automática. Projetar acesso, retenção e integridade antes de exposição; SHA não é assinatura nem prova de desempenho |

## Fronteiras verificadas

Plan contém somente strings privadas do Snapshot A. Decode/Freeze serializam
dados próprios; Manifest, Tree, Candidate e Evidence.JSON entregam cópias.
Clone copia Tools e ambos os perfis, cujos campos são valores escalares.
Alterações sequenciais dos inputs após Prepare não alteram o plano; Candidate
revalida a baseline fornecida em cada chamada. **O chamador deve possuir os inputs
durante a chamada**: não é permitido mutar simultaneamente bytes do manifesto,
Tools ou perfis da baseline. O plano concluído admite leitores concorrentes;
isso não torna uma baseline externa mutável segura para escrita concorrente.

Probes têm duas fixtures fechadas, prompt de até 8192 bytes, evidência limitada
a 2048 bytes e deadline cooperativo de um segundo ou prazo menor do pai.
Não há goroutine de trabalho abandonada, callback de modelo, tarefa livre,
filesystem, rede, store ou Session. O prazo não é preempção: uma operação
cooperativa suspensa só retorna ao próximo checkpoint. Testes synctest pausam
um checkpoint por tempo virtual para demonstrar recusa após expiração, sem
alegar uma quota rígida de latência/CPU. Falhas não publicam evidência parcial.

Todos os 18 gates são descrições novas por chamada, sempre unverified; não são
uma autoridade. RequireRealExecution recusa incondicionalmente após validar
contexto. Manifestos usam schema A estrito: não há campo de ativação. Fixtures
não recebem gates, modelos, comandos ou callbacks; não existe endpoint C1.
Alterar a cópia retornada dos gates não muda o estado de execução.

Python carrega os contratos originais verificados de core.tree e substrate.base;
não carrega config, core.spec, substrate.__init__, codex ou opencode. O teste
confere Protocol original, assinaturas e defaults. materialise, execute,
read_trajectory e preflight levantam ExecutionUnavailable antes de acessar
paths ou instalação. Preview aceita somente dict exato de um prompt str, valida
UTF-8/bytes e usa validate original com allow_subagents=False. Não fabrica Install.
O loader altera sys.modules e exige processo dedicado: não promete reload
concorrente, isolamento contra código Python no mesmo processo ou sandbox.
Os dois defeitos Windows antigos não foram reintroduzidos: B ainda cria o
processo suspenso, associa ao Job antes de resumir e espera o encerramento dos
membros no cleanup. Linux mantém Setpgid antes de exec e kill do grupo; isso
não contém um futuro processo adversarial que use setsid. C1 não altera esses
arquivos ou adquire subprocessos próprios.

Nenhum hash/contador indica qualidade: structural_only=true,
performance_evaluated=false e real_execution=false. O caso compartilhado com
script ausente é estruturalmente válido no Go e recusado pelo upstream; essa
diferença explícita não pode ser interpretada como aprovação de execução.

## Validação e ambiente

Código corrigido: `f4571ecaf48a9219c61224af97a9a26f820368de`.
Ambientes: Go 1.27.1, Python 3.12.10 isolado da B, imagem Linux
`sha256:bbbdfac62f6851c8191f46b39d3404febe84a3fa80f9426dc6bf5bd9303d619a`.
As duas variáveis DAIMON_HARNESS2_TEST_PYTHON/SITE foram configuradas nas
execuções locais Go: validação real e E2E não foram substituídos por mocks.

| Validação desta revisão | Resultado |
| --- | --- |
| Regressão de formatação antes da correção | RED: TestReviewDebugFormattingKeepsEvidencePrivate falhou, sem imprimir dados privados |
| C1 completo Windows após correção, race/count=10 | PASS, 13,114 s, incluindo casos Go → Python → Go |
| Python Windows | PASS: B 8 testes; C1 6 testes contra upstream real |
| Integridade upstream e licença | PASS: 39 fontes, commit fixado e Apache-2.0; checkout oficial limpo |
| UI Windows | PASS: typecheck, 80 testes/15 arquivos, build; assets versionados idênticos |
| Vet Windows, gofmt e diff check após correção | PASS |
| Primeiro runner Linux, snapshot inicial da revisão | PASS: Go completo normal/race, B 8 + C1 5 + limites 1 Python, vet e demo offline |
| Windows completo inicial com race | FAIL: TestPlanScopeExecutableNoToolsFirstResponse, build offline status 1 sem stderr, 133,85 s; A/B/C1/sandbox PASS; sem aviso de data race |
| Windows completo inicial sem race | FAIL: TestManagedPreimageExecutableLifecycle, build offline status 1 sem stderr, 120,90 s; A/B/C1/sandbox PASS |
| Windows completo corrigido, sem race, -p 1 | PASS: CLI 152,244 s; A 0,913 s; B 4,231 s; C1 1,422 s; sandbox 6,583 s |
| Windows completo corrigido, com race, -p 1 | PASS: CLI 172,676 s; A 1,977 s; B 5,352 s; C1 3,402 s; sandbox 8,156 s; normal=0/race=0 |
| Linux completo corrigido normal / race, UID 65534, 2 CPUs | PASS / PASS: C1 0,468 / 1,996 s; B 0,872 / 1,986 s; sandbox 4,483 / 5,941 s; managedworkspace 183,056 / 174,983 s |
| Python Linux corrigido e vet/demo | PASS: B 8 + C1 6 + limites Linux 1; vet completo e demo completed, 2 passos/1 ferramenta |
| CI push/PR do código corrigido | PASS: Go/Linux e UI em ambos os eventos, runs 39978747491/39978752738 |

Os FAILs Windows ocorreram durante cargas concorrentes de build/teste no host.
Ambos os testes usam build offline com contexto de dois minutos e cache novo.
As durações são compatíveis com expiração sob contenção, mas o erro capturado
não comprova sozinho a causa; não houve instrumentação que autorize conclusão
mais forte. Os testes são idênticos à main. Não foram alterados, pulados ou
tiveram prazos aumentados. A repetição usa `go test -p 1 -count=1 ./...` e
`go test -p 1 -race -count=1 ./...`, serializando pacotes e preservando os testes
concorrentes internos e race detector. Um PASS posterior não apaga os FAILs
nem demonstra estabilidade ilimitada sob carga.

Os testes e guardas existentes foram preservados. Os FAILs históricos de noexec
e NO_XDEV continuam em HARNESS2_NATIVE_SUBSTRATE_C1.md; não foram apagados ou
convertidos em PASS. O runner correto usa /work no overlay raiz, UID 65534,
fonte read-only e network=none. Não se adicionaram exceções às guardas.
Nesta revisão o primeiro runner usou 4 CPUs; a repetição do código corrigido
usa 2 CPUs, com os mesmos 4 GiB e teto de 512 processos. Esses são limites do
ambiente de validação, não capacidades de sandbox do produto.

Logs locais não versionados em `D:/hyperframes-cache/temp/`:
`daimon-c1-review-windows-normal.log`, `daimon-c1-review-windows-race.log`,
`daimon-c1-review-windows-serial-normal.log`,
`daimon-c1-review-windows-serial-race.log`, `daimon-c1-review-linux.log`,
`daimon-c1-review-linux-final.log` e `daimon-c1-review-regressions.log`.

O GitHub informou PR draft, sem reviews registradas, base main preservada e
sem conflitos. A consulta de proteção de main respondeu "Branch not protected"
e a lista de rulesets estava vazia. Isso não substitui revisão humana nem
autoriza merge nesta tarefa. O workflow continua sem executar Python real.

Arquivos alterados pela revisão: `internal/harness2substrate/substrate.go`
(GoString), `internal/harness2substrate/review_test.go` (regressões),
`python/harness2_native_substrate/test_substrate.py` (observação de I/O e
imutabilidade), este relatório e o link em HARNESS2_NATIVE_SUBSTRATE_C1.md.
Nenhum arquivo de ambiente, credencial, cache, dependência instalada ou log
integra o diff. Main, A/B, runtime, policy, Sessions e UI foram preservados.

## Recomendação e entrada em C2

Merge de C1 é tecnicamente recomendável somente com os checks do HEAD final
aprovados e revisão humana conforme a política do mantenedor. Não é homologação
de execução. O PR permanece draft; esta tarefa não realiza merge.

Antes de C2: definir escopo e threat model por SO; prover deployment imutável,
filesystem e egress sob autoridade externa, separação de credenciais, quotas
agregadas, ownership e kill/join/reaping comprovados, limpeza após cancelamento
e crash; manter workspace/transcript privados e validar candidato congelado
sem conectar automaticamente a Sessions. Testes adversariais só após autorização
e backend de isolamento apropriado. Gates não podem ser satisfeitos por
manifestos, evidências de fixtures ou autoatestação do modelo. Recursão e
Experience Library continuam fora do escopo.
