# Web UI v1 — Phases 7–11

> Historical layout documentation. Phase 16.5 replaces the navigation, presentation
> and localization with [Product UI V2](PRODUCT_UI_V2.md). The API, approval,
> event observation and computer authority contracts described here remain in force.

DAIMON's local UI shows a persistent transcript plus separate runtime activity.
React + TypeScript + Vite produce static files. There is no JS production server,
SSR, client router, runtime in React, external CDN or browser provider SDK.

## Architecture and build

`ui/src/App.tsx` owns selection, resource lists, editors and per-Thread local turns.
`components/Editors.tsx` contains accessible native dialogs and forms.
`api/client.ts` centralizes same-origin `/api/v1` fetches, cancellation, DTO
projection, validation and allowlisted error messages. `api/types.ts` follows
actual HTTP projections. `hooks/useSessionEvents.ts` owns event observation;
`hooks/useConversation.ts` owns bounded paginated transcript loading/refetch.

```sh
cd ui
npm ci
npm run typecheck
npm test
npm run build
cd ..
go run ./cmd/daimon serve
```

Vite writes directly to the dedicated `internal/server/ui/` directory. Commit
the source, package-lock.json AND complete built index/hashed assets together.
The build clears only that directory. Go embeds these committed files, so fresh
clones and `go test ./...` need neither Node nor a previous npm build. No
node_modules, source maps or duplicate temporary dist are distributed.
Rebuild Go after rebuilding the UI; an already running binary retains its assets.
Complete the UI build before compiling/testing Go; do not replace embedded assets
concurrently with Go compilation.

## Development

Terminal 1: `go run ./cmd/daimon serve`. Terminal 2: `cd ui && npm run dev`.
Open the loopback Vite URL. Development configuration alone targets
`http://127.0.0.1:3000`; `/api` proxy preserves Host/Origin and streams SSE.
For another Go port, set `DAIMON_DEV_PORT` before starting Vite; only a numeric
loopback port is accepted. This setting is not part of the browser API client.
Production requests always use relative same-origin URLs, including dynamic ports.
Vite binds loopback. Production requires only Go and serves `/` and `/assets/*`.
There is no broad SPA fallback: unknown routes and unknown APIs remain JSON 404.
Index uses no-cache; hashed assets use one-year immutable cache and explicit MIME.

## Panels and REST

Desktop has Bots, Threads and Runtime Console panels. Narrow screens place the
resource panels above the console. Restrained dark tokens and keyboard focus
indicators apply throughout. Dialogs use native focus trapping, Escape and focus
restoration; errors use alerts and status announcements are textual.

Bots support list/select/create/full PUT/delete. Providers show actual registered
IDs; Groq/OpenAI-compatible labels are local presentation labels, not API metadata
or certification of model availability. No model/tool discovery endpoint exists.
Tools are comma-separated manual declarations, never browser authorization.
The server remains authoritative for all validation and availability.

Bot responses omit instructions. Editing therefore requires deliberately entering
the complete instructions again; empty input cannot erase them accidentally. No
private instructions endpoint is added. Validation checks required fields,
UTF-8 byte limits, tool syntax/duplicates and explicit permission mode.
`ask` never auto-approves. `read-only` resolves only read capabilities, and
still requires approval. `serve` uses individual Web approvals. Writes require explicit server configuration and contract preview approval.

Threads returned by GET are filtered by the selected Bot ID. Creation supplies
a prefixed UUID, explicit workspace text and UTC timestamps. Editing changes
title/update time only, retaining exact immutable Bot/workspace/created time;
update time does not regress. Workspace references are deliberately returned by
the Thread API and shown as escaped text, never browsed/opened in the browser.
Removal requires confirmation and does not cascade, delete workspace files or
abort an admitted Session. Threads with persisted messages cannot be removed.

## Session controls and local data

Start sends `{id, thread_id, message, message_id}` with separate generated IDs. Prefixes make crypto.randomUUID
compatible with the backend's lowercase-initial ID syntax. Messages are checked
for nonblank content and the 32768-byte bound; injected runtime budgets can
reject additional limits. Created/running/waiting states disable new Start and
enable Abort. Abort is cooperative: its acknowledgment is not terminal status.
Snapshot GET is authoritative for all six lifecycle states.

If Start's response is lost after admission, the client looks up only the same
generated identity, without retrying POST or creating a duplicate execution.
Known asynchronous resolution categories produce controlled explanations.
Accepted user and successful assistant bubbles are fetched from Conversation Store.
The console shows safe event labels, step/tool indexes and counters in separate
Runtime activity, never fabricated assistant content or raw JSON. Start acceptance
and snapshot changes including terminal completion refetch the paginated transcript.

## SSE lifecycle

One hook observes the selected local Session through native EventSource at
`/api/v1/sessions/{id}/events/stream`. Browser reconnections retain Last-Event-ID.
The hook also remembers the processed ID, checks event kind/sequence agreement,
deduplicates replay and keeps at most 256 displayed events. Unsafely large JSON
sequence numbers are rejected rather than rounded; runtime limits keep ordinary
runs far below JavaScript's safe integer limit.

`replay_gap` marks skipped events, refreshes snapshot and continues. `stream_end`
closes EventSource and refreshes final snapshot, without deriving state solely
from the envelope. Relevant lifecycle events also request coalesced snapshot
refreshes. A snapshot is fetched at connection start and transport error. Only
while transport fails, a five-second fallback polls snapshot; a terminal snapshot
stops fallback/reconnection. Healthy streams have no recurring snapshot polling.
Changing Session or unmounting closes observation and cancels in-flight GET only;
it never calls Abort. Switching back can replay retained events for a locally
tracked Session. Server restart loses Session state and is reported safely.

## Privacy and browser boundary

React escapes all server/user text. No dangerouslySetInnerHTML, arbitrary provider
URL, credential input, API key storage, localStorage transcript, direct filesystem
access or frontend policy/runtime is present. Messages go to the existing runtime
and its configured provider when Start is explicitly requested. Neither approval
nor write exposure is added. Secret configuration remains in the Go composition root.

The actual HTTP listener remains loopback-only. Its network handler checks Host
is localhost or a literal loopback address to limit DNS rebinding, rejects
cross-site/same-site Fetch Metadata and requires any Origin to match HTTP scheme
and exact request authority. Non-browser local clients without Origin remain
supported. There are no CORS changes, cookies, authentication or auth tokens.
Static responses apply CSP allowing only local scripts/styles/connections, with
no framing, base injection or external sources. Development Vite has its own
development CSP behavior; production CSP belongs to Go.
These checks do not authenticate trusted local processes or provide a sandbox.
Do not expose the server publicly or use an untrusted reverse proxy/workspace.

## Limitations and troubleshooting

- No tool-result history or summarization; manual Memory is separate.
- Transcript persists; runtime activity tracks the latest local Session per Thread.
- Reload restores the selected Thread and its active Session, including pending review.
- No per-Thread client queue; another browser/client can cause thread_busy.
- Session capacity includes finished Sessions until server restart.
- Bots with missing providers show a warning. Models are manually specified.
- Read approvals require review; write tools cannot start without runtime
  opt-ins, which the HTTP schema does not expose.

If resources fail to load, check the server URL and use Refresh resources. If SSE
fails, snapshot fallback exposes current safe status. A missing Session after
restart cannot be restored. If old UI assets appear, rebuild UI and Go/restart.
Provider configuration errors must be fixed in the server environment, never in
browser secrets. Keep only one server/writer per private state directory.

Validation uses Vitest/Testing Library with an injected fake EventSource, Go
embedded-route/origin tests, existing offline runtime/SSE tests, and a local fake
compatible provider for browser smoke. No real credentials/provider are required.
Optional smoke: `cd ui`, install a Playwright Chromium browser with
`npx playwright install chromium`, then `npm run smoke`. On Windows with Edge
already installed, set `DAIMON_SMOKE_CHANNEL=msedge` instead. The script compiles
Go and uses disposable temporary state/workspace plus a fake loopback provider;
it exercises CRUD, two-turn model continuity, completion, abort, reload, server
restart, blocked history deletion and narrow layout. Windows process
termination is forced by Node; graceful CLI shutdown is separately tested in Go
using an injected canceled application context.
Set `DAIMON_SMOKE_DEV=1` to run the same browser smoke through the development
Vite proxy. Both smoke modes use ephemeral ports and leave existing servers alone.
See [Conversation History v1](CONVERSATION_HISTORY_V1.md) for privacy, persistence,
context selection and failure semantics. Intelligent Memory remains deferred; next recommended phase is Phase 14 Sandboxed Computers.
Deferred: Intelligent Memory, Subagents, External agents, Authentication/Public exposure. Approval UX and MCP are implemented in their separate sections.

## Persistent transcript

Thread selection loads `GET /api/v1/threads/{id}/messages?after=0&limit=40`, then
successive next_after pages while has_more. UI accepts at most 1024 messages /
16 MiB of content. Reads cancel on selection/unmount; stale results cannot replace
another Thread's transcript. Errors retain last accepted records and show a safe
classification. User/assistant roles render escaped text with exact whitespace,
without raw HTML, Markdown execution, optimistic assistant bubbles or localStorage.
Failed/aborted runs add only the accepted user. Session failure explanations remain
in ephemeral activity; restart does not reconstruct historical Session status.
The composer discloses that selected previous messages go to the chosen provider
on each new turn. Conversation files contain private plaintext content; only the
messages endpoint deliberately returns it. No telemetry/analytics/content logs.


## Phase 9 Web Approval

Waiting status opens a human review modal with the complete escaped target,
provider disclosure for reads, and exact contract preview for writes. Allow once
and Deny are disabled while POST is pending. Errors refetch pending state without
retrying POST. Closing the modal keeps the Session waiting; a review-again button
reopens it. Abort waiting Session cancels the run. The Thread ID in the URL hash
restores selection; GET active Session then SSE/GET pending recovers review after
reload, with no transcript or approval contents in browser storage. Server restart
loses ephemeral Sessions. Original MaxRunDuration continues during human review.

See [Web Approval v1](WEB_APPROVAL_V1.md) for runtime ownership, complete deliberate previews, bounded pending records, strict HTTP decisions, SSE ordering and cancellation. Public snapshots/events remain free of approval content; GET pending is a separate deliberate review surface. Terminal providers remain supported. Writes require explicit process capability and exact contract approval; HTTP cannot grant capabilities.

## Phase 10 MCP integration

See [MCP v1](MCP_V1.md). Application-owned stdio clients supply immutable namespaced tools to Session capability resolution. Explicit local read classification still requires the existing one-shot human approval; write/other/unclassified tools are denied. MCP approval shows server/tool/read and external-process warning without raw arguments. GET /api/v1/mcp/servers and /api/v1/mcp/tools expose readonly metadata; Settings / MCP and Bot checkboxes use these routes. They cannot configure processes or grant permissions. SSE, native contracts and private transcript persistence remain unchanged. Sessions close before MCP clients/processes. Phase 11 Memory is implemented separately below.

## Phase 11 Memory panel

[Memory v1](MEMORY_V1.md) adds a Memory button and deliberate full record list
with global/Bot/Thread filter, kind, tags, manual provenance and updated timestamp.
Scope/kind and target are explicit user choices, scope stays immutable on edit,
and deletion needs confirmation. Local plaintext/provider disclosure is visible.
CRUD never arises automatically from a message, tool result or model response.
Reload/restart fetch saved records through bounded paginated API calls; safe
errors retain accepted display data. Memory never grants permissions.
`npm run smoke:memory` tests the real UI/local runtime offline; Remember-message
UX and Phase 12 intelligent retrieval/extraction are deferred.


## Phase 12 Computer UI

See [Computer Use v1](COMPUTER_USE_V1.md). Settings / Computer reads status and
capabilities; setup is manual and external. Bot Editor has Enable Computer and a
configured CUA backend selector; eligible CUA tool checkboxes are gated by this
profile and read-only mode. Unknown/unsupported entries remain denied. Disabled
profiles do not authorize retained declarations; remove them for native-only runs.
The Thread badge shows frozen active binding In use, or current connected/missing/
unavailable metadata on selection/completion. Refresh status explicitly.

Approval reuses the existing panel with local-computer warning, action class,
complete target and full reversible ASCII typing preview. Runtime activity uses
existing typed events, never raw tool/result/image payload. The Phase 12 metadata UI itself has no canvas/video/live screenshot or Human
Takeover; Phase 13 adds a separate viewer below. No arbitrary desktop REST action
or installation button.
Offline smoke: npm run smoke:computer; missing variant node scripts/computer-smoke.mjs
--missing. Existing native/MCP/Memory UI and smokes remain regression checks.

## Phase 13 Computer viewer

Chat / Computer / Activity tabs expose a deliberate canvas viewer and ownership
status. Take Control and Give Back are explicit; focused canvas input is separate
from the composer. Escape releases. Multiple viewers observe; only one owns
human input. Approvals stay accessible in every tab. Media reconnect is bounded
and reload never restores authority. Missing media preserves Driver actions.
See [Computer View v1](COMPUTER_VIEW_V1.md); `npm run smoke:computer-view` uses
an offline protocol fixture. Presence/shared cursors are absent.

Phase 14 adds Settings / Sandboxes, explicit Bot Computer Mode and safe
browser/resource presets. A Session shows guest preparation and cleanup state.
The Computer tab reuses the Phase 13 viewer and attaches only to its exact guest.
Guest files are ephemeral; host workspace is not mounted. Runtime setup is manual.
