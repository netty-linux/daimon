# Revalidação conjunta dos PRs #22 e #23 para merge

Data: 2026-10-10. O usuário autorizou nesta conversa atualizar, revalidar, enviar e mesclar ambos os PRs após ser informado das limitações com CUA real. Esta autorização não declara sucesso de nenhum teste real.

## Atualização e revisão

O #23 foi rebaseado de 809dbec sobre 169dc45 (#22), com guardas de push --force-with-lease. Os nove commits anteriores foram reaplicados; o único conflito ocorreu no index.html gerado, regenerado ao final. O range-diff de ui/src e ui/scripts mostrou equivalência dos commits de código anteriores. Não há alterações em arquivos Go no diff do #23 contra a nova base.

Foram preservados os 12 quadros aprovados (01, 02, 06, 08, 09, 10, 11, 12, 13, 14, 15, 16), excluindo 03, 04, 05, 07. Editar Bot permanece sem campos fictícios no DTO; a fonte PNG sem referências continua removida. O código da Atividade mantém ocorrências perdidas, e ComputerPanel mantém as mensagens fixas de startup_failed em PT-BR. O bundle não contém marcadores dos fixtures nem o PNG; ui/src não importa ui/scripts.

Build final: index-CqpeXhAo.js e index-B7VX4gBZ.css. Os bundles antigos da base e do Pen foram removidos pelo build limpo. Hashes [antes](validation/stacked-prs-merge-2026-10-10/assets-before.sha256) e [depois](validation/stacked-prs-merge-2026-10-10/assets-after.sha256).

## Validação reexecutada

UI: Windows, Edge headless e servidores de fixture locais. npm ci passou (98 pacotes, zero vulnerabilidades), typecheck e 80 testes / 15 arquivos passaram; build com 58 módulos. Todos os smokes offline listados abaixo passaram. A imagem [chat-fixture-current.png](validation/stacked-prs-merge-2026-10-10/chat-fixture-current.png) foi inspecionada: sidebar, abas, conversa e compositor sem overflow ou sobreposição. Textos da conversa em inglês são conteúdo do fixture, não textos da UI.

Go: Linux Docker golang:1.27.1, sem rede, sobre git archive imutável do commit 3cc269e. Todas as validações Go passaram, incluindo race detector. gofmt não listou arquivos e vet não emitiu diagnósticos. Demo: completed, 2 passos, 1 chamada de ferramenta, zero resultados truncados.

| Comando | Saída completa |
| --- | --- |
| `npm ci` | [npm-ci.txt](validation/stacked-prs-merge-2026-10-10/npm-ci.txt) |
| `npm run typecheck` | [typecheck.txt](validation/stacked-prs-merge-2026-10-10/typecheck.txt) |
| `npm test` | [npm-test.txt](validation/stacked-prs-merge-2026-10-10/npm-test.txt) |
| `npm run build` | [build.txt](validation/stacked-prs-merge-2026-10-10/build.txt) |
| `gofmt -l . / go vet ./... / go test -count=1 ./... / go test -race -count=1 ./... / go run ./cmd/daimon demo` | [go-snapshot.txt](validation/stacked-prs-merge-2026-10-10/go-snapshot.txt) |
| `npm run smoke (rotinas, perdas e Editar Bot)` | [smoke.txt](validation/stacked-prs-merge-2026-10-10/smoke.txt) |
| `npm run smoke:approval` | [smoke-approval.txt](validation/stacked-prs-merge-2026-10-10/smoke-approval.txt) |
| `npm run smoke:mcp` | [smoke-mcp.txt](validation/stacked-prs-merge-2026-10-10/smoke-mcp.txt) |
| `npm run smoke:memory` | [smoke-memory.txt](validation/stacked-prs-merge-2026-10-10/smoke-memory.txt) |
| `npm run smoke:computer` | [smoke-computer.txt](validation/stacked-prs-merge-2026-10-10/smoke-computer.txt) |
| `npm run smoke:computer-view` | [smoke-computer-view.txt](validation/stacked-prs-merge-2026-10-10/smoke-computer-view.txt) |
| `npm run smoke:sandbox` | [smoke-sandbox.txt](validation/stacked-prs-merge-2026-10-10/smoke-sandbox.txt) |
| `npm run smoke:cloud (fixture)` | [smoke-cloud.txt](validation/stacked-prs-merge-2026-10-10/smoke-cloud.txt) |
| `npm run smoke:environments` | [smoke-environments.txt](validation/stacked-prs-merge-2026-10-10/smoke-environments.txt) |
| `node scripts/computer-smoke.mjs --missing` | [smoke-computer-missing.txt](validation/stacked-prs-merge-2026-10-10/smoke-computer-missing.txt) |
| `Smoke adicional para captura visual` | [smoke-visual.txt](validation/stacked-prs-merge-2026-10-10/smoke-visual.txt) |
| `rg: fixtures, PNG e imports de scripts` | [bundle-markers.txt](validation/stacked-prs-merge-2026-10-10/bundle-markers.txt) |

## Ordem e checks de merge

Os pushes só ocorrem após validação local completa. O CI dos commits finais deve passar antes de retirar os drafts e mesclar. #22 é mesclado em main por merge commit, preservando a ancestralidade; depois o #23 muda a base para main, tem o diff/checks conferidos e é mesclado também por merge commit. Nenhum novo PR é aberto. As referências dos merges e o estado final são registrados no GitHub e no relatório final desta conversa.

## Limitações aceitas, ainda não validadas com CUA real

Descoberta pelo DAIMON, leitura de janela, aprovação real, rotinas com computador, Space, Volume, Windows com cua-driver.exe e Fleet. Os smokes de mídia, input, sandbox e cloud usam apenas fixtures offline, sem provisioning pago ou login real. discovery_too_large permanece sem emissão quando não há evidência tipada suficiente; ErrLimit ambíguo fica unknown. Nenhum limite, variável CUA_DRIVER_*, schema mcp.json, policy, aprovação, budget ou contrato do runtime foi alterado para permitir o merge.

.zcodeignore e IDEA.md preexistentes permanecem intocados. Os resultados de rodadas anteriores continuam históricos; as saídas desta pasta correspondem à revalidação conjunta.
