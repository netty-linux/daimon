import { copy } from '../../i18n/copy';
import { useState } from 'react';
import { eventLabels, type SessionEvent, type SessionSnapshot } from '../../api/types';
import { pt } from '../../i18n/pt-BR';

export function Activity({ events, snapshot }: { events: SessionEvent[]; snapshot?: SessionSnapshot }) {
  const [technical, setTechnical] = useState(false);
  return <section className="activity-surface"><h2>{pt.activity}</h2>{!events.length && <p className="empty">{pt.noActivity}</p>}
    <ol className="events">{events.map(event => <li key={event.sequence}><span className="timeline-dot" aria-hidden="true" /><span>{eventLabels[event.kind]}</span>{technical && <small>{event.kind} · {event.sequence} · {event.step}/{event.tool_index}</small>}</li>)}</ol>
    <label className="toggle-label"><input type="checkbox" checked={technical} onChange={event => setTechnical(event.target.checked)} />{pt.technical}</label>
    {technical && snapshot && <p className="hint">{snapshot.steps} {copy["passos ·"]}{' '}{snapshot.tool_calls} {copy["tentativas ·"]}{' '}{snapshot.truncated_tool_results} {copy["resultados limitados"]}</p>}
  </section>;
}
