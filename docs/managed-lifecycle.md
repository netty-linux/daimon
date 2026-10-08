# Lifecycle de cópias gerenciadas — experimental, Linux amd64 only

Este fluxo nunca modifica a source root nem publica o output nela. O UID
proprietário, processos do mesmo UID e administradores são confiáveis.
Não há isolamento contra processos hostis do mesmo UID. Apply em roots
compartilhadas permanece bloqueado.

List/inspect e tombstones usam `total_size_bytes` para tamanhos numéricos.
Tombstones vinculam `state_sha256` e `preview_sha256`. Nomes legados são
recusados sem reparação; veja o [contrato metadata-only](managed-evidence-export.md#schema-metadata-only-e-incompatibilidade-legada).

```text
daimon managed-workspace --base /tmp/daimon-store create --source /input
daimon managed-workspace --base /tmp/daimon-store apply --run <id> --plan /input-plan.json
daimon managed-workspace --base /tmp/daimon-store report --run <id>
daimon managed-workspace --base /tmp/daimon-store list
daimon managed-workspace --base /tmp/daimon-store inspect --run <id>
daimon managed-workspace --base /tmp/daimon-store discard --run <id> --enable-discard
```

`--base` vem primeiro. Flags específicas aceitam qualquer ordem, sem repetição,
flags desconhecidas ou argumentos extras. Run ID é um identificador de 32
hexadecimais minúsculos, nunca um path. Sem `--enable-discard`, o comando é
recusado antes de abrir a store ou exibir aprovação.

| Comando | Efeito | Source | Preview/aprovação | Auditoria |
|---|---|---|---|---|
| list | leitura limitada | não acessada | não | nenhuma escrita |
| inspect | leitura limitada | não acessada | não | nenhuma escrita |
| discard | remoção de um run privado | não acessada | completo, uma vez | tombstone fora do run |

List e inspect não provisionam, reparam ou removem artefatos. A saída JSON
separa `declared` do estado `state` verificado, mostra presença de artefatos
separadamente dos indicadores `*_verified` (presença não comprova integridade),
timestamp, contagens, bytes e hash do inventário quando completos. Não contém
output, conteúdo, plano, prompts ou credenciais. Ler pode atualizar atime;
bytes e mtimes não são atualizados pelo lifecycle de leitura.

## Estados da visão lifecycle v1

| Estado | Evidência |
|---|---|
| ready | manifest válido, sem tentativa/staging, snapshot inicial correspondente |
| succeeded | Report verifica journal, plano aprovado e hashes das operações concluídas |
| partial | Report íntegro sem conclusão total; inclui tentativas negadas/falhadas, diferenciadas por declared |
| unknown_interrupted | tentativa/staging residual ou auditoria incompleta; não permite retomada |
| invalid | artefato, inventário ou identidade inseguros/inconsistentes |
| missing | run ausente e sem auditoria de descarte |
| discarded | run ausente e tombstone terminal completo e válido |

O verificador existente de Report é reutilizado. Ele verifica os outputs das
operações confirmadas; não autentica metadados contra o proprietário confiável
nem fornece evidência histórica de cada arquivo não afetado pelo plano.
O inventário adiciona validação de tipos, identidade, limites e hashes atuais.
Ausência de manifest, journal esperado, plano aprovado esperado ou output
esperado não é sucesso. Dados nulos, duplicados, truncados ou inesperados são
recusados. Um tombstone adulterado não prova descarte.

Store vazia produz `[]`. Entradas com nomes que não sejam IDs estritos são
ignoradas sem ecoar esses nomes; candidatos com IDs válidos e tipo inseguro
aparecem como invalid. Máximo de 256 entradas na store e no diretório de
tombstones, 256 IDs combinados, 128 entradas por run, 4 MiB por inventário,
1 MiB por arquivo e por preview. Exceder falha integralmente.

## Descarte

O descarte aceita apenas runs com evidência íntegra ready, succeeded ou partial.
Staging, interrupção ou auditoria residual bloqueiam a remoção; não há reparo,
retry ou mecanismo alternativo de limpeza neste comando.

O preview privado deliberado mostra store em ASCII escapado, ID/caminho relativo,
formato, estado declarado/verificado, presença de artefatos, lock/staging,
contagens/bytes, hash de estado, limites e advertências. Não mostra conteúdo.
Privacidade de logs não significa esconder a ação de quem a aprova.
Responda `n` (ou EOF) para negar sem remover nem criar tombstone. Responda `y`
somente depois de conferir a cópia identificada. Falha de display e cancelamento
impedem aprovação. Não existe sempre-permitir.

A aprovação é vinculada à capability discard, store e identidade do run,
inventário de bytes/metadados (incluindo modo/ctime e versão do diretório do run),
resumo, preview e prazo do contexto. Cópias do
permit compartilham consumo atômico. Alteração posterior invalida a aprovação.
Um flock exclusivo no diretório coordena processos cooperantes: apply mantém
o lock desde Prepare até Run.Close; discard mantém desde o preview até terminar.
Não execute versões antigas que não participem deste protocolo de locks
concomitantemente sobre a mesma store. Store deve permanecer aberta enquanto
suas propostas/permits estiverem em uso; Close da proposta encerra sua tentativa.
Não se confunde esse lock ativo com attempt.lock, que é marcador permanente.

Antes da primeira remoção, cria-se exclusivamente
`store/_tombstones/<id>.jsonl`, com arquivos 0600 e diretório 0700. O registro
inicial, seu diretório e a store são sincronizados. O tombstone não é removido
com o run. Contém versão, sequência, timestamp UTC, store/ID, identificador da
aprovação, hashes de estado/preview, estado inicial, contagens e status/motivo
controlados. Não inclui conteúdo, prompts ou segredos. Falha inicial de
escrita/sync impede remoção. Uma auditoria existente impede nova tentativa.

Após revalidar, a remoção ocorre por entradas verificadas, relativas a roots
abertos; não usa RemoveAll, shell ou subprocesso. Identidade do run, store,
tipos e metadados são conferidos antes de efeitos. Outros runs e tombstones
nunca entram no inventário removido. O diretório da store é sincronizado depois
da remoção. Falhas depois de iniciar são conservadoramente unknown; não há
afirmação de rollback ou ausência de efeitos. Sucesso exige registro terminal
sincronizado. Audit incompleta após crash permanece unknown_interrupted.
Cancelamento ou falha de fechamento/sync final retorna unknown_interrupted,
mesmo quando os bytes do registro terminal já foram escritos. Uma inspeção
posterior só pode confirmar descarte com registro completo, válido e run ausente;
verificar bytes atuais não comprova persistência perante uma falha futura de energia.

Para inspecionar uma interrupção, execute inspect com o ID original; não aplique
nem descarte automaticamente. `started` sem terminal não comprova descarte.
O run não é recriado e não se interpreta ausência como descarte sem tombstone.

## Garantias e limites

Ownership/modes privados, ancestrais protegidos e a política openat2
NO_XDEV/NO_SYMLINKS/BENEATH existente impedem acesso de outros UIDs ao run.
Store precisa estar no filesystem raiz do namespace; bind mounts, symlinks,
hard links e especiais são recusados. Base e nomes do inventário precisam ter
UTF-8 válido, evitando perda de identidade na serialização dos hashes/auditoria.
Roots abertos não impedem um proprietário
confiável ou administrador de mover diretórios: não existe garantia contra
mutação hostil desse UID entre checagens e efeitos. Não se anuncia confinamento
absoluto nem proteção baseada somente em canonicalização.

Windows e arquiteturas não suportadas bloqueiam antes de provisionar/remover.
Filesystems aceitos pela policy não são todos empiricamente validados; execução
Docker offline testa o filesystem local disponível, sem privilégios adicionais.
Não há snapshot global atômica de source mutável, atomicidade de lote,
publicação, rollback, replay, retomada após crash ou limpeza automática.
Não se adicionam ferramentas do modelo, provider, agent loop, rede, Git ou shell.
