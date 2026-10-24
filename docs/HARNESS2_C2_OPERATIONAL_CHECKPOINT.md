# C2 — aquisição executada e preparação operacional

## Única nova tentativa 2 GiB: BLOCKED — 2026-10-24 18:06:55Z

Perfil `bounded-max2` e mínimo **6 GiB** mantidos, sem novo perfil, alteração de
VM, encerramento de aplicativos, alteração de serviços ou limpeza de caches.
Uma única coleta silenciosa de **60 amostras / 181,93 s**, incluindo dois helpers
residentes: mínimo disponível **5,84546 GiB**, CPU p95 **14%**, commit livre mínimo
**14,56987 GiB**, balancer mínimo **5,21875 GiB**, crescimento da ocupação do
pagefile **0 MiB**. Apenas o orçamento físico falhou: déficit **0,15454 GiB
(158,25 MiB; arredondar para pelo menos 159 MiB adicionais)**. O último ponto de
6,073 GiB não substitui estabilidade: não houve GO. Tentativa encerrada, sem repetir
diagnósticos. Não foi solicitada autorização de boot com orçamento reprovado.

Antes da coleta, revisão do executor identificou corrida entre fim do watchdog
e start ainda pendente no controlador. Corrigida com lock de transição protegido,
veto persistente antes do stop e checagem do processo watchdog dentro do lock e
durante monitoramento. **Oito testes reais de processos** passaram (cinco de lease
e três de lock/veto/saída), além de **99 testes offline**. O desligamento de VM em
execução continua **NOT RUN**; a aptidão do código para o primeiro ensaio não é
prova de poweroff nem garantia contra Hyper-V não responsivo. Ownership protegido,
revalidação de ISO e restauração 3/3/4 permanecem obrigatórios após futura aprovação.

A menor intervenção necessária é liberar pontualmente pelo menos **159 MiB
adicionais sustentados** sem fechar aplicativos; uma margem prática de 256 MiB
evita mirar o limiar exato. Isso pode exigir ação manual sobre uma atividade/aba
não utilizada; não é motivo para alterar serviços, reserva ou caches do sistema.
Nenhuma intervenção dessas foi executada. Todos os 18 gates seguem `unverified`,
PR draft e sem merge; ESU/gVisor continuam pendências separadas.

## Calibração leve implementada: BLOCKED por 46 MiB — 2026-10-24

HEAD anterior `8aee9cd`, seis checks aprovados. Nenhuma mutação de VM nesta etapa.
Preparados perfis temporários dinâmicos **2/2/2 GiB** e **2,5/2,5/2,5 GiB**,
exclusivamente para boot confiável da ISO por 90 s; não autorizam instalação.
Perfil original 3/3/4 GiB e quotas dos experimentos permanecem intactos.

Nova coleta silenciosa concluída às **17:53:29Z**, 60 amostras / 182 s, com dois
helpers de sensores residentes: mínimo físico **5,95535 GiB**, CPU p95 **15%**,
commit livre mínimo **14,68087 GiB**, balancer mínimo **5,26367 GiB**, crescimento
da ocupação do pagefile **0 MiB**. Taxa de I/O de paginação continua indisponível.
Helpers: soma bruta de working sets 347–360 MiB (inclui páginas compartilhadas;
não representa custo físico líquido). Disponibilidade nativa já inclui sua presença.

Budget leve: guest máximo + **3,5 GiB de folga física adicional para o host** +
**0,5 GiB de overhead incremental provisório**. Requer **6 GiB** para guest 2 e
**6,5 GiB** para guest 2,5. O menor perfil falhou por **0,04465 GiB (~46 MiB)**.
Não arredondar o déficit nem reduzir a reserva para transformar esta coleta em GO.
Reserva reduzida é restrita a este probe mais curto, com desligamento imediato
abaixo de 3,5 GiB e antecipado abaixo de 3,75 GiB por 2 s. Não é mínimo universal
do Windows nem perfil homologado. Overhead real Hyper-V ainda exige calibração.

**66 testes lifecycle + 33 testes de política offline passaram.** Adicionalmente,
**cinco testes reais de watchdog com processos Windows** passaram: heartbeat
vencido, prazo, JSON inválido, pressão nativa com limiar deliberadamente alto e
conclusão normal. Mesma função usada no watchdog da VM; arquivos, relógio,
sensores e término de processo são reais. O teste real corrigiu conversão de
timestamp no JSON; valores 2,5 são validados sem dependência de separador decimal.
Não houve desligamento real de VM. Evidência anterior WhatIf permanece válida;
prova de poweroff/restauração de VM em execução depende de futura autorização.

Executor restaura mínimo/startup/máximo 3/3/4, relê e só reconcilia journal após
validar o perfil original completo. Ownership e controle privado são obrigatórios;
ACL desconhecida nunca é reparada automaticamente.

Próxima condição concreta: janela sustentada >= **6 GiB** livres, demais critérios
e nova checagem após armar watchdog. Somente então solicitar aprovação única do
probe de 90 s, ajustes temporários, hard poweroff e restauração. Não houve pedido
de aprovação com orçamento reprovado. Todos os 18 gates continuam `unverified`;
ESU/patches e autenticação independente gVisor permanecem separados.

## Primeiro boot preparado: BLOCKED por RAM — 2026-10-24

Inspeção elevada somente leitura concluída às 14:36:49Z: VMId e DiskIdentifier
conferem com o journal protegido; VM Off, configuração completa, ISO autenticada
novamente íntegra, Secure Boot correto, DVD primeiro e zero NICs. Controle privado
validado. Interface de desligamento passou em `WhatIf`; desligamento real não executado.

Coleta silenciosa posterior, 15:01–15:04Z, 60 amostras em aproximadamente 182 s:
RAM disponível mínima **6,569 GiB**, CPU p95 **43%**, commit livre mínimo
**17,676 GiB**, balancer mínimo **5,881 GiB**, crescimento da ocupação do pagefile
**0 MiB**. Taxa de I/O de paginação indisponível. A coleta anterior coincidiu com
preparação/testes locais e não foi usada para concluir pressão persistente de CPU.

**Único bloqueio de admissão observado na coleta silenciosa: orçamento físico.**
Perfil atual máximo 4 GiB requer 8,5 GiB disponíveis; proposta de teto temporário
3 GiB requer 7,5 GiB. Ambos mantêm reserva do host de 4 GiB e margem provisória
de 0,5 GiB. Faltam pelo menos **0,931 GiB** até o perfil leve nesta janela.
Não reduzir a reserva apenas para obter aprovação. Nenhum aplicativo foi encerrado.

Executor de probe limitado, watchdog separado, monitoramento e reconciliação
preparados; **64 testes de lifecycle + 29 testes de boot offline** passaram.
Nenhum executor de boot foi armado ou executado. Nenhuma configuração da VM mudou.
Ver [procedimento e limites](HARNESS2_C2_FIRST_BOOT.md).

Próxima ação: obter janela sustentada com pelo menos 7,5 GiB disponíveis e demais
critérios aprovados, então solicitar autorização específica para teto temporário,
boot de 180 s, desligamento e restauração. Não é necessário mudar serviços globais
ou Windows para resolver este bloqueio medido. ESU/patches e autenticação independente
do gVisor seguem pendentes separados; todos os 18 gates permanecem `unverified`.

## Marco concluído: VM Off configurada e validada — 2026-10-24

**SUCCESS**, observação elevada em 2026-10-24T13:46:35Z, código `da13157` com
63 testes offline e seis checks CI aprovados antes da operação. Journal protegido
persistido em **configured**, relido e configuração real validada novamente.
Não executar nova criação nem tratar recibos do operador como autoridade.

| Item observado | Resultado real |
| --- | --- |
| VM | Generation 2, Off; VMId exato preservado no journal privado |
| CPU | 2 vCPUs; nested virtualization desabilitada |
| Memória | Dinâmica: 3 GiB mínimo, 3 GiB startup, 4 GiB máximo |
| Disco | VHDX Dynamic, máximo 48 GiB; mesmo DiskIdentifier reconciliado |
| ISO | Cópia privada, hash fixado novamente validado; DVD primeiro no boot |
| Rede | Zero adaptadores; nenhum switch criado/modificado |
| Firmware | Secure Boot On, MicrosoftUEFICertificateAuthority |
| Integração/checkpoints | Zero serviços de integração habilitados; zero checkpoints |
| Autostart | Nothing |
| Controle | Raiz, journal e lock validados: somente Administradores/SYSTEM |
| RAM host atual | **7,11 GiB** disponíveis; boot bloqueado pelo mínimo local de 8 GiB |

O domínio de controle não foi afrouxado. A leitura elevada mostrou template
Hyper-V na raiz com CreatorOwner, grupo Virtual Machines e uma capability Windows;
o reparo automático conservador corretamente o recusou. Revisão explícita do
operador conferiu template **exato**, owner/proteção, journal/IDs, VM Off,
configuração completa, mídias e inexistência de outra VM referenciando a raiz.
Mídias foram mantidas abertas contra escrita/substituição durante sua conferência.
Restaurada somente a ACL privada original da raiz própria, com registro protegido.
Depois disso, apenas validação/readback e persistência configured, sem reaplicar
setters Hyper-V. Nenhuma permissão foi alterada fora da raiz recém-criada da C2.

**Limitação conhecida:** concessões amplas do template Hyper-V exigem revisão
explícita, não são aceitas/limpas automaticamente pelo executor. Não declarar
criação fresca ou descarte real completamente automáticos/homologados. Antes e
depois de futuro boot, conferir novamente o domínio de controle e a estabilidade
dessas permissões; um boot bem-sucedido não homologa isolamento.

- [x] VM limpa criada, configuração e ownership reais validados enquanto Off.
- [x] ISO privada e journal/control ACL conferidos; evidências privadas preservadas.
- [ ] Boot: disponibilizar **pelo menos 8 GiB** sem fechar apps automaticamente,
  medir imediatamente antes e obter autorização específica para esta VM/escopo.
- [ ] Instalação Ubuntu: autorização e plano próprios; **NOT RUN**.
- [ ] Calibração em execução, gVisor e provas: requisitos/autorização correspondentes.
- [ ] Descarte real: **NOT RUN**, exige autorização; não apagar o laboratório.

Windows 10 Pro preservado; nenhuma atualização/ativação/reinicialização, instalação
ou boot realizados. Nenhum merge. **C2 não concluída; 18 gates unverified; C3 e
execução de candidatos continuam bloqueados.** Seções seguintes são histórico.

## Domínio de controle preservado — 2026-10-24

Revalidação com comparação semântica de mídia recusou `Unexpected journal/root
access rules.` Journal protegido continua legível/validado, IDs e estado
vm-recorded inalterados. VM Off, 2 vCPUs, dinâmica 3/3/4 GiB e zero NICs novamente
observados; não houve boot. Papel exato com divergência (raiz/lock) ainda deve ser
confirmado; não presumir que qualquer trustee extra seja legítimo.

Executor ganhou recuperação **restritiva**, só no Create pré-boot, com VM Off e
VMId/disco/localização/referências previamente validados. Aceita retirar no máximo
uma ACE Allow explícita ao SID **exato da própria VM**, se o restante da DACL
protegida/owner já corresponder integralmente a Administradores/SYSTEM. Trustees
desconhecidos, grupos amplos, denies, owner alterado, herança não protegida e
journal enfraquecido bloqueiam a recuperação. Nunca concede novos direitos.

Valida todos os candidatos antes de mudar ACLs, usa lock exclusivo, confere de
novo sob lock e registra a revogação em arquivo protegido. Reaplica essa separação
após configurar a VM. Inspect/Start/Discard não fazem reparos administrativos
silenciosos; running/start-pending não são elegíveis. **63 testes offline PASS**.
Próxima retomada deve comprovar permissões reais antes de marcar configured.

## Configuração aplicada; comparação de ACL corrigida — 2026-10-24

Retomada elevada da mesma VM concluiu os setters: 2 vCPUs, nested=false, memória
dinâmica 3/3/4 GiB, zero NICs, disco e DVD próprios, ordem de boot DVD primeiro e
template MicrosoftUEFICertificateAuthority. VM continua Off, sem boot/instalação.
Antes de marcar configured, validador recusou `Media access rules changed.`

Leitura elevada capturou descritores reais/esperados: proprietário Administradores,
DACL protegida e mesmas três ACEs (SYSTEM/Administradores FullControl; SID exato
da VM Read para ISO/FullControl para VHDX). Única diferença no texto de Access:
Windows acrescentou controle AutoInherited (`D:PAI` versus `D:P`). Nenhuma ACE
adicional ou herdada foi observada. Descritores/IDs privados não publicados.

Comparação corrigida para owner/proteção e conjunto completo de ACEs: SID,
máscara, allow/deny, flags de herança/propagação e IsInherited. Aceita somente a
diferença de metadado AI com regras efetivas idênticas; herança desprotegida,
direitos extra, SID trocado, deny extra e owner divergente continuam recusados.
**55 testes offline PASS**, incluindo a regressão Windows e sete recusas.

Journal segue vm-recorded porque a validação final não foi concluída. Próxima
ação é revalidar a mesma VM após CI/UAC e persistir configured, sem recriar mídia
ou VM. Não declarar homologação/boot liberados: os 18 gates continuam unverified.

## VM criada Off, configuração pendente — 2026-10-24

Consulta elevada do disco concluída: GUID real é string em maiúsculas, não vazio;
VHDX Dynamic 48 GiB, 4 MiB físicos, parent vazio, Attached=false, owner
Administradores, sem referências em VMs/checkpoints, sem VM ou diretório de
configuração correspondente naquele momento. Data de criação coincide com a
tentativa que falhou. Journal protegido confirmou disk-pending/IDs ausentes.

Reconciliação explícita elevada: verificou novamente UUID/raiz protegida,
GUID/time/tipo/tamanho exatos, ACL, ausência de referências/VM/config e hash da
ISO privada; salvou registro protegido de revisão e journal disk-ready. Não
alterou o ID/bytes do VHDX nem recriou o disco. Retomada na autorização original
criou a VM e persistiu seu VMId, mantendo-a **Off**, sem boot/instalação.

A configuração recusou `VM ownership/location mismatch`: Hyper-V materializou
ConfigurationLocation em `config-container/vm-name`, não no container passado
a New-VM -Path. Caminho real e VMId foram lidos no tratamento elevado da falha.
Validador corrigido para esse filho **exato**, sem aceitar qualquer prefixo;
SmartPaging/Snapshot permanecem no diretório próprio. **47 testes offline PASS**.
Estado confirmado no journal: vm-recorded, IDs de VM e disco presentes. IDs e
caminhos privados não publicados; recibos locais não substituem o journal.

Próxima ação: retomar configuração da mesma VM por VMId após CI e UAC legítimo.
Não executar New-VM novamente, não apagar recursos, não bootar. Nova VM não
está ainda declarada conforme à configuração 2 vCPU/3–4 GiB/zero NICs até a
validação final; não confundir criação com conclusão da configuração.

## Execução autorizada retomada — 2026-10-24, VHDX pendente

O proprietário confirmou reapresentar UAC e continuar somente a criação Off.
Código `a5bb6f5` reconferido por SHA256, checkout limpo e seis checks SUCCESS antes
do lançamento. A elevação iniciou a mesma operação, sem uma segunda raiz/VM.

Resultado externo do executor: REFUSED_OR_UNRESOLVED, `Invalid resource ID.`
em 2026-10-24T10:12:20Z. Pela ordem de chamadas, a cópia privada da ISO e suas
verificações chegaram ao ponto de criação do VHDX; o registro do identificador
falhou antes de iniciar New-VM. Não houve boot ou instalação. Estado durável
esperado: disk-pending, ainda a confirmar pela leitura elevada; não tratar esta
inferência ou o recibo do operador como autoridade de ownership.

Foi preparada consulta elevada somente leitura ao journal e ao VHDX exatos,
incluindo formato bruto do ID, tipo/tamanho/parent/Attached, owner e referências
em VMs/checkpoints. O Windows reportou cancelamento do UAC dessa consulta; ela
não executou. Proprietário deve confirmar disponibilidade/intenção antes de
reapresentar esse diálogo. Não tentar novo Create nem descartar o disco pendente.

O adaptador agora normaliza GUIDs retornados como strings com chaves/maiúsculas
para a representação canônica do journal, tanto na captura quanto na comparação.
Isso não altera bytes/ID do VHDX nem aceita GUID vazio/malformado. **46 testes
offline PASS**. O formato efetivo do ID deste host ainda não foi observado;
esta correção de representação não resolve automaticamente a identidade perdida.

Próxima ação concreta: consulta elevada restrita, reconciliação explícita do ID
com evidências de ownership e só então retomada da mesma criação Off, dentro da
autorização já recebida. Nenhuma adoção por nome/prefixo ou reset do identificador.
ISO/VHDX pendentes preservados; VM não chegou à chamada de criação. Gates e C3
inalterados. Metadados/recibos privados ficam fora do Git.

## Marco atual — executor do laboratório limpo, 2026-10-24

Base esperada `e0c12d6c178fc1ff736803e12ea30f496a44478b` confirmada, checkout
inicialmente limpo. O proprietário autorizou preparar executor e separar criação
limpa da homologação hostil. [Runbook e revisão do executor](HARNESS2_C2_CLEAN_LAB.md).
Este marco substitui a trava genérica anterior **apenas para preparar/criar uma
VM limpa e offline após aprovação específica**; runtime Harness² continua fechado.

- [x] Classificar criação Off, boot de ISO sem instalação e homologação hostil
  separadamente. ESU não é requisito técnico documentado de Hyper-V, mas permanece
  pendência de segurança do host para homologação hostil.
- [x] Revalidar SHA256 integral da ISO existente: pin reproduzido. Sem novo download.
- [x] Confirmar sessão não administrativa e inventário Hyper-V indisponível.
  RAM 6,57 GiB e disco 171,04 GiB livres na amostra. Sem repetir scan/licenciamento.
- [x] Implementar executor Create/Start/Discard/Inspect, journal protegido,
  ownership por VMId/DiskIdentifier e retomada conservadora após falhas.
- [x] 44 testes offline PASS, incluindo gravação NTFS/Replace real e recusa de
  destino bloqueado; parsing/diff check PASS. Autoridade ACL simulada nesse teste.
- [x] Atualizar preflight com marcos CleanCreate/CleanBoot; default Hostile mantém
  os bloqueios anteriores. Presence-only não autentica ISO: executor confere pin.
- [x] Proprietário aprovou somente criação desligada com recursos/destino exatos
  e risco residual limitado. Primeiro UAC legítimo despachado; operação iniciou
  e recusou atualização do journal (backup path vazio no File.Replace), antes
  da cópia ISO/VHDX/VM. Corrigido marshalling de null; retomar a mesma operação
  com journal existente, sem apagar raiz ou adotar por nome.
- [ ] Criar a VM desligada, verificar configuração real e registrar evidências.
- [ ] Boot somente após 8 GiB disponíveis e aprovação própria; sem instalação.
- [ ] Instalação Ubuntu, gVisor, provas e descarte real: autorizações/requisitos
  correspondentes pendentes, nenhuma execução realizada neste marco.

Destino estava ausente na proposta; a tentativa aprovada criou metadata privada
antes da falha de journal. Retomada deve conferir essa mesma raiz/journal. Nada
criado ou alterado em Hyper-V, nenhum
switch tocado, app encerrado, patch instalado, reboot ou merge. As seções abaixo
são históricas; a declaração anterior de executor não implementado foi superada
somente pelo código limitado do laboratório limpo. 18 gates continuam unverified.

## Retomada operacional — 2026-10-24

Base local `2d8d7d7`, branch esperada e checkout inicialmente limpo. PR #28
reconfirmado OPEN/DRAFT; quatro checks SUCCESS nesse HEAD. Windows 10 Pro
preservado. Nenhuma instalação, ativação, reboot, VM, mudança de switch ou merge.

### Diagnóstico executado, somente leitura

- Windows permanece `19045.6466`. Consulta local de licenciamento retornou 24
  entradas ESU e zero Licensed; isso não exclui modalidades consumer e não
  comprova cobertura. ESU permanece **UNVERIFIED**.
- Busca online deliberada por `Microsoft.Update.Session`, critério
  `IsInstalled=0 and IsHidden=0 and Type='Software'`, retornou ResultCode=2
  (sucesso), uma oferta e somente KB2267602. Não foram chamados downloader ou
  installer. Isso supera a ausência anterior de scan e não reproduz as falhas
  históricas de transporte nessa tentativa; não prova elegibilidade ESU nem
  explica a ausência de oferta cumulativa do OS. Não há fundamento novo para
  reset de Windows Update, DNS/proxy ou reparo administrativo.
- RAM disponível: 6,89 GiB na primeira amostra e 7,09 GiB no preflight; ambas
  abaixo dos 8 GiB exigidos. Disco de trabalho: 171,04 GiB livres. Nenhum app
  encerrado e nenhum limite reduzido para obter admissão.
- Sessão não elevada; Get-VM/Get-VMSwitch indisponíveis. Serviços vmms,
  vmcompute, hns, wuauserv, UsoSvc e BITS Running na amostra. Inventário elevado
  previamente informado pelo proprietário é histórico, não autoridade desta
  sessão. Indicadores CBS/WindowsUpdate de reboot: false.
- Computer Use tentou abrir Configurações; erro `launched app did not expose a
  targetable window`. Nova listagem não encontrou janela de Configurações.
  Proprietário respondeu nesta retomada: **ainda não verificou** ESU manualmente.
  Status/validade permanecem pendentes; nenhuma conta, chave ou ID solicitado.
  Não houve automação de autenticação nem contorno de UAC.

### Preparação verificável

O preflight agora emite timestamp UTC, build, contagens ESU quando disponíveis,
indicadores de reboot e estado explícito do inventário Hyper-V, sem identificadores
de licença. Consulta ESU tem timeout operacional de 15 segundos: no ensaio final
ficou UNAVAILABLE/null, preservando bloqueio e sem reaproveitar silenciosamente
os números da consulta anterior. Saída real: BLOCKED, zero mudanças
administrativas, ISO presente, enforcement NOT_RUN. O próprio preflight não
executa scan online; a busca acima foi uma operação separada.

A descrição do entry point reservado foi alinhada ao plano dinâmico 3/3/4 GiB.
Validação sintática PowerShell PASS; chamada explícita com `-Apply` recusada
antes de qualquer efeito. O executor continua **não implementado**, conforme
a trava de closure/autenticação/revisão; não há provisionador real homologado.

Releitura da [instalação oficial gVisor](https://gvisor.dev/docs/user_guide/install/)
confirma chave distribuída pelo site, rotação documentada em 2021-07-13 e necessidade
do conjunto de sidecars. Não fornece nova âncora independente de confiança para
o fingerprint já observado. Nenhum artefato adquirido foi executado.
O serviço treg retornou HTTP 401/invalid token; fontes oficiais foram consultadas
pela ferramenta web disponível, sem serviço pago nem tentativa de login.

[Microsoft consumer ESU](https://www.microsoft.com/en-us/windows/extended-security-updates)
e [orientação comercial](https://learn.microsoft.com/windows/whats-new/enable-extended-security-updates)
foram consultadas separadamente; não presumir modalidade pela edição Pro nem
confundir pacote preparatório com inscrição. Referência cumulativa
[KB5126657 / 19045.7807](https://support.microsoft.com/en-gb/servicing/os/windows-10/2026/10/kb5126657-windows-10-21h2-22h2-security-update)
reconfirmada; aplicabilidade/oferta neste host não comprovada. Nenhuma recomendação
de migração/reinstalação: preservar Windows 10 Pro é restrição permanente.

### Checklist persistente

- [x] Retomar checkout/PR e substituir hipótese de falha atual de transporte por scan real.
- [x] Medir RAM/disco/acesso atual sem alterar o host.
- [x] Tornar diagnóstico do preflight reproduzível e alinhar descrição de recursos.
- [ ] Proprietário informa status/validade ESU; UI inacessível pela ferramenta.
- [ ] Revisar oferta/atualização do Windows 10 após comprovar ESU; instalação,
  ativação, backup e reboot dependem de autorizações específicas aplicáveis.
- [ ] Obter pelo menos 8 GiB disponíveis sem encerrar apps sem autorização.
- [ ] Completar confiança independente gVisor e closure kernel/systemd/rootfs.
- [ ] Implementar/revisar executor e recuperação por ownership após pré-requisitos;
  obter autorização específica de recursos antes de criar qualquer VM.
- [ ] Laboratório, smoke tests inertes e provas externas: **NOT RUN**.

Os 18 gates continuam **unverified**, execução de candidatos desabilitada e C3
fora do escopo. Mudança de scripts somente diagnóstica/textual; nenhuma alteração
de runtime Go/Python/UI que justifique repetir suas suítes completas neste marco.

## Continuação independente da pendência ESU

O proprietário confirmou que ainda não verificou manualmente ESU; manter
**UNVERIFIED**, sem ativar/instalar/alterar Windows. Auditoria do HEAD e534bb4:
os quatro checks Go/Linux e UI, eventos push/PR, concluíram SUCCESS. Nova leitura
SHA256 integral da ISO e do .deb reproduziu exatamente os hashes do manifesto.
Não houve novo download grande nem execução de conteúdo.

Leitura ISO via libarchive (`tar -tf` e `tar -xOf`, somente stdout) confirmou
identidade de mídia Ubuntu-Server 24.04.5 amd64, datada 20260909. Foram calculados
hashes de três manifests internos, agora no manifesto de deployment. Declarações
da mídia: systemd 255.4-1ubuntu8.17; libc6 2.39-0ubuntu8.8; Python 3.12.3-1ubuntu0.16;
ubuntu-keyring 2023.11.28.1; camada GA 6.8.0-139.139; HWE 7.0.0-31.31~24.04.1.
Os arquivos usam registros de camadas com prefixos de alteração; não tratá-los
como inventário consolidado completo de um guest instalado. Autenticidade deriva
dos bytes da ISO assinada já conferida, não de assinatura independente desses
arquivos internos. Isso reduz incerteza sobre a mídia; não preenche kernel_packages,
systemd_packages ou rootfs_inventory de deployment nem comprova patches atuais.

Consulta oficial de attestation GitHub para o digest do .deb retornou HTTP 404;
não fornece prova de inexistência global de atestações, mas nenhuma evidência
utilizável foi obtida nessa rota. Advisories públicos: zero na consulta, sem
garantia de ausência de vulnerabilidades. Chave gVisor segue UNVERIFIED quanto
à autenticação independente: falta confirmação de identidade/rotação/revogação
ancorada em canal previamente confiável, ou attestation verificável do produtor.
Nenhuma solicitação externa foi enviada em nome do usuário.

PASS: reconferência de hashes, inventário interno autenticado e cinco testes
Python existentes do planejador. FAIL/indisponível de evidência: endpoint de
attestation retornou 404. NOT RUN: instalação, boot, gVisor executável, montagem,
fixtures e novas suítes de runtime inalterado. A pendência ESU bloqueia somente
operações que dependem da confiança do host; aquisição/inspeção autorizadas
continuaram. Os 18 gates permanecem unverified e nenhum recurso Hyper-V foi criado.

Checkpoint 2026-10-23, base ba583bbf399536abed792ff1ef2bfd83b2cc5c85.
Branch preservada e checkout inicialmente limpo; fetch sem sobrescrever arquivos.
**Windows 10 Pro será mantido. Não migrar, reinstalar ou substituir o OS.**
Provisionamento permanece BLOCKED; nenhum dos 18 gates foi alterado.

## Aquisições realmente concluídas

| Item | Evidência | Estado |
| --- | --- | --- |
| Ubuntu 24.04.5 Server amd64 | 4.080.486.400 bytes, SHA256 `97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8`; lista assinada validada por fingerprint Canonical | VERIFIED dos bytes adquiridos e assinatura correspondente; não autoriza instalação |
| runsc 20261019.0 amd64 .deb | 76.131.500 bytes, SHA256 `24e9740411c15f229151155e9b890388a18cfb4c9fce5c8d7ea6ed9f926e7a52`; corresponde a Packages/Release assinados | PASS de integridade; autenticidade independente de produtor continua UNVERIFIED |

Downloads HTTPS oficiais em staging não versionado sob .git, com curl --continue-at,
retry e prazo limitado. Arquivos mantidos com sufixo .part, mas tamanho/hash completos
conferidos; sufixo não é evidência de admissão. Nenhum arquivo adquirido foi incluído
no Git, montado, inicializado ou executado. Staging do repositório é local controlado,
não se promete proteção contra outros processos da conta. Revalidar bytes antes de
transportar/usar; nenhuma assinatura local foi criada como substituto de origem.

GnuPG existente reconfirmou assinatura Ubuntu (exit 0, VALIDSIG
843938DF228D22F7B3742BC0D94AA3F0EFE21092) e assinatura APT gVisor (exit 0,
VALIDSIG 6F1DF85E3A71C24918E727D56FC6D554E32BD943). Fingerprint Ubuntu confrontada
com documentação Canonical; chave gVisor só corroborada em site e GitHub oficiais,
sem âncora independente suficiente. Revogação/freshness deve ser revisada novamente
antes de instalação. O pin mais recente identificado continua release-20261019.0;
consulta pública de advisories anterior não mostrou entradas, sem garantia de ausência
de vulnerabilidades ou embargo.

## Inspeção real do pacote, sem execução

Parser Python stdlib leu ar/tar em memória, sem extrair arquivos para execução.
control não declara Depends. Nove arquivos de dados inventariados com tamanho,
hash, ELF e headers; oito executáveis e etc/containerd/runsc.toml registrados no
manifesto. Nos oito ELF, não foram encontrados PT_INTERP ou PT_DYNAMIC: nenhuma
dependência de loader/biblioteca dinâmica observada nesses headers. Isso não fecha
dependências de kernel, rootfs, configuração, supervisor ou funcionalidades opcionais.

control.tar contém control, postinst, conffiles, changelog. **postinst executaria
runsc install --clobber=false e poderia recarregar Docker se sua configuração
existisse. Não foi executado.** Instalação futura não deve configurar Docker
implicitamente. Modos observados 0755 nos ELF e 0644 no TOML; sem symlinks nos
arquivos de dados inventariados. Layout completo no manifesto não significa
deployment autenticado. Compatibilidade systrap real continua NOT RUN.

## Windows: novo bloqueador demonstrado, sem inventar causa única

Build ainda 19045.6466/KB5071959; ESU: zero entradas Licensed na consulta por
categoria, cobertura UNVERIFIED. Serviços wuauserv/UsoSvc Running, BITS Manual/Stopped.
Histórico de eventos WindowsUpdateClient/Operational mostra cinco falhas de busca:
0x8024402c em 19/10 e 08/08; 0x80244022 em 20/07 e 23/07; 0x8024402f em 02/09.
Datas de 2026. Não publicar URLs internos, contas ou XML bruto.

0x8024402c identifica falha de resolução do servidor/proxy, e 0x80244022 equivale
a HTTP 503. [Microsoft: códigos](https://learn.microsoft.com/en-us/troubleshoot/windows-client/installing-updates-features-roles/common-windows-update-errors),
[resolução de nome](https://learn.microsoft.com/en-us/troubleshoot/windows-client/installing-updates-features-roles/error-0x8024402c-installing-update-feature-on-demand).
Isso comprova falhas anteriores de transporte, não prova que ESU seja a única
causa nem que o problema continue ativo agora. Consulta DNS atual de endpoint
público Microsoft resolveu; WinHTTP informou acesso direto. Sem limpar cache,
alterar DNS/proxy/firewall, reiniciar serviços ou executar reparos. Não confundir
resolução pelo usuário com comunicação pelo serviço em todas as rotas.

Próximo diagnóstico é observar tentativa deliberada oficial de busca e seu novo
código/status de ESU, separando conectividade e elegibilidade. Não preparar um
reset genérico de Windows Update com base em erros históricos. O plano de backup,
KB5126657/SSU, janela de instalação, reboot e recuperação da documentação de
remediação continua aplicável **somente ao Windows 10**.

## Dry-run e ownership: implementação somente de planejamento

scripts/harness2_c2_plan.py é um planejador Python 3.12 stdlib sem operação
administrativa. Create verifica JSON limitado a 128 KiB/chaves únicas, caminho novo
absoluto sem links/junctions, nome restrito, tamanho e SHA256 dos dois artefatos.
Só então produz plano 2 vCPU, memória 3/3/4 GiB, disco até 48 GiB, NIC ausente e
intenção de journal versionado com UUID, raiz/disco e digest de manifesto.
O journal **não é persistido**, não representa recurso criado nem autoridade.

Exemplo privado, sem publicar caminhos reais:

```powershell
py -3.12 scripts/harness2_c2_plan.py --mode create --manifest docs/harness2-c2-deployment.v1.json --root $ApprovedNewRoot --iso $StagedISO --package $StagedDeb
```

Exit 3 significa plano produzido com bloqueios, nunca autorização. Erro/artefato
incompleto/conflito retorna 2. Repetição é idempotente quanto ao host; UUID da
intenção muda deliberadamente, pois não se reutiliza intenção não persistida.
Saída contém paths privados e deve ficar local, nunca no PR.

Discard recebe --journal, --vm-id e --root; exige estado created, UUID exato,
raiz/disco exatos e ausência de links antes de produzir checklist, **sem apagar**.
Journal fornecido pelo usuário não prova ownership: verificação de ACL/autenticidade,
inventário vivo por VMId, estado Off e exclusividade dos discos ainda obrigatórios.
Nenhum executor administrativo de criação/descarte foi implementado: faltam
closure autenticada, estado elevado e revisão; não retirar essas travas para avançar.
Recuperação futura exige intenção protegida antes da criação, VMId retornado
persistido após criação e reconciliação explícita de falhas parciais; jamais apagar
por prefixo, aceitar journal apenas por conter UUID ou descartar recursos existentes.

## Resultados e próxima decisão

- PASS: downloads completos e hashes reais; assinaturas; inventário .deb por leitura.
- PASS: cinco testes Python do planejador (bytes alterados/ausentes, JSON duplicado/
  excesso, caminhos relativos/conflitos/traversal, VMId/disco divergentes, intenção
  não criada); não são provas de isolamento do OS.
- PASS: create dry-run com os artefatos reais, saída BLOCKED/zero ações administrativas,
  exit 3; JSON e diff check.
- Memória: 6,94 GiB disponíveis na amostra, ainda insuficiente para admissão planejada;
  disco livre amostrado 173,34 GiB durante download, medir novamente após conclusão.
- Preflight final: 7,39 GiB disponíveis, 170,91 GiB livres em disco; ISO presente,
  exit 3, sem autoridade elevada e com requisitos obrigatórios ainda pendentes.
  Os avisos genéricos de autenticação no preflight referem-se ao deployment completo,
  não anulam a verificação específica dos bytes da ISO.
- NOT RUN: instalação, execução ELF, fixtures, VM, criação/cleanup administrativo,
  testes gVisor/Ubuntu reais. Go/Python B/C1/UI locais não repetidos porque runtime
  inalterado; testes novos do planejador executados. CI por HEAD reportado na entrega.

Intervenções necessárias: proprietário confirma manualmente status/validade ESU
sem compartilhar conta/chave; caso precise inscrição ou atualização, obter aprovação
separada com backup/janela/recuperação já descritos. Nenhuma mudança de Windows é
autorizada por download. Também falta comprovação independente da chave gVisor,
closure kernel/systemd/rootfs e calibração RAM. Nenhuma mensagem externa foi enviada.
Depois disso, revisar e autorizar executor administrativo exato; só então provisionar.
Todas as demais frentes autorizadas avançaram sem aguardar essas intervenções.
