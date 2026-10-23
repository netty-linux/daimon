# C2 — plano operacional de infraestrutura

Pré-provisionamento atualizado: [inventário, scripts e decisão pendente](HARNESS2_C2_PREFLIGHT.md),
com [manifesto v1 incompleto e bloqueado](harness2-c2-deployment.v1.json).

Registro de 2026-10-23. Base auditada: PR #28,
`55fd556991baa6654e5e7e788fa997887f8c219d`. **Plano para execução posterior
autorizada, não provisionamento concluído nem implementação de isolamento.**
Nenhuma configuração global, runtime, daemon, gate ou código de produção foi
alterado. Não foram executadas fixtures adversariais, candidatos ou Sessions.

## 1. Camadas auditadas e interpretação

| Camada | Observação somente leitura | Conclusão permitida / limite |
| --- | --- | --- |
| Windows | Windows 10 Pro 19045; i3-10100F; 8 CPUs lógicas; aproximadamente 16 GiB físicos, 6,1 GiB livres na amostra; D: NTFS com aproximadamente 175 GiB livres | Capacidade plausível para VM pequena; memória livre atual insuficiente para a reserva proposta abaixo |
| Hyper-V | HypervisorPresent=true; módulo Get-VM disponível; vmms Running/Automatic; processo não elevado; Get-VM retorna VirtualizationException | Não comprova VM pronta, inventário vazio ou permissão administrativa. CIM retornou flags de firmware/SLAT false; com hipervisor ativo, resultado inconclusivo, não justificativa para alterar BIOS |
| WSL | 3.0.3.0; kernel informado 6.18.54.1-1; apenas docker-desktop listado, Stopped; nenhuma .wslconfig do usuário encontrada | Não há distro Linux de desenvolvimento administrada identificada. Não foi iniciada/importada distro |
| Docker Desktop | 4.89.0; Engine 29.7.2; containerd 2.3.3; runc 1.4.3; desktop-linux via named pipe; API alcançável | Kernel do daemon identifica WSL2, mas backend efetivamente ativo não foi confirmado: inventário WSL e API são observações distintas. Campos selecionados do settings-store retornaram null; não inferir backend deles |
| Daemon Linux | Kernel 6.18.54.1-microsoft-standard-WSL2, 8 CPUs, aproximadamente 7,7 GiB; cgroup v2/cgroupfs; MemoryLimit=true e CPUCfsQuota=true; runtimes runc/nvidia/io.containerd.runc.v2 | Há suporte anunciado a limites pelo daemon; gVisor não está registrado. Não significa delegação de cgroups para um processo dentro de container |
| Container auditado no #28 | cgroup montado somente leitura, unshare EPERM, runsc ausente nos caminhos previstos | Restrição desse container/política; não demonstra kernel incapaz de namespaces ou cgroups. Não foi usado privileged nem nsenter para contorná-la |

Windows e hipervisor integram a base de confiança. O suporte regular do Windows
10 Pro terminou em 2025; verificar cobertura de atualizações/ESU e patches antes
de aceitar o host para testes hostis. A cobertura não foi auditada nem se afirma
que o host esteja sem correções. [Ciclo de vida Microsoft](https://learn.microsoft.com/en-us/lifecycle/products/windows-10-home-and-pro).

Comandos usados: git status/rev-parse, gh pr view, CIM de OS/computador/CPU/volume,
wsl --version/--status/--list, Get-Service vmms, Get-Command/Get-VM e docker
version/info/context inspect. Somente campos de capacidade foram registrados;
não foram lidos valores de credenciais, tokens ou chaves privadas.

## 2. Ambiente recomendado e dimensionamento

Recomendação: **VM Linux amd64 descartável no Hyper-V existente**, com console
local, sem Docker Desktop dentro dela e sem dependência de cloud. Alternativa
equivalente: máquina Linux física dedicada, vazia e administrada. Uma VM não é
requisito intrínseco do gVisor; aqui fornece administração independente, descarte
e separação dos arquivos/serviços Windows. O serviço Hyper-V ativo não elimina
a necessidade de validar permissões e criação de uma VM após autorização.
[Requisitos Hyper-V](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/get-started/Install-Hyper-V).

Uma distro WSL2 separada seria alternativa a investigar, não homologada: exigiria
systemd, delegação, política de mounts/interop e testes equivalentes, além da
auditoria das pontes com Windows. Não alterar a distro interna do Docker Desktop.
Não usar container aninhado privilegiado como substituto dessa administração.

| Recurso | Mínimo operacional proposto | Recomendado nesta máquina |
| --- | --- | --- |
| CPU | 2 vCPUs amd64, uma execução de fixture por vez | 2 vCPUs; não reservar todos os 8 threads |
| RAM | 4 GiB fixos no guest; pelo menos 4 GiB livres reservados ao host | Exigir pelo menos 8 GiB disponíveis antes de ligar a VM; fechar cargas/encerrar Docker somente com autorização |
| Disco | 40 GiB Linux nativo, sem compartilhamentos NTFS | VHDX limitado a 48 GiB; reservar 120 GiB para base, cópia descartável, ISO e relatórios; impedir snapshots ilimitados |
| Kernel | Linux suportado pela distro, cgroup v2, namespaces e seccomp; verificar cgroup.kill e interfaces de quota | Candidato Ubuntu Server 24.04.5 LTS, kernel GA 6.8 com patches; ABI/pacotes exatos ainda precisam ser congelados |
| Administração | Administrador apenas para provisionar guest, mounts e delegação; conta do workload sem sudo | Contas distintas para administração, supervisão e UID convidado não zero; sem chaves Windows no guest |

Usar `systrap` explícito na versão fixada de runsc. A documentação o recomenda
para VMs e não exige expor /dev/kvm; KVM aninhado não é pré-requisito deste plano.
O upstream consultado exige Linux 5.6+, mas satisfazer a versão mínima não prova
compatibilidade. [Plataformas](https://gvisor.dev/docs/architecture_guide/platforms/),
[instalação](https://gvisor.dev/docs/user_guide/install/).
Manter GA e HWE distintos ao fixar o kernel da distro; não inferir a ABI apenas
do nome 24.04.5. [Ciclo dos kernels Ubuntu](https://ubuntu.com/kernel/lifecycle).

### Alternativa de dimensionamento sob demanda — revisão de 2026-10-23

**Proposta documental, não configuração aplicada. Estado PENDENTE.** O operador
informou inventário administrativo sem VMs registradas e com um switch interno;
essa evidência é fornecida pelo usuário, não repetida por esta sessão sem elevação.
Não conectar a VM ao switch por conveniência: um switch interno permite comunicação
host/guest e não constitui barreira de rede. Permanecem ausência de NIC nos ensaios
e revisão separada da rede de manutenção.

Ubuntu 24.04 amd64 possui LIS integrado e suporte documentado a hot-add e ballooning.
O perfil exato host/kernel ainda precisa de validação; memória dinâmica pode falhar
quando o guest já está sob pressão. Não instalar LIS externo. Consultar driver
hv_balloon e hotplug na imagem autenticada futura; rever compatibilidade GA/HWE sem
trocar kernel implicitamente. [Suporte Microsoft](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/supported-ubuntu-virtual-machines-on-hyper-v).

Startup RAM é a memória disponível durante instalação/upgrade; máximo não garante
disponibilidade física. Ballooning depende do guest e não assegura devolução imediata.
Smart Paging tem uso restrito em reinício, não é reserva operacional nem solução
para sobrescrição. [Semântica Hyper-V](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/dynamic-memory).
Canonical informa mínimo ISO de 1,5 GB e mínimo sugerido de 3 GB para Ubuntu 24.04
amd64. Esses valores não incluem a carga C2 nem homologam 2 GiB para este laboratório.
[Requisitos Ubuntu](https://ubuntu.com/server/docs/system-requirements/).

| Perfil, sempre 2 vCPUs | Mínimo / inicial / máximo | Benefício | Limite |
| --- | --- | --- | --- |
| Conservador original | 4 / 4 / 4 GiB fixos | Capacidade previsível; melhor referência de quotas/latência | Mantém toda a RAM enquanto ligado; plano exigia 8 GiB livres antes do boot |
| Dinâmico econômico, experimental | 2 / 2 / 4 GiB | Menor alocação inicial | Menos margem para instalação, serviços e hot-add; não recomendado como perfil inicial; só avaliar depois de referência estável |
| Dinâmico inicial recomendado | 3 / 3 / 4 GiB | Economiza até 1 GiB frente ao fixo; margem superior à opção de 2 GiB | Pode crescer para 4 GiB, não é garantia de estabilidade nem economia constante |

Escolha inicial proposta: **3 GiB mínimo e inicial, 4 GiB máximo, buffer alvo 20%,
prioridade padrão**, sem overcommit planejado; todos os tamanhos múltiplos de
128 MiB. Não diminuir mínimo abaixo de 3 GiB antes de medir. Teto de 4 GiB não
inclui todo overhead do host. VHDX permanece até 48 GiB e reserva de disco 120 GiB.
VM desligada fora de sessões deliberadas de laboratório; sem auto-start ou agente
em background. Perfil fixo de 4 GiB continua referência para comparação reproduzível.

#### Estimativa de memória, sem medições gVisor nesta máquina

Valores seguintes são **reservas de engenharia a validar**, não benchmarks ou
garantias do upstream. Evitar build Go/race, instalação de pacotes e ensaio de
isolamento concorrentes dentro da VM pequena; validar esses custos separadamente.

| Componente dentro do guest | Reserva inicial de planejamento |
| --- | --- |
| Kernel, systemd e serviços mínimos, contabilidade cgroups incluída | 768–1.280 MiB |
| Supervisor/watchdog fora do cgroup limitado da fixture | 128–256 MiB |
| Sentry, gofers, stubs, auxiliares e fixture juntos | Até 512 MiB, teto agregado proposto já existente; overhead runtime também consome esse teto |
| Cache recuperável e margem de boot/hotplug | 512–768 MiB |

Soma de planejamento: aproximadamente 1,88–2,75 GiB; há incerteza e picos de boot.
Não somar workspace tmpfs de 64 MiB novamente se já contabilizado nos 512 MiB;
garantir que sua cobrança realmente pertença ao subtree correto. O supervisor não
compete pelo mesmo teto da fixture. Cgroups não têm custo fixo universal: metadados,
page tables e kernel memory precisam entrar na medição agregada. Nenhuma faixa
autoriza aumentar memory.max para caber runsc.

O modelo gVisor contabiliza memória de aplicação via memfd/shmem; medir
memory.current/peak e memory.stat completos, não apenas anon/RSS nem relato interno
da fixture. Não há valor universal de overhead Sentry/gofer aplicável ao pin ainda
não instalado. [Modelo de recursos gVisor](https://gvisor.dev/docs/architecture_guide/resources/).

Quotas propostas de segurança permanecem intactas: uma fixture por vez, 512 MiB
agregados, swap negado no subtree, 0,5 CPU, processos/disco/tempo do perfil-base.
2 vCPUs são capacidade do guest, não limite da fixture. Se runtime confiável não
couber no teto, registrar falha e revisar desenho antes de executar fixtures;
não aumentar quotas nem reduzir garantias silenciosamente.

#### Reserva do Windows e critérios de admissão/interrupção

Amostra somente leitura desta revisão: 15,85 GiB visíveis e **6,69 GiB disponíveis**.
Pagefile: 18.432 MiB alocados, 2.044 MiB em uso, pico 2.049 MiB. Isso não mede taxa
de paginação nem prova thrashing. Consulta Win32_PerfFormattedData_PerfOS_Memory
falhou com classe inválida; métricas de commit/paginação não obtidas. Não reparar
contadores como efeito colateral. Chrome, Discord, ChatGPT e Codex ficam abertos.

Reservar pelo menos 4 GiB disponíveis para o Windows e crescimento normal dos apps,
mais margem **provisória** de 0,5 GiB de overhead host Hyper-V, a medir. Para boot e
smoke inerte de 3 GiB, exigir inicialmente 7,5 GiB disponíveis; para autorizar ensaio
que possa chegar a 4 GiB, exigir **8,5 GiB**, ou margem maior se a calibração indicar.
Isso explicita overhead ausente no cálculo conservador de 8 GiB, não reduz a reserva
anterior. Não usar 7,5 GiB para admitir ensaios de quota a 4 GiB. Scripts e manifesto
anteriores não foram alterados e continuam bloqueados; esses números ainda exigem
revisão antes de codificar admissão.

Com 6,69 GiB livres, contas aproximadas após boot: 4 GiB fixos deixariam 2,19 GiB;
dinâmico inicial 3 GiB deixaria 3,19 GiB; inicial 2 GiB deixaria 4,19 GiB, mas no
pico de 4 GiB também cairia para 2,19 GiB. São subtrações estáticas com overhead
estimado, sem prever cache/ballooning ou crescimento dos aplicativos. Portanto
**nenhuma opção está comprovada apta hoje**, e não se promete uso normal sem impacto.

Política futura de monitoramento, ainda não implementada: amostrar host/guest a
cada segundo por observador fora do subtree da fixture. Não iniciar nova execução
se host cair abaixo de 4 GiB disponíveis. Se permanecer abaixo por 5 s, cancelar
ensaio cooperativamente e iniciar cleanup; abaixo de 2 GiB em qualquer amostra,
cancelar imediatamente. Esses limiares não são garantia contra esgotamento entre
amostras; manter reserva e autoridade externa são essenciais. Nunca encerrar apps.

Também interromper a campanha se houver OOM global no guest, falha de hot-add,
supervisor/watchdog sem resposta, memória guest disponível abaixo de 512 MiB por
5 s, ou pressão PSI full contínua por 5 s. OOM **esperado dentro** do cgroup da
fixture é resultado do teste, desde que supervisor e guest sobrevivam e façam cleanup.
Se commit host ultrapassar 85% por 10 s ou paginação de entrada exceder duas vezes
o p95 do baseline por 10 s junto de disco >80% ativo, suspender novos ensaios e
revisar. São critérios provisórios de operação; com métricas ausentes, não admitir
testes de pressão. Degradação perceptível pelo usuário interrompe a campanha.
Não alterar pagefile/swap global como compensação automática.

#### Plano posterior de medição — tudo NOT RUN

1. Depois de sanar ESU/patches, autenticação e autorização administrativa, medir
   baseline por 10 minutos com os quatro apps no uso habitual: RAM disponível,
   commit/limite, paginação e disco do host. Selecionar contadores realmente
   disponíveis ou Task Manager/PerfMon manualmente; não inventar números ausentes.
2. Em janela autorizada, iniciar somente a VM do laboratório com 3/3/4 GiB;
   medir boot e 10 minutos ociosa: MemoryAssigned/MemoryDemand/MemoryStatus no host,
   hv_balloon/hotplug, MemAvailable, PSI, swap e OOM no guest. Não assumir que
   páginas hot-added serão devolvidas; medir alocação real e margem para os apps.
3. Smoke confiável inerte, depois runtime/supervisor sem fixture; registrar pico
   de cada cgroup, guest total e overhead host. Só depois de revisão própria executar
   fixtures controladas C2, sequenciais. Sem candidatos, inferência ou Sessions.
4. Após autorização das provas, exercitar crescimento bounded e OOM no subtree,
   cancelamento/cleanup, sem pressão global intencional. Comparar com 4 GiB fixos
   em execuções separadas, com mesmo kernel/runtime/limites; no mínimo três ciclos
   por perfil. Separar mudanças de alocação de evidência de desempenho/isolamento.
5. Encerrar novas execuções, cancelar/aguardar run próprio, verificar cgroups vazios,
   mounts e journal reconciliados; solicitar shutdown limpo do guest e confirmar
   estado Off e recuperação da RAM no host. Não usar Saved como substituto de
   shutdown limpo. Timeout de shutdown é falha de lifecycle, sem force-off automático;
   diagnosticar e solicitar intervenção. Nada disso foi executado nesta tarefa.

A instalação pode precisar de janela distinta de RAM; se 3 GiB não bastar, parar
e planejar instalação com recursos suficientes, nunca confiar em hot-add durante
instalação. Não incluir patching/build pesado no orçamento das fixtures.

Pendências independentes da economia de RAM: **Windows/ESU e patches comprovados**;
**ISO, runtime completo, rootfs e dependências autenticados**; scripts efetivos de
provisionamento/descarte revisados e aprovação explícita. Inventário informado
remove uma incerteza administrativa anterior, não essas condições. Todos os 18
gates continuam unverified; RequireRealExecution segue ErrUnavailable.

## 3. Deployment reproduzível e autenticação

Valores abaixo são **candidatos de aquisição**, não artefatos baixados ou autenticados:

| Item | Pin candidato / condição |
| --- | --- |
| ISO | ubuntu-24.04.5-live-server-amd64.iso; SHA256 anunciado `97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8` |
| gVisor | release-20261019.0; commit `760b80785dc53311d4c87bd2de167525a99709c1` |
| Bundle amd64 | gvisor-x86_64.tar.bz2; digest anunciado pela API GitHub `1298e8ed9c3fee900f00d2ea96ec2d79d80ed93f2175812f29b7e7da718041a3` |
| Plataforma | systrap; sem seleção automática ou fallback |
| Go / Python futuro | 1.27.1 / 3.12.10, já usados na validação do projeto; revisar correções de segurança antes da admissão e substituir por novo pin aprovado se necessário |
| Harness² | 1cfe254e8d25b15f37048c3730134686abe60335, sem launchers/extras pagos |
| Kernel/systemd/bibliotecas/rootfs/políticas | PENDENTE: versão completa, arquitetura, closure de pacotes, snapshot de repositório assinado, hashes e assinatura do deployment |

Fontes dos pins: [checksums Ubuntu](https://releases.ubuntu.com/24.04/SHA256SUMS),
[release gVisor](https://github.com/google/gvisor/releases/tag/release-20261019.0).
O tag anotado gVisor consultado pela API GitHub estava `verified=false`,
`reason=unsigned`. SHA de asset e HTTPS não equivalem a assinatura do produtor.

Procedimento de confiança a concluir **antes de instalar/executar runsc**:

1. Administrador valida a chave Ubuntu por fingerprint em canal confiável externo
   ao guest; verifica SHA256SUMS.gpg com essa chave e só então o ISO. A assinatura
   não é substituída pelo hash anunciado neste documento.
   [Verificação oficial Ubuntu](https://documentation.ubuntu.com/security/software-integrity/image-verification/).
2. Para gVisor, preferir aquisição de pacote/metadata de repositório APT assinado,
   com chave/fingerprint aprovados externamente e versão exata congelada. Não
   assumir que esse pacote corresponde ao tarball candidato acima. Selecionar
   **uma** origem autenticada e registrar todos os bytes obtidos. A documentação
   não promete pin de point release por APT; canal por data pode mudar.
3. Checksum e tag unsigned não fornecem autenticação independente do produtor.
   Uma assinatura local ou aprovação do operador não substitui essa autenticação.
   Sem cadeia de origem verificável, interromper aquisição e instalação, mesmo
   que o operador assine o bundle. Assinatura de aprovação é apenas adicional.
4. Incluir runsc **e todo gvisor-bin/sidecars** da mesma release. O layout atual
   deixou de ser um único executável. Não permitir downloads implícitos em startup.
   O pacote APT pode configurar Docker automaticamente; não o instalar às cegas.
   O caminho recomendado usa OCI direto e não precisa de Docker no guest.
5. Gerar inventário exato de ISO, kernel, systemd, libs, runtime, rootfs, helper,
   perfil OCI/seccomp e supervisor, com tamanhos/hashes/versões, builder e logs.
   Assinar o manifesto com chave mantida fora do guest e da futura sandbox.
   Fixar a chave pública por canal separado; não aceitar chave fornecida no bundle.
6. Congelar uma imagem base aprovada e seu manifesto. Qualquer campo PENDENTE,
   pacote sem versão, tag latest ou assinatura inválida impede o provisionamento
   para testes. Não há lock completo de deployment pronto nesta entrega.
   Atualização de segurança exige novo conjunto revisado, nunca troca silenciosa.

## 4. Sequência operacional posterior, sujeita a autorização

1. Aprovar criação da VM e checar capacidade livre, administração Hyper-V e
   manutenção do host. Nenhum Enable-WindowsOptionalFeature, wsl --shutdown,
   import de distro, mudança de BIOS ou parada de Docker foi executado aqui.
2. Criar VM limpa pela console, disco exclusivo, sem enhanced-session redirection,
   clipboard, drives, pastas compartilhadas, sockets do host ou agent forwarding.
   Instalar ISO autenticado. Permitir rede de manutenção apenas para aquisição
   controlada dos artefatos assinados; depois desligar/remover NIC para ensaios.
3. Resolver e congelar pacotes de segurança da distro em snapshot assinado,
   registrar kernel/systemd exatos e produzir imagem base. Não criar chaves de
   usuário/serviço reais nela. Transportar bundles aprovados por mídia read-only;
   não montar o diretório do projeto Windows dentro de uma fixture.
4. Provisionar no guest identidades separadas, diretório de runtime root-owned
   não gravável pelo workload e armazenamento de estado privado do supervisor.
   Separar builder, supervisor, watchdog e workload. Administrar runtime por OCI
   direto; não executar `runsc install`/reload do daemon Windows/Docker Desktop.
5. Administrador cria a subtree cgroup v2/slice dedicada; somente a autoridade de
   controle pode configurar/migrar membros. Fixar perfil OCI mínimo e verificar
   capacidades necessárias ao runtime; não concedê-las ao workload. Rootless só
   é opção se delegação e provas equivalentes forem demonstradas, não por nome.
6. Antes de qualquer fixture, um smoke **inerte e confiável**, autorizado à parte,
   deve provar inicialização do layout completo, política carregada, identidade,
   cgroup, mounts e teardown. runsc --version sozinho não satisfaz essa etapa.
7. Só então revisar e implementar fixtures controladas em C2. Nenhum desses passos
   instala infraestrutura ou adiciona um launcher ao PR #28 nesta tarefa.

Comandos de verificação futuros, somente no guest administrado, após validação
dos artefatos: `uname -r`, `systemd --version`, `stat -fc %T /sys/fs/cgroup`,
`cat /sys/fs/cgroup/cgroup.controllers`, `systemctl show` da slice/service e
`runsc --version`. Ler configuração/procfs é disponibilidade; criar uma subtree
descartável, aplicar controles e testar seus efeitos já é enforcement, ainda NOT RUN.

## 5. Perfil deny-by-default a revisar, não configuração implementada

- Raiz mínima e read-only; mount namespace privado, sem propagação; somente
  runtime/dependências e workspace temporário explicitamente necessários.
  Sem HOME, /mnt/c, /mnt/d, socket Docker, SSH agent, credenciais de serviço ou
  workspace/transcript DAIMON. FDs herdados fechados salvo canais limitados.
- UID/GID convidado não zero, mapeamento dedicado, capabilities vazias,
  no-new-privileges e dispositivos mínimos. Sem novos namespaces/mounts/cgroups
  controláveis pelo workload. Não habilitar overrides OCI fornecidos por modelos.
- Rede externa ausente, namespace separado e nenhuma interface/rota de saída;
  firewall na autoridade Linux como defesa adicional. **network=none mantém
  loopback gVisor**, portanto não satisfaz a exigência de negar localhost.
  [Comportamento documentado](https://gvisor.dev/docs/user_guide/networking/).
- Candidato para negar também loopback: filtro OCI de syscalls de aplicação,
  default-deny, bloqueando criação/uso de sockets e caminhos alternativos, com
  `--oci-seccomp=true` explícito. O flag existe no código pinado e é false por
  padrão; isso **não prova** que o filtro será aplicado corretamente. Validar
  todos os ABIs, FDs herdados, exec/descendentes e atalhos como io_uring; perfil
  inválido deve impedir startup. Nunca confundir filtro do Sentry com filtro do
  aplicativo. Se a release não permitir negar loopback com prova, parar C2.
  [Flag no código pinado](https://github.com/google/gvisor/blob/release-20261019.0/runsc/config/flags.go).

Orçamento inicial proposto, não aplicado: concorrência 1; 0,5 CPU de taxa;
512 MiB agregados; 64 tarefas/threads host; alvo de 16 tarefas convidadas;
workspace host-backed tmpfs de 64 MiB e 512 inodes; 5 s incluindo startup;
2 s de orçamento de encerramento; stdout/stderr limitados a 4 KiB. Calibrar com
smoke confiável e revisar política se o runtime não couber, sem aumentar limites
silenciosamente para fazer testes passarem.

cpu.max limita taxa, não total vitalício. Uma quota cumulativa precisa medição
externa de cpu.stat e margem de overshoot declarada. memory.max, swap negado e
controle de OOM devem abranger Sentry, gofers e auxiliares no mesmo subtree;
o watchdog fica fora dela. Limitar cada escrita/log e impedir overlay/temporários
não contabilizados. Quota de disco inclui arquivos abertos após unlink, mmap,
metadados/inodes e diretórios; não é RLIMIT_FSIZE por arquivo.

Em gVisor os processos convidados não são PIDs host equivalentes. pids.max
protege tarefas host, **não comprova sozinho limite de processos virtuais**.
Avaliar limite convidado separado, por exemplo RLIMIT_NPROC hard/soft para UID
não zero, com criação de userns/troca de UID negadas, somente se efetivamente
suportado e testado. Se não houver barreira verificável, gate de processos fica
aberto. [Modelo de recursos gVisor](https://gvisor.dev/docs/architecture_guide/resources/).

## 6. Ownership, cancelamento e recuperação

Planejar intenção durável com ID aleatório e ownership exato antes de criar
recursos. Inicializar o launcher **já dentro** do cgroup/scope por autoridade
systemd/clone apropriada; não executar cmd.Start e conter depois. Conferir todo
Sentry/gofer/auxiliar no subtree antes de liberar a fixture. Proibir migração
de cgroup e fuga via setsid; process group/PID numérico não constituem ownership.

Watchdog externo deve sobreviver ao crash do supervisor. Deadline/cancelamento
ou perda de heartbeat encerram o subtree, aguardam processos próprios e estado
terminal, removem somente mounts/workspace/refs exatos. Considerar
KillMode=control-group e cgroup.kill, sem presumir que matar signifique reap.
Exigir cgroup.events populated=0, handles/PIDs de ownership finalizados e mount
removido; falha fica unresolved e bloqueia novos runs. Reconciliação após reboot
consulta só journal autenticado/protegido, nunca varredura-delete por prefixo.
Snapshot da VM não é substituto de cleanup correto.
[Semântica systemd](https://www.freedesktop.org/software/systemd/man/latest/systemd.kill.html),
[cgroup v2](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html).

## 7. Provas negativas necessárias — todas NOT RUN nesta tarefa

| Controle | Disponibilidade a verificar | Enforcement e critério futuro de aprovação |
| --- | --- | --- |
| Deployment | Chaves aprovadas, manifesto completo, layout/versão | Alterar binário/sidecar/política/assinatura deve impedir startup antes da fixture |
| Filesystem | Namespaces e mounts efetivos, raiz read-only | Sentinela fora da raiz inacessível; escrita externa recusada e sentinelas inalteradas; traversal, symlink, hardlink, mount aliases e /proc/*/root não escapam |
| Rede | Namespace, filtro efetivo e configuração runtime | IPv4/IPv6, localhost próprio e host, UDP/TCP/DNS e metadata sintética recusados; endpoint positivo em namespace de teste confirma disponibilidade; observar errno e tráfego externamente, não apenas timeout |
| Credenciais | UID mapping, env/FD/mount allowlists | Marcadores sintéticos de env/arquivo/Unix socket fora da sandbox nunca aparecem na evidência; sem secrets reais em qualquer teste |
| CPU/memória | Controles efetivos para todos os auxiliares | Carga limitada por fixture atinge throttling/OOM esperado, com counters externos e sem afetar watchdog/host; taxa não é orçamento cumulativo |
| Processos | pids host e limite virtual separado | Descendentes, threads, exec e setsid não escapam; ultrapassar cada teto é recusado; contagem observada do convidado separada da host |
| Disco | Workspace e logs com teto/inodes | Exaustão controlada termina com ENOSPC/limite comprovado, sem expansão do host; vários arquivos, sparse/unlink aberto/mmap e logs incluídos |
| Lifecycle | Ownership/supervisor/watchdog/journal | Cancelar em startup/exec/cleanup, matar supervisor e reiniciar guest não deixa processos/mounts/refs ativos; falha pendente impede nova execução |

Os endpoints e sentinelas devem existir só no laboratório Linux, sem NIC externa.
Usar controle positivo confiável fora da sandbox para distinguir recusa de alvo
inexistente; nunca tocar metadata real, DNS público ou credenciais do usuário.
Não basta aceitar flags, ler um limite ou confiar no relato da própria fixture.

## 8. Critérios objetivos para retomar C2

1. Autorização explícita para provisionar VM/guest e alterações administrativas
   especificadas; memória/disco livres e suporte/patches do host confirmados.
2. Manifesto de deployment completo, assinado por autoridade aceita fora do guest;
   origem de todos os artefatos justificada, sem pins PENDENTE/unsigned aceitos
   implicitamente; imagem base identificada e reprodução documentada.
3. Linux/cgroup v2 administráveis, criação controlada de namespaces, supervisor
   externo e instalação gVisor completa com systrap demonstrados em smoke inerte.
4. Revisão do perfil de loopback e quota de processos virtuais; plano de provas
   fechado, sentinelas/endpoints sintéticos, budgets e critérios de cleanup fixados.
5. Nova revisão C2 autoriza somente fixtures controladas. Depois das provas,
   registrar ambiente, tolerâncias, PASS/FAIL/SKIP/NOT RUN e condições de validade.

Disponibilidade dessas condições permite **retomar desenvolvimento e testes da
C2**, não habilita candidatos ou fecha gates automaticamente. Os 18 gates
permanecem unverified; RequireRealExecution permanece ErrUnavailable. C3 e
autoridade futura de execução continuam fora desta entrega.

Nesta tarefa: PASS para auditoria/checagem documental; NOT RUN para instalação,
provisionamento, smoke e provas de enforcement; nenhum teste adversarial. Só
documentação foi alterada no #28; não houve motivo para repetir suítes de código
inalterado ou apresentar resultados históricos como novos. Riscos residuais:
kernel/hipervisor/administrador confiáveis, comprometimento de supply chain,
canais laterais, bugs de runtime e compatibilidade do perfil ainda não testada.
