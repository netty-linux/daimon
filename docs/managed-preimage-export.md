# Preimage Export — experimental, Linux amd64 only

Exporta exatamente uma captura privada verified de uma operação replace_file,
selecionada exclusivamente pelo operation ID de 64 caracteres SHA-256 hexadecimal
minúsculo. Esse ID é derivado internamente de run, plano aprovado e índice; não é
um path nem um hash de conteúdo escolhido pelo operador. Obtenha-o em report ou
inspect. Apenas run succeeded íntegro com retenção verified é elegível. Runs
legados sem captura, ready/partial/unknown/invalid/missing/discarded são recusados.

```text
daimon managed-workspace --base /tmp/privado/store export-preimage --run <run-id> --operation <operation-id> --destination revisao-1 --source-check /tmp/privado/source --enable-preimage-export
```

Todas as flags são obrigatórias. Nenhum path livre, wildcard, diretório ou
seleção de output é aceito. Limite: uma captura até 65536 bytes; limites menores
existentes continuam aplicáveis. Destination é um nome lógico ASCII minúsculo,
dígitos/hífens, 1–64 caracteres, sem hífen nas extremidades, nunca path externo.

Source is never modified. Same-UID processes are trusted: UID proprietário e
administradores são confiáveis; processos hostis do mesmo UID estão fora do
modelo. Kernel/filesystem/policy devem suportar as primitivas existentes; não há
fallback inseguro nem Windows managed write. Source-check verifica somente
identidade/topologia/overlap, sem bytes regulares ou hash da source.

O executor reutiliza os locks, staging privado, Sync/Close, reabertura/hash,
auditoria externa e renameat2 sem overwrite de Output Export. Usa namespace
irmão separado `.daimon-preimage-exports-<identidade-da-store>`, fora da
source/store/run/output/tombstones/Evidence e dos pacotes Output Export. Pai e
área privados 0700; pacote e arquivos 0700/0600. A seleção interna da captura
não é exibida como path de storage. Preparação ou negativa não provisiona área.

Preview deliberado completo identifica run, operação/índice, path relativo,
expected/captured/approved hashes, tamanho, nome lógico, área, nonce, limites,
deadline e avisos. O digest inclui explicitamente capability=preimage_export,
integridade verified do run, versão da captura e single_use=true. Não contém bytes. A confirmação é exatamente:

```text
EXPORTAR PREIMAGE <digest-da-proposta>
```

A frase de Output Export não autoriza Preimage Export. Retenção não autoriza
exportação. EOF, negativa, entrada inválida, display falho/incompleto e
cancelamento não aprovam. Approval é vinculada à execução, run/operação/plano,
metadata/bytes por hashes e tamanho, destino, integridade e display; single-use,
inclusive se copiada, expira pelo contexto/deadline e é consumida antes de efeitos.

Antes da cópia, revalida run/report/manifest/journals/plano/approval original,
operação replace, captura verificada, identidade/hash/tamanho e destino. Audit
externa metadata-only é criada e sincronizada antes do staging. Apenas
`preimage.bin` recebe conteúdo; `manifest.json` usa schema próprio metadata-only
versionado com nomes explícitos de hash e tamanho. Staging e audit são conferidos
novamente antes do rename sem overwrite; publicação e fechamentos são conferidos
antes de sucesso, incluindo contexto após fechar os handles finais.

Pré-imagem pode conter precisamente segredos que foram removidos pelo replace.
Não há secret scanning garantido, anonimização, criptografia ou secure erase.
Hashes não são anonimização e permitem correlação. Saída operacional normal só
mostra estado/quantidade/tamanho e avisos, sem conteúdo/path/hash completo.
Preview e pacote metadata-only deliberados podem mostrar metadados vinculados.

Falha anterior ao rename retorna not_published, que não significa ausência de
área/audit/staging ou bytes residuais privados. Falha posterior ao rename/close
ou cancelamento final retorna unknown_interrupted, preservando o arquivo já
publicado; não afirma ausência de efeitos. Uma linha anterior de audit de sucesso
não elimina erro posterior. Manifest descreve bytes verificados, não autentica
conclusão contra o UID confiável. Não há garantia contra queda física de energia.

Source, run, output, captura interna e outro run são apenas lidos pelo export.
Evidence Export continua exatamente seis arquivos metadata-only, sem bytes ou
path interno da captura. Output Export mantém seu contrato. Discard explícito
remove o run e sua captura interna, mas export final e audit externos permanecem;
operador é responsável pela permanência. Não há remoção automática de resíduos.

Preimage Export não é Patch Export. Não combina before/after, não exporta output
adicional ou múltiplos arquivos/diretórios, não publica na source, não restaura,
não faz rollback/replay/cleanup. Não chama provider/agent loop/rede/Git/shell ou
subprocessos no runtime. Não cria nenhuma ferramenta de modelo.
