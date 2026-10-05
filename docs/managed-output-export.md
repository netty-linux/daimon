# Output Export — experimental, Linux amd64 only

Exporta um único arquivo regular de `output/` de run `succeeded` íntegro.
O conteúdo pode ser sensível. Não há garantia de detecção de segredos,
anonimização, criptografia ou apagamento seguro. Source is never modified.
Proprietário Unix, processos do mesmo UID e administradores são confiáveis;
processos hostis do mesmo UID estão fora do modelo.

```text
daimon managed-workspace --base /tmp/area-privada/store export-output --run <id> --path config.txt --destination revisao-1 --source-check /tmp/area-privada/source --enable-output-export
```

`--path` é relativo a output, sem traversal, symlink, hardlink, arquivo especial,
diretório ou wildcard. `--destination` é um nome lógico de 1 a 64 caracteres
ASCII minúsculos, dígitos e hífens, sem hífen nas extremidades. Não aceita path
externo. Todas as flags acima são obrigatórias e exclusivas deste comando.
Limite de conteúdo: um arquivo, até 65536 bytes; limites mais restritivos do
run/plano/importação continuam aplicáveis. Metadata do pacote: até 4096 bytes;
auditoria até 16384 bytes. Runs ready/partial/unknown/invalid/missing/discarded
não são elegíveis. Runs sem pré-imagem podem exportar output íntegro; não há
captura retroativa nem uso de pré-imagem.

O pai da store deve ser privado (0700, ownership do operador) e aprovado pela
policy de filesystem existente. A Daimon deriva uma área irmã da store com
nome `.daimon-output-exports-<digest-da-store-e-identidade>`, privada, fora da
store/source/run. Área preexistente sem o marcador exato da store é recusada.
`--source-check` vincula o path da origem ao manifest e verifica apenas
identidade/tipo/topologia/overlap, sem abrir bytes regulares da source.
Origem ausente ou substituída após preview bloqueia a execução.

Preparar/visualizar/negar não provisiona a área. A aprovação exige digitar
exatamente `EXPORTAR <digest-da-proposta>` exibido pelo prompt, em uma linha.
`y`, EOF, entrada incompleta, falha de display e cancelamento não aprovam.
A aprovação é específica, vinculada ao run, conteúdo por hash/tamanho,
destino, limites e execução; vale uma vez e expira pelo contexto/deadline.
O preview identifica a ação e seus avisos sem mostrar bytes do arquivo.

Após aprovação/revalidação, a área 0700 e o marcador são provisionados quando
necessário. Auditoria externa metadata-only em `_audit/` precede o pacote.
Staging privado recebe `output.bin` (único arquivo de conteúdo) e
`manifest.json` próprio metadata-only, ambos 0600. Sync/Close, reabertura,
hash/tamanho, run e destino são conferidos antes do rename sem overwrite.
Kernel/filesystem sem as primitivas necessárias falham fechado.
Após o rename, pai, pacote, run e auditoria são conferidos novamente.
Sucesso depende de todos os fechamentos e do contexto ainda válido.

O display deliberado mostra `managed_area` e `destination_name`. O pacote
fica no pai da store / managed_area / destination_name. Para verificar sem
exibir conteúdo, compare SHA-256 e tamanho de `output.bin` com o manifest.
Manifest contém identificadores, path relativo, hashes, tamanho, timestamps,
binding da aprovação e aviso de sensibilidade. Metadata não é anonimização;
hashes completos permitem correlação de conteúdo conhecido. Saída normal
mostra somente estado, quantidade e bytes, não path/hash/conteúdo.

`not_published` significa que este executor não confirmou um rename final;
pode haver área, audit e staging residual com conteúdo. `unknown_interrupted`
após publicação significa que o destino pode existir sem conclusão íntegra
confirmada. O manifest descreve bytes verificados, não autentica uma conclusão
externa de export; auditoria e resultado devem ser considerados. Não há
remoção automática de resíduos, rollback, replay ou retomada após crash.
Mesmo uma linha de auditoria anterior de sucesso não suprime erro posterior
de close/contexto. Não há prova física contra queda de energia nem atomicidade
de lote ou revogação de cópias. Não há secure erase.

O run, output, outro run e origem são apenas lidos. Discard do run não remove
o pacote exportado nem sua auditoria externa. O operador é responsável pela
permanência; nenhum comando de limpeza do novo pacote é adicionado.

Evidence Export continua exatamente seis arquivos metadata-only e não recebe
conteúdo. Este corte não implementa Preimage Export, Patch Export, árvore,
múltiplos arquivos, publicação na origem, restore, rollback, replay, limpeza
automática, criptografia ou Windows managed write. Não adiciona ferramenta de
modelo, provider, loop de agente, rede, shell, Git ou subprocesso no runtime.
