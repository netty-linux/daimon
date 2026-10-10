# Decisões aplicadas — Sessions e rotinas perdidas

Base: `feature/cua-persistent-bots`, commit `77bac03`. Data: 2026-10-10.

## Commits de implementação

- `f7aaf1b` — contrato em AGENTS.md e remoção da linha em branco extra.
- `a287306` — descarte de Sessions terminais antigas no limite.
- `341c259` — detecção e persistência de ocorrência perdida na inicialização.
- `cfcf188` — ocorrência perdida na Atividade, PT-BR e smoke de reinício.
- `81c869c` — histórico independente da reprogramação e fixture HTTP sem regressão artificial de relógio.
- Commit de documentação/evidências: inclui este relatório e saídas de validação.

## Contratos implementados

O Manager da aplicação admite no máximo 128 Sessions retidas; configurações menores continuam explícitas. Ao admitir Start no limite, escolhe a terminal finalizada com menor StartedAt, com desempate por ID. Somente completed/failed/aborted com done fechado são elegíveis. Startup, execução, aprovação, persistência e cleanup permanecem retidos. Sem candidata, retorna `sessions.Error{Kind: Capacity}` antes de alterar Sessions. Depois do descarte, Get/Events retornam NotFound; um Wait já iniciado continua usando sua referência. IDs admitidos permanecem reservados em tombstones de identidade, sem dados da execução; isso não promete um limite rígido de memória do processo.

O agendador chama Initialize antes de iniciar seus ticks. Para rotina ativa vencida, registra apenas o último horário diário já passado, na timezone declarada. Mantém uma ocorrência perdida mais recente por rotina, até 32 registros no store, com horário previsto e detecção UTC. Não cria Session, não consulta autorização, não muda LastAttemptAt ou intervalo por Bot, não faz catch-up de vários dias. Avança o próximo horário; se a inicialização coincide exatamente com o horário diário atual, preserva esse horário para o disparo normal. Pausadas não geram perda. Dois reinícios sucessivos preservam exatamente a mesma ocorrência e horário de detecção.

O store grava versão 2 com dois timestamps limitados por rotina. Versão 1 permanece legível; uma gravação válida converte para versão 2. Schema estrito exige os campos exatos de cada versão, sem chaves duplicadas, null, campos desconhecidos ou recuperação silenciosa. Horário de detecção não precede o previsto. Histórico não depende do NextAt mutável: pausar/ativar não invalida a ocorrência já registrada.

GET de rotinas acrescenta `missed` opcional com estado constante `missed`, `scheduled_at` e `detected_at`. Não há endpoint novo. A UI mostra “Perdida”, os dois horários em UTC e que nenhuma tarefa foi executada. Decode valida estado/timestamps; conteúdo usa escape React. Nenhum layout/CSS de outras telas foi modificado.

ToolPolicy, aprovação, budget do loop, Computer, CUA, Sandbox e Cloud não foram alterados. A política de cloud paga em rotina continua bloqueada.

## Validação e saídas

Todas as validações finais abaixo passaram (exit 0). Os arquivos com sufixo `-final` contêm as repetições completas após as correções.

Go: Linux real em Docker `golang:1.27.1`, rede desativada, GOPROXY=off e árvore somente leitura. UI: Windows, Edge headless, servidor real com provider/driver de fixture. Nenhum acesso a serviços pagos ou credenciais.

| Comando | Saída desta rodada |
| --- | --- |
| `npm ci` | 98 pacotes, 0 vulnerabilidades; [registro](validation/native-routines-decisions-2026-10-10/npm-ci.txt) |
| `npm run typecheck` | Sem diagnósticos; [saída](validation/native-routines-decisions-2026-10-10/typecheck.txt) |
| `npm test` | 74 testes / 15 arquivos; [saída](validation/native-routines-decisions-2026-10-10/npm-test.txt) |
| `npm run build` | 55 módulos, JS 311,21 kB, CSS 21,51 kB; [saída](validation/native-routines-decisions-2026-10-10/build.txt) |
| `npm run smoke` (rotinas) | CRUD, perda sem modelo/Session, pausa sem perda, dois reinícios idempotentes; [saída](validation/native-routines-decisions-2026-10-10/smoke-routines.txt) |
| `npm run smoke:approval` | Preview, allow/deny, reload, replay, duas abas, abort e shutdown; [saída](validation/native-routines-decisions-2026-10-10/smoke-approval.txt) |
| `gofmt -l .` | Sem arquivos listados; etapa inicial da [validação Linux final](validation/native-routines-decisions-2026-10-10/go-validation-final.txt) |
| `go vet ./...` | Sem diagnósticos; segunda etapa da [validação Linux final](validation/native-routines-decisions-2026-10-10/go-validation-final.txt) |
| `go test -count=1 ./...` | [Saída integral Linux final](validation/native-routines-decisions-2026-10-10/go-validation-final.txt) |
| `go test -race -count=1 ./...` | [Saída integral Linux final](validation/native-routines-decisions-2026-10-10/go-race-final.txt) |
| `go run ./cmd/daimon demo` | completed, 2 passos / 1 ferramenta; [saída](validation/native-routines-decisions-2026-10-10/routines-race-demo.txt) |

Tentativas iniciais preservadas em `go-validation.txt` e `go-race.txt`: falharam no novo teste HTTP que avançava o relógio da fixture em 48 horas e depois usava o horário real para pausar. O teste foi isolado, a relação indevida entre histórico e NextAt removida e as suítes completas repetidas. O primeiro smoke encontrou um seletor exato que incluía também texto do timestamp; o seletor foi corrigido e o smoke repetido com sucesso.

## Assets e PR #23

O build alterou o JavaScript e o index.html embutidos; o CSS ficou idêntico. **A branch feature/pen-frontend-v1 (PR #23) também contém esses assets e precisará de rebase e novo build após incorporar a base atualizada.** Nenhuma alteração foi feita nessa branch ou no PR #23.

SHA-256 antes/depois: [antes](validation/native-routines-decisions-2026-10-10/assets-before.sha256), [depois](validation/native-routines-decisions-2026-10-10/assets-after.sha256).

## Não validado com CUA real

- Disparo de rotina, aprovação/efeito e bloqueio por aprovação pendente com CUA real.
- Windows com `cua-driver.exe` real; o bloqueio de discovery anterior não foi contornado.
- Space e Volume, suas associações e funcionalidades.
- CUA Fleet/provisionamento cloud pago, login e custo real.
- Esta rodada não reexecutou todos os outros smokes Computer/Sandbox/Cloud; os testes Go desses pacotes fazem parte das suítes completas, com fixtures offline.

## Arquivos de implementação por pacote (+adições / −remoções)

Contagens relativas a `77bac03`, incluindo testes; arquivos de evidência ficam no diretório de validação acima.

| Arquivo | + | − |
| --- | ---: | ---: |
| `AGENTS.md` | 9 | 5 |
| `docs/cua-persistent-bots.md` | 12 | 3 |
| `internal/routines/missed_test.go` | 212 | 0 |
| `internal/routines/scheduler.go` | 31 | 13 |
| `internal/routines/scheduler_test.go` | 1 | 1 |
| `internal/routines/store.go` | 59 | 6 |
| `internal/server/routines_test.go` | 28 | 0 |
| `internal/server/ui/assets/{index-DcvWEFiB.js => index-CKUx2n26.js}` | 6 | 6 |
| `internal/server/ui/index.html` | 1 | 1 |
| `internal/sessions/manager.go` | 29 | 6 |
| `internal/sessions/manager_test.go` | 3 | 1 |
| `internal/sessions/retention_test.go` | 105 | 0 |
| `internal/sessions/types.go` | 1 | 1 |
| `ui/scripts/smoke.mjs` | 20 | 1 |
| `ui/src/components/RoutinePanel.test.tsx` | 13 | 0 |
| `ui/src/components/RoutinePanel.tsx` | 4 | 2 |
| `ui/src/i18n/routines.ts` | 6 | 1 |

### SHA-256 — Antes

```text
B8210052CB6CE70186D6D4ADBD1CED9EEC606441E35D45E837E00C645477E18F  internal\server\ui\index.html
225D46BB9CA6008DE3777FED5099B12C1CDB0E577A86008CC90134CA2BE05AAD  internal\server\ui\assets\index-BcRgBuhR.css
6701964F3F19D16AEF8DA10810BF374BC1465368D0B16660664E8431131A8E7A  internal\server\ui\assets\index-DcvWEFiB.js
```

### SHA-256 — Depois

```text
09503710AEF039985C7C49D4C0F75BB6AD599CD95BBDE88A7C3FDA4B7399D3B0  internal\server\ui\index.html
225D46BB9CA6008DE3777FED5099B12C1CDB0E577A86008CC90134CA2BE05AAD  internal\server\ui\assets\index-BcRgBuhR.css
D21A5529A0E65DD5385C5B51B370492BEC92BBF7DE2F6AA819CF66B402B80764  internal\server\ui\assets\index-CKUx2n26.js
```

### Arquivos de evidência (linhas adicionadas; novos arquivos)

| Arquivo | Linhas |
| --- | ---: |
| `docs/validation/native-routines-decisions-2026-10-10/assets-after.sha256` | 3 |
| `docs/validation/native-routines-decisions-2026-10-10/assets-before.sha256` | 3 |
| `docs/validation/native-routines-decisions-2026-10-10/build.txt` | 17 |
| `docs/validation/native-routines-decisions-2026-10-10/go-race-final.txt` | 31 |
| `docs/validation/native-routines-decisions-2026-10-10/go-race.txt` | 35 |
| `docs/validation/native-routines-decisions-2026-10-10/go-validation-final.txt` | 31 |
| `docs/validation/native-routines-decisions-2026-10-10/go-validation.txt` | 35 |
| `docs/validation/native-routines-decisions-2026-10-10/npm-ci.txt` | 1 |
| `docs/validation/native-routines-decisions-2026-10-10/npm-test.txt` | 17 |
| `docs/validation/native-routines-decisions-2026-10-10/routines-race-demo.txt` | 17 |
| `docs/validation/native-routines-decisions-2026-10-10/smoke-approval.txt` | 5 |
| `docs/validation/native-routines-decisions-2026-10-10/smoke-routines.txt` | 5 |
| `docs/validation/native-routines-decisions-2026-10-10/typecheck.txt` | 4 |
