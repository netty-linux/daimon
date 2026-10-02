# Contrato de criação aprovada no workspace

Implementado: criação de um arquivo novo, Linux somente, efêmera e opt-in.
Não substitui contrato/proposta/Permit de replace_file. Reutiliza somente a interação
terminal de preview integral e a validação estrita de JSON path/content já existentes.

## Entrada, vínculo e aprovação

Somente workspace --root "diretório" --enable-create-file "mensagem" registra
create_file. Política padrão Deny; configuração direta Allow ou prompt genérico não
podem autorizar a ferramenta. Nenhuma flag aprova escrita automaticamente.
Plan e chat não expõem criação. As duas flags de escrita são incompatíveis neste PR;
exposição conjunta futura exigiria flags independentes, nunca permissão implícita.

O root aberto tem identificador aleatório efêmero, distinto por instância, exibido no
preview sem revelar caminho absoluto. Proposta conserva esse handle, identidades e
modos dos pais, caminho relativo canônico, ausência observada, bytes copiados e hash,
limites e nonce de proposta. ID vincula esses campos; somente o runtime possui Permit.
Paths absolutos, traversal, NUL, ADS, UNC, separadores alternativos, componentes não
canônicos, aliases Windows e symlinks observados são rejeitados. Pais precisam existir
como diretórios, sem criação implícita. Alvo existente de qualquer tipo é conflito.

Preview mostra ausência na preparação, conteúdo integral em Go ASCII escapes
reversíveis, quantidade/hash dos bytes, limites e avisos. Não há truncamento aceito.
EOF, negativa, input inválido, falha ou escrita parcial do display impedem aprovação.
A proposta só permite uma tentativa de aprovação, mesmo após falha. Revalidação de
pais/ausência ocorre antes e depois da decisão e novamente antes de abrir o alvo.
Cada ferramenta possui uma tentativa de preparação por run; dois creates no lote
são rejeitados na autorização antes de qualquer Execute. Permit copiado compartilha
estado atômico de consumo. Executar em outro path/argumento gasta e invalida Permit.

## Executor e ponto de sucesso

Apply consome o Permit antes de qualquer efeito, inclusive contextos inválidos,
cancelamento ou falha. Contexto da aprovação permanece vinculado e contexto de
execução precisa de deadline; CLI/loop fornecem budget explícito já existente.
Valida limites/hash e reabre parent com os.Root, confirmando identidade observada.
Usa O_RDWR|O_CREATE|O_EXCL, modo inicial 0600, no parent preso ao root. Um nome que
passe a existir gera conflito, sem abrir/truncar/sobrescrever esse objeto.

Após obter o arquivo exclusivo, força chmod 0600 para não depender de umask e
verifica modo final. Testes reais executam umask 000 e 777. Escreve chunks até 32 KiB,
checa contextos antes/depois, rejeita short write sem retry. Sync, seek, leitura limitada
a FinalBytes+1 e hash confirmam conteúdo exato, tamanho, arquivo regular, link único
e permissões. Fecha, revalida pais e identidade/metadados Linux (incluindo ctime,
uid/gid/link count) no path antes de marcar sucesso. Nenhum rename, overwrite ou mkdir.

A criação é exclusiva, mas não é publicação atômica de conteúdo completo: alvo pode
estar visível vazio/parcial durante execução. O sucesso é marcado somente após toda
verificação/Sync/Close e checagem final de contexto. Cancelamento anterior tenta cleanup;
cancelamento posterior não desfaz sucesso e pode ser observado pelo loop sem recibo.

Falhas tentam fechar e remover somente o inode recém-criado. Lstat compara identidade
antes da remoção; alvo diferente, symlink ou com hard links não é removido e retorna
ErrCleanup adicional. Erro de remove também retorna ErrCleanup. Pode haver resíduo
se cleanup não for possível; nunca sucesso falso ou retry. Limpeza não é uma operação
atômica de compare-and-remove contra escritor externo hostil.

## Limites e erros

Limits explícitos positivos: FinalBytes até 1 MiB, Lines até 1000, PathBytes até 4096,
PreviewBytes positivo. CLI usa 64 KiB/1000/4096/1 MiB; limite JSON do loop também vale.
Linhas contam LF+1, incluindo última linha vazia. UTF-8 inválido é rejeitado; vazio
é conteúdo válido e ainda requer aprovação integral. Checks são por bytes, não tokens.

- ErrInvalid: configuração, conteúdo ou contexto sem deadline inválidos.
- ErrUnsafePath / ErrFile: caminho/pais inseguros ou impossíveis de inspecionar.
- ErrConflict: qualquer objeto já existe ou O_EXCL recusa nome concorrente.
- ErrChanged: pais, identidade, bytes, links ou metadados mudaram.
- ErrLimit: conteúdo, path, linhas ou preview acima dos limites.
- ErrDenied / ErrUsed / ErrApproval: negativa, capacidade gasta ou falha de display/aprovação.
- ErrUnsupported: plataforma fora do Linux; nenhum arquivo criado.
- ErrWrite: falha de criação/escrita/chmod/sync/verificação/close.
- ErrNoSpace e fs.ErrPermission: categorias preservadas com erros controlados.
- ErrCleanup: cleanup incompleto, junto ao erro primário.
- Erros de contexto: identidade context.Canceled/DeadlineExceeded preservada.

Error nunca inclui PathError original, conteúdo, caminho ou texto livre da falha.
Recibo de sucesso: file created. Recibos de falha são controlados e sujeitos ao budget.
Eventos continuam tipados sem argumentos/IDs/paths/outputs; resumo mostra apenas
contagens fixas e commits confirmados. Preview é display deliberado distinto de logs.
Provider conhece os argumentos que gerou; runtime não injeta preview/dados adicionais.
Leituras aprovadas mantêm a fronteira de privacidade anterior.

## Premissas e fora de escopo

Workspace/filesystem sob controle do usuário. os.Root confina operações, mas não é
sandbox contra movimentação hostil, inode externo via hard link ou alteração concorrente
entre check e uso. Sem garantia absoluta contra escritor externo. Não há fsync de
parent/diretório ou garantia de durabilidade em perda de energia, ACL/ownership/xattrs
customizados, rollback após sucesso ou cancelamento rígido de syscall bloqueada.
Contrato/executor de replace_file e modo plan permanecem intactos.

Não cria diretórios, vários arquivos por call, substituição/exclusão/movimentação de
alvos existentes, lote, shell/git/diff, integração/MCP/servidor, banco/memória/cache,
subagentes, streaming, retry/fallback, novos providers, permissão persistente ou policy
em arquivo. Cleanup do arquivo recém-criado incompleto é a única remoção interna.

## Validação

Smokes via httptest e fixtures: schema opt-in, preview/aprovação, bytes exatos e recibo,
negação/EOF/input inválido, display falho/parcial, cancelamento/deadline, limite,
reuso, conflito e resumo sem dados. Testes Linux reais: symlinks/hard links/pais,
O_EXCL com corrida simulada, modo com umask, limites exatos, faults de permissão,
disco cheio, sync, close, short write, cleanup e inode concorrente. Falhas injetadas
são determinísticas; não simulam um filesystem fisicamente cheio. Windows valida
falha fechada. Nenhuma chamada externa ou credencial real nos testes.
