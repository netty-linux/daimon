import {label} from '../i18n/pt-BR';
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {afterEach,beforeEach,expect,it,vi} from 'vitest';
import {api,decodeEnvironment,decodeSandboxProfile} from '../api/client';
import {SandboxPanel,SandboxStatus} from './SandboxPanel';
import {BotEditor} from './Editors';
beforeEach(()=>{Object.defineProperties(HTMLDialogElement.prototype,{showModal:{configurable:true,value:function(){this.setAttribute('open','');}},close:{configurable:true,value:function(){this.removeAttribute('open');}}});});
afterEach(()=>{cleanup();vi.restoreAllMocks();vi.unstubAllGlobals();});
it('shows unavailable runtime and safe owned cleanup status without admin actions',async()=>{
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue({ok:true,status:200,json:async()=>({runtime:{backend:'cua-local',runtime:'gvisor',available:false},sandboxes:[{id:'sb-'+ 'a'.repeat(24),backend:'cua-local',runtime:'gvisor',status:'failed',image:{alias:'linux'},owner_session:'session-one',cleanup:'unresolved',orphan:true}]})}));
 render(<SandboxPanel/>);await screen.findByText(/isolado local está indisponível/);expect(screen.getByText(/Liberação: Requer atenção/)).toBeTruthy();expect(screen.queryByRole('button',{name:/create|delete|install|terminal/i})).toBeNull();
});
it('accepts only explicit supported profile and environment states',()=>{expect(()=>decodeSandboxProfile({backend:'cua-local',image:'evil',runtime:'runc',browser:false,resources:'small',network:'outbound'})).toThrow();for(const state of ['creating','ready','cleaning_up','deleted','failed']){const e=decodeEnvironment({mode:'sandbox',runtime:'gvisor',state,cleanup:'pending'});const v=render(<SandboxStatus environment={e}/>);expect(screen.getByRole('status').textContent).toContain(label(state));v.unmount();}expect(()=>decodeEnvironment({mode:'host',runtime:'gvisor',state:'ready',cleanup:'pending'})).toThrow();});
it('saves safe sandbox mode without enabling a host computer',async()=>{
 vi.spyOn(api,'computers').mockResolvedValue([]);vi.spyOn(api,'mcpTools').mockResolvedValue([]);const save=vi.spyOn(api,'saveBot').mockResolvedValue({id:'bot',name:'Bot',description:'',provider_id:'openai',model:'fake',tools:[],permission_mode:'ask'});
 render(<BotEditor providers={[{id:'openai'}]} onSave={()=>{}} onClose={()=>{}}/>);fireEvent.change(screen.getByLabelText("Modo do computador"),{target:{value:'sandbox'}});fireEvent.click(screen.getByLabelText(/list_apps/));fireEvent.change(screen.getByLabelText("Nome"),{target:{value:'Bot'}});fireEvent.change(screen.getByLabelText("Instruções"),{target:{value:'instructions'}});fireEvent.change(screen.getByLabelText("Modelo"),{target:{value:'fake'}});fireEvent.click(screen.getByRole('button',{name:"Salvar bot"}));await waitFor(()=>expect(save).toHaveBeenCalled());const arg=save.mock.calls[0][0];expect(arg.sandbox_profile).toEqual({backend:'cua-local',image:'linux',runtime:'gvisor',browser:false,resources:'standard',network:'outbound'});expect(arg.computer_profile).toBeUndefined();expect(arg.tools).toEqual(['mcp__sandbox__list_apps']);
});

it('requires explicit cloud placement and displays cloud cost before saving',async()=>{
 const profile={backend:'cua-cloud',placement:'cloud',image:'linux',runtime:'gvisor',browser:false,resources:'small',network:'outbound'};
 expect(decodeSandboxProfile(profile).placement).toBe('cloud');
 for(const bad of [{...profile,placement:undefined},{...profile,resources:'standard'},{...profile,backend:'cua-local'}])expect(()=>decodeSandboxProfile(bad)).toThrow();
 vi.spyOn(api,'computers').mockResolvedValue([]);vi.spyOn(api,'mcpTools').mockResolvedValue([]);
 const save=vi.spyOn(api,'saveBot').mockResolvedValue({id:'bot',name:'Bot',description:'',provider_id:'openai',model:'fake',tools:[],permission_mode:'ask'});
 render(<BotEditor providers={[{id:'openai'}]} onSave={()=>{}} onClose={()=>{}}/>);
 fireEvent.change(screen.getByLabelText("Modo do computador"),{target:{value:'cloud'}});
 expect(screen.getByText(/Enviar uma tarefa usa capacidade paga/)).toBeTruthy();
 fireEvent.change(screen.getByLabelText("Capacidade do computador"),{target:{value:'medium'}});
 fireEvent.change(screen.getByLabelText("Nome"),{target:{value:'Bot'}});fireEvent.change(screen.getByLabelText("Instruções"),{target:{value:'instructions'}});fireEvent.change(screen.getByLabelText("Modelo"),{target:{value:'fake'}});
 fireEvent.click(screen.getByRole('button',{name:"Salvar bot"}));expect(save).not.toHaveBeenCalled();fireEvent.click(screen.getByRole("button",{name:"Confirmar uso da nuvem"}));await waitFor(()=>expect(save).toHaveBeenCalled());
 expect(save.mock.calls[0][0].sandbox_profile).toEqual({...profile,resources:'medium'});expect(save.mock.calls[0][0].computer_profile).toBeUndefined();
});
