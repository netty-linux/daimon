// Opt-in browser verification against real Linux tools/contracts, offline Model.
// Docker owns all disposable state. Only a host loopback port is published.
import { chromium } from 'playwright';
import { spawn, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import assert from 'node:assert/strict';

const mcpMode = process.argv.includes('--mcp');
const name = 'daimon-approval-' + randomUUID();
const docker = (...args) => {
  const r = spawnSync('docker', args, { encoding: 'utf8', timeout: 30000 });
  assert.equal(r.status, 0, r.stderr); return r.stdout.trim();
};
const child = spawn('docker', ['run', '--rm', '--pull', 'never', '--name', name,
  '-p', '127.0.0.1::3001', '--mount', `type=bind,source=${resolve('..')},target=/workspace,readonly`,
  '--tmpfs', '/fixture:rw,nosuid,size=32m', '--workdir', '/workspace',
  ...(mcpMode ? ['--env', 'MCP_SMOKE=1'] : []), '--env', 'GOTOOLCHAIN=local', '--env', 'GOPROXY=off', 'golang:1.27.1', 'sh', '-ec',
  'go build -tags approvalsmoke -buildvcs=false -o /tmp/approval-fixture ./ui/scripts/approval-fixture\nexec /tmp/approval-fixture'], { stdio: ['ignore', 'pipe', 'pipe'] });
let output = '', errors = '', exited = false, browser;
child.stdout.on('data', b => { output += b; }); child.stderr.on('data', b => { errors += b; });
const exit = new Promise(resolve => child.once('exit', code => { exited = true; resolve(code); }));
try {
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('fixture startup timeout: ' + errors)), 180000);
    const onData = () => { if (output.includes('READY')) { clearTimeout(timer); child.stdout.off('data', onData); resolve(); } };
    child.stdout.on('data', onData);
    void exit.then(code => { clearTimeout(timer); reject(new Error(`fixture exited ${code}: ${errors}`)); });
    onData();
  });
  const base = 'http://' + docker('port', name, '3001/tcp') + '/';
  browser = await chromium.launch({ headless: true, ...(process.env.DAIMON_SMOKE_CHANNEL ? { channel: process.env.DAIMON_SMOKE_CHANNEL } : {}) });
  const context = await browser.newContext(), page = await context.newPage();
  const pageErrors = [], external = [];
  context.on('page', p => { p.on('pageerror', e => pageErrors.push(e.message)); p.on('request', r => { if (!r.url().startsWith(base)) external.push(r.url()); }); });
  page.on('pageerror', e => pageErrors.push(e.message));
  page.on('request', r => { if (!r.url().startsWith(base)) external.push(r.url()); });
  await page.goto(base); await page.getByText("Crie seu primeiro bot").waitFor();
  await page.getByRole('button', { name: "Novo bot", exact: true }).click();
  await page.getByLabel("Nome", { exact: true }).fill('Approval fixture');
  await page.getByLabel("Instruções", { exact: true }).fill('Offline test.');
  await page.getByRole("tab",{name:"Modelo",exact:true}).click(); await page.getByLabel("Provedor", { exact: true }).selectOption('fixture');
  await page.getByRole("textbox",{name:"Modelo",exact:true}).fill('offline');
  await page.getByRole("tab",{name:"Ferramentas",exact:true}).click(); await page.getByRole("textbox",{name:"Ferramentas",exact:true}).fill(mcpMode ? '' : 'read_file,replace_file');
  if (mcpMode) await page.getByRole('checkbox', { name: /^mcp__local__lookup/ }).check();
  await page.getByRole('button', { name: "Salvar bot", exact: true }).click();
  await page.locator(".console-heading h2").waitFor();
  await page.getByRole('button', { name: "Nova conversa", exact: true }).click();
  await page.getByLabel("Título", { exact: true }).fill('Approvals');
  await page.getByLabel("Pasta local", { exact: true }).fill('/fixture/workspace');
  await page.getByRole('button', { name: "Salvar conversa", exact: true }).click();
  const pending = async () => {
    const thread = new URLSearchParams(new URL(page.url()).hash.slice(1)).get('thread');
    const active = await (await page.request.get(base + `api/v1/threads/${thread}/session`)).json();
    return (await (await page.request.get(base + `api/v1/sessions/${active.session.id}/approval`)).json()).approval;
  };
  const send = async text => {
    await page.getByLabel("Mensagem", { exact: true }).fill(text);
    await page.getByRole('button', { name: "Enviar", exact: true }).click();
    await page.getByRole('heading', { name: "Ação requer sua aprovação" }).waitFor();
    return await pending();
  };
  const decide = async choice => {
    await page.getByRole('button', { name: choice, exact: true }).click();
    await page.getByText("Concluído", { exact: true }).waitFor();
  };
  if (mcpMode) {
    await page.locator(".topbar").getByRole("button",{name:"Configurações",exact:true}).click();await page.getByRole("tab",{name:"MCP",exact:true}).click();
    await page.locator("summary").filter({hasText:"Conexões MCP"}).click();
    await page.getByText(/^local.*Disponível$/).waitFor();
    await page.getByRole("button",{name:"Fechar",exact:true}).click();const a = await send('MCP allow');
    const shown = await page.getByRole('dialog').innerText();
    assert.ok(!shown.includes('browser-private-args'));
    assert.equal(a.server_id, 'local');
    await decide("Permitir uma vez");
    await page.getByText('MCP response persisted', { exact: true }).waitFor();
    await page.reload();
    await page.getByText('MCP response persisted', { exact: true }).waitFor();
    await send('MCP deny'); await decide("Negar");
    await page.getByText('MCP denied', { exact: true }).waitFor();
    assert.equal(docker('exec', name, 'cat', '/fixture/mcp-trace').split('call').length - 1, 1);
    await send('MCP shutdown'); docker('stop', '--time', '10', name);
    assert.equal(await exit, 0, errors); assert.ok(output.includes('MCP CLOSED'));
    assert.deepEqual(pageErrors, []); assert.deepEqual(external, []);
    console.log('MCP browser smoke passed: discovery, selection, approval, denial, persistent history and process shutdown.');
  } else {
  await send('Read allow'); assert.equal(await page.getByText('READ-MARKER', { exact: true }).count(), 0);
  // Pen frame 16: dismiss only the display, keep server approval pending.
  const beforeReview = await pending();
  await page.getByRole('button',{name:'Fechar diálogo',exact:true}).click();
  await page.getByRole('heading',{name:'Aprovação pendente',exact:true}).waitFor();
  assert.equal(await page.getByLabel('Mensagem',{exact:true}).isDisabled(),true);
  assert.equal((await pending()).id,beforeReview.id);
  assert.equal(await page.evaluate(()=>localStorage.length+sessionStorage.length),0);
  await page.getByRole('button',{name:'Revisar ação pendente',exact:true}).click();
  await page.getByRole('heading',{name:'Ação requer sua aprovação',exact:true}).waitFor();

  assert.equal(await page.locator('.approval-target').innerText(), '"read.txt"');
  await decide("Permitir uma vez"); await page.getByText('Read allowed', { exact: true }).first().waitFor();
  await send('Read deny'); await decide("Negar"); await page.getByText('Read denied', { exact: true }).waitFor();
  await send('Write deny'); const preview = await page.locator('.approval-preview').innerText();
  assert.ok(preview.includes('original')); assert.ok(preview.includes('proposed\\n<safe>'));
  await decide("Negar"); assert.equal(docker('exec', name, 'cat', '/fixture/workspace/target.txt'), 'original');
  const write = await send('Write allow'); await decide("Permitir uma vez");
  assert.equal(docker('exec', name, 'cat', '/fixture/workspace/target.txt'), 'proposed\n<safe>');
  const post = (p, decision) => page.request.post(base + `api/v1/sessions/${p.session_id}/approvals/${p.id}`, { data: { decision }, headers: { Origin: new URL(base).origin } });
  assert.equal((await post(write, 'allow')).status(), 409);
  const reload = await send('Reload pending'); await page.reload();
  await page.getByRole('heading', { name: "Ação requer sua aprovação" }).waitFor();
  assert.equal((await pending()).id, reload.id); await decide("Permitir uma vez");
  const tabs = await send('Two tabs'), second = await context.newPage(); await second.goto(page.url());
  await second.getByRole('heading', { name: "Ação requer sua aprovação" }).waitFor();
  const replies = await Promise.all([post(tabs, 'deny'), second.request.post(base + `api/v1/sessions/${tabs.session_id}/approvals/${tabs.id}`, { data: { decision: 'allow' }, headers: { Origin: new URL(base).origin } })]);
  assert.deepEqual(replies.map(r => r.status()).sort(), [200, 409]);
  await page.getByText("Concluído", { exact: true }).waitFor(); await second.close();
  const aborted = await send('Abort pending'); await page.getByRole('button', { name: "Parar execução" }).click();
  await page.getByText("Cancelado", { exact: true }).waitFor(); assert.equal((await post(aborted, 'allow')).status(), 409);
  await send('Shutdown pending'); docker('stop', '--time', '10', name);
  assert.equal(await exit, 0, errors); assert.ok(output.includes('CLOSED'));
  assert.deepEqual(pageErrors, []); assert.deepEqual(external, []);
  console.log('Approval browser smoke passed: read/write allow/deny, exact preview, reload, two tabs, replay, abort and shutdown.');
  }
} finally {
  await browser?.close();
  if (!exited) { spawnSync('docker', ['stop', '--time', '10', name], { timeout: 30000 }); await exit; }
}
