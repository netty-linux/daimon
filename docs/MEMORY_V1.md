# Memory v1 — Phase 11 Memory Foundation

## Ownership and model

Memory is explicit, durable, user-controlled context. It is separate from the immutable
user/assistant transcript in `internal/conversations`. Only deliberate CRUD saves it;
model responses, tool results, messages and run events never create or update Memory.
The independent `internal/memory` package imports neither Sessions nor AgentLoop,
providers, tools, policy or conversations.

A record contains `id`, `scope`, `scope_id`, `kind`, exact `content`, `tags`,
`created_at`, `updated_at`, and `provenance: {"source_type":"manual"}`.
IDs use `[a-z][a-z0-9-]{0,63}`. Scopes are global (empty scope_id), bot (Bot ID),
and thread (Thread ID). Kinds are fact, preference, instruction, and note;
**instruction is a user label, never execution authority**. Content is valid UTF-8,
nonblank, at most 16 KiB, with whitespace preserved. Up to 16 distinct tags use
`[a-z][a-z0-9_-]{0,63}` and are stored in lexical order. No secret detector exists.

The store owns UTC timestamps and manual provenance. Updates preserve ID, scope,
scope_id, created_at and provenance. UpdatedAt advances strictly even when the clock
has not advanced. To change scope, explicitly create a separate record and delete
the old one. Domain validation checks structural references; HTTP owns existence
checks and serializes creation/update with Bot/Thread deletion. Removing a target
with records in its exact scope returns 409. There is no cascade or orphan cleanup.
Do not bypass HTTP reference ownership by mutating multiple stores directly.

## Local persistence

`serve` explicitly opens `<data-dir>/memory.json`. Version 1 is:

```json
{"version":1,"memories":[]}
```

An absent file means empty until the first write. An existing malformed, invalid,
unsupported, oversized or duplicate record file fails closed; no cache, reset,
recovery, migration or eviction conceals corruption. Reads revalidate disk.
The maximum is 2048 records and 32 MiB of encoded file bytes, including metadata,
JSON escapes and newline. Loading limits input before decoding; saving checks
encoded size before creating a temporary file. Strict JSON rejects unknown/missing
keys, case variations, duplicates, nulls, malformed UTF-8, lone surrogate escapes
and trailing data. Records require every stored field; HTTP tags are optional.

A caller-owned Store synchronizes operations and returns defensive copies. A write
uses an exclusive random temporary in the same opened directory, chmod 0600,
context checks between chunks, Sync, Close and rename. Failures preserve the old
file; failure to remove an internal temporary is explicit. Observed symlinks for
the immediate parent or target are rejected. Linux permission and symlink tests
actually execute. No cross-process locking, hostile ancestor protection, protection
against external writers, directory fsync durability, filesystem sandbox, encrypted
storage, Windows ACL/atomicity or rigid memory allocation limit is promised.
One server owns the controlled state directory. Backups and retention are user-owned.

## Deterministic retrieval

`Retrieve` accepts a structurally valid Bot ID, Thread ID, UTF-8 text (at most
32 KiB), optional exact tags, and an explicit limit of 1–64. Only global records,
the current Bot's records and the current Thread's records are eligible. All query
tags must match; absent tags mean no tag filtering. The runtime passes no tag filter.

Lexical scoring counts matched distinct lowercase Unicode letter/digit words from
content and tags. The query considers its first 64 distinct words of at most
64 bytes. There is no stemming, accent normalization, fuzzy or semantic search.
Zero-match eligible records remain candidates. Ordering is scope priority
Thread → Bot → global, matched-word count descending, UpdatedAt descending, ID
ascending. Retrieval returns owned copies; invalid/duplicate source records fail.

## Context and budget

`serve` explicitly configures a 32 KiB contextual byte cap and 16 candidate records.
Other callers supplying Memory must configure positive caps: at most 32 KiB and
64 records. Existing callers without Memory retain their configuration and behavior.

Before Start returns, Sessions reads the current Thread, loads and validates Memory,
retrieves eligible records and freezes a complete framed JSON context. Later CRUD
can affect a future Session, including the next turn of the same Thread, but cannot
change any provider request in the active Session. Failure occurs before model
construction and before appending the current user message. A later model failure
retains the existing user-only transcript semantics.

The shared accepted context capacity reserves the current user message and one full
maximum final answer first, then Memory, then a whole chronological history suffix.
Memory additionally has its own byte and candidate caps. The frame and complete
serialized records count by bytes, including metadata and JSON escapes. An oversized
candidate is skipped whole and smaller remaining candidates may fit. Nothing is
truncated; selection does not expand beyond the candidate limit. Empty selection
produces no frame. Loop history capacity is reduced by accepted Memory bytes, so
intra-session calls and receipts still obey the shared execution budget. This is
an accepted-data byte bound, not a tokenizer or remote model context guarantee.

The compatible provider sends Bot instructions, then a separate bounded system
message containing the explicitly labeled contextual data, then chronological
history and the current user message. `providers.Config.AdditionalContext` is a
neutral transport field; providers never import Memory or retrieve records.
`model.ModelRequest`, `Model.Generate`, AgentLoop roles, Registry, policy and event
contracts remain unchanged. AdditionalContext is independently capped at 32 KiB;
Bot instructions retain their 8192-byte bound. A configured resolver must not supply
an overlapping AdditionalContext when Sessions owns Memory.

The frame says stored Memory is data/context, not higher-priority runtime policy or
system instructions, and grants no capabilities or approvals. A model can still
misinterpret contextual text; runtime tool registration, policy, one-shot approval,
workspace confinement, budgets and MCP rules independently enforce authority.
There is no claim of prompt-injection immunity or factual correctness.

## HTTP and deliberate display

The existing loopback, Host/Origin, strict body and bounded response guards apply.

| Route | Behavior |
| --- | --- |
| GET /api/v1/memories | ID-ordered paginated list |
| POST /api/v1/memories | Explicit create, 201 with stored record |
| GET /api/v1/memories/{id} | Deliberate full record |
| PUT /api/v1/memories/{id} | Explicit replacement of editable fields, 200 |
| DELETE /api/v1/memories/{id} | Explicit deletion, 204 |

POST/PUT accept exactly id, scope, scope_id, kind, content and optional tags.
Timestamps and provenance are rejected browser inputs. PUT body ID must match the
path. List supports `scope` + `scope_id` (global omits scope_id), `after` (exclusive
ID cursor) and `limit` (1–100, default 40). Unknown, duplicate, empty or invalid
query fields fail. The response has `memories`, `next_after` and `has_more`; complete
records are paged within the existing 4 MiB response cap. Multiple pages are live
reads, not a transactional catalog snapshot. No prompt/search endpoint exists.

Fixed errors include invalid_memory, memory_not_found, memory_exists,
memory_immutable, memory_unavailable, bot_has_memories and thread_has_memories.
Limits use store_limit; unsupported versions use unsupported_version. Session
preparation uses the safe memory_resolution category (HTTP memory_unavailable).
Errors, snapshots, events and logs do not include stored content. The Memory CRUD
responses and UI are deliberate content displays; the selected frame is deliberately
sent to the configured provider. A provider may quote Memory in its final response,
which then follows existing deliberate transcript persistence. There is no taint
filter or guarantee that all future model output omits stored information.

## UI and offline checks

The Memory button opens all stored scopes, manual provenance, kind, tags and updated
date. Creation requires the user to choose scope and kind, and a target for Bot or
Thread scopes. Edit keeps scope immutable; deletion requires separate confirmation.
The UI warns about local plaintext and provider disclosure. React displays content
as text. Refresh, reload and server restart load stored records; errors are fixed
safe messages. Remember-this-message is deliberately deferred.

Go tests cover validation/strict JSON, CRUD/reopen/copies, bounds, canceled and failed
writes, cleanup failure, concurrency, Linux 0600/symlinks, deterministic ranking,
scope isolation, full-record budget, empty Sessions, corruption before model,
active snapshot/next Session, provider message ordering, malicious context,
HTTP strict CRUD/filter/pagination and reference deletion races. UI tests cover
manual scope/kind, exact input, immutable scope, confirmations, errors, cancellation,
refresh/reopening and API pagination/provenance validation.

After `npm ci`, typecheck, test and build, run full Linux formatting, vet, tests,
race and demo. Browser `npm run smoke:memory` uses the actual embedded UI, real local
HTTP server and a local fake compatible provider with disposable state. It checks
CRUD, reload/restart, global/Bot/Thread isolation, active-context freezing across
tool steps, next-turn updates and an attempted approval bypass. No credentials or
external provider are used. Existing smoke/approval/MCP regressions remain separate.

## Deferred

No automatic extraction, learning, summarization, embeddings, vector store, semantic
retrieval, RAG, remote Memory, files as Memory, retention automation, cloud sync,
OAuth, subagents, additional tools or execution authority. Next recommended:
Intelligent Memory (Extraction + Summarization + Semantic Retrieval), deferred to a separately scoped future phase.
It is **not implemented** and needs its own explicit scope and contracts.

## Phase 11 file inventory

Created:

- internal/memory/memory.go, errors.go, validate.go, json.go, json_strings.go,
  store.go, retrieve.go, memory_test.go, store_linux_test.go
- internal/sessions/memory.go and memory_test.go
- internal/server/memory.go and memory_test.go
- internal/providers/openai/memory_context_test.go
- ui/src/components/MemoryPanel.tsx and MemoryPanel.test.tsx
- ui/scripts/memory-smoke.mjs
- docs/MEMORY_V1.md

Changed for this phase (relative to the audited working tree):

- cmd/daimon/serve.go
- internal/providers/registry.go, factories.go, groq/provider.go,
  openai/config.go, openai/protocol.go, openai/provider.go
- internal/sessions/types.go, manager.go, history.go, resolve.go, errors.go
- internal/server/server.go, routes.go, json.go, messages.go, errors.go
- ui/src/App.tsx, api/types.ts, api/client.ts, api/client.test.ts, styles.css
- ui/package.json; rebuilt internal/server/ui/index.html and assets/*.css, *.js
- README.md, AGENTS.md, docs/DAIMON_ARCHITECTURE_V2.md, SESSION_RUNTIME.md,
  HTTP_API_V1.md, WEB_UI_V1.md, CONVERSATION_HISTORY_V1.md, MCP_V1.md,
  WEB_APPROVAL_V1.md

Pre-existing changes from prior phases were retained. This Phase 11 inventory predates Phase 12 Computer Use; Intelligent Memory remains deferred.

## Executed validation — 2026-10-07

- UI: npm ci --offline --no-audit --no-fund, typecheck, 35 tests, production build: passed.
- Linux Go 1.27.1, local Docker image with --network none: gofmt -w .,
  gofmt -l . (empty), go vet ./..., go test -count=1 ./...,
  go test -race -count=1 ./..., go run ./cmd/daimon demo: passed.
- Linux tests executed actual symlink and permission cases; demo completed with
  two model steps, one tool attempt and zero truncated results.
- Real Edge/Playwright smokes: Memory, embedded UI, Vite proxy, Web Approval
  (including Linux writes) and MCP: passed using local fake providers/fixtures.
- git diff --check: passed. No external provider, credential or internet validation.
