# C2: bounded first boot preparation

No boot or installation is authorized by this document, an acknowledgement string,
or a passing offline test. All 18 isolation gates remain `unverified`.

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

## Admission policy

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
