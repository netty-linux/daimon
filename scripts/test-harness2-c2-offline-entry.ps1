# Pure report tests and a private, own-user scratch directory test; no process/VM diagnostics.
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-offline-result.psm1')
$count=0
function Test($name,[scriptblock]$body){& $body;$script:count++;"PASS $name"}
function GoodSuite {
    $date='2026-10-24T00:00:00Z';$d=@{origin_ticks=1000;frequency=1000;limit_seconds=90}
    $fault=@{method='HeartbeatStall30';controller_isolation=@{verified=$true;pid=10};watchdog_pid=20;watchdog_started_utc=$date;vm_id='own';operation_id='op';last_heartbeat_ticks=4000}
    $watch=@{reason='controller-heartbeat-lost';poweroff_command_issued=$true;isolation=@{verified=$true;pid=20;started_utc=$date};vm_id='own';operation_id='op';temporal_verdict='OFF_BY_DEADLINE'
      timing=@{state_before_stop='Running';confirmed_off=$true;stop_requested_ticks=15000;stop_command_invoked_ticks=15100;stop_command_returned_ticks=15200;off_observed_ticks=16000}}
    $cleanup=@('Payload','Controller','Watcher','Supervisor'|ForEach-Object {@{identity=@{role=$_};status='EXITED_PROVEN';registration=@{metadata='MATCH';handle_retained=$true}}})
    @{status='PASS';launcher_isolation=@{verified=$true};vm_access=$false;independence='PROVEN_FOR_OWN_OS_PROCESSES_NO_SHARED_JOB_OR_CONSOLE';tests=@(
      @{name='stall';passed=$true;scope='OWN_PROCESS_NOT_VM';supervisor=@{verified=$true};fault=$fault;watchdog=$watch;deadline=$d;cleanup_complete=$true;cleanup=$cleanup;external_processes_terminated=$false},
      @{name='supervisor-exit';passed=$true;scope='OWN_PROCESS_NOT_VM';supervisor=@{verified=$true};verdict='WATCHDOG_SURVIVED_OWN_CONTROLLER_AND_SUPERVISOR_EXIT';watchdog=$watch;deadline=$d;cleanup_complete=$true;cleanup=$cleanup;external_processes_terminated=$false})}
}
foreach($case in 'missing-cleanup','incomplete-cleanup','divergent-cleanup','unknown-cleanup','duplicate-cleanup','cleanup-command-error','external-effect'){
    Test "cleanup cannot produce false PASS for $case" {
        $s=GoodSuite;$c=$s.tests[0]
        switch($case){
            'missing-cleanup'{$c.Remove('cleanup_complete')};'incomplete-cleanup'{$c.cleanup=@($c.cleanup[0])}
            'divergent-cleanup'{$c.cleanup[0].status='LIVE_DIVERGENT'};'unknown-cleanup'{$c.cleanup[0].status='INDETERMINATE'}
            'duplicate-cleanup'{$c.cleanup[0].identity.role='Watcher'};'cleanup-command-error'{$c.cleanup[0].error='denied'};'external-effect'{$c.external_processes_terminated=$true}
        }
        if((Get-C2OfflineResult $s 0).status -eq 'PASS'){throw 'unsafe cleanup accepted'}
    }
}
Test 'both positive proofs are necessary and sufficient for offline PASS'{if((Get-C2OfflineResult (GoodSuite) 0).status -ne 'PASS'){throw 'healthy reports refused'}}
foreach($case in 'exit-failed','suite-failed','one-only','duplicate','missing-pass','wrong-scope','supervisor-not-isolated','watcher-no-command','target-already-off','late-off','wrong-exit-verdict','vm-access'){
    Test "no false offline PASS for $case" {
        $s=GoodSuite;$code=0
        switch($case){
          'exit-failed'{$code=1};'suite-failed'{$s.status='FAIL'};'one-only'{$s.tests=@($s.tests[0])};'duplicate'{$s.tests[1].name='stall'}
          'missing-pass'{$s.tests[1].passed=$false};'wrong-scope'{$s.tests[0].scope='VM'};'supervisor-not-isolated'{$s.tests[0].supervisor.verified=$false}
          'watcher-no-command'{$s.tests[0].watchdog.poweroff_command_issued=$false};'target-already-off'{$s.tests[0].watchdog.timing.state_before_stop='Off'}
          'late-off'{$s.tests[0].watchdog.timing.off_observed_ticks=92000};'wrong-exit-verdict'{$s.tests[1].verdict='unknown'};'vm-access'{$s.vm_access=$true}
        }
        if((Get-C2OfflineResult $s $code).status -eq 'PASS'){throw 'unsafe report accepted'}
    }
}
Test 'safe environmental refusal is BLOCKED even if process returns zero'{
    $s=GoodSuite;$s.status='BLOCKED';if((Get-C2OfflineResult $s 0).status -ne 'BLOCKED'){throw 'refusal promoted to PASS'}
}
Test 'missing suite is BLOCKED'{if((Get-C2OfflineResult $null 1).status -ne 'BLOCKED'){throw 'missing report accepted'}}
Test 'malformed report cannot become PASS or crash classification'{if((Get-C2OfflineResult @{status='PASS'} 0).status -ne 'FAIL'){throw 'missing shape accepted'}}
Test 'missing isolation facts cannot become PASS'{
    $s=GoodSuite;$s.launcher_isolation=@{};if((Get-C2OfflineResult $s 0).status -ne 'FAIL'){throw 'missing isolation accepted'}
}
Test 'double-click entry contains no automatic elevation or policy override'{
    $cmd=Get-Content -LiteralPath (Join-Path $PSScriptRoot 'harness2-c2-offline-start.cmd') -Raw
    if($cmd -match '(?i)-Verb\s+RunAs|-ExecutionPolicy|Set-ExecutionPolicy|EncodedCommand|Invoke-Expression|powershell\s+-Command'){throw 'unsafe bootstrap option'}
    if($cmd -notmatch '(?i)start\s+"[^"]*"\s+/wait' -or $cmd -notmatch 'C2-offline-attempt.lock'){throw 'missing separate console or one-attempt marker'}
}
Test 'offline evidence directory is current-user owned and does not require lab/admin ACL'{
    $tokens=$null;$errors=$null
    $ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'test-harness2-c2-emergency.ps1'),[ref]$tokens,[ref]$errors)
    if($errors.Count){throw 'suite parse error'}
    $definition=$ast.Find({param($a)$a -is [Management.Automation.Language.FunctionDefinitionAst] -and $a.Name -eq 'New-TestDirectory'},$true)
    . ([scriptblock]::Create($definition.Extent.Text))
    $path=Join-Path ([IO.Path]::GetTempPath()) ('c2-user-acl-test-'+[guid]::NewGuid().ToString('N'))
    New-TestDirectory $path
    $acl=Get-Acl -LiteralPath $path;$sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    if($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $sid -or -not $acl.AreAccessRulesProtected){throw 'wrong own-user directory ACL'}
    $rules=@($acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]))
    if($rules.Count -ne 2 -or @($rules|Where-Object {$_.IdentityReference.Value -notin @($sid,'S-1-5-18')}).Count){throw 'non-user evidence trustees'}
    # Keep the unique empty evidence directory; no recursive deletion or system changes.
}
"PASS $count offline-entry tests; VM and app state NOT ACCESSED."
