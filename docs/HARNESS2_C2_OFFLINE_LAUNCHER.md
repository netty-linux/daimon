# One local offline entry — no elevation

The previous generated `C2-run-offline-helper-test-outside-Work.ps1` requested
`RunAs` and depended indirectly on administrator-only evidence directories.
That artifact is retired. Its replacement is the single double-click entry
**INICIAR-C2-TESTE-OFFLINE.cmd**, built from `scripts/harness2-c2-offline-start.cmd`.
It uses the already-present PowerShell runtime and reviewed repository sources.
No install, UAC request, ExecutionPolicy override, Job breakaway or machine setting
change is performed. Existing policy can block script execution; that is reported
as BLOCKED instead of bypassed.

## User execution

1. Open the saved outputs folder in Explorer outside Work/Codex and its terminal.
2. Double-click **INICIAR-C2-TESTE-OFFLINE.cmd** once, normally (not Run as administrator).
3. Allow roughly one minute for the two positive own-process scenarios. Read the
   final console status and the adjacent `C2-offline-result.json`.

The entry opens a separate PowerShell console; it does not share its CMD console.
Runtime Job and console membership are still verified. External launch is a
candidate context, never assumed to be detached. If Job/shared-console remains,
the proof stays BLOCKED. The launcher does not try another method.

The first invocation creates a local attempt marker and never removes it or retries.
An existing result is displayed instead of rerunning. An incomplete prior attempt
is reported blocked. Do not delete markers to start an unapproved diagnostic loop.

Adjacent outputs:

- `C2-offline-result.json`: normalized PASS / FAIL / BLOCKED, both case records,
  reviewed source hashes, reason and minimum correction.
- `C2-offline-status.txt`: short human-readable status.
- `C2-offline-suite.json`: raw suite evidence.
- `C2-offline-run.log`: captured output and error details.
- `C2-offline-attempt.lock/`: the one-attempt marker, not a machine/global lock.

If policy/runtime prevents the PowerShell entry from starting at all, CMD writes
a minimal BLOCKED result and engine exit code. It does not change policy or elevate.
The suite now creates private **current-user + SYSTEM** scratch directories and
does not import the lab module. Production VM journal ACLs are unchanged. No admin
membership is needed to create the new test evidence.

## What PASS requires

PASS is not merely exit code zero or an environment-refusal test passing. The
result validator requires exactly two named positive scenarios:

1. `stall`: the controller pauses its own heartbeat thread for the full 30 s;
   the separate watchdog remains operational and terminates its own bound dummy
   payload before the controller resumes, with valid command/Off timing.
2. `supervisor-exit`: the own test controller and its supervisor exit; the watchdog
   survives and still stops that exact dummy payload and records its evidence.

Each helper must be outside a Job and without console peers, with fresh PID/start
bindings. The validator checks scope, isolation, heartbeat cause, actual own-target
command, Running-to-Off evidence and deadlines. One scenario, duplicates, missing
fields, already-Off, late Off, no command or failed helper never produce PASS.

The suite closes only newly-created test helpers. Cleanup/stop bindings require
the expected PowerShell executable, creation time, creator PID, exact test entry
and unique case path. It never accepts an arbitrary external PID from the launcher.
The watcher keeps the exact Process object and avoids closure lookup of script-local
functions during asynchronous observation. Other applications are not targeted.

This is offline OS-process evidence, not proof of Hyper-V emergency poweroff. A
PASS prepares the next separate approval request only after a fresh real-VM
preflight and existing >=6 GiB admission criteria; no boot is authorized or launched
by this file or its result. Historical 91.1312018 s FAIL remains permanent, and all
18 gates stay `unverified`.

## Minimum correction by result

| Result/reason | Next bounded action |
| --- | --- |
| BLOCKED: Job/console | Verify the entry was started manually outside Work. If yes, preserve the report; review that exact containment issue without weakening checks. |
| BLOCKED: runtime/policy | Review the local error/runtime availability or source hashes. Do not bypass policy or install/elevate automatically. |
| FAIL: scenario/binding/timing | Inspect that case's stderr/timing and correct the specific implementation defect. No VM trial or automatic rerun. |
| PASS | Review both scenario records, then prepare a new explicit real-trial authorization with a fresh preflight. |

Source integrity uses SHA-256 over UTF-8 text with BOM removed and CRLF normalized
to LF, so Git checkout line endings do not invalidate the reviewed manifest.
It detects source drift; it is not a producer signature or an authority grant.
The launcher does not call the real emergency VM supervisor at all.
