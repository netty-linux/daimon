# Sandbox Computers v1 — Phase 14

## Audit and plan before source changes (2026-10-08)

Reviewed AGENTS, README, architecture, Session, Computer Use/View, MCP, Memory
and Web Approval contracts, plus the existing Computer/Session/server/CLI/UI.
Earlier phases are uncommitted in this working tree and must be preserved.

CUA public audit:
- https://github.com/trycua/cua/blob/main/docs/content/docs/cua-sdk/concepts/how-sandboxes-work.mdx
- https://github.com/trycua/cua/blob/main/docs/content/docs/cua-cli/reference/cli/sandbox.mdx
- https://github.com/trycua/cua/blob/main/libs/cua/skills/cua-sandboxes/SKILL.md
- https://github.com/trycua/cua/blob/main/docs/content/docs/cua-cli/reference/cli/runtime.mdx
- CLI public JSON shape/flags: libs/cua/crates/cua-cli/src/sandbox.rs and lib.rs.
- SDK service contracts: libs/cua/crates/cua-sdk/src/native/sandbox.rs.
- Container runtime selection and doctor contracts: cua-vmm container/mod.rs,
  auto.rs and cua-daemon local.rs. No external implementation is copied.

The Rust CLI 0.4.1 documentation supports global --json, --embedded and
--state-dir; create --on local --kind container --runtime gvisor, --cpu,
--memory, --network default, --port spacesd=3211, --wait desktop and
--ready-timeout; info and rm accept exact local:NAME, rm requires --force.
Create already deletes readiness failures, but DAIMON must independently journal
and reconcile partial/cancelled operations. CLI create is not ephemeral by itself.
Default engine/runtime can silently resolve differently, so every placement is
explicit, and doctor must confirm a ready local Unix container engine and gVisor.
No runtime setup, daemon autostart, managed VM bootstrap or runc downgrade.
The CLI disk flag does not enforce a quota; network none requires QEMU, not these
containers. Do not claim either guarantee.

Plan:
1. Separate internal/sandbox domain: strict IDs/profile/presets, typed errors,
   minimal Create/Get/Delete/Close backend, explicit local/gVisor/Linux limits.
2. Original direct-exec CLI adapter with bounded stdout/stderr, contexts/process
   cleanup, strict JSON, official exit mapping and explicit operational env.
   Private CUA home/state under the controlled data directory. No shell, cloud,
   account/provider secrets, arbitrary images, guest env, mounts or commands.
3. SandboxManager journals unpredictable exact names and per-Session ownership
   before create. One spent sandbox per Session, max four resources and bounded
   registry. Readiness must complete before Computer capability resolution.
4. Versioned strict sandboxes.json with atomic controlled-directory replacement,
   no credentials/endpoints. Persist failed cleanup. Startup only reconciles exact
   registry-owned local names, never prefix scans/adoption/deletion of strangers.
5. Extend ComputerManager with a bounded catalog of independent Computer managers;
   reuse existing binding/control/view semantics. Sandbox lifecycle stays outside
   Computer. Sandbox IDs and Computer IDs differ; stable sandbox tool namespace.
6. A scoped sandbox adapter uses official guest Driver MCP via cua sb mcp on an
   exact immutable local ref. Reuse existing small approved action subset and
   presenters. Obtain server-only media config from the guest service; reuse
   Phase 13 RCDP abstraction and UI, never fallback to host actions or media.
7. Separate optional Bot SandboxProfile, safe Linux/gVisor resource presets and
   browser toggle. Existing ComputerProfile and Bots remain valid. Freeze profile
   per Start. No content/Memory may alter provisioning, placement or resources.
8. Session preparation metadata distinguishes creating/ready/cleanup. Environment
   setup precedes first model call. Reverse cleanup closes media/control/binding
   before deleting guest; handle completion, failure, abort, lifetime and shutdown.
9. Readonly sandbox list/get with safe runtime status and ownership. Creation only
   through Bot/Session; no raw create/delete/admin/terminal API in this cut.
10. Settings/Sandboxes, explicit Bot mode, status and ephemeral/outbound/host
    workspace disclosure. Attach existing Computer viewer to exact sandbox ID.
11. Fake backend/protocol tests: ownership journal, partial create/cancel, deletion
    failure/retry, reconciliation, bounded limits/copies/concurrency, no host
    fallback, no secrets/mounts, sandbox actions/media/takeover, UI and browser E2E.
12. UI install/typecheck/tests/build then actual offline Linux format/vet/test/race/
    demo and diff check. Real CUA smoke only if an already configured safe runtime
    exists; otherwise explicitly unrun. No Phase 15 implementation or commit/push.

Security boundary: gVisor contains guest processes/filesystem through the external
runtime; DAIMON does not itself implement that isolation. Outbound networking can
reach the internet/local services. Host Thread workspace remains the separate
existing native-tool boundary and is never mounted into the guest. Image pulls
may occur during explicit sandbox creation; runtime installation is manual.

## Implemented contracts

`internal/sandbox.Manager` owns isolation lifecycle. `computer.Manager` owns
capabilities, control and media. A Session owns the temporary Computer binding.
A separate optional `sandbox_profile` selects the environment. Existing Bots
without that field keep their previous behavior.

```json
{"backend":"cua-local","image":"linux","runtime":"gvisor","browser":true,"resources":"small","network":"outbound"}
```

Profiles accept exactly those six fields. Image, runtime, backend and network
are closed aliases. Resources are `small` (1 CPU, 2048 MiB) or `standard` (2 CPU,
4096 MiB). Default create timeout is 120 seconds, lifetime 15 minutes, cleanup
15 seconds; explicit Manager options are validated. At most four unresolved
resources and 64 journal records are admitted. A Session ID cannot create twice,
even after successful deletion. Journal history is bounded and is not compacted
in this release; reaching capacity fails closed.

### Operator configuration and runtime

Run on a Linux host with an already installed compatible CUA CLI and local
container engine/gVisor:

```text
daimon serve --data-dir /controlled/daimon-data --sandbox-cua /absolute/path/to/cua
```

The executable is operator configuration, never accepted by Bot/browser APIs.
Without the flag, Sandbox metadata remains visible and provisioning is
unavailable. Runtime installation is manual. DAIMON does not run setup or start
a daemon. The adapter uses public CLI JSON in embedded mode and private
`<data-dir>/cua-local` home/state. It requires a reachable Unix container engine
and gVisor in read-only runtime doctor. Windows/macOS, remote engines, runc and
QEMU are unsupported. No downgrade or host desktop fallback exists.

Canonical image alias `linux` is resolved by CUA. Resolved image metadata is
retained when returned, without claiming digest pinning. CPU/memory are runtime
container limits. No disk quota is claimed. Network is explicitly outbound;
this does not prevent reaching host/local services or implement an egress
firewall. Isolation is supplied by the external gVisor runtime, not DAIMON.

### Lifecycle, ownership and recovery

Transitions are closed: creating → running/failed/deleting; running →
deleting/failed; failed → deleting; deleting → deleted/failed. Deleted is terminal.
The journal is versioned, size bounded and rejects unknown/duplicate/case-aliased
fields, invalid ownership and inconsistent profile/resource/state metadata.

The manager writes a creation intention before external effects. Names use
12 random bytes: `daimon-<24 hex>`. Exact `local:NAME` references stay private.
The public Sandbox ID is `sb-<24 hex>`, and its distinct Computer ID is
`sandbox-<24 hex>`. Registry ownership is structural, not a cryptographic proof
against another process able to modify the controlled data directory.

Create waits for desktop readiness, then discovers the guest Driver's approved
Computer capabilities. Any failure prevents the first model call. Sandbox tools
use `mcp__sandbox__NAME` and are bound to one immutable guest reference. Agent
arguments cannot choose another sandbox, host computer, runtime or endpoint.
Missing guest capabilities fail the Session; media absence remains explicitly
unavailable. The existing native Thread workspace remains a separate host
capability boundary. It is never implicitly mounted into the guest.

Completion, failure, cancellation and lifetime expiration revoke the Computer,
media and input ownership before deleting the guest. Cleanup checks the exact
owned reference/runtime and only marks deleted after backend confirmation.
Failed deletion remains `failed`/`unresolved`, retaining ownership and consuming
capacity. Close performs bounded cleanup and reports unresolved IDs privately.
Startup examines only journal entries; it marks remaining owned resources as
orphans and reconciles exact references. Unknown CUA sandboxes are never listed,
adopted or deleted by prefix. A missing owned resource completes cleanup.

The single-writer controlled-directory journal uses a private temporary,
Sync/Close and rename. Directory fsync, exclusion of external writers and power
loss recovery guarantees are not promised. Guest data is ephemeral. There are
no persistent volumes or exports. Thread/history identity survives deletion;
a subsequent Session gets a fresh environment.

### Computer, media and approval

Guest Driver actions use public `sb mcp local:NAME spacesd tools/call` commands
with explicit argv and bounded JSON. No shell, exec, filesystem transfer,
clipboard, host mounts or guest environment configuration is exposed.
Provider credentials are not passed to CUA; only explicitly selected operational
environment variables reach the CLI. Its private media configuration stays
server-side and never enters registry, events, model context or API DTOs.

The Phase 13 RCDP viewer, single human input owner and Give Back semantics are
reused. Two viewers can observe but only one controls. Agent input is rejected
while a human owns the guest. Guest-specific approval displays identify its
Computer and disposable environment; every call still needs individual approval.
A stale `sandbox-*` Computer ID has a closed lookup path that never queries the
host backend. Cleanup removes the catalog entry and closes existing viewers.

### HTTP and UI

`GET /api/v1/sandboxes` returns safe owned metadata and runtime availability.
`GET /api/v1/sandboxes/{id}` returns one owned record. There is no raw create,
delete, terminal or administrative endpoint. Bot configuration plus Session
admission are the only creation path.

Settings / Sandboxes shows runtime availability, owned environments, image,
owner and cleanup. Bot Computer Mode supports None, Host Computer and Sandboxed
Computer, with browser toggle and resource presets. Session environment metadata
uses creating, ready, cleaning_up, deleted and failed; preparation occurs before
AgentLoop. Computer tab attaches only to the active Session's exact guest ID.

Safe typed events are sandbox_creating, sandbox_ready, sandbox_cleanup_started,
sandbox_cleanup_completed and sandbox_failed. They carry no names, endpoints,
paths, command arguments, tokens or results. Memory and conversation text cannot
change the frozen profile.

## Validation and limitations

Automated domain, process, Session, API and UI tests use fake backends and
loopback fixtures. `npm run smoke:sandbox` exercises real Go Session/HTTP/UI,
frames, two viewers, takeover, exclusive input, Give Back, approved guest action,
cleanup, stale-view failure, untouched host and persisted history. It simulates
backend behavior and does not prove real OS isolation.

Real CUA/gVisor smoke is optional and has not been run in this Windows workspace.
No runtime is installed automatically. Local Linux only; no cloud, pools,
persistent environments/volumes, snapshots, host bind mounts, file transfer,
clipboard, Intelligent Memory, Subagents or External Agents. Next recommended
phase: Phase 15 — Cloud Computers, intentionally not implemented here.

### Actual validation (2026-10-08)

- Linux `golang:1.27.1`, network disabled: gofmt -w ., empty gofmt -l .,
  go vet ./..., go test -count=1 ./..., go test -race -count=1 ./... and
  go run ./cmd/daimon demo all passed. Existing symlink suites ran in Linux.
- git diff --check passed (Git only reported pre-existing line-ending warnings).
- UI npm ci --offline --no-audit --no-fund, npm run typecheck, npm test and
  npm run build passed: 52 tests in 12 files.
- Playwright Edge sandbox smoke passed after the final frontend build.
  Existing host Computer viewer smoke passed on repetition; one earlier
  repetition failed to observe the expected human text input.
- Additional Sandbox Session tests passed in Windows, including freezing the
  profile while provisioning, abort before/during run and unresolved deletion.
- Real CUA/gVisor smoke was not run; no CUA executable is configured on this host.
  Offline fakes validate orchestration and scoping, not physical runtime isolation.


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
