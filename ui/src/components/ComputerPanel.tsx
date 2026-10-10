import { startupReasons } from '../i18n/computer';
import { copy } from '../i18n/copy';
import { useEffect, useState } from 'react';
import { api, safeError } from '../api/client';
import type { ComputerInfo, ComputerProfile, ComputerBinding } from '../api/types';

const labels:Record<ComputerInfo['status'],string>={connected:copy["Connected"],configured:copy["Configured · disabled"],executable_missing:copy["Missing · executable not found"],startup_failed:copy["Unavailable · MCP startup failed"],unavailable:copy["Unavailable"]};
export function ComputerPanel(){
 const [items,setItems]=useState<ComputerInfo[]>([]),[error,setError]=useState(''),[revision,setRevision]=useState(0);
 useEffect(()=>{const c=new AbortController();void api.computers(c.signal).then(v=>{if(!c.signal.aborted){setItems(v);setError('');}}).catch(e=>{if(!c.signal.aborted)setError(safeError(e));});return()=>c.abort();},[revision]);
 return <section className="settings-summary"><h3>{copy["Computer"]}</h3><p className="hint">Status: {items.length?labels[items[0].status]:copy["No local computer configured."]}</p><details className="mcp-panel"><summary>{copy["Settings / Computer"]}</summary><p className="notice">{copy["Host CUA Driver controls your real local desktop. Sandbox guests are identified separately. Individual approval is required; live view depends on configured media."]}</p><button onClick={()=>setRevision(r=>r+1)}>{copy["Refresh Computer status"]}</button>{error&&<p className="error">{error}</p>}{!items.length&&<p>{copy["No local computer configured."]}</p>}{items.map(c=><section key={c.id}><h3>{c.backend==='cua-cloud'?copy["CLOUD · CUA Fleet"]:c.id.startsWith('sandbox-')?copy["LOCAL Sandbox"]:'CUA Driver'} · {c.id}</h3><p>{labels[c.status]}{c.busy?copy[" · In use"]:''}</p>{c.status==='startup_failed'&&<p className="hint">{startupReasons[c.reason??'unknown']}</p>}<ul>{c.capabilities.map(cap=><li key={cap.id}>{cap.id} · {cap.class} · {cap.available?copy["approval required"]:copy["denied or unsupported"]}</li>)}</ul></section>)}<p className="hint">{copy["CUA Driver not available? Install it manually using the official CUA instructions, grant the required OS permissions, and configure its absolute executable in the server’s mcp.json. Restart DAIMON explicitly. This panel cannot install, reconnect, or drive the computer."]}</p><a href="https://cua.ai/docs/cua-driver/quickstart" target="_blank" rel="noreferrer">{copy["Manual CUA setup"]}</a></details></section>;
}
export function ComputerStatus({profile,binding,active}:{profile?:ComputerProfile;binding?:ComputerBinding;active:boolean}){
 const [info,setInfo]=useState<ComputerInfo>();
 const id=binding?.id??(profile?.enabled?profile.mcp_server_id:undefined);
 useEffect(()=>{const c=new AbortController();setInfo(undefined);if(id)void api.computers(c.signal).then(items=>{if(!c.signal.aborted)setInfo(items.find(i=>i.id===id));}).catch(()=>{});return()=>c.abort();},[id,active]);
 if(!id)return null;
 return <span className="badge">{copy["Computer"]} {active&&binding?copy["In use"]:info?.status==='connected'?copy["Connected"]:info?.status==='executable_missing'?copy["Missing"]:copy["Unavailable"]}</span>;
}
