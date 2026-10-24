# Pure classification. Never imports the lab module or queries OS/VM state.
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-helper.psm1')
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-deadline.psm1')
function Get-C2OfflineResult($Suite,[int]$ExitCode) {
    if(-not $Suite){return @{status='BLOCKED';reason='NO_SUITE_REPORT';correction='Review the local launcher log; no policy bypass or automatic retry.'}}
    if($Suite -isnot [Collections.IDictionary]){return @{status='FAIL';reason='INVALID_REPORT_SHAPE';correction='Preserve and review the invalid local report; do not infer success.'}}
    foreach($key in 'status','launcher_isolation','vm_access','independence','tests'){
        if(-not $Suite.Contains($key)){return @{status='FAIL';reason='INCOMPLETE_REPORT';correction='Correct the missing report fields; both positive proofs are required.'}}
    }
    if(-not $Suite.launcher_isolation -or -not $Suite.launcher_isolation.Contains('verified')){return @{status='FAIL';reason='MISSING_ISOLATION_PROOF';correction='Require actual launcher isolation facts; do not infer a detached launch.'}}
    if($Suite.status -eq 'BLOCKED' -or -not $Suite.launcher_isolation.verified){return @{status='BLOCKED';reason='HELPER_JOB_OR_CONSOLE_ISOLATION_UNPROVEN';correction='Run the same entry from outside Work in a separate console. If it remains blocked, stop and review the report; do not weaken isolation.'}}
    if($ExitCode -ne 0 -or $Suite.status -ne 'PASS'){return @{status='FAIL';reason='SUITE_FAILED';correction='Inspect the failed case and its local stderr; correct that cause only, without launching a VM.'}}
    if($Suite.vm_access -ne $false -or $Suite.independence -ne 'PROVEN_FOR_OWN_OS_PROCESSES_NO_SHARED_JOB_OR_CONSOLE' -or $Suite.tests.Count -ne 2){return @{status='FAIL';reason='INCOMPLETE_OR_WRONG_SCOPE';correction='Both named positive scenarios must pass; a safe refusal is not independence proof.'}}
    foreach($name in 'stall','supervisor-exit'){
        $case=@($Suite.tests|Where-Object {$_.name -ceq $name})
        if($case.Count -ne 1 -or -not $case[0].passed -or $case[0].scope -ne 'OWN_PROCESS_NOT_VM' -or -not $case[0].supervisor.verified){return @{status='FAIL';reason='MISSING_POSITIVE_SCENARIO';correction='Require exactly one verified positive result for each scenario.'}}
    }
    $stall=@($Suite.tests|Where-Object {$_.name -ceq 'stall'})[0]
    if((Get-C2EmergencyProofVerdict $stall.fault $stall.watchdog $stall.deadline) -ne 'WATCHDOG_POWEROFF_PROVEN'){return @{status='FAIL';reason='STALL_PROOF_REJECTED';correction='Correct the heartbeat/timing/binding failure recorded by the stalled-controller case.'}}
    $exit=@($Suite.tests|Where-Object {$_.name -ceq 'supervisor-exit'})[0];$w=$exit.watchdog
    if($exit.verdict -ne 'WATCHDOG_SURVIVED_OWN_CONTROLLER_AND_SUPERVISOR_EXIT' -or -not $w.isolation.verified -or $w.reason -ne 'controller-heartbeat-lost' -or -not $w.poweroff_command_issued -or $w.timing.state_before_stop -ne 'Running' -or (Get-C2TemporalVerdict $exit.deadline $w.timing) -ne 'OFF_BY_DEADLINE'){
        return @{status='FAIL';reason='POST_EXIT_OPERATION_NOT_PROVEN';correction='Require watchdog survival and a real own-payload stop after both test helpers exit.'}
    }
    @{status='PASS';reason='BOTH_POSITIVE_OFFLINE_SCENARIOS_PROVEN';correction='None for this offline proof. VM trial still requires fresh preflight and separate human approval.'}
}
Export-ModuleMember -Function Get-C2OfflineResult
