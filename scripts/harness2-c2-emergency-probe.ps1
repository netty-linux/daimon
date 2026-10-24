#Requires -Version 7.4
# Future explicit emergency trial supervisor. Never auto-elevates or retries; no VM mutations itself.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Root,[Parameter(Mandatory)][string]$IsoPath,
 [Parameter(Mandatory)][guid]$OperationId,[Parameter(Mandatory)][guid]$VMId,
 [Parameter(Mandatory)][string]$ReceiptPath,[string]$Authorization)
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
$ack='C2-WATCHDOG-STALL30-V1:'+$OperationId.ToString()+':'+$VMId.ToString()+':MAX2'
if($Authorization -cne $ack){throw 'Separate human emergency-trial approval required; no action.'}
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-helper.psm1')
$isolation=Assert-C2DedicatedHelper $PSCommandPath
$base=Import-Module (Join-Path $PSScriptRoot 'harness2-c2-lab.psm1') -PassThru
$c=& $base {param($r,$i,$o) Get-C2Contract $r $i $o} $Root $IsoPath $OperationId
$ReceiptPath=& $base {param($p) $r=Get-C2Path $p;Assert-C2NoLinks $r;$r} $ReceiptPath
$controllerReceipt=$ReceiptPath+'.controller.json'
if((Test-Path -LiteralPath $ReceiptPath) -or (Test-Path -LiteralPath $controllerReceipt) -or $ReceiptPath.StartsWith($c.root+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'New external report paths required.'}
& $base {param($c) Assert-C2Host 'Inspect' $c} $c
$child=$null;$report=@{status='BLOCKED';gates='all-18-unverified';supervisor_isolation=$isolation;authority='REPORTS_ARE_NOT_AUTHORIZATION'}
try{
    $arguments=@('-NoProfile','-NonInteractive','-File',('"'+(Join-Path $PSScriptRoot 'harness2-c2-firstboot.ps1')+'"'),'-Mode','Probe','-Root',('"'+$Root+'"'),'-IsoPath',('"'+$IsoPath+'"'),'-OperationId',$OperationId.ToString(),'-VMId',$VMId.ToString(),'-MaximumGiB','2','-FaultInjection','HeartbeatStall30','-Authorization',('"'+$ack+'"'),'-ReceiptPath',('"'+$controllerReceipt+'"')) -join ' '
    $child=Start-Process -FilePath (Join-Path $PSHOME 'pwsh.exe') -ArgumentList $arguments -WindowStyle Hidden -PassThru
    $report.controller_pid=$child.Id;$report.controller_started_utc=$child.StartTime.ToUniversalTime().ToString('o')
    if(-not $child.WaitForExit(360000)){throw 'Controller outcome unconfirmed; no retry or automatic supervisor VM action.'}
    if(-not (Test-Path -LiteralPath $controllerReceipt)){throw 'Missing controller receipt; do not infer Off.'}
    $receipt=Get-Content -LiteralPath $controllerReceipt -Raw|ConvertFrom-Json -AsHashtable;$report.controller_receipt=$receipt
    if($receipt.status -ne 'EMERGENCY_WATCHDOG_PROOF_PASS'){throw 'Controller did not prove emergency route; no further stages.'}
    $guard=& $base {param($g,$c)
        $g=Get-C2Path $g
        if([IO.Path]::GetDirectoryName($g) -ine $c.root -or [IO.Path]::GetFileName($g) -notmatch '^firstboot-[a-f0-9]{32}$'){throw 'Foreign guard path.'}
        Assert-C2Acl $g -Directory;$g
    } $receipt.guard_directory $c
    $records=@{}
    foreach($name in 'policy.json','fault-intent.json','fault-returned.json','watchdog-result.json','boot-clock.json'){
        $path=Join-Path $guard $name
        & $base {param($p) Assert-C2Acl $p;if((Get-Item -LiteralPath $p).Length -gt 65536){throw 'Oversized evidence.'}} $path
        $records[$name]=Get-Content -LiteralPath $path -Raw|ConvertFrom-Json -AsHashtable
    }
    $p=$records['policy.json']
    if($p.version -ne 3 -or $p.fault_injection -ne 'HeartbeatStall30' -or $p.vm_id -cne $VMId.ToString() -or $p.operation_id -cne $OperationId.ToString() -or $p.controller_pid -ne $child.Id -or ([datetime]$p.controller_started_utc).ToUniversalTime().Ticks -ne $child.StartTime.ToUniversalTime().Ticks){throw 'Protected controller binding failed.'}
    $verdict=Get-C2EmergencyProofVerdict $records['fault-intent.json'] $records['watchdog-result.json'] $records['boot-clock.json'].deadline
    if($verdict -ne 'WATCHDOG_POWEROFF_PROVEN'){throw ('Protected emergency proof failed: '+$verdict)}
    $report.final_state=& $base {param($c,$id)
        Assert-C2Acl $c.root -Directory;Assert-C2Acl (Join-Path $c.root 'operation.lock')
        $lock=[IO.FileStream]::new((Join-Path $c.root 'operation.lock'),'Open','Read','None')
        try{$j=Read-C2Journal $c;if($j.vm_id -cne $id -or $j.state -ne 'configured'){throw 'Configured journal not proven.'};$v=Assert-C2VM $j -Complete;Assert-C2MediaAcl $j 'iso';Assert-C2MediaAcl $j 'disk';@{vm_id=$j.vm_id;state=$v.State.ToString();memory_gib=@(3,3,4);journal=$j.state;control='PRIVATE_VALIDATED'}}finally{$lock.Dispose()}
    } $c $VMId.ToString()
    $report.protected_records=$records;$report.emergency_verdict=$verdict;$report.status='EMERGENCY_TRIAL_PASS'
}catch{$report.reason=$_.Exception.Message;$report.status='BLOCKED_OR_FAILED'}
finally{
    $report.utc=[datetime]::UtcNow.ToString('o')
    $f=[IO.FileStream]::new($ReceiptPath,'CreateNew','Write','Read')
    try{$bytes=[Text.UTF8Encoding]::new($false).GetBytes(($report|ConvertTo-Json -Depth 20));$f.Write($bytes,0,$bytes.Length);$f.Flush($true)}finally{$f.Dispose()}
    # Never kill/restart an unconfirmed controller or watchdog here. They own the recovery leases.
    if($child){$child.Dispose()}
}
if($report.status -ne 'EMERGENCY_TRIAL_PASS'){exit 2}
