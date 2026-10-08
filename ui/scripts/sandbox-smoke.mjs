// Offline end-to-end Computer media/control fixture. No desktop, credentials
// or network service is used. The private probe stays inside container loopback.
import {chromium} from 'playwright';
import {spawn,spawnSync} from 'node:child_process';
import {randomUUID} from 'node:crypto';
import {resolve} from 'node:path';
import assert from 'node:assert/strict';
const docker=(...args)=>{const r=spawnSync('docker',args,{encoding:'utf8',timeout:30000});assert.equal(r.status,0,r.stderr);return r.stdout.trim()};
const cloud=process.argv.includes('--cloud');
const name='daimon-sandbox-'+randomUUID();
const child=spawn('docker',['run','--rm','--pull','never','--name',name,'-p','127.0.0.1::3001','--mount',`type=bind,source=${resolve('..')},target=/workspace,readonly`,'--tmpfs','/fixture:rw,nosuid,size=32m','--workdir','/workspace','--env','COMPUTER_SMOKE=1','--env','COMPUTER_VIEW_SMOKE=1','--env',cloud?'CLOUD_SMOKE=1':'SANDBOX_SMOKE=1','--env','GOTOOLCHAIN=local','--env','GOPROXY=off','golang:1.27.1','sh','-ec','go build -buildvcs=false -o /tmp/cua-driver ./ui/scripts/cua-fixture\ngo build -tags approvalsmoke -buildvcs=false -o /tmp/approval-fixture ./ui/scripts/approval-fixture\nexec /tmp/approval-fixture'],{stdio:['ignore','pipe','pipe']});
let output='',errors='',exited=false,browser;
child.stdout.on('data',b=>{output+=b});child.stderr.on('data',b=>{errors+=b});const exit=new Promise(resolve=>child.once('exit',code=>{exited=true;resolve(code)}));
try{
 await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error('fixture startup timeout: '+errors)),180000);const check=()=>{if(output.includes('READY')){clearTimeout(timer);child.stdout.off('data',check);resolve()}};child.stdout.on('data',check);void exit.then(code=>{clearTimeout(timer);reject(new Error('fixture exit '+code+': '+errors))});check()});
 const base='http://'+docker('port',name,'3001/tcp')+'/',origin=new URL(base).origin;
 browser=await chromium.launch({headless:true,...(process.env.DAIMON_SMOKE_CHANNEL?{channel:process.env.DAIMON_SMOKE_CHANNEL}:{})});const context=await browser.newContext(),page=await context.newPage(),pageErrors=[];
 context.on('page',p=>p.on('pageerror',e=>pageErrors.push(e.message)));page.on('pageerror',e=>pageErrors.push(e.message));
 const post=(route,data)=>page.request.post(base+'api/v1/'+route,{data,headers:{Origin:origin}});
 assert.equal((await post('bots',{id:'view-bot',name:'View Bot',instructions:'Offline fixture.',provider_id:'fixture',model:'offline',tools:['mcp__sandbox__click'],permission_mode:'ask',sandbox_profile:{backend:cloud?'cua-cloud':'cua-local',...(cloud?{placement:'cloud'}:{}),image:'linux',runtime:'gvisor',browser:true,resources:'small',network:'outbound'}})).status(),201);
 const now=new Date().toISOString();assert.equal((await post('threads',{id:'view-thread',bot_id:'view-bot',workspace:'/fixture/workspace',title:'View Thread',created_at:now,updated_at:now})).status(),201);
 await page.goto(base+'#thread=view-thread');await page.getByLabel("Mensagem",{exact:true}).fill('Sandbox takeover');await page.getByRole('button',{name:"Enviar",exact:true}).click();await page.getByRole('heading',{name:"Ação requer sua aprovação"}).waitFor();
 const active=async()=> (await(await page.request.get(base+'api/v1/threads/view-thread/session')).json()).session;
 const session=await active();assert.equal(session.computer.backend,cloud?'cua-cloud':'cua-local');assert.equal(session.environment.placement,cloud?'cloud':'local');const computerID=session.computer.id;assert.match(computerID,/^sandbox-[a-f0-9]{24}$/);const pending=async()=> (await(await page.request.get(base+`api/v1/sessions/${session.id}/approval`)).json()).approval;const first=await pending();
 await page.getByRole('button',{name:"Fechar diálogo",exact:true}).click();await page.getByRole('tab',{name:"Computador",exact:true}).click();try { await page.getByText("Ao vivo · Agente no controle",{exact:true}).waitFor(); } catch(e) { console.error('Viewer state:',await page.locator('.computer-viewer').innerText()); throw e; }
 assert.equal(await page.locator('canvas').evaluate(c=>c.getContext('2d').getImageData(0,0,1,1).data[1]),140);
 const state=async()=> (await(await page.request.get(base+'api/v1/computers/'+computerID+'/view')).json());const probe=()=>JSON.parse(docker('exec',name,'curl','-fsS','http://127.0.0.1:3002'));
 assert.equal((await state()).owner,'agent_control');await page.getByRole('button',{name:"Assumir controle",exact:true}).click();await page.getByLabel("Tela do computador — seu controle está ativo").waitFor();assert.equal((await state()).owner,'human_control');
 const second=await context.newPage();await second.goto(base+'#thread=view-thread');await second.getByRole('heading',{name:"Ação requer sua aprovação"}).waitFor();await second.getByRole('button',{name:"Fechar diálogo",exact:true}).click();await second.getByRole('tab',{name:"Computador",exact:true}).click();await second.getByText("Ao vivo · Outro usuário está no controle",{exact:true}).waitFor();assert.equal(await second.getByRole('button',{name:"Assumir controle",exact:true}).isDisabled(),true);
 await page.getByLabel("Tela do computador — seu controle está ativo").click({position:{x:25,y:25}});await page.keyboard.type('human');
 for(let i=0;i<100;i++){const events=probe().inputs;if(events.filter(e=>e.kind==='text_commit').map(e=>e.text).join('')==='human')break;await new Promise(r=>setTimeout(r,50));if(i===99)assert.fail('human text not received')}
 const events=probe().inputs;assert.ok(events.some(e=>e.kind==='pointer'&&e.phase==='down'));assert.ok(events.some(e=>e.kind==='pointer'&&e.phase==='up'));assert.equal(events.filter(e=>e.kind==='text_commit').map(e=>e.text).join(''),'human');
 assert.equal((await post(`sessions/${session.id}/approvals/${first.id}`,{decision:'allow'})).status(),200);
 let next;for(let i=0;i<100;i++){next=await pending();if(next&&next.id!==first.id)break;await new Promise(r=>setTimeout(r,50))};assert.ok(next&&next.id!==first.id);assert.equal(docker('exec',name,'sh','-c','test ! -e /fixture/sandbox-actions && printf no-actions'),'no-actions','agent bypassed human ownership');
 await page.getByRole('heading',{name:"Ação requer sua aprovação"}).waitFor();await page.getByRole('button',{name:"Fechar diálogo",exact:true}).click();await page.getByRole('button',{name:"Devolver controle",exact:true}).click();await page.getByText("Ao vivo · Agente no controle",{exact:true}).waitFor();assert.equal((await state()).owner,'agent_control');
 assert.equal((await post(`sessions/${session.id}/approvals/${next.id}`,{decision:'allow'})).status(),200);await page.getByText("Concluído",{exact:true}).waitFor();assert.equal(docker('exec',name,'cat','/fixture/sandbox-actions').split('guest-call').length-1,1,'agent authority not restored');
 await page.reload();await page.getByRole('tab',{name:'Chat',exact:true}).click();await page.getByText('Sandbox takeover completed',{exact:true}).waitFor();await page.getByRole('tab',{name:"Computador",exact:true}).click();const catalog=(await(await page.request.get(base+'api/v1/sandboxes')).json()).sandboxes;assert.equal(catalog.length,1);assert.equal(catalog[0].cleanup,'complete');assert.equal(catalog[0].status,'deleted');assert.equal((await page.request.get(base+'api/v1/computers/'+computerID+'/view')).status(),404);assert.equal(docker('exec',name,'cat','/tmp/cua-driver.trace').includes('call'),false,'sandbox used host');
 const messages=(await(await page.request.get(base+'api/v1/threads/view-thread/messages')).json()).messages;assert.ok(messages.every(m=>!m.content.includes('data:image')&&!m.content.includes('human')));
 await second.close();docker('stop','--time','10',name);assert.equal(await exit,0,errors);assert.ok(output.includes('MEDIA CLOSED'));assert.deepEqual(pageErrors,[]);
 console.log((cloud?'Cloud ':'Local ')+'Sandbox browser smoke passed: live frames, two viewers, exact human input, agent exclusion, Give Back, deleted viewer unavailable, private history and joined shutdown.');
}finally{await browser?.close();if(!exited){spawnSync('docker',['stop','--time','10',name],{timeout:30000});await exit}}
