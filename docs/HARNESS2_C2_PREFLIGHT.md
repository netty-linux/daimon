# C2 — decisão de pré-provisionamento Hyper-V

Auditoria somente leitura em 2026-10-23; base do PR #28 antes desta atualização:
`e6fe3962b3f09d3d4698d0094f862f334a9ea67e`. **Provisionamento BLOQUEADO.**
Não há homologação de isolamento, executor ou mudança dos 18 gates unverified.
Nenhuma mudança administrativa foi executada. Esta entrega permite decidir os
próximos passos de aquisição; ainda não permite autorizar uma instalação completa.

## Inventário público, sem identificadores privados

| Verificação | Resultado e alcance |
| --- | --- |
| CPU/RAM | 8 CPUs lógicas; aproximadamente 15,85 GiB físicos. Memória disponível oscilou entre 6,9 e 7,7 GiB, abaixo da reserva proposta de 8 GiB |
| Armazenamento | Volume NTFS de trabalho com aproximadamente 175 GiB livres; acima da reserva proposta de 120 GiB. Capacidade não é reserva exclusiva nem quota |
| Hyper-V | Hipervisor presente; vmms Running; módulo disponível; sessão não administrativa |
| VMs/switches | Get-VM e Get-VMSwitch falharam com VirtualizationException/NotSpecified. Inventário desconhecido, não vazio; conflitos ainda não excluídos |
| Windows | Windows 10 Pro build 19045. Get-HotFix informou registro mais recente em 2026-09-10; não comprova instalação de todas as correções ou cobertura ESU |
| Suporte | Suporte regular encerrado em 2025-10-14. Exigir confirmação independente da cobertura vigente e revisão de patches; nenhuma ativação/licença foi alterada ou segredo consultado |
| WSL/Docker | Somente distro interna docker-desktop identificada. API Docker indisponível nesta amostra (named pipe ausente); nenhum serviço/distro foi iniciado. Auditoria anterior com daemon alcançável é histórica |
| ISO/verificador | Nenhuma ISO na raiz do projeto; não houve busca em diretórios pessoais. Nenhum gpg/gpgv no PATH. Disponibilidade de ISO em outros locais desconhecida |

Memória e disco devem ser medidos novamente imediatamente antes de criar/ligar a
VM. Docker/WSL podem competir por memória/CPU/disco; sua indisponibilidade não
comprova ausência de consumo. Não fechar aplicações ou parar serviços implicitamente.
[Suporte Microsoft](https://learn.microsoft.com/en-us/lifecycle/products/windows-10-home-and-pro).

## Aquisição autenticada e manifesto

`harness2-c2-deployment.v1.json` é proposta versionada, não lock instalável e não
entrada autorizadora de execução. Null/NOT_RUN/UNRESOLVED impedem instalação.
Os pins anteriores foram preservados como candidatos, sem reclassificar hashes
anunciados como verificados. Não se baixou ISO nem se verificou assinatura nesta
tarefa: falta verificador disponível e cadeia de aquisição completa.

Procedimento posterior, em estação/verificador confiável, sem importar confiança
do próprio bundle:

1. Obter chave Ubuntu pelo endereço oficial documentado; confrontar fingerprint
   da chave de imagem `843938DF228D22F7B3742BC0D94AA3F0EFE21092` com canal oficial
   independente do espelho de imagem. Validar vigência/revogação no momento da aquisição.
2. Baixar SHA256SUMS, SHA256SUMS.gpg e ISO da release exata. Em keyring exclusivo
   contendo somente a chave aprovada, executar `gpgv --keyring ./ubuntu-image.gpg
   ./SHA256SUMS.gpg ./SHA256SUMS`. Exigir status válido e fingerprint esperado;
   depois comparar SHA256 dos bytes da ISO à entrada exata autenticada.
   Guardar evidência, versões do verificador, tamanho e hashes; assinatura inválida,
   arquivo ausente ou chave inesperada interrompem o processo.
3. gVisor: autenticar metadata APT assinada, chave aprovada por canal independente,
   versão exata e cadeia Release → Packages → pacote. Rejeitar metadata expirada,
   replay e downgrade. O tarball/tag unsigned atual não satisfaz essa condição.
   Não instalar pacote automaticamente: maintainer scripts podem configurar Docker.
4. Inventariar todos os arquivos do bundle da mesma versão: runsc, gvisor-bin e
   sidecars, shim quando aplicável, modos e links; não executar download em startup.
   Lista exata ainda pendente: não inferir layout completo pelo nome do tarball.
5. Congelar versões/arquiteturas/hashes de kernel GA, systemd, bibliotecas, rootfs,
   dependências e perfis, com cadeia de pacotes assinada. Não misturar GA/HWE nem
   usar latest. Registrar builder e reprodução. Nenhuma closure completa existe ainda.
6. Antes de cada implantação revisar avisos de segurança, expiração e revogação;
   qualquer atualização cria nova revisão do manifesto e exige revisão humana.
   Versões revogadas não recebem fallback. Assinatura local é aprovação adicional,
   nunca substituto de autenticação de origem.

[Verificação Ubuntu](https://documentation.ubuntu.com/security/software-integrity/image-verification/),
[layout e aquisição gVisor](https://gvisor.dev/docs/user_guide/install/).

## Configuração proposta e alterações que exigem autorização

Uma VM Generation 2, 2 vCPUs, RAM fixa 4 GiB, VHDX dinâmico com teto 48 GiB,
em diretório novo e privado aprovado pelo operador; Secure Boot com template
Ubuntu compatível, a validar. Não iniciar automaticamente. Sem checkpoints
automáticos, compartilhamentos Windows, enhanced-session redirection ou credenciais
de produção. Não reutilizar diretórios, discos ou nomes existentes.

Autorizações futuras devem enumerar exatamente:

- Criar diretório exclusivo com ACL restrita a administrador/SYSTEM e identidade
  Hyper-V necessária; criar VHDX, configuração da VM e mídia ISO autenticada.
- Configurar somente essa VM: CPU/RAM/boot, checkpoints e serviços de integração
  revisados; remover NIC inicial antes de qualquer boot. Não alterar configuração
  global de enhanced session ou switches compartilhados.
- Opcionalmente conectar manutenção a switch existente identificado e aprovado,
  sem criar/alterar switch, NAT, firewall ou adaptador compartilhado. Instalar pela
  console com administração controlada; remover NIC antes de experimentação.
- No guest, criar identidades distintas de administração, supervisor e workload,
  sem sudo/credenciais para workload; configurar pacotes autenticados e controles
  somente em escopo dedicado. Nenhum script guest foi preparado nesta entrega.
- Autorizar descarte específico depois de verificar ownership; nunca remover
  VM/disco por prefixo de nome ou por encontrar um diretório.

Rede experimental inicialmente ausente. Isso não comprova bloqueio de loopback
no guest ou gVisor: as provas negativas do plano-base continuam NOT RUN.

## Scripts preparados e limites explícitos

`scripts/harness2-c2-preflight.ps1` é somente leitura, repetível, recebe caminhos
locais explícitos e nome proposto. Verifica RAM/disco, administração, inventário,
conflitos de nome/diretório, ancestrais reparse point e presença de ISO. Emite JSON
sem caminhos, nomes de VMs/switches ou dados de licença; exit 3 indica bloqueio.
Não autentica imagens, não reserva recursos e não comprova isolamento.

Exemplo somente leitura (substituir parâmetros localmente, não publicar caminhos):

```powershell
& ./scripts/harness2-c2-preflight.ps1 -LabRoot $ApprovedNewDirectory -IsoPath $SelectedISO
```

`scripts/harness2-c2-provision.ps1` é uma entrada administrativa **bloqueada**:
descreve recursos propostos e recusa inclusive Apply/Discard. Não foi executada.
Não contém implementação mutante nem promessa de provisionamento pronto. Evita
um instalador parcialmente autenticado ou um cleanup perigoso enquanto faltam
manifesto e inventário. Preparar o provisionador efetivo e seu descarte continua
pendente; não basta remover um throw ou preencher um booleano no manifesto.

O provisionador futuro deve validar criptograficamente a closure antes de qualquer
escrita; recusar conflitos; persistir journal protegido com VMId aleatório retornado
pelo Hyper-V, IDs dos discos, manifesto e caminhos canônicos exatos. Rerun só pode
ser noop se estado e ownership coincidirem integralmente; qualquer divergência
falha fechado. Crash parcial exige reconciliação, sem exclusão automática. Descarte
deve exigir VM desligada, VMId e discos exclusivos correspondentes, ausência de
reparse points e containment de cada caminho sob raiz aprovada, sem remover ISO
compartilhada/switch ou VM prévia. Não implementar delete recursivo por nome.

## Validação e decisão

Resultados de parser, preflight e revisão de diff estão registrados na entrega do
PR. Suítes Go/Python/UI não foram repetidas localmente por esta alteração apenas
documental/PowerShell; CI é reportado separadamente por HEAD. Instalação, boot,
provisionamento, descarte e enforcement: NOT RUN. Nenhum teste de disponibilidade
é PASS de isolamento. Falhas Windows históricas permanecem na documentação C2/C1.

Validação local desta revisão:

- PASS: parser AST PowerShell nos dois scripts; comandos do provisionador limitados
  a Write-Output, sem invocação dinâmica ou mutadores. O provisionador não foi executado.
- PASS de recusa: preflight real exit 3, RAM 7,05 GiB/disco 174,86 GiB; inventário
  indisponível e imagem ausente registrados, sem recursos criados.
- PASS: caminhos relativos rejeitados (exit 1); diretório já existente reportado
  como conflito (exit 3). Não se criaram arquivos para esses ensaios.
- PASS: JSON do manifesto parseável e estado bloqueado; git diff --check.
- NOT RUN: assinatura/checksum da ISO, proveniência completa gVisor, provisionamento,
  testes administrativos/cleanup e provas de enforcement. A falta de gpg/Docker
  disponível não foi contornada por instalação automática.

Para autorizar provisionamento: memória suficiente e inventário elevado somente
leitura completo; suporte/patches comprovados; ISO e todos os artefatos autenticados;
closure sem pendências; scripts administrativos e cleanup implementados/revisados;
nomes, caminhos, ACLs e eventual switch aprovados explicitamente; plano de rollback
por ownership. **Essas condições ainda não estão satisfeitas.** Todos os 18 gates
continuam unverified; RequireRealExecution continua ErrUnavailable. Sem C3 ou Sessions.
