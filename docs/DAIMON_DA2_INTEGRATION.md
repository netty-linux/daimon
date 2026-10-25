# DA-2 — Python real, browser e importação do CI

Escopo: PR draft #29, branch `feature/daimon-development-automation`, a partir de
`c0758c1`. Sem alteração do PR #28, código C2, VM ou dos 18 gates UNVERIFIED.

## Auditoria e decisão

- Worker B: `test_worker.py` já possui oito testes reais de protocolo, integridade,
  validação upstream e recusa de operações. O adapter C1 possui seis testes Python;
  `test_linux_limits.py` verifica RLIMIT em um filho próprio, sem root. Não são
  candidatos reais ou prova de isolamento operacional.
- A integração Go já usa `DAIMON_HARNESS2_TEST_PYTHON/SITE`; sem isso seus testes
  reais são pulados. A receita nova configura ambos antes dos testes existentes.
- `smoke.mjs` e `memory-smoke.mjs` já usam servidor/browser reais com providers fake
  em loopback. São executados sem modificar esses scripts. Os smokes restantes
  dependem de Docker e continuam NOT RUN em linha separada da matriz.
- A repetição push+PR e uma segunda execução completa dos jobs existentes foram
  evitadas: `ci.yml` vira workflow reutilizável, chamado uma vez pelo orquestrador.
  Não há consulta privilegiada ou transferência entre workflows/runs diferentes.

## Receita Python reproduzível

Runner Linux descartável, não root; Python 3.12.10 e uv 0.12.17 seguem a receita
existente, sem instalação de serviço. Atualizar esses pins exige revisão separada;
não são declaração de homologação de segurança do ambiente.

1. Checkout upstream no commit `1cfe254e8d25b15f37048c3730134686abe60335`.
2. `verify_pin.py` confere inventário de fontes e licença **antes** do build/import.
3. Criação de venv nova e ferramentas setuptools 80.9.0/wheel 0.45.1 instaladas
   como wheels por `--require-hashes`, conforme `automation/build-requirements.txt`.
4. `uv sync --locked --inexact --no-build-isolation --no-managed-python` usa o
   `uv.lock` original. `--inexact` conserva somente o tooling de build já fixado na
   venv recém-criada, sem resolver versões novas. Não altera os arquivos da ponte.
5. Executar as três suítes existentes com `-I -S -B`, registrando testes e skips em
   JSON. Worker/adaptador conferem novamente os arquivos instalados e proveniência.
6. Exportar Python/site apenas para os próximos passos do mesmo job. Rodar o Go
   completo e race uma vez cada, exigindo evidência positiva dos testes reais da
   ponte, incluindo cancelamento, lifetime e descendentes ordinários Linux.

O pin Git e os hashes identificam conteúdo; não constituem assinatura independente
do produtor. Nenhuma autenticação pendente de gVisor/ESU é promovida por esses testes.

## Browser e CI existente

O job UI mantém `npm ci`, Vitest, typecheck/build e diff do bundle embutido. A
typecheck já está dentro do script de build; evita-se repeti-la separadamente.
Vitest gera JSON com contagens e nomes de casos pulados; resultado vazio, falha ou
assertions incompletas não recebem PASS.

Após esse job passar, o Playwright instalado pelo package-lock baixa somente o
Chromium correspondente. Não há `--with-deps`, apt, sudo, Docker ou browser de uma
sessão do usuário. Se as bibliotecas do runner forem insuficientes, o teste falha;
não instalar dependências privilegiadas para contornar. Executar os dois smokes
existentes exige seus marcadores finais, assertivas, zero pedidos externos do
browser e providers fake sem Authorization. Registrar versão Playwright, revisão
do browser e hash do package-lock. Não há chamada a API paga.

## Evidência e importação

Cada componente do CI existente é convertido em bundle, não reexecutado por um
"importador". Os jobs reutilizáveis herdam o mesmo `GITHUB_RUN_ID`, tentativa e
checkout SHA do orquestrador. A agregação valida esses campos, escopo do inventário,
schema, hashes, tamanhos e resultado estruturado reconstruído do log.

Novos componentes importados: `linux-existing-ci`, `ui-existing-ci`,
`python-upstream-integration`, `browser-integration`. Cada etapa conserva comando,
exit code, tempo monotônico decorrido, contagem e skips. Logs detalhados ficam em
`stage-NN.log.gz`, limitados a 32 MiB descomprimidos por etapa e 8 MiB por anexo;
há manifesto SHA-256/tamanho. JSON Vitest também é preservado. Não há execução de
conteúdo baixado, token de escrita, secrets ou promoção automática de autoridade.

PASS exige execução positiva. FAIL, BLOCKED, NOT RUN e UNVERIFIED continuam
distintos. Skips nas integrações obrigatórias impedem PASS e reprovam o job.
O Go existente contém um teste propositalmente não aplicável ao Linux:
`TestManagedPreimageUnsupportedBeforeEffects`. Esse skip é listado com razão em
test/race e conserva a cobertura Go como UNVERIFIED; somente essa ausência conhecida
pode manter o job legado verde. Skip da ponte real nunca é permitido. A matriz
operacional continua BLOCKED e os 18 gates UNVERIFIED.

## Validação e limitações

Validação local inicial: 52 testes da automação aprovados, mais 14 testes existentes
do worker/adaptador Python realmente executados em venv nova no Windows. Os limites
Linux e browser Linux não são inferidos desse resultado local; devem passar no CI.
Não iniciar WSL/VM para conseguir esse resultado. A documentação de resultado final
do PR deve incluir run/tentativa, SHA, contagens, hashes e limitações observadas.

O workflow agora só é acionado pelo orquestrador (PR, push main ou manual). Nomes
de checks reutilizáveis podem ganhar prefixo; revisar regras de branch protection
antes de eventual merge, sem alterar essas regras nesta tarefa. A mudança de CI
pode requerer resolução de conflito futura com o PR #28; esse PR não é alterado aqui.
Sem merge automático ou instalação persistente no host. Artefatos conservam retenção
de um dia; a cópia local auditada e o resumo versionado preservam o histórico útil.

Fontes: [uv locked sync](https://docs.astral.sh/uv/concepts/projects/sync/),
[Playwright browsers](https://playwright.dev/docs/browsers),
[GitHub jobs/needs](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-jobs).
