# DAIMON

DAIMON é a fundação experimental de um **Sovereign Personal Agent**: um agente pessoal
sob controle do usuário, com modelos, ferramentas, memória e canais substituíveis como
direção futura. **Esta versão não é um agente pessoal pronto.**

O primeiro corte vertical implementa somente **Tiny Agent Loop → Reliable Agent Loop**:

```text
mensagem → modelo → chamadas de ferramenta → resultados → modelo → resposta final
```

Tudo roda localmente, sem credenciais, rede, API externa ou dependências fora da biblioteca
padrão. O modelo é um roteiro programável: a demonstração não comprova inteligência de
um modelo real, mas comprova a coordenação do ciclo.

## Execução

Requisito: Go 1.27.1 ou posterior. O demo e a análise estática foram validados em
Windows/amd64; a suíte completa, incluindo symlinks, em Linux/amd64 com Go 1.27.1.

```sh
go run ./cmd/daimon demo
gofmt -w cmd internal
go vet ./...
go test ./...
gofmt -l cmd internal
```

O último comando não deve listar arquivos. A CLI aceita apenas `demo`; argumentos
inválidos e falhas retornam exit code diferente de zero.

No ambiente inicial o Go não estava no PATH. Foi instalado o arquivo oficial, com SHA-256
verificado, em `%LOCALAPPDATA%\DAIMON\toolchains\go1.27.1\go`. Para usá-lo no PowerShell:

```powershell
$env:PATH = "$env:LOCALAPPDATA\DAIMON\toolchains\go1.27.1\go\bin;$env:PATH"
go run ./cmd/daimon demo
```

O demo registra `echo` e `read_file` (workspace = diretório atual, limite = 64 KiB), recebe
“repita DAIMON”, solicita `echo({"text":"DAIMON"})` e retorna `DAIMON` no segundo passo.
Imprime a resposta, dois passos e nove eventos, incluindo `tool_completed` e `loop_stopped`.

## Arquitetura

```text
cmd/daimon/           montagem e saída da CLI
internal/agentloop/   coordenação, limites, erros e eventos
internal/model/       protocolo neutro e Scripted Model
internal/tools/       contratos, Registry, echo e read_file
```

`model.Model` recebe apenas mensagens e descrições de ferramentas (nome, descrição e
schema). Não recebe o Registry nem ferramentas concretas. O loop conhece contratos;
somente a CLI escolhe implementações. Os requests são copiados para que um modelo não
altere o histórico pertencente ao loop. `Scripted` também copia roteiro e requests e
permite injetar erros por chamada usando `ScriptStep.Err`.

`ToolResult` contém conteúdo e `IsError`. O Registry preserva ordem de registro e rejeita
duplicatas. Configuração e execução são sequenciais; não há suporte a uso concorrente.
O proprietário de `ReadFile` deve chamar `Close` ao terminar.

## Semântica e invariantes

- Uma chamada ao modelo conta como um passo, inclusive se retornar erro. As ferramentas
  da resposta pertencem a esse passo, executadas na ordem recebida. MaxSteps deve ser positivo.
- Uma resposta válida contém texto final não vazio/não branco **ou** uma lista não vazia de
  tool calls. Texto (inclusive espaços) junto a calls é ambíguo e retorna `ErrInvalidResponse`.
- IDs devem ser não vazios e únicos em toda a execução; nomes devem ser não vazios.
  Valida-se o lote completo antes de executar qualquer ferramenta.
- O histórico inclui user, assistant com suas calls, tool com `ToolCallID` e `IsError`, e
  assistant final. Todo resultado referencia uma call existente. A resposta final encerra uma vez.
- Nunca são feitas mais de MaxSteps chamadas ao modelo. `ErrMaxSteps` preserva o histórico,
  incluindo ferramentas executadas no último passo. Não existe nova tentativa automática.
- Contexto é verificado antes e depois das chamadas externas. Um contexto já cancelado
  impede chamadas. Cancelamento/deadline permanecem verificáveis com `errors.Is`.
- Falhas do modelo retornam `*ModelError` com passo e causa (`errors.As`/`errors.Is`).
  Roteiro esgotado retorna `model.ErrScriptExhausted`, preservado pelo loop.
- Ferramenta desconhecida, JSON inválido, validação ou erro normal de execução geram
  resultado controlado para o modelo, permitindo recuperação. Erros de contexto encerram.
- Erros controláveis não causam panic. Implementações fornecidas devem cumprir os contratos;
  não há recuperação de panics de código arbitrário nem suporte a interfaces com ponteiro nil tipado.
- `Result` preserva histórico e contador também em falhas. Cancelamento pode deixar calls
  sem resultado: a execução foi interrompida, e nenhum resultado fictício é criado.

## Observabilidade

EventSink tem implementações Noop e Memory. Para o ciclo do demo:

```text
loop_started → model_requested → model_responded → tool_requested
→ tool_completed → model_requested → model_responded → final_answer → loop_stopped
```

Falhas recuperáveis emitem `tool_failed` no lugar de `tool_completed`. Erro no modelo
não emite `model_responded`. Há um único `loop_stopped` em toda saída normal ou com erro,
inclusive contexto já cancelado ou configuração inválida. Uma interrupção durante chamada
pode deixar um evento requested sem responded/completed.

Eventos contêm apenas tipo, número do passo e índice da ferramenta: sem timestamps,
texto, argumentos, nomes/IDs fornecidos pelo modelo ou mensagens de erro. O sink deve ser
local, rápido e aceitar registros de encerramento com contexto cancelado. Memory preserva
os eventos em ordem e retorna cópias para inspeção; não há event sourcing.

O histórico e os requests de teste, por outro lado, contêm dados de usuário e de arquivos;
devem ser tratados como sensíveis. Mensagens de erro de ferramentas vão ao modelo e
implementações futuras devem evitar incluir segredos nessas mensagens.

## Ferramentas e segurança

- `echo`: objeto com `text` obrigatório e string (string vazia é válida).
- `read_file`: objeto com `path` obrigatório e string. Somente caminhos relativos ao
  workspace; bloqueia caminhos absolutos, segmentos `..`, prefixos de unidade, UNC e ADS.
  Abertura usa `os.Root`, que resolve symlinks sem permitir escape do diretório raiz.
  Symlinks internos são permitidos. Diretórios e arquivos não regulares são recusados.
  O tamanho é verificado antes de ler e a leitura é limitada a limite + 1 byte, detectando
  também crescimento. Campos desconhecidos, duplicados, null e valores extras são rejeitados.

Os testes usam diretórios temporários. Os testes de symlink precisam de permissão para
criá-los (no Windows, modo de desenvolvedor ou privilégio apropriado); falham explicitamente
se indisponível. Não usam rede nem APIs externas.

Alternativa para Windows sem esse privilégio, com Docker Desktop em execução
(o download inicial da imagem requer rede, mas a execução dos testes não):

```powershell
docker pull golang:1.27.1
docker run --rm --network none --mount "type=bind,source=$((Get-Location).Path),target=/workspace,readonly" -w /workspace golang:1.27.1 go test ./...
```

## Testes e limites atuais

Os testes cobrem resposta direta, uma e múltiplas chamadas, ordem, correlação, recuperação
de falhas, JSON estrito, respostas ambíguas, IDs inválidos/duplicados, erro do modelo,
MaxSteps, contexto cancelado antes/durante chamadas, deadline expirado, eventos, Registry,
cópias defensivas, roteiro esgotado, CLI e segurança de read_file.

Cancelamento é cooperativo: não interrompe à força um modelo/ferramenta que ignora contexto
nem uma chamada de sistema bloqueada. Não há goroutines de timeout. MaxSteps limita chamadas
ao modelo, mas não o tamanho das respostas, quantidade de ferramentas em um lote ou memória
total do histórico. O workspace deve ser confiável: os.Root não é um sandbox de processo e
não bloqueia hard links ou todos os efeitos de alterações concorrentes no filesystem.
Os testes não simulam corridas de filesystem, dispositivos ou montagens especiais.

Ainda não existem provedores reais, persistência, memória longa/vetorial, banco, gateway,
subagentes, MCP, servidor, canais de mensagens, interface visual, shell, edição de arquivos
ou frameworks de agentes. Não são criadas pastas para funcionalidades futuras.

A soberania nesta fase é a independência técnica de provedores: contratos pequenos,
execução local, nenhuma credencial obrigatória e nenhuma transmissão de dados.
Próximo passo recomendado: definir e testar limites de volume por execução (tamanho de
resposta, calls por passo e histórico) antes de integrar um provedor real. Não implementado aqui.
