# DAIMON

Visão: Sovereign Personal Agent sob controle do usuário, com componentes substituíveis.
Escopo atual: apenas Tiny Agent Loop → Reliable Agent Loop, em Go, com modelo
programável, ferramentas `echo` e `read_file`, histórico local à execução e eventos mínimos.

## Arquitetura e validação

- Loop pequeno, explícito, síncrono e independente de provedores.
- Biblioteca padrão; justifique antes de adicionar dependências externas.
- Interfaces pequenas com uso real; sem frameworks, reflection ou controle de fluxo oculto.
- Uma chamada ao modelo = um passo; nunca exceder MaxSteps.
- Chamadas de ferramenta sequenciais, IDs não vazios e únicos, resultados correlacionados.
- Cubra comportamentos importantes com testes determinísticos, sem rede.
- Execute `gofmt -w cmd internal`, `go vet ./...` e `go test ./...`.
- Verifique `gofmt -l cmd internal` e `go run ./cmd/daimon demo`.

## Segurança

- Verifique contexto antes de modelo e ferramenta; implementações devem cooperar com cancelamento.
- Erros recuperáveis de ferramenta voltam ao modelo; não usar panic, log.Fatal ou os.Exit no loop.
- Eventos não carregam mensagens, argumentos, respostas, erros, segredos ou raciocínio privado.
- read_file aceita apenas caminhos relativos, valida JSON estritamente, limita bytes,
  rejeita diretórios e traversal e usa os.Root contra escape por symlinks.
- Não substituir os.Root por simples checagem de prefixo ou validação seguida de abertura irrestrita.
- Testes de symlink devem executar; falta de permissão é falha explícita, não teste ignorado.

## Fora do escopo — não implementar prematuramente

Memória longa/vetorial, banco de dados, gateway, provedores reais ou múltiplos provedores,
subagentes, MCP, servidor HTTP, Telegram, Discord, TUI, interface visual/web,
event sourcing completo, shell, escrita/edição de arquivos, concorrência e abstrações especulativas.
