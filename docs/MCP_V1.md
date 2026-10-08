# MCP Tool Integration v1 — Phase 10

Implemented as one additional source of tools. MCP supplies tools; DAIMON resolves capabilities; policy authorizes; the existing ApprovalProvider captures the human decision; AgentLoop executes sequentially. Conversation History remains the separate persistence boundary. Manual Memory is implemented separately in Phase 11.

## Ownership and flow

```text
local mcp.json → cmd/daimon serve → application-owned MCP Manager
                                   │ stdio Client → external MCP process
Bot.Tools (names only)              │ initialize → initialized → tools/list
       ↓                           ↓ immutable discovered catalog
Session capability resolution ← exact allowed intersection
       ↓
per-Session Tool Registry: native Tool + MCP Tool adapter
       ↓
Policy → one-shot Web Approval → AgentLoop → tools/call
                                               ↓
                              bounded text ToolResult
                                               ↓
                                  provider → final response
                                               ↓
                                    Conversation Store → UI
```

The loop, model.Model, tools.Tool, tools.Registry and provider contracts do not import MCP. Sessions assembly receives a narrow MCP lookup interface; private adapter/client pointers never enter Bot, Thread, Binding, DTOs or model requests. Each run freezes tool names and schemas; lookup does not spawn or reconnect. Native tool resolution retains its existing policy and contracts.

HTTP shutdown drains first. SessionManager.Close cancels and joins workers, approvals and persistence, then MCP Manager.Close closes and joins child clients. Partial startup failure also releases owned children. One broken server is marked unavailable while others remain usable. No retry, automatic restart, dynamic refresh or hidden fallback.

## Local configuration

`go run ./cmd/daimon serve --data-dir /private/daimon` reads optional `/private/daimon/mcp.json`. An absent default file means no MCP servers. `--mcp-config /private/config/mcp.json` makes that file mandatory. The private data directory is validated before MCP process startup. Config is versioned and strict: unknown/missing fields, duplicate JSON keys/server IDs, invalid classifications and unsupported version fail startup with a fixed public error. Observed config symlinks/nonregular files are refused. There is no browser configuration endpoint, file watcher or persistent environment field.

```json
{
  "version": 1,
  "servers": [
    {
      "id": "local",
      "command": "/absolute/path/to/trusted-mcp-server",
      "args": ["--stdio"],
      "enabled": true,
      "tools": {
        "lookup": {"classification": "read"},
        "mutate": {"classification": "write"}
      }
    }
  ]
}
```

All fields shown are mandatory; empty arrays/maps are permitted. Disabled entries never spawn. Commands must be absolute; args are separate argv values, never shell interpolation. Known shell executables are refused. The administrator still must trust the executable and arguments: invoking an interpreter or malicious program is not sandboxing.

Server IDs match `[a-z][a-z0-9-]{0,31}`. Remote names contain only lowercase letters, digits, `_` and `-`, start with a letter, and produce a complete name of at most 64 bytes. The deterministic exact name is `mcp__<server-id>__<remote-name>`. Unsupported names are rejected, never normalized or silently truncated. Duplicate discovered names fail that server. Server IDs cannot contain underscores, so the namespace boundary is unambiguous.

Bot.Tools can declare only these names. An unavailable or denied declaration is retained by the editor, but Session startup rejects it before calling the model. The browser cannot grant authority through a checkbox or message.

## Protocol and transport

This client deliberately implements the stateful **2025-11-25** MCP profile: [lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle), [stdio transport](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports), and [tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools). The current [latest specification](https://modelcontextprotocol.io/specification/latest) uses a different negotiation profile. Only the configured 2025-11-25 version is accepted here; no version fallback is implied.

The client sends initialize with empty client capabilities, validates server tools capability/version/serverInfo, sends notifications/initialized, then collects paginated tools/list. Discovery never invokes tools/call. Cursor loops, duplicate names, unsupported required task support and invalid schema envelopes fail discovery. Input schema must be a bounded JSON object; a supplied root type must be object. Bytes are privately owned and copied into Tool descriptions. DAIMON does not implement full JSON Schema semantic validation, dereference remote references, or expose schemas in public metadata. Server annotations, instructions and self-reported read hints never grant permission or augment system instructions.

UTF-8 newline-delimited JSON-RPC 2.0 uses monotonic numeric request IDs, one serialized writer, one reader and a bounded correlated pending map. Duplicate JSON keys, malformed envelopes, wrong/late/duplicate IDs, stdout nonprotocol text, oversized frames and queue overflow retire the client. Protocol ping requests are answered; server requests/notifications outside this profile retire it, including catalog-change notifications. There are no roots, sampling, elicitation, tasks, resources, prompts or protocol batch support.

Tools call the exact discovered remote name with copied JSON object arguments. Cancellation/deadline preserves context identity, sends a best-effort cancellation notification, then retires the connection and kills the process: an uncooperative late response cannot be reused. Other calls sharing that server can fail; other server connections remain independent. There is no rollback guarantee for effects already performed by the external program.

Text content blocks are joined with newline within 64 KiB. Image/audio/resource blocks are unsupported; results do not become executable instructions. Tool `isError` and RPC errors become fixed controlled failures; remote free-form error text/data never enter public errors. Private successful result text follows the ordinary tool receipt into the provider, whose successful final assistant response is persisted by the existing conversation pipeline. SSE and snapshots expose only the existing typed lifecycle metadata.

## Authorization and deliberate displays

Explicit local classes are read, write and other. Unmapped tools resolve to other and fail closed. This v1 admits **read only**, always requiring the existing per-call human approval. The local classification is an administrator assertion, not proof that an external implementation is harmless. Discovery, Bot selection and server-provided annotations never authorize execution.

All MCP write/other tools are denied at Session capability resolution and independently in the adapter. No safe MCP write presenter exists in this cut. Native create/replace process flags do not enable MCP writes. A generic JSON preview is not accepted as an edit/create contract.

The deliberate read approval presents server ID, exact namespaced tool, read classification and an external-process/provider warning. It contains no raw arguments, tool results, schema, command, environment or stderr. The pending decision remains bound privately to the original copied call. Existing one-shot Allow/Deny, whole-batch preauthorization, abort/deadline invalidation, reload recovery and duplicate-decision rejection apply unchanged. No always-allow, persistent trust or browser permission toggle exists.

## HTTP and UI

Both routes are readonly, subject to the same loopback, Host/Origin/Fetch-Metadata checks as the existing local API. Query strings and bodies are rejected. POST/PUT/DELETE cannot add servers or invoke tools.

- `GET /api/v1/mcp/servers` → `{ "servers": [{ "id": "local", "status": "connected" }] }`. Status is connected, disabled or unavailable.
- `GET /api/v1/mcp/tools` → `{ "tools": [{ "name": "mcp__local__lookup", "server_id": "local", "description": "...", "classification": "read", "available": true, "permitted": true }] }`.

Description is bounded and escaped to ASCII for the public catalog. Permitted means eligible to request approval, never preapproved. Metadata omits commands, argv, env, credentials, stderr, schema, protocol bodies, call arguments and results. Remote descriptions are deliberate untrusted catalog text, not an authority.

Settings / MCP displays catalog/status with a manual refresh. Bot Editor provides native/MCP checkboxes and retains unavailable selections. Approval uses the existing panel and SSE lifecycle rather than another MCP event channel. React escapes text; no innerHTML rendering or browser credential/server installation form is added.

## Process security and limits

The application uses direct exec and explicit operational environment only: PATH, SystemRoot, WINDIR, TEMP, TMP, TMPDIR, LANG and LC_ALL. It never copies os.Environ; DAIMON provider secrets are not inherited. The environment resolver is injected and forbids DAIMON_* names. Persistent JSON contains no env field. No stdout/stderr/raw protocol logging: stderr is drained to io.Discard with zero retained bytes.

Defaults: initialization plus discovery 8 seconds, tool call 30 seconds, stdin-close grace 2 seconds. Parent Session budget/deadlines remain authoritative. Shutdown closes stdin, then terminates/kills if needed, closes pipes and waits for reader/writer/process. Linux uses an owned process group; Windows uses a kill-on-close Job Object and hides child windows. Other OS platforms fail startup closed. External MCP tools do not inherit the native os.Root workspace confinement; they run with the application user permissions and inherited process working directory. These mechanisms clean ordinary descendants; they do not establish a security sandbox or prove containment against a hostile executable.

Fixed admission limits: 256 KiB config, 16 servers, 64 discovered tools/server, 256 total tools, 32 KiB/schema, 64 KiB/arguments, 64 KiB/result, 1 MiB/JSON-RPC message, 32 outstanding requests and 32 queued writes. Descriptions are at most 1024 bytes, cursors 1024 bytes and discovery at most 64 pages. Config also bounds argv/environment count and total bytes. These bound accepted data and queues, not external process CPU/memory or the overall Go process heap.

## Offline verification

Real subprocess fixtures implement initialization, initialized notification, paginated discovery and call responses. Tests cover correlation, malformed/oversized output, wrong IDs, unsupported version/schema/names, crash, timeout, cancellation, bounded results, stderr flooding, literal argv, provider-secret isolation, denied classes, isolated servers and process cleanup. Session integration checks pending approval before invocation, deny without call, controlled failures and exact assistant persistence. HTTP tests ensure readonly metadata and no configuration/execution endpoint; UI tests exercise catalogs, selection and approval projection.

Run the standard Linux formatting/vet/test/race/demo commands in AGENTS.md. Frontend: npm ci, npm run typecheck, npm test, npm run build. `DAIMON_SMOKE_CHANNEL=msedge npm run smoke:mcp` in ui is opt-in, offline and uses Docker golang:1.27.1 with deterministic MCP/model processes and disposable tmpfs state. It validates discovery, Bot selection, individual approval/denial, response persistence after reload and shutdown with a pending approval. `npm run smoke:approval` preserves the native contract regression.

## Deferred

Only stdio/tools are supported. No remote MCP transport, resources, prompts, OAuth, MCP server hosting, public authentication, sandbox, write presenter, auto-restart, dynamic discovery, permanent approvals, automatic Memory or subagents. Next recommended phase: **Phase 13 — Live Computer View + Human Takeover**, not implemented.

## Change inventory for Phase 10

Created:
- internal/mcp/config.go, client.go, tools.go, manager.go
- internal/mcp/process_linux.go, process_windows.go, process_other.go
- internal/mcp/mcp_test.go, process_cleanup_linux_test.go, mcptest/fixture.go
- internal/sessions/mcp_test.go
- internal/server/mcp.go, mcp_test.go
- ui/src/components/MCPPanel.tsx, MCPPanel.test.tsx
- docs/MCP_V1.md

Changed:
- cmd/daimon/serve.go, serve_test.go
- internal/sessions/types.go, resolve.go, approval.go, approval_adapter.go
- internal/server/server.go, routes.go
- ui/src/App.tsx, api/types.ts, api/client.ts, api/client.test.ts
- ui/src/components/Editors.tsx, ApprovalPanel.tsx; ui/src/styles.css
- ui/package.json, scripts/approval-fixture/main.go, scripts/approval-smoke.mjs
- Generated internal/server/ui/index.html and hashed assets
- README.md, AGENTS.md, docs/DAIMON_ARCHITECTURE_V2.md, HTTP_API_V1.md,
  SESSION_RUNTIME.md, WEB_UI_V1.md, WEB_APPROVAL_V1.md, CONVERSATION_HISTORY_V1.md

The working tree already contained earlier phases. This inventory describes only the MCP addition; earlier loop/contracts/provider changes were preserved.


## Phase 12 Computer backend exception

See [Computer Use v1](COMPUTER_USE_V1.md). Optional local computer_backend="cua-local"
designates exactly one cua-driver mcp entry and selects legacy 2025-06-18 explicitly.
Generic MCP remains 2025-11-25/read-only. Every designated CUA tool is denied by
Generic Lookup, including reads; the Computer domain independently gates exact
names, strict local arguments, profile, lease and per-call human approval. Generic
write/other denial and native flags remain unchanged. The same Client handles
stdio/cancellation/limits/process cleanup; per-block text filtering additionally
rejects image data before CUA receipts. No raw config/action HTTP route exists.

CUA-only composition includes explicitly selected display/authentication and user
OS-directory variables (see Computer Use v1); generic MCP retains its original
operational environment. Neither path inherits provider credentials or CUA bypass
mode variables.
