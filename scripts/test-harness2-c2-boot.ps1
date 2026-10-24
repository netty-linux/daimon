#Requires -Version 7.4
# Offline budget, abort and watchdog decisions. No VM state changes.
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-boot.psm1') -Force
$count=0
function Check($c,$why){if(-not $c){throw $why}}
function Test($name,[scriptblock]$body){& $body;$script:count++;"PASS $name"}
function Samples([double]$available=9){@(1..60|ForEach-Object{@{available_gib=$available;commit_gib=14;commit_limit_gib=34;balancer_available_gib=7;pagefile_used_mib=3000;cpu_percent=15}})}
$caps=@{vm_validated=$true;iso_verified=$true;stop_interface=$true;events_readable=$true;paging_io_rate=$false}
Test 'full ceiling includes 4 GiB guest, host reserve and explicit overhead'{
    $p=Get-C2BootBudget 4;Check ($p.required_available_gib -eq 8.5 -and $p.host_reserve_gib -eq 4) 'budget wrong'
}
Test 'temporary cap reduces guest ceiling without reducing host reserve'{
    $p=Get-C2BootBudget 3;Check ($p.required_available_gib -eq 7.5 -and $p.host_reserve_gib -eq 4 -and $p.guest_startup_gib -eq 3) 'reserve weakened'
}
Test 'current ceiling blocked at 7.7 but capped proposal can be presented'{
    $s=Samples 7.7;$a=Get-C2BootAssessment $s 180 $caps 4;$b=Get-C2BootAssessment $s 180 $caps 3
    Check ($a.status -eq 'BLOCKED' -and $b.status -eq 'GO_FOR_SEPARATE_AUTHORIZATION' -and $b.configuration_change_required) 'unsafe admission'
}
Test 'temporary cap still blocks below its full physical budget'{
    $a=Get-C2BootAssessment (Samples 7.49) 180 $caps 3;Check ($a.reasons -contains 'physical-budget-insufficient') 'floor ignored'
}
Test 'short or sparse baseline cannot authorize a proposal'{
    $a=Get-C2BootAssessment (Samples) 30 $caps 3;Check ($a.reasons -contains 'baseline-incomplete') 'short sample accepted'
}
foreach($cap in 'vm_validated','iso_verified','stop_interface','events_readable'){
    Test "missing $cap blocks readiness" {$c=$caps.Clone();$c[$cap]=$false;$a=Get-C2BootAssessment (Samples) 180 $c 3;Check ($a.status -eq 'BLOCKED') 'missing prerequisite ignored'}
}
Test 'missing telemetry is not converted to zero pressure'{
    $s=Samples;$s[5].balancer_available_gib=$null;$a=Get-C2BootAssessment $s 180 $caps 3;Check ($a.reasons -contains 'required-telemetry-unavailable') 'missing data accepted'
}
Test 'commit and pagefile growth independently block admission'{
    $s=Samples;$s[15].commit_gib=32;$s[20].pagefile_used_mib=3400;$a=Get-C2BootAssessment $s 180 $caps 3
    Check ($a.reasons -contains 'host-commit-pressure' -and $a.reasons -contains 'baseline-pagefile-growth') 'pressure ignored'
}
Test 'missing paging I/O remains explicit even with conservative reserve'{
    $a=Get-C2BootAssessment (Samples) 180 $caps 3;Check (-not $a.paging_io_rate_available) 'missing I/O invented'
}
$state=@{telemetry_ok=$true;ownership_ok=$true;control_acl_ok=$true;low_memory_seconds=0;high_cpu_seconds=0;pagefile_growth_mib=0}
$sample=@{available_gib=5;commit_gib=14;commit_limit_gib=34};$vm=@{network_adapters=0;assigned_gib=3};$policy=Get-C2BootBudget 3
foreach($reason in 'telemetry','ownership','acl','memory','commit','low','cpu','pagefile','nic','ceiling','deadline'){
    Test "abort reason $reason is bounded to the own-VM stop decision" {
        $s=$state.Clone();$m=$sample.Clone();$v=$vm.Clone();$elapsed=5
        switch($reason){'telemetry'{$s.telemetry_ok=$false};'ownership'{$s.ownership_ok=$false};'acl'{$s.control_acl_ok=$false};'memory'{$m.available_gib=1.9};'commit'{$m.commit_gib=33};'low'{$s.low_memory_seconds=5};'cpu'{$s.high_cpu_seconds=10};'pagefile'{$s.pagefile_growth_mib=256};'nic'{$v.network_adapters=1};'ceiling'{$v.assigned_gib=4};'deadline'{$elapsed=180}}
        Check ([bool](Get-C2ProbeAbortReason $m $s $elapsed $policy $v)) 'abort was missed'
    }
}
Test 'healthy sample does not request shutdown'{Check ($null -eq (Get-C2ProbeAbortReason $sample $state 5 $policy $vm)) 'spurious shutdown'}
Test 'rollback stops and verifies Off before restoring ceiling and journal'{
    $trace=[Collections.Generic.List[string]]::new()
    $a=@{Stop={$trace.Add('stop')}.GetNewClosure();State={'Off'};Restore={$trace.Add('restore4')}.GetNewClosure();Journal={$trace.Add('configured')}.GetNewClosure()}
    $r=Invoke-C2ProbeRollback $a -ReconcileJournal
    Check ($r -eq 'OFF_MAX4_CONFIGURED' -and ($trace -join ',') -eq 'stop,restore4,configured') 'rollback order wrong'
}
Test 'unverified poweroff cannot rewrite ceiling or configured journal'{
    $trace=[Collections.Generic.List[string]]::new();$a=@{Stop={};State={'Running'};Restore={$trace.Add('restore')}.GetNewClosure();Journal={$trace.Add('journal')}.GetNewClosure()}
    $failed=$false;try{Invoke-C2ProbeRollback $a -ReconcileJournal}catch{$failed=$true}
    Check ($failed -and $trace.Count -eq 0) 'unsafe rollback continued'
}
Test 'failed restoration leaves journal unresolved'{
    $trace=[Collections.Generic.List[string]]::new();$a=@{Stop={};State={'Off'};Restore={throw 'restore refused'};Journal={$trace.Add('journal')}.GetNewClosure()}
    $failed=$false;try{Invoke-C2ProbeRollback $a -ReconcileJournal}catch{$failed=$true}
    Check ($failed -and $trace.Count -eq 0) 'journal falsely reconciled'
}
Test 'watchdog restores own VM but does not race controller journal writer'{
    $a=@{Stop={};State={'Off'};Restore={}}
    Check ((Invoke-C2ProbeRollback $a) -eq 'OFF_MAX4_JOURNAL_PENDING') 'watchdog claimed reconciliation'
}
Test 'probe CLI refuses missing acknowledgement before sensor, filesystem or Hyper-V actions'{
    $failed=$false
    try{& (Join-Path $PSScriptRoot 'harness2-c2-firstboot.ps1') -Root 'D:\c2-never-created' -IsoPath 'D:\absent.iso' -OperationId 'ace3eaf4-4e8d-45d0-ae5e-f8029c326a16' -VMId '582f9057-03bf-4b8e-bfb3-a349e11837f4' -ReceiptPath 'D:\c2-never-written.json'}catch{if($_.Exception.Message -match 'Specific boot/cap/poweroff authorization'){$failed=$true}else{throw}}
    Check $failed 'missing acknowledgement accepted'
}
"PASS $count first-boot policy tests; no real boot or poweroff."
