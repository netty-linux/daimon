import { cleanup,fireEvent,render,screen,waitFor } from '@testing-library/react';
import { afterEach,beforeEach,expect,it,vi } from 'vitest';
import { api } from '../api/client';
import type {ComputerInfo} from '../api/types';
import {ComputerPanel,ComputerStatus} from './ComputerPanel';
import {BotEditor} from './Editors';
import {ApprovalPanel} from './ApprovalPanel';
import {sessionFixture} from '../test-fixtures';
const computer:ComputerInfo={id:'cua',backend:'cua-local',status:'connected',busy:false,capabilities:[{id:'click',tool:'mcp__cua__click',class:'navigate',available:true},{id:'future_untrusted',tool:'mcp__cua__future_untrusted',class:'dangerous',available:false}]};
beforeEach(()=>{
 Object.defineProperties(HTMLDialogElement.prototype,{showModal:{configurable:true,value:function(){this.setAttribute('open','');}},close:{configurable:true,value:function(){this.removeAttribute('open');}}});
 vi.spyOn(api,'computers').mockResolvedValue([computer]);vi.spyOn(api,'mcpTools').mockResolvedValue([]);
});
afterEach(()=>{cleanup();vi.restoreAllMocks();});
it('shows missing driver, manual setup and no action or live view controls',async()=>{
 vi.mocked(api.computers).mockResolvedValue([{...computer,status:'executable_missing',capabilities:[]}]);render(<ComputerPanel/>);fireEvent.click(screen.getByText("Conexão e recursos do computador"));
 await screen.findByText("Instalação ausente");expect(screen.getByText(/Instale manualmente/)).toBeTruthy();expect(screen.queryByRole('button',{name:/install|click|launch|takeover/i})).toBeNull();expect(document.querySelector('canvas,img,video')).toBeNull();
});
it('gates CUA selection behind explicit Bot profile and retains individual approval',async()=>{
 const save=vi.spyOn(api,'saveBot').mockResolvedValue({id:'bot',name:'Bot',description:'',provider_id:'openai',model:'fake',tools:[],permission_mode:'ask'});
 render(<BotEditor providers={[{id:'openai'}]} onClose={()=>{}} onSave={()=>{}}/>);await screen.findByLabelText(/mcp__cua__click/);
 expect((screen.getByLabelText(/mcp__cua__click/) as HTMLInputElement).disabled).toBe(true);fireEvent.click(screen.getByLabelText("Ativar computador"));fireEvent.click(screen.getByLabelText(/mcp__cua__click/));expect((screen.getByLabelText(/future_untrusted/) as HTMLInputElement).disabled).toBe(true);
 fireEvent.change(screen.getByLabelText("Nome"),{target:{value:'Bot'}});fireEvent.change(screen.getByLabelText("Instruções"),{target:{value:'instructions'}});fireEvent.change(screen.getByLabelText("Modelo"),{target:{value:'fake'}});fireEvent.click(screen.getByRole('button',{name:"Salvar bot"}));
 await waitFor(()=>expect(save).toHaveBeenCalledWith(expect.objectContaining({computer_profile:{enabled:true,backend:'cua-local',mcp_server_id:'cua'},tools:['mcp__cua__click']}),false));
});
it('shows complete escaped typing preview and uses existing Allow once',async()=>{
 vi.spyOn(api,'approval').mockResolvedValue({id:'approval-cua',session_id:sessionFixture.id,kind:'computer',tool:'mcp__cua__type_text',target:'pid: 1 / window_id: 2',preview:'"full\\n\\u202e<script>"',warning:'Your real local desktop. No sandbox.',computer_id:'cua',backend:'cua-local',server_id:'cua',classification:'input'});
 const decision=vi.spyOn(api,'decideApproval').mockResolvedValue(undefined);render(<ApprovalPanel snapshot={{...sessionFixture,status:'waiting_approval'}} onAbort={()=>{}}/>);
 await screen.findByText('Computador local: cua · CUA Driver · Entrada');expect(screen.getByText('"full\\n\\u202e<script>"')).toBeTruthy();expect(document.querySelector('script,img,canvas')).toBeNull();fireEvent.click(screen.getByRole('button',{name:"Permitir uma vez"}));await waitFor(()=>expect(decision).toHaveBeenCalledWith(sessionFixture.id,'approval-cua','allow'));
});
it('uses frozen binding for active Thread badge and omits disabled Computer',async()=>{
 const view=render(<ComputerStatus profile={{enabled:false,backend:'cua-local',mcp_server_id:'cua'}} active={false}/>);expect(screen.queryByText(/Computador /)).toBeNull();view.rerender(<ComputerStatus binding={{id:'cua',backend:'cua-local',capability_ids:['click']}} active={true}/>);await screen.findByText("Computador Em uso");
});

it('never offers an active cloud guest as a host computer',async()=>{
 const id='sandbox-'+ 'a'.repeat(24);
 vi.mocked(api.computers).mockResolvedValue([{...computer,id,backend:'cua-cloud'},computer]);
 render(<BotEditor providers={[{id:'openai'}]} onClose={()=>{}} onSave={()=>{}}/>);
 fireEvent.click(screen.getByRole("tab",{name:"Computador"}));fireEvent.change(screen.getByLabelText("Modo do computador"),{target:{value:'host'}});
 await screen.findByRole('option',{name:'CUA Driver · cua · Disponível'});
 expect(screen.queryByRole('option',{name:new RegExp(id)})).toBeNull();
});
it('identifies cloud approvals with the exact guest',async()=>{
 const id='sandbox-'+ 'b'.repeat(24);
 vi.spyOn(api,'approval').mockResolvedValue({id:'approval-cloud',session_id:sessionFixture.id,kind:'computer',tool:'mcp__sandbox__click',target:'pid: 1 / window_id: 2',preview:'',warning:'CLOUD remote guest.',computer_id:id,backend:'cua-cloud',server_id:'sandbox',classification:'navigate'});
 render(<ApprovalPanel snapshot={{...sessionFixture,status:'waiting_approval'}} onAbort={()=>{}}/>);
 await screen.findByText('Computador na nuvem: '+id+' · CUA Fleet · Navegação');
 expect(screen.queryByText(/Computador local:/)).toBeNull();
});

it('shows a fixed Portuguese startup cause without action controls',async()=>{
 vi.mocked(api.computers).mockResolvedValue([{...computer,status:'startup_failed',reason:'discovery_too_large',capabilities:[]}]);render(<ComputerPanel/>);
 await screen.findByText('A descrição ou o frame de descoberta excedeu o limite permitido.');expect(screen.queryByText('discovery_too_large')).toBeNull();
});
