import { penCopy } from '../i18n/pen';
import {label} from '../i18n/pt-BR';
import { copy } from '../i18n/copy';
import { useEffect, useRef, useState } from 'react';
import { api, safeError } from '../api/client';
import type { ApprovalPresentation, SessionSnapshot } from '../api/types';
import { ErrorMessage, Modal } from './Editors';

export function ApprovalPanel({ snapshot, onAbort }: { snapshot?: SessionSnapshot; onAbort: () => void }) {
  const [presentation, setPresentation] = useState<ApprovalPresentation>();
  const [error, setError] = useState(''), [busy, setBusy] = useState(false), [dismissed, setDismissed] = useState(false), [revision, setRevision] = useState(0);
  const generation = useRef(0);
  useEffect(() => {
    const controller = new AbortController(); const current = ++generation.current;
    setPresentation(undefined); setDismissed(false); setBusy(false); setError('');
    if (snapshot?.status === 'waiting_approval') {
      void api.approval(snapshot.id, controller.signal).then(p => { if (generation.current === current && !controller.signal.aborted) setPresentation(p); }).catch(e => { if (!controller.signal.aborted) setError(safeError(e)); });
    }
    return () => controller.abort();
  }, [snapshot?.id, snapshot?.status, snapshot?.last_event_sequence, revision]);
  const decide = async (decision: 'allow' | 'deny') => {
    if (!presentation || busy) return;
    const current = generation.current, p = presentation; setBusy(true); setError('');
    try {
      await api.decideApproval(p.session_id, p.id, decision);
      if (current === generation.current) setPresentation(undefined);
    } catch (e) {
      if (current !== generation.current) return;
      setError(safeError(e));
      // A lost response or another tab may have consumed the approval. Never
      // retry the decision; re-read the authoritative pending presentation.
      try { const pending = await api.approval(p.session_id); if (current === generation.current) setPresentation(pending); }
      catch { /* retain the safe error and original deliberate review */ }
    } finally { if (current === generation.current) setBusy(false); }
  };
  return <><ErrorMessage message={error} />{snapshot?.status === 'waiting_approval' && !presentation && <button onClick={() => setRevision(r => r + 1)}>{copy["Refresh pending approval"]}</button>}{presentation && (dismissed ? <section className="pending-approval" aria-label={penCopy.pending}><h3>{penCopy.pending}</h3><p>{penCopy.pendingHint}</p><button onClick={() => setDismissed(false)}>{copy["Review pending action"]}</button></section> : <Modal title={copy["Human approval required"]} onClose={() => { if (!busy) setDismissed(true); }}>
    <details><summary>{copy["Detalhes da ação"]}</summary><p className="eyebrow">{label(presentation.kind)} / {presentation.tool}</p>{presentation.computer_id && <p>{presentation.backend==='cua-cloud'?copy["CLOUD Computer"]:presentation.computer_id.startsWith('sandbox-')?copy["Sandbox Computer"]:copy["Local Computer"]}: {presentation.computer_id} · {presentation.backend==='cua-cloud'?'CUA Fleet':'CUA Driver'} · {label(presentation.classification??'other')}</p>}{presentation.server_id && !presentation.computer_id && <p>{copy["External MCP server:"]} {presentation.server_id} {copy["· Classification:"]} {label(presentation.classification??'other')}</p>}</details><h3>{copy["Target"]}</h3><pre className="approval-target">{presentation.target}</pre>
    <p className="notice">{presentation.warning}</p>{presentation.preview && <><h3>{presentation.kind==='computer'?copy["Exact text to type"]:copy["Complete contract preview"]}</h3><pre className="approval-preview">{presentation.preview}</pre></>}
    <p className="hint">{copy["This decision applies once to this exact action. Closing this display keeps the Session waiting."]}</p><ErrorMessage message={error} />
    <footer><button disabled={busy} onClick={onAbort}>{copy["Abort waiting Session"]}</button><button disabled={busy} onClick={() => void decide('deny')}>{copy["Deny"]}</button><button className="primary" disabled={busy} onClick={() => void decide('allow')}>{busy ? copy["Submitting…"] : copy["Allow once"]}</button></footer>
  </Modal>)}</>;
}
