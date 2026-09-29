# AGENTS.md

Este documento define as invariantes e decisões de arquitetura do DAIMON.

## Invariantes Obrigatórias

### 1. Execution Budget

Todo agente DAIMON deve respeitar limites explícitos de execução:

- **MaxSteps**: Número máximo de iterações do loop (padrão: 8)
- **MaxToolCallsPerStep**: Máximo de tool calls por resposta do modelo (padrão: 4)
- **MaxTotalToolCalls**: Máximo total de tool calls na execução (padrão: 16)
- **MaxUserMessageBytes**: Tamanho máximo da mensagem do usuário (padrão: 32 KiB)
- **MaxFinalAnswerBytes**: Tamanho máximo da resposta final (padrão: 256 KiB)
- **MaxToolArgumentBytes**: Tamanho máximo dos argumentos de tool call (padrão: 64 KiB)
- **MaxToolResultBytes**: Tamanho máximo do resultado de tool call (padrão: 64 KiB)
- **MaxHistoryMessages**: Número máximo de mensagens no histórico (padrão: 128)
- **MaxHistoryBytes**: Tamanho máximo do histórico em bytes (padrão: 2 MiB)
- **MaxRunDuration**: Duração máxima da execução (padrão: 5 minutos)
- **MaxModelCallDuration**: Duração máxima de chamada ao modelo (padrão: 2 minutos)
- **MaxToolCallDuration**: Duração máxima de ferramenta (padrão: 30 segundos)

### 2. Validação de Budget

- Budget inválido deve falhar **antes** da primeira chamada ao modelo.
- Zero não significa "ilimitado" — é inválido.
- Todos os limites devem ser positivos (> 0).

### 3. Pré-validação de Tool Calls

Antes de executar qualquer ferramenta de uma resposta:

1. Validar toda a resposta
2. Rejeitar IDs vazios
3. Rejeitar nomes vazios
4. Rejeitar IDs duplicados
5. Validar quantidade de calls no step
6. Validar se o lote ultrapassaria o total da execução
7. Validar tamanho de todos os argumentos
8. Validar capacidade do histórico

**Se qualquer call do lote violar um limite, nenhuma ferramenta daquele lote deve ser executada.**

### 4. Reserva para Resultados

Antes de executar um lote de tool calls, certifique-se de que haverá capacidade para registrar:

- A mensagem assistant contendo as tool calls
- Uma mensagem de resultado para cada tool call

Use `MaxToolResultBytes` como limite superior de reserva para cada resultado.

**Não execute uma ferramenta se não for possível preservar um recibo correlacionado no histórico.**

### 5. Contabilização de Bytes

- Conte bytes usando `len()` sobre UTF-8, não runes.
- `MaxHistoryBytes` deve incluir: `Message.Content`, `Message.ToolCallID`, IDs das tool calls, nomes das ferramentas, argumentos JSON, resposta final armazenada no histórico.

### 6. Truncamento de Tool Results

Se um resultado de ferramenta ultrapassar `MaxToolResultBytes`:

1. Não falhe a execução inteira
2. Normalize para UTF-8 válido
3. Trunque em fronteira UTF-8 válida
4. Inclua indicação curta de truncamento: `\n[truncated: original_bytes=N]`
5. Preserve o limite final em bytes
6. Incremente contador de resultados truncados
7. Não corte silenciosamente
8. Não grave conteúdo completo em evento ou log

### 7. Resposta Final

- Deve ser não vazia e não apenas whitespace
- Deve respeitar `MaxFinalAnswerBytes`
- **Não deve ser truncada silenciosamente**
- Se ultrapassar o limite, a execução deve parar com erro tipado (`StopReasonFinalAnswerLimit`)
- Não deve ser adicionada ao histórico se não couber integralmente
- Uma resposta exatamente no limite deve ser aceita

### 8. Histórico

Antes de adicionar qualquer mensagem:

- Verifique `MaxHistoryMessages`
- Verifique `MaxHistoryBytes`

Ao atingir o limite:
- Interrompa explicitamente
- Preserve o histórico já aceito
- Retorne erro tipado
- Defina `StopReasonHistoryLimit`
- Não execute novos efeitos

### 9. Timeouts Cooperativos

- Preserve deadlines externos menores
- Não amplie deadlines recebidos
- Não use goroutine para matar chamadas
- Continue documentando que cancelamento é cooperativo
- Diferencie: cancelamento externo, deadline externo, timeout do budget, timeout de chamada ao modelo, timeout de ferramenta

### 10. StopReason

Todo retorno de `Loop.Run`, com sucesso ou erro, deve definir `StopReason` de maneira determinística.

### 11. Eventos

Eventos não devem conter:

- Prompts ou respostas completas
- Argumentos ou resultados de ferramentas
- Nomes de arquivos ou IDs fornecidos pelo modelo
- Mensagens de erro detalhadas
- Secrets ou chain-of-thought

O evento `loop_stopped` deve:
- Ser emitido exatamente uma vez
- Receber a razão correta (`stop_reason`)

## Decisões de Arquitetura

### Sem Dependências Externas

DAIMON usa apenas a biblioteca padrão do Go. Não há dependências externas.

### Sem Provider Real (v0.2)

A versão v0.2 não implementa providers reais (OpenAI, Anthropic, etc.). O modelo é abstrato e testado via `ScriptedModel`.

### Sem Framework de Agente

DAIMON não é um framework. É uma fundação minimalista para um agent loop determinístico.

### Sem Concorrência entre Ferramentas

Ferramentas são executadas sequencialmente, uma por uma.

### Sem Retries

Não há retry de ferramentas ou chamadas ao modelo.

### Sem Tokenização Específica de Provedor

Contagem de tamanho é em bytes UTF-8, não tokens.

## Processo de Modificação

1. **Inspecione o estado atual** antes de modificar.
2. **Resuma o comportamento existente**.
3. **Mostre os arquivos que pretende alterar ou criar**.
4. **Defina as semânticas das mudanças**.
5. **Liste as invariantes novas ou alteradas**.
6. **Só então implemente**.
7. **Execute testes e validações**:
   ```bash
   gofmt -w .
   gofmt -l .
   go vet ./...
   go test -count=1 ./...
   go test -race -count=1 ./...
   ```
8. **`gofmt -l .` não deve listar arquivos**.

## Roadmap

| Versão | Descrição |
|--------|----------|
| v0.1 | ✅ Reliable Agent Loop |
| v0.2 | ✅ Execution Budget |
| v0.3 | 🔄 Provider OpenAI-compatible |
| v0.4 | ⏳ Streaming |
| v0.5 | ⏳ Sessions e persistência |
| v0.6 | ⏳ Context Compiler |
