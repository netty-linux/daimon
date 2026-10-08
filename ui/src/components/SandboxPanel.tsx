import {label} from '../i18n/pt-BR';
import { copy } from '../i18n/copy';
import {useEffect,useState} from 'react';

import {sandboxCatalog,safeError} from '../api/client';

import type {SandboxCatalog,EnvironmentMetadata} from '../api/types';

export function SandboxPanel(){

 const [catalog,setCatalog]=useState<SandboxCatalog>(),[error,setError]=useState(''),[revision,setRevision]=useState(0);

 useEffect(()=>{const c=new AbortController();void sandboxCatalog(c.signal).then(v=>{if(!c.signal.aborted){setCatalog(v);setError('');}}).catch(e=>{if(!c.signal.aborted)setError(safeError(e));});return()=>c.abort();},[revision]);

 return <section className="settings-summary"><h3>{copy["Settings / Sandboxes"]}</h3>{catalog?.backends?.map(backend=><p className="hint" key={backend.backend}>{backend.backend==='cua-cloud'?'Nuvem':'Local'} · {backend.available?'Disponível':'Indisponível'}</p>)}<details className="providers"><summary>{copy["Settings / Sandboxes"]}</summary><p>{copy["CUA local / official Fleet cloud · Linux · gVisor"]}</p>{catalog?.backends?.map(b=><p key={b.backend}>{b.backend}: {b.available?"ready":b.reason??"unavailable"}</p>)}{error&&<p role="alert">{error}</p>}{catalog&&!catalog.runtime.available&&<p role="status">{copy["Local Runtime unavailable. Configure CUA and its runtime manually on the server."]}</p>}<p className="hint">{copy["Disposable per Session. Guest files disappear on cleanup. Outbound networking is enabled. The host workspace is not mounted."]}</p>{catalog?.sandboxes.map(s=><article key={s.id}><strong>{s.id}</strong><p>{s.placement==='cloud'?copy["CLOUD · "]:copy["LOCAL · "]}{label(s.status)} · {s.image.alias} · {s.runtime}</p><p>{copy["Session:"]}{' '}{s.owner_session}</p>{s.expires_at&&<p>{copy["Claim expires:"]}{' '}{s.expires_at}</p>}<p>{copy["Cleanup:"]}{' '}{label(s.cleanup)}{s.orphan?copy[" · recovered orphan"]:''}</p></article>)}<button onClick={()=>setRevision(n=>n+1)}>{copy["Refresh sandboxes"]}</button></details></section>;

}

export function SandboxStatus({environment}:{environment?:EnvironmentMetadata}){if(!environment)return null;return <span className="badge" role="status">{environment.placement==='cloud'?copy["CLOUD"]:copy["Sandbox"]} · {label(environment.state)} · {label(environment.cleanup)}</span>;}
