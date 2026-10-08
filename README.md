# DAIMON

DAIMON é a fundação experimental de um **Sovereign Personal Agent** em Go:
execução sob controle do usuário e contratos independentes de provedor.
**Ainda não é um agente pessoal pronto.**

O corte atual reúne Reliable Agent Loop, Execution Budget, um adapter
OpenAI-compatible para Chat Completions sem streaming e a primeira capacidade
real de exploração de workspace com aprovação humana:
mensagem → modelo → ferramentas opcionais → resultados → modelo → resposta final.
Usa somente a biblioteca padrão, um modelo programável e as ferramentas
`echo`, `read_file`, `list_dir`, `replace_file` e `create_file`. O demo e os testes não requerem credenciais
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
- `internal/tools`: `Tool.Execute`, Registry determinístico, echo, read_file, list_dir, replace_file e create_file.
- `internal/editcontract`: proposta imutável, preview completo, aprovação de uso único
  e substituição via temporário/rename em Linux.
- `internal/createcontract`: contrato separado de ausência, preview e criação exclusiva
  aprovada de um arquivo novo em Linux.
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
  Padrão do chat: `echo` → allow, `list_dir` e `read_file` → require approval; `replace_file` e `create_file` → deny,
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

Esta fundação não oferece isolamento de processo, limite rígido de alocação de memória,
tokenização, sumarização ou persistência. Um componente pode alocar
uma resposta grande antes de devolvê-la; o budget limita o que o loop aceita e armazena.
Cancelamento é cooperativo e não interrompe um componente que ignora o contexto.
Valores padrão são pontos de partida experimentais, não garantias de operação.

os.Root não impede hard links nem isola um workspace hostil.
Implementações fornecidas devem respeitar os contratos; não há recuperação de panics
de código arbitrário nem suporte a interfaces contendo ponteiros nil tipados.

Não existem banco, memória vetorial, gateway, subagentes, transportes MCP remotos,
streaming de tokens do provider, Responses API, retry, fallback, múltiplos providers simultâneos,
shell, edição em lote, criação além de create_file, exclusão/movimentação de arquivos do usuário,
aprovação permanente ou configuração de
política nativa em arquivo. A compatibilidade foi testada com servidores locais
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
Não há shell, lote, criação além de create_file, exclusão/movimentação de alvos, integrações adicionais,
retry/fallback, memória ou múltiplos workspaces. Falhas de abertura não expõem root.

### Planejamento read-only

`daimon workspace --root "<diretório>" plan "analisar a mudança"` abre uma sessão
efêmera de diagnóstico. O runtime solicita ao modelo diagnóstico, objetivo,
arquivos prováveis, mudança pretendida por arquivo, riscos/suposições, validação e
bloqueios. A resposta é texto livre: nenhuma validação semântica, parser, arquivo,
cache, persistência ou execução do plano é realizada.

Somente echo, list_dir e read_file são registrados; --enable-replace-file é
incompatível com plan. Pedidos de escrita no texto ou calls maliciosas não mudam
schema nem política. Ferramenta ausente gera recibo de ferramenta desconhecida.
Root e formato do comando são validados antes de configurar o provider; argumentos
inválidos são rejeitados antes de abrir o workspace. Leituras continuam aprovadas
por chamada. Os displays deliberados de aprovação mostram o tipo da ferramenta,
o caminho relativo integral com escapes ASCII reversíveis e o aviso de envio do
resultado ao provider. Os argumentos originais são preservados para execução.

A saída deliberada `Plano proposto` mostra o texto do modelo, seguida pelo resumo
público existente. O plano pode conter nomes de arquivos e dados conhecidos pelo
modelo; não deve ser tratado como um log público. Resumo, eventos e erros públicos
não exibem esses dados. A aprovação é um display privado deliberado para a pessoa
que precisa conhecer o alvo. Falhas do run não exibem um plano parcial.
Leituras aprovadas ainda enviam seus resultados ao provider; plan não acrescenta
mensagens com dados do workspace além do protocolo existente. Testes usam somente
httptest e fixtures locais, sem credenciais reais ou requests externos.

Diferenças: chat usa cwd e imprime resposta final; workspace exige root e imprime
somente resumo; workspace plan exige root, solicita e exibe plano deliberado sem
ferramenta de escrita; workspace com --enable-replace-file disponibiliza substituição
Linux com preview/aprovação, sem execução automática de planos. Um plano não concede
permissão futura. Limitações do executor permanecem inalteradas e fora deste corte.

### Criação aprovada e opt-in

`daimon workspace --root "<diretório>" --enable-create-file "proponha um arquivo novo"`
registra create_file somente nesta sessão. A flag não concede autorização: a proposta
precisa de preview integral e aprovação explícita por chamada. Chat, workspace normal,
plan e --enable-replace-file não expõem criação. Neste corte, as duas flags de escrita
não podem ser combinadas; nenhum opt-in implica o outro. Plan permanece incompatível
com ambas. O modelo usa {"path":"novo.txt","content":"conteúdo"}.

replace_file substitui uma versão existente aprovada usando temporário/rename.
create_file usa contrato separado de inexistência: caminho relativo canônico, root
aberto, pais seguros já existentes, bytes copiados, limites e aprovação de uso único.
Nunca cria diretórios nem sobrescreve qualquer alvo existente. Uma proposta/tentativa
por instância/run; dois creates no mesmo lote falham antes de executar ferramentas.

Somente Linux. Criação direta exclusiva O_CREATE|O_EXCL|O_RDWR, chmod explícito 0600,
chunks com checagens de contexto, Sync, leitura limitada para hash/verificação exata,
Close e validação final de identidade/metadados/pais. O arquivo fica visível durante
a gravação: não é publicação atômica de conteúdo completo. Falhas tentam remover
somente o arquivo recém-criado identificado; outra identidade, symlink ou hard link
impede limpeza e reporta ErrCleanup. Limpeza recusada pode deixar resíduo, nunca
sucesso falso. Não há proteção contra escritor hostil entre verificações, exclusão
de cleanup atômica, rollback depois do sucesso ou durabilidade em perda de energia.

Limites CLI: conteúdo UTF-8 64 KiB, 1000 linhas, path 4096 bytes, preview 1 MiB;
budget de argumentos JSON pode restringir conteúdo antes disso. Permissões finais
0600 são verificadas e testadas com umask 000 e 777. Deadline obrigatório. Cancelamento,
conflito ou erro consomem a capacidade; sem retry/fallback. Resumo agrega commits
confirmados em Escritas confirmadas e separa Execute por tipo incluindo create_file.

Preview é a exibição deliberada de ausência, conteúdo completo com escapes ASCII,
limites e avisos. Eventos, recibos, erros e resumo não incluem os dados. O provider
já conhece os argumentos que produziu; recebe somente o recibo adicional controlado,
sem injeção pelo runtime de preview, conteúdo ou identidade. A resposta final livre
continua omitida no workspace. Testes usam somente httptest/fixtures locais.
O [contrato de criação](docs/workspace-create-contract.md) detalha garantias e erros.

### Aprovação informada e privacidade

Privacidade de logs não implica ocultar a ação da pessoa que precisa aprová-la.
Para read_file/list_dir, o display de aprovação identifica o caminho relativo
integral e informa que o resultado aprovado será enviado ao provider. O conteúdo
não aparece nesse display. Caminhos usam QuoteToASCII de Go, incluindo controles,
Unicode, espaços, aspas e escapes. O display inteiro tem limite de 32768 bytes;
excesso ou escrita incompleta impede aprovação e execução, sem corte silencioso.
Negativa, EOF e entrada inválida continuam negando; cancelamento interrompe o run.

Há quatro superfícies distintas: aprovação privada deliberada; plano textual
deliberado; eventos/resumo público; erros públicos. Só as duas primeiras podem
mostrar os dados deliberados descritos acima. Permissões do preview de substituição
são octais (0600/0640/0644); bytes do display atualizado continuam sujeitos ao
limite integral de preview, e a proposta interna aprovada permanece imutável.

Erros públicos de workspace usam somente categorias tipadas, preservando a causa
por Unwrap: conflito, mudança de alvo, limite, cancelamento, prazo, aprovação e
limpeza. Erro livre nunca é usado para inferir diagnóstico. Negação e EOF são
recibos controlados, não falhas de autorização. Falha de revisão nem sempre permite
distinguir erro de display de outro erro do reviewer; nesse caso a explicação
permanece genérica. Contadores de escritas confirmadas devem ser consultados mesmo
após falha: cancelamento não desfaz sucesso confirmado e limpeza pode deixar resíduo.
Identificadores, flags, ferramentas e recibos de protocolo não são traduzidos.

A validação operacional usou provider HTTP simulado, não um LLM real. Permanecem
as limitações de workspace controlado, concorrência externa, visibilidade durante
criação, durabilidade e metadados descritas nos contratos. Git read-only permanece
bloqueado até existir isolamento real de processo e filesystem; não faz parte deste corte.

### Instruções operacionais de workspace

Cada request de workspace usa uma mensagem system separada, identificada no código
como DAIMON workspace protocol v1, seguida da mensagem livre do usuário intacta.
Há orientações distintas para plan, workspace read-only, criação e substituição.
Chat, demo e smoke não recebem essa instrução de workspace. Model.Generate, o
histórico do loop, as ferramentas e seus schemas permanecem iguais.

As orientações pedem tool calling estruturado em vez de autorização em linguagem
natural, evidência de ferramenta ou declaração explícita do usuário antes de
alegar fatos, distinção de fato/inferência/hipótese e bloqueio quando faltam dados.
Plan solicita sete seções em ordem: Diagnóstico; Objetivo da mudança; Arquivos
prováveis; Alteração proposta por arquivo; Riscos e suposições; Validação proposta;
Bloqueios ou informações faltantes. Criação e substituição orientam somente o
arquivo/alteração solicitado, sem inventar comportamentos ou efeitos adicionais.

O adapter aceita instrução UTF-8 de até 8192 bytes, como configuração explícita do
chamador. Esse overhead estático de transporte é limitado separadamente e não é
contado como histórico aceito pelo budget do loop. O CLI fornece somente constantes,
sem segredos, caminhos pessoais ou dados do host. O runtime não imprime essa
configuração em eventos, resumo, erros ou aprovação. A mensagem system é enviada
novamente em cada request; não é memória persistente.

Instruções não garantem adesão do modelo nem mitigam completamente alucinações.
Planos e propostas continuam não confiáveis até revisão humana. Não há parser
semântico, formato JSON obrigatório, validação por outro LLM ou retry. Uma resposta
não aderente não provoca execução automática nem reduz policy, budget, aprovação,
preview, binding, revalidação ou confinamento. Conteúdo não relacionado deve ser
preservado em substituições, salvo pedido explícito do usuário.

Na validação real com Groq/openai/gpt-oss-20b, o modelo entregou um plano incompleto,
pediu autorização em linguagem natural e propôs documentação com integração em
testes e limpeza automática não confirmadas. A proposta foi negada e a sessão
interrompida; nenhuma escrita ocorreu. Esses achados motivam o protocolo, mas não
comprovam sua eficácia: este corte é validado com httptest/fixtures, sem nova chamada
real. A aprovação humana e os contratos de escrita continuam sendo a barreira.

A orientação de plan inclui DAIMON plan discovery v2: o root já foi definido,
caminhos fornecidos são alvos relativos e a investigação começa por list_dir "."
quando faltar contexto, seguida apenas das listagens e leituras necessárias.
Nomes vistos em listagem não confirmam conteúdo nem comportamento. Negativa,
falha, limites ou dúvidas remanescentes são Bloqueios; não justificam insistir na
aprovação. Não há sequência automática de ferramentas nem obrigatoriedade de ler
arquivos sem necessidade. Os outros modos mantêm suas instruções anteriores.

Esse reforço responde ao relato operacional do usuário de uma sessão concluída
em um passo, sem ferramentas ou escritas, que pediu um caminho já informado.
O relato evidencia falta de adesão à tarefa, não um defeito confirmado do runtime.
Os testes locais verificam o envio da orientação; melhora de adesão do modelo real
continua pendente de nova validação pelo operador.

### Validação restrita de escopo em plan (opt-in)

`daimon workspace --root "diretório" plan --validate-scope "pedido"` habilita
um contrato estruturado somente quando o pedido inteiro corresponde ao template.
A forma equivalente `daimon workspace --root "diretório" plan "pedido" --validate-scope`
também é aceita. Sem a flag, `plan "pedido"` mantém o comportamento anterior.
Forneça exatamente um argumento de pedido não vazio: a CLI não junta argumentos
nem remove espaços ou modifica UTF-8. Flags desconhecidas, duplicadas e argumentos
extras são recusados. No shell Linux, use `"$DAIMON_PLAN_REQUEST"` para preservar
o pedido inteiro como um argumento; a variável precisa estar definida no processo
do shell que executa o binário.

Template reconhecido:

```text
Analise esta fixture. Proponha criar um arquivo curto de documentacao em <diretorio>/ e alterar somente <arquivo> de <antigo> para <novo>. Nao execute alteracoes. Use ferramentas somente quando precisar de evidencia. Registre incertezas em Bloqueios.
```

A grafia, pontuação e espaços do template são intencionais: não há interpretação
geral de linguagem natural. Caminhos são relativos canônicos ASCII (letras,
dígitos, ponto, hífen, underscore e barra); os literais têm de 1 a 128 caracteres
ASCII alfanuméricos ou `_`, `=`, `+`, `-`. Pedidos diferentes, inclusive outra
grafia, continuam no fluxo anterior sem essa validação. A flag não é permissão
de ferramenta nem ativa escrita. Sem a flag, mesmo o template mantém o fluxo antigo.
Cada caminho tem no máximo 4096 bytes. Componentes com ponto final e nomes de
dispositivo Windows (CON, PRN, AUX, NUL, COM1–COM9, LPT1–LPT9, inclusive com
extensão) são recusados para evitar interpretações diferentes entre plataformas.

O modelo mantém as sete seções humanas em texto simples, com títulos únicos e
em ordem, e acrescenta exatamente um bloco final. Cada delimitador ocupa uma
linha própria; não há cercas markdown na resposta. Exemplo do bloco interno:

```text
<daimon-plan-scope>
{"create":["docs/NOTES.md"],"modify":["src/config.txt"],"delete":[],"operations":[{"path":"src/config.txt","old":"mode=initial","new":"mode=final"}],"blockers":[]}
</daimon-plan-scope>
```

Todos os campos são obrigatórios. O validador exige uma criação diretamente no
diretório indicado, uma única modificação no arquivo indicado, a troca literal
exata e nenhuma exclusão. Rejeita JSON ambíguo com chaves duplicadas, campos
desconhecidos, caminhos não canônicos e operações adicionais. Bloqueios são
códigos fechados: `information_missing`, `read_denied`, `read_failed` ou
`budget_exhausted`; falta de acesso de escrita não é bloqueio.
O JSON tem limite de 16384 bytes e profundidade máxima de quatro níveis. Os
campos têm nomes exatos: cinco no objeto principal e três por operação. Há uma
criação, uma modificação, uma operação, zero exclusões e até quatro bloqueios
distintos. A resposta inteira permanece limitada pelo budget de resposta final.

O contrato é conferido antes da exibição. Uma resposta incompatível é retida e
permite no máximo uma recuperação explícita no mesmo histórico, sem repetir ou
resumir o pedido original. Essa chamada conta como passo/request dentro do budget
existente, usa schema sem ferramentas e rejeita qualquer tool call antes da
autorização. Não há reserva adicional de passos, retry de transporte ou fallback.
Nova incompatibilidade, interrupção ou limite impede exibir o plano. Erros de
transporte ou JSON HTTP inválido encerram sem retry, inclusive na recuperação.
Eventos e resumo continuam contendo somente contadores. O bloco interno é
validado, retido no histórico necessário ao protocolo e removido da exibição;
somente as seções humanas são apresentadas como plano deliberado.

Limitação: a validação cobre o contrato estruturado e a presença/ordem das sete
seções, não a semântica da prosa livre, a verdade dos diagnósticos nem a fidelidade
da prosa ao bloco. Um modelo pode contradizer o bloco no texto; a revisão humana
continua necessária. O bloco não aprova, prepara ou executa escrita e não valida
existência dos alvos. A comparação de paths é lexical: não resolve symlinks,
não segue o filesystem e não comprova confinamento físico de uma futura escrita.
As ferramentas de leitura continuam confinadas pelo contrato existente de
os.Root; o validador não concede acesso a nenhum path. Este corte é testado apenas com fixtures e provider HTTP
simulado local; aderência de um LLM real não foi validada nesta rodada.

### Capabilities, diagnóstico e trabalho de conclusão do núcleo

A sessão workspace usa `policy.WorkspacePolicy`: leitura exige aprovação por
chamada; create/replace continuam exigindo suas flags e preview vinculado. Plan
nunca habilita escrita, mesmo com configuração interna inconsistente. Recovery
nunca recebe tools. Chat, smoke e demo mantêm a política anterior.

`daimon workspace --root "diretório" --diagnostic plan --validate-scope "pedido"`
ativa diagnóstico deliberado no stderr, apenas com reconhecimento, categorias de
falha e contagens. Não imprime prompt, paths, arquivos, IDs remotos ou chaves.
HTTP 404 indica endpoint ou modelo não encontrado, sem inferir qual é a causa.
Erros TLS, transporte, timeout, autenticação, rate limit, serviço e protocolo são
classificados quando existe evidência tipada, preservando a causa e StopReason.

O formato versionado e os limites internos de plano estão descritos em
[docs/workspace-core.md](docs/workspace-core.md). **Não existe comando apply-plan
habilitado neste estado.** Há um journal independente de metadados para sink
fornecido pelo chamador, sem abertura automática de arquivos nem execução de apply.
O requisito forte de confinamento
sob mutação externa concorrente ainda está bloqueado: mover um pai após a última
verificação pode causar escrita transitória pelo descritor aberto. A revalidação
detecta o problema e a limpeza pode remover o arquivo, mas não impede aquela
escrita. Os executores existentes continuam exigindo workspace controlado;
não há isolamento de processo/filesystem.

### Pedido longo em PowerShell e Docker

O fluxo offline de cópia privada está disponível separadamente como
`managed-workspace --base <store> create|apply|report`, somente em Linux amd64.
O proprietário, processos do mesmo UID e administradores são confiáveis.
A Daimon nunca modifica conteúdo ou permissões da origem nem a abre para escrita;
leituras podem atualizar atime. Apply em roots compartilhadas continua
bloqueado; publicação na origem não existe. Sintaxe, limites, artefatos e riscos
estão em [docs/managed-workspace.md](docs/managed-workspace.md).

Para testes manuais, prepare um diretório de artefatos fora do repositório com o
binário Linux recompilado, `plan.env` e `run-plan.sh`. Use `--env-file` e um script
montado readonly, em vez de embutir o pedido longo em `sh -lc`: PowerShell, Docker
e o shell do container podem interpretar quoting em etapas diferentes.

`plan.env` contém somente pedido e configuração não secreta. A sintaxe de
`docker --env-file` não é expansão de shell: **não coloque aspas em torno do
valor** e não espere interpolação de outras variáveis. Mantenha o pedido numa
única linha. Não inclua API keys nesse arquivo, não o versione nem o imprima.

```text
DAIMON_BASE_URL=https://api.groq.com/openai/v1
DAIMON_MODEL=openai/gpt-oss-20b
DAIMON_PLAN_REQUEST=Analise esta fixture. Proponha criar um arquivo curto de documentacao em docs/ e alterar somente src/config.txt de mode=initial para mode=final. Nao execute alteracoes. Use ferramentas somente quando precisar de evidencia. Registre incertezas em Bloqueios.
```

Salve `run-plan.sh` com finais de linha LF. O shell é preparação manual do
operador, não ferramenta concedida ao modelo. Este exemplo instala certificados
no container para o teste manual; não foi executado contra provider externo nesta
rodada. Nunca desative a verificação TLS.

```sh
#!/bin/sh
set -eu
: "${DAIMON_PLAN_REQUEST:?pedido ausente}"
apt-get update
apt-get install -y --no-install-recommends ca-certificates
exec /artifact/daimon-linux-amd64 workspace --root /workspace plan \
  --validate-scope "$DAIMON_PLAN_REQUEST"
```

No PowerShell, com credencial já configurada pelo operador somente no ambiente:

```powershell
$artifact = '<diretório absoluto de artefatos fora do repositório>'
$fixture = '<workspace descartável absoluto>'
docker run --rm -it `
  --env-file (Join-Path $artifact 'plan.env') `
  --env DAIMON_API_KEY `
  --mount "type=bind,source=$artifact,target=/artifact,readonly" `
  --mount "type=bind,source=$fixture,target=/workspace,readonly" `
  debian:bookworm sh /artifact/run-plan.sh
```

O pedido é expandido **dentro** do container por `"$DAIMON_PLAN_REQUEST"`, formando
um único argumento. A CLI preserva seus bytes. Plan e os dois mounts são
read-only; a credencial não aparece no comando nem nos arquivos do exemplo.

## Architecture direction

**Experimental — Current:** reliable local agent core em Go.
**Planned — Target:** plataforma local provider-agnostic Daimon Bots.
DAIMON não é um modelo; é o ambiente onde modelos externos se tornam agentes.

**Implemented:** contrato independente `model.Model`, loop com budget explícito,
tools/policies/aprovação, contratos locais de workspace, adapter OpenAI-compatible
e wrapper Groq. **Foundation implemented:** registry explícito de providers com
IDs estáveis, factories e configuração por chamada; o CLI usa essa construção
sem mudar comandos, flags, defaults ou autorização. Endpoints compatíveis podem
ser configurados explicitamente; isso não certifica todos os servidores/modelos.

**Implemented — Bot Domain:** `internal/bots` define configuração reutilizável,
validação central e store JSON local versionado com CRUD. Bot referencia provider
e modelo; não contém configuração de credenciais. Ferramentas e modos `ask` e
`read-only` são declarativos e não concedem autorização. Existência de providers
e ferramentas é resolvida pelo Session Runtime, separadamente da validação do domínio.

O store recebe caminho explícito em diretório existente e controlado. Arquivo
ausente significa coleção vazia; dados inválidos/versão desconhecida são recusados.
Listagem é ordenada por ID e resultados têm cópias defensivas. Gravação usa
temporário no mesmo diretório, modo 0600 onde aplicável, Sync/Close/rename, sem
truncar o arquivo anterior. Uso sequencial por um escritor; sem garantia de
atomicidade Windows, isolamento de escritores externos ou durabilidade de diretório.
Não coloque segredos nos textos livres: o schema não detecta credenciais em prosa.
O domínio ainda não tem comandos CLI, execução de bots ou interface de conversa.

**Implemented — Thread Domain:** `internal/threads` define metadata persistente
de conversa: ID, BotID, workspace explícito, título opcional e timestamps UTC.
Validação é estrutural; não consulta Bot Store nem abre workspace. O store JSON
versionado fornece CRUD, listagem ordenada e valores independentes, com limites
de 256 threads e 2 MiB. BotID, workspace e CreatedAt permanecem fixos; UpdatedAt
não retrocede e os timestamps são fornecidos pelo chamador. A gravação segue
temporário/Sync/Close/rename, com os mesmos limites de concorrência e plataforma
do Bot Store. Nenhum caminho padrão ou diretório do usuário é acessado implicitamente.

Thread continua sendo apenas metadata. A execução ocorre pelo Session Runtime
interno; mensagens persistentes pertencem ao Conversation Store separado. Cada Session fixa sua
própria configuração do Bot; não há snapshot de provider/model persistido na Thread.
Novos turns usam um sufixo limitado do transcript com o Bot atual.

**Implemented — Session Runtime:** `internal/sessions` monta Thread → Bot →
Provider/Model → ferramentas/policy → agentloop por código interno, com Start
assíncrono, lifecycle, binding por execução, cancelamento, Wait/Close e buffer ring
de eventos tipados com sequências e indicação explícita de gaps. Uma sessão ativa
por Thread; Budget e limites de buffer/sessões são explícitos. Leitura exige aprovação
individual; escrita exige opt-in por Start, Linux e preview completo aprovado.
`read-only` resolve somente ferramentas classificadas explicitamente como leitura.

Snapshots contêm apenas metadata/contadores; resposta final e histórico não são
retidos pelo manager. Configuração de credenciais é injetada separadamente, sem
persistência. O runtime permanece em memória: restart perde Sessions e buffers.
O runtime interno não possui UI. A Phase 5 adiciona o transporte HTTP descrito abaixo.
Veja [Session Runtime](docs/SESSION_RUNTIME.md)
para ownership, resolução, permissões e limites de cancelamento cooperativo.

**Implemented — Local HTTP Server:** `go run ./cmd/daimon serve` escuta somente em
`127.0.0.1:3000`. Aceita `--port` e `--data-dir`; padrão de estado `~/.daimon`, com
stores de Bots/Threads/conversas/Memory e Sessions somente em memória. `/api/v1` oferece
health, providers, CRUD Bots/Threads com PUT completo, início/consulta/abort de
Sessions e polling JSON limitado de eventos. Shutdown por SIGINT/SIGTERM drena
HTTP e cancela/aguarda o Manager. Os comandos anteriores mantêm seu comportamento.

Servidor local sem autenticação, destinado a clientes confiáveis; não exponha
publicamente nem por reverse proxy. Não há CORS nem API de credenciais.
`serve` exige aprovação individual para leitura; escrita nativa exige opt-in explícito. Instruções de Bot são aceitas
como configuração, mas omitidas nas respostas; binding nunca é retornado.
Transcript/resposta são dados deliberados somente no endpoint GET messages. Thread CRUD mostra deliberadamente o workspace configurado.
Config de provider vem somente do ambiente da CLI; modelo vem do Bot.
Veja [HTTP API v1](docs/HTTP_API_V1.md) para schemas, limites e segurança.

**Implemented — SSE Event Streaming:** GET `/api/v1/sessions/{id}/events/stream`
acompanha os mesmos eventos do ring, com sequência, Last-Event-ID/after, replay_gap
explícito e heartbeat. Disconnect não cancela Session. Estado terminal envia os
eventos restantes e stream_end; shutdown encerra streams antes de fechar o Manager.
Até 64 streams, payloads/deadlines limitados, sem filas por cliente ou polling ocupado.
O polling JSON permanece disponível. Não há streaming de tokens do provider.
Veja [SSE v1](docs/SSE_V1.md) para protocolo, limites e reconexão.

**Implemented — Web UI:** abra `/` após `go run ./cmd/daimon serve` para gerenciar
Bots e Threads, iniciar/abortar Sessions e observar eventos SSE. React/TypeScript/
Vite compila assets locais versionados e embutidos em Go; runtime não precisa Node.
`cd ui`, `npm ci`, `npm run typecheck`, `npm test`, `npm run build` recompila a UI.
Desenvolvimento: servidor Go e `npm run dev` em terminais separados, com proxy SSE.
Host loopback, Origin/Fetch Metadata e CSP restringem acesso pelo navegador.

A tela mostra transcript persistente e activity SSE separada. Reload preserva
Bots/Threads/mensagens e limpa acompanhamento local, sem abortar Sessions.
Limitações explícitas: sem tool history ou summarization; Memory manual é separada.
Editar Bot exige reentrada das instruções privadas. Veja [Web UI v1](docs/WEB_UI_V1.md).

**Implemented — Phase 8 Conversation History + Response Persistence:** mensagens
user/assistant imutáveis, UTF-8 exato e sequência por Thread em JSON versionado
separado (`conversations/<thread-id>.json`). User persiste antes da execução;
assistant somente após sucesso e cleanup. Failed/aborted pode deixar apenas user.
Falha de persistência é explícita; não existe transação ACID ou replay de Session.
GET `/api/v1/threads/{id}/messages?after=0&limit=40` fornece páginas privadas;
POST Sessions aceita message_id para duplicatas. Contexto contínuo inclui somente
mensagens recentes inteiras dentro do Budget; arquivos ausentes são histórico vazio.
32 KiB/user, 256 KiB/assistant, 1024 mensagens e 16 MiB por Thread; um escritor local.
Transcript sobrevive a reload/restart. Threads com mensagens não podem ser removidas.
Veja [Conversation History v1](docs/CONVERSATION_HISTORY_V1.md).

**Implemented — Phase 9 Web Approval Flow:** revisão individual de leitura e preview completo dos contratos de escrita. Flags serve --enable-replace-file / --enable-create-file habilitam somente capacidade (Linux); decisão humana continua obrigatória, única e efêmera. Reload recupera pendência; Abort/deadline/shutdown invalidam. Veja [Web Approval v1](docs/WEB_APPROVAL_V1.md).

**Implemented — Phase 10 MCP Tool Integration:** servidores stdio configurados localmente em mcp.json, discovery real, ferramentas namespaced e seleção no Bot. MCP read exige aprovação individual; write/other são negados. Settings / MCP mostra somente catálogo e status. Subprocessos usam ambiente explícito sem credenciais do provider e encerram após Sessions. Veja [MCP v1](docs/MCP_V1.md).

**Implemented:** Phase 11 — Memory Foundation. Computer Use Foundation está implementada na Phase 12; Intelligent Memory permanece futura.
O transporte permanece em `internal/providers/openai`, compartilhado pelo Groq.
Não há descoberta automática, persistência de chaves ou mudança de permissões.

Consulte a [arquitetura V2 e migração incremental](docs/DAIMON_ARCHITECTURE_V2.md).

## Phase 11 — Memory Foundation

Memory manual, persistente e separada do transcript: escopos global/Bot/Thread,
kinds fact/preference/instruction/note, CRUD explícito na UI e HTTP. `serve` abre
`memory.json` versionado no data-dir: 2048 registros, 16 KiB por conteúdo, 32 MiB
por arquivo. Contexto congelado por Session, recuperação lexical determinística,
registros integrais, até 16 candidatos/32 KiB com reserva para mensagem/final/history.
Nada é salvo automaticamente. Memory não altera ferramentas, policy ou aprovação.
Armazenamento local em texto claro; registros selecionados vão ao provider.
Veja [Memory v1](docs/MEMORY_V1.md) para contratos, limites e validação offline.
`cd ui && npm run smoke:memory` valida UI/runtime reais com provider local fake.
Phase 13 — Live Computer View + Human Takeover está implementada abaixo. Intelligent Memory (Extraction + Summarization + Semantic Retrieval) permanece futura.


## Phase 12 — Computer Use Foundation + CUA Driver

Computer é uma capability opcional de cada Bot, com backend local CUA Driver pelo
MCP stdio existente. ComputerManager possui disponibilidade e lease exclusiva;
Session congela seu binding; policy e aprovação humana individual continuam
obrigatórias. Nomes em Bot.Tools, Memory e mensagens não habilitam Computer.

Settings / Computer mostra status/capabilities e instalação manual; o editor do
Bot habilita explicitamente Computer e seleciona ferramentas. Observações textuais,
click, type_text e bring_to_front exigem revisão por chamada, incluindo preview
integral da digitação. Desktop real, permissões do usuário: sem sandbox guarantee.
Nenhuma imagem entra no modelo, histórico, Memory, logs, snapshots ou SSE.

Configure um único servidor em mcp.json com computer_backend "cua-local", caminho
absoluto para cua-driver[.exe] e args ["mcp"]. O perfil optional computer_profile do
Bot referencia esse ID. O MCP genérico permanece read-only; CUA usa o perfil legado
2025-06-18 explicitamente. GET /api/v1/computers é apenas metadata, sem action API.

Veja [Computer Use v1](docs/COMPUTER_USE_V1.md) para configuração completa, ferramentas
reais suportadas, aprovação, lease, falhas e limites. Testes offline usam fake CUA
MCP; ui/npm run smoke:computer verifica o percurso no navegador sem ações reais.

Phase 13 — Live Computer View + Human Takeover está implementada abaixo.
Intelligent Memory, Subagents e External
Agents permanecem futuros, sem instalação automática ou permissões permanentes.

## Phase 13 — Live Computer View + Human Takeover

Implemented: optional local CUA RCDP v2 media, Chat / Computer / Activity tabs,
independent ephemeral viewers and exclusive human control with explicit Take /
Give Back. Driver MCP agent actions retain individual policy/approval. Media
and human input use dedicated same-origin sockets; frames never enter Session
SSE, transcript or Memory. Driver-only use remains available without media.

Configure an existing local media daemon with `serve --computer-media-url
http://127.0.0.1:3211` and server-only `DAIMON_CUA_MEDIA_TOKEN`; no daemon or
Space is created. See [Computer View v1](docs/COMPUTER_VIEW_V1.md) for contracts,
limits, FSL external-component licensing, configuration and validation.

Offline browser check: `npm run smoke:computer-view` in ui. Simulated frames
and input verify ownership and reload; real CUA media remains operator opt-in.
Phase 14 — Sandboxed Computers is implemented below.

## Phase 14 — local sandboxed computers

Optional Bot SandboxProfile provisions a disposable per-Session Linux gVisor
Computer through the public CUA CLI. Host Computer mode remains available.
Enable on an already configured Linux host with
`serve --data-dir /controlled/data --sandbox-cua /absolute/path/to/cua`.
Settings / Sandboxes exposes safe runtime/ownership/cleanup state. Existing live
view and human takeover are reused when the guest provides compatible media.
Cleanup is automatic; failures remain journaled for startup reconciliation.

Local only, Linux first, gVisor only. No cloud, pools, persistent volumes, host
mounts or snapshots. Runtime installation is manual. Outbound network remains
enabled; no egress firewall/disk quota/digest pinning is claimed. Real runtime
smoke has not been executed here. See [Sandbox Computers](docs/SANDBOX_COMPUTERS_V1.md).

## Phase 15 — Cloud Computers

Cloud is explicit Sandbox placement through official CUA Fleet. Configure an
already installed CLI with `serve --cloud-cua /absolute/cua` on Linux and external
process credentials; DAIMON performs no login or installation. Cloud Bot mode
shows paid capacity before Send, fixed 2 CPU/4 GiB or 4 CPU/8 GiB presets and
a 15-minute claim TTL. Two cloud reservations, no warm provisioning, no fallback
to host/local. Claim release may retain billable managed pool capacity until GC.
Existing approval, live view, takeover and owned cleanup/reconciliation are reused.
See [Cloud Computers V1](docs/CLOUD_COMPUTERS_V1.md). Offline fake browser check:
`npm run smoke:cloud` in ui. **real CUA Fleet not validated**.
Next recommended: Phase 17 — Background Tasks + Long-Running Agent Runs.


## Persistent Environments — Phase 16

See [Persistent Environments v1](docs/PERSISTENT_ENVIRONMENTS_V1.md) for durable Thread-owned local workspace
revisions, bounded official filesystem transfer, expected-revision commits,
explicit enable/delete, hydration before model, successful sync before compute
cleanup and assistant persistence. Existing Threads are not automatically imported.
Session failures/abort before commit preserve the prior revision; an already
confirmed workspace commit survives later cleanup/persistence failure or abort and
is reported explicitly. Compute remains disposable; no persistent processes.

Authorized inspection exception: a fixed DAIMON helper may run in the guest only
through official ProcessService, solely to verify metadata absent from the filesystem
API (inode/device/hard-link count). Fixed executable/code/arguments; no user/model
command strings, shell, arbitrary command tool, stdin or content transfer. Structured
bounded output, timeout and cancellation/kill-on-disconnect are required. Unsupported
or unproven metadata fails closed. All content transfer uses FilesystemService.
No paid CUA smoke, installation or login is automatic. Phase 17 Background Tasks +
Long-Running Agent Runs, Intelligent Memory, Subagents and External Agents stay deferred.


## Interface de produto — Phase 16.5

A interface local prioriza Bots, Conversas e Chat, com visual escuro e textos em
PT-BR. Computador, Arquivos, Memória e Atividade ficam em abas; provedores, MCP
e detalhes de infraestrutura ficam em Configurações. Aprovações individuais e
previews completos continuam obrigatórios. Arquivos apresenta metadados do
ambiente persistente; não é um gerenciador de arquivos.

Veja [Product UI V2](docs/PRODUCT_UI_V2.md) para arquitetura, acessibilidade,
validação e limitações. Nenhuma alteração de backend ou implementação da Phase 17.

## Managed workspace — funcionalidades integradas

```text
daimon managed-workspace --base /tmp/daimon-store create --source /input
daimon managed-workspace --base /tmp/daimon-store apply --run <id> --plan /input-plan.json
daimon managed-workspace --base /tmp/daimon-store report --run <id>
daimon managed-workspace --base /tmp/daimon-store list
daimon managed-workspace --base /tmp/daimon-store inspect --run <id>
daimon managed-workspace --base /tmp/daimon-store discard --run <id> --enable-discard
```

Aplicação determinística somente na cópia privada, com preview integral, aprovação
de uso único, journal e relatório verificado. Não há publicação na origem, rollback,
retomada após crash, limpeza automática ou suporte Windows. Veja
[o contrato e as limitações](docs/managed-workspace.md) e
[o formato do plano](docs/workspace-core.md).

List e inspect são read-only. Discard remove somente uma cópia gerenciada,
com opt-in, preview completo, confirmação única, revalidação e tombstone privado
sincronizado fora do run. Não modifica a origem nem faz limpeza automática.
Runs interrompidos ou inconsistentes permanecem bloqueados para descarte.
Veja [estados, auditoria e limitações do lifecycle](docs/managed-lifecycle.md).

### Evidence export — experimental, Linux amd64 only

Os schemas metadata-only usam hashes `*_sha256` e tamanhos numéricos `*_bytes`.
Artefatos com nomes legados ambíguos são recusados sem migração ou fallback.
Veja a [incompatibilidade documentada](docs/managed-evidence-export.md#schema-metadata-only-e-incompatibilidade-legada).

```text
daimon managed-workspace --base /tmp/daimon-store export-evidence --run <id> --destination /tmp/daimon-review/evidence-1 --source-check /input --enable-export-evidence
```

Exige pai privado 0700 existente, destino novo fora da source/store e aprovação
única. Exporta somente seis artefatos metadata-only de ready/succeeded íntegro,
com hashes, inventário e auditoria externa; source is never modified. Conteúdo,
output e patch não são exportados. Não implementa secret scanning nem promete
ausência universal de segredos. Same-UID processes are trusted. Windows bloqueia
antes de efeitos. Veja [contrato, limites e falhas](docs/managed-evidence-export.md).

Validação restrita e opt-in de planos: `workspace --root <root> plan --validate-scope <pedido>` (flag também aceita após o pedido). [Contrato, template reconhecido e recuperação sem tools](docs/workspace-core.md#validação-opt-in-de-escopo-do-plano). Pedidos fora do template continuam planos livres; não há execução ou nova permissão.

Retenção privada opt-in de pré-imagens de managed replace_file: [contrato experimental, Linux amd64 only](docs/managed-preimage-retention.md). Nenhuma pré-imagem é exportada.

Output Export experimental de um único arquivo íntegro, Linux amd64 only: [contrato, aprovação reforçada e limites](docs/managed-output-export.md). Conteúdo potencialmente sensível; destino privado gerenciado; origem não modificada.

Preimage Export experimental (Linux amd64): [contrato e limites](docs/managed-preimage-export.md).
