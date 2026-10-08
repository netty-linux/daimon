import { copy } from '../../i18n/copy';
import type { Bot, Thread } from '../../api/types';
import { pt } from '../../i18n/pt-BR';
import { ErrorMessage } from '../../components/Editors';
import { Icon } from '../../design/Icon';

export function BotNavigation({ bots, selected, loading, error, canCreate, onSelect, onCreate, onSettings, onMemory, onComputers, onEdit, onDelete }: {
  bots: Bot[]; selected: string; loading: boolean; error: string; canCreate: boolean;
  onSelect: (id: string) => void; onCreate: () => void; onSettings: () => void;
  onMemory: () => void; onComputers: () => void; onEdit:(bot:Bot)=>void;onDelete:(bot:Bot)=>void;
}) {
  return <nav className="panel bots-panel" aria-label={pt.bots}>
    <div className="panel-heading"><h1>{pt.bots}</h1><button className="icon-button" aria-label={pt.newBot} disabled={!canCreate} onClick={onCreate}><Icon name="plus"/></button></div>
    <div className="panel-list"><ErrorMessage message={error} />{loading ? <p className="empty" role="status">{pt.loading}</p> : !bots.length ? <p className="empty">{pt.noBots}</p> : bots.map(bot =>
      <div className="list-row" key={bot.id}><button className={`list-item ${selected === bot.id ? 'selected' : ''}`} aria-pressed={selected === bot.id} onClick={() => onSelect(bot.id)}>
        <span className="avatar" aria-hidden="true">{bot.name.slice(0, 1).toUpperCase()}</span>
        <span className="item-copy"><strong>{bot.name}</strong><small>{bot.model}</small></span>
      </button><details className="item-menu"><summary aria-label={"Ações de "+bot.name}><Icon name="more"/></summary><div><button onClick={e=>{e.currentTarget.closest("details")?.removeAttribute("open");onEdit(bot)}}>{pt.editBot}</button><button className="danger-text" onClick={e=>{e.currentTarget.closest("details")?.removeAttribute("open");onDelete(bot)}}>{pt.deleteBot}</button></div></details></div>)}</div>
    <button className="new-bot" disabled={!canCreate} onClick={onCreate}>+ {pt.newBot}</button>
    <div className="navigation-links"><button onClick={onMemory}><Icon name="memory"/> {pt.memory}</button><button onClick={onComputers}><Icon name="computer"/> Computadores</button></div>
    <button className="settings-link" onClick={onSettings}><Icon name="settings"/> {pt.settings}</button>
  </nav>;
}

export function ConversationNavigation({ threads, bot, selected, loading, error, onSelect, onCreate, onEdit, onDelete }: {
  threads: Thread[]; bot?: Bot; selected: string; loading: boolean; error: string;
  onSelect: (id: string) => void; onCreate: () => void; onEdit: (thread: Thread) => void; onDelete: (thread: Thread) => void;
}) {
  const visible = threads.filter(thread => thread.bot_id === bot?.id);
  return <nav className="panel threads-panel" aria-label={pt.conversations}>
    <div className="panel-heading"><h2>{pt.conversations}</h2><button className="icon-button" aria-label={pt.newConversation} disabled={!bot || loading} onClick={onCreate}><Icon name="plus"/></button></div>
    <p className="panel-subtitle">{bot?.name ?? pt.chooseHint}</p>
    <div className="panel-list"><ErrorMessage message={error} />{loading ? <p className="empty">{pt.loading}</p> : !visible.length ? <div className="empty"><h3>{pt.noConversations}</h3><p>{pt.conversationHint}</p>{bot && <button onClick={onCreate}>+ {pt.newConversation}</button>}</div> : visible.map(thread =>
      <div className="list-row" key={thread.id}><button className={`list-item thread-item ${selected === thread.id ? 'selected' : ''}`} aria-pressed={selected === thread.id} onClick={() => onSelect(thread.id)}>
        <strong>{thread.title || pt.unnamed}</strong><time dateTime={thread.updated_at}>{new Date(thread.updated_at).toLocaleDateString('pt-BR', { day: 'numeric', month: 'short' })}</time>
      </button><details className="item-menu"><summary aria-label={"Ações da conversa "+(thread.title||pt.unnamed)}><Icon name="more"/></summary><div><button onClick={e=>{e.currentTarget.closest("details")?.removeAttribute("open");onEdit(thread)}}>{pt.editConversation}</button><button className="danger-text" onClick={e=>{e.currentTarget.closest("details")?.removeAttribute("open");onDelete(thread)}}>{pt.deleteConversation}</button></div></details></div>)}</div>

  </nav>;
}
