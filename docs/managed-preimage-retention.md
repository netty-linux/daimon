# Retenção privada de pré-imagens — experimental, Linux amd64 only

Retenção é opt-in explícito por run durante managed apply, somente para a única
operação `replace_file` permitida pelo plano. Não altera o fluxo legado sem flag.
Source is never modified. Same-UID processes are trusted: proprietário e
administradores são confiáveis; processos hostis do mesmo UID estão fora do modelo.

```text
daimon managed-workspace --base /tmp/store-privado apply --run <id> --plan /tmp/plano.json --enable-replace-file --enable-preimage-retention
daimon managed-workspace --base /tmp/store-privado report --run <id>
daimon managed-workspace --base /tmp/store-privado inspect --run <id>
```

O managed apply legado habilita as operações do plano mediante seu preview e
aprovação. Para retenção, a confirmação explícita `--enable-replace-file` é
obrigatória junto de `--enable-preimage-retention`. Retenção não habilita replace
sozinha. Ambas as flags pertencem exclusivamente a managed apply; não se aplicam
a workspace plan, create, list, inspect, discard ou export. Plano sem replace
recusa retenção. `create_file` nunca recebe pré-imagem.

O preview informa retenção de conteúdo potencialmente sensível, limite de 64 KiB,
quantidade, alvo relativo e metadados da versão aprovada. Bytes anteriores não
são adicionados ao preview. O display integral, plano, run e precondições são
vinculados à aprovação de uso único. Negativa, EOF, falha de display ou
cancelamento não capturam conteúdo. Nenhuma opção de aprovação permanente existe.

## Captura e integridade

Após aprovação e revalidação, os bytes atuais do alvo privado são conferidos
contra o hash esperado. A captura é exclusiva, limitada a 64 KiB; limites mais
restritivos de importação, plano, conteúdo UTF-8, linhas e executor prevalecem.
Não se consulta source atual, Git ou provider para reconstruí-la.

Dentro do run, diretório privado `preimages/` usa 0700 e seus arquivos usam 0600.
Nomes são derivados internamente de run, hash do plano e índice da operação,
nunca de paths fornecidos pelo modelo. Esse namespace fica fora de output; plano
e ferramentas não podem acessá-lo. Conteúdo não é serializado em journal,
manifest, report, tombstone, inventário exportado, logs ou saída normal.

Fluxo: conferir alvo → escrever captura exclusiva → Sync/Close do arquivo e
diretório → reabrir/conferir bytes, hash e identidade → persistir/Sync/Close do
journal metadata-only de captura → registrar metadata verificada → vincular
manifest → revalidar captura, alvo e journal → executor existente → confirmar
pós-imagem → journal de execução → manifest/report.

Formato interno v1 registra run ID, operation ID/índice, path relativo,
`expected_before_sha256`, `captured_before_sha256`, `approved_after_sha256`,
`preimage_size_bytes`, timestamp UTC, hash do plano e da aprovação e sequência
do journal de execução. Identidade física do storage é privada. Nenhum campo
transporta conteúdo nos artefatos metadata-only. Hashes são SHA-256 completos,
hexadecimal minúsculo, 64 caracteres; não são anonimização e permitem correlação
de conteúdo conhecido.

Estados são `not_requested` (opção registrada, captura ainda não iniciada),
`capturing`, `captured`, `persisted`, `verified`, `failed`, `unknown`.
Sem opt-in, ausência de metadata significa nenhuma retenção solicitada.
Verificado exige persistência, fechamentos, conferência e journal durável no
modelo de I/O documentado. Falha impede replace; evidência parcial permanece
para inspeção, sem limpeza automática. Falha após começo de operação continua
unknown/partial conforme os efeitos confirmados. Não há atomicidade de lote,
restauração automática ou garantia contra queda real de energia/disco defeituoso.
Contagem refere-se a capturas integralmente verificadas. Em estado failed ou
unknown podem existir arquivos incompletos, mesmo com contagem zero; isso não
afirma ausência de bytes ou efeitos. Esses resíduos permanecem no run.

## Lifecycle e export

Report e inspect exibem apenas estado, contagem, tamanho e hashes metadata-only.
List mostra somente contagem/status da retenção. Verificação não faz repair.
Pré-imagem divergente, truncada, trocada entre runs/operações ou vínculo
inconsistente impede tratar o run como execução íntegra.

Evidence Export mantém exatamente seus seis arquivos e tipos próprios; não
inclui bytes, caminhos de storage nem cópias cegas dos artefatos de captura.
Um run íntegro continua elegível ao export metadata-only, mas retenção não
autoriza nenhuma modalidade de conteúdo. Discard explícito e aprovado remove
a árvore do run, incluindo capturas; não há garantia de apagamento seguro no
filesystem, snapshots ou hardware. A auditoria externa permanece metadata-only.

Runs legados sem pré-imagem continuam nas funções atuais. Não podem ser
considerados automaticamente elegíveis para uma futura operação dependente de
captura; ausência nunca permite reconstrução por source atual. Esta entrega
não implementa essa operação futura.

Preimage retention is not patch export.
Preimage retention is not content export.
Preimage retention is not rollback.
Preimage retention is not publication.

Sem output export, replay, retomada automática, retenção por idade, limpeza
automática ou escrita Windows. Não há secret scanning nem promessa de ausência
de segredos. Store/destinos devem obedecer ao contrato privado existente;
permissões privadas dependem do UID e do sistema operacional.
