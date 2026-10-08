import { APIError, integer, object, request } from './client';
export type Ownership = 'agent_control' | 'human_control' | 'transitioning' | 'view_only';
export interface ViewState { available: boolean; owner: Ownership; controller_view_id?: string; agent_session_id?: string; viewers: number }
export interface ViewTicket { id: string; token: string; expires_at: string }
export interface ControlTicket { token: string; expires_at: string }
export function viewState(value: unknown): ViewState {
 const v=object(value);if(typeof v.available!=='boolean'||!['agent_control','human_control','transitioning','view_only'].includes(String(v.owner))||!integer(v.viewers)||v.viewers>4||(v.controller_view_id!==undefined&&typeof v.controller_view_id!=='string')||(v.agent_session_id!==undefined&&typeof v.agent_session_id!=='string'))throw new APIError('invalid_response');
 return {available:v.available,owner:v.owner as Ownership,viewers:v.viewers,...(v.controller_view_id?{controller_view_id:String(v.controller_view_id)}:{}),...(v.agent_session_id?{agent_session_id:String(v.agent_session_id)}:{})};
}
function ticket(value:unknown):ControlTicket {const v=object(value);if(typeof v.token!=='string'||!/^[A-Za-z0-9_-]{32}$/.test(v.token)||typeof v.expires_at!=='string'||!Number.isFinite(Date.parse(v.expires_at)))throw new APIError('invalid_response');return {token:v.token,expires_at:v.expires_at};}
const path=(id:string)=>`/computers/${encodeURIComponent(id)}`;
export const computerViewAPI={
 state:async(id:string,signal?:AbortSignal)=>viewState(await request(`${path(id)}/view`,'GET',undefined,signal)),
 open:async(id:string,signal?:AbortSignal):Promise<ViewTicket>=>{const value=await request(`${path(id)}/views`,'POST',{target:'desktop'},signal);const v=object(value),t=ticket(v);if(typeof v.id!=='string'||!/^view-[A-Za-z0-9_-]{32}$/.test(v.id))throw new APIError('invalid_response');return {...t,id:v.id};},
 take:async(id:string,v:ViewTicket)=>ticket(await request(`${path(id)}/control/take`,'POST',{view_id:v.id,token:v.token})),
 release:async(id:string,viewID:string,token:string)=>request(`${path(id)}/control/release`,'POST',{view_id:viewID,token}),
};
export function mediaSocket(id:string,v:ViewTicket,kind:'media'|'input',token=v.token):WebSocket{
 const u=new URL(`/api/v1${path(id)}/views/${encodeURIComponent(v.id)}/${kind}`,location.href);u.protocol=location.protocol==='https:'?'wss:':'ws:';
 return new WebSocket(u,['rcdp.v2',`daimon.${kind}.${token}`]);
}
