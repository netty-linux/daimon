# Pen frontend v1 — integração e limites

Fonte: `Daimon-Refactory.pen`, lido exclusivamente pelo MCP `pencil`, em 2026-10-09.
Base local: `feature/cua-persistent-bots`, commit `809dbec902ac65d531fb237541da5cffb848d599`.
A branch solicitada `feature/pen-frontend-v1` já existia no mesmo commit; foi reutilizada sem reset.
PR #22: `feature/cua-persistent-bots` → `main`; nenhuma alteração nessa branch, merge ou abertura de PR.
Os arquivos não rastreados `.zcodeignore` e `IDEA.md` são do workspace e ficam fora dos commits.

## Quadros autorizados

| Quadro / nó Pen | Integração no frontend | Adaptação e diferença visual |
| --- | --- | --- |
| 01 / `g7ZwgF` | Navegação unificada com busca local, chat e computador em painel lateral quando existe binding. | Conteúdo vem das rotas existentes; não é o dashboard Acme desenhado. Topbar e abas continuam explícitos. |
| 02 / `k4okQf` | Estado vazio existente dentro do novo shell, criação deliberada de Bot/conversa. | Sem nomes/status fictícios, mascotes atribuídos arbitrariamente ou sugestões que executem tarefas. |
| 06 / `n7TAs` | Aba Computador expande o viewer existente; painel lateral permite abrir essa aba. | O viewer recebe mídia real do contrato, ou fixture nos testes, sem reconstruir um desktop com HTML. |
| 08 / `gd4yV` | Configurações apresentam permissões e aprovação individual, com os painéis existentes. | Sem “aprovar tudo”, switches globais de autorização, chamadas recebidas ou preferências sem rota. |
| 09 / `qXMcY` | Chat ocupa a área principal quando não há computador; bolhas, superfícies, composer e wordmark oficial. | Fonte do sistema, largura de leitura responsiva, autores/horários e abas de recursos preservados. |
| 10 / `gTIia` | Modal existente de configuração do Bot recebe dimensões e superfícies do shell; nome é editável pelo PUT existente. | Reprodução parcial: não há seletor de personagens/cores ou prévia personalizada, pois o DTO não contém esses campos. PUT continua exigindo reentrada integral das instruções. |
| 11 / `dSsdI` | Revisão deliberada em modal, alvo exato, aviso, decisão única e preview integral quando fornecido pelo contrato. | Leitura não mostra conteúdo antes da aprovação; escrita mostra o preview reversível integral. O modal mantém o foco e acesso em todas as abas. |
| 12 / `sYX1F` | Falha, detalhes e recuperação do rascunho existentes permanecem acessíveis no shell. | “Tentar novamente” preenche o rascunho; exige novo envio deliberado e não repete POST automaticamente. |
| 13 / `coURT` | Atividade com apresentação de eventos e cartões das rotinas existentes. | Eventos são os da execução selecionada; rotinas são do Bot selecionado, sem timeline global fictícia, catch-up ou execução fora do servidor. |
| 14 / `GHzh0` | Memória com busca local por conteúdo/tipo/escopo/tags, grupos e cartões, preservando CRUD manual. | Disclosures de plaintext/contexto continuam visíveis; nenhum contexto é extraído automaticamente. |
| 15 / `M80GPT` | Estado explícito “Visualização indisponível”, texto de continuidade do chat e controle desabilitado. | Validado com metadados do servidor Go e driver de fixture, sem serviço de mídia; não depende de CUA real. |
| 16 / `J33Yq` | Card “Aprovação pendente” após fechar a revisão; reabrir solicitação e aviso junto ao composer bloqueado. | A revisão continua usando o mesmo ID e decisão única do servidor; fechar não decide nem cancela. Fixture do servidor real no smoke de aprovação. |

Quadros **03, 04, 05 e 07**, marcados “Em revisão”, não foram integrados nem modificados.
Não houve mutação do documento Pen: chamadas usadas foram leitura, screenshot de referência e exportação do wordmark do quadro 09.
Projetos e conta/persona fictícia da sidebar também não foram reproduzidos.

## Contratos e acessibilidade

Nenhum endpoint, DTO, código Go de produção, loop, policy, capability, aprovação ou budget foi alterado.
O viewer lateral usa os mesmos contratos deliberados de visualização e takeover; não envia input sem lease explícito.
Seleção por hash, contexto de conversa, falhas, abort e recuperação de admissão continuam com o fluxo anterior.
Busca é somente apresentação em memória React; não altera seleção ou inicia Session.
Menus fecham ao escolher ações. Recolher bots mantém conversas acessíveis; recolher ambas remove a navegação.
Modal nativo, foco/restauração, tabs com teclado e redução de movimento continuam presentes.
Layouts são responsivos: abaixo de 1100px o computador fica abaixo do chat; em telas pequenas a navegação ocupa uma faixa limitada.
Smokes verificam ausência de overflow do documento em 1440, 1280, 1024 e 600px.
Não existe storage novo no navegador; frames, tickets, controle e aprovações permanecem efêmeros.

Textos novos: `ui/src/i18n/pen.ts`. Estilos/tokens: `ui/src/design`.
Literais de cor dos estilos anteriores foram centralizados em `tokens.css`; CSS de componentes usa variáveis.
Wordmark exportado pelo MCP: `ui/src/design/assets/x6AmZ.png` (arte original, sem geração de imagem).

## Arquivos alterados

- `ui/src/App.tsx`: composição da navegação unificada, painel lateral do computador, aviso de pendência, wordmark e fechamento dos menus.
- `ui/src/features/navigation/WorkspaceNavigation.tsx`: busca e composição de Bot/conversa com recolhimento independente.
- `ui/src/design/{tokens.css,product.css,dots.css,pen.css,assets/x6AmZ.png}` e `ui/src/styles.css`: tokens, superfícies, contraste e responsividade.
- `ui/src/i18n/pen.ts`: catálogo PT-BR da integração.
- `ui/src/components/{ApprovalPanel.tsx,ComputerViewer.tsx,MemoryPanel.tsx}`: pendência deliberada, ausência de mídia e busca de memória.
- `ui/src/features/{activity/Activity.tsx,settings/Settings.tsx}`: apresentações de atividade e permissões.
- `ui/src/features/product.test.tsx`: busca sem efeitos, recolhimento independente e menu fechado ao abrir configurações.
- `ui/scripts/{approval-smoke.mjs,computer-smoke.mjs}`: quadros 16/15 com servidor real e fixtures; contagem de mídia exclui logo estático.
- `internal/server/ui/index.html` e `internal/server/ui/assets/*`: build Vite versionado.
- Este relatório e `docs/validation/pen-frontend-v1/*`: saídas de comandos, hashes e screenshot exclusivo de chat de fixture.

## Validação

Saídas integrais ficam em [validation/pen-frontend-v1](validation/pen-frontend-v1).
Go executado em Linux real via Docker `golang:1.27.1`, árvore somente leitura, `GOPROXY=off` e rede desativada.
Browser: Edge headless, servidor local real e providers/drivers determinísticos; sem credenciais, cloud paga ou CUA real.
Cada smoke limpa seus recursos temporários.

| Comando | Resultado / saída |
| --- | --- |
| `npm ci` em `ui/` | 98 pacotes; 0 vulnerabilidades. [Saída](validation/pen-frontend-v1/npm-ci.txt) |
| `npm run typecheck` | Exit 0. [Saída](validation/pen-frontend-v1/typecheck.txt) |
| `npm test` | 75 testes / 15 arquivos. [Saída](validation/pen-frontend-v1/npm-test.txt) |
| `npm run build` | TypeScript + Vite. [Saída](validation/pen-frontend-v1/build.txt) |
| `npm run smoke` | CRUD, transcript/restart, SSE, falha, abort, rotina, layout. [Saída](validation/pen-frontend-v1/smoke.txt) |
| `npm run smoke:approval` | Aprovação nativa, preview, pendência quadro 16, deny/reload/replay. [Saída](validation/pen-frontend-v1/smoke-approval.txt) |
| `npm run smoke:mcp` | Catálogo, seleção, aprovação, negativa e cleanup. [Saída](validation/pen-frontend-v1/smoke-mcp.txt) |
| `npm run smoke:memory` | CRUD manual, escopos, contexto imutável e aprovação. [Saída](validation/pen-frontend-v1/smoke-memory.txt) |
| `npm run smoke:computer` | Aprovações observe/click/type, quadro 15, deny/abort/busy/crash. [Saída](validation/pen-frontend-v1/smoke-computer.txt) |
| `npm run smoke:computer-view` | Mídia de fixture, dois viewers, takeover, exclusão do agente e reload. [Saída](validation/pen-frontend-v1/smoke-computer-view.txt) |
| `npm run smoke:sandbox` | Isolamento local de fixture, view/input e cleanup. [Saída](validation/pen-frontend-v1/smoke-sandbox.txt) |
| `npm run smoke:cloud` | Cloud simulada, view/input e cleanup. [Saída](validation/pen-frontend-v1/smoke-cloud.txt) |
| `npm run smoke:environments` | Revisões local/cloud, falha/abort, restart e remoção. [Saída](validation/pen-frontend-v1/smoke-environments.txt) |
| `node scripts/computer-smoke.mjs --missing` | Driver ausente. [Saída](validation/pen-frontend-v1/smoke-computer-missing.txt) |
| `gofmt -l .` | Saída vazia. [Registro](validation/pen-frontend-v1/gofmt.txt) |
| `go vet ./...` | Saída vazia, exit 0. [Registro](validation/pen-frontend-v1/go-vet.txt) |
| `go test -count=1 ./...` | Todos os pacotes passam, incluindo symlink. [Saída](validation/pen-frontend-v1/go-test.txt) |
| `go test -race -count=1 ./...` | Todos os pacotes passam. [Saída](validation/pen-frontend-v1/go-race.txt) |
| `go test -count=1 ./internal/server` após build final | Exit 0. [Saída](validation/pen-frontend-v1/go-server-final.txt) |
| `go run ./cmd/daimon demo` | Completed; 2 passos / 1 ferramenta. [Saída](validation/pen-frontend-v1/demo.txt) |

As primeiras execuções encontraram recolhimento conjunto, contraste herdado e contagem indevida do logo como mídia; os problemas foram corrigidos e revalidados.
Uma compilação de fixture coincidiu com regeneração de assets e foi repetida após estabilizar o build.
`npm ci` precisou de execução fora da restrição de subprocessos do sandbox Windows.
Essas falhas não foram convertidas em sucesso nem substituídas por comandos simulados.

## Hashes dos assets

SHA-256 completo, antes: [assets-before.sha256](validation/pen-frontend-v1/assets-before.sha256).
Depois: [assets-after.sha256](validation/pen-frontend-v1/assets-after.sha256).
O build substitui os nomes com hash de CSS/JS e acrescenta o PNG do wordmark. Não há conteúdo de frame/aprovação nesse asset.

## Entrega

- `dca92f9`: shell/chat — quadros 01, 02, 06, 09 e adaptação parcial de 10.
- `81a2efe`: estados/painéis — quadros 08, 11, 12, 13, 14, 15 e 16.
- Commit final: assets, evidências e este relatório.
- Push permitido exclusivamente de `feature/pen-frontend-v1`; sem PR ou merge.

Não é uma reprodução pixel a pixel. Além das diferenças da tabela, o dashboard Acme e as mensagens desenhadas são exemplos visuais, substituídos por dados reais do runtime ou fixtures nos testes. A navegação não inventa identidade/conta/modelos, disponibilidade ou atividades globais.

### SHA-256 — Antes

```text
B8210052CB6CE70186D6D4ADBD1CED9EEC606441E35D45E837E00C645477E18F  internal\server\ui\index.html
225D46BB9CA6008DE3777FED5099B12C1CDB0E577A86008CC90134CA2BE05AAD  internal\server\ui\assets\index-BcRgBuhR.css
6701964F3F19D16AEF8DA10810BF374BC1465368D0B16660664E8431131A8E7A  internal\server\ui\assets\index-DcvWEFiB.js
```

### SHA-256 — Depois

```text
278E1A7B4956ABC65BAA190E42E1CFFAA42299E974264683C0BD32A2DB9A780B  internal\server\ui\index.html
F6EE4888319E4612367E5068F1998B1373C56A31B07EAFF6236922B283A03FEF  internal\server\ui\assets\index-C7JeaBoJ.js
27DE15ADCB2BC2A8FEBC7CCFF55B55A94722D1F31A2805DECF6904959083EF1C  internal\server\ui\assets\index-yLEfndF4.css
2C388E8685A268AB54411F175D27E07DCF49B81B65816A80473AB88F870F7179  internal\server\ui\assets\x6AmZ-V0AtGXwv.png
```
