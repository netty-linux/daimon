# Contrato de proposta e aprovação de edição

Estado: preparação, preview, aprovação e executor de substituição implementados.
`replace_file` está registrado no chat/smoke, com aprovação obrigatória. O loop e
seus contratos não mudaram. O demo permanece offline, sem ferramenta de escrita.
A escrita é suportada em Linux; outras plataformas retornam ErrUnsupported antes
de criar temporário. O contrato de preparação/Consume continua portátil.

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

Limits exige todos os campos positivos: InputBytes (original, até 1 MiB), FinalBytes
(proposto, até 1 MiB), Lines
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
  ou mtime exigem nova preparação e aprovação; em Linux também se compara ctime,
  uid/gid e número de links, retidos privadamente. Não há retry automático.
- Cancelamento/deadline: preserva errors.Is de contexto e nunca valida sucesso
  depois de observado. Aprovação/consumo cancelado gasta a tentativa/capacidade.
- Symlink observado em qualquer componente, interno ou externo: rejeitado na
  preparação e revalidação. Em Linux, número de hard links diferente de um também
  é rejeitado. Diretório, alvo ausente, leitura/inspeção falha ou
  workspace fechado impedem aprovação/consumo. Erros não expõem conteúdo/caminho
  nem mensagens livres dos componentes. Falha de filesystem pode retornar
  ErrFile/ErrSymlink/ErrLimit em vez de ErrChanged; todas impedem o fluxo.
- Leitura é limitada antes da aceitação. UTF-8 inválido ou limite excedido não
  produz proposta. Prepare/Approve/Consume permanecem somente leitura; a escrita
  ocorre exclusivamente em Apply após aprovação.

## Executor implementado: Permit.Apply

Apply consome o mesmo estado de uso único de Consume. Se Consume já foi usado,
Apply falha e vice-versa. Não aceita caminho ou conteúdo adicionais: aplica somente
os bytes privados da proposta aprovada ao caminho original. Invalidar, falhar ou
cancelar não devolve a capacidade. Deadline é obrigatório; no loop, os deadlines
de run e ferramenta vêm do Budget existente. Não há timeout concorrente próprio.

Antes de criar qualquer temporário: valida plataforma, limites, hash proposto,
contexto e versão do alvo. Abre o diretório pai como os.Root e revalida o alvo
também nesse handle. O temporário tem nome aleatório e é criado uma vez com
O_EXCL e modo 0600, no diretório do alvo, sem retry em colisão. O original nunca
é aberto com flag de escrita/truncamento.

Grava em chunks de até 32 KiB, verifica contexto, copia permissões rwx, faz Sync
do temporário e Close. Bits setuid/setgid/sticky são rejeitados antes de staging.
Ownership, ACLs, xattrs e timestamps não são copiados; o novo inode é criado pelo
usuário executor. Se copiar permissões falhar, não há commit.

Depois de staging, revalida conteúdo/hash, identidade, modo, metadados e links
do temporário e do alvo no caminho do workspace e no diretório pai fixado,
e verifica cancelamento antes
do rename. O rename do temporário para o alvo é o ponto de commit atômico. Alvo
observado ausente é rejeitado; não existe ferramenta de criação, exclusão ou
mudança de nome de arquivo do usuário.

Antes do commit, falha/cancelamento fecha e remove o temporário, deixando o
original intacto pelo executor. Se o filesystem negar a remoção, ErrCleanup
relata explicitamente que pode existir temporário residual; não se afirma limpeza
incondicional contra falha do próprio filesystem. Depois do commit não há rollback:
cancelamento que concorra com rename pode ser observado pelo loop após o efeito,
com histórico sem recibo, conforme a regra existente de cancelamento cooperativo.
Sync do arquivo não equivale a fsync do diretório nem garante durabilidade em
queda de energia. Atomicidade de visibilidade em Linux não é atomicidade de versão.

### Integração e erros

ReplaceFile.Prepare/Approve rodam no authorizer antes da passagem de efeitos.
O provider de leitura continua mostrando somente argumentos; o reviewer de edição
mostra a proposta completa. Ambos compartilham o mesmo buffered reader na CLI.
Política Allow direta ou aprovação genérica de argumentos não pode autorizar
replace_file. Execute sem Permit, com argumentos diferentes ou com Permit usado
falha fechado. Permite apenas uma tentativa de proposta por instância/run; um lote
com duas substituições falha na autorização antes de executar qualquer ferramenta.
O authorizer usa runCtx; Execute usa toolCtx. Não existe memória de permissões.

Configuração da CLI: original/final 64 KiB, caminho 4096 bytes, 1000 linhas por
versão, display completo 1 MiB. O budget também limita os argumentos JSON em
64 KiB, portanto escaping e overhead podem restringir o conteúdo final alcançável.
Falhas de preparação/aprovação viram AuthorizationError; falhas normais de Apply
viram recibos controlados ToolFailed. Cancelamento/deadline interrompem o loop.
Eventos não contêm conteúdo, nomes/IDs, caminhos, argumentos ou textos de erro.

| Erro | Comportamento |
|---|---|
| ErrLimit / ErrInvalid / ErrUnsafePath | proposta/configuração rejeitada antes de staging |
| ErrFile / ErrChanged / ErrSymlink / ErrHardLink | alvo inadequado ou versão divergente; sem commit |
| ErrDenied / ErrUsed | sem aprovação válida de uso único |
| ErrUnsupported | escrita desabilitada fora de Linux |
| ErrStage | falha em criar/gravar/chmod/sync/close; cleanup |
| ErrCommit | rename falhou; cleanup |
| ErrNoSpace / fs.ErrPermission | categoria controlada preservada por errors.Is |
| ErrCleanup | remoção do temporário falhou; possível residual |
| erro de contexto | errors.Is preservado; capacidade invalidada |

Mensagens não incluem conteúdo, caminho ou mensagem livre de erro do OS. As
categorias são sentinelas; causas com paths sensíveis não são incorporadas à mensagem.

## Critérios ainda não validados

A sequência de inspeção/leitura/check não elimina corridas de filesystem. os.Root
confina resolução, mas as verificações de symlink não são exclusão atômica de
trocas concorrentes. Mudanças transitórias restauradas (ABA) podem não ser detectadas.
O executor é apropriado a um workspace controlado, não prova sandbox ou segurança
em filesystem hostil. Hard links observados são rejeitados em Linux, inclusive
aliases fora do workspace. Um escritor externo ainda pode agir no intervalo entre
a última verificação e rename: a biblioteca padrão não oferece compare-and-rename.

Não se validou escrita atômica no Windows, exclusão de escritores externos,
durabilidade após falha de energia, preservação de ACL/ownership/xattrs ou recuperação
de temporário após crash. Esses critérios não são apresentados como garantias.
Não há shell, edição em lote, permissões persistentes, retry ou fallback.

Testes Linux cobrem aplicação exata/permissões, modificação/remoção/symlink/hard link
antes de aplicação e durante staging, cancelamento após chunk escrito, deadlines,
limites exatos independentes de entrada/final e aprovação reutilizada. ENOSPC e
EACCES são injetados deterministicamente nas operações reais de temporário, assim
como falhas de chmod/sync/close/rename; os testes verificam original e ausência de
temporários. Smokes HTTP locais exercitam CLI/loop/provider: aprovação, negativa
e lote com duas substituições. Nenhum teste desse corte requer rede externa/chave.

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
