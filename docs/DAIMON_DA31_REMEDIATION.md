# DA-3.1 — remediação controlada

Base auditada: `4a648db81e2cc0f58528d4e710c09a7d9559a932`, PR #29.
Branch: `fix/da3-1-security-remediation`; destino do PR draft:
`feature/daimon-development-automation`. Sem merge.

## Causas e mudanças

| Componente | Antes | Depois | Motivo |
| --- | --- | --- | --- |
| Go / stdlib | 1.27.1 | 1.27.3 | 13 advisories corrigidos em 1.27.2; GO-2026-6809 exige 1.27.3 |
| setuptools | 80.9.0 | 84.1.0 | PYSEC-2026-3447 e GHSA-xmj3-9gf7-h52w |
| wheel | 0.45.1 | 0.46.2 | CVE-2026-24049 / GHSA-8rrh-rw8j-w5fx |
| packaging (build) | não instalado explicitamente | 26.3 | Dependência `packaging>=24.0` introduzida por wheel; mesma versão já auditada no lock das ferramentas |

Go 1.27.3 é estável oficial, publicado em 2026-10-19. Os 14 achados são na
biblioteca padrão; não há módulo Go terceiro a atualizar. `go.mod` governa o Go
de todos os jobs. As referências Go nos runners Docker opcionais e exemplos do
README acompanham o mínimo novo para não quebrar comandos com GOTOOLCHAIN=local.
Esses runners Docker continuam NOT RUN; não foram executados neste trabalho.

setuptools e wheel são ferramentas de construção do upstream Python, não uma
troca do upstream. As versões PyPI selecionadas não estão retiradas (yanked),
suportam Python 3.12 e têm wheels oficiais. Os SHA-256 em
`automation/build-requirements.txt` foram regenerados a partir do JSON oficial
do PyPI e confrontados com os bytes baixados antes do uso. O Go Windows ZIP
usado localmente também foi conferido contra o SHA-256 do catálogo oficial.
Essas verificações comprovam correspondência ao registro oficial via HTTPS,
não uma atestação independente do produtor.

`uv lock` 0.12.17 regenerou `uv.lock`; somente as três build constraints mudaram.
O commit Harness² `1cfe254e8d25b15f37048c3730134686abe60335`, o inventário de
fontes e sua licença continuam os mesmos. Node, npm, Playwright, scanners,
permissões, políticas e testes não foram alterados. Nenhuma allowlist foi criada.

## Reprodução e comparação

O [run histórico 40239742452, tentativa 1](https://github.com/netty-linux/daimon/actions/runs/40239742452)
permanece FAIL. Seu SHA sintético de teste é
`371644be726e84d4ace9e9bc42f347fcfa17c06c`, cujo segundo pai é o HEAD auditado.
`DAIMON_DA31_BASELINE.json` preserva os 17 IDs, versões, fixes, aliases, identidade
da execução e hashes dos relatórios e resultados brutos históricos. Não é uma
allowlist nem é consumido pelos scanners ou por sua decisão de aprovação.

Uma nova execução local de govulncheck 1.9.0 e pip-audit 2.10.1 sobre o checkout
antigo reproduziu exatamente os mesmos 14 e 3 advisories, sem novos IDs.
Cada registro foi consultado novamente no banco Go ou OSV, sem registros
retirados. A reprodução é de findings do scanner; não foram executados exploits.

Com as versões novas, os mesmos scanners locais retornaram zero findings.
Go ficou PASS e Python UNVERIFIED, por cobertura VCS não comprovada. Os 84 testes
determinísticos da automação e oito testes do worker real passaram localmente,
sem skips. O upstream foi reconstruído sem cache com setuptools/wheel novos.
O resultado local não substitui as provas Linux, race e Playwright no Actions.

O PR de remediação deve registrar o SHA efetivamente testado, HEAD, run ID,
tentativa, resultados de todos os jobs e comparação com o baseline. Os artefatos
existentes incluem logs, resultados brutos, hashes, versões e matriz; sua retenção
no Actions é de um dia. Preservar a cópia auditada local antes da expiração.
Importar evidências exige hash, identidade, escopo e reprocessamento semântico
válidos. Comparar também os nomes dos skips, não apenas o exit code.

## Critérios e bloqueio de cobertura conhecido

- Cada um dos 17 IDs deve estar ausente de uma varredura completa nas versões
  corrigidas; IDs novos, falhas e saídas incompletas continuam visíveis.
- Go, Python real, UI, Playwright, automação, integridade e análise de workflows
  devem executar. Sem skips novos; o teste Go de plataforma já não aplicável no
  Linux permanece explicitamente UNVERIFIED.
- O parser DA-3 preserva `security-python=UNVERIFIED` mesmo com zero findings:
  PyPI não comprova vulnerabilidades do código obtido por Git. Não consultar um
  pacote PyPI homônimo nem substituir a revisão Git por um nome de distribuição.
- Pela política existente, esse escopo obrigatório não PASS mantém os jobs de
  segurança/relatório vermelhos. Zero findings não é autorização para mudar a
  política ou alegar cobertura completa.
- Menor encaminhamento seguro: manter o bloqueio e criar uma validação específica
  de segurança do commit upstream, com revisão de código/dependências e critérios
  de cobertura verificáveis. Uma declaração humana genérica ou hashes de arquivos
  não bastam para remover o bloqueio. Isso exige trabalho próprio, fora de uma
  remediação limitada a versões/locks; nenhum prazo ou PASS é presumido.

Também permanecem fora da cobertura: OS/CPython, auditorias online de Actions,
atestação integral da imagem hospedada, testes Docker opcionais e prova operacional
C2. Os bancos de vulnerabilidades mudam; preservar os dados brutos desta execução
permite auditar a comparação sem prometer resultados futuros idênticos.

PR #28, código C2 e os 18 gates UNVERIFIED preservados. Nenhuma VM, serviço pago,
credencial de produção, operação privilegiada ou instalação global necessária.

## Fontes oficiais

- [Go: versões estáveis e correções](https://go.dev/doc/devel/release)
- [Go: catálogo e SHA-256 dos arquivos](https://go.dev/dl/?mode=json)
- [Setuptools: correção 84.1.0](https://github.com/pypa/setuptools/security/advisories/GHSA-xmj3-9gf7-h52w)
- [Setuptools: correção 83.0.0 incorporada](https://github.com/pypa/setuptools/security/advisories/GHSA-h35f-9h28-mq5c)
- [Wheel: correção 0.46.2](https://github.com/pypa/wheel/security/advisories/GHSA-8rrh-rw8j-w5fx)
- [PyPI setuptools 84.1.0](https://pypi.org/pypi/setuptools/84.1.0/json)
- [PyPI wheel 0.46.2](https://pypi.org/pypi/wheel/0.46.2/json)
- [PyPI packaging 26.3](https://pypi.org/pypi/packaging/26.3/json)

Os URLs individuais dos 17 registros e seus hashes estão no baseline JSON.
