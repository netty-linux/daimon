# DAIMON

DAIMON é a fundação experimental de um **Sovereign Personal Agent** em Go:
execução sob controle do usuário e contratos independentes de provedor.
**Ainda não é um agente pessoal pronto.**

O corte atual reúne Reliable Agent Loop, Execution Budget, um adapter
OpenAI-compatible para Chat Completions sem streaming e a primeira capacidade
real de exploração de workspace com aprovação humana:
mensagem → modelo → ferramentas opcionais → resultados → modelo → resposta final.
Usa somente a biblioteca padrão, um modelo programável e as ferramentas
`echo`, `read_file`, `list_dir` e `replace_file`. O demo e os testes não requerem credenciais
nem APIs externas; o comando chat se conecta ao endpoint escolhido pelo usuário.

## Execução e validação

Requisito: Go 1.27.1 ou posterior. Na raiz do projeto:

```sh
go run ./cmd/daimon demo
gofmt -w .
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
```

`gofmt -l .` não deve listar arquivos. O demo recebe “repita DAIMON”, executa
`echo({"text":"DAIMON"})` e retorna `DAIMON` em dois passos, com dez eventos.
Registra também `read_file`, limitado a 64 KiB e ao diretório atual, e `list_dir`,
limitado a 256 entradas e 32 KiB por listagem.
A CLI aceita `demo` e `chat "mensagem"`; argumentos inválidos, mensagem vazia e
falhas retornam código não zero. Chat imprime resposta final, passos, tool calls,
resultados truncados e StopReason, sem imprimir o histórico.

No ambiente Windows inicial, o SDK foi instalado em
`%LOCALAPPDATA%\DAIMON\toolchains\go1.27.1\go`:

```powershell
$env:PATH = "$env:LOCALAPPDATA\DAIMON\toolchains\go1.27.1\go\bin;$env:PATH"
go run ./cmd/daimon demo
```

Os testes de symlink precisam de permissão para criá-los. No Windows, sem modo de
desenvolvedor ou privilégio apropriado, falham explicitamente. Para executar a
suíte inteira em Linux com Docker Desktop (download inicial requer rede):

```powershell
docker pull golang:1.27.1
docker run --rm --network none --mount "type=bind,source=$((Get-Location).Path),target=/workspace,readonly" -w /workspace golang:1.27.1 bash -c 'go vet ./... && go test -count=1 ./... && go test -race -count=1 ./... && go run ./cmd/daimon demo'
```

## Arquitetura

- `cmd/daimon`: escolhe Scripted ou provider, monta Registry, Budget, sink,
  política e aprovação; lê ambiente e conecta stdin/stdout/stderr.
- `internal/model`: mensagens, descrições de ferramentas, `Model.Generate` e `Scripted`.
- `internal/tools`: `Tool.Execute`, Registry determinístico, echo, read_file, list_dir e replace_file.
- `internal/editcontract`: proposta imutável, preview completo, aprovação de uso único
  e substituição via temporário/rename em Linux.
- `internal/policy`: `ToolPolicy` estática, `ApprovalProvider`, authorizer composto
  e aprovação de terminal. Implementa o `ToolAuthorizer` que o loop conhece.
- `internal/agentloop`: loop síncrono, Budget, autorização, limites de histórico,
  erros e eventos tipados.
- `internal/providers/openai`: configuração, HTTP e tradução privada do protocolo.
  Implementa `model.Model`; não executa ferramentas. Model e agentloop não importam providers.

O modelo recebe `ModelRequest` com cópias do histórico e descrições de ferramentas;
não recebe Registry nem implementações. `ToolCall.Arguments` é `json.RawMessage`.
O Registry preserva a ordem de registro e rejeita duplicatas.
Configure os componentes antes de executar; não há suporte a uso concorrente.

O loop recebe explicitamente `Budget: agentloop.DefaultBudget()`.
A configuração anterior `Loop.MaxSteps` foi substituída por `Loop.Budget.MaxSteps`.
Não há default implícito para um budget vazio.

## Chat OpenAI-compatible — non-streaming

### Smokes reais: Groq por padrão

`go run ./cmd/daimon smoke "mensagem"` usa Groq em
`https://api.groq.com/openai/v1`, com `openai/gpt-oss-20b` por padrão.
`DAIMON_API_KEY` é obrigatória; `DAIMON_GROQ_MODEL` pode trocar o modelo.
Esse comando ignora DAIMON_BASE_URL e DAIMON_MODEL, evitando reutilizar uma
configuração antiga de outro serviço. `chat` mantém sua configuração explícita.
O pacote `internal/providers/groq` reutiliza o transporte e protocolo existentes.

Em PowerShell 7, informe a chave sem gravá-la em arquivos ou no histórico:

```powershell
$env:DAIMON_API_KEY = Read-Host "Chave Groq" -MaskInput
go run ./cmd/daimon smoke "Use a ferramenta echo com text DAIMON e depois responda apenas DAIMON."
Remove-Item Env:DAIMON_API_KEY
```

Smokes são opt-in e usam rede/cota; não fazem parte de go test ou do CI.
Cada chamada ao modelo consome um request. Não há retry automático ou fallback.
Use workspace de teste com arquivos inofensivos: resultados aprovados são enviados
ao serviço remoto. list_dir e read_file continuam exigindo aprovação por chamada.
Comece com echo; depois teste manualmente listagem aprovada/negada e leitura de
arquivo pequeno. Mantenha a suíte determinística offline para regressões frequentes.


O adapter envia `POST {base_url}/chat/completions`, preservando o prefixo configurado,
como `/v1`. Usa o formato de [Chat Completions](https://developers.openai.com/api/reference/cli/resources/chat),
sem SDK: `model`, `messages`, `tools` quando presentes e `stream: false`.
Este suporte é ao protocolo, não uma garantia de compatibilidade com todo serviço.

Somente a CLI lê:

| Variável | Uso |
|---|---|
| `DAIMON_BASE_URL` | Obrigatória; prefixo da API, sem `/chat/completions` |
| `DAIMON_MODEL` | Obrigatória; nome disponível no serviço escolhido |
| `DAIMON_API_KEY` | Credencial; pode ser omitida para provider local sem autenticação |

Exemplo local em PowerShell, com o servidor compatível já em execução:

```powershell
$env:DAIMON_BASE_URL = "http://127.0.0.1:11434/v1"
$env:DAIMON_MODEL = "modelo-local"
Remove-Item Env:DAIMON_API_KEY -ErrorAction SilentlyContinue
go run ./cmd/daimon chat "Responda apenas DAIMON"
```

Exemplo remoto genérico em PowerShell 7 (substitua URL/modelo pelos do serviço):

```powershell
$env:DAIMON_BASE_URL = "https://provider.example/v1"
$env:DAIMON_MODEL = "modelo-disponivel-no-servico"
$env:DAIMON_API_KEY = Read-Host -Prompt "API key" -MaskInput
go run ./cmd/daimon chat "Responda apenas DAIMON"
Remove-Item Env:DAIMON_API_KEY
```

A chave vazia destina-se a serviços locais sem autenticação; para uso remoto,
configure a credencial exigida pelo serviço. O adapter só envia Authorization
quando há chave. Não há argumento CLI para chave, armazenamento de secrets ou
impressão da configuração. A CLI também oculta a chave se ela for ecoada na resposta.

**Enviar a um provider remoto transmite a mensagem e todo o histórico aceito,
incluindo resultados de ferramentas.** O chat registra `read_file` e `list_dir`
no diretório atual; ambos exigem aprovação humana por chamada, exibida no stderr
com os argumentos sanitizados. `replace_file`, quando habilitado explicitamente, exige aprovação do preview
completo; substitui um arquivo existente em Linux, com uma proposta por run.
No Windows a escrita falha fechado, sem temporário. `echo` executa automaticamente; qualquer outra
ferramenta é negada. Execute somente em um workspace apropriado e com um serviço
confiável. O confinamento ao workspace não é uma política de privacidade para os
arquivos que estão dentro dele. Não existe opção de aprovar tudo nem de lembrar
decisões.

### Tradução e semântica

- Preserva ordem de mensagens, ferramentas, calls, nomes, IDs e correlação.
- `user` e assistant final enviam content string. Assistant com calls omite content.
  Resultados usam `role: tool`, `tool_call_id` e content, sem campo IsError.
- Ferramentas são `type: function`; parameters conserva o schema como JSON objeto.
  Nomes vazios e schemas inválidos são rejeitados antes de HTTP.
- Arguments são strings no protocolo externo. O adapter não interpreta seu JSON;
  até argumentos malformados são preservados para que o loop produza erro controlado
  e envie a conversa na chamada seguinte. Arguments ausentes/null são inválidos.
- Mensagens internas incompatíveis, roles desconhecidas, ausência de ToolCallID,
  assistant vazio ou misturando texto/calls são rejeitados antes do envio.
- Respostas usam a primeira choice. Content pode ser string ou null com tool calls.
  Campos desconhecidos e finish_reason são ignorados; a forma da mensagem define
  texto final versus ferramentas. Conteúdo multimodal, tool types diferentes de
  function, resposta vazia/ambígua e campos obrigatórios ausentes são recusados.
- Nenhuma ferramenta é executada no adapter. Não há retry, fallback ou streaming.

### Transporte, limites e erros

BaseURL não aceita userinfo, query, fragment, host vazio ou schemes fora de HTTP/HTTPS.
HTTP é permitido apenas para localhost, 127.0.0.0/8 e ::1; destinos remotos exigem HTTPS.
Não há resolução DNS para tentar tornar um hostname remoto elegível a HTTP local.
O cliente padrão não segue redirects (nem no mesmo host), tem headers limitados a
64 KiB e usa verificação TLS padrão. Envia Content-Type/Accept application/json,
User-Agent daimon/0.3 e, quando configurado, Bearer Authorization.

`Config.HTTPClient` permite injeção. Sua política de redirects, proxy, TLS e timeout
é responsabilidade do chamador; preserve a restrição de redirects ao usar credenciais.
O adapter não altera o cliente recebido, não usa http.DefaultClient e não define
Client.Timeout. O request usa exatamente o contexto recebido do Budget. Ctrl+C
cancela o contexto da CLI. Configure cliente/transporte antes do uso.

`MaxResponseBytes` é obrigatório e positivo no adapter; a CLI usa **2 MiB**. A leitura
é limitada a esse valor + 1, independentemente de Content-Length, antes de decodificar.
O limite é inclusivo e também se aplica a bodies de erro e ao conteúdo descomprimido
pelo transporte. Bodies são fechados em sucesso e falha. O maior int64 é rejeitado
para evitar overflow ao somar 1.

Esse é um limite do body HTTP. O Execution Budget continua aplicando limites próprios
ao texto final, argumentos, ferramentas e histórico após a tradução. Um body grande
gera erro de tamanho antes da validação de status/JSON; o erro conserva o StatusCode.

| Erro do pacote openai | Significado |
|---|---|
| `ConfigError` | Configuração inválida, sem ecoar seu valor |
| `RequestError` | Mensagem/schema ou codificação interna inválida; não envia HTTP |
| `TransportError` | Falha HTTP/leitura; Unwrap preserva a causa |
| `HTTPError` | Status fora de 2xx; StatusCode e metadados limitados |
| `ResponseTooLargeError` | Body acima de MaxResponseBytes |
| `JSONError` | Body vazio, JSON malformado ou valores JSON concatenados |
| `ProtocolError` | JSON válido com estrutura/conteúdo não suportado |

Todos permitem `errors.As`. Cancelamento/deadline continuam verificáveis com
`errors.Is`. Error() não inclui URL, chave ou corpo. HTTPError conserva x-request-id,
code, type e Retry-After como metadados ASCII sanitizados, com no máximo 128 bytes
por campo; não usa a mensagem livre do servidor. Não há espera nem retry por status.
A causa original de transporte fica acessível via Unwrap para diagnóstico: não a
registre indiscriminadamente, pois erros de net/http podem conter URLs.

## Execution Budget

| Campo | Padrão |
|---|---|
| MaxSteps | 8 chamadas ao modelo |
| MaxToolCallsPerStep | 4 |
| MaxTotalToolCalls | 16 |
| MaxUserMessageBytes | 32 KiB |
| MaxFinalAnswerBytes | 256 KiB |
| MaxToolArgumentBytes | 64 KiB por chamada |
| MaxToolResultBytes | 64 KiB por resultado |
| MaxHistoryMessages | 128 |
| MaxHistoryBytes | 2 MiB |
| MaxRunDuration | 5 minutos |
| MaxModelCallDuration | 2 minutos |
| MaxToolCallDuration | 30 segundos |

Todos os campos precisam ser positivos. Zero e valores negativos são inválidos.
Budget inválido falha antes da primeira chamada ao modelo.

Uma chamada ao modelo é um passo, inclusive em falha. Chamadas de ferramenta são
sequenciais e pertencem ao passo que as solicitou. `Result.ToolCalls` conta
tentativas iniciadas, incluindo ferramenta desconhecida, argumentos inválidos,
erros controlados e chamadas interrompidas. Lotes rejeitados não consomem chamadas.

IDs e nomes precisam ser não vazios/não brancos. IDs são únicos durante toda a
execução. O lote inteiro é validado antes da primeira ferramenta: quantidade por
passo, total, argumentos individuais e capacidade de histórico. Falha em qualquer
limite impede todas as ferramentas daquele lote.

O histórico conta bytes usando `len`: Content, ToolCallID e IDs, nomes e argumentos
das calls. Conta bytes do protocolo, não memória da struct, JSON serializado ou tokens.
Antes do lote, reserva-se a mensagem assistant e um recibo por call contendo
`len(ID) + MaxToolResultBytes`. A reserva é conservadora mesmo para ferramentas que
normalmente retornam pouco. A aritmética de capacidade evita overflow; valores de
diagnóstico saturam no maior int da plataforma.

A mensagem inicial também precisa caber no histórico. Uma resposta final deve ser
não branca, sem calls, respeitar seu limite e caber integralmente no histórico.
Texto junto de calls é inválido. Respostas finais grandes falham sem truncamento
e sem serem adicionadas ao histórico.

Resultados de ferramentas, inclusive erros controlados, são normalizados para UTF-8
válido e limitados em bytes. Toda alteração recebe um marcador:
`\n[truncated: original_bytes=N]`; se não couber, `[truncated]`; para limites menores
que esse marcador, `~`. O marcador está incluído no limite. Original_bytes mede a
entrada antes da normalização. `TruncatedToolResults` conta resultados modificados,
inclusive normalização de bytes inválidos abaixo do limite.

## Autorização de ferramentas

Toda execução exige um `ToolAuthorizer` explícito. Configuração sem autorizador
falha antes da primeira chamada ao modelo.

O lote completo de ferramentas válidas é autorizado antes da execução da primeira
ferramenta. As decisões possíveis nesta versão são:

- `allow`: a ferramenta pode ser executada;
- `deny`: a ferramenta não é executada e recebe um resultado controlado `tool denied`.

A CLI compõe dois conceitos antes de responder `allow`/`deny` ao loop:

- `ToolPolicy`: decide `allow`, `deny` ou `require approval` por nome de ferramenta.
  Padrão do chat: `echo` → allow, `list_dir` e `read_file` → require approval; `replace_file` → deny,
  qualquer outra → deny. Política com fallback não configurado nega (zero value).
- `ApprovalProvider`: pergunta ao humano quando necessário. A implementação de
  terminal exibe o pedido no stderr, sanitiza argumentos (controles e formatação
  Unicode substituídos, valores limitados), assume `No` como padrão, nega em EOF ou
  entrada inválida, não persiste decisão, não oferece “sempre permitir” e vale
  para uma única chamada. Cancelamento interrompe a aprovação e retorna o erro
  de contexto. Erros não contextuais do provider falham fechado como
  `AuthorizationError`, com mensagem limitada a passo e ferramenta.

Sequências de eventos:

```text
permitida diretamente pela política:
ToolAllowed → ToolRequested → ToolCompleted/ToolFailed

aprovada pelo humano:
ApprovalRequested → ApprovalGranted → ToolAllowed → ToolRequested → ToolCompleted

negada (pela política ou pelo humano):
ApprovalRequested → ApprovalDenied → ToolDenied → receipt controlado
  (sem Approval* quando a política nega diretamente)

ferramenta desconhecida ou argumentos JSON inválidos:
ToolRequested → ToolFailed
```

## Cancelamento, erros e encerramento

Timeouts usam `context.WithTimeoutCause`, mantendo deadlines anteriores do pai.
O contexto é verificado antes e depois de cada chamada, inclusive se o componente
retornar sucesso depois de expirar. Não há goroutine para interromper chamadas.

O cancelamento/deadline externo já observado tem precedência. Caso contrário,
a causa herdada identifica timeout do budget total, do modelo ou da ferramenta.
Timeouts do budget retornam `LimitError` e preservam
`errors.Is(err, context.DeadlineExceeded)`. Erros de contexto retornados diretamente
por ferramentas também encerram: cancelamento gera `canceled`; deadline próprio da
ferramenta gera `tool_timeout`, mesmo com contexto recebido ainda ativo.

Falhas normais do modelo retornam `*ModelError`, preservando passo e causa.
Ferramenta desconhecida, JSON inválido e erro normal de ferramenta retornam uma
mensagem tool com `IsError`, permitindo recuperação pelo modelo.
`ErrInvalidConfig`, `ErrInvalidResponse` e `ErrMaxSteps` continuam verificáveis com
`errors.Is`. `LimitError` expõe Kind, Limit e Actual por `errors.As`.
`errors.Is(err, LimitError{Kind: ...})` distingue o limite; Kind vazio aceita qualquer
limite. Em timeout, Actual registra a duração limite alcançada.

Todo retorno define `Result.StopReason` e emite exatamente um `loop_stopped` com
a mesma razão, inclusive configuração inválida e mensagem inicial rejeitada:

- `completed`, `invalid_config`, `authorization_error`, `invalid_response`,
  `model_error`;
- `canceled`, `external_deadline`, `run_timeout`, `model_timeout`, `tool_timeout`;
- `max_steps`, `max_tool_calls`, `user_message_limit`, `argument_limit`,
  `final_answer_limit`, `history_limit`.

O resultado preserva o histórico já aceito, também em falhas. Interrupção durante
um lote pode deixar calls sem recibo; não se inventam resultados nem se executam
as chamadas restantes. A reserva garante espaço, não conclusão após cancelamento.

## Observabilidade e ferramentas

EventSink tem implementações Noop e Memory, sem emissor global. O demo emite:

```text
loop_started → model_requested → model_responded → tool_allowed → tool_requested
→ tool_completed → model_requested → model_responded → final_answer → loop_stopped
```

Eventos de aprovação (`approval_requested`, `approval_granted`, `approval_denied`)
são emitidos pelo authorizer composto, não pelo loop, e obedecem à mesma regra
de conteúdo. Erros controlados usam `tool_failed`. Interrupção pode deixar
requested sem conclusão. Eventos contêm somente tipo, passo, posição de ferramenta
e razão de encerramento; não incluem mensagens, nomes/IDs fornecidos pelo modelo,
argumentos, motivos de decisão, outputs ou erros.
O sink deve ser local, rápido e aceitar encerramento com contexto cancelado.
O histórico e os requests, ao contrário, contêm dados potencialmente sensíveis.

`echo` exige um objeto com text string. `read_file` exige path relativo e usa
`os.Root` para confinar a resolução de symlinks ao workspace; symlinks internos são
permitidos. Bloqueia caminhos absolutos, traversal, diretórios, arquivos não regulares
e arquivos acima do limite. `list_dir` compartilha o mesmo confinamento e rejeita
os mesmos caminhos; a listagem é não recursiva, ordenada por nome, rotula
arquivo/diretório/symlink sem seguir links, e falha integralmente quando excede
os limites de entradas ou de bytes, sem cortes silenciosos. Ambos rejeitam campos
desconhecidos/duplicados, campos obrigatórios ausentes, null e tipos inválidos.
O proprietário de ReadFile e ListDir deve chamar Close.

## CI e testes

`.github/workflows/ci.yml` roda em Ubuntu 24.04 a cada push e pull request, usando
o Go de go.mod. Verifica formatação (falha se houver arquivos listados), vet,
testes e race detector, sem cache de resultados dos testes. Tem timeout de
15 minutos, permissões de leitura e Actions fixadas por SHA.
O check `Go / Linux` pode ser exigido nas regras de proteção do repositório.

Os testes preservam os cenários da v0.1 e acrescentam budget inválido, limites
exatos/excedidos, pré-validação de lotes, reserva com IDs, overflow, UTF-8, marcadores,
erros tipados, StopReason e timeouts de execução/modelo/ferramenta. Testes de deadline
aguardam ctx.Done; não dependem de corrida contra sleeps arbitrários.
Os testes do provider e do chat usam httptest.Server local para validar protocolo,
headers, erros, cancelamento, redirects, limites e o ciclo com ferramentas.
Nenhum teste acessa internet ou depende de um provider externo.

## Limites e próximo passo

Há um preview ASCII reversível em `internal/diffview` e um contrato de edição
em `internal/editcontract`: proposta imutável, display completo, aprovação de uso
único e revalidação do arquivo. `replace_file({"path":"arquivo.txt","content":"novo conteúdo"})`
prepara e aprova antes do Execute; o executor substitui o original via temporário
no mesmo diretório e rename. Entrada original e conteúdo final têm limites separados
de 64 KiB na CLI, preview completo de 1 MiB e até 1000 linhas. O limite de argumentos
JSON do loop também se aplica. Não cria um novo alvo, não edita em lote nem permite
segunda tentativa de proposta na mesma execução.
O [contrato de edição](docs/workspace-edit-contract.md) descreve erros, cleanup,
cancelamento, Linux como plataforma de escrita e limitações de concorrência/durabilidade.

Esta fundação não oferece sandbox de processo, limite rígido de alocação de memória,
tokenização, sumarização ou persistência. Um componente pode alocar
uma resposta grande antes de devolvê-la; o budget limita o que o loop aceita e armazena.
Cancelamento é cooperativo e não interrompe um componente que ignora o contexto.
Valores padrão são pontos de partida experimentais, não garantias de produção.

os.Root não impede hard links nem transforma um workspace hostil em sandbox.
Implementações fornecidas devem respeitar os contratos; não há recuperação de panics
de código arbitrário nem suporte a interfaces contendo ponteiros nil tipados.

Não existem banco, memória vetorial, gateway, subagentes, MCP, servidor, UI,
streaming/SSE, Responses API, retry, fallback, múltiplos providers simultâneos,
shell, edição em lote, criação/exclusão/movimentação de arquivos do usuário,
aprovação permanente ou configuração de
política em arquivo. A compatibilidade foi testada com servidores locais
simulados; os smokes Groq anteriores estão registrados no contrato. Nenhuma credencial
é necessária para validar o projeto. O smoke de substituição usa servidor HTTP local
e fixture temporária. Não há garantia de exclusão de escritores externos entre a
última validação e rename, nem rollback após commit ou durabilidade em queda de energia.

### Chat com substituição opt-in

`go run ./cmd/daimon chat --enable-replace-file "proponha a substituição de arquivo.txt"`
expõe replace_file ao provider OpenAI-compatible somente nesta execução. Chat comum,
smoke e demo não registram a ferramenta; a política padrão nega a substituição.
A opção não aprova escrita: a proposta exige preview integral no stderr e resposta
explícita y. EOF, resposta inválida, display parcial/falho, cancelamento e revalidação
negativa impedem a escrita. Uma única proposta pode ser tentada por run.

O provider recebe o schema, a mensagem do usuário, os argumentos que ele próprio
produziu e o recibo controlado necessário para a resposta final. O runtime não envia
original, diff, identidade do arquivo ou preview como mensagens adicionais. read_file
continua enviando seu resultado quando aprovado; a resposta final pode repetir conteúdo
que o modelo já conhece. Eventos e erros locais não incluem conteúdo; somente o preview
deliberado exibe original e proposta com escapes reversíveis. Opt-in não garante
confidencialidade perante o provider. Testes usam httptest sem credencial/request externa.

Garantias mantidas: aprovação exata e única, limites, confinamento e escrita atômica
em Linux. Limites assumidos: workspace controlado, sem exclusão de escritor externo
entre revalidação e rename, sem rollback após commit ou durabilidade em perda de energia;
ACLs, ownership e xattrs não são preservados. Fora de escopo: outras plataformas de
escrita, novos alvos, exclusão, movimentação, lote, shell e permissões persistentes.

### Sessão efêmera de workspace

`daimon workspace --root "<diretório>" "mensagem"` usa exclusivamente o root explícito
para list_dir e read_file, com aprovação por chamada. O diretório precisa existir e
poder ser aberto com os.Root antes de configurar o provider. Não há root implícito.
`daimon workspace --root "<diretório>" --enable-replace-file "mensagem"` também registra
replace_file: a flag não concede aprovação nem dispensa o preview integral.
Texto da mensagem, modelo e ambiente não ativam escrita.

Ao contrário de chat, que imprime a resposta final do modelo e usa o diretório atual,
workspace imprime somente um resumo público: stop reason, passos, tool calls,
aprovações solicitadas/concedidas/negadas, tentativas de Execute por tipo fixo,
escritas concluídas, truncamentos e duração arredondada em segundos. Negativas não
contam como Execute. Falhas iniciadas contam; escrita concluída é contada quando o
executor confirma o commit, inclusive se o loop observar cancelamento logo depois.
Runs interrompidos também produzem resumo quando o loop foi iniciado. Erros prévios
à execução não produzem resumo. A resposta livre do modelo é deliberadamente omitida,
pois pode repetir dados conhecidos. Prompts de leitura e preview de edição mantêm
seus displays explícitos no stderr; não são parte do resumo.

A sessão não persiste contadores, eventos, permissões, histórico ou configuração.
Ferramentas de leitura aprovadas ainda enviam resultados ao provider configurado;
não é um modo offline para uso real. A validação usa somente httptest offline.
O root e o filesystem precisam estar sob controle do usuário. O executor Linux não
foi alterado: permanece sem compare-and-rename contra escritor hostil, rollback após
commit, durabilidade em perda de energia ou preservação de ACL/ownership/xattrs.
Não há shell, lote, criação/exclusão/movimentação de alvos, integrações adicionais,
retry/fallback, memória ou múltiplos workspaces. Falhas de abertura não expõem root.
