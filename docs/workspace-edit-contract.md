# Contrato proposto para edição do workspace

Estado: proposta, sem ferramenta de escrita e sem alteração da política atual.
O agente permanece somente leitura. Implementar edição exige uma mudança explícita
de escopo no AGENTS.md e revisão própria.

## Preview implementado

`internal/diffview.Render` produz um preview determinístico com três linhas de
contexto, regiões separadas, CRLF visível e marcador de ausência de newline final.
Todas as linhas alteradas são mostradas; exceder limites retorna erro sem preview
parcial. MaxLines e MaxBytes são positivos. Há tetos adicionais de 1000 linhas por
versão e 1 MiB por string de entrada, inclusive caminho, para limitar o trabalho.
O limite de saída conta bytes e inclui cabeçalhos e marcadores.

O formato é para exibição, não é um patch aplicável. Controles e caracteres de
formatação Unicode viram `?`; essa transformação pode tornar conteúdos diferentes
visualmente iguais. Portanto este preview isolado não basta para autorizar escrita.
Antes dessa integração, a exibição deve escapar caracteres de forma inequívoca,
incluindo o próprio caractere de escape e os marcadores de terminadores de linha.

## Requisitos da futura edição

1. Começar com substituição de um único arquivo regular existente, relativo ao
   workspace, com limites explícitos e validação estrita de argumentos. Não criar,
   remover, renomear arquivos nem executar shell nesse corte.
2. Preparar uma proposta imutável: caminho, bytes originais, bytes propostos e
   identificador do conteúdo. Validar todos os limites antes de pedir aprovação.
3. Mostrar a mudança completa em representação inequívoca. Se não couber no
   preview, negar; nunca pedir aprovação de uma mudança parcialmente exibida.
4. Vincular aprovação de uso único à proposta exata, incluindo caminho e bytes.
   Não aceitar aprovação persistente nem permitir que o modelo altere a proposta
   entre a exibição e o efeito.
5. Revalidar conteúdo e identidade do alvo antes de escrever. Se houver mudança,
   recusar sem efeito e exigir nova proposta/aprovação. Uma simples sequência
   read/check/write não elimina corrida: a estratégia de concorrência e troca
   segura precisa ser definida e testada antes da implementação.
6. Definir confinamento com os.Root, política de symlinks/hard links, permissões,
   substituição atômica, falhas e limpeza do temporário. Não prometer sandbox nem
   atomicidade sem evidência em Linux e Windows.
7. Preservar autorização do lote antes de efeitos, budget, cancelamento, recibos
   correlatos e eventos sem conteúdo sensível. Preparação e preview ficam fora
   do modelo e não introduzem retries ou execução concorrente.
8. Cobrir negativa, EOF, cancelamento, proposta alterada, alvo alterado, symlinks,
   limites, controles Unicode, falhas de escrita e ausência de efeitos em rejeição.

## Smoke test local pendente

Na revisão de 2026-10-02, o Ollama instalado não respondeu em 127.0.0.1:11434.
Não houve chamada a provider real, download de modelo ou uso do endpoint remoto
configurado. Os testes HTTP usam servidores simulados locais.

Quando um modelo local com suporte a tools estiver disponível, executar em um
workspace de teste, com chave ausente e base URL local explicitamente configurada:
solicitar uma listagem, conferir os argumentos e aprovar manualmente uma vez;
repetir negando, depois testar read_file em arquivo inofensivo. Confirmar ausência
de execução em negativa e recuperação do modelo após o recibo controlado. Registrar
modelo, resultado e limitações sem credenciais ou conteúdo sensível.
