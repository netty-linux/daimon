# Contrato de proposta e aprovação de edição

Estado: preparação, preview e aprovação implementados como biblioteca independente
e somente leitura. Não há executor, ferramenta de edição, registro no Registry ou
integração à CLI/loop. A política atual permanece inalterada. A aprovação deste
contrato não executa nem habilita escrita.

## Contrato implementado

`internal/diffview.Render` produz um preview determinístico com três linhas de
contexto, regiões separadas, CRLF visível como `\r` e marcador de ausência de newline final.
Todas as linhas alteradas são mostradas; exceder limites retorna erro sem preview
parcial. MaxLines e MaxBytes são positivos. Há tetos adicionais de 1000 linhas por
versão e 1 MiB por string de entrada, inclusive caminho, para limitar o trabalho.
O limite de saída conta bytes e inclui cabeçalhos e marcadores.

O formato é para exibição, não é um patch aplicável. Conteúdo e caminhos usam
escapes de string Go ASCII reversíveis: controles, aspas, barra invertida e todo
caractere não ASCII são escapados. `\r` do terminador CRLF distingue-se do texto
literal `\\r`; Unicode visualmente parecido mantém representações diferentes.
Bytes UTF-8 inválidos são rejeitados. Linhas de conteúdo têm prefixo próprio;
marcadores de estrutura não podem ser confundidos com o conteúdo escapado.

`internal/editcontract.Workspace` abre um os.Root e prepara uma Proposal para um
único arquivo regular existente. O chamador mantém o workspace aberto até concluir
o ciclo. O contrato aceita caminhos relativos canônicos com `/`: não aceita
traversal, absoluto, `.` como alvo, barra invertida, ADS, nomes reservados Windows,
componentes com ponto/espaço final ou caracteres de caminho proibidos. Não normaliza
silenciosamente o caminho. Não altera read_file/list_dir, que mantêm seus contratos.

A proposta vincula o workspace aberto, caminho exato, SHA-256/bytes/modo/mtime do
original, identidade do arquivo retida privadamente com os.SameFile, cópia dos
bytes exatos propostos e limites. Seu ID inclui os hashes, metadados, caminho,
limites e nonce aleatório. IDs não são tokens de autorização persistentes.
Nenhuma referência mutável aos bytes internos é entregue ao chamador.

Limits exige todos os campos positivos: InputBytes (por versão, até 1 MiB), Lines
(por versão, até 1000), PathBytes (até 4096) e PreviewBytes (display completo).
O display inclui ID, caminho, versão original, hash/tamanho proposto, limites,
diff e **conteúdo proposto completo** como string ASCII entre aspas, com todos os
terminadores. Contexto omitido pelo diff não omite bytes do conteúdo proposto.
Cabeçalhos, escapes e marcadores contam no limite. Se qualquer parte não couber,
Prepare retorna erro e nenhuma proposta aprovável; nunca há corte parcial.

Proposal.Approve aceita um Reviewer injetado e só uma tentativa. Revalida o
arquivo antes e depois da decisão. Só Allow explícito produz Permit; nil, decisões
inválidas, erro, negativa e cancelamento falham fechado. View e Review são cópias
destacadas; alterações feitas pelo reviewer não mudam a proposta. Um reviewer
é um componente confiável: deve exibir todo o display e obter uma decisão fresca.
Não há proteção contra uma implementação de reviewer que minta deliberadamente.

O reviewer Terminal implementado escreve o display inteiro e o prompt no writer
injetado antes de ler a resposta. Escrita parcial/falha impede aprovação; controles
não chegam ao terminal. Padrão No, EOF e entrada inválida negam. Resposta longa ou
erro de leitura inutiliza a instância, impedindo que sobras aprovem outra proposta.
Cancelamento interrompe a espera; um reader bloqueado pode manter uma goroutine
até o fechamento. Descarte o Terminal após cancelamento e feche o reader quando
possível. Não lê os.Stdin, não persiste decisões e não oferece sempre permitir.

Permit.Consume gasta a aprovação antes de revalidar e devolve uma cópia dos bytes
somente se o alvo ainda corresponder. A capacidade também permanece vinculada ao
contexto usado na aprovação: cancelá-lo impede consumo mesmo com novo contexto.
O chamador deve usar o contexto de execução na aprovação. Cópias de Proposal/Permit compartilham o
estado de uso único. Um resultado Validated não é uma operação de escrita nem
uma credencial serializável; não pode ser reutilizado como autorização de executor.
O fluxo é síncrono; não há suporte a uso concorrente do Workspace/Terminal.

### Alterações, cancelamento, symlinks e falhas

- Arquivo alterado antes/durante aprovação: sem Permit. Depois da aprovação:
  Consume falha e gasta o Permit. Mudanças em conteúdo, identidade, modo, tamanho
  ou mtime exigem nova preparação e aprovação; não há retry automático.
- Cancelamento/deadline: preserva errors.Is de contexto e nunca valida sucesso
  depois de observado. Aprovação/consumo cancelado gasta a tentativa/capacidade.
- Symlink observado em qualquer componente, interno ou externo: rejeitado na
  preparação e revalidação. Diretório, alvo ausente, leitura/inspeção falha ou
  workspace fechado impedem aprovação/consumo. Erros não expõem conteúdo/caminho
  nem mensagens livres dos componentes. Falha de filesystem pode retornar
  ErrFile/ErrSymlink/ErrLimit em vez de ErrChanged; todas impedem o fluxo.
- Leitura é limitada antes da aceitação. UTF-8 inválido ou limite excedido não
  produz proposta. Nenhuma dessas operações escreve, cria, remove ou renomeia arquivos.

## Executor futuro: ausente neste PR

Um executor só poderá ser implementado em outro corte explicitamente autorizado,
para um arquivo existente por operação. Deve consumir a capacidade junto ao efeito
sem transformar os bytes retornados em autorização permanente. Precisa preservar
budget, autorização prévia, cancelamento, recibos e eventos sem conteúdo sensível.
Não há shell, edição em lote, permissões persistentes, retry ou fallback.

## Critérios ainda não validados

A sequência de inspeção/leitura/check não elimina corridas de filesystem. os.Root
confina resolução, mas as verificações de symlink não são exclusão atômica de
trocas concorrentes. Mudanças transitórias restauradas (ABA) podem não ser detectadas.
O contrato é apropriado a um workspace controlado, não prova sandbox ou segurança
em filesystem hostil. Hard links não são rejeitados nem resolvidos neste corte.

Antes de escrita: definir exclusão de concorrência/conflitos, proteção de identidade
até o efeito, política de hard links, permissões, troca atômica, durabilidade,
limpeza de temporários e tratamento de falhas/cancelamento durante o commit.
Atomicidade, ausência de escrita após cancelamento e comportamento sob concorrência
precisam de evidência própria em Linux e Windows. Os testes deste PR validam somente
o contrato read-only, não essas garantias de um executor inexistente.

## Smokes reais

Groq é o padrão dos smokes opt-in via `daimon smoke`, com `openai/gpt-oss-20b`.
Em 2026-10-02, o smoke real de echo passou: dois requests, uma chamada de
ferramenta, resposta DAIMON e StopReason completed. Nesse primeiro teste a chave
foi usada apenas no processo.

Também passaram os smokes reais de listagem aprovada, listagem negada e leitura
aprovada de fixture temporária, cada um com dois requests, uma tentativa de
ferramenta e StopReason completed. A negativa retornou DENIED; a leitura retornou
exatamente DAIMON_SMOKE_FIXTURE_20261002. As respostas y/n foram fornecidas pelo
operador de teste sob autorização do usuário e não alteram o padrão No da CLI.
A comprovação de ausência de Execute em negativa vem dos testes determinísticos;
a resposta do modelo sozinha não comprova ausência de efeito.

Por solicitação explícita do usuário, a chave passou a ficar na variável de
ambiente DAIMON_API_KEY do usuário Windows, fora do repositório. Não há chave em
arquivo versionado nem carregamento implícito de secrets pelo provider.
O comando conserva a aprovação de uso único por chamada.

### Smoke local pendente

Na revisão de 2026-10-02, o Ollama instalado não respondeu em 127.0.0.1:11434.
Não houve chamada a provider real, download de modelo ou uso do endpoint remoto
configurado. Os testes HTTP usam servidores simulados locais.

Quando um modelo local com suporte a tools estiver disponível, executar em um
workspace de teste, com chave ausente e base URL local explicitamente configurada:
solicitar uma listagem, conferir os argumentos e aprovar manualmente uma vez;
repetir negando, depois testar read_file em arquivo inofensivo. Confirmar ausência
de execução em negativa e recuperação do modelo após o recibo controlado. Registrar
modelo, resultado e limitações sem credenciais ou conteúdo sensível.
