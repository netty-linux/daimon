// Optional real-browser smoke: local fake provider, disposable state/workspace,
// no credentials. Install a Playwright browser first or set DAIMON_SMOKE_CHANNEL.
import { chromium } from 'playwright';
import { createServer } from 'node:http';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import assert from 'node:assert/strict';

const root = resolve('..');
const temp = await mkdtemp(join(tmpdir(), 'daimon-ui-smoke-'));
const workspace = join(temp, 'workspace'), state = join(temp, 'state');
await mkdir(workspace); await mkdir(state);
let calls = 0, continuity = false;
const answer = 'Persisted assistant response';
const provider = createServer(async (req, res) => {
  assert.equal(req.url, '/v1/chat/completions');
  assert.equal(req.headers.authorization, undefined);
  let body = ''; for await (const chunk of req) body += chunk;
  const value = JSON.parse(body); calls++;
  assert.equal(value.stream, false);
  const last = value.messages.at(-1).content;
  if (last === 'Continue with the previous context') {
    assert.deepEqual(value.messages.filter(m => m.role !== 'system'), [
      { role: 'user', content: 'Respond briefly' },
      { role: 'assistant', content: answer },
      { role: 'user', content: last },
    ]);
    continuity = true;
  }
  if(last==='Fail this execution'){res.statusCode=503;res.end('{}');return;}
  const abort = last === 'Wait so I can abort';
  const timer = setTimeout(() => { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: answer } }] })); }, abort ? 15000 : 600);
  res.on('close', () => clearTimeout(timer));
});
await new Promise(resolve => provider.listen(0, '127.0.0.1', resolve));
const binary = join(temp, process.platform === 'win32' ? 'daimon.exe' : 'daimon');
const build = spawnSync('go', ['build', '-o', binary, './cmd/daimon'], { cwd: root, encoding: 'utf8' });
if (build.status !== 0) throw new Error(build.stderr);
const devMode = process.env.DAIMON_SMOKE_DEV === '1';
const startServer = port => spawn(binary, ['serve', '--port', String(port), '--data-dir', state], { cwd: root, env: { ...process.env, DAIMON_API_KEY: '', DAIMON_BASE_URL: `http://127.0.0.1:${provider.address().port}/v1` }, stdio: ['ignore', 'pipe', 'pipe'] });
const ready = process => new Promise((resolve, reject) => {
  let output = ''; const timer = setTimeout(() => reject(new Error('startup timeout')), 10000);
  process.stdout.on('data', data => { output += data; const match = output.match(/http:\/\/127\.0\.0\.1:\d+\//); if (match) { clearTimeout(timer); resolve(match[0]); } });
  process.once('exit', () => { clearTimeout(timer); reject(new Error('exited before ready')); });
});
const stop = async process => { process.kill('SIGINT'); await new Promise(resolve => { if (process.exitCode !== null) resolve(); else process.once('exit', resolve); }); };
let child = startServer(0);
let browser;
let vite;
try {
  let base = await ready(child);
  const serverPort = new URL(base).port;
  if (devMode) {
    vite = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--host', '127.0.0.1', '--port', '0'], { cwd: process.cwd(), env: { ...process.env, DAIMON_DEV_PORT: new URL(base).port }, stdio: ['ignore', 'pipe', 'pipe'] });
    base = await new Promise((resolve, reject) => {
      let output = ''; const timer = setTimeout(() => reject(new Error('Vite startup timeout')), 10000);
      vite.stdout.on('data', data => { output += data; const match = output.match(/http:\/\/127\.0\.0\.1:\d+\//); if (match) { clearTimeout(timer); resolve(match[0]); } });
      vite.once('exit', () => { clearTimeout(timer); reject(new Error('Vite exited before ready')); });
    });
  }
  browser = await chromium.launch({ headless: true, ...(process.env.DAIMON_SMOKE_CHANNEL ? { channel: process.env.DAIMON_SMOKE_CHANNEL } : {}) });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const external = [], errors = [];
  page.on('request', req => { if (!req.url().startsWith(base)) external.push(req.url()); });
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(base); await page.getByText("Crie seu primeiro bot").waitFor();
  await page.getByRole('button', { name: "Novo bot", exact: true }).click();
  await page.getByLabel("Nome", { exact: true }).fill('Daimon Coder');
  await page.getByLabel("Descrição", { exact: true }).fill('Local smoke fixture');
  await page.getByLabel("Instruções", { exact: true }).fill('Answer briefly.');
  await page.getByRole("tab",{name:"Modelo",exact:true}).click(); await page.getByLabel("Provedor", { exact: true }).selectOption('openai');
  await page.getByRole("textbox",{name:"Modelo",exact:true}).fill('fake-model');
  await page.getByRole('button', { name: "Salvar bot", exact: true }).click();
  await page.locator(".console-heading h2").waitFor();
  await page.getByRole('button', { name: "Nova conversa", exact: true }).click();
  await page.getByLabel("Título", { exact: true }).fill('Inspect the workspace');
  await page.getByLabel("Pasta local", { exact: true }).fill(workspace);
  await page.getByRole('button', { name: "Salvar conversa", exact: true }).click();
  await page.getByLabel("Mensagem", { exact: true }).fill('Respond briefly');
  await page.getByRole('button', { name: "Enviar", exact: true }).click();
  await page.getByText("Concluído", { exact: true }).waitFor();
  await page.getByRole("tab",{name:"Atividade",exact:true}).click(); await page.getByText("Pensando", { exact: true }).waitFor(); await page.getByRole("tab",{name:"Chat",exact:true}).click();
  await page.getByText(answer, { exact: true }).waitFor();
  const select = async (bot = 'Daimon Coder', thread = 'Inspect the workspace') => {
    await page.getByRole('button').filter({ has: page.locator('strong', { hasText: bot }) }).click();
    await page.getByRole('button', { name: new RegExp(thread) }).click();
    await page.getByText(answer, { exact: true }).first().waitFor();
  };
  await page.reload(); await select();
  await page.getByLabel("Mensagem", { exact: true }).fill('Continue with the previous context');
  await page.getByRole('button', { name: "Enviar", exact: true }).click();
  await page.getByText("Concluído", { exact: true }).waitFor();
  await page.waitForFunction(text => [...document.querySelectorAll('.chat-message.assistant .markdown')].filter(p => p.textContent === text).length === 2, answer);
  assert.equal(continuity, true);
  await page.route("**/events/stream",route=>route.abort());
  await page.getByLabel("Mensagem", { exact: true }).fill('Wait so I can abort');
  await page.getByRole('button', { name: "Enviar", exact: true }).click();
  await page.getByText("Executando", { exact: true }).waitFor(); await page.getByText("Reconectando ao DAIMON…",{exact:true}).waitFor();
  await page.getByRole('button', { name: "Parar ■", exact: true }).click();
  await page.getByText("Cancelado", { exact: true }).waitFor();await page.unroute("**/events/stream");
  await page.getByLabel("Ações da conversa Inspect the workspace").click();await page.getByRole('button', { name: "Editar conversa", exact: true }).click();
  await page.getByLabel("Título", { exact: true }).fill('Reviewed workspace');
  assert.equal(await page.getByLabel("Pasta local", { exact: true }).getAttribute('readonly'), '');
  await page.getByRole('button', { name: "Salvar conversa", exact: true }).click();
  await page.getByRole('button', { name: /Reviewed workspace/ }).waitFor();
  await page.getByLabel("Ações do bot").click(); await page.getByRole("button",{name:"Editar bot",exact:true}).click();
  assert.equal(await page.getByLabel("Instruções", { exact: true }).inputValue(), '');
  await page.getByLabel("Instruções", { exact: true }).fill('Updated instructions.');
  await page.getByLabel("Nome", { exact: true }).fill('Updated Coder');
  await page.getByRole('button', { name: "Salvar bot", exact: true }).click();
  await page.locator(".console-heading h2").waitFor();
  await page.reload(); await page.getByRole('button').filter({ has: page.locator('strong', { hasText: 'Updated Coder' }) }).click();
  await page.getByRole('button', { name: /Reviewed workspace/ }).click();
  await page.getByText(answer, { exact: true }).first().waitFor();
  await stop(child); child = startServer(serverPort); await ready(child);
  await page.reload(); await select('Updated Coder', 'Reviewed workspace');
  assert.equal(await page.locator('.chat-message.assistant').count(), 2);
  assert.equal(await page.locator('.chat-message.user').count(), 3);
  await page.getByRole('tab',{name:'Atividade',exact:true}).click();
  await page.getByText('Rotinas do Bot',{exact:true}).waitFor();
  await page.getByText('Nova rotina diária',{exact:true}).click();
  await page.getByLabel('Título da rotina',{exact:true}).fill('Native routine');
  await page.getByLabel('Conversa da rotina',{exact:true}).selectOption({label:'Reviewed workspace'});
  await page.getByLabel('Tarefa da rotina',{exact:true}).fill('Never execute through an external harness');
  await page.getByRole('button',{name:'Criar rotina',exact:true}).click();
  await page.getByText('Native routine',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Pausar rotina',exact:true}).click();
  await page.getByRole('button',{name:'Ativar rotina',exact:true}).waitFor();
  await page.reload(); await select('Updated Coder','Reviewed workspace');
  await page.getByRole('tab',{name:'Atividade',exact:true}).click();
  await page.getByRole('button',{name:'Ativar rotina',exact:true}).waitFor();
  await page.getByRole('button',{name:'Excluir rotina',exact:true}).click();
  await page.getByRole('button',{name:'Confirmar exclusão da rotina',exact:true}).click();
  await page.getByRole('tab',{name:'Chat',exact:true}).click();
  await page.locator(".topbar").getByRole("button",{name:"Configurações",exact:true}).click();await page.getByRole("tab",{name:"Modelos / Provedores",exact:true}).click();await page.getByText("Credenciais e conexão são configuradas fora do navegador.").waitFor();await page.getByRole("button",{name:"Fechar",exact:true}).click();
  if (process.env.DAIMON_SMOKE_SCREENSHOT) await page.screenshot({ path: process.env.DAIMON_SMOKE_SCREENSHOT, fullPage: true });
  await page.getByLabel("Mensagem",{exact:true}).fill("Fail this execution");await page.getByRole("button",{name:"Enviar",exact:true}).click();await page.getByText("Falhou",{exact:true}).waitFor();await page.getByRole("button",{name:"Tentar novamente",exact:true}).waitFor();assert.equal(await page.locator(".chat-message.assistant").count(),2);
  for (const width of [1440,1280,1024,600]) { await page.setViewportSize({width,height:900}); assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false); }
  await page.getByLabel("Recolher bots").click();
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  await page.getByLabel("Ações da conversa Reviewed workspace").click();await page.getByRole('navigation', { name: "Conversas", exact: true }).getByRole('button', { name: "Excluir conversa", exact: true }).click();
  await page.getByRole('button', { name: "Confirmar exclusão", exact: true }).click();
  await page.getByRole('dialog').getByRole('alert').waitFor();
  await page.getByRole('button', { name: "Cancelar", exact: true }).click();
  await page.getByRole('button', { name: "Nova conversa", exact: true }).click();
  await page.getByLabel("Título", { exact: true }).fill('Disposable workspace');
  await page.getByLabel("Pasta local", { exact: true }).fill(workspace);
  await page.getByRole('button', { name: "Salvar conversa", exact: true }).click();
  await page.getByLabel("Ações da conversa Disposable workspace").click();await page.getByRole('navigation', { name: "Conversas", exact: true }).getByRole('button', { name: "Excluir conversa", exact: true }).click();
  await page.getByRole('button', { name: "Confirmar exclusão", exact: true }).click();
  await page.getByRole('button', { name: /Reviewed workspace/ }).waitFor();
  await page.getByLabel("Ações do bot").click(); await page.getByRole("button",{name:"Excluir bot",exact:true}).click();
  await page.getByRole('button', { name: "Confirmar exclusão", exact: true }).click();
  await page.getByRole("heading",{name:"Bem-vindo ao DAIMON",exact:true}).waitFor();
  assert.equal((await (await page.request.get(base+"api/v1/bots")).json()).bots.length,0);
  assert.ok(calls >= 3); assert.deepEqual(external, []); assert.deepEqual(errors, []);
  console.log(`PASS: ${devMode ? 'Vite proxy' : 'embedded UI'}, persisted transcript, two-turn provider continuity, reload/restart, Abort user-only, safe deletion, CRUD, SSE, responsive layout, no external browser requests.`);
} finally {
  await browser?.close();
  if (vite) { vite.kill('SIGINT'); await new Promise(resolve => { if (vite.exitCode !== null) resolve(); else vite.once('exit', resolve); }); }
  if (child.exitCode === null) await stop(child);
  await new Promise(resolve => provider.close(resolve));
  await rm(temp, { recursive: true, force: true });
}
