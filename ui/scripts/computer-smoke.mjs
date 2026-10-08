// Opt-in, offline browser/Go/MCP integration. No actual CUA or desktop actions.
import {chromium} from 'playwright';
import {spawn,spawnSync} from 'node:child_process';
import {randomUUID} from 'node:crypto';
import {resolve} from 'node:path';
import assert from 'node:assert/strict';

const docker=(...args)=>{const r=spawnSync('docker',args,{encoding:'utf8',timeout:30000});assert.equal(r.status,0,r.stderr);return r.stdout.trim()};
const missing=process.argv.includes('--missing'),name='daimon-computer-'+randomUUID();
const child=spawn('docker',['run','--rm','--pull','never','--name',name,'-p','127.0.0.1::3001','--mount',`type=bind,source=${resolve('..')},target=/workspace,readonly`,'--tmpfs','/fixture:rw,nosuid,size=32m','--workdir','/workspace','--env','COMPUTER_SMOKE=1',...(missing?['--env','CUA_MISSING=1']:[]),'--env','GOTOOLCHAIN=local','--env','GOPROXY=off','golang:1.27.1','sh','-ec','go build -buildvcs=false -o /tmp/cua-driver ./ui/scripts/cua-fixture\ngo build -tags approvalsmoke -buildvcs=false -o /tmp/approval-fixture ./ui/scripts/approval-fixture\nexec /tmp/approval-fixture'],{stdio:['ignore','pipe','pipe']});
let output='',errors='',exited=false,browser;
child.stdout.on('data',b=>{output+=b});child.stderr.on('data',b=>{errors+=b});
const exit=new Promise(resolve=>child.once('exit',code=>{exited=true;resolve(code)}));
try{
 await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error('startup timeout: '+errors)),180000);const check=()=>{if(output.includes('READY')){clearTimeout(timer);child.stdout.off('data',check);resolve()}};child.stdout.on('data',check);void exit.then(code=>{clearTimeout(timer);reject(new Error('fixture exit '+code+': '+errors))});check()});
 const base='http://'+docker('port',name,'3001/tcp')+'/',origin=new URL(base).origin;
 browser=await chromium.launch({headless:true,...(process.env.DAIMON_SMOKE_CHANNEL?{channel:process.env.DAIMON_SMOKE_CHANNEL}:{})});
 const context=await browser.newContext(),page=await context.newPage(),pageErrors=[],external=[];
 context.on('page',p=>{p.on('pageerror',e=>pageErrors.push(e.message));p.on('request',r=>{if(!r.url().startsWith(base))external.push(r.url())})});
 page.on('pageerror',e=>pageErrors.push(e.message));page.on('request',r=>{if(!r.url().startsWith(base))external.push(r.url())});
 await page.goto(base);await page.getByText("Crie seu primeiro bot").waitFor();await page.locator(".topbar").getByRole("button",{name:"Configurações",exact:true}).click();await page.getByRole("tab",{name:"Computador",exact:true}).click();await page.getByText("Conexão e recursos do computador",{exact:true}).click();
 await page.getByText(missing?"Instalação ausente":"Disponível",{exact:true}).waitFor();
 assert.equal(await page.locator('canvas,img,video').count(),0);await page.getByRole('button',{name:'Fechar',exact:true}).click();
 await page.getByRole('button',{name:"Novo bot",exact:true}).click();await page.getByLabel("Nome",{exact:true}).fill('Computer Bot');await page.getByLabel("Instruções",{exact:true}).fill('Offline controlled computer fixture.');await page.getByRole("tab",{name:"Modelo",exact:true}).click(); await page.getByLabel("Provedor",{exact:true}).selectOption('fixture');await page.getByRole("textbox",{name:"Modelo",exact:true}).fill('offline');await page.getByRole("dialog").getByRole("tab",{name:"Computador",exact:true}).click(); await page.getByLabel("Ativar computador",{exact:true}).check();
 if(!missing){for(const tool of ['list_apps','click','type_text'])await page.getByRole('checkbox',{name:new RegExp('^mcp__cua__'+tool+' ')}).check();assert.equal(await page.getByRole('checkbox',{name:/^mcp__cua__future_untrusted/}).isDisabled(),true)}
 await page.getByRole('button',{name:"Salvar bot",exact:true}).click();await page.locator(".console-heading h2").waitFor();
 await page.getByRole('button',{name:"Nova conversa",exact:true}).click();await page.getByLabel("Título",{exact:true}).fill('Computer A');await page.getByLabel("Pasta local",{exact:true}).fill('/fixture/workspace');await page.getByRole('button',{name:"Salvar conversa",exact:true}).click();
 await page.waitForURL(url=>new URLSearchParams(url.hash.slice(1)).has('thread'));
 const thread=new URLSearchParams(new URL(page.url()).hash.slice(1)).get('thread');
 const pending=async()=>{const active=await(await page.request.get(base+`api/v1/threads/${thread}/session`)).json();return(await(await page.request.get(base+`api/v1/sessions/${active.session.id}/approval`)).json()).approval};
 const send=async text=>{await page.getByLabel("Mensagem",{exact:true}).fill(text);await page.getByRole('button',{name:"Enviar",exact:true}).click();if(!missing){await page.getByRole('heading',{name:"Ação requer sua aprovação"}).waitFor();return await pending()}};
 const info=async()=> (await(await page.request.get(base+'api/v1/computers')).json()).computers[0];
 const waitFree=async()=>{for(let i=0;i<100;i++){if(!(await info()).busy)return;await new Promise(r=>setTimeout(r,20))}assert.fail('lease not released')};
 const post=(route,data)=>page.request.post(base+'api/v1/'+route,{data,headers:{Origin:origin}});
 if(missing){await send('Computer missing');await page.getByText("Falhou",{exact:true}).waitFor();assert.equal((await info()).busy,false);assert.equal((await info()).status,'executable_missing')}
 else{
  const first=await send('Computer sequence');assert.equal(first.classification,'observe');assert.equal((await info()).controller_session_id,first.session_id);
  assert.equal(docker('exec',name,'cat','/tmp/cua-driver.trace').includes('call'),false);
  const threadList=(await(await page.request.get(base+'api/v1/threads')).json()).threads,bot=threadList[0].bot_id,now=new Date().toISOString();
  assert.equal((await post('threads',{id:'thread-b',bot_id:bot,workspace:'/fixture/workspace',title:'Computer B',created_at:now,updated_at:now})).status(),201);
  assert.equal((await post('sessions',{id:'busy-run',thread_id:'thread-b',message:'Computer click'})).status(),202);
  for(let i=0;i<100;i++){const s=await(await page.request.get(base+'api/v1/sessions/busy-run')).json();if(s.status==='failed'){assert.equal(s.error_category,'computer_busy');break}await new Promise(r=>setTimeout(r,20));if(i===99)assert.fail('busy run did not fail')}
  await page.getByRole('button',{name:"Permitir uma vez",exact:true}).click();
  await page.locator("dialog summary").click();await page.getByText("Computador local: cua · CUA Driver · Navegação",{exact:true}).waitFor();assert.ok((await page.locator('.approval-target').innerText()).includes('window_id: 2'));
  await page.getByRole('button',{name:"Permitir uma vez",exact:true}).click();
  await page.locator("dialog summary").click();await page.getByText("Computador local: cua · CUA Driver · Entrada",{exact:true}).waitFor();assert.ok((await page.locator('.approval-preview').innerText()).includes('Exact browser text\\n<safe>'));await page.getByRole('button',{name:"Permitir uma vez",exact:true}).click();
  await page.getByText("Concluído",{exact:true}).waitFor();await waitFree();assert.equal(docker('exec',name,'cat','/tmp/cua-driver.trace').split('call').length-1,3);
  await page.reload();await page.getByText('Computer sequence completed',{exact:true}).waitFor();
  // Another Thread may use the computer once the first has finished.
  const response=await post('sessions',{id:'second-run',thread_id:'thread-b',message:'Computer click'});assert.equal(response.status(),202);
  let second;for(let i=0;i<100;i++){second=(await(await page.request.get(base+'api/v1/sessions/second-run/approval')).json()).approval;if(second)break;await new Promise(r=>setTimeout(r,20))}assert.ok(second);assert.equal((await post(`sessions/second-run/approvals/${second.id}`,{decision:'allow'})).status(),200);await waitFree();
  await send('Computer deny');await page.getByRole('button',{name:"Negar",exact:true}).click();await page.getByText("Concluído",{exact:true}).waitFor();await waitFree();assert.equal(docker('exec',name,'cat','/tmp/cua-driver.trace').split('call').length-1,4);
  const aborted=await send('Computer abort');await page.getByRole('button',{name:"Parar execução",exact:true}).click();await page.getByText("Cancelado",{exact:true}).waitFor();await waitFree();assert.equal((await post(`sessions/${aborted.session_id}/approvals/${aborted.id}`,{decision:'allow'})).status(),409);
  // Unknown declarations fail at capability resolution, before model/approval.
  const botValue=(await(await page.request.get(base+'api/v1/bots/'+bot)).json());
  const put=async tools=>page.request.put(base+'api/v1/bots/'+bot,{data:{...botValue,instructions:'Offline',tools},headers:{Origin:origin}});
  assert.equal((await put(['mcp__cua__future_untrusted'])).status(),200);await page.getByLabel("Mensagem",{exact:true}).fill('Computer unknown');await page.getByRole('button',{name:"Enviar",exact:true}).click();await page.getByText("Falhou",{exact:true}).waitFor();await waitFree();assert.equal(await page.getByRole('heading',{name:"Ação requer sua aprovação"}).count(),0);
  assert.equal((await put(botValue.tools)).status(),200);
  // Retire the fake driver with a controlled crash; no automatic reconnect.
  docker('exec',name,'sh','-c','printf crash > /tmp/cua-driver.mode');await send('Computer crash');await page.getByRole('button',{name:"Permitir uma vez",exact:true}).click();await page.getByText("Concluído",{exact:true}).waitFor();await waitFree();assert.equal((await info()).status,'unavailable');
  for(const action of ['click','type','screenshot','launch'])assert.equal((await post('computers/cua/'+action,{})).status(),404);
 }
 docker('stop','--time','10',name);assert.equal(await exit,0,errors);assert.ok(output.includes('COMPUTER CLOSED'));assert.ok(output.includes('MCP CLOSED'));assert.deepEqual(pageErrors,[]);assert.deepEqual(external,[]);
 console.log(missing?'Computer missing-driver browser smoke passed.':'Computer browser smoke passed: observe/click/type approvals, deny, abort, busy, release, second Thread, unknown action, crash, history and shutdown.');
}finally{await browser?.close();if(!exited){spawnSync('docker',['stop','--time','10',name],{timeout:30000});await exit}}
