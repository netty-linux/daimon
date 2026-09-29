# DAIMON

Visão: fundação experimental de um Sovereign Personal Agent sob controle do usuário.
Escopo atual: Reliable Agent Loop + Execution Budget, em Go, com Scripted, echo e read_file.

## Arquitetura

- Biblioteca padrão, interfaces pequenas e fluxo explícito, síncrono.
- Preserve Model.Generate, Tool.Execute, Registry e EventSink tipado.
- Não adicione emissor global nem entregue Registry/implementações ao modelo.
- Copie requests e chamadas para proteger o histórico de mutações externas.
- Ferramentas sequenciais, sem retries ou concorrência.
- Configuração explícita: Loop.Budget; DefaultBudget somente quando solicitado pelo chamador.
- Preserve errors.Is para ErrInvalidConfig, ErrInvalidResponse, ErrMaxSteps e contextos;
  ModelError deve preservar passo e causa. LimitError deve distinguir Kind.

## Invariantes do budget

- Todos os campos são positivos. Valide antes de chamar o modelo; zero nunca é ilimitado.
- Uma chamada ao modelo = um passo; nunca ultrapasse MaxSteps.
- Pré-valide o lote inteiro antes de efeitos: IDs/nomes não brancos, IDs únicos em toda
  execução, quantidade por passo/total, bytes por argumento e capacidade de histórico.
- Conte Content, ToolCallID, IDs, nomes e json.RawMessage por bytes, não tokens ou runes.
- Reserve assistant e todos os recibos: cada recibo inclui len(ID) + MaxToolResultBytes.
  Aritmética não pode aceitar um lote por overflow.
- Verifique capacidade para a mensagem inicial e para a resposta final antes de adicioná-las.
- Rejeite texto com calls, resposta final branca, acima do limite ou que não caiba integralmente.
- Normalize resultados para UTF-8 e limite em fronteira válida. Inclua marcador completo:
  original_bytes quando couber, [truncated] ou ~ para limites mínimos. Conte toda alteração.
- ToolCalls conta tentativas iniciadas, incluindo falhas controladas/interrupções.
  Lotes rejeitados e chamadas não iniciadas não contam.
- Cancelamento pode deixar calls sem recibo; preserve histórico aceito e pare novos efeitos.
- Use timeouts cooperativos com causa identificável, respeitando deadlines externos.
  Verifique antes/depois das chamadas; sucesso após expiração não é sucesso do loop.
- StopReason deve existir em todo retorno e no único loop_stopped correspondente.
- Evento não contém mensagens, nomes/IDs do modelo, argumentos, resultados ou texto de erro.

## Segurança e escopo

- Ferramentas desconhecidas, JSON inválido e erros normais viram resultados controlados.
- Não usar panic, log.Fatal ou os.Exit no loop.
- read_file permanece confinado com os.Root, somente caminhos relativos, limite de bytes,
  validação estrita e bloqueio de traversal/symlink externo/diretórios.
- Testes de symlink devem executar; falta de permissão é falha explícita.
- Não adicionar prematuramente banco, memória longa/vetorial, gateway, provedores reais,
  subagentes, MCP, servidor HTTP, Telegram/Discord, TUI/web, event sourcing completo,
  shell, escrita/edição de arquivos, streaming ou abstrações especulativas.
- Não anunciar garantias de sandbox ou limite rígido de memória: o budget limita dados aceitos.

## Processo e validação

Antes de modificar: inspecione o estado, resuma o comportamento, liste arquivos,
semântica e invariantes afetadas. Preserve os testes da versão anterior, migrando
apenas a configuração necessária. Cubra limites e falhas com testes sem rede.

Execute em Linux, incluindo os testes de symlink:
```sh
gofmt -w .
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go run ./cmd/daimon demo
```
gofmt -l não pode listar arquivos. CI executa formatação, vet, testes e race detector
a cada push e pull request. Não substitua execução real por comandos simulados.
