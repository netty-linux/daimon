# Cloud Computers V1 — Phase 15

## Audit and implementation plan

Phase 14 owns disposable guests in `internal/sandbox`, journals intent before
provisioning, reserves capacity under a mutex and closes Computer bindings/media
before deleting the exact owned reference. Sessions resolve guests before invoking
the model. Guest IDs never fall through to the host. These boundaries remain.

Cloud is an explicit placement of this domain. Missing placement on legacy local
profiles means local. Cloud requires `cua-cloud` and `placement: cloud`; failed
cloud provisioning never selects local or host. Changes affect sandbox profiles,
journal validation, backend selection, quotas, CUA CLI lifecycle, Computer backend
identity, session metadata, safe HTTP projections and the existing UI.

Implementation order: domain/backward compatibility; official Fleet CLI adapter;
owned lifecycle/quota/TTL; Computer and Session binding; read-only status API and
UI; offline failure/concurrency tests; Linux and UI validation.

## Public CUA contracts reviewed

- [Cloud guide](https://github.com/trycua/cua/blob/main/docs/content/docs/cua-sdk/guides/cloud.mdx)
- [Sandbox skill](https://github.com/trycua/cua/blob/main/libs/cua/skills/cua-sandboxes/SKILL.md)
- [CLI sandbox implementation](https://github.com/trycua/cua/blob/main/libs/cua/crates/cua-cli/src/sandbox.rs)
- [CLI authentication](https://github.com/trycua/cua/blob/main/libs/cua/crates/cua-cli/src/auth.rs)

Control uses explicit embedded CLI mode, `--on cloud`, Linux containers and
gVisor. Claims have an explicit bounded TTL; `--no-warm` disables implicit warm
creation and `--max-pool-size 2` bounds each managed pool. Claim release can leave
managed pool capacity until Fleet garbage collection; it does not guarantee an
immediate end to billing. No custom pool administration is introduced.

Authentication is administered externally. No login/setup/install or paid cloud
operation is part of automatic validation. Secrets, cloud service URLs and headers
stay private and never enter Bot stores, guest configuration or public metadata.

## Architecture and configuration

`SandboxManager` selects an application-injected backend from the frozen profile.
Legacy profiles without placement stay local. Cloud requires:

```json
{"backend":"cua-cloud","placement":"cloud","image":"linux","runtime":"gvisor","browser":false,"resources":"small","network":"outbound"}
```

Use an already installed official CUA CLI on a Linux control host:

```sh
go run ./cmd/daimon serve --data-dir /controlled/daimon --cloud-cua /absolute/cua
```

`--sandbox-cua` independently configures local guests. Cloud availability does
not block serving local resources. No login, installation or automatic smoke is
performed. No endpoint/image/pool/region override is accepted from the browser.

## Authentication

The administrator supplies `FLEETS_TOKEN` or `CUA_CLIENT_ID` and
`CUA_CLIENT_SECRET` to the DAIMON process. Only cmd reads these variables and
explicitly supplies them to cloud control subprocesses. They are absent from
local CUA, model configuration, guests, journals and browser responses.
CUA uses a private application home/state and explicit embedded mode; the
ordinary user's external CUA login store is not discovered in this version.
Read-only `auth whoami` verifies authentication against the fixed official
`https://run.cua.ai` origin. Safe status categories distinguish not configured,
missing executable, signed out, cloud unavailable and ready; no identity or
server error text is exposed. Unsupported control hosts remain explicit.

## Resources, TTL and cost

Cloud small: 2 CPU / 4096 MiB. Cloud medium: 4 CPU / 8192 MiB. Local presets
remain unchanged. Default Session lifetime and explicit claim TTL are 900 seconds;
provisioning is independently bounded to 120 seconds. Internal caller options
remain bounded to one hour. The lifetime starts before provisioning, so DAIMON
does not rely on a keep-alive daemon or renew claims in the background.
Authoritative expiry, when reported, is normalized to UTC and can shorten local
cleanup timing. Missing expiry is not fabricated.

Two cloud reservations are admitted under the same mutex before any creation.
This is independent of the four-guest total quota. Unresolved cleanup retains its
reservation. No queue or implicit capacity expansion exists. Each managed pool
has CLI `--max-pool-size 2`; this is a per-pool bound, not an account-wide billable
capacity quota. Different resource shapes can create different managed pools.
`--no-warm` prevents implicit warm requests; release does not immediately destroy
managed pools. Fleet manages idle capacity/GC. Users must review account billing
externally. The editor and pre-send warning explain paid remote provisioning.

## Lifecycle, ownership and recovery

Creation journals an exact random `cloud:daimon-<24 hex>` owned reference before
calling CUA. Journal version 1 accepts the new optional profile placement and
UTC expiry while retaining strict validation of old local records. It contains no
credentials, service routes, claim headers or guest output. No cloud resource is
adopted by scanning/listing a prefix. One guest belongs to one Session; bindings
are frozen before the model. Partial failure/cancel invokes bounded cleanup.
Media/input and Computer bindings are revoked before exact-reference deletion.
Only authoritative not-found completes missing-resource cleanup. Authentication,
transport and permission errors remain unresolved, including after restart.
A TTL expiry never by itself erases an owned record or refunds a reservation.

## Computer, media and security

Cloud reuses the Sandbox Computer adapter, existing action allowlist, one-shot
approval and typed Sandbox events. It has backend identity `cua-cloud`. Host
Computer profiles cannot select cloud; only an explicit Sandbox profile can.
Model/AgentLoop contracts, MCP generic authority and text-only observations are
unchanged. No host mount, workspace copy, credential upload or file transfer.

Cloud uses the official `env` service on port 3211, including its spacesd
authentication header. Private media config comes only from the CLI command bound
to the exact guest.
The existing StreamService/RCDP client admits HTTPS/WSS only at `run.cua.ai`,
without userinfo/query/fragment, at a strictly shaped `/api/svc/.../...-env` (or compatible spacesd)
gateway route. Gateway bearer and claim headers are copied privately. DNS answers
must all be public and the chosen address is pinned for the dial; TLS verifies the
official hostname. No proxy environment, redirect, alternate URL or local media
fallback is used. Signed query URLs and unsupported configurations are refused.
Media unavailable does not change the action binding or route to the host.
Existing viewer tickets and exclusive Take / Give Back gates remain the browser
boundary. Frames and human input never enter history, Memory or Session SSE.

## HTTP/UI

Existing read-only `GET /api/v1/sandboxes` adds safe `backends` availability;
legacy `runtime` remains local. Individual GET exposes owned metadata only.
There is no raw create/delete, authentication mutation or pool management API.
The editor offers None, Host, Local Sandbox and Cloud Sandbox. Cloud shows fixed
resources, billing warning and ephemeral files before Send. Settings shows safe
availability and owned cleanup/expiry. Viewers use the active exact guest ID.

## Offline validation

Fake backend tests cover placement, legacy profiles, resource presets, bounded
CLI flags, signed-out status, partial creation/cancel, quota, unresolved deletion,
restart recovery, and cloud Session approval/cleanup. The TLS fixture verifies
private headers, prefixed RPC routes and redirect refusal. UI covers explicit
cloud configuration/cost. `npm run smoke:cloud` runs the existing viewer/takeover
flow against a simulated cloud backend; no real Fleet, paid claim or credentials.

## Known limitations and intentionally deferred work

Linux control host first. The CLI's generic failure code cannot safely distinguish
billing refusal from exhausted external capacity; no human stderr parsing is used.
The 64-record journal is not compacted. Managed pool GC/billing belongs to Fleet.
Private media tokens are captured at guest binding creation; later expiry fails
closed instead of refreshing credentials or retrying input. Actual cloud endpoint,
account quota, billing and media compatibility require an operator's paid opt-in
smoke. No guarantee of atomic remote provisioning/deletion or account-wide cost
ceiling is claimed.

Custom Fleet/pool CRUD, warm managers, alternate providers, persistent disks or
workspaces, workspace transfer, background agents, Intelligent Memory, Subagents
and External Agents are deferred.

## Validation status

Validated on 2026-10-08:

- Real Linux Docker execution, network disabled, Go 1.27.1: `gofmt -w .`,
  empty `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...`,
  `go test -race -count=1 ./...`, `go run ./cmd/daimon demo` — PASS.
  Existing symlink tests execute on Linux. Full suite repeated after the official
  env-service adjustment and added HTTP/DNS tests — PASS.
- `npm ci --offline --no-audit --no-fund`, typecheck, 55 tests in 12 files,
  final production build — PASS.
- Offline local and cloud Sandbox browser smokes — PASS: frames, two viewers,
  exact human input, agent exclusion, Give Back, deleted viewer unavailable,
  private history and joined shutdown. Cloud smoke repeated on final assets — PASS.
- Final embedded assets: Linux HTTP package tests and race tests — PASS.
- `git diff --check` — PASS; existing line-ending notices are not test failures.

No paid claim, actual desktop, credentials, external provider or real Fleet smoke
was used. Actual CUA authentication/provisioning/media/account behavior remains
unverified. **real CUA Fleet not validated**.
Phase 17 — Background Tasks: parte Native Routines Foundation implementada (escopo restrito); próximo passo é a validação com CUA real.

## Files created

- `docs/CLOUD_COMPUTERS_V1.md`
- `internal/sandbox/cloud_test.go`
- `internal/computer/fleet_media_test.go`
- `internal/computer/socket/fleet_test.go`

## Files changed in Phase 15

- Sandbox: `types.go`, `limits.go`, `errors.go`, `json.go`, `store.go`, `manager.go`,
  `cua.go`, `command.go` in `internal/sandbox`.
- Computer: `types.go`, `profile.go`, `manager.go`, `sandbox_adapter.go`,
  `cua_media.go`, `socket/socket.go` in `internal/computer`.
- Sessions: `types.go`, `resolve.go`, `approval.go`, `approval_adapter.go`,
  `sandbox_test.go` in `internal/sessions`.
- Bots: `internal/bots/store.go`, `internal/bots/validate.go`.
- Serve/API: `cmd/daimon/serve.go`, `internal/server/sandboxes.go`,
  `internal/server/sandboxes_test.go`.
- UI: `ui/src/api/types.ts`, `ui/src/api/client.ts`, `ui/src/App.tsx`,
  `ui/src/components/Editors.tsx`, `SandboxPanel.tsx`, `SandboxPanel.test.tsx`,
  `ComputerPanel.tsx`, `ComputerPanel.test.tsx`, `ApprovalPanel.tsx`.
- Offline browser fixture: `ui/scripts/approval-fixture/main.go`,
  `ui/scripts/approval-fixture/sandbox.go`, `ui/scripts/sandbox-smoke.mjs`,
  `ui/package.json`; regenerated `internal/server/ui/index.html` and JavaScript.
- Documentation: `AGENTS.md`, `README.md`, `docs/DAIMON_ARCHITECTURE_V2.md`,
  `docs/SESSION_RUNTIME.md`, `docs/HTTP_API_V1.md`, `docs/COMPUTER_VIEW_V1.md`.

All pre-existing work from prior phases remains in the working tree.


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
