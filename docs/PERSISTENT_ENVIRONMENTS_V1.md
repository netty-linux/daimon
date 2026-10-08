# Persistent Environments v1 — Phase 16

Status: Phase 16 implemented and validated offline on 2026-10-08. The numbered sections record
the plan written before source changes. The implementation contract and actual
validation evidence below supersede planning statements; Phase 15 invariants remain.

## 1. Audit findings and remaining decision

The current SandboxManager owns disposable local/Fleet guests; ComputerManager
owns interaction, media and input control. Sessions provision before the model,
close resources before persisting the assistant and publish Completed last.
WithIdleThread provides an admission reservation for deletion without filesystem
I/O under the Session Manager mutex. Thread.Workspace is the separate existing
host native-tool boundary; it is not guest persistent storage.

Existing managedworkspace snapshots reject observed links/special files and bound
entries and bytes. Their private copy/apply lifecycle must remain independent of
the new durable Environment. Conversation History and Memory remain separate.
The working tree contains extensive earlier uncommitted work; preserve it.

Public CUA contracts inspected:

- [CLI sandbox access](https://github.com/trycua/cua/blob/main/docs/content/docs/cua-cli/reference/cli/sandbox-access.mdx)
- [CLI cp implementation](https://github.com/trycua/cua/blob/main/libs/cua/crates/cua-cli/src/sandbox.rs)
- [spacesd README](https://github.com/trycua/cua/blob/main/libs/cua-spacesd/README.md)
- [FilesystemService protocol](https://github.com/trycua/cua/blob/main/libs/cua/proto/cua/env/v1/filesystem.proto)
- [SDK reference](https://github.com/trycua/cua/blob/main/docs/content/docs/cua-sdk/reference/index.mdx)
- [Spaces contract](https://github.com/trycua/cua/tree/main/libs/cua/crates/cua-spaces-contract)

The inspected cp implementation copies one file, not a directory tree. The
official FilesystemService supports bounded pagination, link-aware listing,
chunked reads and resumable uploads, but EntryInfo has no inode/link-count field.
Therefore that metadata cannot prove that a regular guest file has no hard links.
Do not present ordinary type checks as satisfying the requested hard-link rule.

Authorized exception (2026-10-08): the user explicitly permits a fixed internal
DAIMON helper through the official Process API exclusively for metadata inspection.
It cannot expose shell/arbitrary commands, use user/model command strings, transfer
contents or relax a failed security proof. Fixed arguments, structured bounded output,
cooperative cancellation and timeouts are mandatory. Transfers remain exclusively
FilesystemService. This exception is also recorded in AGENTS.md.

## 2. Domain and ownership

Introduce internal/environments with no imports from model, agentloop, provider,
Memory or Conversation Store. Each Environment has one immutable Thread ID and
a random restricted Environment ID. One Thread has at most one Environment.
Its existence does not imply a running Sandbox or Computer. Session is temporary.

## 3. Storage authority

The application explicitly opens <data-dir>/environments. Files live in private
filesystem directories; versioned metadata and manifests contain metadata only.
Do not store contents in JSON, Sandbox journals, Memory or Cua Volume. Controlled
Linux directories use 0700 and materialized regular files use 0600.

## 4. Workspace representation

Represent regular files and directories, including empty directories and .git.
Use a portable bounded relative-path contract. Reject absolute paths, traversal,
links, special files, duplicate paths and case collisions before materialization.
No magical exclusions or automatic Git operations. Conservative permissions do
not preserve executable bits or arbitrary source modes; disclose this limitation.

## 5. Manifest

Sort entries deterministically; record path, type, exact size and SHA-256 for each
file, plus aggregate counts/bytes and a deterministic framed manifest hash.
Validate metadata, manifest and actual tree together before accepting a revision.
Contents remain private and are never automatically inserted into model context.

## 6. Revision and conflicts

Start an explicit empty Environment at revision zero. Freeze its revision for a
Session. Successful sync requires that exact expected revision and increments
once; detect uint64 exhaustion before staging. A mismatch is a controlled conflict,
never a lost update. One process owns the store; cross-process writes unsupported.

## 7. Staging and commit

Write a complete new workspace, manifest and revision metadata into an exclusive
owned stage. Check all limits, hashes and context before publishing a pointer to
the complete revision. Readers observe one complete revision. Never truncate the
valid current workspace. A cancellation observed before commit prevents publish.

## 8. Retention and recovery

Keep current plus bounded staging/previous state, not an unbounded revision log.
On restart inspect only exact registered DAIMON-owned stages and safely discard
uncommitted ones. Unknown/corrupt ownership fails explicitly; no prefix sweep,
adoption or deletion of arbitrary directories. Directory durability after power
loss and exclusion of external writers are not promised.

## 9. Transfer boundary

Use one private transfer abstraction bound to the exact owned Sandbox lease.
Both placements share it; the model cannot choose refs/endpoints/destinations.
Only the fixed guest workspace root is eligible. Official FilesystemService
listing/read/upload is preferred; do not recursively cp an arbitrary guest tree.

## 10. Transport security

Reuse validated loopback local or official Fleet TLS transport, private copied
headers, bounded responses, context and redirect/SSRF protections. No public share
URL, credentials or signed URLs in events, snapshots, errors or persisted metadata.
Transport capability absence fails explicitly; there is no host/local fallback.

## 11. Hydration

Reserve/freeze the Environment, provision Sandbox, materialize only the frozen
revision at the backend's canonical workspace, verify exact entries and hashes,
then open the Computer binding and call the model. Any failed/partial hydration
prevents model execution and still invokes existing bounded guest cleanup.

## 12. Successful sync-back

After successful AgentLoop and a context check, export only canonical workspace
into bounded private staging, validate completely and commit with expected revision.
Failed or aborted loops never initiate sync. Sync failure fails Session and preserves
the previous valid revision; cleanup still runs, including cloud claim release.

## 13. Commit versus later failure

Preserve existing assistant-after-cleanup ordering: loop success, workspace commit,
resource cleanup, assistant append, Completed. Cleanup/assistant failure after a
workspace commit cannot undo the new revision. Expose safe committed revision
metadata even when Session later fails/aborts; no false rollback or Completed.
Abort after commit cannot undo filesystem effects. No cross-store ACID promise.

## 14. Concurrency and cancellation

Use per-Environment reservation in addition to the existing per-Thread admission.
Deletion and execution must share reservation semantics. Store mutations serialize
inside one process; Session locks never cover transfer or disk I/O. Transfer has a
positive bounded duration and respects earlier caller/Sandbox lifetime deadlines.

## 15. API

Add strict POST/GET/DELETE /api/v1/threads/{id}/environment. Creation explicitly
creates an empty Environment; GET returns safe metadata only. Creation/deletion
requires idle Thread admission. Thread deletion is blocked while its Environment
exists. Reject unknown JSON fields, queries and unavailable dependencies safely.
No file browser, raw filesystem endpoint or automatic import from Thread.Workspace.

## 16. UI

Selected Thread shows explicit enable/delete, revision, file count, bytes and
updated timestamp. Deletion needs deliberate confirmation and is disabled during
active Session/transfer. Display hydrating, saving and cleanup separately. Cloud
saving retains compute briefly, bounded by transfer and lifetime; explain cost.

## 17. Session metadata and events

Freeze Environment ID/starting revision in Binding. Keep persistent metadata
distinct from the existing ephemeral Snapshot.environment Sandbox metadata.
If typed lifecycle events are needed, carry only fixed kinds and counters. No file
paths, names, contents, commands, private refs, URLs or free-form error text.

## 18. Proposed initial limits

Deliberately small initial bounds: 32 Environments, 64 files, 32 directories,
64 KiB per file, 1 MiB total workspace, 4096-byte paths, depth 8 and 60-second
transfer duration. Reject a whole oversized transfer without silent truncation.
These acceptance bounds do not claim strict memory/disk quotas on guest processes.

## 19. Offline tests

Store: reopen, strict versions/corruption, exact binary bytes/hash, directories,
limits, links/special files, staging failures, recovery, expected-revision conflicts,
defensive copies, simultaneous reads and canceled commits. Use actual Linux links.
Transfer: fake local/cloud endpoints, partial data, malicious paths, size/hash
changes, timeout and cancellation. Sessions: hydrate-before-model, success-before-
delete, failure/abort preserving old revision, cleanup after sync failure and new
revision retained on delete failure. Critical test: local A -> cloud A+B -> local
A+B, with store restart. HTTP/UI: explicit lifecycle, strict inputs, admission and
deletion guards, safe displays/errors and offline browser flow.

## 20. Migration and executed validation

No automatic Environment creation/import and no rewrite of existing Thread or
Sandbox schemas. Missing association preserves Phase 15 behavior. New schema
versions fail closed. No tests have been run for Phase 16 because source changes
have not started. Once implemented, run UI install/typecheck/tests/build before
actual Linux gofmt/vet/full tests/race/demo and diff check. Real CUA transfer smoke
is opt-in only; do not login, install, provision or spend automatically.

## 21. Limitations, files and next phase

Storage is local plaintext without secret detection. Persist files only, not
processes/VM state. Defer import/export, live sync, mounts, NFS/SMB/FUSE, warm pools,
Cua Volume, revision browser, background/scheduled work, collaboration, automatic
Git, Intelligent Memory, Subagents and External Agents. Next: Phase 17 Background
Tasks + Long-Running Agent Runs, requiring its own explicit scope.

Planned source areas: internal/environments; scoped transfer in internal/sandbox;
private transport reuse in internal/computer only if needed; Session preparation/
finalization and safe metadata; server routes/deletion guards; cmd/daimon composition;
ui API/components/tests/fixtures. Preserve workspace contracts, native tools, loop
and provider interfaces. Update AGENTS, README, architecture, Session, Sandbox and
Cloud documentation only when the implemented contracts and test evidence exist.

## Implemented contract

`internal/environments` owns metadata and immutable workspace revisions. The Store
requires an explicit existing private Linux directory (0700); only cmd chooses and
creates `<data-dir>/environments`. Windows keeps existing serve behavior with this
capability unavailable. Data is plaintext. No credentials, transcript, Memory,
files from Thread.Workspace, browser profile or guest home are implicitly imported.

Each `env-<24 random hex>` directory holds `current.json` and one
`r-<16 hex revision>/` containing `workspace/` plus `manifest.json`. Files are exact
binary bytes, 0600; directories 0700; executable bits are deliberately not preserved.
The manifest sorts portable ASCII relative paths, preserving .git/empty directories,
and includes per-file SHA-256 and aggregate deterministic SHA-256. No exclusions.
Limits: 32 Environments, 64 files, 32 directories, 64 KiB/file, 1 MiB total, path
4096 bytes/depth 8; encoded manifests/metadata at most 128 KiB. Case collisions,
missing explicit parents, traversal, observed symlinks/hard links and special files
fail closed. Reads check root/components/file identity and revalidate manifest.

An immutable reservation pins starting revision and bytes; defensive copies cannot
change its commit authority. One reservation prevents deletion/reentry. Store mutex
serializes readers/commit, while Session admission serializes HTTP mutations with
execution and Thread deletion. There are no cross-process locks; another process
using the same data directory is unsupported. Expected revision is rechecked at
commit, overflow rejects, successful sync increments once even with unchanged files.

Creation journals an exact random ID before constructing an unpublished tree;
`current.json` is renamed into place only after complete construction. Sync writes
exact `pending.json` intent before creating `stage/`, validates the whole tree,
renames it to the next immutable revision, writes/syncs/closes `next.json`, checks
context and current revision, then renames the metadata pointer. Readers see one
complete revision. No valid target is truncated. A returned committed flag remains
true even if subsequent owned-stage/previous cleanup fails. Pending intent permits
recovery of only its exact stage, next metadata and one previous/candidate revision;
corrupt ownership/version fails explicitly. No history browser or unlimited retention.
A process crash is recoverable through intent; filesystem directory durability and
power-loss guarantees are not promised. Unknown artifacts are not prefix-swept.

`Sandbox.Lease.Workspace` resolves only the exact owned running guest and backend.
Both local/cloud use one workspaceTransfer. Canonical guest root is `/workspace`,
fixed by DAIMON. It must be writable by the configured Linux image's acting user;
unsupported images/interpreter/process/filesystem capabilities fail hydration before
model execution. Existing nonempty guest root is rejected. No fallback or install.

Private control transport reuses CUAMedia's loopback/Fleet endpoint validation,
private header copies, TLS, DNS pinning, proxy refusal and redirect refusal. Only
allowlisted FilesystemService methods and ProcessService.StartProcess are reachable
through this internal infrastructure; no browser/model RPC proxy or Tool is added.
Responses are bounded to 256 KiB; transfers to 60 seconds and existing earlier
caller/Sandbox deadlines. No signed/public URL is generated or persisted. Uploads
use BeginUpload/UploadChunk/CommitUpload with CREATE_NEW, 0600, expected size/hash,
8192-byte-or-smaller chunks, no retries, 60-second partial-upload TTL and explicit
AbortUpload on failure (bounded 5-second cleanup). Export uses bounded ReadFile
streams, verifies entry/path/type/size, contiguous offsets, final length and hash.
No file contents travel through the inspection helper.

The embedded `workspace_inspector.py` is sent as fixed code in ProcessConfig args
for `/usr/bin/python3 -I -c`, cwd `/`, 15-second server timeout, bounded scrollback,
kill_on_disconnect=true; no shell, stdin, custom environment or caller command.
It uses only lstat/fstat/stat/scandir and no-follow directory descriptors, never
opens a regular file's contents, never writes, and rejects different devices, links,
special types, privileged mode bits, unstable identity, entry/size/depth/output limits.
Its structured report includes paths/types/sizes/inode/device/link count/mtime/ctime,
with at most 64 KiB stdout. Any stderr, malformed report, missing successful process
start/end, nonzero exit/signal/timeout, changed metadata or unsupported helper fails
closed. Guest errors/paths are never exposed in public error strings. Inspection
runs before and after export; hydration verifies by a full export/hash comparison.
These are observed checks, not writer exclusion or guest executable attestation.
The controlled image, OS/filesystem service and same-UID/admin writers are trusted.

Session resolves Environment only when explicitly created for its Thread. Its
presence requires Sandbox mode; Host/None fails before the model. Binding freezes
Environment ID/starting revision. Provision -> hydrate -> Computer binding -> model;
only successful uncanceled AgentLoop revokes Computer agent/human input before
export/stage/commit. The guest remains alive for transfer. Then existing
cleanup -> assistant append -> Completed ordering remains. Failed/aborted loops do
not sync. Hydration/sync failure still cleans compute. Commit cannot be rolled back
by later abort, deletion failure or assistant persistence failure: safe metadata
explicitly exposes committed bool/revision. No cross-store ACID transaction/resume.

POST/GET/DELETE `/api/v1/threads/{id}/environment` requires strict empty-object
creation, existing Thread and idle mutation admission. GET is metadata only; no
manifest entries or content. Delete is explicit, blocked by Session/reservation;
Thread deletion is blocked while an Environment exists. Existing Thread/Sandbox
schemas do not migrate and old Threads get no Environment automatically. Safe
Session `persistent_workspace` metadata is distinct from ephemeral `environment`.
Five fixed typed Environment events carry no IDs/paths/content/URLs/error text.

UI shows explicit enable of an empty Environment, revision/files/bytes/update time,
hydrating/ready/saving/saved/failed, committed revision and confirmed deletion.
It warns about plaintext/no implicit import and saving before release of paid cloud
compute. Existing Computer approval/media/takeover and History/Memory are unchanged.

## Phase 16 source inventory

Created: internal/environments/{workspace,store,json,platform_linux,platform_other,
store_test}.go; internal/managedworkspace/export.go;
internal/computer/workspace_rpc.go; internal/sandbox/workspace.go,
workspace_proto.go, workspace_inspector.py, workspace_test.go,
workspace_inspector_test.go; internal/sessions/environment.go, environment_test.go;
internal/server/environments.go, environments_test.go;
ui/src/components/EnvironmentPanel.tsx, EnvironmentPanel.test.tsx;
ui/scripts/approval-fixture/environment.go; ui/scripts/environments-smoke.mjs;
this document.

Changed: Session types/resolve/manager/errors/sandbox test fixture; typed event
constants; server dependencies/DTO/routes/messages/SSE; serve composition; UI API
DTO/client/App/package scripts; existing offline fixture main; embedded UI assets;
AGENTS/README/architecture/Session/Sandbox/Cloud/HTTP/SSE documentation. All prior uncommitted
phases remain intact. No commit, push, install or paid smoke is part of this work.

## Actual validation

UI npm ci offline, typecheck, 58 tests in 13 files and production build passed.
Final Linux execution passed: gofmt -w ., empty gofmt -l ., go vet ./...,
go test -count=1 ./..., go test -race -count=1 ./... and
go run ./cmd/daimon demo (completed). This run includes rejection of an
unregistered preexisting stage without adopting or deleting its contents.
git diff --check passed. The cached Go container used --network none.
Offline Environment browser smoke passed: explicit enable, local A -> cloud A+B ->
local, failed/aborted sync preservation, busy deletion, server restart and confirmation.
Targeted Linux storage/Sandbox/Session/server/Computer tests passed, including actual
link/FIFO cases; protocol fake round trip validates official filesystem-only content
transfer and fixed inspection. Existing Sandbox and Cloud browser regression smokes
passed. Browser compute is an offline fixture, not a real CUA deployment: image
compatibility, writable /workspace, /usr/bin/python3 and real local/Fleet services
remain unverified. No paid cloud smoke, installation or account provisioning ran.
