# C2 — aquisição executada e preparação operacional

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
