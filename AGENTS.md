# DAIMON

## Native Routines Foundation — escopo autorizado em 2026-10-09

- Rotinas só agendam SessionManager.Start; nunca Space.agentStart, routineAdd ou
  agentes persistentes/harnesses CUA. ToolPolicy, budget e aprovação por call intactos.
- Agendador possui lifetime da aplicação e só funciona com servidor aberto, sem
  catch-up de slots vencidos. Uma Session agendada reserva seu Bot; approval/startup/
  cleanup bloqueiam novo disparo. Pausar rotina não decide aprovação nem aborta Session.
- Store explícito/versionado limitado a 32 rotinas, 4 ativas/Bot, intervalo mínimo
  de 15 minutos entre tentativas por Bot; horário diário e timezone IANA explícitos.
- Cloud paga permanece bloqueada em rotina; escrita nativa permanece Linux-only
  opt-in por processo e aprovação individual. Nenhuma permissão vem da agenda/Volume.
- Memory/Volume controlado e associação Bot/Space continuam separados e pendentes;
  não antecipar importação automática, comandos, retries ou agentes externos.

Visão: plataforma local experimental Daimon Bots, sob controle do usuário,
com runtime nativo provider-agnostic e contratos fortes de execução local.
Escopo atual: Reliable Agent Loop + Execution Budget + OpenAI-compatible
non-streaming + Human Approval + Workspace Read + Single-File Replacement,
em Go, com Scripted, echo, read_file, list_dir, replace_file e create_file (escrita em Linux).

## Product architecture direction

DAIMON é um runtime de agentes provider-agnostic: não possui LLM próprio.
"DAIMON is not a model. DAIMON is the environment where models become agents."
Provider, instância model.Model, Bot, Thread e Session são conceitos distintos.
A direção oficial e auditoria estão em docs/DAIMON_ARCHITECTURE_V2.md.
Preserve também workspacefs, workspaceplan, workspacejournal e managedworkspace:
artefatos de workspace não são sessões/conversas persistentes nem garantem sandbox.

## Incremental architecture rule

Nenhuma fase futura pode ser implementada antecipadamente sem primeiro estabilizar
e testar contratos da fase atual. Roadmap: 0 baseline arquitetural; 1 identidade,
registry/configuração de providers; 2 Bots; 3 Threads; 4 Sessions; 5 servidor local;
6 SSE; 7 UI web; 8 histórico e respostas; 9 Web Approval; 10 MCP stdio tools;
11 Memory Foundation manual; 12 Computer Use Foundation + CUA Driver local;
13 Live Computer View + Human Takeover explícito e efêmero.
Phases 0–13 estão estabelecidas, com transcript persistente separado
e controle de input humano exclusivo no ComputerManager. Phases 14/15 adicionam sandbox local/cloud; Phase 16 adds durable workspace revisions; próxima: Phase 17 Background Tasks.
As fases futuras exigem escopo explícito próprio; não criam autorização para efeitos.
Não criar adapters vazios, abstrações especulativas ou dependências sem necessidade.

## Provider independence

agentloop/model/tools não podem importar providers. model.Model continua sendo a
fronteira. Registry de providers é explícito, local ao chamador, sem globals mutáveis,
init registrations, descoberta, rede ou credenciais capturadas nas factories.
Config sensível não é logada/persistida. Factories produzem model.Model e não tools.
Mantenha o transporte compatível em providers/openai e o wrapper Groq sem duplicação.

## Server/UI boundary

Server/UI futuros são clientes do runtime, não o local onde lógica do agente vive.
Devem adaptar EventSink tipado e encaminhar aprovação de uso único, sem outro loop,
emissor global, permissões implícitas ou conteúdo sensível nos eventos públicos.
HTTP local é Phase 5; SSE é Phase 6; UI local é Phase 7. O servidor só transporta intenção.

## Compatibility

CLI existente deve continuar funcionando durante a evolução: demo, chat, smoke,
workspace e managed-workspace. Preserve flags, defaults, diagnósticos, instruções,
políticas, erros tipados e contratos existentes. Somente cmd lê o ambiente atual;
seleção/construção usa o registry explícito sem alterar o protocolo do runtime.

## Bot Domain

- Bot é configuração declarativa; não é provider, modelo, thread, sessão ou processo.
- Bot usa providers.ID e valida seu formato compartilhado. Existência do provider
  e das ferramentas é resolução futura do runtime, nunca registry global no domínio.
- Tools declara capabilities, nunca autorização. PermissionMode é explícito e
  validado; nenhum valor bypassa policy, aprovação ou flags de escrita.
- model, agentloop, tools e policy não importam bots. Não alterar o loop nesta fase.
- Bot não contém campos de credenciais, endpoints ou factories. Nunca inserir
  secrets nos textos de configuração; textos livres são privados e não são logs.
- Store JSON local versionado, estrito, limitado e com caminho explícito; sem HOME,
  persistência automática, recuperação silenciosa ou registry global de bots.
- Store é sequencial, com um único escritor em diretório controlado. Temporário no
  mesmo diretório, modo restrito, Sync/Close/rename; não truncar arquivo válido.
  Não prometer exclusão de escritores externos, atomicidade Windows ou durabilidade
  de diretório. Cleanup falho é erro explícito. Preserve cópias defensivas de Tools.

## Thread Domain

- Thread é metadata persistente de conversa; não executa nada, não autoriza tools,
  não resolve providers e não contém credenciais, histórico ou estados de sessão.
- BotID é referência estrutural. Existência de Bot e acesso/existência de workspace
  são resolução futura; domínio não abre/canonicaliza workspace nem usa cwd implícito.
- ID, BotID, workspace e CreatedAt ficam fixos no store; UpdatedAt não retrocede.
  Timestamps UTC não zero são fornecidos pelo chamador, nunca inventados pelo store.
- Thread não guarda snapshot de Bot/provider/model; Session Runtime fixa um binding
  por execução. Novos turns recebem contexto limitado do transcript e resolvem o Bot atual.
- Store local explícito/versionado/limitado segue as restrições de escrita do Bot
  Store. Não abre Bot Store nem cria sessões. Sem HOME ou persistência escondida.
- model, agentloop, tools e policy não importam threads. Preservar core e CLI.

## Session Runtime

- Session monta os contratos existentes; model/agentloop/tools/policy não importam
  sessions, bots ou threads. Não alterar o loop para conhecer domínios/stores.
- Dependências/config/budget são explícitos. Registry é copiado antes de uso
  concorrente; leitores e callbacks precisam suportar chamadas simultâneas.
- Binding é cópia por execução, sem credenciais/recursos; Bot mutado não altera run.
  Snapshots omitem instruções/modelo/texto/tools; Binding é acesso privado deliberado.
- Uma Session ativa (inclusive startup/approval/cleanup) por Thread, IDs não reutilizados,
  estados/transições fechados. Limites positivos de sessões retidas e buffer são explícitos.
- Start assíncrono tem ownership pelo contexto e Manager.Close/Wait; Abort cancela,
  não desfaz commits. Dependências cooperam com contexto; não prometer interrupção
  forçada. Budget do loop governa execução/aprovação; resolução usa contexto externo.
- Nenhum lock do manager envolve provider, model, tool, approval ou filesystem.
  Guardas convertem panics de componentes antes de desenrolar o loop; nunca logam
  panic value/stack. Testes concorrentes usam canais e passam race detector.
- Runtime resolution falha fechado, sem fallback de Thread/Bot/provider/tool/root.
  Root de runtime deve ser absoluto explícito, aberto pela boundary existente.
- Capabilities fixas: read_file/list_dir leitura; replace_file/create_file escrita;
  echo other. read-only só resolve leitura; ask preserva policy. Escrita exige opt-in
  por Start (flags incompatíveis), Linux e reviewer individual com preview integral.
- Erros públicos/categorias e eventos omitem dados sensíveis. Buffer ring sequenciado
  contém só agentloop.Event e informa gap de replay; não há segundo event loop.
- Session é efêmera, sem arquivo ou credencial persistida. FinalText é transitório
  e alimenta ConversationStore após sucesso/cleanup; resultados públicos continuam
  contadores/StopReason/categoria, sem texto ou histórico.
- Threads diferentes podem executar em paralelo; roots compartilhadas e streams
  de aprovação exigem ownership/serialização do chamador, sem garantia entre managers.

## Local HTTP Server

- internal/server usa net/http e dependências explícitas; não resolve HOME/ambiente.
  model/agentloop/tools/policy não importam server. Manager é autoridade do runtime.
- Bind somente loopback nesta fase; default 127.0.0.1:3000, sem opção pública/LAN.
  Serve valida endereço TCP real do listener injetado. Não habilitar CORS wildcard.
- Application/CLI possui Manager e contexto de sessão independente do request HTTP.
  Shutdown HTTP primeiro, depois Close/join do Manager, com prazo explícito.
- Adapters dos stores serializam CRUD e leitura do Manager nas mesmas instâncias;
  nenhum handler ignora validação do domínio. Escritores externos não são suportados.
- JSON estrito, chaves exatas/únicas, corpos e respostas limitados; erros classificados,
  nunca err.Error/cause/stack arbitrários. Não logar bodies, prompts, secrets ou paths.
- Nenhuma credencial/config de provider entra por HTTP. Config continua somente cmd.
  Não retornar instruções de Bot, Binding ou args/results de tool. Conteúdo de
  conversa só aparece no GET messages deliberado; eventos/sessões/erros não.
  Thread CRUD deliberado mostra a referência workspace.
- HTTP não expõe flags de escrita ou API de aprovação. serve nega leituras por chamada;
  nunca aprova automaticamente nem lê terminal. Policy/contratos permanecem existentes.
- API /api/v1 inclui polling JSON limitado e SSE de eventos, sem Wait/long polling.
  Sem autenticação; apenas clientes locais confiáveis. Loopback/ausência de CORS não
  substituem autorização local. Phase 7 verifica Host loopback e Origin/Fetch Metadata.

## Web UI

- React é cliente estático somente; backend valida e governa runtime/policy/estado.
- REST e SSE same-origin; sem API keys, armazenamento de secrets ou URLs de provider
  no browser; nenhum acesso direto a filesystem ou lógica de agente em React.
- Escape React obrigatório, sem raw HTML/dangerouslySetInnerHTML para dados.
- SSE disconnect/troca de seleção nunca aborta Session. GET snapshot é autoridade;
  replay_gap é explícito; stream_end fecha EventSource. Eventos visíveis são limitados.
- Não inventar assistant response/history nem expor instruções privadas de Bot.
  Full PUT exige reentrada deliberada de instruções. Web Approval individual usa o provider/runtime existente; veja docs/WEB_APPROVAL_V1.md.
- Build em ui gera internal/server/ui versionado e embutido; go test independe de Node.
- Preserve loopback-only e ausência de CORS/autenticação/exposição pública.
  Network handler exige Host local; Origin presente deve corresponder exatamente;
  Fetch Metadata cross-site/same-site é negado. Clientes locais sem Origin continuam.
- Validação frontend é auxiliar: validação do servidor continua autoridade.

## SSE de eventos

- Ring da Session é a única fonte de replay; SSE usa EventsSince sem mudar polling.
  ObserveEvents captura geração antes do replay, sem filas/registro por cliente.
- Subscriber observa, nunca consome/remove eventos. Last-Event-ID usa sequence da
  Session; tem prioridade sobre after. Gap explícito precede o sufixo disponível.
- Heartbeat é comentário, não consulta replay nem altera sequence. Sem busy loop.
- Streams globais limitados, payloads limitados e deadlines por write/flush; nenhum
  lock do Manager durante I/O HTTP. Nomes e stop reasons são constantes validadas.
- Disconnect/falha de escrita só encerram observação, nunca abortam/falham Session.
  Sessions pertencem à aplicação. Shutdown HTTP sinaliza streams antes de drenar.
- Estado terminal finalizado drena eventos e envia stream_end sem id, depois EOF.
  Transport envelopes não criam eventos/sequence no runtime nem expõem texto final.
- SSE não contém secrets, mensagens, instruções, paths, args/results ou erros livres.
  Falha pós-início usa transport_error controlado quando possível; não truncar JSON.

## Arquitetura

- Biblioteca padrão, interfaces pequenas e fluxo explícito, síncrono.
- Preserve Model.Generate, Tool.Execute, Registry e EventSink tipado.
- Não adicione emissor global nem entregue Registry/implementações ao modelo.
- Copie requests e chamadas para proteger o histórico de mutações externas.
- Dentro de cada execução, ferramentas sequenciais, sem retries ou concorrência.
- Configuração explícita: Loop.Budget; DefaultBudget somente quando solicitado pelo chamador.
- Preserve errors.Is para ErrInvalidConfig, ErrInvalidResponse, ErrMaxSteps e contextos;
  ModelError deve preservar passo e causa. LimitError deve distinguir Kind.

## Invariantes do budget

- Todos os campos são positivos. Valide antes de chamar o modelo; zero nunca é ilimitado.
- Uma chamada ao modelo = um passo; nunca ultrapasse MaxSteps.
- Pré-valide o lote inteiro antes de efeitos: IDs/nomes não brancos, IDs únicos em toda
  execução, quantidade por passo/total, bytes por argumento e capacidade de histórico.
- Conte Content, ToolCallID, IDs, nomes e json.RawMessage por bytes, não tokens ou runes.
- Reserve assistant e todos os recibos: cada recibo inclui len(ID) + MaxToolResultBytes.
  Aritmética não pode aceitar um lote por overflow.
- Verifique capacidade para a mensagem inicial e para a resposta final antes de adicioná-las.
- Rejeite texto com calls, resposta final branca, acima do limite ou que não caiba integralmente.
- Normalize resultados para UTF-8 e limite em fronteira válida. Inclua marcador completo:
  original_bytes quando couber, [truncated] ou ~ para limites mínimos. Conte toda alteração.
- ToolCalls conta tentativas iniciadas, incluindo falhas controladas/interrupções.
  Lotes rejeitados e chamadas não iniciadas não contam.
- Cancelamento pode deixar calls sem recibo; preserve histórico aceito e pare novos efeitos.
- Use timeouts cooperativos com causa identificável, respeitando deadlines externos.
  Verifique antes/depois das chamadas; sucesso após expiração não é sucesso do loop.
- StopReason deve existir em todo retorno e no único loop_stopped correspondente.
- Evento não contém mensagens, nomes/IDs do modelo, argumentos, resultados ou texto de erro.

## Invariantes de autorização

- Loop.Authorizer é obrigatório; nil é ErrInvalidConfig antes da primeira chamada ao modelo.
- Autorização é fail-closed: nenhuma chamada executa sem decisão Allow explícita.
- O lote inteiro é autorizado antes do primeiro Execute, em passagem única por chamada,
  sem execução nem efeitos; o lote rejeitado pelo budget nunca chega ao Authorize.
  Eventos de aprovação podem ser emitidos durante essa passagem; nenhum efeito de
  ferramenta ocorre antes de todas as decisões.
- ToolPolicy e ApprovalProvider são conceitos fora do loop: um authorizer composto
  implementa ToolAuthorizer e resolve política e aprovação antes de responder.
  O loop não conhece terminal, política ou motivos.
- Política estática nega por padrão; fallback não configurado nega (zero value).
  Decisão de política inválida falha fechado, sem consultar aprovação.
- ApprovalProvider: uma aprovação vale para uma única call; não persiste decisão,
  não oferece "sempre permitir". Padrão é No; EOF e entrada inválida negam sem
  falhar o run. Cancelamento interrompe a aprovação e retorna o erro de contexto.
- Argumentos exibidos são sanitizados: controles e formatação Unicode não chegam
  ao terminal; aprovações de leitura não exibem conteúdo de arquivo. A substituição
  exige o preview completo com escapes do contrato. Prompts vão ao stderr injetado;
  nada lê os.Stdin dentro da política.
- Authorize recebe runCtx e continua governado por MaxRunDuration e deadlines externos.
- ToolAuthorizationRequest recebe os argumentos copiados defensivamente.
- Somente allow e deny são decisões válidas; qualquer outro valor falha fechado.
- Erros do authorizer impedem todo Execute daquele lote; erros de contexto voltam
  diretamente; erros não de contexto viram AuthorizationError, cuja mensagem só
  nomeia passo e ferramenta e preserva a causa por Unwrap.
- Permitido direto: ToolAllowed → ToolRequested → ToolCompleted/ToolFailed.
- Aprovado: ApprovalRequested → ApprovalGranted → ToolAllowed → ToolRequested →
  ToolCompleted.
- Negado: (ApprovalRequested → ApprovalDenied quando passa por aprovação) →
  ToolDenied → recibo controlado correlato, sem execução.
- Desconhecido ou JSON inválido: ToolRequested → ToolFailed, sem evento de autorização.
- Eventos nunca contêm nomes, IDs, argumentos, motivos de decisão ou resultados.
- A política padrão do CLI permite echo, exige aprovação para list_dir e read_file; replace_file e create_file são negados por padrão
  e nega qualquer outra ferramenta; não existe opção global de aprovar tudo.
- Não adicionar aprovação permanente, wildcards de permissão, configuração de
  política em arquivo ou ferramentas além de replace_file e create_file neste corte.

## Segurança e escopo

- internal/editcontract prepara propostas imutáveis e aprovação de uso único.
  Permit.Apply substitui um arquivo existente em Linux. Vincule caminho, versão
  original, bytes propostos e limites; preview ASCII reversível completo obrigatório.
  Rejeite symlinks observados em qualquer componente; revalide antes/depois da
  decisão e na aplicação. Rejeite hard links em Linux. Falha/cancelamento gasta
  a tentativa/capacidade, sem retry. Temporário exclusivo no diretório do alvo,
  sync/close e rename; cleanup em falha, com erro explícito se não puder remover.
  Não truncar o alvo diretamente. Uma proposta de escrita por run/instância.
  Deadline obrigatório; cancelamento antes do commit impede rename, após commit
  não desfaz o efeito. Não prometer exclusão atômica de escritores externos,
  durabilidade de diretório, sandbox ou escrita atômica no Windows.

- Ferramentas desconhecidas, JSON inválido e erros normais viram resultados controlados.
- Não usar panic, log.Fatal ou os.Exit no loop.
- read_file e list_dir permanecem confinados com os.Root, somente caminhos
  relativos, limites de bytes, validação estrita e bloqueio de traversal/symlink
  externo/diretórios; list_dir é não recursivo, ordenado, rotula symlink sem
  seguir e falha integralmente ao exceder limites, sem corte silencioso.
- Testes de symlink devem executar; falta de permissão é falha explícita.
- Banco, memória inteligente e subagentes são fases futuras do
  roadmap. MCP stdio tools está implementado na Phase 10, dentro do contrato próprio. Registry de providers é Phase 1;
  protocolos adicionais só entram com necessidade real e testes offline.
- Não adicionar gateway, Telegram/Discord, TUI, event sourcing completo, shell,
  novas ferramentas, edição em lote ou exclusão/movimentação de arquivos do usuário
  neste corte. Preserve o fluxo offline já existente de cópia gerenciada e seus limites;
  ele não habilita apply na árvore compartilhada. Temporários internos são permitidos.
- Não anunciar garantias de sandbox ou limite rígido de memória: o budget limita dados aceitos.

## Provider HTTP

- Providers implementam model.Model sem modificar os contratos do loop.
- HTTP e structs privadas do protocolo pertencem a internal/providers; agentloop e model
  e tools não importam providers. cmd/daimon escolhe Scripted ou constrói o adapter
  pelo registry explícito; nenhuma seleção fica no loop.
- Secrets entram pela configuração explícita. Somente cmd lê DAIMON_BASE_URL,
  DAIMON_MODEL, DAIMON_GROQ_MODEL e DAIMON_API_KEY; nunca imprimir/gravar chave ou aceitá-la em argumento CLI.
- Smokes reais opt-in usam Groq por padrão via cmd/daimon smoke. internal/providers/groq
  configura o adapter OpenAI-compatible existente; não duplica transporte/protocolo.
  Testes automáticos e CI continuam offline, sem credenciais; não há fallback.
- Provider traduz mensagens e descrições; não executa ferramentas, não faz retry,
  não implementa streaming/SSE, Responses API, fallback ou SDK externo.
- Preserve argumentos como string/raw bytes, inclusive JSON inválido, para recuperação
  pelo loop. Valide presença/tipos do envelope e schema JSON antes de enviar.
- HTTPS remoto; HTTP somente localhost, 127.0.0.0/8 e ::1. Sem userinfo/query/fragment.
- Cliente padrão recusa redirects. Cliente injetado tem política do chamador;
  não configure timeout próprio que concorra com o Budget nem substitua o contexto recebido.
- Limite body antes de decodificar, incluindo bodies de erro, e feche-o em todos os caminhos.
- Error() não inclui chave, URL, corpo ou mensagem livre do servidor. Preserve causa de
  transporte por Unwrap e identidade de erros de contexto. Metadados precisam ser limitados.
- Demo permanece offline. Testes de provider/chat usam httptest.Server; nenhum teste toca
  a internet e o CI não usa credenciais ou provedores externos.

## Processo e validação

Antes de modificar: inspecione o estado, resuma o comportamento, liste arquivos,
semântica e invariantes afetadas. Preserve os testes da versão anterior, migrando
apenas a configuração necessária. Cubra limites e falhas com testes sem rede.

Execute em Linux, incluindo os testes de symlink:
```sh
gofmt -w .
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go run ./cmd/daimon demo
```
gofmt -l não pode listar arquivos. CI executa formatação, vet, testes e race detector
a cada push e pull request. Não substitua execução real por comandos simulados.

## Chat de substituição opt-in

- No CLI, somente chat ou workspace com --enable-replace-file registra replace_file e configura RequireApproval
  para esta execução. Chat comum, smoke e demo não expõem a ferramenta.
- A opção nunca aprova escrita: preview completo e aprovação explícita continuam obrigatórios.
- Não enviar original, diff ou preview adicional ao provider. Argumentos produzidos pelo
  modelo e recibo integram o protocolo; read_file aprovado mantém sua fronteira atual.
- Conteúdo só aparece no preview deliberado, nunca em eventos, recibos ou erros.

## Sessão efêmera de workspace

- workspace exige --root explícito válido antes de configurar o provider; todas as
  ferramentas usam esse root, sem fallback para o diretório atual.
- --enable-replace-file e --enable-create-file são opt-ins separados de escrita,
  incompatíveis entre si neste corte; mensagem e ambiente não ativam nenhum deles.
- Preservar contratos, preview, autorização e executor existentes sem modificações.
- Resumo usa somente eventos tipados e contadores dos cinco tipos fixos de ferramenta;
  nunca imprimir resposta final, argumentos, paths, IDs, outputs ou segredos no resumo.
- Contar tentativas de Execute e commits confirmados, inclusive em runs interrompidos.
- A sessão workspace continua efêmera; não criar abstrações genéricas de sessão
  ou múltiplos workspaces neste corte. O fluxo offline managed-workspace mantém
  seus artefatos privados existentes, sem persistência de conversa/provider/segredos.

## Planejamento read-only

- workspace --root "diretório" plan "mensagem" solicita plano textual estruturado,
  sem interpretar, validar semanticamente, persistir ou executar seu conteúdo.
- Plan nunca registra replace_file; flag de escrita e plan são incompatíveis.
- Validar formato antes de abrir workspace; root antes de configurar provider.
- Preservar policy e requests originais. Nos displays deliberados, prompts de aprovação
  mostram tipo e caminho relativo integral com escapes reversíveis e aviso de envio ao provider; ainda exigem decisão por chamada.
- Plano é saída deliberada separada do resumo público e pode conter dados do modelo.
  Resumo, eventos e erros públicos não incluem esses dados; displays deliberados de aprovação identificam o alvo.
- Contratos/preview/executor e limites Linux existentes não mudam.
- Preservar a validação de escopo opt-in e a recuperação única sem ferramentas
  já existentes: não aprovam escrita, contam no budget e não validam a verdade
  da prosa. O parser workspaceplan não habilita apply na árvore compartilhada.

## Criação opt-in de um arquivo

- No CLI, somente workspace --root explícito --enable-create-file registra create_file.
  Plan, chat e a flag de replace_file não habilitam criação; neste corte não combinar flags.
- internal/createcontract é distinto de editcontract: proposta/Permit separados,
  root aberto, pais existentes sem symlink, ausência, bytes exatos e limites vinculados.
- Preview completo ASCII reversível e aprovação individual de uso único obrigatórios.
  Falha, negativa, cancelamento, conflito ou aplicação gastam tentativa/capacidade.
- Linux apenas: O_EXCL direto no alvo ausente, chmod 0600 independente de umask,
  chunks/contexto, Sync, hash/verificação limitada, Close e identidade/metadados finais.
- Nunca sobrescrever/renomear. Cleanup de arquivo incompleto próprio é permitido;
  falha de limpeza/identidade deve ser explícita e nunca convertida em sucesso.
- Não prometer conteúdo publicado atomicamente: arquivo pode ser visto em gravação.
  Workspace controlado; sem garantia contra escritor externo ou perda de energia.
- Preservar contratos/executor de replace_file, plan, loop e eventos sem dados sensíveis.

## Conversation History — Phase 8

- Conversation History != Memory. Transcript pertence a internal/conversations;
  ThreadStore continua metadata. Somente user/assistant final são persistidos.
- Mensagens são imutáveis, append-only, UTF-8 exato, não brancas, com limites em
  bytes, IDs restritos/únicos e sequência monotônica por Thread. Sem edit/delete.
- Sem tool results, system instructions, eventos ou credenciais no transcript.
  Conteúdo nunca entra em runtime events, snapshots, logs ou erros públicos.
- Contexto seleciona somente um sufixo ordenado de mensagens inteiras; reserva
  novo user/final e respeita Budget, cloning e validação independente do loop.
  Exclusão do contexto não apaga mensagens; sem tokenizer/summarization.
- User persiste antes da execução; falha/abort pode deixá-lo sem assistant.
  Assistant exato só persiste após sucesso e cleanup, antes de Completed.
  Falha de persistência é explícita; não há transação ACID nem recuperação silenciosa.
- Commit de assistant confirmado não é desfeito por cancelamento posterior.
  Binding fica imutável por run e uma Session ativa por Thread inclui persistência.
- Version 1 por Thread; arquivo ausente = vazio, versão/corrupção falham fechado.
  Um processo/Store possui o diretório; sem garantia entre escritores externos.
- message_id evita duplicata persistente; GET messages é endpoint privado deliberado
  paginado. DELETE Thread com transcript é bloqueado sem remoção de conteúdo.
- UI busca transcript do Store, separado da activity SSE; React escapes obrigatórios.
  Reload/restart preservam mensagens, sem restaurar Sessions. Memory inteligente e agentes avançados continuam futuros; Web Approval e MCP são
  extensões separadas nas Phases 9 e 10.

## Web Approval — Phase 9
- Preserve docs/WEB_APPROVAL_V1.md: uma pendência por Session, ID aleatório, decisão Allow/Deny de uso único; nunca persistir confiança.
- GET approval é display deliberado. Nunca incluir argumentos brutos, previews ou alvos em SSE/snapshots/erros. Preview de escrita deve ser Review.Display integral do contrato.
- Browser não cria capabilities nem altera policy. Flags serve de escrita são explícitas, exclusivas e não aprovam efeitos. Abort/deadline/Close/shutdown invalidam pendências.
- Nunca adicionar always-allow, permissões persistentes ou fila genérica de aprovação. Dupla resolução e IDs consumidos devem falhar explicitamente.
- Registrar presentation completa antes de publicar ApprovalRequested. Autorizar o lote completo antes do primeiro Execute; nenhuma espera humana segura mutex do Manager.
- Desconexão SSE, reload, troca de seleção e fechamento do modal nunca decidem. Recuperação lê Session ativa e GET pending; conteúdo não vai para storage do browser.
- Preservar providers de aprovação terminal, contratos de escrita e limites de preview existentes; não truncar displays para caber no HTTP.

## MCP Tool Integration — Phase 10

- Preserve docs/MCP_V1.md: MCP stdio é fonte de ferramentas; loop, Tool/Registry, policy, ApprovalProvider e Conversation Store preservam responsabilidades.
- Config local versionada estrita; nomes exatos mcp__server__tool; browser somente catálogo/status e seleção de nomes, nunca configuração, spawn ou autorização permanente.
- Read local explícito exige aprovação individual sem argumentos brutos. Write/other/unclassified são negados; flags nativas nunca habilitam escrita MCP; sem presenter genérico.
- Discovery não executa tools. Protocol 2025-11-25, frames/pending/queues/results bounded, IDs correlacionados; timeout/cancel/protocol violation aposentam conexão, sem restart ou retry.
- Exec direto, ambiente operacional injetado sem DAIMON_*; stderr descartado; subprocessos encerram após Sessions e são aguardados. Sem promessa de sandbox ou limite de memória externo.
- Eventos/snapshots/erros públicos sem argumentos/resultados/protocolo/segredos. Aprovação e catálogo são displays deliberados mínimos.
- Testes offline com subprocesso real, incluindo Linux cleanup, autorização, persistência, falhas e navegador opt-in. Memory é extensão separada da Phase 11; Computer possui contrato próprio na Phase 12. Phase 13 é implementada pelo contrato Computer View; Phases 14/15 seguem os contratos de sandbox/cloud abaixo; não antecipar Phase 17.

## Memory Foundation — Phase 11

- Memory manual pertence a internal/memory e é separada do transcript. Sem criação
  automática por modelo, ferramenta, mensagem ou evento; UI/HTTP CRUD deliberado.
- Escopos global/Bot/Thread, kinds fact/preference/instruction/note; conteúdo UTF-8
  exato até 16 KiB, 16 tags restritas, ID/escopo/criação/proveniência imutáveis.
  Store possui timestamps UTC e origem manual; UpdatedAt avança estritamente.
- memory.json versionado: 2048 registros/32 MiB; JSON estrito, cópias, mutex,
  temporário exclusivo 0600, Sync/Close/rename e falha explícita de cleanup.
  Corrupção/versionamento falham fechado; sem reset/eviction/recovery silencioso.
- HTTP valida existência e serializa referências com remoção dos alvos; scoped
  Memory impede DELETE Bot/Thread, sem cascata. Store isolado valida só formato.
- Retrieval lexical determinístico só global/Bot atual/Thread atual: prioridade
  Thread/Bot/global, matches, UpdatedAt e ID; limites positivos explícitos.
- Congelar contexto antes de Start retornar. Reservar usuário/final completos,
  Memory e depois history; frame e registros integrais contam bytes, sem truncar.
  Bot instructions permanecem separadas, AdditionalContext limitado a 32 KiB.
- Memory é dado contextual, nunca autoridade. Não mudar Model/AgentLoop/policy,
  capabilities, aprovação individual, workspace, providers ou contratos MCP.
- CRUD e contexto do provider são displays deliberados. Nenhum conteúdo em
  eventos/snapshots/logs/erros. Plaintext local explícito, sem detector de segredos,
  criptografia, sandbox, lock cross-process ou garantia atômica Windows.
- Preservar regressões, testes offline/race/Linux/symlink e UI build antes de Go
  embed. Browser smoke:memory usa servidor real/provider fake local sem credenciais.
- Intelligent Memory (fase futura independente) (Extraction + Summarization + Semantic Retrieval)
  continua adiada; não adicionar extração, resumo, embedding, RAG ou subagentes.


## Computer Use Foundation — Phase 12

- Computer é domínio próprio em internal/computer; CUA é backend, não autoridade.
  Model e AgentLoop não importam computer. Native/MCP mantêm seus contratos.
- computer_backend opcional em mcp.json é explícito, apenas cua-local, um servidor
  físico; comando absoluto cua-driver[.exe], argv exato ["mcp"]. Sem novo client/SDK,
  instalação, atualização, bypass, modo unrestricted, retry ou restart automático.
- MCP genérico segue read-only; Lookup nega toda tool do servidor CUA. Transporte
  reutilizado seleciona legado 2025-06-18 para CUA, 2025-11-25 para demais servidores,
  sem fallback. Discovery não chama tools. MCP é transporte, nunca autoridade.
- Bot.ComputerProfile é opcional compatível: enabled/backend/mcp_server_id exatos
  obrigatórios quando presente. Ausente/disabled não habilita nomes CUA em Tools.
  Clone defensivo; Memory/transcript/instruções/aprovação não criam capabilities.
- Session possui binding congelado com subset local/discovery/Tools/permissão.
  Toda ação suportada, inclusive observe, exige aprovação individual. Read-only
  admite só observe. Unknown/dangerous e opções não suportadas negam por padrão.
- Preview de Computer descreve ação e alvo completos sanitizados; type_text mostra
  texto integral com escapes ASCII reversíveis. Nunca JSON bruto, truncamento,
  permissão permanente ou aprovação global. Reutilizar preautorização do lote.
- ComputerManager coordena lease exclusiva por Session no desktop físico, inclusive
  leituras/esperas de aprovação. Concorrente recebe computer_busy antes do modelo.
  Completion/abort/failure/close liberam após calls terminarem; handles antigos
  negam. Cancelamento não desfaz efeito executado. Lease só coordena um processo.
- Screenshots são dados efêmeros e não entram em logs/SSE/history/Memory/snapshot.
  Modelo é text-only; get_window_state exige include_screenshot:false. Capturas
  não são registradas; imagens inesperadas e JSON/base64 em text blocks são
  rejeitados antes dos recibos. Sem live view/canvas/video/remote input nesta fase.
- Local CUA usa permissões reais do usuário/OS; não herda confinamento os.Root e
  não oferece sandbox. Ambiente operacional explícito; nunca passar secrets de
  DAIMON/OpenAI/Groq/Anthropic/OpenRouter ou CUA_DRIVER_* de modo/bypass ao processo.
- Crash/falha de CUA é controlado, marca backend unavailable e não derruba DAIMON.
  Fechar HTTP → media/input authority → Sessions → Computer bindings → MCP
  subprocessos, aguardando cleanup.
- GET /computers e /computers/{id} somente metadados; nenhuma action API. UI mostra
  status, perfil/catálogo e aprovação individual; não instala nem dirige desktop.
- Testes offline com fake CUA subprocesso real; nenhum teste automático usa desktop,
  rede externa ou credenciais. Preservar testes Linux/symlink/race e UI existentes.
- Persistent Environments, Intelligent
  Memory, Subagents e External Agents são futuros explícitos. Não antecipar.

## Live Computer View + Human Takeover — Phase 13

- Preserve the original action/Policy/Approval plane. Media and human input use
  separate same-origin WebSockets, never Session SSE/history/Memory.
- ComputerManager owns exclusive input. ViewSession is independent from Session;
  all viewers observe, one explicit human lease may dispatch input.
- Take drains current agent input while blocking new input; Give Back requires
  confirmed media revocation. Failed close remains fail-closed. Observations and
  noncomputer tools continue. Session completion revokes human authority.
- Optional explicit loopback CUA media URL and server-only token; no provisioning,
  installation, fallback, arbitrary proxy, public access or browser root secret.
- Strict bounded RCDP input, capability scopes, frame validation/keyframes and
  backend lease expiry. No automatic input retry or control restore on reload.
- Protocol-only original client; do not copy FSL spacesd/HTML5 code. Presence and
  shared cursors are absent. See docs/COMPUTER_VIEW_V1.md.
- Phase 14 Sandboxed Computers and Phase 15 Cloud Computers are implemented below.

## Sandbox Computers — Phase 14

- SandboxManager owns isolation lifecycle; ComputerManager owns capabilities/media/input.
- A Session owns its frozen disposable binding. Sandbox mode never falls back to host.
- Only explicit local Linux/gVisor profiles and safe aliases/presets are accepted.
- gVisor and runc are different boundaries; runc/QEMU are unsupported in V1.
- Persist intention before create; cleanup partial creation on abort or failure.
- Revoke media/control before deletion. Failed deletion stays tracked for reconciliation.
- Only exact registry-owned references may be auto-cleaned; never scan-delete a prefix.
- No arbitrary image, command, env or runtime from the browser; no provider secrets in CUA.
- No host workspace mounts, automatic runtime setup, pools or persistent guest data.
- Sandbox contents disappear on cleanup. Local services remain reachable over outbound network.
- See docs/SANDBOX_COMPUTERS_V1.md for limits and recovery guarantees.

## Cloud Computers — Phase 15

- Cloud is Sandbox placement, never a new AgentLoop or Computer authority domain.
- Legacy missing placement remains local; cua-cloud requires explicit cloud.
- Only official Fleet, Linux gVisor and fixed small/medium resource presets.
- Explicit embedded CLI, bounded claim TTL, --no-warm, managed pool max 2.
- Reserve at most two cloud guests before effects; unresolved cleanup retains quota.
- Exact journal-owned qualified refs only; auth/transport failure never means gone.
- External process credentials only; no browser login or secret persistence/upload.
- No host/local fallback, workspace mounts/sync/transfer or guest credentials.
- Existing action approval/media/takeover gates; TLS official gateway only, no redirects.
- Cost visible before Send. Managed pool billing may continue until Fleet GC.
- Offline tests/smokes only by default; real paid smoke needs operator opt-in.
- See docs/CLOUD_COMPUTERS_V1.md. Phase 16 is implemented below; Phase 17 Background Tasks
  is the next recommended phase, not implemented in this cut.


## Persistent Environments — Phase 16

See [Persistent Environments v1](docs/PERSISTENT_ENVIRONMENTS_V1.md) for durable Thread-owned local workspace
revisions, bounded official filesystem transfer, expected-revision commits,
explicit enable/delete, hydration before model, successful sync before compute
cleanup and assistant persistence. Existing Threads are not automatically imported.
Session failures/abort before commit preserve the prior revision; an already
confirmed workspace commit survives later cleanup/persistence failure or abort and
is reported explicitly. Compute remains disposable; no persistent processes.

Authorized inspection exception: a fixed DAIMON helper may run in the guest only
through official ProcessService, solely to verify metadata absent from the filesystem
API (inode/device/hard-link count). Fixed executable/code/arguments; no user/model
command strings, shell, arbitrary command tool, stdin or content transfer. Structured
bounded output, timeout and cancellation/kill-on-disconnect are required. Unsupported
or unproven metadata fails closed. All content transfer uses FilesystemService.
No paid CUA smoke, installation or login is automatic. Phase 17 Background Tasks +
Long-Running Agent Runs, Intelligent Memory, Subagents and External Agents stay deferred.


## Product UI V2 — Phase 16.5

- Chat é a superfície principal; navegação por Bot e Conversa. Thread, Session,
  IDs, caminhos internos, códigos de erro e fases não dominam a interface principal.
- Textos de produto em PT-BR; manter catálogos em ui/src/i18n e tokens em
  ui/src/design. Preservar conteúdo do usuário, código e previews reversíveis exatos.
- Configurações e detalhes deliberadamente expandidos preservam as capacidades
  existentes. Não inventar catálogo de modelos, disponibilidade ou permissões.
- Aprovações continuam acessíveis em todas as abas, individuais e sem concessão
  implícita. Não truncar preview, alterar DTOs, autoridade ou comportamento do loop.
- Markdown usa nós React seguros, sem HTML arbitrário. Não simular streaming.
- Memória é CRUD explícito; Arquivos mostra metadados e ações explícitas do ambiente.
- Não persistir segredos, frames, tickets ou controle no navegador. Seleção por hash
  não concede acesso. Configuração de nuvem confirma custo, nunca aprova ferramentas.
- Preservar foco de modais, navegação por teclado, foco visível, redução de movimento
  e layout sem overflow do documento. Menus fecham ao selecionar uma ação.
- Build estático deve terminar antes de compilar Go. Manter testes anteriores e
  executar testes de interface e smokes reais com fixtures offline; sem CUA pago.
