# C2: bounded first boot preparation

No boot or installation is authorized by this document, an acknowledgement string,
or a passing offline test. All 18 isolation gates remain `unverified`.

## Latest status

The second real normal trial passed at **70.6801584 s** with verified original
3/3/4 and protected journal recovery. [Sanitized evidence](evidence/HARNESS2_C2_SECOND_BOOT_2026-10-24.json).
The first **91.1312018 s FAIL is permanent**. Emergency watchdog poweroff is still
unproved. New self-thread-stall preparation is **BLOCKED**: both the Work helper
and a separate elevated test helper are inside a Windows Job, so independence
from the enclosing session fault domain has not been proved. No live VM trial
was run in this preparation. [Current emergency proposal and block](HARNESS2_C2_EMERGENCY_PROOF.md).
Historical V2 instructions below are superseded for future launches by schema V3
and mandatory helper-isolation checks; existing receipts remain historical facts.

## Current revision: deadline correction after the first real probe

One real probe on `6450e38` verified boot, controller poweroff, restored 3/3/4 GiB
and a consistent private journal. Its **91.1312018 seconds violated the authorized
90-second limit** and remain permanently classified as temporal failure.
[Original sanitized evidence](evidence/HARNESS2_C2_FIRST_BOOT_2026-10-24.json).

The corrected executor uses an origin before start, controller stop at 70 s,
watchdog boot stop at 72 s and absolute Off confirmation deadline at 90 s.
Async dispatch and a 15 s observation budget are distinct from actual Off evidence.
Only timely confirmed Off can pass timing; recovery never erases late/unknown or
failed timing. Heartbeats are monotonic; journal ownership/writer restrictions and
all host budgets are preserved. Old acknowledgement/guard version is refused.

**This revision was tested offline only; no new VM inspection, boot or configuration
change.** The new timing path and emergency Hyper-V poweroff require fresh explicit
authorization and live proof. See [deadline and recovery procedure](HARNESS2_C2_DEADLINE_RECOVERY.md).
Historical preparation and blocked measurements below are retained as history.

## Light calibration revision (supersedes only the earlier first-boot budget)

Latest bounded retry ended at **2026-10-24T18:06:55Z: BLOCKED**. Profile 2 GiB and
the 6 GiB admission floor are unchanged. One 60-sample / 181.93-second baseline
including two resident helpers measured minimum available **5.84546 GiB**, CPU
p95 **14%**, minimum commit headroom **14.56987 GiB**, balancing **5.21875 GiB**,
and pagefile occupancy growth **0 MiB**. Only physical admission failed, by
**158.25 MiB**. A final point above 6 GiB does not override the failed minimum.
No additional sampling in this attempt; no VM changes or boot authorization.
Eight real-process watchdog/transition tests and 99 offline tests passed. Real
running-VM poweroff remains unproved and requires its own authorized calibration.

Previous decision: **BLOCKED**. Quiet sampling on 2026-10-24 ended at 17:53:29Z:
60 samples / 182 s, minimum available 5.95535 GiB, CPU p95 15%, minimum commit
headroom 14.68087 GiB and balancing headroom 5.26367 GiB, pagefile occupancy growth
0 MiB. The 2-GiB profile misses its 6-GiB threshold by approximately 46 MiB;
the 2.5-GiB profile misses its 6.5-GiB threshold. No authorization requested on
failed admission, no VM changes or boot. Five real-process watchdog tests and
99 offline lifecycle/policy tests passed. Current VM inspection is reused as prior
read-only evidence; the executor must revalidate the protected live state before
any separately approved mutation.

The original and future workload configuration remains dynamic 3/3/4 GiB. Its
default validator and general lifecycle admission remain unchanged. A dedicated
explicit probe opt-in accepts temporary 2/2/2 GiB or 2.5/2.5/2.5 GiB. Equal
minimum/startup/maximum bounds avoid relying on ballooning, hot-add or Smart Paging.
2048 and 2560 MiB are multiples of 128 MiB. No network, CPU, disk, firmware,
integration-service or checkpoint setting is changed by this calibration.

Canonical documents an Ubuntu 24.04 amd64 ISO minimum of 1.5 GB, with 3 GB suggested.
Therefore these are candidates for a simple stock-ISO boot, not a demonstrated
successful install or an approved adversarial runtime. Startup RAM must itself
meet requirements; dynamic memory does not rescue an undersized installer.

| Temporary dynamic min/startup/max | Required available host RAM | Probe / independent deadline |
| --- | --- | --- |
| 2/2/2 GiB | 6.0 GiB | 90 s / 120 s |
| 2.5/2.5/2.5 GiB | 6.5 GiB | 90 s / 120 s |

Each budget adds **3.5 GiB of available physical headroom** for the Windows host
and its already-running applications, plus **0.5 GiB of provisional incremental
Hyper-V overhead**, to the guest ceiling. The 3.5 GiB is extra free headroom, not
a limit on the host's total memory footprint. It is an engineering bound for a
90-second clean calibration, not a measured universal minimum Windows requirement.
Do not carry this reduced reserve into experiments or installation approval.

Use a quiet baseline of at least 120 seconds / 40 samples. The current measurement
also runs an additional native-sensor process so helper resident memory is already
charged to available RAM. Hyper-V incremental overhead cannot be measured while
the VM remains Off; 0.5 GiB is an allowance to calibrate, not an asserted result.
Neither paging nor reclaiming application working sets is counted as capacity.

Abort at available physical RAM below **3.75 GiB for 2 seconds**, or below
**3.5 GiB on any sample**, in addition to the existing CPU, commit, pagefile-growth,
ownership, network, event-error and ACL conditions. The independent native sensor
checks the critical threshold every approximately 2 seconds. Controller collection
is typically approximately 3 seconds including PDH reads. These are best effort
sampling intervals, not real-time guarantees. A stale heartbeat after 10 seconds,
invalid input, or the independent deadline also invokes the exact-owned-VM stop.

Rollback verifies Off, restores **all three original memory values (3/3/4 GiB)**,
reads them back, performs the full original validator and only then reconciles the
protected journal. Only original/probe values are accepted during partial-setting
recovery; unknown drift is refused. Protected ownership must be established before
any mutation and each stop authenticates the captured VMId, name, marker and path.

`test-harness2-c2-watchdog.ps1` launches real Windows child processes and runs the
same watchdog loop against real heartbeat files, a monotonic clock and native
memory telemetry. It verifies stale lease, deadline, malformed input, a deliberately
high critical-memory threshold and a completed lease. Four cases terminate only
the exact test PID/start-time; completion leaves it alive until test cleanup.
This tests process execution, IPC, timing and a real stop adapter without mocks.
**It does not prove Hyper-V poweroff**. The VM stop interface was checked in
read-only WhatIf; its real running-VM shutdown and restoration remain part of a
separately authorized first calibration. No live VM stop is claimed from offline tests.

The first-boot readiness review additionally found and closed a controller/watchdog
transition race. A protected existing file lock serializes start and hard-poweroff.
A stop request writes a persistent protected veto before acquiring that lock;
the controller checks both veto and live watchdog process inside the start lock,
including immediately before Start-VM. A later start cannot follow a completed
watchdog stop. The controller also aborts if the watchdog exits during monitoring.
Three additional real-process tests verify lock timeout, a veto surviving lock
release and refusal after watchdog exit. An unresponsive Hyper-V call can still
prevent timely recovery; the executor must report unresolved rather than claim Off.

References: [Canonical ISO memory requirements](https://ubuntu.com/server/docs/reference/installation/system-requirements/),
[Hyper-V Ubuntu support](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/supported-ubuntu-virtual-machines-on-hyper-v),
[startup RAM and cold-start Smart Paging limitations](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/dynamic-memory).

## Read-only evidence

The protected journal and live Hyper-V inventory agree on the owned VM and VHDX.
The VM is Off, Generation 2, with two virtual processors, dynamic memory
3/3/4 GiB, no network adapters, no checkpoints and disabled integration services.
Secure Boot uses MicrosoftUEFICertificateAuthority; the authenticated private
Ubuntu DVD is first in the boot order. Its SHA-256 was checked again against
the previously authenticated ISO digest. Root, journal and lock ACL checks pass.

The VMMS and Worker administrative event channels are readable. A scoped
`Stop-VM -TurnOff -Force -WhatIf` succeeds and leaves the VM Off. This does not
prove actual shutdown, installation, runtime ACL stability or real disposal.

## Previous admission policy and historical measurement

Decision on 2026-10-24: **BLOCKED**. A quiet 60-sample / approximately 182-second
baseline measured minimum physical availability 6.569 GiB, CPU p95 43%, minimum
commit headroom 17.676 GiB, minimum balancing availability 5.881 GiB and pagefile
occupancy growth 0 MiB. Only physical admission failed in this quiet window.
The lighter proposal is short by 0.931 GiB; the VM remains Off at its original
3/3/4 GiB configuration. No boot authorization is requested while this fails.

The former 8 GiB threshold is a local policy, not a universal Hyper-V requirement.
Admission now explicitly budgets the guest maximum, a provisional 0.5 GiB
virtualization allowance and an unchanged 4 GiB physical host reserve:

| Profile | Guest startup/maximum | Required physical availability |
| --- | --- | --- |
| Existing configuration | 3/4 GiB | 8.5 GiB |
| Proposed temporary ceiling | 3/3 GiB | 7.5 GiB |

The second profile requires separate approval to cap the maximum at 3 GiB and
restore 4 GiB after shutdown. It does not reduce startup memory or the host reserve.
No ballooning or Smart Paging benefit is assumed for cold startup.

Admission also requires a baseline of at least 120 seconds and 40 samples,
CPU p95 at most 60%, commit ratio at most 80%, pagefile occupancy growth at most
128 MiB, commit headroom of guest maximum + 2.5 GiB, and Hyper-V balancing
availability of guest maximum + 0.5 GiB. Missing required telemetry blocks admission.
Native physical/commit/CPU telemetry and the registered Hyper-V balancing gauge
work on this host. Paging I/O rate is unavailable; pagefile occupancy growth is
reported separately and must not be presented as an I/O rate.

## Prepared executor; not executed

`scripts/harness2-c2-firstboot.ps1` prepares a headless stock-ISO probe of at most
180 seconds, with no installer input. It revalidates protected ownership, the
original Off configuration, ISO hash, event channels and a fresh baseline.
It requires explicit operation/VM/profile acknowledgement after human approval.

A separate protected watchdog receives exact ownership references, warms its
sensors before readiness, and checks the controller heartbeat. A 210-second
deadline or a stale heartbeat triggers scoped hard poweroff. The controller also
aborts for memory pressure, commit/CPU/pagefile pressure, network or control-ACL
drift, or Hyper-V error events. Hard poweroff can lose guest writes; approval must
cover it. No guest installation is permitted in this probe.

Rollback targets only the journal-owned VM: turn it Off, verify Off, restore
the original 4 GiB maximum, then reconcile the journal only after full validation.
Failure to stop prevents restoration/reconciliation from being claimed. Unknown
ACL drift is never repaired automatically. Hyper-V previously reapplied a broader
root storage ACL during creation, so runtime ACL stability remains an explicit
unproved risk. Guard records are retained for reconciliation, not silently deleted.
These fallbacks are best effort if Hyper-V itself becomes unresponsive.

Offline tests exercise admission, aborts and ordered rollback with fake adapters.
They do not prove a live watchdog, live poweroff or real resource disposal.

## Separate security prerequisites

Windows 10 ESU/patch coverage remains unverified. It does not by itself demonstrate
a technical inability to cold-boot an offline trusted ISO; accepting residual host
risk for a bounded clean probe requires specific authorization. Hostile workload
homologation remains blocked. Independent gVisor producer authentication is also
pending and is not satisfied by this boot preparation.

References: [Hyper-V dynamic memory](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/dynamic-memory),
[host requirements](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/host-hardware-requirements),
[Stop-VM](https://learn.microsoft.com/en-us/powershell/module/hyper-v/stop-vm).
