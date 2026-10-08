import {afterEach,expect,it,vi} from 'vitest';
import {api,decodeComputer,decodeComputerProfile,decodeSession} from './client';
import {sessionFixture} from '../test-fixtures';
afterEach(()=>vi.unstubAllGlobals());
it('projects Computer metadata and optional binding without private transport fields',()=>{
 const info={id:'cua',backend:'cua-local',status:'connected',capabilities:[{id:'click',tool:'mcp__cua__click',class:'navigate',available:true}],busy:true,controller_session_id:'run',command:'private-executable',screenshot:'private-image'};
 expect(decodeComputer(info)).not.toHaveProperty('command');expect(decodeComputer(info)).not.toHaveProperty('screenshot');
 expect(()=>decodeComputer({...info,status:'unrestricted'})).toThrow();expect(()=>decodeComputer({...info,capabilities:[{...info.capabilities[0],tool:'mcp__other__click'}]})).toThrow();
 expect(decodeSession({...sessionFixture,computer:{id:'cua',backend:'cua-local',capability_ids:['click'],handle:'private'}}).computer).toEqual({id:'cua',backend:'cua-local',capability_ids:['click']});
 expect(()=>decodeComputerProfile({enabled:true,backend:'cua-local'})).toThrow();
});
it('requires complete Computer typing review and rejects foreign or dangerous presentations',async()=>{
 const p={id:'approval-a',session_id:'session-a',tool:'mcp__cua__type_text',kind:'computer',target:'pid 1 window 2',warning:'Local desktop',preview:'"all text"',server_id:'cua',computer_id:'cua',backend:'cua-local',classification:'input',arguments:'private',screenshot:'private'};
 const reply=(value:unknown)=>vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({approval:value}))));
 reply(p);expect(await api.approval('session-a')).not.toHaveProperty('arguments');
 reply(p);await expect(api.approval('session-b')).rejects.toThrow();
 reply({...p,preview:''});await expect(api.approval('session-a')).rejects.toThrow();
 reply({...p,classification:'dangerous'});await expect(api.approval('session-a')).rejects.toThrow();
 reply({...p,computer_id:'other'});await expect(api.approval('session-a')).rejects.toThrow();
});
