# C2 — decisão de pré-provisionamento Hyper-V

## Windows e supply chain — verificação de 2026-10-23

**Provisionamento: BLOCKED.** Base inspecionada `5352e830744948de0d986fd4cd5e229260a1ce24`,
checkout inicialmente limpo. Só documentação/manifesto alterados. Memória e calibração
3–4 GiB continuam pendências independentes; os 18 gates permanecem unverified.

### Windows: atualização do OS insuficiente e ESU UNVERIFIED

Windows 10 Pro x64, 19045.6466. Histórico local completo disponível (546 entradas,
QueryHistory, sem scan online) registra último cumulativo de OS identificado:
KB5071959, sucesso em 2026-02-26; KB5066791 em 2026-02-21. A build atual corresponde
ao [OOB de novembro de 2025](https://support.microsoft.com/en-us/servicing/os/windows-10/2025/11/november-11-2025-kb5071959-windows-10-version-22h2-os-build-19045-6466-out-of-band).
Datas de instalação não são datas de publicação. Get-HotFix isoladamente não
apresentou todos os mesmos KBs; histórico e build foram confrontados, sem inventar
reparo ou reset do catálogo.

Referência oficial atual: [KB5126657, 2026-10-13, build 19045.7807](https://support.microsoft.com/en-gb/servicing/os/windows-10/2026/10/kb5126657-windows-10-21h2-22h2-security-update).
Não aplicada conforme build/histórico consultados; atualizar requer revisão de
elegibilidade, pré-requisitos/SSU e autorização administrativa. Não foi executada
busca de aplicabilidade online, portanto não se lista cada pacote faltante como
diagnóstico individual. KB5126256 é preparação ESU, não cumulativo de segurança;
Defender/.NET não foram contados como patch do OS.

Suporte regular encerrado; [ESU exige inscrição/ativação aplicável](https://learn.microsoft.com/windows/whats-new/enable-extended-security-updates),
Windows 22H2 e pré-requisitos atualizados. SoftwareLicensingProduct consultado só
por classificação: 24 entradas ESU, zero LicenseStatus=1. Cobertura **UNVERIFIED**,
sem inferir modalidade consumer nem publicar IDs/chaves. Serviços wuauserv/UsoSvc
Running, BITS Stopped (manual); isso não comprova integridade de componentes nem
exige reparo. Indicadores CBS e WindowsUpdate de reinício pendente: false. Nenhuma
ativação, instalação, reparo ou reinício realizado.

### Ubuntu: assinatura da lista PASS; bytes ISO NOT RUN

Metadados pequenos baixados de releases.ubuntu.com/24.04 e keyring de
archive.ubuntu.com/ubuntu/project/ubuntu-archive-keyring.gpg para staging local
não versionado sob .git. Nenhuma imagem grande baixada. GnuPG 2.4.9 já existente
na instalação Git foi localizado fora do PATH; isso supera a limitação anterior
de verificador não localizado, sem instalar software ou alterar keyring global.

Fingerprint `843938DF228D22F7B3742BC0D94AA3F0EFE21092` conferida separadamente na
[documentação Canonical](https://documentation.ubuntu.com/security/software-integrity/image-verification/).
`gpg --batch --no-options --homedir <staging> --no-default-keyring --keyring
<keyring-baixado> --status-fd 1 --verify SHA256SUMS.gpg SHA256SUMS` retornou exit 0,
GOODSIG e VALIDSIG dessa fingerprint, assinatura em 2026-09-15. Chave sem expiração
no keyring obtido; nenhuma revogação indicada nele, sem prova independente de
freshness de revogação. TRUST_UNDEFINED é esperado sem trustdb global: autenticação
usou comparação explícita de fingerprint, não confiança automática do GPG.
A primeira tentativa falhou por sintaxe de caminho Windows no GPG MSYS; corrigir
para caminho MSYS permitiu validação, sem aceitar a falha como sucesso.

SHA256 dos bytes obtidos de SHA256SUMS:
`728064ecf411f4ab702d9c3ca0a938ce672771424c028a1b52b2449f7eb5a068`.
Entrada autenticada para ubuntu-24.04.5-live-server-amd64.iso:
`97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8`.
**ISO CANDIDATE, integridade dos bytes NOT RUN**. Não houve busca em arquivos
pessoais nem aprovação para download grande. Após download explicitamente aprovado,
revalidar assinatura/fingerprint/revogação e calcular hash da ISO inteira.

### gVisor: cadeia APT parcialmente demonstrada; origem ainda UNVERIFIED

GitHub API oficial confirmou release-20261019.0 publicada em 2026-10-22, commit
760b80785dc53311d4c87bd2de167525a99709c1; mais recente na consulta de releases.
Tag unsigned. Consulta de advisories públicos retornou lista vazia; isso **não**
é prova de ausência de vulnerabilidades ou correções sob embargo. Notas da release
e [política de backports](https://gvisor.dev/release/) revisadas; pin exato não
recebe futuras correções automaticamente. Nenhuma versão foi trocada silenciosamente.

Canal recomendado [APT oficial](https://gvisor.dev/docs/user_guide/install/):
Release/Release.gpg e Packages amd64 baixados, sem instalar APT/runsc. Assinatura
matematicamente válida (exit 0, VALIDSIG) pela chave obtida em gvisor.dev/archive.key,
fingerprint `6F1DF85E3A71C24918E727D56FC6D554E32BD943`. Falta corroborar essa chave
por fonte independente aprovada; HTTPS e assinatura com a própria chave baixada
não fecham confiança de produtor. Estado geral **UNVERIFIED**.

Release de 2026-10-22 autentica digest/tamanho do Packages baixado: SHA256
`9eee2a2c0c09542751dfd12741597d42206c5d0b21ba9e20519b734f71eadc64`, 597 bytes.
Packages anuncia runsc amd64 20261019.0, pacote 76.131.500 bytes e digest no manifesto.
Pacote NÃO baixado/verificado/executado. Versão futura deve ser exata (`runsc=20261019.0`),
com captura dos metadados autenticados e revisão de maintainer scripts; canal por
data pode receber backports e não basta para reprodução. Release sem Valid-Until
observado: exigir política externa de freshness/anti-replay antes de implantação.

Layout declarado pelo código pinado tools/release.bzl e BUILD: runsc, shim
containerd-shim-runsc-v1 e seis sidecars listados no manifesto. Não equivale ao
inventário byte-a-byte do pacote. Preservar diretório gvisor-bin junto a runsc,
inclusive auxiliares condicionais, sem downloads implícitos. Systrap evita exigir
KVM aninhado; mínimo upstream Linux 5.6+ não substitui kernel suportado corrigido.
Kernel/systemd/libs/rootfs, modos/links e dependências reais continuam sem closure
autenticada. Nenhum binário baixado foi executado; nenhuma instalação feita.

### Próximas ações e validação

1. Comprovar inscrição ESU vigente ou planejar OS suportado; aprovar atualização
   cumulativa/SSU apropriada e eventual reboot separadamente. Até lá, host bloqueado.
2. Aprovar download ISO grande e verificar seus bytes contra a lista autenticada.
3. Corroborar chave APT gVisor externamente, revisar revogação/freshness, verificar
   pacote e todos os arquivos/dependências sem executar conteúdo adquirido.
4. Congelar kernel/systemd/rootfs e políticas autenticados, revisar provisionador
   e cleanup; somente depois decidir provisionamento. RAM continua independente.

Manifesto diferencia VERIFIED da assinatura de metadados, CANDIDATE da ISO e
UNVERIFIED do runtime; status global BLOCKED. JSON parseado, invariantes de schema
version 1 verificadas e git diff --check; não existe schema formal de deployment
no projeto identificado nesta revisão. Suites Go/Python/UI não repetidas por mudança
somente documental; CI novo consultado separadamente. Checks verdes não autenticam
artefatos. Evidências pequenas ficam no staging local; nenhum caminho privado,
produto/licença/token ou arquivo adquirido foi incluído no commit.

Dimensionamento reavaliado: [alternativa dinâmica 3/3/4 GiB e plano de consumo](HARNESS2_C2_INFRASTRUCTURE_PLAN.md#alternativa-de-dimensionamento-sob-demanda--revisão-de-2026-10-23).
O usuário informou inventário administrativo sem VMs e com um switch interno;
não repetido nesta sessão. A proposta não altera scripts/manifesto nem configurações.
Memória atual 6,69 GiB disponível: PENDENTE mesmo com alocação inicial menor.
ESU/patches e autenticação de artefatos continuam pendências obrigatórias separadas.

## Diagnóstico administrativo complementar — 2026-10-23

**Resultado: PENDENTE**, não APTO PARA PROVISIONAMENTO. HEAD inspecionado
`423f348d7df0caafed00becca1e1a965b1159482`, branch esperada e checkout inicialmente
limpo. Não foi comprovada incompatibilidade de hardware. Permanecem bloqueios
obrigatórios de acesso, memória e autenticação; nenhum recurso foi provisionado.
Os registros abaixo atualizam as amostras históricas das seções seguintes.

| Área | Evidência atual somente leitura | Interpretação |
| --- | --- | --- |
| Instalação Hyper-V | Win32_OptionalFeature: os sete componentes Microsoft-Hyper-V consultados, incluindo hypervisor, serviços, clientes e PowerShell, com InstallState=1; HypervisorPresent=true | Instalado e hipervisor ativo; criar/ligar VM ainda não validado |
| Serviços | vmms Running/Automatic; vmcompute e hns Running/Manual | Serviços ativos, sem reinício ou mudança de configuração |
| Autoridade | WindowsPrincipal.IsInRole(Administrator)=false; SID do grupo Hyper-V Administrators ausente do token atual | Sessão sem autoridade demonstrada para inventário/provisionamento; não se inferiu ausência de associação administrativa fora desse token |
| Erro de inventário | Get-VM/Get-VMSwitch: VirtualizationException → ServerConnectionException → CimException; mensagens classificadas localmente como acesso negado, camadas superiores mencionam elevação/administração | Falta de acesso comprovada para essas consultas. Nenhuma tentativa de elevar ou contornar UAC |
| Inventário | Consulta CIM de virtualização mostrou somente objeto host; inventário de VMs/switches/discos permanece incompleto | Não usar visibilidade parcial como prova de ausência de recursos/conflitos; não varrer discos pessoais |
| CPU/RAM | 8 CPUs lógicas, 15,85 GiB físicos e 6,21 GiB disponíveis na amostra | 2 vCPUs plausíveis; faltam aproximadamente 1,79 GiB para reserva mínima de 8 GiB. Não reduzir reserva |
| Disco | Volume NTFS de trabalho: 174,86 GiB livres | Satisfaz capacidade proposta de 120 GiB, sem garantir reserva/ACL/conflitos; nenhum VHDX foi criado |
| Consumo | Maiores working sets agregados por tipo: navegador ~4,38 GiB; aplicação de conversa ~2,30 GiB; comunicação ~1,00 GiB; serviços ~0,83 GiB; Node ~0,47 GiB | Dados de processos, sem command lines, conteúdo ou paths. Working sets incluem páginas compartilhadas; somas não equivalem a RAM exclusiva nem recuperação garantida |
| Docker/WSL | docker-desktop Stopped/WSL2; API Docker indisponível por named pipe ausente; só wslservice observado entre processos pesquisados | Nenhum consumo grande de vmmem identificado nessa amostra; não demonstra conflito de arquitetura nem autoriza parar serviços |
| Windows | Windows 10 Pro 19045.6466 | Versão lida do OS/registro; não afirmar atualização completa |
| Update | Histórico local: últimas 100 entradas com ResultCode=2; 93 classificadas como inteligência de segurança, 7 outras, nenhuma identificada como cumulativa nessa janela | Sucesso de inteligência de segurança não comprova patch do kernel/OS. QueryHistory não iniciou scan, download ou instalação |
| Serviços Update | UsoSvc Running; BITS Stopped/Manual; wuauserv passou de Stopped para Running entre amostras, sem intervenção desta tarefa | Estado transitório não é falha por si; nenhum serviço foi iniciado/parado pelo diagnóstico |
| Reinício | CBS RebootPending=false, WindowsUpdate RebootRequired=false, PendingFileRenameOperations ausente/vazio | Nenhum sinal nesses três indicadores; não prova ausência de toda condição possível de reinício |
| ESU | SoftwareLicensingProduct com Name LIKE '%ESU%': 24 entradas, zero LicenseStatus=1 | Nenhuma licença ESU ativa demonstrada por essa consulta. Não basta para excluir todas as modalidades consumer; cobertura vigente permanece não comprovada |

Get-HotFix segue mostrando KB5126256 (2026-09-10) como registro mais recente.
Esse KB é **preparação de licenciamento**, não inscrição ESU nem atualização de
segurança do Windows, conforme a [Microsoft](https://support.microsoft.com/en-us/servicing/os/windows-10/2026/09/kb5126256-windows-10-21h2-22h2-standalone-cbs).
Conferir cobertura/validade do programa aplicável e patch cumulativo vigente
separadamente, pela [orientação ESU oficial](https://learn.microsoft.com/windows/whats-new/enable-extended-security-updates).
Não foram lidos product keys, IDs de ativação ou contas vinculadas.

Computer Use: skill lida e runtime inicializado; inventário de interfaces e
tentativa de abrir Configurações não produziram janela targetable. Nova consulta
de janelas confirmou ausência da interface alvo; não se contornou a falha por
automação alternativa. Hyper-V Manager não foi iniciado via terminal nem houve
diálogo UAC controlado. Gerenciador de Tarefas não agregaria evidência sobre a
consulta estruturada já obtida. Nenhuma propriedade foi alterada pela UI.

### Comandos e limitações de reprodução

Consultas utilizadas: git status/rev-parse; Get-CimInstance para
Win32_OperatingSystem, Win32_ComputerSystem, Win32_OptionalFeature,
SoftwareLicensingProduct e Msvm_ComputerSystem; Get-Process agrupado por nome
(sem argumentos/caminhos); Get-Service; Get-Volume; Get-VM/Get-VMSwitch;
Get-HotFix; Microsoft.Update.Session.CreateUpdateSearcher().QueryHistory;
Test-Path/Get-ItemProperty somente nos indicadores públicos de reinício/build;
wsl --list --verbose; docker info; gh pr view. Mensagens brutas com identificadores
do host não foram publicadas. Não se executou script de provisionamento.

A parte do inventário que exige elevação foi interrompida. Próxima intervenção
manual solicitada: usuário abre PowerShell como administrador por conta própria,
sem automatização do UAC, e executa somente consultas. Exemplo sanitizado:

```powershell
$vms = @(Get-VM -ErrorAction Stop)
$vms | Group-Object State | Select-Object Name, Count
Get-VMSwitch -ErrorAction Stop | Group-Object SwitchType | Select-Object Name, Count
$disks = @($vms | Get-VMHardDiskDrive -ErrorAction Stop)
$disks | Measure-Object
$disks | Where-Object Path | ForEach-Object {
    Get-VHD -Path $_.Path -ErrorAction Stop |
        Select-Object VhdType, Size, FileSize, Attached
}
```

Não enviar nomes de máquinas, diretórios, discos ou contas no relatório público.
Essas consultas não inventariam discos órfãos/desanexados nem excluem conflitos
no diretório futuro: revisão local do destino e nomes aprovados ainda necessária.
Se a consulta elevada também falhar, preservar código/categoria e investigar
provider/serviços antes de propor instalação ou reparo.

### Pendências, correções recomendadas e autorização posterior

1. **Acesso:** intervenção manual elevada para completar inventário somente leitura.
   Não adicionar usuário a grupos, mudar ACL ou instalar Hyper-V como tentativa
   automática de correção. Elevação de consulta não autoriza provisionamento.
2. **RAM:** usuário decide se fecha cargas dispensáveis ou amplia hardware;
   nenhuma aplicação encerrada aqui. Repetir medição e exigir pelo menos 8 GiB.
3. **Segurança:** verificar inscrição ESU válida e histórico cumulativo adequado;
   se ausentes, apresentar plano de atualização/OS suportado antes de executá-lo.
   Não declarar host seguro apenas por receber definições de segurança.
4. **Supply chain:** autenticar ISO/metadata/pacotes e completar closure de deployment;
   manifesto atual permanece bloqueado. Provisionador e descarte efetivos pendentes.

Alterações administrativas futuras que precisam de autorização específica:
instalar patches/alterar OS ou inscrição ESU se necessário; reiniciar se indicado;
alterar grupos/ACLs somente se uma necessidade for comprovada; criar diretório
privado, VHDX e VM com configuração aprovada; configurar somente essa VM e eventual
conexão a switch existente identificado; provisionar identidades/pacotes no guest;
descarte estritamente por ownership. Nenhuma dessas ações foi executada ou está
autorizada implicitamente pelo diagnóstico. Não há recomendação atual de reparar
Hyper-V, criar switch ou modificar Docker/WSL.

Validação desta atualização: diff somente documental, sem segredos/identificadores;
CI do HEAD auditado 423f348: Go/Linux e UI PASS em push e PR. Novos checks devem
ser avaliados pelo novo HEAD documental. Não houve mudança de código que exigisse
repetir suites locais. Os 18 gates continuam unverified, sem prova gVisor, C3,
execução adversarial ou alteração de Sessions.

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
