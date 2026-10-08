import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { api, APIError } from '../api/client';
import type { ConversationMessage } from '../api/types';
import { useConversation } from './useConversation';
const user: ConversationMessage = { id: 'message-a', thread_id: 'thread-a', sequence: 1, role: 'user', content: 'Question', created_at: '2026-10-07T12:00:00Z', session_id: 'session-a' };
const assistant: ConversationMessage = { ...user, id: 'message-b', sequence: 2, role: 'assistant', content: 'Actual saved answer' };
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it('loads ordered pages and reads the saved transcript again after remount', async () => {
  const get = vi.spyOn(api, 'messages').mockImplementation(async (_, after) => after ? { messages: [assistant], next_after: 2, has_more: false } : { messages: [user], next_after: 1, has_more: true });
  const first = renderHook(() => useConversation('thread-a'));
  await waitFor(() => expect(first.result.current.messages).toHaveLength(2));
  expect(first.result.current.messages[1].content).toBe('Actual saved answer'); first.unmount();
  const second = renderHook(() => useConversation('thread-a'));
  await waitFor(() => expect(second.result.current.messages).toHaveLength(2));
  expect(get).toHaveBeenCalledTimes(4);
});
it('refetches after terminal revision without fabricating assistant messages on failure or abort', async () => {
  const get = vi.spyOn(api, 'messages').mockResolvedValue({ messages: [user], next_after: 1, has_more: false });
  const { result, rerender } = renderHook(({ revision }) => useConversation('thread-a', revision), { initialProps: { revision: 'running' } });
  await waitFor(() => expect(result.current.messages).toHaveLength(1));
  rerender({ revision: 'failed' }); await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  expect(result.current.messages.every(m => m.role === 'user')).toBe(true);
  rerender({ revision: 'aborted' }); await waitFor(() => expect(get).toHaveBeenCalledTimes(3));
  expect(result.current.messages).toHaveLength(1);
  get.mockResolvedValue({ messages: [user, assistant], next_after: 2, has_more: false });
  rerender({ revision: 'completed' }); await waitFor(() => expect(result.current.messages).toHaveLength(2));
});
it('cancels old Thread loads and ignores late replies', async () => {
  let finish: ((value: { messages: ConversationMessage[]; next_after: number; has_more: boolean }) => void) | undefined;
  const get = vi.spyOn(api, 'messages').mockImplementation(id => id === 'thread-a' ? new Promise(resolve => { finish = resolve; }) : Promise.resolve({ messages: [], next_after: 0, has_more: false }));
  const { result, rerender, unmount } = renderHook(({ id }) => useConversation(id), { initialProps: { id: 'thread-a' } });
  rerender({ id: 'thread-b' }); await waitFor(() => expect(result.current.loading).toBe(false));
  expect(get.mock.calls[0][2]?.aborted).toBe(true);
  await act(async () => finish?.({ messages: [user, assistant], next_after: 2, has_more: false }));
  expect(result.current.messages).toHaveLength(0); unmount(); expect(get.mock.calls[1][2]?.aborted).toBe(true);
});
it('shows safe errors and retains the last accepted transcript on refresh failure', async () => {
  const get = vi.spyOn(api, 'messages').mockResolvedValue({ messages: [user], next_after: 1, has_more: false });
  const { result } = renderHook(() => useConversation('thread-a'));
  await waitFor(() => expect(result.current.messages).toHaveLength(1));
  get.mockRejectedValue(new APIError('conversation_unavailable')); act(() => result.current.refresh());
  await waitFor(() => expect(result.current.error).toContain('histórico'));
  expect(result.current.messages).toHaveLength(1);
});
