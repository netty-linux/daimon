# Managed Workspace Evidence Export — experimental

**Linux amd64 only.** O único export implementado é metadata-only.
Content export e patch são futuros contratos separados, não capacidades atuais.
Não há secret scanning, remoção de segredos ou garantia de conteúdo secret-free.

```text
daimon managed-workspace --base /tmp/daimon-store export-evidence --run <id> --destination /tmp/daimon-review/evidence-1 --source-check /input --enable-export-evidence
```

O operador deve preparar um pai novo/privado 0700, como `/tmp/daimon-review`,
pertencente ao UID executor. O destino deve ser um path absoluto canônico, novo,
com basename no subset ASCII portátil, sem nome de controle iniciado por ponto.
Não cria pais automaticamente nem aceita destino já existente, mesmo vazio.
O pai não pode sobrepor source ou store, nem ser ancestral/descendente delas.

`--source-check` é obrigatório: o manifest legado guarda somente o hash do path
canônico da origem. Este argumento verifica esse vínculo e a identidade física
da origem, sem ler seu conteúdo. Exige a origem original ainda disponível, sem
symlinks, com path absoluto exato. Origem movida/inacessível bloqueia export.
Não resolve novamente aliases. Essa exigência evita aceitar como destino a
origem desconhecida a partir de um hash. Não escreve na origem.

## Elegibilidade e pacote

Aceita somente `ready` íntegro ou `succeeded` íntegro. Partial, interrupted,
invalid, missing, discarded e artefatos divergentes/truncados ficam bloqueados.
Em ready, journal/plano/relatório persistido devem estar ausentes: o pacote
declara `journal_present=false`, `approved_plan_present=false` e
`report_persisted=false`; journal vazio e operações vazias representam ausência,
não execução. Lock/staging ou um report inesperado impedem essa exportação.

O pacote contém exatamente seis arquivos 0600 em diretório 0700:

| Arquivo | Transformação dedicada |
| --- | --- |
| export-manifest.json | Versão, estado verificado, flags, limites, avisos fixos, hashes e tamanhos dos outros cinco arquivos |
| run-manifest.json | Run ID, timestamps, contagens, hashes da referência de origem e snapshot; sem path absoluto ou regras livres |
| journal.jsonl | Sequência, timestamp, operação, path relativo, hashes e status validados |
| report.json | Estado e operações confirmadas, ordem, hashes e tamanho final |
| approved-plan.metadata.json | Operações, precondições por hash e status; sem content, blockers ou assumptions |
| inventory.metadata.json | Inventário atual do output: path relativo, tipo, tamanho, hash e modo octal; sem bytes de conteúdo |

Limites: seis arquivos, 64 KiB por artefato, 256 KiB no pacote, 32 entradas de
inventário, duas operações e oito registros do journal. Runs maiores podem ser
válidos para outros comandos e exceder estes limites conservadores de export.
Preview integral: limite de 1 MiB. Prazo da CLI: dois minutos, incluindo aprovação.
Limites de duração são cooperativos, não interrupção rígida de syscalls.

Flags obrigatórias do manifest:

```json
{"export_kind":"evidence","contains_file_content":false,"contains_patch_content":false,"secret_free_guarantee":"not_applicable","source_not_modified":true,"content_export_not_included":true}
```

Todo artefato passa por serializer dedicado sem campos de conteúdo. Não copia
raw JSON do run. Não inclui output, pre/postimages, patch, diffs, prompts,
respostas de provider, erros livres ou campos livres do plano. Runs succeeded
legados funcionam sem preimagem. Hashes não são anonimização; nomes/paths e
metadados deliberados ainda podem ser sensíveis. Não há promessa de ausência
de segredos arbitrários em nomes escolhidos pelo operador.

## Aprovação, auditoria e falhas

Preparação é read-only. Mantém flock exclusivo do run, sem criar lock no run,
impedindo apply/discard/export concorrentes cooperativos durante a aprovação.
Preview informa destino, source-check, run, estado, os seis hashes/tamanhos,
limites e avisos. Paths absolutos são ASCII escapados no display deliberado.
Resumo/erros públicos não incluem esses paths nem bytes de conteúdo.
EOF, negativa, entrada inválida, cancelamento e display incompleto bloqueiam.
Aprovação é de uso único, compartilhado entre cópias, vinculada ao preview,
run/inventário versionado, operações, hashes, bytes de metadados e destino;
expira com o contexto de preparação/aprovação.

Auditoria externa: `<pai>/.daimon-evidence-audit/<export-id>.jsonl`, com 0700/0600.
Registra started e sincroniza arquivo/diretório/pai **antes** de criar staging.
Falha inicial impede staging/publicação; pode deixar auditoria incompleta.
Staging privado: `<pai>/.daimon-evidence-staging-<export-id>`. Sincroniza os seis
arquivos e diretório, verifica pacote, revalida run/destino/auditoria e publica
por renameat2(RENAME_NOREPLACE) no mesmo filesystem, sem overwrite/fallback.
Após rename, sincroniza pai, verifica pacote e run novamente, então registra
exported. Erros após início são unknown_interrupted; não prometem ausência de
efeito. Registra esse estado adicional quando a auditoria ainda permite escrita.
Nunca faz retry de export nem remove resíduos automaticamente.

A API Go `VerifyEvidence(ctx, destino)` é read-only: exige exatamente os seis
arquivos, schemas estritos/canônicos, hashes, estados/ops consistentes e auditoria
externa started→exported íntegra. Ausência, truncamento, estado unknown ou registro
extra bloqueiam; não repara nada e não consulta a store/source. O pacote isolado
sem sua auditoria adjacente não comprova conclusão. SHA-256 do export manifest
e hash conjunto são vinculados à aprovação/auditoria; manifest não se auto-hasheia.

Export também verifica os arquivos não afetados. O framing v1 da snapshot usa
paths, tipos, tamanhos e hashes. Remove do inventário as criações aprovadas e usa
o hash anterior aprovado da única substituição permitida. O tamanho anterior
dessa substituição é determinado pelo total importado menos os arquivos
inalterados. O conjunto deve produzir o hash original da snapshot. Isso confere
metadados; não reconstrói bytes antigos, não captura pré-imagens e não habilita
patch. Runs fora desses limites atuais falham fechado.

Para revisão manual, confira as seis flags acima, a lista exata de seis arquivos
e os cinco pares SHA-256/tamanho em `artifacts`. Compare cada arquivo com o hash
e tamanho listado. Compare o SHA-256 do próprio export manifest com o preview
aprovado e `manifest_sha256` da auditoria adjacente. Conferir hashes isolados não
substitui a validação de schemas, journal e estado realizada por VerifyEvidence.
Use inspect antes do comando para conferir elegibilidade; não há modo force.
Negação/EOF não criam pacote nem auditoria. Destino existente ou sob source/store
falha antes do prompt. Partial/interrupted/invalid também bloqueiam preparação.

Verificação é de evidências atuais, não de durabilidade histórica: erro de
Close/Sync pode impedir registrar o estado desconhecido, mesmo havendo bytes
legíveis. O retorno com erro prevalece operacionalmente sobre uma verificação
posterior; não reclassificar tentativa desconhecida só porque seus bytes existem.
Não há garantia contra queda de energia ou hardware defeituoso.

Cancelamento também é conferido após os fechamentos finais. A CLI fecha a store
antes de imprimir o resultado; falha nesse fechamento ou cancelamento tardio
não imprimem exported. Cancelamento durante o último Close pode ocorrer quando
a auditoria já está fechada: o retorno unknown_interrupted com erro continua
prevalecendo sobre os bytes legíveis da trilha.

## Modelo de ameaça e limites

Source is never modified: export não escreve nem publica na origem, run, outro
run ou store. Leituras de evidências podem atualizar atime. Exportar não é
publicar. Same-UID processes are trusted: proprietário e administradores são
confiáveis. DAC privado, handles confinados e openat2 rejeitam symlinks/mount
crossings, mas não isolam processos hostis do mesmo UID nem administradores.
Não há snapshot global atômica da origem mutável ou imobilização de árvores.
Sem shell/Git/rede/provider/agent loop/subprocesso/publicação/rollback/replay ou
limpeza automática. Windows e outras arquiteturas bloqueiam antes de efeitos.

Content export futuro exigirá opt-in separado, aviso de conteúdo potencialmente
sensível, confirmação reforçada, succeeded íntegro e bytes deliberados, sem
prometer secret scanning completo. Patch futuro exigirá preimagem capturada e
vinculada durante apply: nunca reconstruir pela source/snapshot atual. Nenhum
desses contratos está habilitado; flags output/content/patch são recusadas.

Testes desta rodada usam apenas fixtures com segredos sintéticos, sem provider
real, credenciais ou rede. Testes de Sync/permits/falhas são injeções locais e
não comprovam durabilidade sob crash real ou falha física de disco.
