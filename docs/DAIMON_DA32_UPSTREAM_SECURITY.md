# DA-3.2 — verificação estática do upstream Git Harness²

Base: PR #30, `3b8bc8fb944978c080527080b7406c6b39e603c0`.
Nova branch `feature/da3-2-upstream-security`, PR draft destinado a
`fix/da3-1-security-remediation`. Upstream não atualizado:
`https://github.com/google-research/harness2`, commit
`1cfe254e8d25b15f37048c3730134686abe60335`.

## Aquisição existente e fronteiras executadas

O job DA-2 clona o upstream sem checkout, destaca o commit exato e executa
`verify_pin.py` antes do build. Esse verificador confere a lista de todos os
arquivos Python do pacote, os blobs Git e a licença. Ele não inventariava todo
o repositório nem todo o conteúdo lido pelo backend de construção.

O build usa setuptools, wheel e packaging fixados com hashes; uv instala o mesmo
commit sem build isolation. O backend pode executar lógica de construção: essa
superfície permanece restrita ao job Linux descartável existente. A nova análise
DA-3.2 não instala, constrói, importa ou executa o upstream.

O worker usa Python 3.12 `-I -S -B`, verifica inventário, hashes e direct_url antes
dos imports e executa snapshots dos três módulos originais abaixo. O audit hook
é instalado depois desses imports; não é uma sandbox e não protege a etapa de
importação. O adaptador C1 carrega um quarto módulo após conferir o hash. Seu
namespace inerte evita carregar os launchers de `substrate/__init__.py`.

| Arquivo original | Uso real |
| --- | --- |
| `harness2/__init__.py` | Namespace do pacote, carregado pela ponte |
| `harness2/core/__init__.py` | Namespace core, carregado pela ponte |
| `harness2/core/tree.py` | `SurfaceSpec` e `validate`, em memória; funções de filesystem também existem no módulo carregado |
| `harness2/substrate/base.py` | Contratos originais, carregados pelo adaptador C1 e testes existentes |

Os hashes do worker, do adaptador e do verificador estão vinculados ao inventário
independente. Mudança nesses limites sem revisão falha fechado. O relatório AST
lista cada import e cada arquivo como runtime-loaded, installed-not-loaded ou
repository-not-installed. Módulo carregado não significa que todas as suas funções
foram chamadas: não há alegação de alcance interprocedural. Os entrypoints CLI
instalados pelo pacote não são invocados pela ponte ou por esta análise.

## Inventário reproduzível e aquisição independente

`automation/upstream-inventory.lock.json` contém 114 entradas da árvore Git,
modos e object IDs. Para 80 arquivos de código/configuração/build/licença, contém
também tamanho e SHA-256 do conteúdo. Inclui todos os 58 arquivos Python, inclusive
scripts, testes e third_party. Os demais 34 blobs, como mídia e documentação,
permanecem identificados pelo object ID Git, sem alegar SHA-256 de bytes não lidos.

`upstream_security.py` inicia um repositório bare efêmero e faz fetch somente da
origem HTTPS e do commit aprovados, com objetos pequenos. Desabilita configuração
Git global/sistema, helpers de credenciais, prompts e hooks; não faz checkout,
submodule update ou LFS. Rejeita symlinks, submódulos, caminhos inseguros, arquivos
textuais maiores que 1 MiB, inventário adicional e divergências de conteúdo.

Cada conteúdo analisado é preservado como base64 no resultado, com SHA-256 e
identidade de blob conferidos. O agregador revalida origem/commit/árvore, inventário,
conteúdo e fronteiras e recalcula a análise AST a partir desses bytes. Compara o
commit com o worker real em outro job. Isso fornece uma aquisição e verificação
independentes do build DA-2, não uma assinatura do mantenedor ou prova contra
comprometimento da origem ou do runner.

Para reproduzir: usar um checkout limpo desta branch, Python 3.12, os locks de
ferramentas e a receita `automation/security_setup.py` seguida por
`automation/security.py` em Linux descartável sem root. O Actions fornece essa
receita. A inspeção usa apenas objetos de texto e parsers; nunca importa o alvo.
Os conteúdos, hashes e resultados permanecem no bundle `security-upstream`.

## Ferramentas e regras

Bandit **1.9.4** (PyCQA, Apache-2.0) é fixado no lock Python com hashes. Instalação
somente de wheels; novos transitivos fixados: stevedore 5.9.1 e colorama 0.4.6.
Os pins anteriores foram conservados. pip-audit audita também esses pacotes junto
das dependências de build/scanners existentes. Sem serviço pago ou credenciais.

Bandit executa de fato sobre a lista exata de 58 arquivos, com `--ignore-nosec`,
configuração própria vazia de exclusões, sem baseline, skips ou filtros de
severidade/confiança. O processo recebe Python `-I -B`; o diretório de trabalho
contém somente os arquivos Python materializados como dados. Nenhum código do
alvo é importado pelo scanner.

Regras AST suplementares, versionadas em `upstream_checks.py`, registram:

- DA32-PROCESS: subprocess, os.exec/spawn/system/popen/fork, inclusive aliases de import;
- DA32-FILESYSTEM: leitura/escrita, criação, remoção, rename, permissões, shutil/tempfile;
- DA32-DESERIALIZE: pickle/dill/marshal e YAML inseguro;
- DA32-JSON-INPUT: desserialização JSON como fronteira de dados, sem alegar execução de código;
- DA32-DYNAMIC-CODE: eval/exec/compile, importlib e runpy;
- DA32-NETWORK: chamadas de bibliotecas de rede identificadas.

São regras sintáticas, não um motor de taint. Podem produzir falsos positivos e
não rastreiam todos os aliases dinâmicos, reflection ou chamadas indiretas. Sinais
de risco não são CVEs, advisories nem prova de exploração. A matriz apresenta
contagem de regras e registros estáticos separada de advisories conhecidos.

## Dependências e riscos fora do pip-audit

`pyproject.toml` declara **zero dependências de runtime padrão**. Os quatro
módulos carregados usam stdlib e referências ao próprio upstream. A análise
lista também imports externos nos outros arquivos; não os confunde com pacotes
PyPI resolvidos. Build requirements setuptools/wheel são confrontados com os alvos
de pip-audit; packaging e toda a closure das ferramentas estão no lock com hashes.
Mudanças em requisitos padrão/dinâmicos geram lacuna explícita, nunca resolução
ou instalação automática.

Extras `render` (openpyxl/python-pptx) e `bridge` (litellm/fastapi/google-auth),
ferramentas vendorizadas, patches e scripts shell não são instalados nem executados.
As declarações e seus pins/faixas são preservados, mas suas closures transitivas
e advisories estão **NOT RUN**. Não há um pacote PyPI `harness2` substituto.

Riscos que o pip-audit não cobre incluem comandos externos, uso de filesystem,
compilação dinâmica, leitura JSON de entradas, geração/instalação de artefatos,
launchers opcionais, código vendorizado e operações de build/import antes de
restrições de runtime. SHA-1 para fingerprints pode ter uso não relacionado a
segurança, mas o achado Bandit B324 continua visível e exige revisão contextual.
Nenhuma supressão ou patch do upstream foi aplicado.

## Critérios formais e estados

| Critério | Evidência exigida |
| --- | --- |
| C1 identidade | Origem, commit e árvore exatos; inventário completo sem extras |
| C2 conteúdo | SHA-256/tamanho/blob de cada fonte analisada e vínculo com loaders DAIMON |
| C3 execução do scanner | Versão/pins/cápsula válidos, todos os 58 arquivos em metrics, zero erros/skips, exit coerente |
| C4 consistência | Reparse independente dos bytes, hashes de artefatos e SHA/run/tentativa/escopo válidos |
| C5 dependências utilizadas | Requisitos e imports conhecidos; alvos build/scanner auditados no mesmo run; pin igual ao worker real |
| C6 achados | Nenhum achado ou sinal pendente de revisão específica; nenhuma supressão genérica |
| C7 cobertura restante | Sem lacunas não resolvidas na closure ou nas fronteiras exigidas; revisão contextual verificável |

PASS exigiria C1–C7 satisfeitos por evidência reproduzível; ausência de findings
sozinha não satisfaz C7. A implementação conserva lacunas explícitas de análise
interprocedural, extras e código vendorizado; não há promoção automática a PASS.
Resolver essas lacunas exige um corte revisado de cobertura e evidências adicionais,
não retirar o marcador ou renomear um pacote.

- **FAIL:** divergência de identidade/conteúdo, evidência contraditória ou achados/sinais reais pendentes;
- **BLOCKED:** aquisição/scanner indisponível, timeout ou falha técnica que impede completar a análise;
- **UNVERIFIED:** execução completa sem findings, porém cobertura ainda insuficiente;
- **NOT RUN:** escopo explicitamente não executado, como extras/shell/atestação do produtor;
- **PASS:** somente critérios formais completos, nunca inferido de exit zero.

Quando findings e lacunas coexistem, a lane é FAIL e conserva a lista de lacunas.
`security-python` continua UNVERIFIED para a origem Git, mesmo com auditoria PyPI
sem findings. O novo escopo obrigatório `security-upstream` não substitui nem
silencia esse resultado. Os gates operacionais continuam os 18 UNVERIFIED.

## Testes e evidências

Testes determinísticos cobrem commit/origem/árvore alterados, conteúdo adulterado,
arquivos extras/ausentes, symlink/submódulo/traversal, alteração de loaders, resumo
recalculado falsamente, requisitos novos, aliases/riscos, nosec/versão divergente,
scanner indisponível, metrics/exit contraditórios, skips, duplicatas e comparação
entre jobs. Fixtures são AST/dados inertes; não executam upstream nem processos
adversariais. Os 84 testes anteriores permanecem; a suite local totalizou 104.

A análise local real teve 61 achados Bandit (53 LOW, 5 MEDIUM, 3 HIGH) e 476 sinais
AST antes da última ampliação de detecção os.remove/makedirs/replace. O resultado
definitivo é o do run Actions registrado no PR, com fonte, logs, hashes, cápsula,
inventário e matriz. Findings reais devem permanecer FAIL; novos achados das
ferramentas também devem ser publicados. Artefatos Actions retidos por um dia;
preservar cópia local auditada. Resultados históricos DA-3/DA-3.1 permanecem intactos.

PR #28/C2 sem alterações. Sem VM, privilégios, merge ou atualização do upstream.

## Fontes oficiais

- [Bandit: modelo AST e plugins](https://bandit.readthedocs.io/en/latest/)
- [Bandit: CLI e ignore-nosec](https://bandit.readthedocs.io/en/latest/man/bandit.html)
- [Bandit 1.9.4 e dependências](https://pypi.org/pypi/bandit/1.9.4/json)
- [Upstream no commit exato](https://github.com/google-research/harness2/tree/1cfe254e8d25b15f37048c3730134686abe60335)
- [Requisitos e backend originais](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/pyproject.toml)
