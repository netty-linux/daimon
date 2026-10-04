# Plano estruturado e journal — Managed Workspace v1

Feature experimental, somente Linux amd64. [Managed workspace](managed-workspace.md)
aplica este formato apenas em cópia privada. A origem não é aberta para escrita.
O proprietário, processos do mesmo UID e administradores são confiáveis.
Apply em roots compartilhadas permanece bloqueado; não há publicação, rollback,
replay, retomada após crash ou limpeza automática.

## Artefato estruturado v1


Planos humanos não são automaticamente convertidos para este artefato.
O formato abaixo é aceito por managed-workspace apply; workspace compartilhado
não possui CLI apply-plan habilitada:

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

## Journal e integridade

workspacejournal recebe um io.Writer do executor, sem conteúdo, prompts ou
credenciais nos registros. JSONL v1 contém sequência, timestamp, tipo, path
relativo, hashes anterior/final e status. São no máximo duas operações e oito
registros. Write curto, erro ou falha de Sync inutiliza a instância sem retry.
Started deve ser persistido antes de cada efeito; falha bloqueia novas operações.
Falha após started tem efeito desconhecido; sucesso anterior mais falha posterior
é parcial, sem rollback. Não há atomicidade do lote nem durabilidade absoluta.

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