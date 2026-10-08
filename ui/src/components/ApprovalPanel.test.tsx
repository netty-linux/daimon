import { cleanup, fireEvent, render, screen, waitFor, act } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ApprovalPanel } from './ApprovalPanel';
import { App } from '../App';
import { api, APIError } from '../api/client';
import { sessionFixture } from '../test-fixtures';
import type { ApprovalPresentation } from '../api/types';
const waiting = { ...sessionFixture, status: 'waiting_approval' as const, last_event_sequence: 3 };
const presentation: ApprovalPresentation = { id: 'approval-one', session_id: waiting.id, tool: 'read_file', kind: 'read', target: '"src/\\u202eexample.go"', warning: 'Approved content will be sent to the provider.', preview: '' };
beforeEach(() => {
  Object.defineProperties(HTMLDialogElement.prototype, {
    showModal: { configurable: true, value: function() { this.setAttribute('open',''); } },
    close: { configurable: true, value: function() { this.removeAttribute('open'); } },
  });
  history.replaceState(null,'','/'); vi.spyOn(api,'approval').mockResolvedValue(presentation);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
it('shows the reversible path and submits Allow once while disabling buttons', async () => {
  let finish: (() => void) | undefined;
  const decide = vi.spyOn(api,'decideApproval').mockImplementation(() => new Promise(resolve => { finish = () => resolve(undefined); }));
  render(<ApprovalPanel snapshot={waiting} onAbort={() => {}} />);
  await screen.findByRole('dialog'); expect(screen.getByText(presentation.target)).toBeTruthy();
  expect(screen.queryByText('arguments')).toBeNull(); fireEvent.click(screen.getByRole('button',{name:"Permitir uma vez"}));
  expect(decide).toHaveBeenCalledWith(waiting.id,presentation.id,'allow');
  expect((screen.getByRole('button',{name:"Negar"}) as HTMLButtonElement).disabled).toBe(true);
  await act(async () => finish?.()); await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
});
it('denies once and displays an exact escaped full write preview without HTML', async () => {
  vi.mocked(api.approval).mockResolvedValue({ ...presentation, tool:'replace_file',kind:'write',preview:'Original: "a\\n"\nProposed: "<script>new</script>"\nLimits: 64 KiB' });
  const decide = vi.spyOn(api,'decideApproval').mockResolvedValue(undefined);
  render(<ApprovalPanel snapshot={waiting} onAbort={() => {}} />); await screen.findByText("Alterações propostas — contrato completo");
  expect(document.querySelector('.approval-preview')?.textContent).toContain('<script>new</script>');
  expect(document.querySelector('.approval-preview script')).toBeNull(); fireEvent.click(screen.getByRole('button',{name:"Negar"}));
  await waitFor(() => expect(decide).toHaveBeenCalledWith(waiting.id,presentation.id,'deny'));
});
it('refetches after a stale decision and never retries POST', async () => {
  vi.mocked(api.approval).mockResolvedValueOnce(presentation).mockResolvedValue(undefined);
  const decide = vi.spyOn(api,'decideApproval').mockRejectedValue(new APIError('approval_already_resolved'));
  render(<ApprovalPanel snapshot={waiting} onAbort={() => {}} />); fireEvent.click(await screen.findByRole('button',{name:"Permitir uma vez"}));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull()); expect(screen.getByRole('alert').textContent).toContain('recebeu uma decisão');
  expect(api.approval).toHaveBeenCalledTimes(2); expect(decide).toHaveBeenCalledTimes(1);
});
it('clears review on abort and fetches sequential approvals from later events', async () => {
  const abort = vi.fn(); const { rerender } = render(<ApprovalPanel snapshot={waiting} onAbort={abort} />);
  fireEvent.click(await screen.findByRole('button',{name:"Parar execução"})); expect(abort).toHaveBeenCalledOnce();
  rerender(<ApprovalPanel snapshot={{ ...waiting,status:'aborted' }} onAbort={abort} />); await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  vi.mocked(api.approval).mockResolvedValue({ ...presentation,id:'approval-two' });
  rerender(<ApprovalPanel snapshot={{ ...waiting,last_event_sequence:6 }} onAbort={abort} />); await screen.findByRole('dialog'); expect(api.approval).toHaveBeenCalledTimes(2);
});
it('recovers the active Session and pending approval from the selected Thread URL after reload', async () => {
  history.replaceState(null,'','/#thread=thread-a');
  vi.stubGlobal('EventSource',class { addEventListener() {} close() {} });
  vi.spyOn(api,'bots').mockResolvedValue([{ id:'bot-a',name:'Bot',description:'',provider_id:'openai',model:'fake',tools:['read_file'],permission_mode:'ask' }]);
  vi.spyOn(api,'threads').mockResolvedValue([{ id:'thread-a',bot_id:'bot-a',workspace:'/workspace',title:'Reload task',created_at:waiting.started_at,updated_at:waiting.started_at }]);
  vi.spyOn(api,'providers').mockResolvedValue([{ id:'openai' }]); vi.spyOn(api,'messages').mockResolvedValue({messages:[],next_after:0,has_more:false});
  vi.spyOn(api,'activeSession').mockResolvedValue(waiting); vi.spyOn(api,'session').mockResolvedValue(waiting);
  render(<App />); await screen.findByRole('dialog'); expect(api.activeSession).toHaveBeenCalledWith('thread-a',expect.any(AbortSignal));
  expect(api.approval).toHaveBeenCalledWith(waiting.id,expect.any(AbortSignal));
});
