# Conversation History v1 — Phase 8

Conversation History != Memory. Thread identifies a persistent conversation;
Conversation Store owns its explicit transcript; Session remains ephemeral
execution; AgentLoop remains execution authority. No knowledge extraction,
summarization, embeddings, cross-thread recall or implicit memory is introduced.

## Domain and ordering

`internal/conversations.Message` contains restricted opaque ID, ThreadID, monotonic
Sequence, closed Role (`user`/`assistant`), exact Content, UTC CreatedAt and SessionID.
SessionID is a restricted string correlation value, avoiding a domain/runtime
dependency cycle. Sequence starts at 1 per Thread; timestamps never determine order.
Content must be valid UTF-8, nonblank and bounded by bytes. Whitespace and newlines
are preserved exactly. Messages cannot be edited or individually deleted.
Append assigns Sequence; callers supply zero. List returns independent values.
IDs and `(SessionID, Role)` are unique within a Thread, including across restart.
Assistant must immediately follow the user from the same Session. Failed user
turns may therefore be consecutive. There are no persisted system/tool messages.

## Persistence and migration

`serve` opens an explicit `conversations/` directory under its configured state
directory. Domain code never reads HOME/environment or creates directories.
Each `<thread-id>.json` is a strict version-1 envelope:

```json
{"version":1,"thread_id":"thread-a","messages":[{"id":"message-a","thread_id":"thread-a","sequence":1,"role":"user","content":"Hello","created_at":"2026-10-07T12:00:00Z","session_id":"session-a"}]}
```

Missing file means empty history. Existing threads.json is unchanged; no empty
arrays or migration writes are needed. Unknown versions, corruption, duplicate or
unknown keys, nulls, invalid IDs/roles/sequences/content and malformed Unicode
are explicit errors. Never overwrite corrupt data or silently recover/drop records.

Versioned per-Thread JSON was chosen over JSONL to retain the existing bounded
validate/rewrite contract without partial-record recovery. Each append rereads and
validates the entire bounded file, then writes an exclusive random temporary in
the same directory, chmod 0600, checks cancellation between chunks, Sync/Close,
revalidates the target and renames. Failure cleans the temporary; cleanup failure
is explicit. The valid target is never directly truncated. os.Root confines file
operations and observed symlink/nonregular targets are rejected.

Fixed lock stripes serialize append/read for a Thread inside this Store while
allowing distinct noncolliding Threads concurrently. One process/Store owns the
private controlled directory. This is not protection against external writers,
cross-process locking, atomic replacement on Windows or directory durability.
No claim of a filesystem sandbox or hard memory bound is made.

## Limits

| Accepted data | Bound |
| --- | --- |
| User content | 32 KiB UTF-8 bytes |
| Assistant content | 256 KiB UTF-8 bytes |
| Messages per Thread | 1024 |
| Encoded file per Thread | 16 MiB |
| HTTP page | 1–100 messages, default 40; encoded response at most 4 MiB |
| Browser transcript load | 1024 messages / 16 MiB of content |

Loop Budget remains authoritative and can impose lower content/history bounds.
Encoding escapes count toward the file/page bound. No silent truncation, eviction
or automatic deletion occurs when the Store reaches capacity. An assistant append
may hit capacity after its user was accepted; this is an explicit failed Session.
Fetching pages rereads the bounded whole file; pagination is transport bounding,
not a database index. Append is O(history size); these are deliberately small stores.

## Session integration and finalization

Manager receives optional `ConversationStore` (List/Append) through Dependencies;
serve injects the same Store into Manager and HTTP. Legacy internal callers with
nil retain their prior ephemeral behavior. Neither model.Model nor Tool.Execute
changes; the loop knows only private `InitialHistory []model.Message`, not stores.

Start validates/reserves Session and Thread first. With persistence configured it
checks Thread existence, loads/selects prior context and appends the exact user
before starting the owned worker. No Manager lock covers filesystem calls.
Preparation failure returns a classified error, releases the Thread and retains
a failed metadata-only Session identity (Aborted when canceled during preparation).
No model/tool execution starts.
Later Bot/provider/workspace resolution failure leaves the accepted user intact.

The worker keeps Result.FinalAnswer transiently. After a successful loop and
successful resource cleanup, it appends the exact assistant before publishing
Completed, waking terminal observers or releasing the Thread. No response text
is retained in public SessionSnapshot or the event buffer. Persistence failure
publishes Failed (`conversation_persistence`); loop StopReason/counters continue
to describe the actual loop, which may have completed successfully.

Failed/aborted runs create no fabricated assistant. Cancellation before commit
prevents append where observed. After a successful append commit, later Abort
cannot undo it and final status is Completed. Otherwise cancellation finalizes
Aborted. Dependencies must honor context; there is no forced interruption.

User persistence, execution and assistant persistence are not an ACID transaction.
A crash between them can leave a user without a response; Sessions/events/bindings
are not resumed. A crash after assistant commit can leave a durable response before
terminal metadata was delivered. No retry/replay worker is created.

## Context assembly

Read previous records before appending the new user. Reserve its full byte length,
one whole MaxFinalAnswerBytes response and two history slots. Walk backward from
the newest message while complete messages fit remaining bytes/count and their
role-specific runtime limit. Stop at the first that does not fit; retain that
contiguous suffix in ascending sequence, then append the new user once.
This may start with an assistant or contain consecutive failed user turns.
No message is split and excluded records remain stored. A budget unable to reserve
the new user plus full final bound fails explicitly before user append.

AgentLoop independently validates roles, nonblank UTF-8, role byte bounds and
total history capacity before calling Model.Generate, and clones messages for
each request. Tool calls/receipts still consume intra-Session budget; this policy
does not reserve every possible tool result. Existing full-batch checks still
fail closed. There is no token estimator or provider-specific context promise.
Bot instructions remain separate, resolved from immutable per-run Binding.
Later runs use the current Bot; prior messages are not rewritten after Bot edits.
Tool outputs are ephemeral, so later turns cannot replay prior file/tool context.

## HTTP API and idempotency

POST `/api/v1/sessions` accepts `{id, thread_id, message, message_id}`. Role is
always user. UI generates separate restricted Session/message IDs. Repeated IDs
are rejected, not reexecuted: same Session uses existing session_exists; same
message in a new Session returns 409 message_exists. For old callers omitting or
emptying message_id, Manager derives `message-` plus the first 16 SHA-256 bytes of
SessionID as hex, preserving restart-safe duplicate protection for that identity.
This is minimum idempotency, not a replay of the original HTTP response.

GET `/api/v1/threads/{id}/messages?after=0&limit=40` returns:

```json
{"messages":[],"next_after":0,"has_more":false}
```

Records have all Message fields above, ordered by sequence greater than after.
Use next_after for the next page. It equals after for an empty page. has_more
signals count/encoded-byte pagination; after beyond the last sequence is empty.
Only these two query keys are accepted, once each, unsigned decimal values;
limit must be 1–100. Missing Thread is 404, invalid query 400, unsupported format
version 422 unsupported_version, corrupt/unreadable history 500
conversation_unavailable. No partial response or raw error cause is returned.

DELETE Thread is blocked by active admission and by any persisted messages
(409 thread_has_history). Empty metadata can be removed. The Manager's short
admission reservation covers check/delete without holding its mutex over I/O.
No transcript/workspace cleanup, individual deletion or orphan reassignment exists.
Internal direct ThreadStore users must preserve these composition-level rules.

## UI and privacy

Selecting a Thread loads paginated persisted messages through useConversation.
Changing Thread/unmount cancels pending reads and ignores stale responses.
Start acceptance and Session status/finalization refresh the Store transcript.
User/assistant bubbles come only from stored records, never optimistic model text
or SSE events. Runtime activity/counters/failures are displayed separately.
Failed/aborted runs retain the user and show the associated Session category while
that Session is locally tracked. Reload/restart retains transcript but clears
ephemeral Session tracking; no durable failed-run status is added. Phase 9 recovers the selected active Session through metadata.
Load errors retain the last accepted transcript with a classified error display.

Content is deliberately private data returned ONLY by messages GET and supplied
to the runtime/model. Prior selected conversation messages are sent to the chosen
provider on each new turn; the composer discloses this. User text can contain
secrets: there is no automatic secret detector. State files are plaintext local
data, not encrypted. There are no analytics, telemetry, content logs or external
browser requests. Snapshots, SSE, errors, health/providers and Thread metadata do
not gain content. Unwrap causes are private and must never be serialized/logged.
React escapes text with preserved whitespace; no raw HTML/Markdown execution.
Loopback Host/Origin/Fetch Metadata and same-origin requests remain unchanged;
they are not authentication. Trusted local clients and one private writer remain
assumptions; public exposure/multi-user access are outside scope.

## Validation and non-goals

Offline Go tests cover exact append/reopen, ordering, duplicates, byte/count/file
bounds, strict corrupt/version validation, commit preservation/cleanup, Linux
permissions/symlinks, concurrent readers/writers, continuous model requests,
whole-message selection, failed/aborted/persistence paths, preparation cancellation,
confirmed-commit cancellation ordering and HTTP privacy/pages.
Frontend tests cover DTOs, pages, stale reads, failed/aborted user-only transcript,
terminal refetch, selection/reload and escaped rendering. The local browser smoke
checks two-turn provider context, reload/server restart, abort, deletion safety,
CRUD, SSE and responsive layout with a fake provider and no credentials.

No automatic Memory, summaries, semantic retrieval, tool-result persistence, message edits,
deletes, branching, regeneration, attachments, streaming tokens, web approval,
MCP, subagents, external agents, cloud sync or multi-user support. Next scoped
phase: Phase 12 — Intelligent Memory (not implemented). MCP tools are documented in [MCP v1](MCP_V1.md); their successful final response uses this existing persistence boundary. Phase 9 Web Approval is documented in [Web Approval v1](WEB_APPROVAL_V1.md).

## Phase 11 separate Memory

See [Memory v1](MEMORY_V1.md). Explicit scoped manual Memory has a separate
versioned Store and contextual snapshot, never an automatic transcript extraction.
History keeps immutable ordered user/assistant messages. Memory takes its bounded
share after user/final reservation and before the whole history suffix. No tool
receipts or Memory records are copied into the conversation Store.
