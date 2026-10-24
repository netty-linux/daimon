# Emergency watchdog preparation — BLOCKED

No real emergency VM trial, VM inspection or VM reconfiguration occurred in this
revision. No service, application, Windows setting or kernel Job was modified.
All 18 gates remain `unverified`; PR remains draft. No boot approval requested.

## Real evidence retained separately

The [second boot](evidence/HARNESS2_C2_SECOND_BOOT_2026-10-24.json) passed the normal
controller route on `0ce479d`: request 70.0426436 s, dispatch 70.1701778 s, Off
observation 70.6801584 s, original 3/3/4 restored and journal/ACLs validated.
The watchdog found an already-Off VM and sent no poweroff command. This is not
emergency proof. The [first boot's 91.1312018 s failure](evidence/HARNESS2_C2_FIRST_BOOT_2026-10-24.json)
remains a permanent temporal FAIL, never superseded by the second result.

## Concrete blocking evidence

[Permanent sanitized refusal evidence](evidence/HARNESS2_C2_HELPER_ISOLATION_BLOCK_2026-10-24.json).

A read-only native `IsProcessInJob` query found the Work command helper in a
Windows Job. A separate elevated, noninteractive helper launched through normal
UAC also returned `job_query_ok=true`, `in_job=true`, isolated console and
`verified=false` on 2026-10-24 at 22:22:43Z. The new suite refused the fault before
creating its target/controller/watchdog graph. `vm_access=false`.

We have not proved these are the same Job, inspected its kill-on-close limits or
proved that Work closure would actually kill the watchdog. Membership alone does
not establish that behavior. It is nevertheless insufficient for the conservative
required guarantee: no unknown enclosing Job and no shared console. UAC alone did
not prove escape from that fault domain. No breakaway flags, Job policy changes,
service installation, Task Scheduler registration or repeated launch attempts were
used to bypass the block.

The current fail-closed runtime checks reject a controller, supervisor or watchdog
with any Job membership, failed membership query, unknown/shared console or stale
PID/start-time binding. They execute before boot/readiness and before injecting
the fault. This revision can therefore also block a normal launch in this host
context; that is intentional, not permission to weaken the check.

## Implemented candidate fault — not run on a VM

`harness2-c2-emergency-probe.ps1` is the future elevated supervisor. It never
auto-elevates or retries. It launches a fresh `pwsh -NoProfile -NonInteractive -File`
controller directly, so the controller has its own entrypoint instead of running
inside a Work/terminal application. The controller spawns the independent watchdog.
All three must prove no Job and no shared console. The supervisor is not required
for the watchdog's lease loop or poweroff; it gathers evidence and independently
rechecks the final protected state. Native VMMS, Windows, storage and runtime remain
shared dependencies, so this is not host/provider fault tolerance.

The new explicit `HeartbeatStall30` mode accepts only the existing 2/2/2 GiB
candidate and a distinct `C2-WATCHDOG-STALL30-V1` acknowledgement. A normal approval
or old V2 guard cannot activate it. Guard schema is now 3. After fresh admission,
exact ownership/ISO/configuration checks, watchdog readiness/PID/start-time proof
and actual Running observed within the first 10 s, the controller writes a
protected fault intent and one last heartbeat. It then blocks **only its own
dedicated thread for 30 seconds**. It sends no heartbeat or normal shutdown during
that interval. There is no arbitrary PID input, process kill, process suspension
API, service action or Hyper-V-global change in the fault mechanism.

This models a bounded unresponsive controller, not a crashed controller. After
30 s it resumes, waits at most 5 s for watchdog proof and uses the existing safe
reconciliation path. The controller remains the sole journal writer under its
operation lock. Watchdog does not acquire that lock or write the journal; it uses
its authenticated VM reference and transition lock to stop and restore memory.
The fault is injected only after the start transition lock has been released, so
the intentionally stalled controller cannot hold the watchdog's stop lock.

## PASS and failure rules

Emergency PASS requires all of the following, recomputed by the supervisor from
the fresh protected guard, not merely a public controller receipt:

- Exact operation/VM and distinct helper PID/start-time/isolation bindings.
- Cause `controller-heartbeat-lost`, not a normal stop or absolute-deadline fallback.
- Watchdog saw `Running`, actually invoked its own poweroff command and confirmed
  Off. Finding an already-Off VM cannot pass.
- Stop request within **14 s** of the last heartbeat and confirmed Off within
  **30 s** of that heartbeat, also within the unchanged **90 s** absolute boot
  deadline. Existing heartbeat expiry is 10 s, native polling approximately 2 s
  and confirmation observation budget 15 s. These are acceptance thresholds,
  not hard guarantees against provider/scheduler hangs.
- No command/clock/confirmation-timeout failure; no host safety abort masquerading
  as the intended proof; original 3/3/4, private control/media and `configured`
  journal independently verified after Off. No installation or network.

Existing admission remains >=6 GiB, CPU p95 <=60%, commit/pagefile/balancer limits
unchanged. The independent watchdog retains critical native physical-memory and
commit aborts plus heartbeat/absolute clock protections. The normal controller's
full CPU/pagefile/event collection intentionally pauses with the fault; this is
a bounded monitoring gap, not continuous full telemetry coverage. Its VM ceiling
remains 2 GiB and only a trusted offline ISO is permitted. Any unexpected pressure,
integrity fault or watchdog nonresponse fails this proof, even if another recovery
route later restores Off. Unknown Off forbids restoration/reconciliation and new
stages. No later recovery rewrites a temporal/proof failure into PASS.

If the controller resumes without valid watchdog proof, its existing recovery can
attempt an exact-owned-VM stop and restore, but the emergency trial remains FAIL.
The supervisor has no VM mutation fallback. It never kills/restarts uncertain
helpers. A supervisor timeout or unreadable Off must be reported unresolved and
requires separate administrative inspection. If the controller itself dies, the
watchdog still has its private reference/lease and may stop/restore; it does not
take over journal writing. A new reviewed reconciliation is then required.

## Tests and next unblock condition

Deterministic tests reject wrong VM/helper binding, no actual watchdog command,
already-Off, stale/wrong cause, timing violations and partial command failures.
The existing real-process lease/timed-stop suite does not prove independence from
an enclosing Job. The new independent suite uses only its own disposable processes:
one scenario stalls a controller for the full 30 s; another exits both an own
heartbeat producer/controller and its own supervisor. A surviving watchdog must
stop its own exact PID/start-time/path-bound test payload and write timing proof.
It never calls Hyper-V. The second scenario is process-exit independence evidence,
not the production `HeartbeatStall30` proof.

**Those two positive detached scenarios remain NOT RUN here because the launcher
failed isolation.** The actual negative refusal under UAC is proven. CI may run
the positive scenarios only when its own environment satisfies the same check;
otherwise it reports the expected refusal and explicitly marks independence
unproved. Green CI is not a waiver for this host block.

Next safe action is an offline-only invocation of the existing independent suite
from a user-started administrative PowerShell outside Work/its terminal, with
`-NoProfile -NonInteractive -File scripts/test-harness2-c2-emergency.ps1 -RequireIndependent`
and an explicit local report path. This is a candidate launch context, not an
assumption that it is detached. It must actually report no Job, isolated consoles
and both positive scenarios passing. It requires no OS/service change or VM boot.
If that also fails, stop; do not improvise another launch route or weaken checks.

Only after that proof, current CI and a fresh exact-VM/integrity/admission preflight
may a new approval be requested for one real `HeartbeatStall30` execution: UAC,
temporary 2/2/2, trusted ISO offline, self-thread stall, watchdog stop, verified
Off and 3/3/4/journal recovery. No such approval is requested in this BLOCKED state.
