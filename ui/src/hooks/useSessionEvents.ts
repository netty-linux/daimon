import { useEffect, useState } from 'react';
import { api, APIError, decodeEvent, integer, object, safeError } from '../api/client';
import { eventLabels, terminal, type SessionEvent, type SessionSnapshot } from '../api/types';

export type EventSourceFactory = (url: string) => EventSource;
const nativeSource: EventSourceFactory = url => new EventSource(url);
export function useSessionEvents(sessionId?: string, factory: EventSourceFactory = nativeSource) {
  const [state, setState] = useState<{ snapshot?: SessionSnapshot; events: SessionEvent[]; connection: string; notice: string; error: string; lastEventId: string }>({ events: [], connection: 'Idle', notice: '', error: '', lastEventId: '' });
  useEffect(() => {
    setState({ events: [], connection: sessionId ? 'Connecting' : 'Idle', notice: '', error: '', lastEventId: '' });
    if (!sessionId) return;
    const controller = new AbortController();
    let alive = true, ended = false, last = 0, refreshRunning = false, refreshAgain = false;
    let fallback: ReturnType<typeof setInterval> | undefined;
    let source: EventSource | undefined;
    const stopFallback = () => { if (fallback) clearInterval(fallback); fallback = undefined; };
    const refresh = async () => {
      if (!alive) return;
      if (refreshRunning) { refreshAgain = true; return; }
      refreshRunning = true;
      try {
        const snapshot = await api.session(sessionId, controller.signal);
        if (snapshot.id !== sessionId) throw new APIError('invalid_response');
        if (alive) {
          setState(s => ({ ...s, snapshot, error: '' }));
          // Drain the stream through stream_end on healthy connections; terminal
          // snapshots stop reconnection when transport is unavailable.
          if (terminal(snapshot.status) && (ended || fallback)) { source?.close(); stopFallback(); setState(s => ({ ...s, connection: 'Closed' })); }
        }
      } catch (error) {
        if (alive) {
          setState(s => ({ ...s, error: safeError(error) }));
          if (error instanceof APIError && error.code === 'session_not_found') { ended = true; source?.close(); stopFallback(); setState(s => ({ ...s, connection: 'Unavailable' })); }
          else if (ended && !fallback) { fallback = setInterval(() => { void refresh(); }, 5000); }
        }
      } finally { refreshRunning = false; if (refreshAgain && alive) { refreshAgain = false; void refresh(); } }
    };
    const reconnecting = () => {
      if (!alive || ended) return;
      setState(s => ({ ...s, connection: 'Reconnecting' }));
      if (!fallback) fallback = setInterval(() => { void refresh(); }, 5000);
      void refresh();
    };
    try {
      source = factory(`/api/v1/sessions/${encodeURIComponent(sessionId)}/events/stream`);
      source.onopen = () => { if (alive && !ended) { stopFallback(); setState(s => ({ ...s, connection: 'Live' })); } };
      source.onerror = reconnecting; // Native EventSource retains Last-Event-ID.
      for (const kind of Object.keys(eventLabels)) source.addEventListener(kind, event => {
        if (!alive || ended) return;
        try {
          const frame = event as MessageEvent<string>;
          const value = decodeEvent(JSON.parse(frame.data));
          if (value.kind !== kind || frame.lastEventId !== String(value.sequence)) throw new APIError('invalid_response');
          if (value.sequence <= last) return;
          last = value.sequence;
          setState(s => ({ ...s, events: [...s.events, value].slice(-256), lastEventId: frame.lastEventId, notice: s.events.length >= 256 ? 'A atividade mostra somente os últimos 256 eventos.' : s.notice }));
          if (['loop_started', 'approval_requested', 'approval_granted', 'approval_denied', 'loop_stopped'].includes(kind)) void refresh();
        } catch { ended = true; source?.close(); stopFallback(); setState(s => ({ ...s, error: safeError(new APIError('invalid_response')), connection: 'Closed' })); void refresh(); }
      });
      source.addEventListener('replay_gap', event => {
        if (!alive || ended) return;
        try {
          const gap = object(JSON.parse((event as MessageEvent<string>).data));
          if (!['requested_after', 'oldest_available', 'latest_available'].every(k => integer(gap[k]))) throw new APIError('invalid_response');
          setState(s => ({ ...s, notice: 'Parte da atividade não está mais disponível. Atualizando o estado da execução.' }));
          void refresh();
        } catch { reconnecting(); }
      });
      source.addEventListener('stream_end', event => {
        if (!alive || ended) return;
        try {
          const end = object(JSON.parse((event as MessageEvent<string>).data));
          if (!['completed', 'failed', 'aborted'].includes(String(end.status)) || !integer(end.last_sequence)) throw new APIError('invalid_response');
          ended = true; source?.close(); stopFallback();
          setState(s => ({ ...s, connection: 'Closed' })); void refresh();
        } catch { reconnecting(); }
      });
      source.addEventListener('transport_error', reconnecting);
    } catch { reconnecting(); }
    void refresh();
    return () => { alive = false; controller.abort(); stopFallback(); source?.close(); };
  }, [sessionId, factory]);
  // Never display the previous Session during the render preceding effect cleanup.
  return state.snapshot && state.snapshot.id !== sessionId ? { ...state, snapshot: undefined, events: [] } : state;
}
