# Núcleo de workspace: implementação e limites

Este documento distingue trabalho implementado de requisitos ainda pendentes.
Não declara o núcleo concluído nem o produto pronto para produção.

Decisão posterior: o fluxo [managed-workspace](managed-workspace.md) aplica
planos exclusivamente em cópias privadas Linux sob modelo de UID confiável.
As fases abaixo referem-se ao apply na árvore compartilhada, que segue bloqueado.
Não há publicação automática na origem nem equivalência com isolamento absoluto.

## Estruturas reutilizadas

- `agentloop.Loop`: budget, autorização por chamada, histórico defensivo,
  validação final e recuperação única sem ferramentas.
- `policy.WorkspacePolicy`: capabilities por modo e flags, sem grant persistente.
- `editcontract` e `createcontract`: preview completo, aprovação de uso único,
  bytes vinculados, revalidação e executores existentes. Não foram duplicados.
- `workspacefs.Binding`: caminho canônico privado, identidade do diretório,
  hash da root e identificador efêmero da execução.
- `workspaceplan`: parser puro de artefato versionado; não é executor nem tool.
- Erros tipados de `providers/openai`: classificação controlada, sem corpo remoto.

## Progresso por fase

| Fase | Estado |
|---|---|
| 0: inspeção e caracterização | Inspeção e regressões existentes executadas |
| 1: capabilities | Política central e matriz de modos/flags implementadas |
| 2: aprovações | Contratos existentes estendidos com root, execução e operação; uso único preservado |
| 3: confinamento | Canonicalização e revalidação da identidade implementadas; garantia forte sob mutação externa concorrente bloqueada |
| 4: formato | Parser versionado estrito implementado, ainda sem comando de aplicação |
| 5: apply determinístico | Não habilitado: depende da garantia da fase 3 |
| 6: journal e recuperação de aplicação | Journal de metadados implementado e integrado à cópia gerenciada; replay e recuperação de aplicação continuam ausentes |
| 7: limites | Parser tem limites conservadores; budget existente preservado; apply pendente |
| 8: observabilidade | Eventos tipados de validação/recuperação e diagnóstico opt-in implementados; eventos de apply pendentes |
| 9: erros de provider | Classificação tipada e mensagens públicas controladas implementadas |
| 10: documentação | Documentação incremental; exemplos de apply pendentes |

## Aprovações e root

Cada proposta retém privadamente o workspace aberto, bytes copiados e estado
esperado. IDs de aprovação vinculam os hashes, limites, path, operação, root e
execução. A cópia pública do review não retargeta o permit. Permits consumidos,
negados, cancelados ou invalidados não podem ser reutilizados. A validade está
ligada ao contexto da execução; não há autorização persistente.

A root é canonicalizada ao abrir. Um alias/symlink de root fornecido pelo
operador é resolvido uma vez para a root física; as rotas relativas de escrita
não podem atravessar symlinks. A identidade física e o caminho canônico são
revalidados durante a preparação e imediatamente antes das operações existentes.

Isso não impede um escritor externo autorizado pelo SO de mover um diretório
entre a última verificação e a syscall. O teste Linux
`TestCanonicalPathConfinementRequiresExternalMutationIsolation` reproduz uma
escrita transitória no diretório movido para fora do caminho canônico original.
A operação falha e a limpeza remove o arquivo, mas uma escrita transitória já
ocorreu. O teste caracteriza a limitação; passar não prova confinamento forte.

`os.Root` confina resolução relativa ao diretório aberto, não imobiliza a árvore
no namespace do filesystem. Acrescentar mais verificações ou um lock cooperativo
não excluiria renames por outro processo com permissões equivalentes. Até existir
uma estratégia comprovada de isolamento dessas mutações, o novo apply não será
habilitado. Os executores anteriores continuam restritos a Linux e workspace
controlado, com as limitações existentes explicitamente preservadas.

## Artefato estruturado v1

O plano textual humano e o envelope atual de `--validate-scope` não são
automaticamente convertidos para este artefato. O formato interno abaixo é para
aplicação determinística futura e ainda não há CLI `apply-plan` nesta versão:

```json
{
  "version": 1,
  "kind": "workspace_apply",
  "operations": [
    {
      "type": "create_file",
      "path": "docs/NOTES.md",
      "content": "Fixture\n",
      "precondition": { "absent": true },
      "validation": { "sha256": "<SHA-256 real do conteúdo final>" }
    },
    {
      "type": "replace_file",
      "path": "src/config.txt",
      "content": "mode=final\n",
      "precondition": { "sha256": "<SHA-256 real dos bytes originais>" },
      "validation": { "sha256": "<SHA-256 real do conteúdo final>" }
    }
  ],
  "blockers": [],
  "assumptions": []
}
```

Os placeholders do exemplo precisam ser substituídos por hashes hexadecimais
minúsculos reais de 64 caracteres. Eles são recusados pelo parser.

Limites padrão: 256 KiB do JSON; duas operações; no máximo uma criação e uma
substituição; 64 KiB finais por arquivo; 128 KiB finais totais; path de 4096 bytes
e 16 componentes; até quatro suposições de 256 bytes. Um plano aplicável não tem
bloqueios. Pré-condições e hash de validação são obrigatórios. Paths usam subset
ASCII portátil; aliases de dispositivos Windows e conflitos por diferença de
caixa ou ancestralidade são rejeitados. Nulls, campos desconhecidos/duplicados,
versões desconhecidas, fences, envelopes múltiplos e texto residual são recusados.

## Observabilidade e provider

`workspace --root "diretório" --diagnostic plan --validate-scope "pedido"`
ativa diagnóstico deliberado no stderr. Ele registra apenas categorias fixas,
reconhecimento do template e contagens de eventos. Não imprime pedido, conteúdo,
path, ID remoto, Authorization ou chave. Não altera aprovações ou tools.

A classificação de provider usa tipos de erro e status HTTP. HTTP 404 não prova
que o modelo é inválido: pode indicar endpoint inexistente. Timeout e cancelamento
preservam as causas; `model_error` continua sendo StopReason quando aplicável.
`invalid_response` local não é classificado como falha do provider. Não há retry,
fallback, diagnóstico de texto remoto nem desativação de TLS.

## Isolamento físico: política bloqueada por plataforma

`workspacefs.ApplyIsolation` retorna `Strong=false` em todas as plataformas.
`WorkspaceApply` nega inclusive create/replace e apply_plan, mesmo com ambas as
flags. Não há CLI de aplicação. O fallback é leitura/planejamento, nunca um
executor lexical que se anuncie fisicamente confinado. Os executores legados de
arquivo único não adquirem esta garantia por existir este módulo.

| Plataforma | Primitivas investigadas | Garantia efetiva / situação |
|---|---|---|
| Linux amd64 | openat2 BENEATH, NO_SYMLINKS, NO_XDEV; O_NOFOLLOW; diretórios abertos | Recusam escapes durante resolução. Não imobilizam um inode movido por outro processo. Apply bloqueado. |
| Windows | NtCreateFile relativo a handle, FILE_OPEN_REPARSE_POINT; compartilhamento sem FILE_SHARE_DELETE | Teste local de handle sem delete-sharing impede rename/remoção do diretório aberto. Ainda não constitui protocolo completo para toda a cadeia, reparse points, troca atômica e filesystems. Apply bloqueado. |
| Outras plataformas/arquiteturas | Nenhuma implementação forte validada | Não suportado; apply bloqueado. |

Fontes locais consultadas: Go `src/os/root.go`, `src/os/root_windows.go`,
`src/internal/syscall/windows/at_windows.go` e Linux `linux/openat2.h`.
Go os.Root mantém o diretório original após rename e não proíbe cruzamento de
mounts. No Windows seus handles permitem FILE_SHARE_DELETE; não fornecem o
protocolo de exclusão necessário. Canonicalização e segunda checagem não eliminam
TOCTOU. O teste openat2 desta rodada resolve um arquivo por um pai aberto mesmo
após esse pai ser movido para fora da root; não o utiliza como executor.

Namespaces/chroot não foram adotados: no Docker local `unshare -Urnm` falhou com
EPERM. Não foram pedidos privilégios adicionais. Mesmo um bind mount/chroot de
uma árvore compartilhada não impede um escritor no host de mover seus inodes.
Uma alternativa Linux exige árvore privada e política de acesso que exclua
escritores externos, além de protocolo de publicação seguro; publicar de volta
no workspace original reabre a questão. Outra alternativa é um ambiente gerido
com ownership/mounts e ameaça explicitamente limitada, ainda não implementado.
Windows exige handles exclusivos para todos os ancestrais, abertura relativa,
inspeção de reparse tags e publicação testada em cada filesystem suportado.
Não se assume equivalência entre NTFS, ReFS, SMB, bind mounts ou outros FS.
Administradores/host privilegiado também não podem ser considerados excluídos
por uma sandbox de processo comum. Nenhuma dessas alternativas foi habilitada.

## Journal independente

`workspacejournal` recebe um io.Writer explicitamente fornecido pelo chamador.
Não abre arquivos, não lê credenciais, não cria destino padrão, não adiciona flag
e não executa operações. Registra JSONL v1: sequência, timestamp UTC, tipo,
path relativo portátil, hashes anterior/final e status de enumeração fechada.
Não aceita conteúdo, erro livre, prompt ou segredo como campo do registro.
Paths e hashes são metadados deliberados privados: não são resumo público.
Hashes não são anonimização; dados previsíveis podem ser inferidos por dicionário.

Limites: duas operações, paths de até 4096 bytes/16 componentes, no máximo oito
registros. Estados prepared -> started/denied/failed; started ->
succeeded/failed/unknown. Falha depois de started representa efeito desconhecido,
nunca afirma ausência de alteração. Resumo tem apenas contagens e partial.
Um sucesso e outro resultado não confirmado são parcial, sem rollback implícito.
Resultados são relatados pelo chamador, não verificados pelo journal.

Write curto, erro ou falha de Sync inutilizam a instância sem retry. Quando o
sink suporta Sync, cada registro é sincronizado antes do retorno; isso sozinho
não garante durabilidade do diretório, hardware ou filesystem. Sem Sync não há
garantia de recuperação após crash. Não há parser de recuperação/replay nesta
rodada. O executor da cópia gerenciada persiste started antes do efeito e para
se o journal falhar. Esse vínculo não habilita apply na árvore compartilhada,
que permanece bloqueado.
Os testes do pacote journal simulam sucesso, negação, erro antes de efeito,
falha parcial, concorrência e falhas do sink. Separadamente, a suíte gerenciada
exercita aplicação real offline na cópia, inclusive pela CLI executável.
Report é read-only: confere manifest, marcador exclusivo, journal, plano aprovado
e hashes dos arquivos confirmados. Artefatos contraditórios ou incompletos não
são sucesso. Staging residual ou tentativa interrompida resulta em
unknown_interrupted. Não há autenticação contra falsificação coerente pelo UID
confiável, snapshot global atômica ou auditoria de arquivos fora do plano.

## Validação opt-in de escopo do plano

Sintaxes equivalentes:

```text
daimon workspace --root "diretório" plan --validate-scope "pedido"
daimon workspace --root "diretório" plan "pedido" --validate-scope
```

Exige um único pedido não vazio e aceita a flag no máximo uma vez. O parser
preserva os bytes do argumento; quoting e expansão de variável são responsabilidade
do shell do operador. Não existem novas ferramentas ou permissões.

Este contrato reconhece exclusivamente o seguinte template ASCII, com caminhos
relativos simples e literais curtos (não é um parser geral de linguagem natural):

```text
Analise esta fixture. Proponha criar um arquivo curto de documentacao em docs/ e alterar somente src/config.txt de mode=initial para mode=final. Nao execute alteracoes. Use ferramentas somente quando precisar de evidencia. Registre incertezas em Bloqueios.
```

O diretório, arquivo e literais podem variar conforme o pattern fechado de
`recognizePlanScope`. Pedidos fora desse template mantêm o comportamento de plan
livre, mesmo com a flag: não recebem validação parcial nem garantia de escopo.
Sem a flag, o comportamento anterior continua preservado.

Quando reconhecido, o provider recebe instrução para produzir as sete seções
humanas com títulos exatos, em ordem, e um único envelope final
`<daimon-plan-scope>` / `</daimon-plan-scope>` em linhas próprias. O JSON obrigatório
lista create, modify, delete, operations (path/old/new) e blockers. O validador
recusa duplicatas, null nas listas, campos desconhecidos, JSON residual, fences,
caminhos ambíguos, exclusões e operações fora do escopo. O bloco tem limite de
16384 bytes. Uma criação diretamente no diretório indicado e exatamente uma
substituição literal no arquivo indicado são exigidas; blockers aceita somente
information_missing, read_denied, read_failed e budget_exhausted.

A resposta final incompatível permite no máximo uma recuperação dentro do budget
existente: o pedido original e os recibos permanecem no histórico, uma instrução
explícita pede o formato correto, e o request não contém tools. Qualquer tool call
na recuperação é recusada antes de autorização/execução. A resposta recuperada
passa pelo mesmo validador; falha não exibe plano parcial. Resposta vazia ou envelope
HTTP inválido falha fechado no provider/loop, sem recuperação adicional.
Cancelamento, limites e erros de modelo interrompem o fluxo; não há retry de rede.
O envelope é interno e não aparece no display do plano validado.

A validação garante estrutura e escopo declarado no JSON; não prova equivalência
semântica da prosa livre, veracidade dos fatos do modelo nem existência física de
um caminho proposto. O plano continua sem execução e sem persistência. Nenhuma
operação create/replace/apply/discard/export é registrada como ferramenta de plan.
Requests de teste usam somente httptest local, sem credenciais.
