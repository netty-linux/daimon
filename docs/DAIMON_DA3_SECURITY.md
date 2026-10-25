# DA-3 — Security & Dependency Automation

Base: PR draft #29, `bedab7b`. Escopo: auditoria de desenvolvimento em runners
descartáveis, sem root, serviços pagos, credenciais de produção, VM ou promoção
dos 18 gates. Não altera PR #28, código C2 ou versões das dependências do projeto.

## Scanners e insumos

| Trilha | Ferramenta fixada | Alvo e limites |
| --- | --- | --- |
| security-go | govulncheck v1.9.0 | Grafo Go Linux/GOARCH do runner, inclusive stdlib; análise source/symbol. Não executa a aplicação nem certifica outros GOOS. |
| security-python | pip-audit 2.10.1 | Requisitos PyPI de build da DA-2 e ferramentas DA-3, resolvidos e hash-pinned. O Git upstream não é confundido com um homônimo PyPI. |
| security-npm | npm 11.13.0 | package-lock UI, inclusive dependências de desenvolvimento; somente audit, sem install/fix do projeto. |
| security-workflows | zizmor 1.31.1 + política semântica PyYAML 6.0.3 | Tokens, permissões, pins, triggers e interpolação não confiável. Offline, sem config/ignores. Auditorias online de Actions ficam explicitamente NOT RUN. |
| security-provenance | Verificações determinísticas versionadas | Checkout limpo, lockfiles, digests npm, identidade das ferramentas e insumos inalterados; comparação entre jobs da DA-2/DA-3. |

Pins revisados em `automation/security-tools.json` e `security-tools.in`; todas as
31 dependências Python dos scanners estão em `security-tools.lock`, gerado com uv
0.12.17, versões exatas e hashes. A instalação exige wheels e `--require-hashes` em
venv nova. Inventário instalado precisa coincidir com o lock. Não existe atualização
automática do lock ou comando de correção de dependências.

O govulncheck é instalado por versão usando proxy oficial e Go sumdb habilitado;
o checksum de módulo fixado é conferido no build info. O tarball npm é baixado do
endereço fixado, conferido por SHA-512 antes da extração segura e usado somente no
diretório efêmero. Versões e hashes dos entrypoints ficam numa cápsula vinculada ao
SHA/run/tentativa. Nenhuma configuração global npm/Go do usuário é modificada.

Os scanners recebem ambiente reduzido, sem tokens/secrets. npm usa configurações
de usuário/global vazias no diretório temporário. Só metadados públicos de pacotes
são enviados aos serviços de advisories; Go faz a análise do código localmente.

## Aprovação e indisponibilidade

| Estado | Critério |
| --- | --- |
| PASS | Scanner terminou normalmente, schema/versão/insumos e resultado completos, nenhum achado dentro do escopo declarado. |
| FAIL | Advisory/achado conhecido de qualquer gravidade, violação de integridade/política, evidência contraditória ou adulterada. |
| BLOCKED | Scanner não instalado, timeout/falha de rede ou execução que impede concluir a análise. Contagem de achados é desconhecida, nunca zero. |
| NOT RUN | Componente não executado/artefato ausente ou auditoria deliberadamente fora do escopo (como regras online do zizmor). |
| UNVERIFIED | JSON incompleto mesmo com exit zero, dependência pulada ou cobertura não estabelecida (como a identidade VCS Python). |

Advisory encontrado exige revisão, mesmo sem gravidade ou correção publicada. Não
há allowlist, ignore-vuln, filtro por severidade, `--fix` ou `npm audit fix`. Uma
versão recomendada no relatório é dado para revisão, nunca comando executado.
O job de segurança e o agregador reprovam quando qualquer trilha obrigatória não
é PASS. Portanto **CI vermelho por vulnerabilidades reais é resultado esperado**,
não defeito do scanner a ser escondido. Os outros scanners continuam executando.

govulncheck JSON e zizmor SARIF podem retornar zero com findings: a decisão usa o
conteúdo, não apenas o exit code. pip-audit/npm têm seus códigos confrontados com
os registros e totais. Erro/JSON truncado não vira “nenhuma vulnerabilidade”.

## Normalização e dados duplicados

Cada registro conserva advisory, aliases, pacote, versão afetada, faixa quando
disponível, gravidade nativa (null se não fornecida), correção recomendada, URL,
descrição e contexto. Go distingue traces de módulo/pacote/símbolo; não chamar todo
achado de prova de exploit alcançável. npm conserva o caminho da dependência e
quando uma correção exige mudança de versão principal. Python não inventa CVSS.

As fontes reais podem repetir o mesmo advisory/trace. Repetições equivalentes são
normalizadas com contadores explícitos, sem inflar advisories; definições/fixes
contraditórios, dependências duplicadas e chaves JSON duplicadas reprovam. Relatórios
mostram advisories únicos e registros de finding separadamente. Metadados de
advisory são conteúdo não confiável: nunca são avaliados ou executados.

## Integridade e proveniência

Antes e depois das análises, registrar SHA-256 de go.mod, package-lock, pyproject,
uv.lock, upstream.lock, requisitos de build/scanners e workflows. Os digests de
arquivos/archives têm limites explícitos. A matriz compara os hashes Python/npm
efetivamente usados pela DA-2 e as versões Go/Node dos jobs com a cápsula auditada.
Não aceitar resultado de outro SHA, run, tentativa, escopo ou ambiente.

O zizmor apontou a chamada local legada `./.github/workflows/ci.yml`; a infraestrutura
adota a sintaxe recomendada `$/.github/workflows/ci.yml`, com regressão. Não foi
aplicada nenhuma correção automática de biblioteca. A política semântica também
recusa tokens ampliados, secrets herdados, triggers perigosos, checkout com
credenciais persistidas e interpolação de eventos/inputs em shell.

Hashes, sumdb, pins e verificação de metadados não são assinatura independente de
todos os produtores, attestation do runner/OS ou comprovação dos gates C2. As regras
online de Actions, bibliotecas do sistema, distribuição CPython e código Git não
publicado nos serviços de advisories têm limitações explícitas. A integridade Git
do upstream é verificada pela DA-2; a cobertura de vulnerabilidades dessa identidade
VCS continua UNVERIFIED. Zero findings conhecidos não significa ausência de falhas.

## Relatórios e verificabilidade

As cinco trilhas publicam bundles independentes com `report.json`, `summary.md`,
`raw.log`, `scan.json`, `scanner.stderr.log` e `manifest.json`. Incluem SHA, run ID,
tentativa, comandos, versões, cápsula, instantes UTC, duração monotônica e hashes.
O agregador reprocessa os dados brutos com o parser fixado e confronta o resumo;
recalcular o manifesto após adulterar um resumo não torna o resultado válido.

Limite de saída: 8 MiB por stream. Truncamento, encoding inválido ou timeout retém
evidência parcial e BLOCKED, nunca PASS. O timeout só encerra o filho direto e não
constitui prova de recuperação de VM/árvore adversarial. Artefatos ficam por um dia
no Actions; conservar a cópia local auditada e o resumo do run para revisão.

Testes determinísticos cobrem saída íntegra, incompleta, adulterada, duplicada e
contraditória; outages, schemas, versões, exit zero com finding, patches sugeridos,
VCS sem cobertura e divergências entre jobs. Validar no CI os scanners reais e
documentar o run e os achados, sem buscar verde mediante supressão ou upgrade.

## Fontes oficiais consultadas

- [govulncheck e seus exit codes JSON](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
- [pip-audit: modelo de segurança e JSON](https://github.com/pypa/pip-audit)
- [npm audit](https://docs.npmjs.com/cli/v11/commands/npm-audit/)
- [zizmor: modos, SARIF e limitações](https://docs.zizmor.sh/usage/)
- [GitHub: workflow reutilizável e self-repository](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows)

Sem serviço pago, GPU, instalação administrativa, credencial de produção, VM,
alteração de C2, desbloqueio de gates ou merge. PR #29 permanece draft.
