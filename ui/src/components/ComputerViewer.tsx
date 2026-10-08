import {label} from '../i18n/pt-BR';
import { copy } from '../i18n/copy';
import { useEffect, useRef, useState } from 'react';
import { safeError } from '../api/client';
import { computerViewAPI,mediaSocket,type ViewState,type ViewTicket } from '../api/computer-view';
import { ComputerVideo } from './computer-video';
type ViewStatus='offline'|'connecting'|'live'|'reconnecting'|'error';
export function ComputerViewer({computerID,onNotice}:{computerID?:string;onNotice?:(message:string)=>void}){
 const canvas=useRef<HTMLCanvasElement>(null),view=useRef<ViewTicket>(undefined),control=useRef<{token:string;socket:WebSocket}>(undefined),sequence=useRef(1),queue=useRef<unknown[]>([]),inflight=useRef(false),move=useRef<unknown>(undefined);
 const [state,setState]=useState<ViewState>(),[status,setStatus]=useState<ViewStatus>('offline'),[error,setError]=useState(''),[busy,setBusy]=useState(false),[controlling,setControlling]=useState(false),[restart,setRestart]=useState(0);
 const sendQueued=()=>{const c=control.current;if(!c||c.socket.readyState!==WebSocket.OPEN||inflight.current)return;if(queue.current.length===0&&move.current){queue.current.push(move.current);move.current=undefined}if(!queue.current.length)return;if(c.socket.bufferedAmount>16384){setError(copy["Input connection is too slow. Control disconnected."]);c.socket.close();return}const events=queue.current.splice(0,32);inflight.current=true;c.socket.send(JSON.stringify({type:'interactive_input',payload:{first_sequence:sequence.current,events}}));sequence.current+=events.length;};
 const input=(event:unknown)=>{if(!control.current)return;if(queue.current.length>=32){setError(copy["Input limit reached. Control disconnected."]);control.current.socket.close();return}queue.current.push(event);sendQueued()};
 useEffect(()=>{
  setState(undefined);setError('');setStatus('offline');setControlling(false);view.current=undefined;if(!computerID)return;const abort=new AbortController();let socket:WebSocket|undefined,video:ComputerVideo|undefined,retry:ReturnType<typeof setTimeout>|undefined,stopped=false,attempt=0,lastKeyframe=0;
  const refresh=()=>void computerViewAPI.state(computerID,abort.signal).then(value=>{if(stopped)return;setState(value);if(value.owner!=='human_control'||value.controller_view_id!==view.current?.id){control.current?.socket.close();control.current=undefined;setControlling(false)}}).catch(e=>{if(!stopped)setError(safeError(e))});
  const connect=async()=>{
   if(stopped)return;setStatus(attempt?'reconnecting':'connecting');try{
    const current=await computerViewAPI.state(computerID,abort.signal);if(stopped)return;setState(current);if(!current.available){setStatus('offline');return}
    const ticket=await computerViewAPI.open(computerID,abort.signal);if(stopped)return;view.current=ticket;socket=mediaSocket(computerID,ticket,'media');socket.binaryType='arraybuffer';
    video=new ComputerVideo(canvas.current!,()=>{if(socket?.readyState===WebSocket.OPEN&&Date.now()-lastKeyframe>1000){lastKeyframe=Date.now();socket.send(JSON.stringify({type:'request_keyframe'}))}},()=>{if(!stopped){setStatus('live');attempt=0}},()=>{if(!stopped){setStatus('error');setError(copy["Video decoder unavailable or invalid media."]);socket?.close()}});
    socket.onmessage=e=>{if(e.data instanceof ArrayBuffer)video?.packet(e.data)};
    socket.onclose=()=>{video?.close();control.current?.socket.close();control.current=undefined;setControlling(false);view.current=undefined;if(!stopped){attempt++;if(attempt<=4){setStatus('reconnecting');retry=setTimeout(()=>void connect(),Math.min(500*2**attempt,5000))}else{setStatus('error');setError(copy["Live view disconnected. Reconnect explicitly."])}}};
    socket.onerror=()=>{socket?.close()};
   }catch(e){if(!stopped){setStatus('error');setError(safeError(e))}}
  };void connect();const poll=setInterval(refresh,1000),flush=setInterval(sendQueued,33);
  return()=>{stopped=true;abort.abort();clearInterval(poll);clearInterval(flush);if(retry)clearTimeout(retry);control.current?.socket.close();control.current=undefined;view.current=undefined;socket?.close();video?.close();queue.current=[];move.current=undefined;inflight.current=false};
 },[computerID,restart]);
 const take=async()=>{
  const ticket=view.current;if(!ticket||!computerID)return;setBusy(true);setError('');try{const lease=await computerViewAPI.take(computerID,ticket);if(view.current!==ticket){await computerViewAPI.release(computerID,ticket.id,lease.token);return}const socket=mediaSocket(computerID,ticket,'input',lease.token);control.current={token:lease.token,socket};sequence.current=1;inflight.current=false;queue.current=[];
   socket.onopen=()=>{if(control.current?.socket===socket){setControlling(true);canvas.current?.focus();onNotice?.(copy["Controle assumido"])}};
   socket.onmessage=e=>{try{const msg=JSON.parse(String(e.data));if(msg.type!=='interactive_input_acknowledgement'||msg.payload?.delivered!==true||msg.payload?.through_sequence!==sequence.current-1)throw new Error();inflight.current=false;sendQueued()}catch{setError(copy["Human input was rejected. Control disconnected."]);socket.close()}};
   socket.onclose=()=>{if(control.current?.socket===socket){control.current=undefined;setControlling(false);queue.current=[];inflight.current=false}};
   socket.onerror=()=>socket.close();setState(await computerViewAPI.state(computerID));
  }catch(e){setError(safeError(e))}finally{setBusy(false)}
 };
 const release=async()=>{const c=control.current,t=view.current;if(!c||!t||!computerID)return;setBusy(true);setError('');c.socket.close();setControlling(false);try{await computerViewAPI.release(computerID,t.id,c.token);setState(await computerViewAPI.state(computerID));onNotice?.(copy["Controle devolvido"])}catch(e){setError(safeError(e))}finally{control.current=undefined;setBusy(false)}};
 const modifiers=(e:{ctrlKey:boolean;shiftKey:boolean;altKey:boolean;metaKey:boolean})=>[...(e.ctrlKey?['control']:[]),...(e.shiftKey?['shift']:[]),...(e.altKey?['option']:[]),...(e.metaKey?['command']:[])];
 const point=(e:{clientX:number;clientY:number})=>{const b=canvas.current!.getBoundingClientRect();return {x_normalized:Math.max(0,Math.min(1,(e.clientX-b.left)/Math.max(1,b.width))),y_normalized:Math.max(0,Math.min(1,(e.clientY-b.top)/Math.max(1,b.height)))}};
 const mine=controlling&&state?.controller_view_id===view.current?.id&&state?.owner==='human_control';
 return <section className="computer-viewer" aria-label={copy["Computer viewer"]}><header><h3>{copy["Computer"]}</h3><p role="status">{label(status)} · {mine?copy["Human in control"]:state?.owner==='human_control'?copy["Human in control in another viewer"]:state?.owner==='agent_control'?copy["Agent in control"]:state?.owner==='transitioning'?copy["Transferring control"]:copy["View only"]}</p></header>
  {!computerID?<p>{copy["This Bot has no Computer enabled."]}</p>:state?.available===false?<p>{copy["Computer actions available when the Driver is connected. Live view unavailable."]}</p>:null}
  {error&&<p role="alert">{error}</p>}
  <canvas hidden={!computerID||state?.available===false} ref={canvas} width={640} height={360} tabIndex={mine?0:-1} aria-label={mine?copy["Computer desktop — human input enabled"]:copy["Computer desktop — view only"]} className={mine?'human-input':''}
   onClick={e=>{if(!mine)return;canvas.current?.focus();const at=point(e),button='left';input({kind:'pointer',phase:'down',button,...at,modifiers:modifiers(e)});input({kind:'pointer',phase:'up',button,...at,modifiers:modifiers(e)})}}
   onPointerMove={e=>{if(mine)move.current={kind:'pointer',phase:'move',button:null,...point(e),modifiers:modifiers(e)}}}
   onWheel={e=>{if(!mine||document.activeElement!==canvas.current)return;input({kind:'scroll',...point(e),delta_x:Math.max(-2000,Math.min(2000,e.deltaX)),delta_y:Math.max(-2000,Math.min(2000,e.deltaY)),phase:'none',momentum_phase:'none',precise:true})}}
   onKeyDown={e=>{if(!mine||e.nativeEvent.isComposing)return;e.preventDefault();if(e.key==='Escape'){void release();return}if(e.key.length===1&&!e.ctrlKey&&!e.altKey&&!e.metaKey)input({kind:'text_commit',text:e.key});else{const key=/^Key[A-Z]$/.test(e.code)?e.code.slice(3).toLowerCase():e.key.toLowerCase();input({kind:'key',key,state:'down',modifiers:modifiers(e),repeat:false});input({kind:'key',key,state:'up',modifiers:modifiers(e),repeat:false})}}}
   onCompositionEnd={e=>{if(mine&&e.data)input({kind:'text_commit',text:e.data})}} onPaste={e=>e.preventDefault()} onDragOver={e=>e.preventDefault()} onDrop={e=>e.preventDefault()}/>
  <footer><button disabled={busy||status!=='live'||state?.owner!=='agent_control'||!view.current} onClick={()=>void take()}>{copy["Take Control"]}</button><button disabled={busy||!controlling} onClick={()=>void release()}>{copy["Give Back Control"]}</button><button disabled={busy} onClick={()=>setRestart(n=>n+1)}>{copy["Reconnect view"]}</button></footer>
  <p className="hint">{copy["Viewers observe only. Take Control explicitly enables input in the focused desktop. Escape gives control back. No clipboard, audio, recording or file transfer."]}</p>
 </section>
}
