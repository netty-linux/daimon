import { useEffect, useState } from 'react';
import { api, APIError, safeError } from '../api/client';
import { bytes, type ConversationMessage } from '../api/types';

// Fetch incremental pages, bounded by the store's 1024-message/16-MiB limits.
// Thread changes cancel old requests; runtime events never become chat messages.
export function useConversation(threadId?: string, revision?: string) {
  const [state, setState] = useState<{ threadId?: string; messages: ConversationMessage[]; loading: boolean; error: string }>({ messages: [], loading: false, error: '' });
  const [refreshToken, setRefreshToken] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    if (!threadId) { setState({ messages: [], loading: false, error: '' }); return () => controller.abort(); }
    setState(s => ({ ...s, threadId, messages: s.threadId !== threadId ? [] : s.messages, loading: true, error: '' }));
    void (async () => {
      const messages: ConversationMessage[] = []; let cursor = 0, totalBytes = 0;
      for (let index = 0; index < 1024; index++) {
        const page = await api.messages(threadId, cursor, controller.signal);
        for (const message of page.messages) { totalBytes += bytes(message.content); messages.push(message); }
        if (messages.length > 1024 || totalBytes > 16 * 1024 * 1024) throw new APIError('invalid_response');
        cursor = page.next_after;
        if (!page.has_more) { if (!controller.signal.aborted) setState({ threadId, messages, loading: false, error: '' }); return; }
      }
      throw new APIError('invalid_response');
    })().catch(e => { if (!controller.signal.aborted) setState(s => ({ ...s, loading: false, error: safeError(e) })); });
    return () => controller.abort();
  }, [threadId, revision, refreshToken]);
  return { ...state, messages: state.threadId === threadId ? state.messages : [], refresh: () => setRefreshToken(t => t + 1) };
}
