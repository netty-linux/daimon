# Workspace gerenciado privado — Linux amd64

Este fluxo é separado do workspace compartilhado. `workspace --root ...`
continua sem apply-plan; `WorkspaceApply` continua negado mesmo com flags.
O novo comando só opera em uma cópia criada pela Daimon. Não há publicação,
modelo, rede, comandos externos, retry, rollback automático nem ferramentas novas.

## Modelo de ameaça e condições

O proprietário da execução, processos do mesmo UID e administradores/host
privilegiado são confiáveis.
Processos do mesmo UID **não são isolados**: podem modificar a cópia ou metadados,
inclusive entre revalidação e escrita. Não execute este modo em uma conta
compartilhada com processos hostis. Não há namespace ou chroot.
Esta limitação é explícita; o modo não resolve a garantia impossível da árvore
compartilhada original nem muda sua política de bloqueio.

Linux verifica ownership do UID efetivo, modo 0700 da store, run, output,
artifacts e subdiretórios. Ancestrais devem ser diretórios físicos de root/UID
efetivo sem escrita de grupo/outros; diretórios sticky de root/UID efetivo são
admitidos (por exemplo /tmp), pois outros UIDs não podem remover a entrada deste
usuário. Arquivos privados são 0600. A árvore é recém-criada, não compartilhada,
sem hard links, symlinks ou permissões herdadas da origem. Outros UIDs sem
privilégios não conseguem entrar na árvore nem mover seus filhos por DAC.

A store deve ficar no filesystem raiz do namespace, sem atravessar mounts.
openat2 NO_XDEV/NO_SYMLINKS/BENEATH valida essa condição; kernel ou seccomp que
recuse a syscall bloqueia o modo, sem fallback. Mounts separados (mesmo locais)
e bind aliases não são aceitos para a store. A identidade física da origem é
comparada com os ancestrais reais da store antes da criação, para rejeitar
sobreposição por alias de origem. openat2 aqui protege a topologia da store,
não fornece isolamento contra o UID proprietário nem habilita apply compartilhado.

Tipos admitidos para armazenamento: ext-family, tmpfs, overlayfs, XFS e Btrfs,
com semântica Linux de ownership/modes. A integração foi executada no filesystem
local do Docker; não houve validação empírica de todos esses tipos. Filesystems
remotos, FUSE, stores em mounts do Windows e tipos desconhecidos falham fechado.
Não se garante isolamento contra administradores, alterações de mounts, hardware
ou filesystem malicioso. Windows e outras plataformas/arquiteturas rejeitam o modo antes de
criar a store: um protocolo equivalente de ACLs/handles ainda não foi implementado.

## CLI e layout

Somente Linux amd64. A Daimon nunca modifica conteúdo ou permissões da origem
(source) nem a abre para escrita. Processos do mesmo UID são confiáveis.
Leituras podem atualizar atime conforme o filesystem.

```text
daimon managed-workspace --base /tmp/daimon-store create --source /input
daimon managed-workspace --base /tmp/daimon-store apply --run <id> --plan /input-plan.json
daimon managed-workspace --base /tmp/daimon-store report --run <id>
```

`--base` precisa vir primeiro, seguido do comando; flags específicas aceitam
qualquer ordem, mas não duplicatas, desconhecidas ou argumentos extras. A store
é configurada explicitamente, criada com 0700 se ausente e validada se existente.
Seu pai deve existir. Store e source não podem se sobrepor. Não há destino de
escrita `--root`: o run ID é exatamente 32 dígitos hexadecimais minúsculos,
gerado aleatoriamente pela Daimon, com metadados e identidades de inode vinculadas.
Metadados são confiáveis somente dentro do modelo de proprietário confiável;
não são assinados nem resistentes à falsificação pelo próprio UID.

```text
store/<run-id>/
  manifest.json
  attempt.lock                  # após a primeira tentativa de preparação
  output/                       # cópia; único alvo de create/replace
  artifacts/
    journal.jsonl
    approved-plan.json          # somente após aprovação; pode conter conteúdo
    report.json
```

O layout é exportável/revisável como diretório. A CLI mostra run ID e contagens
na criação, estado/contagens no resumo, e preview deliberado no stderr. Não
mostra paths, hashes, bytes de arquivos ou erros brutos no resumo. A aprovação
deliberada mostra os alvos e bytes completos escapados; não é log público.
Não captura prompts ou credenciais e nem consulta variáveis de provider.

## Snapshot e limites

A origem é aberta read-only com os.Root. Arquivos são lidos por abertura
NOFOLLOW/NONBLOCK e reinspecionados; symlinks, hard links, FIFO/dispositivos,
sockets, bits especiais e nomes fora do subset ASCII portátil são recusados.
Uma root de origem que seja alias é resolvida uma vez, deliberadamente; symlinks
de entradas nunca são importados. Diretórios vazios são preservados.

Limites fixos: 64 arquivos, 32 diretórios, profundidade 8, 64 KiB por arquivo,
1 MiB total. A snapshot fica limitada em memória antes de criar o run. O manifest
guarda referência SHA-256 do path canônico da origem, hash SHA-256 versionado da
árvore importada (paths/tipos/bytes), contagens, timestamp UTC, regras, identidades
e status. Não guarda o path da origem nem seu conteúdo. Hash não é anonimização:
uma referência previsível pode ser inferida por dicionário.

Importação não é snapshot atômica de uma origem mutável. Detecta mudanças
observáveis por entrada/root, mas não promete uma única versão global do source.
O hash vinculante é o da cópia efetivamente importada, verificada após a cópia.
A origem não é publicada nem aberta para escrita. Leituras podem atualizar
atime conforme a política do filesystem da origem. Arquivos da cópia são
normalizados para 0600, diretórios para 0700; permissões originais não são copiadas.
Se importação/persistência falhar, o run pode ficar incompleto, sem possibilidade
de apply. Não há limpeza automática nem comando de exclusão.

## Plano, aprovação e aplicação

O plano é o formato v1 estrito de [workspace-core.md](workspace-core.md), não o
plano humano nem o envelope de validate-scope. JSON residual, fences, duplicatas,
versões/operadores desconhecidos, conflitos e hashes incorretos são rejeitados.
No máximo uma criação e uma substituição; duas operações, 64 KiB finais por
arquivo e 128 KiB totais. O pai de uma criação já deve existir na snapshot.
Não há operação de criação de diretório, exclusão ou rename de arquivo do plano.

Todos os previews são preparados antes de qualquer alteração em output. A
preparação pode escrever metadados de tentativa/artifacts, nunca arquivos alvo.
Display acima de 1 MiB ou incompleto bloqueia aprovação. A aprovação é única
para o conjunto inteiro, vinculada a run ID, hash da snapshot/plano/display,
paths, precondições, bytes exatos e limites. EOF, negativa, cancelamento e falha
de display não aplicam o output. Cópias do Proposal/Permit compartilham uso único.

Cada run permite **uma tentativa** de preparação de plano válido, ainda que
negada ou interrompida. O marcador exclusivo e sincronizado não é removido nem
reutilizado. Outro plano exige novo run. Aprovação não persiste como permissão.
Prazo total da CLI: dois minutos, inclusive leitura, preview e aprovação;
timeouts são cooperativos, não limites rígidos de duração de syscalls.

A aplicação revalida identidade/privacidade da árvore, hash da snapshot e,
antes de cada escrita, as precondições capturadas pelos contratos existentes.
Reutiliza createcontract/editcontract com aprovações internas derivadas da única
aprovação do conjunto, sem novo prompt nem modelo. Preserva as garantias e
limitações desses executores. Sincroniza o diretório alterado e verifica o hash
final antes de registrar sucesso da operação. Não promete atomicidade do lote.

## Journal, falhas e revisão externa

Journal JSONL v1 contém sequência, timestamp, paths relativos, tipos, hashes e
status, sem conteúdo integral, mensagens de modelo, secrets ou erros livres.
Antes de cada efeito, started precisa ser gravado e sincronizado. Falha de
journal/sync impede operações seguintes. Failed após started é efeito
desconhecido; confirmação anterior mais falha posterior é partial, sem rollback.
Permissões/ownership, Sync e resultado do filesystem são condições, não promessa
de durabilidade absoluta contra falhas de hardware.

report.json descreve operações succeeded/denied/unknown/not_started. O
manifest distingue ready, prepared, applying e estados terminais. Uma execução
interrompida em prepared/applying é reportada unknown_interrupted, não retomada.
Falha de gravação do relatório/manifest pode deixar artefato incompleto; a CLI
retorna erro e exige revisão. Não há replay, resume, recovery de escrita ou retry.
Falha de fechamento retorna estado desconhecido, preservando contagens de efeitos
já confirmados. Cancelamento observado após sincronização também interrompe o run;
não desfaz operações confirmadas. Timeouts de syscalls continuam cooperativos.

`report` não escreve nem repara artefatos. Rejeita JSON duplicado/nulo, estados
contraditórios e journal incompleto. Resíduos de staging resultam em
unknown_interrupted, sem remoção ou reparação. Confirma sequência e
transições do journal, correspondência das operações com o plano aprovado e hashes
atuais dos arquivos marcados como sucesso. Manifest ready com marcador de tentativa
é unknown_interrupted. Os pais dos arquivos conferidos devem continuar privados,
sem symlinks ou cruzamento de mounts. O marcador attempt.lock é exclusivo,
sincronizado e permanente para aquele run; nenhum comando o remove.
Ausência de evidência íntegra produz erro controlado, nunca
sucesso presumido. A conferência não autentica contra falsificação coerente pelo
UID confiável e não constitui nova snapshot atômica da saída nem auditoria de
mudanças posteriores em arquivos que não participaram do plano.
approved-plan.json contém o plano aprovado, não uma alegação de que todas as
operações foram aplicadas. Consulte o report/journal para distinguir isso.

A saída e approved-plan podem conter dados sensíveis porque são o artefato
deliberadamente solicitado; permanecem privados, nunca duplicados em logs.
Revise output e artifacts antes de exportar. **Publicar no source é uma operação
externa, fora de escopo, sem comando ou permissão na Daimon.** Descarte de um run
é explícito, opt-in e aprovado; não existe remoção automática por idade.
List/inspect são read-only. Consulte [o lifecycle](managed-lifecycle.md) para
preview, confirmação, tombstones, estados e condições de bloqueio.

Evidence export opt-in, experimental e Linux amd64 only está descrito no
[contrato dedicado](managed-evidence-export.md). Exporta apenas metadados por
serializers próprios, sem duplicar conteúdo, plano bruto, output ou patch.
Destino privado novo fora da source/store, preview e aprovação single-use são
obrigatórios. Origem/run/store não recebem writes de export; content export e
patch permanecem fora do escopo. Source is never modified. Same-UID processes
are trusted; não há isolamento contra proprietário/administradores hostis.
