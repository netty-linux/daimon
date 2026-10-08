import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, APIError, decodeEvent, decodeSession, decodeMessage, safeError } from './client';
import { newID, bytes } from './types';

import { sessionFixture } from '../test-fixtures';
afterEach(() => vi.unstubAllGlobals());
describe('API boundary', () => {
  it('accepts exact read MCP approvals and rejects undeclared write presentations',async()=>{
    const p={id:'approval-a',session_id:'session-a',tool:'mcp__local__lookup',kind:'read',target:'mcp__local__lookup',warning:'External tool',preview:'',server_id:'local',classification:'read',arguments:'private'};
    vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({approval:p}))));
    expect(await api.approval('session-a')).not.toHaveProperty('arguments');
    vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({approval:{...p,classification:'write'}}))));
    await expect(api.approval('session-a')).rejects.toThrow("resposta inesperada");
  });
  it('projects deliberate approvals and rejects foreign Sessions or incomplete write previews', async () => {
    const p = { id: 'approval-a', session_id: 'session-a', tool: 'read_file', kind: 'read', target: '"file"', warning: 'Sent to provider', preview: '', arguments: 'private raw arguments' };
    const reply = (value: unknown) => vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ approval: value }))));
    reply(p); expect(await api.approval('session-a')).not.toHaveProperty('arguments');
    reply(p); await expect(api.approval('session-b')).rejects.toThrow("resposta inesperada");
    reply({ ...p, tool: 'replace_file', kind: 'write' }); await expect(api.approval('session-a')).rejects.toThrow("resposta inesperada");
    reply(null); expect(await api.approval('session-a')).toBeUndefined();
  });
  it('decodes deliberate transcript pages and rejects roles, ordering and foreign Threads', async () => {
    const message = { id: 'message-a', thread_id: 'thread-a', sequence: 1, role: 'assistant', content: 'Saved answer', created_at: '2026-10-07T12:00:00Z', session_id: 'session-a' };
    expect(decodeMessage(message).content).toBe('Saved answer'); expect(() => decodeMessage({ ...message, role: 'tool' })).toThrow();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ messages: [message], next_after: 1, has_more: false }))));
    expect((await api.messages('thread-a')).messages).toHaveLength(1);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ messages: [message], next_after: 1, has_more: false }))));
    await expect(api.messages('thread-b')).rejects.toThrow("resposta inesperada");
  });
  it('uses same-origin requests, projects fields and forwards cancellation', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ bots: [{ id: 'bot-a', name: '<script>bad</script>', description: '', provider_id: 'openai', model: 'test', tools: [], permission_mode: 'ask', instructions: 'private' }] })));
    vi.stubGlobal('fetch', fetch); const signal = new AbortController().signal;
    const bots = await api.bots(signal);
    expect(bots[0].name).toBe('<script>bad</script>'); expect(bots[0]).not.toHaveProperty('instructions');
    expect(fetch).toHaveBeenCalledWith('/api/v1/bots', expect.objectContaining({ signal, credentials: 'same-origin' }));
  });
  it('classifies errors without reflecting server text or unknown codes', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: 'thread_busy', message: 'private-secret' } }), { status: 409 })));
    await expect(api.start('session-a', 'thread-a', 'message')).rejects.toThrow("já tem uma execução ativa");
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: 'private-secret', message: 'private-secret' } }), { status: 500 })));
    try { await api.bots(); } catch (e) { expect(safeError(e)).not.toContain('private-secret'); expect((e as APIError).code).toBe('internal_error'); }
  });
  it('rejects malformed collections, snapshots, unsafe sequence numbers and event kinds', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"threads":null}')));
    await expect(api.threads()).rejects.toThrow("resposta inesperada");
    expect(() => decodeSession({ ...sessionFixture, status: 'invented' })).toThrow();
    expect(() => decodeSession({ ...sessionFixture, steps: -1 })).toThrow();
    expect(() => decodeEvent({ sequence: Number.MAX_SAFE_INTEGER + 1, kind: 'loop_started', step: 0, tool_index: 0, stop_reason: '' })).toThrow();
    expect(() => decodeEvent({ sequence: 1, kind: 'model_started', step: 0, tool_index: 0, stop_reason: '' })).toThrow();
  });
  it('generates valid prefixed IDs and measures UTF-8 bytes', () => { expect(newID('session')).toMatch(/^[a-z][a-z0-9-]{0,63}$/); expect(bytes('é')).toBe(2); });
});

it('loads manual Memory pages and rejects broken pagination or forged provenance',async()=>{
 const record={id:'memory-a',scope:'global',scope_id:'',kind:'fact',content:'chosen',tags:[],created_at:'2026-10-07T12:00:00Z',updated_at:'2026-10-07T12:00:00Z',provenance:{source_type:'manual'},extra:'discard'};
 const fetch=vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({memories:[record],next_after:'memory-a',has_more:true}))).mockResolvedValueOnce(new Response(JSON.stringify({memories:[{...record,id:'memory-b'}],next_after:'memory-b',has_more:false})));
 vi.stubGlobal('fetch',fetch);const list=await api.memories();expect(list).toHaveLength(2);expect(list[0]).not.toHaveProperty('extra');expect(fetch.mock.calls[1][0]).toContain('after=memory-a');
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({memories:[{...record,provenance:{source_type:'automatic'}}],next_after:'memory-a',has_more:false}))));await expect(api.memories()).rejects.toThrow();
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({memories:[],next_after:'memory-a',has_more:true}))));await expect(api.memories()).rejects.toThrow();
});
