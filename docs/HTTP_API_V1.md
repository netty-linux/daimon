# Local HTTP API v1 — Phases 5–11

Implemented using Go net/http. The server transports intent; Session Manager owns
runtime state; AgentLoop owns execution. SSE event observation is Phase 6;
Phase 9 adds deliberate Web Approval. Phase 7 adds a same-origin static Web UI at `/`.

## Starting and ownership

```sh
go run ./cmd/daimon serve
go run ./cmd/daimon serve --port 3000 --data-dir /private/daimon-state
```

Default address: `127.0.0.1:3000`; `--port` accepts 0–65535 (0 chooses an ephemeral
port). There is no host/address/public-bind flag. Server.Serve also verifies an
injected listener's actual TCP address is loopback, rejecting wildcard/LAN/public
addresses. New receives ready stores, registry, Manager and application context;
it never resolves HOME or reads environment. Registry IDs are copied at construction.

CLI chooses `~/.daimon` unless --data-dir is supplied. It creates the state directory
with mode 0700 where supported, rejects an observed symlink at that directory, and
opens domain stores `bots.json`, `threads.json`, `memory.json` and `conversations/`. Missing files
represent empty collections until their first write. Never creates sessions.json.
Existing private directories/ancestors and Windows ACLs remain operator-owned.
Do not run another writer/server against the same stores. No cross-process locking,
hostile ancestor isolation, Windows atomicity or directory durability is promised.

The CLI owns all dependencies. Shared BotStore/ThreadStore adapters serialize
operations, including Session resolution, with separate in-process mutexes. HTTP
Shutdown closes HTTP listeners/connections only; it does not close Manager.
SIGINT/SIGTERM stop admission and drain HTTP, then Manager.Close aborts and joins
workers. One explicit 10-second deadline covers draining and joining; if HTTP
draining expires, Close forces HTTP connections closed. Cancellation remains
cooperative: no forced interruption or rollback of committed effects is promised.
Every early-exit path after Manager construction also requests/join cleanup.

## Routes

| Method | Route | Success |
| --- | --- | --- |
| GET | /api/v1/health | 200 status |
| GET | /api/v1/providers | 200 providers |
| GET | /api/v1/bots | 200 bots |
| POST | /api/v1/bots | 201 Bot metadata |
| GET | /api/v1/bots/{id} | 200 Bot metadata |
| PUT | /api/v1/bots/{id} | 200 Bot metadata |
| DELETE | /api/v1/bots/{id} | 204 |
| GET | /api/v1/threads | 200 threads |
| POST | /api/v1/threads | 201 Thread |
| GET | /api/v1/threads/{id} | 200 Thread |
| GET | /api/v1/threads/{id}/messages?after=0&limit=40 | 200 private transcript page |
| PUT | /api/v1/threads/{id} | 200 Thread |
| DELETE | /api/v1/threads/{id} | 204 |
| POST | /api/v1/sessions | 202 created snapshot |
| GET | /api/v1/sessions/{id} | 200 snapshot |
| POST | /api/v1/sessions/{id}/abort | 202 abort_requested |
| GET | /api/v1/sessions/{id}/events?after=0 | 200 bounded JSON replay |
| GET | /api/v1/sessions/{id}/events/stream | 200 live SSE/replay |

Unknown routes/version prefixes return 404. Wrong methods return 405 and Allow.
Trailing slash is not an alias. IDs use the existing domain syntax (1–64 ASCII
bytes, lowercase initial letter, then lowercase letters/digits/hyphen).
Unknown query parameters are rejected; events support `after`; messages support `after` and `limit`.

## Input schemas

POST/PUT require Content-Type application/json (optional charset=utf-8). Body must
be one UTF-8 JSON object: no empty body, extra JSON, unknown/case-varied/duplicate
keys, null values or incorrect types. Empty arrays are valid only where domain
validation permits. GET/DELETE/abort require an empty body. Bodies are limited
before decoding. Credentials, provider config and write opt-in flags are not fields.

Bot POST/PUT uses the full domain schema:

```json
{"id":"coder","name":"Coder","description":"","instructions":"Use evidence.","provider_id":"groq","model":"openai/gpt-oss-20b","tools":[],"permission_mode":"ask"}
```

Description may be omitted. Other required fields follow Bot validation, including
nonblank instructions/model and explicit non-null tools. PUT replaces the entire
Bot; body ID must equal path ID. Output deliberately excludes instructions (there
is no instructions/binding endpoint); updating requires supplying complete known
configuration rather than round-tripping GET. Description/name/model/tools are
deliberate local configuration metadata, never logs. Do not insert secrets into
free text; the schema cannot recognize arbitrary credentials embedded in prose.

Thread POST/PUT uses:

```json
{"id":"conversation","bot_id":"coder","workspace":"/explicit/workspace","title":"","created_at":"2026-10-07T12:00:00Z","updated_at":"2026-10-07T12:00:00Z"}
```

Title may be omitted. Timestamps are supplied by the caller, nonzero UTC, and
UpdatedAt cannot regress. BotID, exact workspace and CreatedAt are immutable on
update. References are not resolved by CRUD: missing Bot/workspace can be stored
but fail runtime startup. Thread responses deliberately contain this configuration,
including the workspace reference; session/event/error/health responses omit it.
DELETE never cascades or aborts Sessions; Phase 8 blocks deletion of active Threads
or Threads with persisted messages. Empty Thread metadata can be deleted.

Session POST uses:

```json
{"id":"session-a","thread_id":"conversation","message_id":"message-a","message":"Inspect the task."}
```

ID is required; no automatic generation. Message must be nonblank valid UTF-8 and
at most 32768 bytes (also subject to the injected Manager's explicit budget).
Start reserves identity/Thread, resolves prior history and persists user before 202.
Missing Thread or history/user persistence failure rejects Start synchronously.
Bot/provider/tool/workspace/config failures occur asynchronously via snapshots.
HTTP does not pre-resolve or duplicate runtime validation. Request disconnect or
handler return does not cancel an admitted session: it belongs to SessionContext
until completion, abort or application shutdown. A failed response write can still
leave the admitted session running; retrying the same ID returns session_exists.

CLI supports built-in openai/groq, with endpoint/key read once from DAIMON_BASE_URL
and DAIMON_API_KEY. Bot supplies model/instructions; model environment defaults
used by existing chat/smoke commands are not session overrides. Configuration
validation remains in existing resolver/factories. No discovery, credentials over
HTTP, persistence of keys or fallback. Groq retains its fixed endpoint.

Read approvals in serve use individual Web review. Writes require mutually exclusive explicit server flags and exact contract preview approval. HTTP cannot enable capabilities. See [Web Approval v1](WEB_APPROVAL_V1.md).

## Output schemas

Health: `{"status":"ok"}`. Providers: `{"providers":[{"id":"groq"},{"id":"openai"}]}`,
lexically ordered IDs only. No model discovery endpoint or invented model list.
Collections: `{"bots":[...]}` / `{"threads":[...]}`, ordered by domain stores.
Bot metadata has id/name/description/provider_id/model/tools/permission_mode.
Thread has the seven fields above. Private instructions are never returned.

Session projection:

```json
{"id":"session-a","thread_id":"conversation","status":"created","started_at":"2026-10-07T12:00:00Z","finished_at":null,"last_event_sequence":0,"stop_reason":"","error_category":"","steps":0,"tool_calls":0,"truncated_tool_results":0}
```

Status follows Manager lifecycle. FinishedAt is null until finalized. StopReason
can be empty if loop has not run. ErrorCategory is the existing controlled runtime
category, never an underlying error. No binding, instructions, workspace, prompt,
final answer, history, provider raw response, arguments or results. ThreadID is a
local correlation identifier, not a credential. ToolCalls retains loop semantics,
including controlled denied attempts, not successful effects.

Abort success: `{"status":"abort_requested"}`. Repeated abort while active can
return 202; after terminal state it returns 409 session_finished. Unknown ID is 404.
An acknowledged abort is a request, not proof of synchronous completion; poll GET.

Events example:

```json
{"events":[{"sequence":1,"kind":"loop_started","step":0,"tool_index":0,"stop_reason":""}],"first_available":1,"last_sequence":1,"gap":false,"has_more":false,"next_after":1}
```

after is an unsigned decimal uint64; omitted means zero. Invalid/duplicate/overflow
cursors are 400. Replay sequences are strictly increasing and >after. At most 256
events are returned; follow next_after while has_more. last_sequence is the buffer
latest value, not a continuation cursor. gap explicitly reports predecessors evicted
from the Manager buffer; concurrent new events can cause a subsequent gap. Future
cursors return an empty suffix. No blocking Wait endpoint or long polling.

## SSE observation

GET /api/v1/sessions/{id}/events/stream returns text/event-stream with no-cache.
Runtime frames use id=Session sequence, event=existing event kind, compact JSON data
with the same safe fields as polling. Last-Event-ID takes precedence over after;
default zero. Invalid cursors fail with normal HTTP 400 before streaming. Explicit
replay_gap (requested_after/oldest_available/latest_available) precedes the retained
suffix, then delivery continues. Comments `: keep-alive` every 15s do not alter IDs.

Disconnect/write failure ends observation, not Session. Reconnect with the last
processed runtime ID. Finalized terminal state drains replay, sends no-id stream_end
(status/last_sequence), flushes and ends. Clients stop reconnection on stream_end.
Shutdown signals streams before HTTP draining. No-id transport_error is a safe
best-effort indication after unexpected transport/dependency failure; never raw text.

64 global streams per Server (429 stream_capacity), 4096-byte JSON payloads and
5-second refreshed write/flush deadlines. Unsupported flushing/deadline writers
return 500 stream_unsupported before starting. Streams wait on Session notifications,
not periodic replay polling, and hold no unbounded per-client queue. Full protocol,
precedence/gap/reconnect examples: [SSE v1](SSE_V1.md).

## Errors and limits

Stable envelope: `{"error":{"code":"bot_not_found","message":"bot not found"}}`.
Only classified fixed codes/messages, never arbitrary err.Error, Unwrap cause,
stack, path, request data or provider text.

| HTTP | Codes |
| --- | --- |
| 400 | invalid_request, invalid_bot, invalid_thread, invalid_session, id_mismatch |
| 404 | not_found, bot_not_found, thread_not_found, session_not_found |
| 405 | method_not_allowed |
| 409 | bot_exists, thread_exists, session_exists, thread_busy, thread_immutable, session_finished |
| 413 | body_limit, store_limit |
| 415 | unsupported_media_type |
| 422 | unsupported_version |
| 429 | session_capacity, stream_capacity |
| 500 | internal_error, stream_unsupported |
| 503 | runtime_unavailable |

Store corruption/I/O/cleanup failures are 500. Asynchronous runtime resolution
failures are a successful GET of a failed snapshot, not new synchronous HTTP errors.

Fixed HTTP limits: 256 KiB request body, 4 MiB encoded response, 256 events/page.
Domain limits remain 128 Bots/256 Threads and 2 MiB/store. CLI Manager retains at
most 128 sessions and 1024 events/session until restart; terminal sessions count.
Session budget uses an explicit caller invocation of DefaultBudget. Read header
timeout 5s; read/write 15s; idle 60s; configured header limit 16 KiB. No claim of
rigid memory/process isolation. HTTP request timeouts do not replace runtime budget.

## Local security boundary

No authentication; use only with trusted local clients/processes and controlled
private state/workspaces. Never expose with a public reverse proxy or LAN/public
listener. Loopback is a network boundary, not user/process authorization or a
complete defense against hostile browsers. No CORS headers/wildcard, no auth token,
no TLS termination, no request/body/access logging. net/http error logging is
discarded so panic stacks/request data cannot become logs. Responses use JSON,
no-store and nosniff. Component/handler panic responses contain only internal_error.

Phase 7 network admission requires localhost/literal-loopback Host, exact HTTP
Origin matching request authority, and same-origin/none Fetch Metadata when
present. Local clients without Origin remain supported. Rejections use 403
forbidden_origin before effects; CORS is unchanged. Static routes use local CSP,
no-cache index and immutable hashed assets. Unknown routes never fall back to index.
These checks do not authenticate local processes or justify broader trust.
Human approval, contracts and runtime policy remain independent of local network
access. Memory/subagents/external agents are intentionally deferred; MCP readonly metadata is described below. Phase 8 adds
private conversation history through the dedicated messages endpoint below.
See [Web UI v1](WEB_UI_V1.md) for build and limitations.

## Phase 8 conversation API

POST `/api/v1/sessions` additionally accepts optional `message_id` (restricted ID).
The UI supplies it separately from Session `id`; role is always user. Omission or
empty value derives a deterministic ID from SessionID for old clients. Duplicate
message in a new Session returns 409 message_exists, without model execution;
existing Session identity still returns session_exists. With persistence enabled,
Thread validation/history selection/user append occur before 202. Unknown Thread
returns 404 thread_not_found; load/append failures return 500 conversation_unavailable.
Preparation failure retains safe failed Session metadata; no retry execution.

GET `/api/v1/threads/{id}/messages?after=0&limit=40` returns
`{messages: [...], next_after: n, has_more: boolean}`. Each message has id, thread_id,
sequence, role user/assistant, exact content, created_at UTC and session_id. Only
this endpoint deliberately includes private conversation content. Metadata/SSE
schemas above remain unchanged. after is unsigned sequence, default 0; limit 1–100,
default 40. Reject unknown/duplicate/blank/malformed query values with 400.
Responses contain ordered complete records, bounded by count and encoded 4 MiB.
Use next_after while has_more; beyond-last cursors return an empty page. Missing
Thread 404; unknown format version 422 unsupported_version; corrupt/unreadable
history 500 conversation_unavailable. All errors omit raw causes/content/paths.

DELETE `/api/v1/threads/{id}` returns 409 thread_busy while admitted, or 409
thread_has_history when any messages exist. It can remove empty metadata without
creating/deleting a transcript. The check and metadata delete reserve admission
through Manager; no filesystem work holds its state mutex. There is no message
editing/deletion API or cascade. GET messages remains local-only, same-origin,
no-store, with no logs/analytics/telemetry; loopback admission is not authentication.
See [Conversation History v1](CONVERSATION_HISTORY_V1.md) for full contracts.


## Phase 9 Web Approval

GET `/api/v1/threads/{id}/session` returns the active safe Session or null.
GET `/api/v1/sessions/{id}/approval` returns the complete deliberate pending
presentation or null. POST `/api/v1/sessions/{id}/approvals/{approval_id}` accepts
only `{"decision":"allow"}` or `{"decision":"deny"}`. Successful consumption
returns 200; invalid decision returns 400, unknown approval 404, and resolved,
invalidated or wrong-Session approval 409 with fixed classified codes.
Browser POST requires matching Origin when Fetch Metadata is present; no CORS.
`--enable-replace-file` / `--enable-create-file` are mutually exclusive operator
flags, default false, never accepted through Session JSON or granted by decisions.

See [Web Approval v1](WEB_APPROVAL_V1.md) for runtime ownership, complete deliberate previews, bounded pending records, strict HTTP decisions, SSE ordering and cancellation. Public snapshots/events remain free of approval content; GET pending is a separate deliberate review surface. Terminal providers remain supported. Writes require explicit process capability and exact contract approval; HTTP cannot grant capabilities.

## Phase 10 MCP integration

See [MCP v1](MCP_V1.md). Application-owned stdio clients supply immutable namespaced tools to Session capability resolution. Explicit local read classification still requires the existing one-shot human approval; write/other/unclassified tools are denied. MCP approval shows server/tool/read and external-process warning without raw arguments. GET /api/v1/mcp/servers and /api/v1/mcp/tools expose readonly metadata; Settings / MCP and Bot checkboxes use these routes. They cannot configure processes or grant permissions. SSE, native contracts and private transcript persistence remain unchanged. Sessions close before MCP clients/processes. Phase 11 Memory is implemented separately below.

## Phase 11 Memory CRUD

See [Memory v1](MEMORY_V1.md) for the complete contract. `serve` opens memory.json
in the explicit data directory and injects the same Store into HTTP and Sessions.
GET/POST `/api/v1/memories`, GET/PUT/DELETE `/api/v1/memories/{id}` are deliberate
full-content endpoints. Inputs contain id, scope, scope_id, kind, content, optional
tags; browser timestamps/provenance are forbidden. PUT ID matches path, scope
remains immutable. List filters scope/scope_id and accepts exclusive `after` ID
and limit 1–100 (default 40), returning memories/next_after/has_more within 4 MiB.
No prompt/search endpoint. Scoped targets must exist; create/update and target
deletion share a reference mutex. Bot/Thread deletion with exact scoped records
returns 409 bot_has_memories/thread_has_memories, without cascade. Runtime and
HTTP errors use fixed categories; Memory content is absent from public metadata.


## Phase 12 Computer metadata

See [Computer Use v1](COMPUTER_USE_V1.md). GET /api/v1/computers returns computers;
GET /api/v1/computers/{id} returns fixed safe metadata or 404. Fields: id, backend,
status, capabilities (id/tool/class/available), busy, optional controller_session_id.
Statuses: configured, executable_missing, startup_failed, connected, unavailable.
Empty configuration yields []. Bodies/queries are refused, metadata mutations
405 and all computer action subroutes 404. No click/type/screenshot/launch RPC.

Bot CRUD adds optional computer_profile {enabled,backend,mcp_server_id}, requiring
all exact keys when present. Session start never accepts a profile/capability/
permission grant. Session DTO adds optional computer {id,backend,capability_ids},
with no live handles or raw data. Deliberate pending approval kind="computer" adds
computer_id/backend and observe/navigate/input/system classification plus complete
sanitized target and typing preview. Existing exact-call decision routes and SSE
apply; no new action/event channel or permanent grant exists.

## Phase 13 view and control

[Computer View v1](COMPUTER_VIEW_V1.md) defines GET computer `/view`, POST
`/views`, `/control/take`, `/control/release`, and scoped WebSocket upgrades
`/views/{view_id}/media` and `/input`. These are separate deliberate media/input
surfaces, not arbitrary desktop action RPCs. Tokens remain ephemeral and scoped;
Host/Origin/Fetch Metadata are validated. No frames or tokens enter Session DTOs
or SSE. Read-only same-origin state fetch can omit Origin; both sockets and
browser mutations require it.

## Phase 14 Sandboxes

Readonly GET /api/v1/sandboxes and GET /api/v1/sandboxes/{id} expose safe
owned metadata and runtime availability. No raw provisioning/admin endpoints.
Bot DTO optionally accepts a strict sandbox_profile; enabled host and sandbox
profiles are incompatible. Session DTO optionally includes environment metadata.

## Phase 15 safe cloud metadata

`GET /api/v1/sandboxes` retains local `runtime` and adds a bounded `backends`
array with local/cloud backend, gVisor runtime, boolean availability and fixed
reason category. Owned Sandbox info includes explicit placement and optional
UTC `expires_at`; Session environment includes frozen placement/backend/preset.
No credentials, identity, namespaces, claims, service URLs or raw error bodies.
Bot cloud profiles require `backend: cua-cloud` plus `placement: cloud`; Host
Computer profiles remain local. No cloud authentication mutation, raw provisioning
or pool management endpoint exists. See [Cloud Computers](CLOUD_COMPUTERS_V1.md).


## Phase 16 persistent workspace metadata

POST/GET/DELETE `/api/v1/threads/{id}/environment`: explicit empty-object creation,
metadata-only retrieval and idle confirmed deletion. Thread removal is blocked by
an existing Environment. No contents/manifest paths are returned. Session optionally
adds `persistent_workspace` with ID, starting/committed revision, committed bool and
fixed lifecycle state, distinct from ephemeral Sandbox `environment`. See
[Persistent Environments](PERSISTENT_ENVIRONMENTS_V1.md) for limits, failure and commit
semantics; no file browsing/import/export or shell endpoint is added.
