import { act,cleanup,fireEvent,render,screen,waitFor } from '@testing-library/react';
import { afterEach,beforeEach,expect,it,vi } from 'vitest';
import { computerViewAPI,mediaSocket,type ViewState } from '../api/computer-view';
import { ComputerViewer } from './ComputerViewer';
vi.mock('../api/computer-view',async original=>({...await original<typeof import('../api/computer-view')>(),mediaSocket:vi.fn()}));
vi.mock('./computer-video',()=>({ComputerVideo:class{constructor(_canvas:unknown,_keyframe:unknown,private live:()=>void){}packet(){this.live()}close(){}}}));
class FakeSocket {
 readyState=1;bufferedAmount=0;binaryType='';onopen?:()=>void;onclose?:()=>void;onmessage?:(event:{data:unknown})=>void;onerror?:()=>void;sent:string[]=[];
 send(raw:string){this.sent.push(raw)}close(){this.readyState=3;this.onclose?.()}
}
const ticket={id:'view-'+ 'a'.repeat(32),token:'b'.repeat(32),expires_at:'2026-10-08T12:00:00Z'},lease={token:'c'.repeat(32),expires_at:'2026-10-08T12:00:15Z'};
let state:ViewState,media:FakeSocket,input:FakeSocket;
beforeEach(()=>{
 state={available:true,owner:'agent_control',agent_session_id:'run',viewers:1};media=new FakeSocket();input=new FakeSocket();
 vi.spyOn(computerViewAPI,'state').mockImplementation(async()=>({...state}));vi.spyOn(computerViewAPI,'open').mockResolvedValue(ticket);
 vi.spyOn(computerViewAPI,'take').mockImplementation(async()=>{state={...state,owner:'human_control',controller_view_id:ticket.id};return lease});
 vi.spyOn(computerViewAPI,'release').mockImplementation(async()=>{state={...state,owner:'agent_control',controller_view_id:undefined};return {}});
 vi.mocked(mediaSocket).mockImplementation((_id,_v,kind)=>(kind==='media'?media:input) as unknown as WebSocket);
});
afterEach(()=>{cleanup();vi.restoreAllMocks();vi.mocked(mediaSocket).mockReset()});
async function live(){await waitFor(()=>expect(media.onmessage).toBeTypeOf('function'));act(()=>media.onmessage?.({data:new ArrayBuffer(8)}));await screen.findByText(/Ao vivo · Agente no controle/)}
async function take(){fireEvent.click(screen.getByRole('button',{name:"Assumir controle"}));await waitFor(()=>expect(input.onopen).toBeTypeOf('function'));act(()=>input.onopen?.());await screen.findByText(/Você está no controle$/)}
it('opens a view-only live viewer and enables exact human input only after backend takeover',async()=>{
 render(<ComputerViewer computerID="cua"/>);await live();const canvas=screen.getByLabelText("Tela do computador — somente visualização");fireEvent.keyDown(canvas,{key:'x'});fireEvent.click(canvas);expect(input.sent).toHaveLength(0);
 await take();const enabled=screen.getByLabelText("Tela do computador — seu controle está ativo");fireEvent.keyDown(enabled,{key:'x'});expect(JSON.parse(input.sent[0]).payload.events).toEqual([{kind:'text_commit',text:'x'}]);
 act(()=>input.onmessage?.({data:JSON.stringify({type:'interactive_input_acknowledgement',payload:{delivered:true,through_sequence:1}})}));fireEvent.click(enabled,{clientX:12,clientY:8});expect(JSON.parse(input.sent[1]).payload.events[0].phase).toBe('down');
 fireEvent.click(screen.getByRole('button',{name:"Devolver controle"}));await waitFor(()=>expect(computerViewAPI.release).toHaveBeenCalledWith('cua',ticket.id,lease.token));await screen.findByLabelText("Tela do computador — somente visualização");
});
it('keeps input unavailable to a second viewer and never captures the composer keyboard',async()=>{
 state={...state,owner:'human_control',controller_view_id:'different-view'};render(<><textarea aria-label="Chat composer"/><ComputerViewer computerID="cua"/></>);await waitFor(()=>expect(media.onmessage).toBeTypeOf('function'));act(()=>media.onmessage?.({data:new ArrayBuffer(8)}));await screen.findByText(/Outro usuário está no controle/);
 expect((screen.getByRole('button',{name:"Assumir controle"}) as HTMLButtonElement).disabled).toBe(true);fireEvent.keyDown(screen.getByLabelText('Chat composer'),{key:'x'});fireEvent.keyDown(screen.getByLabelText("Tela do computador — somente visualização"),{key:'x'});expect(input.sent).toHaveLength(0);
});
it('separates missing media from actions and exposes no clipboard/recording/file controls',async()=>{
 state={...state,available:false};render(<ComputerViewer computerID="cua"/>);await screen.findByText(/visualização está indisponível/);expect(mediaSocket).not.toHaveBeenCalled();expect(screen.queryByRole('button',{name:/clipboard|record|upload|audio/i})).toBeNull();expect(document.querySelector('input[type=file],audio,video')).toBeNull();
});
it('releases using Escape, disconnects input on reconnect and starts the next viewer view-only',async()=>{
 render(<ComputerViewer computerID="cua"/>);await live();await take();fireEvent.keyDown(screen.getByLabelText("Tela do computador — seu controle está ativo"),{key:'Escape'});await waitFor(()=>expect(computerViewAPI.release).toHaveBeenCalledOnce());
 fireEvent.click(screen.getByRole('button',{name:"Reconectar visualização"}));await waitFor(()=>expect(computerViewAPI.open).toHaveBeenCalledTimes(2));expect(input.readyState).toBe(3);expect(screen.queryByLabelText("Tela do computador — seu controle está ativo")).toBeNull();
});
