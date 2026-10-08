# Web Approval v1 — Phase 9

The browser transports a human decision. Capability determines whether a tool
exists; static policy determines whether it needs approval; the ApprovalProvider
asks the human. Neither a Bot declaration nor an HTTP decision creates a capability.
Existing terminal commands, policy, AgentLoop, tools and write contracts are preserved.

## Composition and ownership

`serve` injects `sessions.Dependencies.WebApprovals: true`. Other Manager callers
retain their injected terminal/legacy providers unless they opt in explicitly.
The Session adapter implements the existing read ApprovalProvider and the separate
edit/create Reviewers, wrapping the existing composed ToolAuthorizer. It copies
the current call, uses its private identity, and discards that copy after authorization.
The loop still validates the complete batch before authorization and authorizes
every call before the first Execute. Tools remain synchronous and sequential.

Each Session owns at most one pending approval. A cryptographically random 128-bit
`approval-...` ID binds the decision to that Session and exact call. The pending
record contains the run context, immutable human presentation and a buffered
one-decision channel. Waiting never holds the Manager mutex and needs no polling
or additional worker. No approvals.json, persistent trust or second event bus exists.
The Manager keeps at most 128 decision IDs per Session, including invalidations;
existing MaxSessions bounds total retention until process restart. Full previews
are discarded when resolved/invalidated. The complete encoded presentation is
limited to 4 MiB minus 1024 bytes; write preview is limited to 1 MiB. An oversized,
incomplete or invalid UTF-8 presentation fails closed before approval can be granted.

## Deliberate presentation

GET pending is a private, deliberate human review surface, distinct from safe
public snapshots/events. It returns ID, Session ID, a fixed tool/type, the full
reversible Go ASCII-quoted relative target, warning, and preview. It never returns
raw ToolCall arguments, model call IDs, Bot instructions or provider configuration.

For read_file/list_dir the warning states that the approved result is sent to the
configured provider. No file contents appear in the read review. The path is
strictly parsed and cannot be silently shortened or terminal-formatted.
For replace_file/create_file, preview is the exact existing immutable contract
`Review.Display`: full reversible ASCII content, original/absence, proposed bytes,
diff where provided, bounds/version and existing warnings. It is not regenerated
in the browser, truncated or sent additionally to the provider. React renders it
as text in a scrollable preformatted block. Scrolling does not omit bytes.

Writes are disabled by default. An operator may start, on Linux:

```sh
go run ./cmd/daimon serve --enable-replace-file
go run ./cmd/daimon serve --enable-create-file
```

These flags are mutually exclusive, process-scoped capability configuration.
HTTP Session inputs cannot enable them; Bot ask/read-only and existing policy
still apply. A flag never approves a write. Existing contracts revalidate the
proposal and filesystem after review and before application, with their existing
single-attempt, conflict, symlink/hard-link, cancellation and cleanup rules.
Replacement does not promise exclusion of external writers or directory durability;
creation does not publish content atomically. Windows writes remain unsupported.

## HTTP

All routes use `/api/v1`:

| Method/path | Result |
| --- | --- |
| GET threads/{id}/session | `{"session": safeSnapshotOrNull}` for the active Session only |
| GET sessions/{id}/approval | `{"approval": presentationOrNull}` |
| POST sessions/{id}/approvals/{approval_id} | Strict `{"decision":"allow"}` or `{"decision":"deny"}`; 200 `{"status":"decision_accepted"}` |

Invalid bodies/decisions return 400. Unknown approval returns 404. Another
Session's approval, a resolved ID or an invalidated/nonpending approval returns
409 with classified fixed codes. Session lookup retains existing 404 semantics.
Duplicate/unknown keys, null, trailing JSON and oversized bodies are rejected.
A browser decision with Fetch Metadata must provide exactly one matching Origin;
cross-site/same-site and mismatched Origin/Host are rejected without resolving.
There is no CORS. Header-free trusted local clients retain the existing boundary;
this is not authentication against local processes.

A successful POST consumes the approval once; it does not promise successful
execution. Context cancellation or contract revalidation can still prevent effects.
Simultaneous tabs serialize resolution; exactly one decision is accepted. Failed
responses are followed by GET pending, never automatic POST retry.

## SSE, recovery and cancellation

The existing typed ApprovalRequested event is published only after the complete
pending presentation is registered. ApprovalGranted/ApprovalDenied and Tool events
continue through the existing EventSink. SSE carries no preview, target, approval
ID, arguments, contents or free-form error; no new event type or stream is added.
The UI refetches GET pending when the safe waiting snapshot changes, including
sequential calls in a batch. Session status is authoritative.

The URL hash stores only the selected Thread ID. On reload the UI reads its active
Session, reconnects SSE and fetches pending review. A late older response cannot
overwrite a newer local run. Closing the modal, navigating or disconnecting SSE
neither approves nor denies; the run remains governed by its original deadline.
Buttons are disabled while a decision is in flight. Deny resumes the existing
controlled denial path. Abort waiting Session cancels the runtime, not a permission.

Abort, external cancellation, MaxRunDuration, Manager.Close and server shutdown
invalidate pending approvals and unblock the worker. Late decisions cannot
restart the Session. Already committed writes retain existing commit semantics.

## Verification and limits

Offline Go tests cover allow/deny, exact Session binding, two simultaneous decisions,
batch ordering, cancellation/deadline/Close, complete presentations and strict HTTP
security/privacy. Linux tests exercise both existing write contracts with allow,
deny, abort, conflicts, replay and unavailable capability. UI tests cover pending
recovery, button disabling, escaped presentation and authoritative refetch.
`cd ui && npm run smoke:approval` uses a disposable Linux Docker fixture with the
real runtime/contracts/server and injected offline Model; Edge can be selected
with DAIMON_SMOKE_CHANNEL=msedge. It checks browser read/write allow/deny, reload,
two tabs, replay, Abort and shutdown. Only a host loopback port is published;
the fixture-only TCP bridge is not part of production server admission.

There is no persistent permission, always-allow, remote approval, policy editor,
authentication/public exposure, Memory, subagent or external agent integration.
Restart loses pending approvals and Sessions. No generic approval queue, Session
resume or multi-workspace coordination is introduced. MCP extends this approval boundary in Phase 10 below. Next: Phase 12 Intelligent Memory, deferred.

## Phase 10 MCP integration

See [MCP v1](MCP_V1.md). Application-owned stdio clients supply immutable namespaced tools to Session capability resolution. Explicit local read classification still requires the existing one-shot human approval; write/other/unclassified tools are denied. MCP approval shows server/tool/read and external-process warning without raw arguments. GET /api/v1/mcp/servers and /api/v1/mcp/tools expose readonly metadata; Settings / MCP and Bot checkboxes use these routes. They cannot configure processes or grant permissions. SSE, native contracts and private transcript persistence remain unchanged. Sessions close before MCP clients/processes. Manual Memory is implemented separately in [Memory v1](MEMORY_V1.md).


## Phase 12 Computer deliberate presenter

Computer uses the same one-shot pending decision lifecycle, whole-batch preapproval
and exact copied call identity. Kind computer presents Computer ID/backend, fixed
action class and complete sanitized target; type_text includes the exact full text
with reversible ASCII escapes. It never displays raw JSON, observations or images.
All operations, including observe, require approval. Deny/abort prevents execution;
an Allow cannot create profile/capability/lease or authorize another call. See
[Computer Use v1](COMPUTER_USE_V1.md). Native read/write contracts remain unchanged.
