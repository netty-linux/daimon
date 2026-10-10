import {Icon} from './design/Icon';
import { copy } from './i18n/copy';

import {EnvironmentPanel} from './components/EnvironmentPanel';
import {RoutinePanel} from './components/RoutinePanel';
import { useEffect, useState } from 'react';
import { api, safeError } from './api/client';
import { bytes, eventLabels, newID, terminal, type Bot, type ProviderSummary, type SessionSnapshot, type SessionStatus, type Thread } from './api/types';
import { BotEditor, ErrorMessage, Modal, ThreadEditor } from './components/Editors';
import { useSessionEvents } from './hooks/useSessionEvents';
import { useConversation } from './hooks/useConversation';
import { ApprovalPanel } from './components/ApprovalPanel';
import {MemoryPanel} from './components/MemoryPanel';


import { ComputerViewer } from './components/ComputerViewer';
import { pt, statusLabels, resolutionMessages } from './i18n/pt-BR';
import { WorkspaceNavigation } from './features/navigation/WorkspaceNavigation';
import { penCopy } from './i18n/pen';
import { Chat, Composer } from './features/chat/Chat';
import { Activity } from './features/activity/Activity';
import { Settings, type SettingsSection } from './features/settings/Settings';
import { ProductTabs } from './components/ProductTabs';

type MainTab = 'chat' | 'computer' | 'files' | 'memory' | 'activity';
const tabs = [{id:'chat',label:pt.chat},{id:'computer',label:pt.computer},{id:'files',label:pt.files},{id:'memory',label:pt.memory},{id:'activity',label:pt.activity}] as const;
const hashState = () => new URLSearchParams(location.hash.slice(1));
const hashTab = (): MainTab => { const value = hashState().get('tab'); return tabs.some(t=>t.id===value) ? value as MainTab : 'chat'; };

export function StatusBadge({ status }: { status: SessionStatus }) { return <span className={'badge '+status}>{statusLabels[status]}</span>; }
type Run = { snapshot: SessionSnapshot; message: string };
type Editor = { kind: 'bot'; bot?: Bot } | { kind: 'thread'; thread?: Thread };

export function App() {
 const [tab,setTab]=useState<MainTab>(hashTab);
 const [settings,setSettings]=useState<SettingsSection>();
 const [toast,setToast]=useState('');
 useEffect(()=>{if(!toast)return;const timer=setTimeout(()=>setToast(''),4000);return()=>clearTimeout(timer);},[toast]);
 const [botsCollapsed,setBotsCollapsed]=useState(false),[conversationsCollapsed,setConversationsCollapsed]=useState(false);
  const [bots, setBots] = useState<Bot[]>([]), [threads, setThreads] = useState<Thread[]>([]), [providers, setProviders] = useState<ProviderSummary[]>([]);
  const [loading, setLoading] = useState(true), [botError, setBotError] = useState(''), [threadError, setThreadError] = useState(''), [providerError, setProviderError] = useState('');
  const [botID, setBotID] = useState(''), [threadID, setThreadID] = useState(() => new URLSearchParams(location.hash.slice(1)).get('thread') ?? '');
  const [editor, setEditor] = useState<Editor>(), [deleting, setDeleting] = useState<{ kind: 'bot' | 'thread'; id: string; name: string }>();
  const [deleteBusy, setDeleteBusy] = useState(false), [deleteError, setDeleteError] = useState('');
  const [runs, setRuns] = useState<Record<string, Run>>({}), [draft, setDraft] = useState(''), [sending, setSending] = useState(false), [aborting, setAborting] = useState(false), [runError, setRunError] = useState('');
  const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController(); setLoading(true); setBotError(''); setThreadError(''); setProviderError('');
    void Promise.allSettled([
      api.bots(controller.signal).then(setBots).catch(e => { if (!controller.signal.aborted) setBotError(safeError(e)); }),
      api.threads(controller.signal).then(setThreads).catch(e => { if (!controller.signal.aborted) setThreadError(safeError(e)); }),
      api.providers(controller.signal).then(setProviders).catch(e => { if (!controller.signal.aborted) setProviderError(safeError(e)); }),
    ]).then(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [reload]);
  const bot = bots.find(b => b.id === botID), thread = threads.find(t => t.id === threadID && t.bot_id === botID);
  useEffect(() => {
    if (!loading && !botID && threadID) { const saved = threads.find(t => t.id === threadID); if (saved && bots.some(b => b.id === saved.bot_id)) setBotID(saved.bot_id); }
  }, [loading, botID, threadID, threads, bots]);
  useEffect(() => {
    if (!thread?.id) return;
    const controller = new AbortController();
    void api.activeSession(thread.id, controller.signal).then(snapshot => { if (!controller.signal.aborted && snapshot) setRuns(r => {
      const current = r[snapshot.thread_id];
      if (current && (current.snapshot.id === snapshot.id || Date.parse(current.snapshot.started_at) >= Date.parse(snapshot.started_at))) return r;
      return { ...r, [snapshot.thread_id]: { snapshot, message: '' } };
    }); }).catch(e => { if (!controller.signal.aborted) setRunError(safeError(e)); });
    return () => controller.abort();
  }, [thread?.id]);
  const run = thread ? runs[thread.id] : undefined;
  const stream = useSessionEvents(run?.snapshot.id);
  useEffect(() => {
    if (stream.snapshot && thread) {
      const snapshot = stream.snapshot;
      setRuns(r => r[thread.id]?.snapshot.id === snapshot.id ? { ...r, [thread.id]: { ...r[thread.id], snapshot } } : r);
    }
  }, [stream.snapshot, thread?.id]);
  const snapshot = stream.snapshot?.thread_id === thread?.id ? stream.snapshot : run?.snapshot;
  const conversation = useConversation(thread?.id, snapshot ? `${snapshot.id}:${snapshot.status}:${snapshot.finished_at}` : undefined);
  const active = !!snapshot && !terminal(snapshot.status);
  const missingProvider = !!bot && !loading && !providers.some(p => p.id === bot.provider_id);
  const selectBot = (id: string) => { history.replaceState(null, '', location.pathname + location.search); setBotID(id); setThreadID(''); setDraft(''); setRunError(''); setSettings(undefined); setTab('chat'); };
  const selectThread = (id: string) => { history.replaceState(null, '', `#thread=${encodeURIComponent(id)}`); setThreadID(id); setDraft(''); setRunError(''); setSettings(undefined); setTab('chat'); };
  const send = async () => {
    if (!thread || sending || active) return;
    if (!draft.trim() || bytes(draft) > 32768) { setRunError(pt.messageLimit); return; }
    const target = thread.id, message = draft, id = newID('session'); setSending(true); setRunError('');
    try {
      let accepted: SessionSnapshot;
      try { accepted = await api.start(id, target, message, newID('message')); }
      catch (error) {
        // A failed response can follow admission. Recover only this same identity;
        // never retry POST or silently create another run.
        try { accepted = await api.session(id); } catch { throw error; }
      }
      if (accepted.id !== id || accepted.thread_id !== target) throw new Error();
      setRuns(r => ({ ...r, [target]: { snapshot: accepted, message } })); setDraft('');
      conversation.refresh();
    } catch (e) { setRunError(safeError(e)); } finally { setSending(false); }
  };
  const abort = async () => {
    if (!snapshot || aborting) return;
    setAborting(true); setRunError(''); const id = snapshot.id, target = snapshot.thread_id;
    try { await api.abort(id); const current = await api.session(id); setRuns(r => r[target]?.snapshot.id === id ? { ...r, [target]: { ...r[target], snapshot: current } } : r); }
    catch (e) { setRunError(safeError(e)); } finally { setAborting(false); }
  };
  const remove = async () => {
    if (!deleting || deleteBusy) return; setDeleteBusy(true); setDeleteError('');
    try {
      if (deleting.kind === 'bot') { await api.deleteBot(deleting.id); setBots(b => b.filter(x => x.id !== deleting.id)); if (botID === deleting.id) selectBot(''); }
      else { await api.deleteThread(deleting.id); setThreads(t => t.filter(x => x.id !== deleting.id)); if (threadID === deleting.id) selectThread(''); }
      setDeleting(undefined);
    } catch (e) { setDeleteError(safeError(e)); } finally { setDeleteBusy(false); }
  };
  const chooseDelete = (kind: 'bot' | 'thread', id: string, name: string) => { setDeleteError(''); setDeleting({ kind, id, name }); };

  const changeTab = (value: MainTab) => {
    setTab(value); setSettings(undefined);
    const state=hashState(); state.set('tab',value);
    history.replaceState(null,'',location.pathname+location.search+'#'+state.toString());
  };
  useEffect(()=>{
    const restore=()=>{const state=hashState();const id=state.get('thread')??'';setThreadID(id);const saved=threads.find(t=>t.id===id);if(saved)setBotID(saved.bot_id);setTab(hashTab());setSettings(undefined);};
    window.addEventListener('hashchange',restore);return()=>window.removeEventListener('hashchange',restore);
  },[threads]);
  const openSettings=(section:SettingsSection='general')=>setSettings(section);
  const computerID=bot?.sandbox_profile ? (active?snapshot?.computer?.id:undefined) : active?snapshot?.computer?.id:bot?.computer_profile?.enabled?bot.computer_profile.mcp_server_id:undefined;
  const failure=snapshot?.error_category ? resolutionMessages[snapshot.error_category]??pt.failed : '';
  return <div className={'app-shell'+(botsCollapsed?' bots-collapsed':'')+(conversationsCollapsed?' conversations-collapsed':'')}>
    <header className="topbar"><div className="brand"><strong className="brand-wordmark">{copy["DAIMON"]}</strong></div>
      <div className="navigation-toggles"><button className="icon-button" aria-label={botsCollapsed?pt.expandBots:pt.collapseBots} aria-expanded={!botsCollapsed} onClick={()=>setBotsCollapsed(v=>!v)}><Icon name="menu"/></button>
        <button className="icon-button" aria-label={conversationsCollapsed?pt.expandConversations:pt.collapseConversations} aria-expanded={!conversationsCollapsed} onClick={()=>setConversationsCollapsed(v=>!v)}><Icon name="conversations"/></button></div>
      <span className="server-state">{copy["● Execução local"]}</span><button className="icon-button" aria-label={pt.settings} onClick={()=>openSettings()}><Icon name="settings"/></button>
    </header>
    <main className="workspace">
      {!(botsCollapsed&&conversationsCollapsed)&&<WorkspaceNavigation hideBots={botsCollapsed} hideConversations={conversationsCollapsed} bots={{bots,selected:botID,loading,error:botError,canCreate:!loading&&!providerError&&providers.length>0,
        onSelect:selectBot,onCreate:()=>setEditor({kind:'bot'}),onSettings:()=>openSettings(),onMemory:()=>changeTab('memory'),onComputers:()=>openSettings('computer'),onEdit:bot=>setEditor({kind:'bot',bot}),onDelete:bot=>chooseDelete('bot',bot.id,bot.name)}}
        conversations={{threads,bot,selected:thread?.id??'',loading,error:threadError,onSelect:selectThread,onCreate:()=>setEditor({kind:'thread'}),onEdit:thread=>setEditor({kind:'thread',thread}),onDelete:thread=>chooseDelete('thread',thread.id,thread.title||pt.unnamed)}}/>}
      <section className="console" aria-label={pt.chat}>
        <header className="console-heading"><div><h2>{bot?.name??copy["DAIMON"]}</h2><p>{thread?.title??pt.welcomeText}</p></div>
          <div className="header-actions">{snapshot?<StatusBadge status={snapshot.status}/>:<span className="badge">● {pt.available}</span>}
            {bot&&<details className="header-menu"><summary aria-label={copy["Ações do bot"]}><Icon name="more"/></summary><div><button onClick={event=>{event.currentTarget.closest('details')?.removeAttribute('open');setEditor({kind:'bot',bot});}}>{pt.editBot}</button><button onClick={event=>{event.currentTarget.closest('details')?.removeAttribute('open');setEditor({kind:'thread'});}}>{pt.newConversation}</button><button onClick={event=>{event.currentTarget.closest('details')?.removeAttribute('open');openSettings();}}>{pt.settings}</button><button className="danger-text" onClick={event=>{event.currentTarget.closest('details')?.removeAttribute('open');chooseDelete('bot',bot.id,bot.name);}}>{pt.deleteBot}</button></div></details>}
          </div></header>
        {!settings&&<ProductTabs id="main" label="Recursos da conversa" items={tabs} value={tab} onChange={changeTab}/>}
        {stream.connection==='Reconnecting'&&<p className="connection-notice" role="status">{pt.reconnecting}</p>}
        <ErrorMessage message={stream.error}/>{stream.notice&&<p className="notice" role="status">{stream.notice}</p>}
        {failure&&<div className="execution-error"><ErrorMessage message={failure}/>{snapshot?.status==='failed'&&<button disabled={active} onClick={()=>{setDraft(run?.message??'');changeTab('chat');}}>{pt.retry}</button>}<button onClick={()=>changeTab('activity')}>{pt.details}</button></div>}
        {settings?<Settings key={settings} initial={settings} providers={providers} bot={bot} onEdit={()=>{if(bot)setEditor({kind:'bot',bot});}} onClose={()=>setSettings(undefined)} onRefresh={()=>setReload(n=>n+1)}/>:
          <div className={'main-content'+(tab==='chat'&&!!computerID?' with-computer':'')} role="tabpanel" id={'main-panel-'+tab} aria-labelledby={'main-tab-'+tab} tabIndex={0}>
            {tab==='chat'&&<Chat bot={bot} thread={thread} messages={conversation.messages} loading={conversation.loading} error={conversation.error} noBots={!loading&&!bots.length}
              onCreateBot={()=>{if(providers.length)setEditor({kind:'bot'});else openSettings('models');}} onCreateConversation={()=>setEditor({kind:'thread'})}/>}
            {tab==='chat'&&computerID&&<aside className="computer-companion"><ComputerViewer computerID={computerID} onNotice={setToast}/><button onClick={()=>changeTab('computer')}>{pt.computer}</button></aside>}
            {tab==='computer'&&<div className="feature-surface"><ComputerViewer computerID={computerID} onNotice={setToast}/>{bot?.sandbox_profile&&!active&&<p className="hint">{copy["O computador isolado é preparado ao enviar uma tarefa."]}</p>}<details><summary>{pt.details}</summary><p>{bot?.sandbox_profile?.placement==='cloud'?copy["Nuvem"]:bot?.sandbox_profile?'Isolado local':bot?.computer_profile?.enabled?'Computador local':'Desativado'}</p><button onClick={()=>openSettings('computer')}>{pt.settings}</button></details></div>}
            {tab==='files'&&<div className="feature-surface">{thread?<EnvironmentPanel key={thread.id} threadID={thread.id} active={active} sandboxEnabled={!!bot?.sandbox_profile} snapshot={snapshot} cloud={bot?.sandbox_profile?.placement==='cloud'} onNotice={setToast}/>:<p className="empty">{pt.chooseHint}</p>}</div>}
            {tab==='memory'&&<MemoryPanel bots={bots} threads={threads} threadID={thread?.id} botID={bot?.id} inline onNotice={setToast} onClose={()=>changeTab('chat')}/>}
            {tab==='activity'&&<><RoutinePanel botID={bot?.id} threads={threads} onOpen={(target,id)=>{selectThread(target);void api.session(id).then(snapshot=>setRuns(r=>({...r,[target]:{snapshot,message:''}}))).catch(e=>setRunError(safeError(e)));}}/><Activity events={stream.events} snapshot={snapshot}/></>}
          </div>}
        <ApprovalPanel snapshot={snapshot} onAbort={()=>void abort()}/>
        {!settings&&tab==='chat'&&<>
          {snapshot?.status==='waiting_approval'&&<p className="pending-composer" role="status">{penCopy.pendingComposer}</p>}
          {bot?.sandbox_profile?.placement==='cloud'&&<details className="cloud-disclosure"><summary>Nuvem · Pode gerar cobrança</summary><p>{pt.cloudCost}</p></details>}
          {missingProvider&&<div className="model-notice"><ErrorMessage message={pt.missingModel}/><button onClick={()=>openSettings('models')}>{pt.configureModel}</button></div>}
          {!loading&&!providers.length&&<div className="model-notice"><p>{pt.noModel}</p><button onClick={()=>openSettings('models')}>{pt.configureModel}</button></div>}
          <Composer draft={draft} onDraft={setDraft} disabled={!thread||active||sending} canSend={!!thread&&!active&&!sending&&!missingProvider&&!providerError&&!!draft.trim()} active={active} sending={sending} aborting={aborting} error={runError} onSend={()=>void send()} onAbort={()=>void abort()}/>
        </>}
      </section>
    </main>{toast&&<div className="product-toast" role="status">{toast}<button className="icon-button" aria-label={pt.dismissNotice} onClick={()=>setToast('')}>×</button></div>}
    {editor?.kind==='bot'&&<BotEditor bot={editor.bot} providers={providers} onClose={()=>setEditor(undefined)} onSave={b=>{setToast(editor.bot?copy["Bot atualizado"]:copy["Bot criado"]);setBots(list=>[...list.filter(x=>x.id!==b.id),b].sort((a,b)=>a.id.localeCompare(b.id)));selectBot(b.id);setEditor(undefined);}}/>}
    {editor?.kind==='thread'&&bot&&<ThreadEditor thread={editor.thread} bot={bot} onClose={()=>setEditor(undefined)} onSave={t=>{setToast(editor.thread?copy["Conversa atualizada"]:copy["Conversa criada"]);setThreads(list=>[...list.filter(x=>x.id!==t.id),t]);selectThread(t.id);setEditor(undefined);}}/>}
    {deleting&&<Modal title={deleting.kind==='bot'?pt.deleteBot:pt.deleteConversation} onClose={()=>{if(!deleteBusy)setDeleting(undefined);}}><p>{pt.removeQuestion(deleting.name)}</p><p className="hint">{pt.deleteHint}</p><ErrorMessage message={deleteError}/><footer><button disabled={deleteBusy} onClick={()=>setDeleting(undefined)}>{pt.cancel}</button><button className="danger-text" disabled={deleteBusy} onClick={()=>void remove()}>{deleteBusy?pt.loading:pt.confirmDelete}</button></footer></Modal>}
  </div>;
}
