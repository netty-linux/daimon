import {Icon} from '../../design/Icon';
import { useEffect, useRef, useState } from 'react';
import type { Bot, ConversationMessage, Thread } from '../../api/types';
import { pt } from '../../i18n/pt-BR';
import { ErrorMessage } from '../../components/Editors';
import { Markdown, CopyText } from './Markdown';

export function Chat({ bot, thread, messages, loading, error, noBots, onCreateBot, onCreateConversation }: {
  bot?: Bot; thread?: Thread; messages: ConversationMessage[]; loading: boolean;
  error: string; noBots: boolean; onCreateBot: () => void; onCreateConversation: () => void;
}) {
  const scroller = useRef<HTMLDivElement>(null), follow = useRef(true), last = useRef('');
  const [newMessage, setNewMessage] = useState(false);
  useEffect(() => { follow.current = true; last.current = ''; setNewMessage(false); }, [thread?.id]);
  useEffect(() => {
    const tail = messages.at(-1)?.id ?? '';
    if (tail === last.current) return; last.current = tail;
    if (follow.current && scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight;
    else if (tail) setNewMessage(true);
  }, [messages]);
  return <div className="chat-scroll" ref={scroller} onScroll={() => {
    const node = scroller.current; if (!node) return;
    follow.current = node.scrollHeight - node.scrollTop - node.clientHeight < 100;
    if (follow.current) setNewMessage(false);
  }}>
    <div className="chat-reading"><ErrorMessage message={error} />{loading && <p className="hint" role="status">{pt.loadingConversation}</p>}
      {!thread || (!loading && !messages.length) ? <div className="welcome"><span className="welcome-symbol" aria-hidden="true">◇</span>
        <h2>{noBots ? pt.welcome : thread ? pt.ready : pt.chooseConversation}</h2>
        <p>{noBots ? pt.welcomeText : thread ? pt.readyHint : pt.chooseHint}</p>
        {noBots ? <button className="primary" onClick={onCreateBot}>{pt.firstBot}</button> : bot && !thread ? <button onClick={onCreateConversation}>+ {pt.newConversation}</button> : null}
      </div> : null}
      <div className="transcript" aria-label={pt.transcript}>{messages.map(message => <article key={message.id} className={`chat-message ${message.role}`}>
        <span className="message-author">{message.role === 'user' ? pt.you : bot?.name ?? pt.assistant}</span>
        {message.role === 'assistant' ? <Markdown content={message.content} /> : <p>{message.content}</p>}
        <div className="message-footer"><time dateTime={message.created_at}>{new Date(message.created_at).toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' })}</time>{message.role==='assistant'&&<CopyText content={message.content}/>}</div>
      </article>)}</div>
    </div>{newMessage && <button className="new-message" onClick={() => { if (scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight; follow.current = true; setNewMessage(false); }}>{pt.newMessage} ↓</button>}
  </div>;
}

export function Composer({ draft, onDraft, disabled, canSend, active, sending, aborting, error, onSend, onAbort }: {
  draft: string; onDraft: (value: string) => void; disabled: boolean; canSend: boolean;
  active: boolean; sending: boolean; aborting: boolean; error: string; onSend: () => void; onAbort: () => void;
}) {
  return <div className="composer"><ErrorMessage message={error} /><div className="composer-surface">
    <label className="sr-only" htmlFor="message">{pt.message}</label><textarea id="message" value={draft}
      onChange={event => onDraft(event.target.value)} disabled={disabled} placeholder={pt.composer} rows={2}
      onKeyDown={event => { if ((event.ctrlKey || event.metaKey) && event.key === 'Enter' && canSend && !event.nativeEvent.isComposing) { event.preventDefault(); onSend(); } }} />
    <div className="composer-actions"><span className="hint">{active ? pt.thinking : 'Ctrl / ⌘ + Enter'}</span>
      {active ? <button className="danger-text" disabled={aborting} onClick={onAbort}>{aborting ? pt.stopping : pt.stop} ■</button> : <button className="primary send-button" aria-label={sending ? pt.sending : pt.send} disabled={!canSend} onClick={onSend}><Icon name="send"/></button>}
    </div></div><p className="composer-disclosure">{pt.savedLocally}</p>
  </div>;
}
