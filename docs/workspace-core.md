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
