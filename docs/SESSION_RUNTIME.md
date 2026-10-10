# Session Runtime — Phase 4

Implemented internal Go API. Phase 5 adds an external local HTTP transport without
changing these contracts; Phase 6 adds change notifications for SSE observation.
Phase 8 adds optional injected ConversationStore for persistent transcript/context.
Phase 7 Web UI consumes HTTP/SSE without changing these runtime contracts.
Session is ephemeral execution; Thread remains persistent identity/metadata.
Transcript is owned separately; see [Conversation History v1](CONVERSATION_HISTORY_V1.md).

## Construction and ownership

`NewManager(Dependencies, Options)` requires BotReader/ThreadReader (Get only),
configured Provider Registry and Config resolver. Approval resolver is required
when a Bot declares reads/writes. Registry factories are copied before concurrent
execution. Readers must support concurrent Get; changes to sequential domain stores
need caller ownership. Factories/resolvers must return per-session resources and
honor context. No core dependency reads environment, HOME or a secret store.

Options have no defaults: explicit valid agentloop.Budget, EventCapacity 1–16384
and MaxSessions 1–1024. Capacity includes terminal sessions retained until process
exit; no eviction/deletion API. Manager contains no global state.

Start takes caller SessionID/ThreadID, one bounded nonblank UTF-8 message and
optional mutually exclusive EnableReplaceFile/EnableCreateFile flags. It reserves
IDs/Thread atomically, then, when ConversationStore is configured, verifies Thread,
loads/selects history and persists user before returning a created Snapshot.
Preparation errors are synchronous and retain failed metadata without execution;
cancellation during preparation records Aborted and preserves context/Abort identity.
Other resolution happens
in the owned worker: failures are visible in Wait/Get. All IDs use the restricted
existing ID syntax; duplicates are rejected permanently, not silently restarted.

One active Session per Thread includes created/startup, waiting approval and cleanup.
Different Threads can run concurrently. This does not prevent two Threads/managers
sharing a workspace: caller must serialize shared-root writes and approval streams.

## Resolution and binding

1. Read/validate Thread and its returned identity.
2. Read/clone/validate referenced Bot and identity.
3. Snapshot BotID, ProviderID, Model, Instructions, copied Tools and PermissionMode.
4. Check declared capabilities and write exposure.
5. Require an absolute workspace reference and open it through workspacefs.Open.
6. Resolve frozen factory and injected transport config.
7. Construct Model, exactly the declared tools and the existing policy.Authorizer.
8. Revalidate workspace binding, then run the unchanged agentloop with explicit Budget.

No missing Thread/Bot/provider/tool/root falls back. Relative workspace metadata
can be persisted, but cannot execute here without an explicit future base resolver.
Canonical resolution is internal and never rewrites Thread. Tools use existing
os.Root and contract boundaries; no new direct file access tool is added.

Config(ctx, ProviderID) supplies endpoint/key/client/response limit separately from
Bot/Thread/Binding. Session sets Model/Instruction from Binding; conflicting nonempty
resolver values are errors. Response limits must be positive, with vendor URL/key
validation in factories. Groq forwards optional bounded instructions through its
existing compatible adapter, keeping endpoint/default model behavior.

Binding is immutable for this run. Editing Bot does not change active Model, tools
or policy. Later Sessions resolve the current Bot again. No conversation-wide Bot
version is fixed: later turns use the current Bot and bounded previous transcript.
Binding(id) returns private configuration with an owned Tools slice, no credentials,
model instance, tool implementation, client or root handle.

## Capability and permission boundary

Fixed catalog: read_file/list_dir = read; replace_file/create_file = write;
echo = other. No name heuristics, unknown capabilities, wildcards or dynamic tools.
read-only rejects any non-read capability, including echo. ask still uses existing
WorkspacePolicy. Reads require individual approvals; missing providers/reviewers
prevent startup. Writes require Linux, matching Start exposure flag and the existing
full-preview reviewer. Flags do not grant permission and cannot be combined.

Write contracts retain immutable proposals, per-call one-use approval, exact bytes,
deadline/cancellation, symlink/hardlink checks, cleanup and platform limits. Sizes
match current CLI: 64 KiB content, 1000 lines, 4096-byte path, 1 MiB full preview.
Root ownership assumptions and external mutation limitations remain unchanged.

## Lifecycle and context

Transitions: created -> running/failed/aborted; running -> waiting_approval/completed/
failed/aborted; waiting_approval -> running/failed/aborted. No terminal restart/resume.
StartedAt is UTC admission/startup time; FinishedAt is after finalized cleanup.
ApprovalRequested/Granted/Denied update status through original typed events.
Cancellation during approval can end directly as aborted without a denial event.

Start context owns startup/run. Abort cancels with ErrAborted and terminal errors
also preserve context.Canceled; external cancellation is distinguishable because
it has no ErrAborted. Caller deadlines mark aborted; loop budget timeouts mark failed
with original StopReason/limit identity. Success after cancellation is not accepted.
Abort after a terminal state returns NotRunning. An Abort during final cleanup or before assistant commit may
win lifecycle finalization even if the loop completed: StopReason/counters describe
the actual loop, while Session status records cancellation. A confirmed assistant
append wins later cancellation and publishes Completed. Commits are never undone.

Loop MaxRunDuration starts at Loop.Run, including approvals. Startup uses caller
deadline/context rather than a competing timer. Get-style reader APIs and external
callbacks must cooperate; forced interruption of blocking dependencies is not
possible. Close(ctx) rejects future starts, aborts workers and joins until ctx ends.
After timed-out Close, callers may Wait/Close again. No fire-and-forget scheduler.

Mutexes cover local state only, never filesystem, model, tool, factory/config or
approval calls. Resources close before active Thread reservation is released.
Cleanup errors remain failures. Guards convert model/tool/authorizer panics into
controlled errors before unwinding the loop; startup/cleanup have the same boundary
defense. No panic value/stack is retained, no retries, and core interfaces remain intact.

## Events, replay and privacy

Event wraps only Sequence and agentloop.Event. There is no second semantic system,
timestamp payload or added model data. Positive bounded ring evicts oldest events;
sequences start at one and are per Session.

EventsSince(id, after) returns a copied ordered suffix with sequence >after,
FirstAvailable, LastSequence and Gap. Gap=true explicitly indicates predecessors
were evicted. Zero requests the beginning; after>=last returns an empty suffix,
including future cursors. Phase 5 projects this replay into bounded JSON polling;
Phase 6 adds SSE using the same ring and semantics; see [SSE v1](SSE_V1.md).

ObserveEvents(id) atomically returns Changed (read-only generation channel), Status
and Terminal. Capture BEFORE EventsSince and wait only when nonterminal. Record
closes/renews the channel and final publication closes its last generation, under
the existing mutex. Finalization after cleanup wakes observers even on startup
failure with zero events. No queues, subscription registry, event consumption,
unsubscribe or goroutines are created by observation. Multiple clients share signals
but own independent cursors. Channel wakeup is a hint to re-read, never event storage.

Get returns value metadata only: identities/status/times, binding IDs/mode/tool
count, last sequence, loop StopReason and loop counters/error category. No model
text, tool names, workspace, instructions, prompt, output, history or key. Private
Binding is separately deliberate. Model/approval data never enters buffered events.

Wait returns terminal metadata and a controlled typed Error. Categories are
whitelisted; Error() omits cause text. Is/As/Unwrap preserve identity, but raw causes
may be sensitive: do not log/serialize them or complete config/error structs.
ToolCalls follows the current loop definition, including controlled denied attempts;
it is not a count of Execute calls or successful commits.

FinalText/History exist transiently in the loop and are not retained by Manager.
Session error identity and bounded metadata/binding/events remain until process
exit. With ConversationStore configured, FinalAnswer is passed privately to assistant
Append after successful loop/cleanup and before Completed/releasing the Thread.
Nothing persists credentials, Sessions, events or automatic Thread updates.
Restart loses active Sessions/buffers; Bot/Thread/conversation stores survive.

## Validation

Offline tests use Scripted models, httptest, actual temporary Bot/Thread stores,
controlled config/readers/reviewers and channel barriers. Cover strict startup,
snapshot mutation isolation, lifecycle, approval denial/waiting, ring gaps,
concurrent observation/Abort, per-Thread exclusion, shutdown, cancellation,
late model success/timeouts, panic redaction and privacy. Linux tests cover both
approved/denied create and replace paths. No real keys, internet or user home files.
Full Linux formatting/vet/test/race/demo remain mandatory before delivery.

## Phase 8 conversation boundary

ConversationStore is an injected List/Append interface; nil preserves legacy
internal callers. It introduces no dependency from loop/model/tools to stores.
Start accepts optional MessageID; if omitted it derives a stable ID from SessionID.
Duplicate message/session-role records survive restart and prevent another model
execution. Prior context is an ordered newest suffix of whole user/assistant text,
reserving new user and full final-answer budget; InitialHistory is revalidated and
cloned by the loop. System instructions still come from immutable Binding.

User persistence/context load failures return safe Persistence/HistoryResolution/
DuplicateMessage categories. Later startup/model/cleanup failure and Abort leave
user intact without an assistant. Assistant append failure yields Failed with
conversation_persistence even if loop StopReason is completed. No ACID transaction:
crash can leave an orphan user or a committed response before terminal publication.
Final content remains absent from snapshots/events/errors. No tool-result history.
WithIdleThread reserves admission for HTTP empty-Thread deletion; callbacks do
filesystem I/O outside Manager locks. Persistent-history deletion is blocked.


## Phase 9 Web Approval

Manager owns one ephemeral pending record per Session and at most 128 decision
identities per Session. WebApprovals selects an adapter for the existing read and
write approval interfaces. The waiter holds no Manager lock; one buffered channel
or the original run context wakes it. Resolution, Abort and Close serialize state
under the Manager lock, while context checks reject expired approvals. One allow
consumes only the exact current call; all batch decisions precede every Execute.
No pending state is persisted. ActiveSession returns only metadata for reload.

See [Web Approval v1](WEB_APPROVAL_V1.md) for runtime ownership, complete deliberate previews, bounded pending records, strict HTTP decisions, SSE ordering and cancellation. Public snapshots/events remain free of approval content; GET pending is a separate deliberate review surface. Terminal providers remain supported. Writes require explicit process capability and exact contract approval; HTTP cannot grant capabilities.

## Phase 10 MCP integration

See [MCP v1](MCP_V1.md). Application-owned stdio clients supply immutable namespaced tools to Session capability resolution. Explicit local read classification still requires the existing one-shot human approval; write/other/unclassified tools are denied. MCP approval shows server/tool/read and external-process warning without raw arguments. GET /api/v1/mcp/servers and /api/v1/mcp/tools expose readonly metadata; Settings / MCP and Bot checkboxes use these routes. They cannot configure processes or grant permissions. SSE, native contracts and private transcript persistence remain unchanged. Sessions close before MCP clients/processes. Phase 11 Memory is implemented separately below.

## Phase 11 Memory Foundation

See [Memory v1](MEMORY_V1.md). Optional `Dependencies.Memory` is an explicit List
reader; it requires positive `MaxMemoryContextBytes` and `MaxMemoryRecords`. Start
prepares and freezes scoped whole-record context before returning, before history
append and model construction. Reserve current user and full final, then Memory,
then history. Runtime reduces loop history capacity by the accepted framed bytes.
The compatible adapter sends separate Bot instructions → labeled contextual data →
history → current user. Memory corruption fails with memory_resolution; empty
Memory emits no additional message. No Memory content enters snapshots/events or
Binding. CRUD during an active Session affects future Sessions only. Capabilities,
workspace, native previews, one-shot approval and MCP rules are independent.


## Phase 12 ComputerBinding

See [Computer Use v1](COMPUTER_USE_V1.md). Optional application-owned Computers
resolves Bot.ComputerProfile into a private frozen per-run ComputerBinding, before
model execution. Disabled/absent profiles cannot resolve CUA via generic MCP. Exact
capabilities intersect discovered/configured tools, supported local actions and
Bot.Tools; read-only excludes input/navigation. Every Computer call uses one-shot
web approval with a specific target/typing presenter. No default grants.

Manager holds one exclusive physical-computer lease for the Session, including
observation and approval waits. Another Thread fails computer_busy before its
model; unavailability/unsupported setup fails computer_resolution. Closers revoke
bindings and release only after an active call unwinds; partial startup also
cleans up. Bot edits affect future resolution. Public optional Computer metadata
contains ID/backend/capability IDs only, with defensive slice copies. No handles,
raw profile, args or screenshots. These are frozen metadata, not live health.
Close order is HTTP, joined Sessions, ComputerManager, MCP processes. Abort can
retire CUA but cannot undo performed input. Core loop/events remain unchanged.

## Phase 13 independent viewers

ViewSession does not end or replace AgentSession. ComputerManager serializes
input across agent and human controllers; approved agent input during human
ownership returns a controlled failure, while observations and other tools
continue. Session completion/abort revokes human input and removes the agent
binding; viewers may remain view-only. Shutdown closes media authority before
joining Sessions and MCP. See [Computer View v1](COMPUTER_VIEW_V1.md).

## Phase 14 environment preparation

Optional frozen Bot SandboxProfile provisions before the first model call. Safe
Snapshot.environment metadata exposes creating/ready/cleaning_up/deleted/failed.
Abort during preparation cleans partial creation. All terminal paths close the
Computer binding then clean the disposable guest; unresolved cleanup stays tracked.
See [Sandbox Computers](SANDBOX_COMPUTERS_V1.md).

## Phase 15 cloud preparation

Frozen SandboxProfile placement selects an injected local or cloud backend before
model generation. Cloud never falls through to host/local. Environment metadata
adds safe placement/backend/resource preset and optional authoritative UTC expiry.
Cloud actions retain the Sandbox namespace, exact guest ID and individual approval.
The existing reverse closer order revokes binding/media before claim cleanup;
unresolved cleanup remains owned and consumes cloud quota. Same typed Sandbox
events; no cloud URLs, claims, credentials or free-form errors in snapshots/events.
See [Cloud Computers](CLOUD_COMPUTERS_V1.md).


## Persistent Environments — Phase 16

See [Persistent Environments v1](PERSISTENT_ENVIRONMENTS_V1.md) for durable Thread-owned local workspace
revisions, bounded official filesystem transfer, expected-revision commits,
explicit enable/delete, hydration before model, successful sync before compute
cleanup and assistant persistence. Existing Threads are not automatically imported.
Session failures/abort before commit preserve the prior revision; an already
confirmed workspace commit survives later cleanup/persistence failure or abort and
is reported explicitly. Compute remains disposable; no persistent processes.

Authorized inspection exception: a fixed DAIMON helper may run in the guest only
through official ProcessService, solely to verify metadata absent from the filesystem
API (inode/device/hard-link count). Fixed executable/code/arguments; no user/model
command strings, shell, arbitrary command tool, stdin or content transfer. Structured
bounded output, timeout and cancellation/kill-on-disconnect are required. Unsupported
or unproven metadata fails closed. All content transfer uses FilesystemService.
No paid CUA smoke, installation or login is automatic. Phase 17 — Background Tasks: parte Native Routines Foundation implementada; execução autônoma longa e runs em background fora do servidor continuam adiados.
Intelligent Memory, Subagents and External Agents stay deferred.
