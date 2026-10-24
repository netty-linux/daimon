# C2 offline cleanup identity correction

## Historical result stays FAIL

The external run at 2026-10-24T23:15:57Z completed both operational cases, but
cleanup threw `Own helper creator/entry/case binding failed.` Original reports,
log and one-attempt marker are preserved. This source correction never
retroactively makes that run PASS.

The throw site was the second metadata predicate in `Bound-Process`, called
from the root's `finally` after `supervisor-exit` appended its positive result.
It reacquired a numeric PID, checked start/executable, then queried CIM and
required creator, command line, entry and case. It retained no original-instance
handle and had no exited-state branch. It recorded neither the failed field nor
observed metadata. The recorded manifests have expected creator/entry/case.

Thus the old report proves the location and phase, **not** whether the historical
failure was exit-time metadata loss, creator mismatch or another entry/case
discrepancy. PID reuse normally fails the preceding birth-time check, but cannot
be excluded from an unrecorded race between queries. Do not claim an unobserved
historical cause. No old/external process is inspected or terminated to guess it.

A bounded own-process fixture now proves that a verified retained original handle
can be signalled while the CIM row/command line is unavailable. Missing live
metadata is not proof of foreign identity; missing PID alone is not exit proof.

## Ownership-preserving correction

Only offline test cleanup and report validation changed. Production watchdog
timing, controllers, VM configuration and journal were not changed or accessed.

Before the fault, the root registers four instances: its supervisor, then that
supervisor's payload/controller/watchdog. It acquires and retains kernel handles,
checks PID, birth time, executable, expected creator, exact script/role/case command
suffix and expected manifest values. Controller waits for registration acknowledgement
before either existing fault. No fault or emergency timing policy was modified.

| Observation | Decision/action |
| --- | --- |
| Verified original handle signalled | `EXITED_PROVEN`; no kill or live CIM requirement |
| Original handle alive, matching metadata | `LIVE_OWN`; revalidate immediately, terminate held instance, confirm exit |
| Original alive, divergent metadata | `LIVE_DIVERGENT`; no termination; global FAIL |
| Unavailable handle/state/metadata or unconfirmed exit | `INDETERMINATE`; no unverified termination; global FAIL |
| Previously completed cleanup | `ALREADY_CLEAN`; no repeated observation or kill |

A reused numeric PID cannot redirect a retained handle to its replacement. Exit
during revalidation is accepted only with the verified original handle. Command
exceptions remain recorded and cannot become clean success even if exit is later
observed. Partial registration cannot pass. Cleanup failures are recorded per
instance, while other registered instances are checked separately. Expected
identity, registration proof, mismatching metadata, command errors and exit proof
are saved in `cleanup-result.json`. No PID-only kill remains in root cleanup.

Each case is marked passed **after** its four cleanups complete. The normalizer
requires both cases and four distinct own-role cleanup proofs without errors or
external effects. Operational success followed by cleanup failure remains FAIL.

## Verification and remaining blocker

213 local checks passed: 66 lifecycle, 33 boot policy, 27 monotonic deadline,
12 real-process watchdog/transition, 22 emergency policy, 27 cleanup (three actual
bounded own-process fixtures), 26 entry/report tests. Cases include exited/live/
divergent/unknown, missing proof/closed handle, creator/entry/case/role/path mismatch,
simulated PID reuse, exit races, command errors, timeout and duplicate cleanup.
A real wrong-creator registration is refused without affecting its live own fixture.
Source verification and diff checks passed. No VM access.

One corrected independent-suite attempt from Work was refused before graph
creation: `in_job=true`, `console_count=2`, `verified=false`. This is **BLOCKED**,
not positive independence proof. No retry, Job escape, UAC or policy bypass.
Both corrected positive cases still need the external run; no real-VM approval
is requested.

The fresh local `outputs/C2-cleanup-corrigido/INICIAR-C2-TESTE-OFFLINE.cmd` uses
the same non-elevating reviewed entry and updated source pins, without changing
the old failed attempt. One normal Explorer double click outside Work runs both
corrected cases with adjacent reports and an independent one-attempt marker.
Review that new global result before preparing any real-emergency request.

VM untouched; PR draft; 18 gates `unverified`. Normal boot Off at 70.6801584 s
preserved; historical 91.1312018 s remains temporal FAIL.
