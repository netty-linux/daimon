# Causas controladas de inicialização MCP/CUA

Base desta rodada: `feature/cua-persistent-bots`, `c2f33e3`. Data: 2026-10-10.

## Commits

```text
5ed3516 feat(ui): exibe motivo de falha do computador em PT-BR
f831dd0 feat(computer): expÃµe motivo fixo de falha do backend CUA
b01e311 feat(mcp): classifica causa de falha na inicializaÃ§Ã£o do driver
```

O commit `docs: registra categorias de falha de inicialização` inclui este relatório, a tabela em cua-persistent-bots.md e as evidências. Nenhum commit foi enviado para main; PR #23 não foi alterado; PR #22 permanece draft, sem merge ou novo PR.

## Classificação e limites

O status startup_failed preserva compatibilidade e recebe reason de categoria fixa. O Manager guarda apenas a categoria, não o erro original. Os GET existentes de Computer projetam reason opcional; não há API de ação nova. O decoder aceita somente a lista fechada e a UI apresenta textos fixos em PT-BR. Nenhum storage de navegador foi acrescentado.

Usam-se somente sentinelas existentes com errors.Is e etapa local conhecida. O helper privado newClient informa a etapa sem mudar a assinatura pública de NewClient, as chamadas, o spawn, o cancelamento, a descoberta, cleanup ou efeitos. Erros livres não são examinados. Tabela completa: [categorias](cua-persistent-bots.md#causa-controlada-de-falha-de-inicialização--2026-10-10).

Limitação explícita: não existe tipo exclusivo para descrição/frame. ErrLimit é compartilhado com schema, nomes duplicados, quantidade/paginação. Portanto discovery_too_large fica reservado e não é emitido; esses erros são unknown. ErrUnavailable tampouco prova handshake_failed e fica unknown. Environment_failed requer ErrConfig preservado pelo resolver; erros livres ficam unknown. Tool_limit_exceeded é emitido somente na comparação MaxTotalTools já existente do Manager.

Nenhum MaxTools/MaxTotalTools, 1.024 bytes, frame ou outro limite foi alterado. Nenhum tipo de erro novo foi criado. CUA_DRIVER_*, mcp.json, ToolPolicy, aprovação, budget, execução Computer/CUA, Sandbox e Cloud permanecem intactos. Teste de regressão com subprocesso fake aceita descrição de 1.024 e rejeita 1.025 bytes com ErrLimit. Testes de classificação envolvem sentinelas com caminho, URL, variável e texto fake de stderr; esses valores não aparecem em reason. Teste das duas rotas de metadata verifica a projeção sem os dados privados.

## Validação

Todas as validações finais passaram com exit 0. npm ci: 98 pacotes / zero vulnerabilidades. UI: 76 testes / 15 arquivos; build com 56 módulos. Go test e race: todos os pacotes aprovados. Demo: completed, 2 passos, 1 chamada de ferramenta, zero resultados truncados.

UI em Windows; smokes com Edge headless, servidor local e fixtures offline. Go em Linux Docker golang:1.27.1, rede desabilitada, sobre git archive imutável do commit 5ed3516. Saídas completas:

| Comando | Registro |
| --- | --- |
| npm ci | [saída final](validation/mcp-startup-reasons-2026-10-10/npm-ci-final.txt) |
| npm run typecheck | [saída final](validation/mcp-startup-reasons-2026-10-10/typecheck-final.txt) |
| npm test | [saída final](validation/mcp-startup-reasons-2026-10-10/npm-test-final.txt) |
| npm run build | [saída final](validation/mcp-startup-reasons-2026-10-10/build-final.txt) |
| npm run smoke (inclui rotinas) | [saída](validation/mcp-startup-reasons-2026-10-10/smoke-routines.txt) |
| npm run smoke:approval | [saída](validation/mcp-startup-reasons-2026-10-10/smoke-approval.txt) |
| gofmt -l . | Sem arquivos listados; início da [validação Go final](validation/mcp-startup-reasons-2026-10-10/go-snapshot-final.txt) |
| go vet ./... | Sem diagnósticos; [validação Go final](validation/mcp-startup-reasons-2026-10-10/go-snapshot-final.txt) |
| go test -count=1 ./... | [validação Go final](validation/mcp-startup-reasons-2026-10-10/go-snapshot-final.txt) |
| go test -race -count=1 ./... | [validação Go final](validation/mcp-startup-reasons-2026-10-10/go-snapshot-final.txt) |
| go run ./cmd/daimon demo | [validação Go final](validation/mcp-startup-reasons-2026-10-10/go-snapshot-final.txt) |

O primeiro npm ci recebeu spawn EPERM do sandbox; repetição autorizada passou com 98 pacotes e zero vulnerabilidades. A primeira rodada UI pegou reason projetado no decoder de binding, em vez de ComputerInfo; corrigido, typecheck e 76 testes passaram. A codificação dos novos textos foi normalizada para UTF-8; typecheck, testes, build e ambos os smokes foram repetidos com os assets finais. Logs sem sufixo final preservam tentativas anteriores. Uma rodada Go iniciou antes da conclusão da revisão de classificação e leu fontes durante a troca: falhou num subprocesso de build com símbolos removidos. Dois fixtures também usavam um nome de executável/args incompatível com a validação CUA existente; corrigidos para cua-driver e mcp sem mudar a produção. Os testes focados MCP/Computer/Server passaram. Uma rodada sobre a árvore compartilhada também leu um bundle removido enquanto o build da UI trocava assets; falhou explicitamente. A validação go-snapshot-final repete integralmente todos os checks sobre git archive imutável de 5ed3516, não aproveita resultados anteriores.

## Assets embutidos

O build mudou JavaScript e index.html para incorporar as mensagens e decode de reason. CSS permaneceu idêntico. **O PR #23 (feature/pen-frontend-v1) precisará de rebase e novo build desses assets.**

Antes ([manifest](validation/mcp-startup-reasons-2026-10-10/assets-before.sha256)):

```text
225D46BB9CA6008DE3777FED5099B12C1CDB0E577A86008CC90134CA2BE05AAD  internal/server/ui/assets/index-BcRgBuhR.css
D21A5529A0E65DD5385C5B51B370492BEC92BBF7DE2F6AA819CF66B402B80764  internal/server/ui/assets/index-CKUx2n26.js
09503710AEF039985C7C49D4C0F75BB6AD599CD95BBDE88A7C3FDA4B7399D3B0  internal/server/ui/index.html

```

Depois ([manifest](validation/mcp-startup-reasons-2026-10-10/assets-after.sha256)):

```text
225D46BB9CA6008DE3777FED5099B12C1CDB0E577A86008CC90134CA2BE05AAD  internal/server/ui/assets/index-BcRgBuhR.css
DBC2F4854F434DFBEC53428FFA8032BA9282029CDAF6F4A76C6BF69497F084F1  internal/server/ui/assets/index-Bg0Dw0-N.js
33AB6CCFF1215576999C1B3DAEAD7F0DB7028ED871575FA4B5E55E3F57EFA7E6  internal/server/ui/index.html
```

## Arquivos por pacote (+inserções / -remoções), relativos a c2f33e3

| Arquivo | Inserções | Remoções |
| --- | --- | --- |
| `internal/computer/cua.go` | +12 | -0 |
| `internal/computer/cua_test.go` | +12 | -0 |
| `internal/computer/startup_test.go` | +16 | -0 |
| `internal/computer/types.go` | +1 | -0 |
| `internal/mcp/client.go` | +13 | -0 |
| `internal/mcp/computer.go` | +12 | -2 |
| `internal/mcp/manager.go` | +7 | -1 |
| `internal/mcp/mcp_test.go` | +18 | -0 |
| `internal/mcp/mcptest/fixture.go` | +6 | -0 |
| `internal/mcp/startup.go` | +36 | -0 |
| `internal/mcp/startup_test.go` | +65 | -0 |
| `internal/server/computers_test.go` | +32 | -0 |
| `internal/server/ui/assets/{index-CKUx2n26.js => index-Bg0Dw0-N.js}` | +4 | -4 |
| `internal/server/ui/index.html` | +1 | -1 |
| `ui/src/api/client.ts` | +3 | -1 |
| `ui/src/api/computer.test.ts` | +6 | -0 |
| `ui/src/api/types.ts` | +2 | -1 |
| `ui/src/components/ComputerPanel.test.tsx` | +5 | -0 |
| `ui/src/components/ComputerPanel.tsx` | +2 | -1 |
| `ui/src/i18n/computer.ts` | +12 | -0 |

Documentação adicional: docs/cua-persistent-bots.md, este relatório e docs/validation/mcp-startup-reasons-2026-10-10. As contagens finais de todos os arquivos são registradas em files-numstat.txt e o total em diff-stat.txt.

## Não validado com CUA real

- Descoberta pelo DAIMON.
- Leitura de janela.
- Aprovação individual e efeito real no desktop.
- Rotinas com computador.
- Space e Volume.
- Windows com cua-driver.exe.
- Fleet (provisionamento, login, cloud paga e custo).

Esta rodada não contorna nem resolve a falha real de discovery anterior. Não foram reexecutados smokes específicos de Computer/View/Sandbox/Cloud; seus testes Go offline integram a suite completa. .zcodeignore e IDEA.md preexistentes não foram alterados.
