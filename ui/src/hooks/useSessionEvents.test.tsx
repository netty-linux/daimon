import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { sessionFixture } from '../test-fixtures';
import type { SessionSnapshot } from '../api/types';
import { useSessionEvents } from './useSessionEvents';

class FakeSource extends EventTarget {
  onopen: (() => void) | null = null; onerror: (() => void) | null = null;
  close = vi.fn();
  emit(kind: string, value: unknown, id = '') { this.dispatchEvent(new MessageEvent(kind, { data: JSON.stringify(value), lastEventId: id })); }
}
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });
const snapshot = sessionFixture as SessionSnapshot;
it('maps real events, deduplicates replay, refreshes gaps and closes on end', async () => {
  const get = vi.spyOn(api, 'session').mockResolvedValue(snapshot); const source = new FakeSource();
  const factory = () => source as unknown as EventSource;
  const { result, unmount } = renderHook(() => useSessionEvents('session-a', factory));
  await waitFor(() => expect(result.current.snapshot?.status).toBe('running'));
  const event = { sequence: 1, kind: 'model_requested', step: 1, tool_index: 0, stop_reason: '' };
  act(() => { source.emit('model_requested', event, '1'); source.emit('model_requested', event, '1'); });
  expect(result.current.events).toHaveLength(1); expect(result.current.lastEventId).toBe('1');
  act(() => source.emit('replay_gap', { requested_after: 1, oldest_available: 3, latest_available: 4 }));
  await waitFor(() => expect(get.mock.calls.length).toBeGreaterThan(1)); expect(result.current.notice).toContain('não está mais disponível');
  get.mockResolvedValue({ ...snapshot, status: 'completed' });
  act(() => source.emit('stream_end', { status: 'completed', last_sequence: 4 }));
  await waitFor(() => expect(result.current.snapshot?.status).toBe('completed'));
  expect(source.close).toHaveBeenCalled(); expect(result.current.connection).toBe('Closed'); unmount();
});
it('closes old observation on selection change and unmount without aborting runtime', async () => {
  const get = vi.spyOn(api, 'session').mockImplementation(async id => ({ ...snapshot, id }));
  const abort = vi.spyOn(api, 'abort'); const sources: FakeSource[] = [];
  const factory = () => { const s = new FakeSource(); sources.push(s); return s as unknown as EventSource; };
  const { rerender, result, unmount } = renderHook(({ id }) => useSessionEvents(id, factory), { initialProps: { id: 'session-a' } });
  await waitFor(() => expect(result.current.snapshot?.id).toBe('session-a'));
  rerender({ id: 'session-b' }); await waitFor(() => expect(result.current.snapshot?.id).toBe('session-b'));
  expect(sources[0].close).toHaveBeenCalled(); expect(get.mock.calls[0][1]?.aborted).toBe(true);
  unmount(); expect(sources[1].close).toHaveBeenCalled(); expect(abort).not.toHaveBeenCalled();
});
it('polls slowly only after disconnect and stops after terminal snapshot', async () => {
  vi.useFakeTimers(); const get = vi.spyOn(api, 'session').mockResolvedValue(snapshot); const source = new FakeSource(); const factory = () => source as unknown as EventSource;
  const { result } = renderHook(() => useSessionEvents('session-a', factory));
  await act(async () => {}); const initial = get.mock.calls.length;
  await act(async () => { vi.advanceTimersByTime(10000); }); expect(get).toHaveBeenCalledTimes(initial);
  await act(async () => source.onerror?.()); expect(result.current.connection).toBe('Reconnecting');
  get.mockResolvedValue({ ...snapshot, status: 'aborted' }); await act(async () => { vi.advanceTimersByTime(5000); });
  expect(source.close).toHaveBeenCalled(); const count = get.mock.calls.length;
  await act(async () => { vi.advanceTimersByTime(10000); }); expect(get).toHaveBeenCalledTimes(count);
});
it('retries the authoritative final snapshot slowly if GET fails after stream_end', async () => {
  vi.useFakeTimers(); const get = vi.spyOn(api, 'session').mockResolvedValue(snapshot);
  const source = new FakeSource(); const factory = () => source as unknown as EventSource;
  const { result } = renderHook(() => useSessionEvents('session-a', factory));
  await act(async () => {});
  get.mockRejectedValueOnce(new Error('private failure'));
  await act(async () => source.emit('stream_end', { status: 'completed', last_sequence: 0 }));
  expect(result.current.snapshot?.status).toBe('running'); expect(result.current.error).not.toContain('private');
  get.mockResolvedValue({ ...snapshot, status: 'completed' });
  await act(async () => { vi.advanceTimersByTime(5000); });
  expect(result.current.snapshot?.status).toBe('completed');
});
it('bounds displayed events and ignores a late detached stream', async () => {
  vi.spyOn(api, 'session').mockResolvedValue(snapshot);
  const source = new FakeSource(); const factory = () => source as unknown as EventSource;
  const { result, unmount } = renderHook(() => useSessionEvents('session-a', factory));
  await waitFor(() => expect(result.current.snapshot).toBeTruthy());
  act(() => { for (let sequence = 1; sequence <= 260; sequence++) source.emit('model_requested', { sequence, kind: 'model_requested', step: 1, tool_index: 0, stop_reason: '' }, String(sequence)); });
  expect(result.current.events).toHaveLength(256); expect(result.current.events[0].sequence).toBe(5);
  expect(result.current.notice).toContain('256'); unmount();
  act(() => source.emit('model_requested', { sequence: 261, kind: 'model_requested', step: 1, tool_index: 0, stop_reason: '' }, '261'));
  expect(source.close).toHaveBeenCalled();
});
