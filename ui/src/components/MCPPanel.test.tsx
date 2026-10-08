import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MCPPanel } from './MCPPanel';
import { BotEditor } from './Editors';
import { ApprovalPanel } from './ApprovalPanel';
import { api } from '../api/client';
import { sessionFixture } from '../test-fixtures';

beforeEach(()=>{
  Object.defineProperties(HTMLDialogElement.prototype,{showModal:{configurable:true,value:function(){this.setAttribute('open','');}},close:{configurable:true,value:function(){this.removeAttribute('open');}}});
  vi.spyOn(api,'mcpServers').mockResolvedValue([{id:'local',status:'connected'},{id:'broken',status:'unavailable'}]);
  vi.spyOn(api,'mcpTools').mockResolvedValue([{name:'mcp__local__lookup',server_id:'local',classification:'read',description:'"<script>external</script>"',available:true,permitted:true},{name:'mcp__broken__lookup',server_id:'broken',classification:'read',description:'"Unreachable"',available:false,permitted:true}]);
});
afterEach(()=>{cleanup();vi.restoreAllMocks();});
it('shows safe read-only server/tool status without configuration controls',async()=>{
  render(<MCPPanel/>);fireEvent.click(screen.getAllByText("Conexões MCP")[0]);
  await screen.findByText('local · Disponível');expect(screen.getByText('broken · Indisponível')).toBeTruthy();expect(screen.getByText('mcp__local__lookup')).toBeTruthy();
  expect(document.querySelector('script')).toBeNull();expect(screen.queryByRole('button',{name:/install|start server|save config/i})).toBeNull();
});
it('selects exact MCP capabilities and retains an unavailable existing declaration',async()=>{
  const saved=vi.spyOn(api,'saveBot').mockResolvedValue({id:'bot-a',name:'Bot',description:'',provider_id:'openai',model:'fake',tools:[],permission_mode:'ask'});
  render(<BotEditor providers={[{id:'openai'}]} onClose={()=>{}} onSave={()=>{}}/>);
  await screen.findByLabelText(/mcp__local__lookup/);fireEvent.click(screen.getByLabelText(/mcp__local__lookup/));expect((screen.getByLabelText("Ferramentas") as HTMLInputElement).value).toBe('mcp__local__lookup');
  expect((screen.getByLabelText(/mcp__broken__lookup/) as HTMLInputElement).disabled).toBe(true);
  fireEvent.change(screen.getByLabelText("Nome"),{target:{value:'Bot'}});fireEvent.change(screen.getByLabelText("Instruções"),{target:{value:"Instruções"}});fireEvent.change(screen.getByLabelText("Modelo"),{target:{value:'fake'}});fireEvent.click(screen.getByRole('button',{name:"Salvar bot"}));
  await waitFor(()=>expect(saved).toHaveBeenCalledWith(expect.objectContaining({tools:['mcp__local__lookup']}),false));
});
it('renders MCP approval metadata only and uses the existing one-shot flow',async()=>{
  vi.spyOn(api,'approval').mockResolvedValue({id:'approval-mcp',session_id:sessionFixture.id,tool:'mcp__local__lookup',kind:'read',target:'mcp__local__lookup',warning:'External MCP receives model arguments; result goes to provider.',preview:'',server_id:'local',classification:'read'});
  const decide=vi.spyOn(api,'decideApproval').mockResolvedValue(undefined);
  render(<ApprovalPanel snapshot={{...sessionFixture,status:'waiting_approval'}} onAbort={()=>{}}/>);
  await screen.findByText('Ferramenta externa: local · Tipo: Leitura');fireEvent.click(screen.getByRole('button',{name:"Permitir uma vez"}));
  await waitFor(()=>expect(decide).toHaveBeenCalledWith(sessionFixture.id,'approval-mcp','allow'));
  expect(screen.queryByText('raw arguments')).toBeNull();
});
