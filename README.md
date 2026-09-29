# DAIMON

**DAIMON** é a fundação de um **Sovereign Personal Agent** escrito em Go.

## Visão Geral

DAIMON é um agent loop determinístico que:
- Executa ferramentas de forma controlada
- Mantém histórico de mensagens
- Respeita limites explícitos de execução (Execution Budget)
- É testável de forma determinística
- Não depende de providers externos na versão atual

## Estado Atual

| Versão | Status | Descrição |
|--------|--------|-----------|
| v0.1 | ✅ Concluído | Reliable Agent Loop |
| v0.2 | ✅ Concluído | Execution Budget |
| v0.3 | 🔄 Próximo | Provider OpenAI-compatible |
| v0.4 | ⏳ Pendente | Streaming |
| v0.5 | ⏳ Pendente | Sessions e persistência |
| v0.6 | ⏳ Pendente | Context Compiler |

## Execution Budget

O DAIMON v0.2 introduz limites explícitos e verificáveis para tornar cada execução **finita, mensurável e segura**.

### Limites Padrão

| Limite | Valor | Unidade |
|--------|-------|--------|
| `MaxSteps` | 8 | quantidade |
| `MaxToolCallsPerStep` | 4 | quantidade |
| `MaxTotalToolCalls` | 16 | quantidade |
| `MaxUserMessageBytes` | 32 KiB | bytes |
| `MaxFinalAnswerBytes` | 256 KiB | bytes |
| `MaxToolArgumentBytes` | 64 KiB | bytes |
| `MaxToolResultBytes` | 64 KiB | bytes |
| `MaxHistoryMessages` | 128 | quantidade |
| `MaxHistoryBytes` | 2 MiB | bytes |
| `MaxRunDuration` | 5 minutos | duração |
| `MaxModelCallDuration` | 2 minutos | duração |
| `MaxToolCallDuration` | 30 segundos | duração |

### Semântica em Bytes

- Contagem de bytes usa `len()` sobre UTF-8, não runes.
- Inclui: `Message.Content`, `Message.ToolCallID`, IDs de tool calls, nomes de ferramentas, argumentos JSON, resultados armazenados no histórico.
- Não estima memória real da struct e não implementa tokenização de provedor.

### Timeouts Cooperativos

- `MaxRunDuration`, `MaxModelCallDuration`, `MaxToolCallDuration` são aplicados via `context.WithTimeout`.
- Deadlines externos menores são preservados (não ampliados).
- Cancelamento é cooperativo: Model e Tool devem respeitar `context.Context`.

### Truncamento de Tool Results

- Resultados de ferramentas que excedem `MaxToolResultBytes` são:
  1. Normalizados para UTF-8 válido
  2. Truncados em fronteira UTF-8 válida
  3. Marcados com indicador: `\n[truncated: original_bytes=N]`
  4. O indicador também respeita o limite final
- O contador `Result.TruncatedToolResults` registra quantos resultados foram truncados.
- Erros de ferramentas convertidos em conteúdo também passam pelo mesmo limite.

### StopReason

Todo retorno de `Loop.Run` define `StopReason` de forma determinística:

| StopReason | Descrição |
|------------|----------|
| `completed` | Execução completou com sucesso |
| `invalid_config` | Budget ou configuração inválida |
| `canceled` | Contexto cancelado externamente |
| `external_deadline` | Deadline externo atingido |
| `run_timeout` | Timeout de execução total |
| `model_timeout` | Timeout de chamada ao modelo |
| `tool_timeout` | Timeout de ferramenta |
| `model_error` | Erro do modelo |
| `invalid_response` | Resposta inválida do modelo |
| `max_steps` | Limite de steps atingido |
| `max_tool_calls` | Limite de tool calls atingido |
| `argument_limit` | Argumento de tool call excedeu limite |
| `final_answer_limit` | Resposta final excedeu limite |
| `history_limit` | Histórico atingiu limite |

### Limitações Conhecidas

- Não há sandbox de segurança para ferramentas.
- Não há tokenização específica de provedor (contagem é em bytes UTF-8).
- Não há summarization ou descarte de histórico antigo.
- Timeouts dependem de cooperação (context.Context).
- Valores padrão são conservadores, mas não garantias de produção.

## Estrutura do Projeto

```
.
├── cmd/
│   └── daimon/
│       └── main.go          # CLI demo
├── internal/
│   ├── agentloop/
│   │   ├── budget.go        # Budget, LimitError, StopReason
│   │   ├── budget_test.go
│   │   ├── size.go          # Medições de tamanho
│   │   ├── size_test.go
│   │   ├── loop.go          # Agent loop principal
│   │   ├── loop_test.go
│   │   ├── errors.go        # Erros tipados
│   │   ├── types.go         # Types: Result, Options
│   │   └── events.go        # Eventos de observabilidade
│   ├── model/
│   │   ├── model.go         # Interface Model
│   │   ├── scripted.go      # Modelo scripted para testes
│   │   └── scripted_test.go
│   └── tools/
│       ├── tool.go          # Interface Tool
│       ├── registry.go      # Registry de ferramentas
│       ├── echo.go          # Ferramenta echo
│       ├── read_file.go     # Ferramenta read_file
│       └── tools_test.go
├── .github/
│   └── workflows/
│       └── ci.yml           # CI com vet, testes, race detector
├── go.mod
├── README.md
└── AGENTS.md
```

## Uso

```bash
# Demo
go run ./cmd/daimon demo

# Testes
go test -count=1 ./...
go test -race -count=1 ./...

# Lint
gofmt -w .
gofmt -l .
go vet ./...
```

## Ferramentas Incluídas

| Ferramenta | Descrição |
|------------|----------|
| `echo` | Retorna o argumento recebido (para testes) |
| `read_file` | Lê o conteúdo de um arquivo local |

## Próximos Passos

1. **v0.3 — Provider OpenAI-compatible**: Conectar um único provider compatível com a API OpenAI.
2. **v0.4 — Streaming**: Suporte a streaming de respostas.
3. **v0.5 — Sessions e persistência**: Persistência de histórico e sessões.
4. **v0.6 — Context Compiler**: Otimização de contexto (summarization, etc.).

## Licença

MIT
