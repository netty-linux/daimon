# C2 — primeiro laboratório limpo, offline

## Decisão de escopo — 2026-10-24

Esta revisão atende à separação solicitada pelo proprietário. Não altera a
admissão do Harness², não homologa isolamento, não libera candidatos ou C3.

| Marco | ESU/Windows | Outros requisitos | Decisão atual |
| --- | --- | --- | --- |
| Criar a VM **desligada**, disco vazio e ISO autenticada | Cobertura e patches continuam UNVERIFIED; aceitar explicitamente o risco residual desta preparação limitada | Administrador via UAC legítimo, inventário elevado sem conflito, raiz nova NTFS, 120 GiB livres, ISO com tamanho/hash fixados | Tecnicamente viável; falta autorização específica/UAC e validação administrativa final |
| Primeiro boot pela ISO stock, **sem instalar** Ubuntu | Não é requisito técnico documentado de Hyper-V; host continua sem homologação de segurança | Criação validada, zero NICs, ISO privada autenticada, VMId exato, autorização de boot separada e pelo menos 8 GiB disponíveis | Bloqueado por RAM nesta amostra, além de criação/autorização pendentes |
| Instalar Ubuntu | Exige decisão própria sobre o risco residual do host | Autorização específica e plano de instalação revisado | NOT AUTHORIZED; nenhum unattended/autoinstall/guest command implementado |
| gVisor e provas hostis | Confiança/patches/ESU do host precisam ser comprovados antes dos testes | Proveniência gVisor, closure de deployment, supervisor e políticas/provas correspondentes | BLOCKED; todos os 18 gates unverified |

A [Microsoft lista Windows 10 Pro nos requisitos de Hyper-V](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/host-hardware-requirements).
O [New-VM cria](https://learn.microsoft.com/en-us/powershell/module/hyper-v/new-vm)
e o Start-VM liga em operações distintas. A documentação não lista inscrição ESU
como pré-condição dessas operações. A conclusão limitada é técnica: ESU não
demonstrado não prova que o Hyper-V recusará criação/boot. **Não** é declaração
de que um host sem patches seja seguro. ESU/patches permanecem bloqueadores da
homologação hostil. Não haverá alteração do Windows para obter esta preparação.

RAM: configurar 3/3/4 GiB em uma VM Off não inicia o guest nem reserva sua memória
de execução. O mínimo local anterior de **8 GiB disponíveis permanece no boot**;
não foi reduzido para acomodar a amostra de 6,57 GiB. A criação desligada foi
separada desse requisito a pedido do proprietário. O VHDX é dinâmico, com teto de
48 GiB; isso não é teto de todo o diretório. A cópia ISO usa cerca de 3,80 GiB;
configuração e metadados são adicionais. Mantida a reserva pré-criação de 120 GiB.

## Executor e fronteiras de autorização

Entry point: `scripts/harness2-c2-provision.ps1`; implementação:
`scripts/harness2-c2-lab.psm1`. Requer Windows/NTFS, PowerShell 7.4+ existente e
sessão elevada. Nenhuma elevação automática. O agente deve iniciar a operação
aprovada por UAC legítimo; o proprietário apenas confirma o diálogo, sem copiar
comandos. Cancelamento do UAC não autoriza fallback ou mudança de grupos/ACLs
preexistentes. O módulo não é um serviço privilegiado nem uma capability segura
para entrada de modelos.

- `Plan`: somente proposta, sem criação de diretório/journal.
- `Create`: cria somente a operação aprovada e termina com VM **Off**.
- `Inspect`: lê journal protegido; não transforma relatório em admissão.
- `Start`: exige outra autorização e VMId exato, verifica configuração/mídia e
  mede RAM imediatamente antes do primeiro Start. Nenhum input no instalador.
- `Discard`: exige autorização própria e VMId exato quando há VM registrada.
  Exige VM Off; nunca a desliga implicitamente.

Todos os modos mutantes exigem acknowledgement exato `AÇÃO:operation_id`.
Essa string é uma confirmação de parâmetros do operador, **não** prova
criptográfica de aprovação humana. A aprovação específica existe na conversa;
nenhum argumento de CLI a substitui. `ReceiptPath` opcional escreve um arquivo
novo no diretório do operador para leitura após UAC, sem sobrescrever arquivos.
O recibo é advisory, nunca é aceito para ownership, reconciliação ou descarte.

Proposta fixa: Generation 2, 2 vCPUs, RAM dinâmica 3 GiB mínimo/startup e 4 GiB
máximo, VHDX novo dinâmico 48 GiB, zero NICs e nenhum switch criado/modificado.
Secure Boot usa MicrosoftUEFICertificateAuthority e o primeiro boot é o DVD da
ISO autenticada. Não há checkpoints, nested virtualization, serviços de
integração ativos ou compartilhamentos Windows configurados. Autostart Nothing;
no desligamento do host, TurnOff apenas para esta VM descartável (sem prometer
preservação de dados do guest). Windows Update e reboot permanecem operações
separadas e não estão no executor.

## Ownership, persistência e descarte

1. Raiz explicitamente nova, um único nível sob parent existente, sem UNC, ADS,
   traversal, aliases Windows ou reparse points. CreateDirectoryW cria com DACL
   protegida e falha se outro diretório vencer a corrida; não aplica ACL em raiz
   preexistente. Raiz, lock e journal: somente Administradores/SYSTEM; owner
   Administradores. Outras autoridades administrativas locais continuam confiáveis.
2. Lock exclusivo por operação. Journal v2 de chaves únicas, limitado a 64 KiB,
   vinculado ao UUID e a todos os caminhos/nomes exatos. Intenção persistida antes
   de cada criação. Escrita CreateNew/WriteThrough/Flush e substituição de arquivo
   no mesmo diretório NTFS. Falha de persistência interrompe a operação.
3. Tamanho e SHA256 da ISO fixados no código a partir da aquisição autenticada.
   Cópia privada CreateNew, hash novamente após cópia e antes de boot; não monta
   ou executa conteúdo no Windows. O pacote gVisor não participa deste marco.
4. DiskIdentifier e VMId retornados pelas APIs são persistidos; nada é adotado
   por nome, prefixo, UUID escrito em arquivo do usuário ou falta de visibilidade.
   A VM só recebe acesso aos próprios arquivos: ISO Read e VHDX FullControl pelo
   seu SID exato, conforme o modelo de permissões do Hyper-V. Journal/raiz não
   concedem acesso ao SID da VM.
5. Repetir Create em configured valida a mesma VM, sem criar outra. Configuração
   parcial com VMId persistido pode ser retomada. Se a criação de disco/VM tiver
   acontecido, mas a resposta/registro do ID se perder, a operação fica pendente:
   nenhuma adoção, retry cego ou exclusão automática. Requer reconciliação elevada
   explícita; é um limite deliberado de segurança, não recuperação mágica.
6. Discard verifica VMId/localização, estado Off, ausência de checkpoints,
   attachments permitidos, identidade/tamanho/tipo/parent do VHDX e referências
   em **todas as VMs e checkpoints** antes de remover mídia. Erro de inventário
   bloqueia exclusão. Remove-VM é usado só na VM exata e sem checkpoints, evitando
   merges implícitos. VHDX e cópia ISO são removidos separadamente por caminho
   literal validado. Não há delete recursivo de diretórios.
7. Falha após remoção pode ser reconciliada contra inventário elevado confiável
   sem repetir efeitos indevidamente. Estado descartado é tombstone idempotente.
   Journal, lock, diretórios e eventuais arquivos residuais de configuração/tmp
   são **retidos para auditoria**, não apagados por varredura. Discard significa
   VM desregistrada e mídias próprias removidas, não limpeza universal do diretório.

Não se promete durabilidade de diretório contra perda de energia, imunidade a
outros administradores ou contenção de um host já comprometido. Journal ausente,
malformado, ACL alterada ou efeito ambíguo falham fechado. Relatórios do próprio
workload nunca são autoridade de ownership. Instalação e execução de candidatos
não existem neste executor.

## Revisão e evidências deste marco

- PASS: 55 testes offline, incluindo escrita perdida antes/depois de efeitos,
  Start com resposta perdida, idempotência, cleanup interrompido, estado Running,
  IDs divergentes, journal adulterado/duplicado/excessivo, VM/path/VHD trocados,
  refs estrangeiras e drift de rede, integração, boot, memória e firmware.
- PASS: criação nativa em caminho **já existente** foi recusada, com ACL original
  inalterada. Validador de ACL testado com objetos de segurança simulados.
- PASS: teste NTFS real de escrita/Flush/Replace e leitura, com autoridade ACL
  simulada para rodar sem elevação; destino bloqueado preserva o journal anterior.
  Regressão da primeira tentativa administrativa: PowerShell converte `$null`
  em string vazia no argumento backup do File.Replace; corrigido com NullString.
- PASS: parsing PowerShell e diff check. CI Windows executa os testes sem Hyper-V
  real; repositório público, runner padrão, sem novo serviço pago.
- PASS: SHA256 integral da ISO existente novamente igual ao pin; não houve novo
  download. Disco: 171,04 GiB livres; destino proposto não existe.
- BLOCKED para autoridade atual: sessão não administrativa; inventário Hyper-V
  recusado. Não se repetiu ESU/Windows Update nem os inventários autenticados já válidos.
- Primeira tentativa administrativa autorizada: criação iniciou e parou na
  atualização do journal por esse erro, antes da cópia ISO/VHDX/VM. Retomada da
  operação original depende da validação do journal protegido; não apagar a raiz
  nem criar uma segunda operação para contornar o estado existente.
- Retomada com null corrigido: execução chegou à criação do VHDX, mas recusou
  registro de resource ID antes de New-VM. Adaptador passou a normalizar GUID
  string com chaves/maiúsculas, sem alterar o VHDX. O formato real neste host
  aguarda consulta elevada: UAC da inspeção foi cancelado. Estado pendente não
  autoriza reutilizar/apagar o arquivo; requer reconciliação explícita.
- Retomada após inspeção/reconciliação explícita: VHDX existente registrado,
  VM criada **Off**, VMId persistido; configuração interrompida pela expectativa
  incorreta de ConfigurationLocation. Corrigido layout real: New-VM -Path indica
  container e Hyper-V acrescenta o nome da VM. Validação permanece exata.
- Configuração aplicada na retomada seguinte; validação de mídia encontrou a
  marca Windows AutoInherited com DACL ainda protegida e ACEs idênticas. Removida
  comparação textual de SDDL em favor de comparação completa de regras efetivas,
  owner e proteção. Não se ignoram ACEs extras, direitos, denies ou herança real.
- NOT RUN: boot, instalação, calibração e descarte real. Configuração final da
  VM ainda precisa ser concluída e validada. Os 18 gates continuam unverified.

Correções da revisão: criação segura sem adotar diretório concorrente; prevenção
de merge de checkpoints; referências estrangeiras também para ISO; permissões
do SID da VM separadas das do journal; boot separado da criação e de instalação;
recusa de boot repetido/ambíguo com estado Off. O código foi revisado nesta sessão;
não houve revisão independente de outro agente ou homologação administrativa.

## Escopo da autorização de criação

O proprietário autorizou **somente criação desligada**, com aceitação do risco
residual ESU/host UNVERIFIED para esse marco limitado. A operação faz a checagem elevada final,
recusará conflitos e criará um diretório privado novo, journal/lock, cópia ISO,
VHDX vazio e uma única VM. Se a checagem elevada falhar, não contornará o bloqueio.
Parâmetros privados exatos e UUID do plano são apresentados localmente ao
proprietário; não publicar caminhos de recibo/contas/host no PR.

Ações que resolvem os bloqueios restantes: confirmar UAC para obter autoridade
administrativa da operação aprovada; para boot, disponibilizar pelo menos 8 GiB
(o proprietário decide quais apps encerrar) e aprovar o boot separadamente; para
testes hostis, comprovar ESU/patches e concluir confiança gVisor/deployment.
