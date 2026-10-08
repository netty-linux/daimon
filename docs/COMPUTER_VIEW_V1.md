# Computer View v1 — Phase 13

## Audit and implementation plan

The Phase 12 action plane uses the application-owned CUA Driver MCP stdio
client, exact capabilities, frozen Session binding, individual approval and one
physical-computer lease. AgentLoop, providers and Session SSE remain unchanged.
The working tree includes earlier uncommitted phases; preserve them.

Public CUA audit (2026-10-08):

- [MEDIA.md](https://github.com/trycua/cua/blob/main/libs/cua/proto/MEDIA.md):
  StreamService opens media, RCDP v2 WebSocket /media transports binary video,
  server-first hello/session_opened, scoped expiring tickets, keyframe on attach,
  geometry/codec epochs and interactive input acknowledgements.
- [StreamService proto](https://github.com/trycua/cua/blob/main/libs/cua/proto/cua/env/v1/stream.proto)
  and [stream reference](https://cua.ai/docs/cua-sdk/reference/protocol/env-stream):
  protobuf gRPC-Web supports browser-compatible control RPCs.
- [Desktop guide](https://cua.ai/docs/spaces/guides/stream-a-desktop): the
  standalone Driver MCP is not the media plane. Existing cua-spacesd can stream
  a primary display; no Space creation is needed for this integration.
- [PRESENCE.md](https://github.com/trycua/cua/blob/main/libs/cua/proto/PRESENCE.md):
  PresenceService is separate, ephemeral participant identity; human media input
  is not an agent cursor. Presence never grants control. Cursor probing can move
  the physical pointer, so this cut will not enable it or simulate shared cursors.
- [HTML5 viewer](https://github.com/trycua/cua/tree/main/libs/cua/crates/cua-spacesd-html5):
  WebCodecs H.264, BGRA/PNG alternatives, input over RCDP. Its source is not copied.
- [Root license](https://github.com/trycua/cua/blob/main/LICENSE.md) is MIT;
  [spacesd license](https://github.com/trycua/cua/blob/main/libs/cua-spacesd/LICENSE)
  and [HTML5 license](https://github.com/trycua/cua/blob/main/libs/cua/crates/cua-spacesd-html5/LICENSE)
  are FSL-1.1-MIT. External use remains subject to those exact terms. DAIMON
  consumes wire interfaces, not their implementation or generated viewer code.

Plan before source changes:

1. Add explicit optional loopback media configuration at the composition root;
   one administrator-designated primary desktop must be the same physical desktop
   as the action backend. No provisioning, daemon launch, hidden environment or
   credentials in HTTP. Keep Driver MCP for agent actions.
2. Implement bounded protobuf gRPC-Web StreamService OpenMedia/CloseMedia and
   RequestKeyframe; authenticate only server-to-daemon. Attach RCDP v2 via
   WebSocket. Never implement desktop capture, encoder or transcoder in Go.
3. Own ViewSessions independently from Agent Sessions. Browser receives only a
   short-lived random DAIMON capability, and connects to a same-origin proxy.
   Daemon root credentials and media tickets stay server-side.
4. Desktop only, video only: passthrough encoded H.264/PNG/BGRA after packet,
   geometry and epoch validation. Browser decoder renders ephemeral canvas.
   Session SSE, logs, errors, transcript and Memory never contain media.
5. Exactly one input controller. Manager serializes agent dispatch, takeover,
   human dispatch and release. Takeover waits bounded for an in-flight operation;
   blocks new agent input while transitioning. Observation/native tools remain
   available. Agent input under human ownership returns a controlled failure.
6. All viewers are view-only upstream. Takeover opens a separate input-capable
   CUA media session owned by the Manager; browser input uses a distinct scoped
   control WebSocket. Allow only the validated RCDP interactive_input subset,
   contiguous sequences, explicit acknowledgements, bounded rate and text.
   Never retry or silently duplicate clicks/keys.
7. Multiple bounded viewers, one human controller. Separate random view and
   control tokens, same-origin/Host/Fetch-Metadata checks before effects. Session
   ID is metadata, never a secret. Release/Session end/shutdown revoke control.
8. Control disconnect expires after a short backend grace; return to the same
   still-active original Agent binding, otherwise neutral. Reload creates a new
   view-only viewer, never silently restores human input. Reconnect media with
   bounded backoff and fresh tickets/keyframe; Computer and Session persist.
9. Limits: four viewers, 1920px long edge, 30fps, bounded packet/control lengths,
   connect and write deadlines, no media queue beyond one in-flight packet.
   Slow H.264 consumers recover from a keyframe instead of decoding gaps.
   Target low latency without promising a measured real-desktop latency.
10. UI Chat / Computer / Activity tabs, explicit live status and ownership,
    visible Take Control / Give Back Control, input only in the focused viewer.
    No global keyboard capture, clipboard, audio, recording or file transfer.
11. Offline fake protocol/media backend, ownership/concurrency/security tests,
    UI tests and browser smoke; preserve action-only behavior without media.
    Final UI build precedes Linux format/vet/test/race/demo.

Absent/failed media shows Live view unavailable and does not remove agent action
capabilities. Real CUA media validation is optional and must not be claimed unless
actually run. Phase 14 Sandboxed Computers is outside this request.

## Implemented contract

`serve --computer-media-url http://127.0.0.1:3211` explicitly enables an
operator-provided, already running CUA media daemon for the configured CUA local
Computer. `DAIMON_CUA_MEDIA_TOKEN` is read only by cmd, injected server-side and
never inherited by MCP processes or returned to the browser. The media endpoint
accepts only literal loopback HTTP with an explicit port. The operator must bind
media and Driver to the same desktop; DAIMON cannot prove their physical identity.
Driver-only configuration remains valid and keeps its existing approved actions.
No daemon is installed, started or provisioned by DAIMON.

The original standard-library client implements only the required protobuf
fields and bounded binary gRPC-Web envelopes for StreamService OpenMedia,
CloseMedia and RequestKeyframe. It requests primary display, no audio, 30fps,
1920px, observe-only geometry, RCDP v2 and H.264/PNG/BGRA. It does not consume
CUA SDK/generated code or viewer source. The authenticated CUA ticket is used
only on the server's upstream WebSocket. Browser capabilities are independent.

### HTTP and sockets

| Method | Route under `/api/v1/computers/{id}` | Contract |
| --- | --- | --- |
| GET | `/view` | Safe availability, ownership, viewer count and optional controller/session IDs |
| POST | `/views` | Exact `{"target":"desktop"}`; new view ticket |
| POST | `/control/take` | Exact view_id + view token; new distinct input token |
| POST | `/control/release` | Exact view_id + control token; confirmed revocation |
| GET upgrade | `/views/{view_id}/media` | `rcdp.v2`, `daimon.media.<token>` subprotocols |
| GET upgrade | `/views/{view_id}/input` | `rcdp.v2`, `daimon.input.<token>` subprotocols |

Queries, arbitrary targets and extra JSON fields are rejected. Browser mutations
and both WebSockets require matching Origin; read-only state fetch admits
same-origin Fetch Metadata without Origin, as emitted by browsers. Host must be
loopback and cross-site Fetch Metadata is rejected. Socket attachments are single
use, scoped to their Computer/view and token kind. Tokens are random 192-bit
capabilities, compared in constant time, held only in component memory, never
URLs, browser storage, SSE, transcript, logs or Memory. IDs are metadata.

### Ownership and lifetime

ComputerManager owns one controller and a cooperative input dispatch gate.
Take installs transitioning before waiting for an existing agent operation,
then opens a separate input-capable media session. Only confirmed setup grants
human_control. Every viewer's own upstream session remains view-only. Human
input bypasses Tool/MCP deliberately through this narrow controller boundary;
it cannot invoke arbitrary actions. Agent input during transition or human
ownership returns `computer_controlled_by_human`; observations and noncomputer
tools continue. The AgentSession and its existing approval/budget remain alive.

Give Back cancels human input and closes the CUA media session before restoring
the same still-active original binding. Completion/abort invalidates that binding;
viewers may stay open in view-only mode. A failed remote close retains a blocked
transitioning controller instead of claiming authority has returned. Failed startup/handshake also retains the handle when remote close fails;
closed viewers with unconfirmed cleanup retain capacity and shutdown reports
the failure. There is no automatic retry of input or failed revocation. Restart/operator investigation
is required after an unconfirmed revocation.

A view must attach within 10 seconds. Initial input attach and disconnected input
have 3-second grace; an attached human lease expires after 15 seconds without
input/pong renewal. Server pings every 5 seconds and enforces a 15-second socket
read deadline. Backend timers enforce cleanup independently of browser unload.
Shutdown stops admission, closes streams and media authority, then joins Sessions
and MCP processes. All session handles are ephemeral.

### Frames, backpressure and input

Binary RCDP video is validated and forwarded on a separate same-origin socket;
there is no transcoder. The browser renders BGRA/PNG on canvas or H.264 through
WebCodecs. Epoch changes and sequence gaps require a keyframe. Sequence need not
start at zero. Browser H.264 decode queue is bounded at three frames; PNG has one
pending decode; the server has one packet in flight and a five-second write
deadline. Slow clients disconnect/recover rather than accumulating a frame queue.
Four viewers, 16 MiB packets, 16 KiB controls and 1920px geometry are the local
limits. Low latency is a design objective, not a measured desktop guarantee.

Input is a strict RCDP interactive_input subset: normalized pointer move/click,
text commit, key down/up with bounded modifiers, and bounded scroll. Batches have
1–32 events, at most 4096 UTF-8 text bytes, contiguous sequences and a 120-events
per-second server limit. Acknowledgement must match session and through_sequence,
with delivered=true. Duplicate keys, malformed Unicode, unsupported event kinds,
unknown fields, stale tokens and replay are rejected before dispatch.

The UI sends one acknowledged batch at a time, with a 32-event queue and 16 KiB
socket buffered limit. Pointer movement is coalesced; clicks/keys are never
retried. Input requires explicit Take and the focused canvas. This initial UI
supports left clicks, text, atomic key pairs and scrolling, not drag gestures or
held-key sequences. Escape releases control. Chat keyboard input stays separate.
Clipboard/paste, file drop, audio, recording and file transfer are blocked/absent.

### UI, reconnect and presence

Chat, Computer and Activity tabs keep human approvals accessible in every tab.
The Computer viewer displays offline/connecting/live/reconnecting/error and
agent/human/transitioning/view-only ownership, with explicit Take/Give Back.
Media reconnect uses fresh tickets and at most four backoff attempts per failure
cycle (one to five seconds); successful live video resets the failure counter.
Reload creates a view-only viewer and never restores an input token. A second
viewer observes while the first owns control. Media failure does not disable
Driver actions.

PresenceService and shared participant cursors are not implemented in this cut.
The actual remote pointer can appear in daemon video; DAIMON adds no synthetic
agent/human cursor and does not move the physical pointer to probe presence.
Controller ownership shown by the UI is Manager state, not a Presence identity.

### Validation and limitations

Offline tests cover real gRPC-Web/WebSocket framing through a fake daemon,
strict input, malformed frames, redirect refusal, failed-handshake cleanup,
capability scopes, multiple viewers, controller races, in-flight
agent drain, session completion, timeout/disconnect, failed close and shutdown.
Browser smoke uses simulated BGRA frames and exact synthetic input; it does not
capture a real desktop. Real CUA media/H.264 compatibility and OS permissions
still need an optional operator-run validation. The fake probe is accessible only
inside container loopback; it is not a production endpoint.

Local only; no cloud, audio, clipboard, recording, file transfer, Spaces creation,
sandbox provisioning, Intelligent Memory, subagents or external agents. FSL
components are optional external dependencies under their own licenses; no FSL
source is bundled. Next recommended: Phase 14 — Sandboxed Computers, not implemented.

## Phase 13 file inventory

Created:
- internal/computer/media.go, cua_media.go, view.go, cua_media_test.go, view_test.go
- internal/computer/socket/socket.go and socket_test.go
- internal/computer/mediatest/fixture.go
- internal/server/computer_view.go and computer_view_test.go
- ui/src/api/computer-view.ts
- ui/src/components/ComputerViewer.tsx and ComputerViewer.test.tsx
- ui/src/components/computer-video.ts and computer-video.test.ts
- ui/scripts/computer-view-smoke.mjs
- docs/COMPUTER_VIEW_V1.md

Changed relative to Phase 12 working tree:
- internal/computer/manager.go
- cmd/daimon/serve.go
- internal/server/routes.go and server.go
- ui/src/api/client.ts, App.tsx, App.test.tsx and styles.css
- ui/package.json and ui/scripts/approval-fixture/main.go
- internal/server/ui/index.html and generated assets
- AGENTS.md, README.md, docs/DAIMON_ARCHITECTURE_V2.md, COMPUTER_USE_V1.md,
  HTTP_API_V1.md, SSE_V1.md, WEB_UI_V1.md and SESSION_RUNTIME.md

Earlier uncommitted phases remain in the working tree. This inventory isolates
Phase 13; it does not classify all Git-untracked foundation files as new work.

## Validation executed (2026-10-08)

- Linux Docker golang:1.27.1, network disabled: gofmt -w ., empty gofmt -l .,
  go vet ./..., go test -count=1 ./..., go test -race -count=1 ./...,
  go run ./cmd/daimon demo: passed, including symlink tests.
- UI: npm ci --offline --ignore-scripts, npm run typecheck, npm test,
  npm run build: passed. Final UI test suite has 49 tests in 11 files.
- Playwright Edge offline browser fixtures: smoke:computer-view,
  smoke:computer, computer-smoke.mjs --missing and smoke:approval: passed.
- Windows targeted Computer/server tests and race checks: passed; these do not
  replace Linux write-contract or symlink validation.
- git diff --check: passed (existing line-ending warnings only).

No real CUA desktop/media service or credential was used. No commit, push,
provisioning, deployment or Phase 14 implementation was performed.

## Phase 14 guest media

A sandbox Computer reuses this viewer/control protocol. Media configuration
remains server-only and is scoped to the exact guest. Human takeover controls
that guest; it cannot choose the host. Cleanup revokes viewers/input before
deleting the guest. Missing guest media is unavailable, with no host fallback.
See [Sandbox Computers](SANDBOX_COMPUTERS_V1.md).

## Phase 15 Fleet gateway transport

Cloud Sandbox Computers reuse these viewers, scoped tickets, media framing and
exclusive takeover gates. Private StreamService/RCDP connections may use only
validated official Fleet HTTPS/WSS gateway routes. Fleet bearer, claim and spacesd
headers never reach browser tickets or persisted/public metadata. Private DNS
answers, alternate hosts, redirects and signed query URLs are refused. An
unsupported/expired gateway config leaves that exact guest's media unavailable;
there is no host/local media fallback. The existing local loopback constructor is
unchanged. See [Cloud Computers](CLOUD_COMPUTERS_V1.md) for limitations.
