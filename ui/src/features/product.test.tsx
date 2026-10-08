import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { App } from '../App';
import { api, APIError, environmentsAPI } from '../api/client';
import { pt } from '../i18n/pt-BR';
import { sessionFixture } from '../test-fixtures';
import { Markdown } from './chat/Markdown';
import { Chat } from './chat/Chat';
import type { Bot, Thread } from '../api/types';

const bot: Bot = { id: 'bot-product', name: 'Daimon', description: '', provider_id: 'fixture', model: 'offline', tools: [], permission_mode: 'ask' };
const thread: Thread = { id: 'thread-product', bot_id: bot.id, title: 'Meu projeto', workspace: '/private-folder', created_at: '2026-10-08T10:00:00Z', updated_at: '2026-10-08T10:00:00Z' };
beforeEach(() => {
  history.replaceState(null, '', '/#thread='+thread.id);
  Object.defineProperties(HTMLDialogElement.prototype, { showModal: { configurable: true, value() { this.setAttribute('open', ''); } }, close: { configurable: true, value() { this.removeAttribute('open'); } } });
  vi.spyOn(api,'bots').mockResolvedValue([bot]); vi.spyOn(api,'threads').mockResolvedValue([thread]); vi.spyOn(api,'providers').mockResolvedValue([{id:'fixture'}]);
  vi.spyOn(api,'activeSession').mockResolvedValue(undefined); vi.spyOn(api,'messages').mockResolvedValue({messages:[],next_after:0,has_more:false});
  vi.spyOn(api,'computers').mockResolvedValue([]); vi.spyOn(api,'mcpTools').mockResolvedValue([]); vi.spyOn(api,'memories').mockResolvedValue([]);
  vi.spyOn(environmentsAPI,'get').mockRejectedValue(new APIError('environment_not_found'));
  vi.stubGlobal('EventSource', class extends EventTarget { close() {} onopen=null; onerror=null; });
});
afterEach(()=>{cleanup();vi.restoreAllMocks();vi.unstubAllGlobals();});
const ready = async()=>{render(<App/>);await screen.findByRole('button',{name:/Meu projeto/});await waitFor(()=>expect(document.querySelector('.console-heading h2')?.textContent).toBe(bot.name));};

it('keeps infrastructure and raw identifiers off the primary surface',async()=>{
  await ready();expect(screen.queryByText('/private-folder')).toBeNull();expect(screen.queryByText(bot.id)).toBeNull();expect(screen.queryByText('fixture')).toBeNull();
  expect(screen.queryByText('PHASE 13')).toBeNull();expect(environmentsAPI.get).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('tab',{name:pt.files}));await waitFor(()=>expect(environmentsAPI.get).toHaveBeenCalledWith(thread.id,expect.any(AbortSignal)));
});
it('preserves conversation bookmarks and adds keyboard-accessible tab state',async()=>{
  await ready();fireEvent.keyDown(screen.getByRole('tab',{name:pt.chat}),{key:'End'});
  expect(screen.getByRole('tab',{name:pt.activity}).getAttribute('aria-selected')).toBe('true');expect(location.hash).toContain('thread='+thread.id);expect(location.hash).toContain('tab=activity');
  const panel=screen.getByRole('tabpanel');expect(panel.getAttribute('aria-labelledby')).toBe('main-tab-activity');
});
it('restores the selected resource on reload with the old thread hash',async()=>{
  history.replaceState(null,'','/#thread='+thread.id+'&tab=files');await ready();expect(screen.getByRole('tab',{name:pt.files}).getAttribute('aria-selected')).toBe('true');
});
it('opens progressive settings without changing conversation selection',async()=>{
  await ready();fireEvent.click(within(document.querySelector('.topbar') as HTMLElement).getByRole('button',{name:pt.settings}));
  fireEvent.click(screen.getByRole('tab',{name:'Modelos / Provedores'}));expect(screen.getByText('Credenciais e conexão são configuradas fora do navegador.')).toBeTruthy();
  fireEvent.click(screen.getByRole('button',{name:pt.close}));expect((screen.getByLabelText(pt.message) as HTMLTextAreaElement).disabled).toBe(false);
});
it('starts once from the composer and recovers a lost admission response without retry',async()=>{
  const start=vi.spyOn(api,'start').mockRejectedValue(new APIError('network_error'));vi.spyOn(api,'session').mockImplementation(async id=>({...sessionFixture,id,thread_id:thread.id,status:'running'}));
  await ready();fireEvent.change(screen.getByLabelText(pt.message),{target:{value:'Minha tarefa'}});fireEvent.keyDown(screen.getByLabelText(pt.message),{key:'Enter',ctrlKey:true});
  await waitFor(()=>expect(screen.getByRole('button',{name:/Parar/})).toBeTruthy());expect(start).toHaveBeenCalledTimes(1);expect(start.mock.calls[0][1]).toBe(thread.id);
  const abort=vi.spyOn(api,'abort').mockResolvedValue(undefined);fireEvent.click(screen.getByRole('button',{name:/Parar/}));await waitFor(()=>expect(abort).toHaveBeenCalledWith(start.mock.calls[0][0]));
});
it('keeps optional instructions simple on create while preserving the required model contract',async()=>{
  const save=vi.spyOn(api,'saveBot').mockResolvedValue(bot);await ready();fireEvent.click(screen.getByRole('button',{name:pt.newBot}));
  fireEvent.change(screen.getByLabelText('Modelo'),{target:{value:'offline'}});fireEvent.click(screen.getByRole('button',{name:'Salvar bot'}));
  await waitFor(()=>expect(save).toHaveBeenCalled());expect(save.mock.calls[0][0]).toMatchObject({instructions:pt.defaultInstructions,permission_mode:'ask',tools:[]});
});
it('reveals advanced bot sections without submitting the form',async()=>{
  const save=vi.spyOn(api,'saveBot').mockResolvedValue(bot);await ready();fireEvent.click(screen.getByRole('button',{name:pt.newBot}));
  fireEvent.change(screen.getByLabelText('Modelo'),{target:{value:'offline'}});fireEvent.click(within(screen.getByRole('dialog')).getByRole('tab',{name:'Computador'}));
  expect(save).not.toHaveBeenCalled();expect(screen.getByRole('combobox',{name:'Modo do computador'})).toBeTruthy();
});
it('offers an explicit empty computer surface and manual setup',async()=>{
  await ready();fireEvent.click(screen.getByRole('tab',{name:pt.computer}));expect(screen.getByText(pt.noComputer)).toBeTruthy();expect(screen.queryByRole('button',{name:'Assumir controle'})?.hasAttribute('disabled')).toBe(true);
});
it('renders markdown without executable HTML or links and preserves code',()=>{
  render(<Markdown content={'# Título\n**forte** e `inline`\n<script>alert(1)</script>\n[x](javascript:alert)\n[docs](https://example.com/docs)\n```go\nfmt.Println("<safe>")\n```'}/>);
  expect(document.querySelector('script')).toBeNull();expect(document.querySelector('a[href^="javascript"]')).toBeNull();expect(screen.getByRole('link',{name:'docs'}).getAttribute('rel')).toContain('noopener');expect(screen.getByText('fmt.Println("<safe>")')).toBeTruthy();
});
it('retains a reader’s scroll position and offers a new message jump',()=>{
  const message={id:'message-a',thread_id:thread.id,sequence:1,role:'assistant' as const,content:'A',created_at:thread.created_at,session_id:'session-a'};
  const props={bot,thread,messages:[message],loading:false,error:'',noBots:false,onCreateBot:()=>{},onCreateConversation:()=>{}};
  const view=render(<Chat {...props}/>);const scroller=document.querySelector('.chat-scroll')!;
  Object.defineProperties(scroller,{scrollHeight:{configurable:true,value:1000},clientHeight:{configurable:true,value:200}});scroller.scrollTop=10;fireEvent.scroll(scroller);
  view.rerender(<Chat {...props} messages={[message,{...message,id:'message-b',sequence:2,content:'B'}]}/>);expect(scroller.scrollTop).toBe(10);fireEvent.click(screen.getByRole('button',{name:/Nova mensagem/}));expect(scroller.scrollTop).toBe(1000);
});


it('requires a separate cost confirmation before saving first cloud configuration',async()=>{
  const save=vi.spyOn(api,'saveBot').mockResolvedValue(bot);await ready();fireEvent.click(screen.getByRole('button',{name:pt.newBot}));
  fireEvent.change(screen.getByLabelText('Modelo'),{target:{value:'offline'}});fireEvent.click(within(screen.getByRole('dialog')).getByRole('tab',{name:'Computador'}));
  fireEvent.change(screen.getByRole('combobox',{name:'Modo do computador'}),{target:{value:'cloud'}});fireEvent.click(screen.getByRole('button',{name:'Salvar bot'}));
  expect(save).not.toHaveBeenCalled();const consent=screen.getByRole('dialog',{name:'Ativar computador na nuvem'});expect(within(consent).getByText(pt.cloudCost)).toBeTruthy();
  fireEvent.click(within(consent).getByRole('button',{name:pt.cancel}));expect(save).not.toHaveBeenCalled();fireEvent.click(screen.getByRole('button',{name:'Salvar bot'}));
  fireEvent.click(within(screen.getByRole('dialog',{name:'Ativar computador na nuvem'})).getByRole('button',{name:'Confirmar uso da nuvem'}));
  await waitFor(()=>expect(save).toHaveBeenCalledTimes(1));expect(save.mock.calls[0][0].sandbox_profile).toMatchObject({placement:'cloud',backend:'cua-cloud'});
});
