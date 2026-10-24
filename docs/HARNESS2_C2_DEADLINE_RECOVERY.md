# C2 deadline correction and future watchdog proof

No VM was started, stopped, inspected or reconfigured while developing this
revision. No second boot is authorized here. Original VM profile remains 3/3/4 GiB;
the temporary candidate remains 2/2/2 GiB with a 6 GiB admission floor. All 18
gates remain `unverified`.

## Permanent real failure

[Sanitized original evidence](evidence/HARNESS2_C2_FIRST_BOOT_2026-10-24.json)
records the original SHA, event times, metrics and hashes of the private raw
receipts. Start event 18500 at 19:05:26.8046477Z and Off event 18502 at
19:06:57.9358495Z give **91.1312018 seconds**, **1.1312018 seconds beyond** the
authorized 90. This remains a temporal failure. Boot, actual controller poweroff,
original-memory restoration, private ACLs and journal consistency were verified.
Emergency watchdog poweroff was not triggered or proved.

The old controller restarted its probe stopwatch after Start-VM returned and the
running journal was written. It checked duration only after host collection,
then read events before stopping. Sampling intervals were 3.177–3.536 s, and the
last elapsed sample was already 90.5544802 s. Stop-VM was synchronous; its 15 s
confirmation stopwatch started only after that command returned. Finally, any
confirmed Off was reported as finished without evaluating an absolute deadline.

The old receipt did not separately timestamp the request, command dispatch and
first Off observation. Therefore it cannot precisely apportion the excess among
sampling jitter, dispatch/provider latency and observation. Native sample UTC is
taken before slower collection, not at command dispatch. No such latency is
invented from those timestamps.

## New monotonic contract

| Stage | Meaning |
| --- | --- |
| Origin | Stopwatch/QPC ticks before Start-VM, never reset after start |
| Controller stop target | Origin + 70 s |
| Independent watchdog boot stop target | Origin + 72 s |
| Absolute Off deadline | Origin + 90 s |
| Stop confirmation observation timeout | 15 s from dispatch invocation, including dispatch latency |
| Independent watchdog lifetime ceiling | Existing 120 s from arming; not permission to run the guest for 120 s |

The controller has a 20 s lead: a 15 s confirmation allowance plus a nominal 5 s
budget for scheduling, sampling and provider overhead. This exceeds the observed
3.54 s sampling interval but is an engineering allowance, not a measured worst-case
Hyper-V bound. The watchdog checks the same protected origin across processes;
its 72 s target and approximately 2 s polling provide a separate earlier stop
route. It also stops on existing heartbeat/memory/commit faults. The absolute
90 s limit and all memory/CPU/commit/pagefile admission limits are unchanged.

Deadlines and heartbeat age use Stopwatch ticks/frequency. UTC is retained only
for audit; UTC jumps cannot extend the lease. The protected clock is bound to the
operation and VM. Invalid or reversed clocks and frequency mismatch fail closed.
Controller checks the lead before and after slow telemetry and bounds its sleep
by the remaining lead time. Optional event collection cannot turn a late Off into
PASS. The clock includes start-command latency and journal writes after start.

Stop timing distinguishes request (before veto/lock), dispatch invocation,
asynchronous command return, last observed non-Off state and first observed Off.
The latter two bound the observed state transition; Hyper-V event UTC is separate
corroboration, not a substitute for monotonic confirmation. `Stop-VM -AsJob` avoids
waiting synchronously for the whole poweroff before starting the observer timeout.
Completed job state alone is never Off evidence.
Both controller and watchdog must validate native Hyper-V `Stop-VM` metadata,
including `AsJob`, before any boot/readiness approval. Missing support fails closed.

Only a fresh, verified Off observation at or before 90 s yields `OFF_BY_DEADLINE`.
Late Off yields `OFF_LATE`; no observation yields `OFF_UNCONFIRMED`. A prior
confirmation timeout or clock fault remains failure after later recovery. Overall
normal completion additionally requires the intended stop route and full original
configuration/journal validation. Failures return nonzero; they cannot become PASS
merely because memory was eventually restored. Protected timing/deadline receipts
are retained separately from the journal's factual `configured` state.

## Concurrency and limits

The controller remains the sole journal writer under `operation.lock`. Watchdog
never takes over journal writes after controller death. Start and stop share a
protected transition lock; stop publishes a persistent veto before waiting. Start
rechecks veto/watchdog after metadata/file work and immediately before dispatch.
Restoration is serialized under the same transition lock and requires verified
Off and exact original/probe values. PID/start-time binding is required for helper
cancellation; VM ownership always uses exact ID, marker, name and configuration path.

Watchdog validates actual Off before honoring a controller `done` heartbeat.
No pending command/job, a heartbeat or a process exit is proof of VM Off. Job/stop
faults and unknown control-ACL drift remain unresolved, with no blind reset. Known
Off may be restored/reconciled even after the deadline, but the timing failure is
retained. Unconfirmed Off forbids restoration/reconciliation and further stages.

This is best effort, not a real-time guarantee. Native metadata/provider calls,
dispatch, filesystem access or an unresponsive VMMS can block or outlast the lead;
the transition lock also has a bounded acquisition timeout. An independent process
uses the same host/provider and cannot guarantee recovery from their failure.
Without a verified Off result the report must say unknown/unresolved. The revised
Hyper-V asynchronous stop path and timing margin have not been proved on a live VM.

## Offline validation and future authorized proof

Deterministic tests cover lead boundaries, delayed telemetry, backwards clocks,
UTC jumps, Off on either side of 90 s (including 91.1312018), completed command
without Off, dispatch failure, confirmation timeout, retained failure after recovery
and missing/reversed timestamps. Existing lifecycle tests cover failed poweroff,
restoration failure and refusal to reconcile an unverified VM.

Real Windows processes exercise the shared lease/timed-stop code, real monotonic
clocks, heartbeat files, process cancellation and transition locks. They include
terminating a separately created heartbeat producer, a fresh lease defeated by the
absolute boot clock, future ticks, false `done`, and timeout/veto races. Only test
processes created by the suite are terminated. These are not Hyper-V recovery proofs.

Before requesting a second run: all offline/real-process tests and CI must pass;
review this revision and its new `FIRSTBOOT-DEADLINE-V2` acknowledgement; revalidate
the exact Off VM, protected journal/media/ownership, ISO, event channels and unchanged
admission baseline. Obtain a new approval specifying whether the run tests normal
early stop or emergency watchdog cancellation. Never reuse the first approval.

For a future emergency proof, authorization must explicitly include terminating
only the new controller helper (exact PID/start time recorded for that attempt),
after the independent watchdog is ready and Running has been observed. No Codex,
Chrome, system service or other app is targeted. Controller death releases its
operation lock; watchdog must retain authenticated ownership, request its own stop,
record command invocation from a non-Off state, verify Off before the absolute
deadline and restore 3/3/4. Merely finding an already-Off VM does not prove its
emergency poweroff route. The host must meet the existing reserves throughout;
all ordinary pressure aborts stay armed. Do not inject a VMMS/provider failure.

Collect private watchdog timing/result, event 18502 and independent read-only
Off/configuration evidence. Because the controller was killed, journal reconciliation
is a separate reviewed administrative action: only after Off, media exclusivity,
original profile and control ACLs are verified. Do not relaunch Probe to repair a
stale journal. Stop on unknown state; no further boot, installation or adversarial
work is authorized by a successful process smoke or this procedure.

Reference: [Microsoft Stop-VM syntax and AsJob](https://learn.microsoft.com/en-us/powershell/module/hyper-v/stop-vm).
