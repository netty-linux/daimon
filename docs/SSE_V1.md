# SSE v1 — Phase 6

SSE is local transport for existing typed Session events. It is not provider token
streaming, a second event bus, message history or an execution/approval interface.

Phase 7's [Web UI](WEB_UI_V1.md) uses native EventSource, bounded safe event labels,
snapshot refresh on gap/error/end and cleanup without Abort. No new semantic events.

## Endpoint and lifecycle

`GET /api/v1/sessions/{id}/events/stream`, served only by the existing loopback
server. Empty request body required. Valid streams return 200, Content-Type
text/event-stream, Cache-Control no-cache and nosniff. No CORS or manually forced
Connection header. JSON polling at GET /events remains unchanged.

Session existence, cursor syntax, Flusher/deadline capability and stream capacity
are checked before starting. Unknown session: 404 session_not_found. Wrong method:
405 with Allow: GET. Invalid cursor: 400 invalid_request. Limit: 429 stream_capacity.
Unsupported writer: 500 stream_unsupported. Shutdown: 503 runtime_unavailable if
observed before starting. These errors retain the normal safe JSON envelope.

Each successful write is flushed. The connection waits for Session notification,
heartbeat, request cancellation or server shutdown. No polling timer checks replay.
HTTP Shutdown/Close first signals SSE handlers to exit; application later closes
the Manager. Stream disconnect/write error ends observation only, never aborts or
fails the Session. Sessions still belong to the application context.

## Framing and sequence

Compact encoding/json data occupies one line. Example:

```text
retry: 3000

id: 41
event: tool_requested
data: {"sequence":41,"kind":"tool_requested","step":2,"tool_index":1,"stop_reason":""}

id: 42
event: tool_completed
data: {"sequence":42,"kind":"tool_completed","step":2,"tool_index":1,"stop_reason":""}

: keep-alive

```

id is exactly the Session sequence, never a transport counter. event is the
whitelisted agentloop.EventKind, including its existing spelling (model_requested,
not model_started). data reuses the JSON polling projection: sequence, kind, step,
tool_index, stop_reason. Sequences are per Session, strictly increasing, start at
one and do not wrap. Event names and stop reasons are whitelisted; unknown values
are never reflected. JSON escapes embedded controls rather than breaking framing.

retry: 3000 is sent/flushed once when connecting. It is a suggested EventSource
reconnection delay in milliseconds, unrelated to provider retry. Transport events
and comments carry no id and never change the runtime sequence or saved cursor.

## Replay and Last-Event-ID

Cursor precedence:

1. One present Last-Event-ID header, including rejecting an empty value.
2. Otherwise a single `after` query value.
3. Otherwise zero, requesting all retained events.

Cursors are 1–20 decimal ASCII digits representing uint64. Negative, signed,
whitespace, empty, repeated or overflow values are rejected before streaming.
Unknown query keys, duplicate after, malformed URL escapes and duplicate header
values are also rejected. A valid header supersedes the query cursor value, even
if that value is invalid; query structure must still be valid.

Last-Event-ID: 41 replays >41, never 41. after >= latest produces no semantic events
until a greater sequence exists; a terminal Session still ends immediately. A future
cursor is accepted, matching EventsSince semantics; clients should save actual IDs.
Buffers are ephemeral: after process restart the old Session ID is not found.

## Gaps

If the requested predecessor was evicted, emit this transport event first:

```text
event: replay_gap
data: {"requested_after":12,"oldest_available":30,"latest_available":55}

```

Then continue from the oldest available event. Do not claim complete replay.
A client may GET /api/v1/sessions/{id} for current safe state, accept unavailable
history and continue. Slow delivery can produce another gap on the next replay.
The notification channel stores no events; bounded ring is the sole source of truth.

## Waiting and multiple clients

Manager.ObserveEvents(id) atomically captures the current change channel and
status/terminality. Server captures BEFORE calling EventsSince, delivers its copied
suffix, then waits on the captured channel if nonterminal. Events/finalization
between observation, replay and select close that generation, preventing lost
wakeups. After wakeup the server captures another observation and replays again.

Record closes and renews the Session's change channel under the existing mutex.
Final publication closes the final channel after cleanup, including startup failures
without loop events. No registration map, unsubscribe, worker per subscription,
unbounded queue, subscriber event copies in the runtime, consumption or global bus.
Each connection has its own cursor and at most a bounded replay snapshot. Observers
do not delete/acknowledge events globally, and hold no Manager lock during writes.

Heartbeat is `: keep-alive` followed by a blank line every 15 seconds while waiting.
It queries no replay and increments no sequence. Intervals are private immutable
server options; tests inject short intervals rather than waiting 15 seconds.

## Terminal and failures

Only finalized completed/failed/aborted states end a normal stream. A loop_stopped
event alone does not imply cleanup finished. Send remaining retained events, then:

```text
event: stream_end
data: {"status":"completed","last_sequence":42}

```

Flush and return/EOF. This no-id transport envelope is intentional: runtime startup
can fail without loop events, and cancellation/cleanup can finalize after
loop_stopped. It does not duplicate an AgentLoop event or expose final output.
Clients must stop automatic reconnection after stream_end (e.g. close EventSource).

After streaming starts, unexpected dependency errors/panics, invalid semantic names
or an oversized semantic payload emit a best-effort no-id transport_error with
`{"code":"internal_error"}`, then close. No raw errors/stack/payload are included.
No JSON is truncated. A failed/short write or Flush ends silently because that
connection may no longer accept a transport_error. Neither path changes Session.

## Reconnect algorithm

1. Connect (zero cursor initially).
2. Process semantic frames in order and remember the last successfully processed id.
3. On transient disconnect, reconnect with Last-Event-ID or after.
4. On replay_gap, retrieve GET session, accept lost predecessors and process suffix.
5. On stream_end, finish observation and stop reconnecting.

The server cannot know which flushed frames the application processed; delivery is
not exactly-once acknowledgement. Clients own cursor persistence/deduplication.
Phase 7 implements this client algorithm; see Web UI v1.

## Limits and security

64 simultaneous streams globally per Server, immediate 429 on saturation; slots
are released on every handler exit. Existing Manager limits remain: event capacity
1–16384 (CLI 1024), retained-session capacity 1–1024 (CLI 128). A replay snapshot is
bounded by ring capacity; there is no separate queue. SSE payload JSON is at most
4096 bytes per frame; frame overhead is fixed/bounded. The whole live stream has
no total byte cap; buffers and individual operations are bounded.

Every write+flush refreshes a 5-second deadline through http.ResponseController,
so SSE does not inherit the ordinary HTTP 15-second absolute response timeout.
This bounds a cooperative blocked TCP writer and frees admission slots; a custom
writer must implement deadline behavior correctly. Idle waiting is interruptible
by shutdown/request context. Normal HTTP timeouts remain unchanged. Deadline is
cleared after every successful flush (before idle waiting) and on return, so it
does not expire between heartbeats. A buffered replay remains deliverable if later ring eviction
occurs; further missed events are signaled by the next replay gap.

Only existing event metadata and controlled transport metadata are exposed. Never
instructions, messages, model free text, paths, arguments, results, keys, provider
raw responses or errors. No logging/body/stack emission, CORS, remote auth/public
exposure, TLS or stronger process/memory isolation claim. Existing local trusted
client and workspace assumptions remain. No WebSocket, MCP, Memory, subagents or external agents. Phase 8 transcript
persistence uses separate GET messages, never SSE content.

Phase 8: terminal publication occurs after assistant persistence/cleanup. Clients
refetch the dedicated messages endpoint after terminal snapshot/stream_end.
Runtime events (including final_answer) never become transcript messages.


## Phase 9 Web Approval

Existing approval_requested is published after pending review registration;
approval_granted/approval_denied remain unchanged typed events. Snapshot waiting
status triggers GET pending, and a later event sequence can identify another
sequential review. No approval ID, target, preview or raw arguments are added to
the SSE DTO. Disconnect/reconnect never decides or aborts. Abort/deadline/shutdown
can terminate approval without a denial event, preserving existing lifecycle.

See [Web Approval v1](WEB_APPROVAL_V1.md) for runtime ownership, complete deliberate previews, bounded pending records, strict HTTP decisions, SSE ordering and cancellation. Public snapshots/events remain free of approval content; GET pending is a separate deliberate review surface. Terminal providers remain supported. Writes require explicit process capability and exact contract approval; HTTP cannot grant capabilities.


## Phase 12 Computer activity

Computer reuses the existing typed model/tool/approval/loop lifecycle events. No
new raw payload or screenshot event is introduced. Optional Computer metadata is
in the REST Session snapshot only; deliberate target/text is in pending approval
only. SSE continues to omit names, IDs of model calls, arguments, outputs, images
and secrets. See [Computer Use v1](COMPUTER_USE_V1.md).

## Phase 13 media separation

Computer frames, media tickets, view/control capabilities and human input never
enter Session SSE or replay buffers. [Computer View v1](COMPUTER_VIEW_V1.md) uses
dedicated same-origin sockets and safe REST ownership state. Existing typed
agent tool events continue unchanged, including controlled failure while human
control owns input.

Phase 14 adds typed sandbox_creating, sandbox_ready, sandbox_cleanup_started,
sandbox_cleanup_completed and sandbox_failed. Existing safe event fields and
replay semantics remain unchanged; no guest refs/commands/secrets are emitted.


## Phase 16 Environment lifecycle

The same bounded typed event ring additionally accepts environment_hydrating,
environment_ready, environment_saving, environment_saved and environment_failed.
No IDs, paths, contents, manifests, private refs, URLs or error strings are included.
Session metadata remains authority, including a committed revision even if later
cleanup fails. See [Persistent Environments](PERSISTENT_ENVIRONMENTS_V1.md).
