import { startupReasons, type StartupReason } from '../i18n/computer';
import {errorMessages} from '../i18n/errors';
import type {SandboxProfile, EnvironmentMetadata, SandboxCatalog, SandboxInfo} from './types';
import { eventLabels, statuses, mcpName, type Bot, type BotInput, type Thread, type ProviderSummary, type SessionSnapshot, type SessionEvent } from './types';
import type { ConversationMessage, MessagePage, ApprovalPresentation, MemoryInput, MemoryRecord, ComputerProfile, ComputerInfo, ComputerBinding, ComputerClass } from './types';

const messages = errorMessages;
export class APIError extends Error {
  constructor(public readonly code: string) { super(messages[code] ?? messages.internal_error); }
}
export const safeError = (error: unknown) => error instanceof APIError ? error.message : 'Não foi possível concluir esta operação.';
export function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new APIError('invalid_response');
  return value as Record<string, unknown>;
}
const strings = (v: Record<string, unknown>, keys: string[]) => keys.every(k => typeof v[k] === 'string');
export const integer = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
function decodeBot(value: unknown): Bot {
  const v = object(value);
  if (!strings(v, ['id', 'name', 'description', 'provider_id', 'model']) || !Array.isArray(v.tools) || !v.tools.every(x => typeof x === 'string') || !['ask', 'read-only'].includes(String(v.permission_mode))) throw new APIError('invalid_response');
  return { ...(v.sandbox_profile===undefined?{}:{sandbox_profile:decodeSandboxProfile(v.sandbox_profile)}), ...(v.computer_profile===undefined?{}:{computer_profile:decodeComputerProfile(v.computer_profile)}), id: v.id as string, name: v.name as string, description: v.description as string, provider_id: v.provider_id as string, model: v.model as string, tools: [...v.tools] as string[], permission_mode: v.permission_mode as Bot['permission_mode'] };
}
function decodeThread(value: unknown): Thread {
  const v = object(value);
  if (!strings(v, ['id', 'bot_id', 'workspace', 'title', 'created_at', 'updated_at']) || !Number.isFinite(Date.parse(v.created_at as string)) || !Number.isFinite(Date.parse(v.updated_at as string))) throw new APIError('invalid_response');
  return { id: v.id as string, bot_id: v.bot_id as string, workspace: v.workspace as string, title: v.title as string, created_at: v.created_at as string, updated_at: v.updated_at as string };
}
export function decodeSession(value: unknown): SessionSnapshot {
  const v = object(value);
  if (!strings(v, ['id', 'thread_id', 'started_at', 'stop_reason', 'error_category']) || !statuses.includes(v.status as SessionSnapshot['status']) || !(v.finished_at === null || typeof v.finished_at === 'string') || !['last_event_sequence', 'steps', 'tool_calls', 'truncated_tool_results'].every(k => integer(v[k]))) throw new APIError('invalid_response');
  return { ...(v.persistent_workspace===undefined?{}:{persistent_workspace:decodePersistentWorkspace(v.persistent_workspace)}), ...(v.environment===undefined?{}:{environment:decodeEnvironment(v.environment)}), ...(v.computer===undefined?{}:{computer:decodeComputerBinding(v.computer)}), id: v.id as string, thread_id: v.thread_id as string, status: v.status as SessionSnapshot['status'], started_at: v.started_at as string, finished_at: v.finished_at as string | null, stop_reason: v.stop_reason as string, error_category: v.error_category as string, last_event_sequence: v.last_event_sequence as number, steps: v.steps as number, tool_calls: v.tool_calls as number, truncated_tool_results: v.truncated_tool_results as number };
}
export function decodeEvent(value: unknown): SessionEvent {
  const v = object(value);
  if (!integer(v.sequence) || v.sequence === 0 || !integer(v.step) || !integer(v.tool_index) || typeof v.stop_reason !== 'string' || typeof v.kind !== 'string' || !Object.hasOwn(eventLabels, v.kind)) throw new APIError('invalid_response');
  return { sequence: v.sequence, kind: v.kind as SessionEvent['kind'], step: v.step, tool_index: v.tool_index, stop_reason: v.stop_reason };
}
export async function request(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<unknown> {
  let response: Response;
  try { response = await fetch(`/api/v1${path}`, { method, signal, credentials: 'same-origin', headers: body === undefined ? { Accept: 'application/json' } : { Accept: 'application/json', 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) }); }
  catch (error) { if (signal?.aborted) throw error; throw new APIError('network_error'); }
  if (response.status === 204 && response.ok) return undefined;
  let data: unknown;
  try { data = await response.json(); } catch { throw new APIError('invalid_response'); }
  if (!response.ok) {
    const envelope = object(data); const error = object(envelope.error);
    throw new APIError(typeof error.code === 'string' && Object.hasOwn(messages, error.code) ? error.code : 'internal_error');
  }
  return data;
}
async function list<T>(path: string, key: string, decode: (v: unknown) => T, signal?: AbortSignal): Promise<T[]> {
  const v = object(await request(path, 'GET', undefined, signal));
  if (!Array.isArray(v[key])) throw new APIError('invalid_response');
  return (v[key] as unknown[]).map(decode);
}
const provider = (value: unknown): ProviderSummary => { const v = object(value); if (typeof v.id !== 'string') throw new APIError('invalid_response'); return { id: v.id }; };
export function decodeMessage(value: unknown): ConversationMessage {
  const v = object(value);
  if (!strings(v, ['id', 'thread_id', 'content', 'created_at', 'session_id']) || !integer(v.sequence) || v.sequence === 0 || !['user', 'assistant'].includes(String(v.role)) || !(v.content as string).trim() || !Number.isFinite(Date.parse(v.created_at as string))) throw new APIError('invalid_response');
  return { id: v.id as string, thread_id: v.thread_id as string, sequence: v.sequence, role: v.role as ConversationMessage['role'], content: v.content as string, created_at: v.created_at as string, session_id: v.session_id as string };
}
export function decodeMemory(value:unknown):MemoryRecord {
 const v=object(value),p=object(v.provenance); const validID=(id:string)=>/^[a-z][a-z0-9-]{0,63}$/.test(id);
 if(!strings(v,['id','scope','scope_id','kind','content','created_at','updated_at']) || !validID(String(v.id)) || !['global','bot','thread'].includes(String(v.scope)) || (v.scope==='global'?v.scope_id!=='':!validID(String(v.scope_id))) || !['fact','preference','instruction','note'].includes(String(v.kind)) || !String(v.content).trim() || new TextEncoder().encode(String(v.content)).length>16384 || !Array.isArray(v.tags) || v.tags.length>16 || v.tags.some(t=>typeof t!=='string'||!/^[a-z][a-z0-9_-]{0,63}$/.test(t)) || new Set(v.tags).size!==v.tags.length || p.source_type!=='manual' || !Number.isFinite(Date.parse(String(v.created_at))) || !Number.isFinite(Date.parse(String(v.updated_at))) || Date.parse(String(v.updated_at))<Date.parse(String(v.created_at))) throw new APIError('invalid_response');
 return {id:v.id as string,scope:v.scope as MemoryRecord['scope'],scope_id:v.scope_id as string,kind:v.kind as MemoryRecord['kind'],content:v.content as string,tags:[...v.tags] as string[],created_at:v.created_at as string,updated_at:v.updated_at as string,provenance:{source_type:'manual'}};
}
const validComputerID=(v:unknown)=>typeof v==='string'&&/^[a-z][a-z0-9-]{0,31}$/.test(v);
export function decodeComputerProfile(value:unknown):ComputerProfile {
 const v=object(value);if(typeof v.enabled!=='boolean'||v.backend!=='cua-local'||!validComputerID(v.mcp_server_id))throw new APIError('invalid_response');
 return {enabled:v.enabled,backend:'cua-local',mcp_server_id:v.mcp_server_id as string};
}
function decodeComputerBinding(value:unknown):ComputerBinding {
 const v=object(value);if(!validComputerID(v.id)||!(v.backend==='cua-local'||v.backend==='cua-cloud'&&String(v.id).startsWith('sandbox-'))||!Array.isArray(v.capability_ids)||v.capability_ids.length>32||v.capability_ids.some(k=>typeof k!=='string'||!/^[a-z][a-z0-9_]*$/.test(k)))throw new APIError('invalid_response');
 return {id:v.id as string,backend:v.backend as 'cua-local'|'cua-cloud',capability_ids:[...v.capability_ids] as string[]};
}
export function decodeComputer(value:unknown):ComputerInfo {
 const v=object(value);if(!validComputerID(v.id)||!(v.backend==='cua-local'||v.backend==='cua-cloud'&&String(v.id).startsWith('sandbox-'))||!['configured','executable_missing','startup_failed','connected','unavailable'].includes(String(v.status))||typeof v.busy!=='boolean'||!Array.isArray(v.capabilities)||v.capabilities.length>64||(v.controller_session_id!==undefined&&(typeof v.controller_session_id!=='string'||!/^[a-z][a-z0-9-]{0,63}$/.test(v.controller_session_id))))throw new APIError('invalid_response');
 if(v.reason!==undefined&&(v.status!=='startup_failed'||typeof v.reason!=='string'||!Object.hasOwn(startupReasons,v.reason)))throw new APIError('invalid_response');
 const capabilities=v.capabilities.map(value=>{const c=object(value);if(typeof c.id!=='string'||!mcpName(String(c.tool))||c.tool!==`mcp__${v.id}__${c.id}`||!['observe','navigate','input','system','dangerous'].includes(String(c.class))||typeof c.available!=='boolean')throw new APIError('invalid_response');return {id:c.id,tool:c.tool as string,class:c.class as ComputerClass,available:c.available};});
 return {...(v.reason?{reason:v.reason as StartupReason}:{}),id:v.id as string,backend:v.backend as 'cua-local'|'cua-cloud',status:v.status as ComputerInfo['status'],capabilities,busy:v.busy,...(v.controller_session_id?{controller_session_id:v.controller_session_id as string}:{})};
}
export const api = {
 computers:(signal?:AbortSignal)=>list('/computers','computers',decodeComputer,signal),
 memories: async(signal?:AbortSignal):Promise<MemoryRecord[]>=>{
  const all:MemoryRecord[]=[];let after='',size=0;
  for(let pages=0;pages<2048;pages++){
   const data=object(await request(`/memories?limit=100${after?'&after='+encodeURIComponent(after):''}`,'GET',undefined,signal));
   if(!Array.isArray(data.memories)||data.memories.length>100||typeof data.next_after!=='string'||typeof data.has_more!=='boolean')throw new APIError('invalid_response');
   const page=data.memories.map(decodeMemory);
   for(const m of page){if(m.id<=after)throw new APIError('invalid_response');after=m.id;size+=new TextEncoder().encode(m.content).length;all.push(m)}
   if(all.length>2048||size>32*1024*1024||data.next_after!==after||(data.has_more&&page.length===0))throw new APIError('invalid_response');
   if(!data.has_more)return all;
  }
  throw new APIError('invalid_response');
 },
 saveMemory:async(input:MemoryInput,edit:boolean)=>decodeMemory(await request(`/memories${edit?'/'+encodeURIComponent(input.id):''}`,edit?'PUT':'POST',input)),
 deleteMemory:(id:string)=>request('/memories/'+encodeURIComponent(id),'DELETE'),
  mcpServers: (signal?: AbortSignal) => list('/mcp/servers','servers',value => {
    const v=object(value); if (!strings(v,['id','status']) || !['connected','disabled','unavailable'].includes(String(v.status))) throw new APIError('invalid_response');
    return {id:v.id as string,status:v.status as 'connected'|'disabled'|'unavailable'};
  },signal),
  mcpTools: (signal?: AbortSignal) => list('/mcp/tools','tools',value => {
    const v=object(value);if (!strings(v,['name','server_id','description','classification']) || !mcpName(String(v.name)) || !String(v.name).startsWith(`mcp__${v.server_id}__`) || !['read','write','other'].includes(String(v.classification)) || typeof v.available!=='boolean' || typeof v.permitted!=='boolean' || (v.permitted && v.classification!=='read')) throw new APIError('invalid_response');
    return {name:v.name as string,server_id:v.server_id as string,description:v.description as string,classification:v.classification as 'read'|'write'|'other',available:v.available,permitted:v.permitted};
  },signal),
  activeSession: async (id: string, signal?: AbortSignal): Promise<SessionSnapshot | undefined> => {
    const data = object(await request(`/threads/${encodeURIComponent(id)}/session`, 'GET', undefined, signal));
    if (data.session === null) return undefined;
    const snapshot = decodeSession(data.session); if (snapshot.thread_id !== id) throw new APIError('invalid_response'); return snapshot;
  },
  approval: async (id: string, signal?: AbortSignal): Promise<ApprovalPresentation | undefined> => {
    const data = object(await request(`/sessions/${encodeURIComponent(id)}/approval`, 'GET', undefined, signal));
    if (data.approval === null) return undefined;
    const v = object(data.approval);
    if (!strings(v, ['id', 'session_id', 'tool', 'kind', 'target', 'warning', 'preview']) || v.session_id !== id || (!['read_file','list_dir','replace_file','create_file'].includes(String(v.tool)) && !mcpName(String(v.tool))) || !['read','write','computer'].includes(String(v.kind))) throw new APIError('invalid_response');
    if(v.kind==='computer') {
      if(!mcpName(String(v.tool))||!strings(v,['server_id','computer_id','backend','classification'])||!(v.backend==='cua-local'||v.backend==='cua-cloud'&&v.server_id==='sandbox')||!(String(v.computer_id).startsWith('sandbox-') ? v.server_id==='sandbox'&&/^sandbox-[a-f0-9]{24}$/.test(String(v.computer_id)) : v.server_id===v.computer_id)||!validComputerID(v.computer_id)||!String(v.tool).startsWith(`mcp__${v.server_id}__`)||!['observe','navigate','input','system'].includes(String(v.classification))||!v.target||!v.warning||new TextEncoder().encode(String(v.preview)).length>1024*1024||(v.classification==='input'&&!v.preview))throw new APIError('invalid_response');
      return {id:v.id as string,session_id:id,tool:v.tool as string,kind:'computer',target:v.target as string,warning:v.warning as string,preview:v.preview as string,server_id:v.server_id as string,computer_id:v.computer_id as string,backend:v.backend as 'cua-local'|'cua-cloud',classification:v.classification as ComputerClass};
    }
    const write = v.tool === 'replace_file'  || v.tool === 'create_file';
    if (v.kind !== (write ? 'write' : 'read') || !(v.target as string) || !(v.warning as string) || (write ? !(v.preview as string) : v.preview !== '') || new TextEncoder().encode(v.preview as string).length > 1024*1024) throw new APIError('invalid_response');
    const external=mcpName(String(v.tool));if (external && (!strings(v,['server_id','classification']) || v.classification!=='read' || !String(v.tool).startsWith(`mcp__${v.server_id}__`))) throw new APIError('invalid_response');
    return { id: v.id as string, session_id: id, tool: v.tool as ApprovalPresentation['tool'], kind: v.kind as ApprovalPresentation['kind'], target: v.target as string, warning: v.warning as string, preview: v.preview as string, ...(external?{server_id:v.server_id as string,classification:'read' as const}:{}) };
  },
  decideApproval: (session: string, approval: string, decision: 'allow' | 'deny') => request(`/sessions/${encodeURIComponent(session)}/approvals/${encodeURIComponent(approval)}`, 'POST', { decision }),
  bots: (signal?: AbortSignal) => list('/bots', 'bots', decodeBot, signal),
  threads: (signal?: AbortSignal) => list('/threads', 'threads', decodeThread, signal),
  providers: (signal?: AbortSignal) => list('/providers', 'providers', provider, signal),
  saveBot: async (b: BotInput, edit: boolean) => decodeBot(await request(`/bots${edit ? `/${encodeURIComponent(b.id)}` : ''}`, edit ? 'PUT' : 'POST', b)),
  saveThread: async (t: Thread, edit: boolean) => decodeThread(await request(`/threads${edit ? `/${encodeURIComponent(t.id)}` : ''}`, edit ? 'PUT' : 'POST', t)),
  deleteBot: (id: string) => request(`/bots/${encodeURIComponent(id)}`, 'DELETE'),
  deleteThread: (id: string) => request(`/threads/${encodeURIComponent(id)}`, 'DELETE'),
  start: async (id: string, thread_id: string, message: string, message_id?: string) => decodeSession(await request('/sessions', 'POST', { id, thread_id, message, ...(message_id ? { message_id } : {}) })),
  messages: async (id: string, after = 0, signal?: AbortSignal): Promise<MessagePage> => {
    const page = object(await request(`/threads/${encodeURIComponent(id)}/messages?after=${after}&limit=40`, 'GET', undefined, signal));
    if (!Array.isArray(page.messages) || !integer(page.next_after) || typeof page.has_more !== 'boolean') throw new APIError('invalid_response');
    const items = page.messages.map(decodeMessage);
    let sequence = after;
    for (const message of items) { if (message.thread_id !== id || message.sequence <= sequence) throw new APIError('invalid_response'); sequence = message.sequence; }
    if (page.next_after !== sequence || (page.has_more && items.length === 0)) throw new APIError('invalid_response');
    return { messages: items, next_after: sequence, has_more: page.has_more };
  },
  session: async (id: string, signal?: AbortSignal) => decodeSession(await request(`/sessions/${encodeURIComponent(id)}`, 'GET', undefined, signal)),
  abort: (id: string) => request(`/sessions/${encodeURIComponent(id)}/abort`, 'POST'),
};

export function decodeSandboxProfile(value:unknown):SandboxProfile {
 const v=object(value),cloud=v.backend==='cua-cloud';
 if((cloud?v.placement!=='cloud'||!['small','medium'].includes(String(v.resources)):v.backend!=='cua-local'||v.placement!==undefined&&v.placement!=='local'||!['small','standard'].includes(String(v.resources)))||v.image!=='linux'||v.runtime!=='gvisor'||typeof v.browser!=='boolean'||v.network!=='outbound')throw new APIError('invalid_response');
 return {backend:cloud?'cua-cloud':'cua-local',...(v.placement===undefined?{}:{placement:cloud?'cloud' as const:'local' as const}),image:'linux',runtime:'gvisor',browser:v.browser,resources:v.resources as SandboxProfile['resources'],network:'outbound'};
}
export function decodeEnvironment(value:unknown):EnvironmentMetadata {
 const v=object(value);if(v.mode!=='sandbox'||v.runtime!=='gvisor'||!['creating','ready','cleaning_up','deleted','failed'].includes(String(v.state))||!['pending','complete','unresolved'].includes(String(v.cleanup))||(v.sandbox_id!==undefined&&!/^sb-[a-f0-9]{24}$/.test(String(v.sandbox_id))))throw new APIError('invalid_response');
 if(v.placement!==undefined&&!['local','cloud'].includes(String(v.placement)))throw new APIError('invalid_response');
 return {...(v.placement===undefined?{}:{placement:v.placement as 'local'|'cloud'}),...(v.expires_at===undefined?{}:{expires_at:String(v.expires_at)}),mode:'sandbox',runtime:'gvisor',state:v.state as EnvironmentMetadata['state'],cleanup:v.cleanup as EnvironmentMetadata['cleanup'],...(v.sandbox_id===undefined?{}:{sandbox_id:String(v.sandbox_id)})};
}
export async function sandboxCatalog(signal?:AbortSignal):Promise<SandboxCatalog>{
 const v=object(await request('/sandboxes','GET',undefined,signal)),r=object(v.runtime);
 if(!Array.isArray(v.sandboxes)||v.sandboxes.length>64||r.backend!=='cua-local'||r.runtime!=='gvisor'||typeof r.available!=='boolean')throw new APIError('invalid_response');
 const sandboxes=v.sandboxes.map(raw=>{const s=object(raw),i=object(s.image);if(!/^sb-[a-f0-9]{24}$/.test(String(s.id))||!['cua-local','cua-cloud'].includes(String(s.backend))||s.runtime!=='gvisor'||i.alias!=='linux'||!['creating','running','deleting','deleted','failed'].includes(String(s.status))||typeof s.owner_session!=='string'||!['pending','complete','unresolved'].includes(String(s.cleanup))||typeof s.orphan!=='boolean')throw new APIError('invalid_response');return {id:String(s.id),backend:s.backend,placement:s.placement,expires_at:s.expires_at,runtime:'gvisor',status:s.status,image:{alias:'linux'},owner_session:s.owner_session,cleanup:s.cleanup,orphan:s.orphan} as SandboxInfo;});
 const backends = v.backends===undefined?undefined:(()=>{if(!Array.isArray(v.backends)||v.backends.length>2)throw new APIError('invalid_response');return v.backends.map(raw=>{const b=object(raw);if(!['cua-local','cua-cloud'].includes(String(b.backend))||b.runtime!=='gvisor'||typeof b.available!=='boolean'||b.reason!==undefined&&!/^[a-z_]{1,64}$/.test(String(b.reason)))throw new APIError('invalid_response');return {backend:b.backend as 'cua-local'|'cua-cloud',runtime:'gvisor' as const,available:b.available,...(b.reason?{reason:String(b.reason)}:{})};});})();
 return {sandboxes,...(backends?{backends}:{}),runtime:{backend:'cua-local',runtime:'gvisor',available:r.available}};
}

function decodePersistentWorkspace(value:unknown):NonNullable<SessionSnapshot['persistent_workspace']>{
 const v=object(value);if(typeof v.id!=='string'||!/^env-[a-f0-9]{24}$/.test(v.id)||!integer(v.starting_revision)||!integer(v.committed_revision)||typeof v.committed!=='boolean'||!['hydrating','ready','saving','saved','failed'].includes(String(v.state)))throw new APIError('invalid_response');
 return {id:v.id,starting_revision:v.starting_revision,committed_revision:v.committed_revision,committed:v.committed,state:v.state as NonNullable<SessionSnapshot['persistent_workspace']>['state']};
}
export function decodePersistentEnvironment(value:unknown):import('./types').PersistentEnvironment{
 const v=object(value);if(v.version!==1||!strings(v,['id','thread_id','manifest_hash','created_at','updated_at'])||!/^env-[a-f0-9]{24}$/.test(String(v.id))||!['revision','files','directories','bytes'].every(k=>integer(v[k]))||Number(v.files)>64||Number(v.directories)>32||Number(v.bytes)>1048576||!/^[a-f0-9]{64}$/.test(String(v.manifest_hash))||!Number.isFinite(Date.parse(String(v.created_at)))||!Number.isFinite(Date.parse(String(v.updated_at))))throw new APIError('invalid_response');
 return {version:1,id:String(v.id),thread_id:String(v.thread_id),revision:Number(v.revision),files:Number(v.files),directories:Number(v.directories),bytes:Number(v.bytes),manifest_hash:String(v.manifest_hash),created_at:String(v.created_at),updated_at:String(v.updated_at)};
}
export const environmentsAPI={
 get:async(id:string,signal?:AbortSignal)=>decodePersistentEnvironment(await request(`/threads/${encodeURIComponent(id)}/environment`,'GET',undefined,signal)),
 create:async(id:string)=>decodePersistentEnvironment(await request(`/threads/${encodeURIComponent(id)}/environment`,'POST',{})),
 remove:async(id:string)=>{await request(`/threads/${encodeURIComponent(id)}/environment`,'DELETE');}
};
