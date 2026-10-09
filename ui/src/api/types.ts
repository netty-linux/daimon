import {copy} from '../i18n/copy';
export interface SandboxProfile {backend:'cua-local'|'cua-cloud';placement?:'local'|'cloud';image:'linux';runtime:'gvisor';browser:boolean;resources:'small'|'standard'|'medium';network:'outbound'}
export interface EnvironmentMetadata {placement?:'local'|'cloud';backend?:'cua-local'|'cua-cloud'|'cua-cloud';resources?:string;expires_at?:string;mode:'sandbox';state:'creating'|'ready'|'cleaning_up'|'deleted'|'failed';sandbox_id?:string;runtime:'gvisor';cleanup:'pending'|'complete'|'unresolved'}
export interface SandboxInfo {id:string;backend:'cua-local'|'cua-cloud';placement?:'local'|'cloud';expires_at?:string;status:'creating'|'running'|'deleting'|'deleted'|'failed';image:{alias:'linux';resolved?:string};runtime:'gvisor';owner_session:string;cleanup:'pending'|'complete'|'unresolved';orphan:boolean}
export interface SandboxCatalog {backends?:{backend:"cua-local"|"cua-cloud";runtime:"gvisor";available:boolean;reason?:string}[];sandboxes:SandboxInfo[];runtime:{backend:'cua-local';runtime:'gvisor';available:boolean;reason?:string}}
export interface ProviderSummary { id: string }
export interface ComputerProfile {enabled:boolean;backend:"cua-local";mcp_server_id:string}
export type ComputerClass="observe"|"navigate"|"input"|"system"|"dangerous";
export interface ComputerInfo {id:string;backend:"cua-local"|"cua-cloud";status:"configured"|"executable_missing"|"startup_failed"|"connected"|"unavailable";capabilities:{id:string;tool:string;class:ComputerClass;available:boolean}[];busy:boolean;controller_session_id?:string}
export interface ComputerBinding {id:string;backend:"cua-local"|"cua-cloud";capability_ids:string[]}
export interface Bot {
 sandbox_profile?:SandboxProfile;
 computer_profile?:ComputerProfile;
  id: string; name: string; description: string; provider_id: string; model: string;
  tools: string[]; permission_mode: 'ask' | 'read-only';
}
export interface BotInput extends Bot { instructions: string }
export interface Thread {
  id: string; bot_id: string; workspace: string; title: string; created_at: string; updated_at: string;
}
export const statuses = ['created', 'running', 'waiting_approval', 'completed', 'failed', 'aborted'] as const;
export type SessionStatus = typeof statuses[number];
export interface SessionSnapshot {
	persistent_workspace?:{id:string;starting_revision:number;committed_revision:number;committed:boolean;state:'hydrating'|'ready'|'saving'|'saved'|'failed'};
 environment?:EnvironmentMetadata;
 computer?:ComputerBinding;
  id: string; thread_id: string; status: SessionStatus; started_at: string; finished_at: string | null;
  last_event_sequence: number; stop_reason: string; error_category: string;
  steps: number; tool_calls: number; truncated_tool_results: number;
}
export const eventLabels = {
	environment_hydrating:copy['Hydrating workspace'],environment_ready:copy['Workspace ready'],environment_saving:copy['Saving workspace'],environment_saved:copy['Workspace committed'],environment_failed:copy['Workspace failed'],
  sandbox_creating:copy['Sandbox creating'], sandbox_ready:copy['Sandbox ready'], sandbox_cleanup_started:copy['Sandbox cleaning up'], sandbox_cleanup_completed:copy['Sandbox deleted'], sandbox_failed:copy['Sandbox failed'],
  loop_started: copy['Execution started'], model_requested: copy['Thinking · model requested'],
  model_responded: copy['Model responded'], tool_allowed: copy['Tool allowed'], tool_denied: copy['Tool denied'],
  tool_requested: copy['Tool requested'], tool_completed: copy['Tool completed'], tool_failed: copy['Tool failed'],
  approval_requested: copy['Approval required'], approval_granted: copy['Approval granted'], approval_denied: copy['Approval denied'],
  final_answer: copy['Final answer produced'], final_validation_requested: copy['Validating response'],
  final_validation_accepted: copy['Response accepted'], final_validation_rejected: copy['Response rejected'],
  recovery_requested: copy['Recovery requested'], recovery_model_requested: copy['Recovery model requested'],
  loop_stopped: copy['Execution finished'],
} as const;
export interface SessionEvent {
  sequence: number; kind: keyof typeof eventLabels; step: number; tool_index: number; stop_reason: string;
}
export interface ReplayGap { requested_after: number; oldest_available: number; latest_available: number }
export interface StreamEnd { status: 'completed' | 'failed' | 'aborted'; last_sequence: number }
export interface APIErrorEnvelope { error: { code: string; message: string } }
export const terminal = (status: SessionStatus) => ['completed', 'failed', 'aborted'].includes(status);
export const bytes = (text: string) => new TextEncoder().encode(text).length;
export const newID = (kind: 'bot' | 'thread' | 'session' | 'message' | 'memory' | 'routine') => `${kind}-${crypto.randomUUID()}`;
export interface ConversationMessage {
  id: string; thread_id: string; sequence: number; role: 'user' | 'assistant';
  content: string; created_at: string; session_id: string;
}
export interface MessagePage { messages: ConversationMessage[]; next_after: number; has_more: boolean }
export interface ApprovalPresentation {
  id: string; session_id: string; tool: string;
  kind: 'read' | 'write' | 'computer'; target: string; warning: string; preview: string;
  server_id?: string; classification?: 'read'|ComputerClass; computer_id?:string; backend?:'cua-local'|'cua-cloud';
}
export interface MCPServer { id: string; status: 'connected' | 'disabled' | 'unavailable' }
export interface MCPTool { name: string; server_id: string; description: string; classification: 'read' | 'write' | 'other'; available: boolean; permitted: boolean }
export const mcpName = (name: string) => name.length <= 64 && /^mcp__[a-z][a-z0-9-]{0,31}__[a-z][a-z0-9_-]*$/.test(name);

export type MemoryScope = 'global' | 'bot' | 'thread';
export type MemoryKind = 'fact' | 'preference' | 'instruction' | 'note';
export type MemoryInput = {id:string;scope:MemoryScope;scope_id:string;kind:MemoryKind;content:string;tags:string[]};
export type MemoryRecord = MemoryInput & {created_at:string;updated_at:string;provenance:{source_type:'manual'}};
export interface PersistentEnvironment {version:1;id:string;thread_id:string;revision:number;files:number;directories:number;bytes:number;manifest_hash:string;created_at:string;updated_at:string}
