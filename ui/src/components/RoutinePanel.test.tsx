import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {afterEach,expect,it,vi} from 'vitest';
import * as client from '../api/client';
import {decodeRoutines,RoutinePanel} from './RoutinePanel';
const routine={id:'routine',bot_id:'bot',thread_id:'thread',title:'Review <script>',daily_at:'09:00',timezone:'UTC',enabled:true,next_at:'2026-10-10T09:00:00Z',state:'waiting_approval',session_id:'session'};
afterEach(()=>{cleanup();vi.restoreAllMocks();});
it('shows server-only limitation, approval status and opens the ordinary execution',async()=>{vi.spyOn(client,'request').mockResolvedValue({routines:[routine],server_only:true});const open=vi.fn();render(<RoutinePanel botID="bot" threads={[]} onOpen={open}/>);await screen.findByText('Review <script>');expect(screen.getByText(/Rotinas só rodam com o servidor/)).toBeTruthy();expect(screen.getByText(/Aguardando aprovação/)).toBeTruthy();fireEvent.click(screen.getByText('Abrir execução'));expect(open).toHaveBeenCalledWith('thread','session');expect(document.querySelector('script')).toBeNull();});
it('pause changes only schedule intent and does not resolve approval or abort',async()=>{const req=vi.spyOn(client,'request').mockResolvedValue({routines:[routine],server_only:true});render(<RoutinePanel botID="bot" threads={[]} onOpen={()=>{}}/>);fireEvent.click(await screen.findByText('Pausar rotina'));await waitFor(()=>expect(req).toHaveBeenCalledWith('/routines/routine','PUT',{enabled:false}));expect(req.mock.calls.some(([path])=>path.includes('approvals')||path.includes('abort'))).toBe(false);});
it('refuses malformed, duplicate and oversized metadata',()=>{for(const value of [{routines:[{...routine,state:'approved'}],server_only:true},{routines:[routine,routine],server_only:true},{routines:[routine],server_only:false},{routines:Array(33).fill(routine),server_only:true}])expect(()=>decodeRoutines(value)).toThrow();});
it('shows a missed occurrence with both timestamps without execution or approval effects',async()=>{
 const missed={state:'missed',scheduled_at:'2026-10-09T09:00:00Z',detected_at:'2026-10-09T10:00:00Z'};
 const req=vi.spyOn(client,'request').mockResolvedValue({routines:[{...routine,state:'active',session_id:undefined,missed}],server_only:true});
 const open=vi.fn();render(<RoutinePanel botID="bot" threads={[]} onOpen={open}/>);
 await screen.findByText('Perdida');expect(screen.getByText('Horário previsto (UTC):')).toBeTruthy();expect(screen.getByText('Detectada em (UTC):')).toBeTruthy();
 expect(document.querySelector('time[datetime="'+missed.scheduled_at+'"]')).toBeTruthy();expect(document.querySelector('time[datetime="'+missed.detected_at+'"]')).toBeTruthy();
 expect(screen.queryByText('Abrir execução')).toBeNull();expect(open).not.toHaveBeenCalled();expect(req.mock.calls.every(([,method])=>method==='GET')).toBe(true);
});
it('rejects malformed missed occurrence timestamps and states',()=>{
 for(const missed of [null,{state:'running',scheduled_at:'2026-10-09T09:00:00Z',detected_at:'2026-10-09T10:00:00Z'},{state:'missed',scheduled_at:'bad',detected_at:'bad'},{state:'missed',scheduled_at:'2026-10-09T10:00:00Z',detected_at:'2026-10-09T09:00:00Z'}]){
  expect(()=>decodeRoutines({routines:[{...routine,missed}],server_only:true})).toThrow();
 }
});
