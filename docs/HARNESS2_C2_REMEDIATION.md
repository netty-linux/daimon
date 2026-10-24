# C2 — sequência de remediação e aquisição, não execução autorizada

Revisão de 2026-10-23, base `4e385c62745a5fd95b5ab8cdf89896d1ccb6a1c6`,
branch esperada e checkout inicialmente limpo. **BLOCKED** para provisionamento.
Nenhum download ISO, instalação, licença, reboot, VM ou mudança de configuração.
Os 18 gates continuam unverified. Memória e calibração são condições independentes.

## 1. Windows: fatos e causa ainda não comprovada

Histórico Microsoft.Update.Session.QueryHistory e CurrentBuild/UBR reconfirmam
22H2 x64, 19045.6466, KB5071959 instalado com sucesso em 2026-02-26; KB5066791 em
2026-02-21. Não confundir data local de instalação com publicação do KB. A build
corresponde ao [OOB de 2025-11-11](https://support.microsoft.com/en-us/servicing/os/windows-10/2025/11/november-11-2025-kb5071959-windows-10-version-22h2-os-build-19045-6466-out-of-band).

Referência de outubro: [KB5126657 / 19045.7807](https://support.microsoft.com/en-gb/servicing/os/windows-10/2026/10/kb5126657-windows-10-21h2-22h2-security-update).
É posterior à build instalada; revisar oferta/aplicabilidade individual depois
de comprovar ESU. A Microsoft documenta SSU atual, LCU de julho de 2025 ou posterior
e condições distintas para servicing offline. O pacote corrente combina SSU
KB5127308 e LCU. Não prescrever instalação cega de SSUs antigos ou de todos os
LCUs intermediários. A revisão da página em 22/10 deve ser respeitada.

As consultas selecionadas não encontraram NoAutoUpdate, UseWUServer, AUOptions,
PauseUpdatesExpiryTime, PauseQualityUpdatesEndTime ou diferimento de qualidade
configurados nos locais consultados. Null não prova ausência de toda política,
MDM, pausa, erro ou bloqueio de oferta. Histórico sem tentativa posterior de LCU
não permite concluir por que a oferta cessou. ESU não comprovado é hipótese a
verificar, não causa raiz demonstrada; não há evidência que justifique reset
Windows Update, DISM de reparo ou mudança de ACL.

Cobertura ESU segue **UNVERIFIED**, conforme relatório anterior. Versão 22H2 é
pré-condição técnica, não prova de elegibilidade comercial, inscrição consumer ou
validade do período atual. Verificação humana deve conferir status e período no
canal oficial aplicável, sem fornecer contas, chaves ou IDs ao relatório público.
Não foi feita busca online de oferta via Windows Update: o KB oficial é referência,
não resultado de scan local de aplicabilidade.

### Plano executável, com autorizações separadas

1. **Consulta/preparação:** administrador verifica manualmente cobertura ESU vigente,
   status de oferta/erros em Windows Update, espaço de sistema e pré-requisitos.
   Registrar só categorias/KB/códigos sanitizados. Se ESU não estiver disponível,
   apresentar migração para Windows suportado compatível com Hyper-V, sem iniciar.
2. **Antes da mudança:** usuário escolhe janela de indisponibilidade, salva trabalho
   e aprova backup. Fazer backup verificável dos dados e imagem de sistema para
   destino controlado com capacidade suficiente, incluindo teste de recuperação.
   Conferir acesso à recuperação/WinRE e, se houver criptografia, disponibilidade
   privada do material de recuperação pelo proprietário; não coletar suas chaves.
   Ponto de restauração isolado não substitui backup. Não há backup realizado aqui.
3. **Autorização de licença, se necessária:** inscrição/ativação ESU exige decisão
   humana própria. Não comprar, ativar ou contornar licença automaticamente.
4. **Autorização de atualização:** revisar known issues do KB e oferta oficial x64
   para 22H2/ESU; preferir fluxo Windows Update do proprietário. Se Catálogo for
   necessário, confirmar produto/arquitetura, assinatura Microsoft e pré-requisitos
   antes de instalar. Não escolher pacote por resultado de busca de terceiro.
5. **Autorização de reboot:** separar instalação de permissão para reiniciar; usuário
   encerra/salva apps quando deliberadamente decidir. Nenhuma promessa de instalação
   sem reboot. Não alterar regras de reinício, Secure Boot, Hyper-V ou pagefile.
6. **Pós-mudança:** verificar build esperada ou cumulativo supersedente aprovado,
   KB/histórico, ESU vigente, ausência de reboot pendente e funcionamento normal de
   apps/Hyper-V. Falha mantém C2 bloqueada; não executar testes como diagnóstico.
7. **Rollback autorizado:** se falha de boot/compatibilidade, entrar em recuperação
   manual controlada e avaliar remoção da LCU apenas se suportada pelo pacote.
   SSU pode não ser removível; não prometer reversão pelo wusa. Se necessário,
   restaurar imagem/backup verificado. Regressar para build antiga implica retornar
   ao estado bloqueado de segurança. Nenhum rollback automático ou comando destrutivo.

## 2. Ubuntu: aquisição pronta para aprovação, ISO ainda CANDIDATE

Assinatura da lista já adquirida reconfirmada com GnuPG existente: exit 0 e
VALIDSIG `843938DF228D22F7B3742BC0D94AA3F0EFE21092`; fingerprint oficial e digest
registrados no manifesto. Não houve download novo da ISO nem alteração de keyring
global. Verificação repetida não comprova freshness/revogação sem nova consulta.

URL exata: https://releases.ubuntu.com/24.04/ubuntu-24.04.5-live-server-amd64.iso.
HEAD HTTP 200 informou **4.080.486.400 bytes (~3,80 GiB)**. Content-Length não é
prova de integridade. Reservar pelo menos 8 GiB de staging livre para download
parcial e arquivo final; isso integra, não substitui, a reserva total de 120 GiB.
SHA256SUMS e SHA256SUMS.gpg são os arquivos oficiais adjacentes, não assinatura
inventada diretamente para a ISO. Hash esperado:
`97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8`.

Após **aprovação específica para download de 4,08 GB**: escolher staging privado
sem reparse points e arquivo novo exclusivo, não sobrescrever arquivo existente;
baixar para nome parcial com limite de tamanho/tempo, sem executar ou montar;
reobter metadados/chave, conferir fingerprint por canal separado e revogação,
validar assinatura, calcular Get-FileHash SHA256 do arquivo completo e comparar
à entrada exata assinada. Qualquer divergência bloqueia uso. Só então registrar
bytes/resultado VERIFIED e publicar nome/hash, sem caminho privado. Aprovação de
download não autoriza instalação ou provisionamento.

## 3. gVisor: corroboração adicional, não autenticação completa

Encontrada chave em **segundo canal oficial**, website/archive.key no commit
760b80785dc53311d4c87bd2de167525a99709c1 do repositório google/gvisor. Seus bytes
são idênticos aos de gvisor.dev/archive.key, SHA256
`757d2ebdf00cdc5661f88443e2bea848d3e2ded0780cbd2d811be05e0c1d5dd`, fingerprint
`6F1DF85E3A71C24918E727D56FC6D554E32BD943`. Histórico de alteração consultado
aponta b6baa377d85db823c9d1c15658e843d6683835a3, assinatura unsigned. Corroboração
de distribuição não fornece âncora criptográfica independente: website e GitHub
podem compartilhar autoridade. Autenticidade de produtor permanece UNVERIFIED.

Próxima prova necessária: fingerprint/rotação/revogação confirmados por mantenedor
em canal autenticado independente aceito pelo operador, ou proveniência verificável
ancorada em identidade previamente confiável. Não criar assinatura local como
substituto. Nenhuma mensagem a mantenedores foi enviada nesta tarefa.

Pacote candidato exato:
https://storage.googleapis.com/gvisor/releases/pool/20261019.0/binary-amd64/runsc.deb,
76.131.500 bytes, SHA256
`24e9740411c15f229151155e9b890388a18cfb4c9fce5c8d7ea6ed9f926e7a52`.
Não baixado. Metadata anuncia versão 20261019.0 amd64; não declara Depends no
stanza observado. Isso não comprova closure de execução: kernel, bibliotecas,
sidecars, supervisor e rootfs continuam pendentes.

Depois de resolver a chave: capturar Release/assinatura/Packages; impor freshness
externa máxima proposta de 7 dias e relógio confiável, recusar data futura além de
5 minutos, replay/downgrade e qualquer Valid-Until vencido. Como Release observado
não contém Valid-Until, não anunciar validade upstream; limite externo ainda exige
revisão. Reaquisição antiga não renova sua data. Registrar snapshot aprovado e
digest, sem usar latest ou pin só por data móvel.

Download posterior do pacote deve conferir assinatura Release → digest Packages →
digest bytes .deb; depois inventariar arquivos, symlinks, permissões, control e
maintainer scripts por leitura, sem instalá-los/executá-los. Comparar layout completo
de oito arquivos declarado no manifesto com conteúdo real, resolver ELF/libs e
dependências por metadados assinados, nunca por ldd de binário não confiável.
Shim é preservado no inventário do bundle, embora OCI direto não use containerd.
Não habilitar downloads de sidecars ou configuração automática de Docker.

## 4. Ordem de decisão e critérios

1. Resolver cobertura/atualização do host e recuperação autorizada.
2. Aprovar aquisição da ISO; verificar assinatura e todos os bytes.
3. Fechar autenticidade da chave gVisor e closure de runtime/rootfs/kernel/systemd.
4. Calibrar recursos em futura janela autorizada: 2 vCPUs, 3–4 GiB dinâmicos,
   uma execução; reserva do Windows preservada. Não fechar apps automaticamente.
5. Revisar provisionador/cleanup e solicitar autorização exata dos recursos.

Resolvido nesta revisão: tamanho/URL ISO e plano de aquisição; assinatura da lista
reconfirmada; chave gVisor corroborada em outro canal oficial; plano Windows com
backup/reboot/rollback. **Não resolvidos:** cobertura ESU, patch efetivo, causa de
ausência de oferta, bytes ISO/pacote, confiança independente da chave, revogação,
closure e capacidade/calibração. Nenhuma dessas etapas libera gates ou C3.

JSON e invariantes verificados; diff somente documental/manifesto. Sem testes de
produção repetidos, nenhuma alteração de scripts, nenhuma instalação ou merge.
