import {label} from '../i18n/pt-BR';
import { copy } from '../i18n/copy';
import { useEffect, useState } from 'react';
import { api, safeError } from '../api/client';
import type { MCPServer, MCPTool } from '../api/types';

export function MCPPanel() {
  const [servers,setServers]=useState<MCPServer[]>([]),[tools,setTools]=useState<MCPTool[]>([]),[error,setError]=useState(''),[revision,setRevision]=useState(0);
  useEffect(()=>{const c=new AbortController();setError('');void Promise.all([api.mcpServers(c.signal),api.mcpTools(c.signal)]).then(([s,t])=>{if(!c.signal.aborted){setServers(s);setTools(t);}}).catch(e=>{if(!c.signal.aborted)setError(safeError(e));});return()=>c.abort();},[revision]);
  return <details className="mcp-panel"><summary>{copy["Settings / MCP"]}</summary><p className="hint">{copy["Local stdio tools. Configuration is managed on the server. Discovery grants no permission."]}</p><button onClick={()=>setRevision(r=>r+1)}>{copy["Refresh MCP status"]}</button>{error && <p className="error">{error}</p>}<h3>{copy["MCP Servers"]}</h3>{!servers.length?<p>{copy["No MCP servers configured."]}</p>:<ul>{servers.map(s=><li key={s.id}>{s.id} · {label(s.status)}</li>)}</ul>}<h3>{copy["MCP Tools"]}</h3><ul>{tools.map(t=><li key={t.name}><code>{t.name}</code><p>{label(t.classification)} · {t.available?'Disponível':'Indisponível'} · {t.permitted?copy["approval required"]:'negado'}</p><p className="hint">{t.description}</p></li>)}</ul></details>;
}
