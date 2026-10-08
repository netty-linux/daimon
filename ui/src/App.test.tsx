import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { App, StatusBadge } from './App';
import { api } from './api/client';
import { statuses, type Bot, type Thread } from './api/types';

const bot: Bot = { id: 'bot-a', name: '<b>Coder</b>', description: '', provider_id: 'openai', model: 'test', tools: [], permission_mode: 'ask' };
const now = '2026-10-07T12:00:00Z';
const thread: Thread = { id: 'thread-a', bot_id: 'bot-a', workspace: '/workspace', title: 'Inspect code', created_at: now, updated_at: now };
beforeEach(() => { history.replaceState(null, '', '/'); vi.spyOn(api, 'activeSession').mockResolvedValue(undefined); vi.spyOn(api, 'messages').mockResolvedValue({ messages: [], next_after: 0, has_more: false }); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it('renders lists as escaped text and clears Thread selection when changing Bots', async () => {
  vi.spyOn(api, 'bots').mockResolvedValue([bot, { ...bot, id: 'bot-b', name: 'Research' }]);
  vi.spyOn(api, 'threads').mockResolvedValue([thread, { ...thread, id: 'thread-b', bot_id: 'bot-b', title: 'Research task' }]);
  vi.spyOn(api, 'providers').mockResolvedValue([{ id: 'openai' }]);
  render(<App />); await screen.findByText('<b>Coder</b>');
  expect(document.querySelector('.item-copy b')).toBeNull();
  fireEvent.click(screen.getByText('<b>Coder</b>')); fireEvent.click(screen.getByText('Inspect code'));
  expect(screen.getByLabelText("Mensagem").hasAttribute('disabled')).toBe(false);
  fireEvent.click(screen.getByText('Research')); expect(screen.queryByText('Inspect code')).toBeNull();
  expect(screen.getByLabelText("Mensagem").hasAttribute('disabled')).toBe(true); expect(screen.getByText('Research task')).toBeTruthy();
});
it('shows safe loading, empty and classified failure states', async () => {
  vi.spyOn(api, 'bots').mockResolvedValue([]); vi.spyOn(api, 'threads').mockResolvedValue([]); vi.spyOn(api, 'providers').mockResolvedValue([]);
  render(<App />); expect(screen.getAllByText("Carregando…")[0]).toBeTruthy(); await screen.findByText("Crie seu primeiro bot");
  expect(screen.getAllByRole('tab').map(t=>t.textContent)).toEqual(['Chat','Computador','Arquivos','Memória','Atividade']);
  expect(screen.getByText("Nenhum modelo configurado.")).toBeTruthy(); expect(screen.getByText("Bem-vindo ao DAIMON")).toBeTruthy();
});
it('labels every real Session state without relying on color', async () => {
  render(<>{statuses.map(status => <StatusBadge key={status} status={status} />)}</>);
  await waitFor(() => expect(screen.getByText("Aguardando aprovação")).toBeTruthy());
  expect(document.querySelectorAll('.badge')).toHaveLength(6);
});
it('renders saved user/assistant text and keeps runtime activity separate', async () => {
  vi.spyOn(api, 'bots').mockResolvedValue([bot]); vi.spyOn(api, 'threads').mockResolvedValue([thread]); vi.spyOn(api, 'providers').mockResolvedValue([{ id: 'openai' }]);
  vi.mocked(api.messages).mockResolvedValue({ messages: [{ id: 'user-a', thread_id: thread.id, sequence: 1, role: 'user', content: 'Saved question', created_at: now, session_id: 'session-a' }, { id: 'assistant-a', thread_id: thread.id, sequence: 2, role: 'assistant', content: '<script>saved answer</script>', created_at: now, session_id: 'session-a' }], next_after: 2, has_more: false });
  render(<App />); fireEvent.click(await screen.findByText(bot.name)); fireEvent.click(screen.getByText(thread.title));
  await screen.findByText('Saved question'); expect(screen.getByText('<script>saved answer</script>')).toBeTruthy();
  expect(document.querySelector('.transcript script')).toBeNull(); expect(screen.queryByText("EVENTOS")).toBeNull();
  expect(document.querySelectorAll('.chat-message.user')).toHaveLength(1); expect(document.querySelectorAll('.chat-message.assistant')).toHaveLength(1);
});
