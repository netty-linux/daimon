#Requires -Version 7.4
# Prepared bounded probe. Default is a refusal: no boot without exact action acknowledgement.
[CmdletBinding()]
param(
    [ValidateSet('Probe','Watchdog')][string]$Mode='Probe',
    [Parameter(Mandatory)][string]$Root,
    [Parameter(Mandatory)][string]$IsoPath,
    [Parameter(Mandatory)][guid]$OperationId,
    [Parameter(Mandatory)][guid]$VMId,
    [ValidateScript({$_ -in @(2,2.5,3,4)})][double]$MaximumGiB=2,
    [string]$Authorization,
    [Parameter(Mandatory)][string]$ReceiptPath,
    [string]$GuardDirectory
)
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
$profileText=$MaximumGiB.ToString([cultureinfo]::InvariantCulture)
$ack='FIRSTBOOT-DEADLINE-V2:'+ $OperationId.ToString()+':'+$VMId.ToString()+':MAX'+$profileText
if($Authorization -cne $ack){throw 'Specific boot/cap/poweroff authorization required; no action performed.'}
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-boot.psm1') -Force
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-deadline.psm1')
$base=Import-Module (Join-Path $PSScriptRoot 'harness2-c2-lab.psm1') -Force -PassThru
$policy=Get-C2BootBudget $MaximumGiB
$parameters=@{Root=$Root;IsoPath=$IsoPath;OperationId=$OperationId}
$script:ref=$null;$script:attempt=$null;$script:transitionPath=$null;$script:stopPath=$null
$script:deadline=$null;$script:timing=@{};$script:stopJob=$null;$script:timingPath=$null;$script:stopVm=$null
function Save-Timing {
    if($script:timingPath){Write-Private $script:timingPath @{actor=$Mode;timing=$script:timing;deadline=$script:deadline;utc=[datetime]::UtcNow.ToString('o')}}
}
function Write-Heartbeat([string]$Phase) {
    $stamp=Get-C2MonotonicStamp
    Write-Private (Join-Path $guard 'heartbeat.json') @{utc=$stamp.utc;monotonic_ticks=$stamp.ticks;phase=$Phase}
}
function Write-Private($Path,$Data){
    & $base {param($p,$d)
        Assert-C2NoLinks $p
        $tmp=$p+'.'+[guid]::NewGuid().ToString('N')+'.tmp'
        $f=New-C2File $tmp
        try{$b=[Text.UTF8Encoding]::new($false).GetBytes(($d|ConvertTo-Json -Depth 8 -Compress));$f.Write($b,0,$b.Length);$f.Flush($true)}finally{$f.Dispose()}
        if(Test-Path -LiteralPath $p){Assert-C2Acl $p;[IO.File]::Replace($tmp,$p,[System.Management.Automation.Language.NullString]::Value)}else{[IO.File]::Move($tmp,$p)}
    } $Path $Data
}
function Read-Private($Path){
    & $base {param($p) Assert-C2Acl $p;if((Get-Item -LiteralPath $p).Length -gt 65536){throw 'Bounded policy file exceeded.'};Get-Content -LiteralPath $p -Raw|ConvertFrom-Json -AsHashtable} $Path
}
function Get-OwnedVM {
    # Immutable reference captured from protected journal before boot. Never scan-delete or target by name alone.
    & $base {param($r)
        Assert-C2NoLinks $r.configuration
        $v=@(Get-VM -Id $r.vm_id -ErrorAction Stop)
        if($v.Count -ne 1 -or $v[0].Name -cne $r.vm_name -or $v[0].Generation -ne 2 -or
          (Get-C2Path $v[0].ConfigurationLocation) -ine $r.configuration -or $v[0].Notes -cne ('DAIMON-C2:'+$r.operation_id)){throw 'Exact VM ownership unavailable; no foreign stop.'}
        $v[0]
    } $script:ref
}
function Stop-OwnedVMCore {
    $adapter=@{Frequency=[Diagnostics.Stopwatch]::Frequency;Now={[Diagnostics.Stopwatch]::GetTimestamp()}
      State={$script:stopVm=Get-OwnedVM;$script:stopVm.State.ToString()};Send={
        # State has just authenticated this exact object. No extra provider query between the dispatch stamp and command.
        $script:timing.stop_job_reused=[bool]$script:stopJob
        if(-not $script:stopJob){$script:stopJob=Stop-VM -VM $script:stopVm -TurnOff -Force -AsJob -Confirm:$false -ErrorAction Stop}
        $script:timing.stop_job_id=[string]$script:stopJob.InstanceId
        $script:stopJob
      };CommandState={param($job) $job.State.ToString()};Sleep={param($ms) Start-Sleep -Milliseconds $ms}}
    try{$null=Invoke-C2TimedStop $script:timing $adapter 15}finally{Save-Timing}
    if(-not $script:timing.confirmed_off){throw 'Poweroff unconfirmed after bounded observation; no restore or reconciliation.'}
}
function Stop-OwnedVM {
    if(-not $script:timing.ContainsKey('stop_requested_ticks')){$script:timing.stop_requested_ticks=[Diagnostics.Stopwatch]::GetTimestamp()}
    if($script:transitionPath){
        # Persistent veto precedes lock acquisition: no later start may follow a watchdog stop.
        Write-Private $script:stopPath @{requested_at_utc=[DateTime]::UtcNow.ToString('o');vm_id=$VMId.ToString()}
        Invoke-C2TransitionGate $script:transitionPath {Stop-OwnedVMCore}
    }else{Stop-OwnedVMCore}
}
function Restore-OwnedMaximumCore {
    $v=Get-OwnedVM;if($v.State -ne 'Off'){throw 'Cannot restore memory while running.'}
    $m=Get-VMMemory -VM $v
    $startup=$policy.guest_startup_gib*1GB;$ceiling=$MaximumGiB*1GB
    if(-not $m.DynamicMemoryEnabled -or $m.Minimum -notin @(3GB,$startup) -or $m.Startup -notin @(3GB,$startup) -or $m.Maximum -notin @(4GB,$ceiling)){throw 'Unexpected memory drift; no arbitrary reset.'}
    if($m.Minimum -eq 3GB -and $m.Startup -eq 3GB -and $m.Maximum -eq 4GB){return}
    Set-VMMemory -VM $v -MinimumBytes 3GB -StartupBytes 3GB -MaximumBytes 4GB -ErrorAction Stop
    $m=Get-VMMemory -VM (Get-OwnedVM)
    if($m.Minimum -ne 3GB -or $m.Startup -ne 3GB -or $m.Maximum -ne 4GB){throw 'Original memory restoration unverified.'}
}
function Restore-OwnedMaximum {
    if($script:transitionPath){Invoke-C2TransitionGate $script:transitionPath {Restore-OwnedMaximumCore}}else{Restore-OwnedMaximumCore}
}
function Host-Sample($Previous){
    $s=Get-C2NativeSample $Previous
    $s.balancer_available_gib=(Get-C2CounterValue $script:balancePath)/1024
    $files=@(Get-CimInstance Win32_PageFileUsage -OperationTimeoutSec 5 -ErrorAction Stop)
    $s.pagefile_used_mib=if($files.Count){[double](($files|Measure-Object CurrentUsage -Sum).Sum)}else{0.0}
    $s
}
function Get-EventSummary([datetime]$Since){
    $events=@()
    foreach($log in 'Microsoft-Windows-Hyper-V-VMMS-Admin','Microsoft-Windows-Hyper-V-Worker-Admin'){
        try{
            $channel=Get-WinEvent -ListLog $log -ErrorAction Stop
            if(-not $channel.IsEnabled){throw 'Required event channel disabled.'}
            if($channel.RecordCount){foreach($e in @(Get-WinEvent -LogName $log -MaxEvents 64 -ErrorAction Stop)){
                if($e.TimeCreated.ToUniversalTime() -ge $Since -and $e.ToXml().IndexOf($VMId.ToString(),[StringComparison]::OrdinalIgnoreCase) -ge 0){
                    $events+=@{log=$log;id=$e.Id;level=$e.Level;record_id=$e.RecordId;utc=$e.TimeCreated.ToUniversalTime().ToString('o')}
                }
            }}
        }catch{throw 'Required Hyper-V event inspection failed.'}
    }
    $events
}
function Reconcile-Off {
    $adapter=@{Stop={Stop-OwnedVM};State={(Get-OwnedVM).State.ToString()};Restore={Restore-OwnedMaximum};Journal={
      & $base {param($p,$id)
        $c=Get-C2Contract $p.Root $p.IsoPath $p.OperationId
        Assert-C2Acl $c.root -Directory;Assert-C2Acl (Join-Path $c.root 'operation.lock')
        $j=Read-C2Journal $c;if($j.vm_id -cne $id){throw 'Journal identity changed.'}
        $null=Assert-C2VM $j -Complete
        Assert-C2MediaAcl $j 'iso';Assert-C2MediaAcl $j 'disk'
        if($j.state -notin 'configured','start-pending','running'){throw 'Unexpected journal state; no blind reconciliation.'}
        $j.state='configured';Write-C2Journal $j
      } $parameters $VMId.ToString()
    }}
    $null=Invoke-C2ProbeRollback $adapter -ReconcileJournal
}

$report=@{};$lock=$null;$watcher=$null;$started=$false;$baselineCaptured=$false;$receiptAllowed=$false
try {
    $c=& $base {param($p) Get-C2Contract $p.Root $p.IsoPath $p.OperationId} $parameters
    if($Mode -eq 'Watchdog'){
        $canonical=& $base {param($g,$root)
            $p=Get-C2Path $g
            if([IO.Path]::GetDirectoryName($p) -ine $root -or [IO.Path]::GetFileName($p) -notmatch '^firstboot-[a-f0-9]{32}$'){throw 'Unexpected guard directory.'}
            Assert-C2Acl $p -Directory;$p
        } $GuardDirectory $c.root
        if([IO.Path]::GetFullPath($ReceiptPath) -ine (Join-Path $canonical 'watchdog-receipt.json')){throw 'Unexpected watchdog receipt path.'}
        $receiptAllowed=$true
        $script:attempt=Read-Private (Join-Path $canonical 'policy.json')
        if($script:attempt.version -ne 2 -or $script:attempt.operation_id -cne $OperationId.ToString() -or $script:attempt.vm_id -cne $VMId.ToString() -or
          $script:attempt.maximum_gib -ne $MaximumGiB -or $script:attempt.original_maximum_gib -ne 4){throw 'Guard binding mismatch.'}
        $script:ref=$script:attempt.reference
        $j=& $base {param($c) Read-C2Journal $c} $c
        if($j.vm_id -cne $script:ref.vm_id -or $j.vm_name -cne $script:ref.vm_name -or $j.operation_id -cne $script:ref.operation_id -or
          $script:ref.configuration -ine [IO.Path]::Combine($j.config,$j.vm_name)){throw 'Guard ownership not established.'}
        $script:transitionPath=Join-Path $canonical 'transition.lock';$script:stopPath=Join-Path $canonical 'stop-requested.json'
        $script:timingPath=Join-Path $canonical 'watchdog-timing.json'
        & $base {param($p) Assert-C2Acl $p} $script:transitionPath
        Assert-C2AsyncStopCapability (Get-Command Stop-VM -ErrorAction Stop)
        # Compile/warm the native sensor before declaring readiness; do not overlap this peak with guest startup.
        $null=Get-C2NativeSample;[GC]::Collect()
        Write-Private (Join-Path $canonical 'ready.json') @{vm_id=$VMId.ToString();operation_id=$OperationId.ToString();process_id=$PID}
        $reason=Wait-C2WatchdogLease -ReadHeartbeat {Read-Private (Join-Path $canonical 'heartbeat.json')} -Sample {Get-C2NativeSample} -Policy $policy -Stop {
            $null=Invoke-C2ProbeRollback @{Stop={Stop-OwnedVM};State={(Get-OwnedVM).State.ToString()};Restore={Restore-OwnedMaximum}}
        } -ReadDeadline {
            $path=Join-Path $canonical 'boot-clock.json'
            if(Test-Path -LiteralPath $path){
                $record=Read-Private $path
                if($record.vm_id -cne $VMId.ToString() -or $record.operation_id -cne $OperationId.ToString() -or $record.deadline.limit_seconds -ne $policy.probe_seconds){throw 'Boot clock binding mismatch.'}
                if(-not $script:deadline){$script:deadline=New-C2Deadline $record.deadline.origin_ticks $record.deadline.frequency $policy.probe_seconds}
                $script:deadline
            }
        } -VerifyDone {
            if((Get-OwnedVM).State -ne 'Off'){return $false}
            if(-not $script:timing.ContainsKey('off_observed_ticks')){$script:timing.off_observed_ticks=[Diagnostics.Stopwatch]::GetTimestamp()}
            $script:timing.confirmed_off=$true;Save-Timing;$true
        }
        $temporal=if($script:deadline){Get-C2TemporalVerdict $script:deadline $script:timing}else{'NO_BOOT_CLOCK'}
        if($reason -ne 'controller-finished'){Write-Private (Join-Path $canonical 'watchdog-result.json') @{status='OFF_VERIFIED';reason=$reason;timing=$script:timing;temporal_verdict=$temporal;poweroff_command_issued=$script:timing.ContainsKey('stop_command_invoked_ticks')}}
        $report=@{status=if($temporal -in 'OFF_BY_DEADLINE','NO_BOOT_CLOCK'){'WATCHDOG_FINISHED'}else{'WATCHDOG_FAILED_OFF_VERIFIED'};reason=$reason;timing=$script:timing;temporal_verdict=$temporal;ownership_authority=$false}
    }else{
        # Receipt must be an explicit new operator file, outside the protected root.
        $receipt=& $base {param($p) $r=Get-C2Path $p;Assert-C2NoLinks $r;$r} $ReceiptPath
        if($receipt.StartsWith($c.root+'\',[StringComparison]::OrdinalIgnoreCase) -or (Test-Path -LiteralPath $receipt) -or -not (Test-Path -LiteralPath ([IO.Path]::GetDirectoryName($receipt)) -PathType Container)){throw 'New external receipt required.'}
        $ReceiptPath=$receipt;$receiptAllowed=$true
        & $base {param($c) Assert-C2Host 'Inspect' $c;Assert-C2Acl $c.root -Directory;Assert-C2Acl (Join-Path $c.root 'operation.lock')} $c
        Assert-C2AsyncStopCapability (Get-Command Stop-VM -ErrorAction Stop)
        $lock=[IO.FileStream]::new((Join-Path $c.root 'operation.lock'),'Open','ReadWrite','None')
        $j=& $base {param($c,$id)
            $j=Read-C2Journal $c
            if($j.vm_id -cne $id -or $j.state -ne 'configured'){throw 'Configured exact VM required.'}
            $null=Assert-C2VM $j -Complete;Assert-C2MediaAcl $j 'iso';Assert-C2MediaAcl $j 'disk';Assert-C2Iso $j.iso;$j
        } $c $VMId.ToString()
        $script:ref=@{vm_id=$j.vm_id;vm_name=$j.vm_name;operation_id=$j.operation_id;configuration=[IO.Path]::Combine($j.config,$j.vm_name)}
        $script:balancePath=Resolve-C2CounterPath 'Hyper-V Dynamic Memory Balancer' 'Available Memory For Balancing'
        $since=[DateTime]::UtcNow;$null=Get-EventSummary $since
        $null=Host-Sample $null;[GC]::Collect()
        $samples=[Collections.Generic.List[object]]::new();$previous=$null;$clock=[Diagnostics.Stopwatch]::StartNew()
        do{
            $s=Host-Sample $previous;$previous=$s.cpu_ticks;$s.Remove('cpu_ticks');$samples.Add($s);Start-Sleep -Seconds 2
        }while($clock.Elapsed.TotalSeconds -lt 180)
        $assessment=Get-C2BootAssessment $samples.ToArray() $clock.Elapsed.TotalSeconds @{vm_validated=$true;iso_verified=$true;stop_interface=$true;events_readable=$true;paging_io_rate=$false} $MaximumGiB
        if($assessment.status -ne 'GO_FOR_SEPARATE_AUTHORIZATION'){throw ('Fresh admission blocked: '+($assessment.reasons -join ','))}
        $baselineCaptured=$true
        $guard=Join-Path $c.root ('firstboot-'+[guid]::NewGuid().ToString('N'))
        & $base {param($g) New-C2Directory $g;Assert-C2Acl $g -Directory} $guard
        $script:timingPath=Join-Path $guard 'controller-timing.json'
        $script:transitionPath=Join-Path $guard 'transition.lock';$script:stopPath=Join-Path $guard 'stop-requested.json'
        & $base {param($p) $f=New-C2File $p;$f.Dispose();Assert-C2Acl $p} $script:transitionPath
        $script:attempt=@{version=2;operation_id=$OperationId.ToString();vm_id=$VMId.ToString();maximum_gib=$MaximumGiB;original_maximum_gib=4;reference=$script:ref;policy=$policy
          controller_pid=$PID;controller_started_utc=(Get-Process -Id $PID).StartTime.ToUniversalTime().ToString('o')}
        Write-Private (Join-Path $guard 'policy.json') $script:attempt
        Write-Heartbeat 'arming'
        $childArgs=@('-NoProfile','-NonInteractive','-File',('"'+$PSCommandPath+'"'),'-Mode','Watchdog','-Root',('"'+$Root+'"'),'-IsoPath',('"'+$IsoPath+'"'),'-OperationId',$OperationId.ToString(),'-VMId',$VMId.ToString(),'-MaximumGiB',$profileText,'-Authorization',('"'+$ack+'"'),'-ReceiptPath',('"'+(Join-Path $guard 'watchdog-receipt.json')+'"'),'-GuardDirectory',('"'+$guard+'"')) -join ' '
        $watcher=Start-Process -FilePath (Get-Command pwsh.exe).Source -ArgumentList $childArgs -WindowStyle Hidden -PassThru
        $ready=[Diagnostics.Stopwatch]::StartNew()
        while(-not (Test-Path -LiteralPath (Join-Path $guard 'ready.json'))){
            if($ready.Elapsed.TotalSeconds -gt 8 -or $watcher.HasExited){throw 'Independent watchdog not ready; no boot.'};Start-Sleep -Milliseconds 200
        }
        $proof=Read-Private (Join-Path $guard 'ready.json');if($proof.vm_id -cne $VMId.ToString() -or $proof.operation_id -cne $OperationId.ToString() -or $proof.process_id -ne $watcher.Id){throw 'Wrong watchdog binding.'}
        # Recheck live headroom after arming the extra host process, before any VM setting change.
        $pre=Host-Sample $null
        if($pre.available_gib -lt $policy.required_available_gib -or $pre.balancer_available_gib -lt $policy.required_balancer_gib -or $pre.commit_limit_gib-$pre.commit_gib -lt $policy.required_commit_headroom_gib -or $pre.commit_limit_gib -le 0 -or $pre.commit_gib/$pre.commit_limit_gib -gt $policy.max_commit_ratio){throw 'Live budget changed while arming; no boot.'}
        Invoke-C2TransitionGate $script:transitionPath {
            Assert-C2StartLease $watcher $script:stopPath
            $j.state='start-pending';& $base {param($j) Write-C2Journal $j} $j
            $v=Get-OwnedVM
            if($MaximumGiB -ne 4){Set-VMMemory -VM $v -MinimumBytes ($policy.guest_startup_gib*1GB) -StartupBytes ($policy.guest_startup_gib*1GB) -MaximumBytes ($MaximumGiB*1GB) -ErrorAction Stop}
            & $base {param($j,$max,$startup) Assert-C2Acl $j.root -Directory;Assert-C2Acl (Join-Path $j.root 'journal.json');$null=Assert-C2VM $j -Complete -ProbeMaximumMemoryGiB $max -ProbeStartupMemoryGiB $startup} $j $MaximumGiB $policy.guest_startup_gib
            Assert-C2StartLease $watcher $script:stopPath
            $startVm=Get-OwnedVM
            $stamp=Get-C2MonotonicStamp
            $script:deadline=New-C2Deadline $stamp.ticks $stamp.frequency $policy.probe_seconds
            $script:timing.start_requested_ticks=$stamp.ticks
            Write-Private (Join-Path $guard 'boot-clock.json') @{vm_id=$VMId.ToString();operation_id=$OperationId.ToString();deadline=$script:deadline;start_requested_utc=$stamp.utc}
            Save-Timing
            Assert-C2StartLease $watcher $script:stopPath
            Start-VM -VM $startVm -ErrorAction Stop
            $script:timing.start_command_returned_ticks=[Diagnostics.Stopwatch]::GetTimestamp()
            Save-Timing
        }
        $started=$true
        $j.state='running';& $base {param($j) Write-C2Journal $j} $j
        $previous=$null;$last=0.0;$low=0.0;$high=0.0;$run=[Collections.Generic.List[object]]::new();$why=''
        do{
            $deadlineState=Get-C2DeadlineState $script:deadline ([Diagnostics.Stopwatch]::GetTimestamp())
            if($deadlineState.request_stop){$why='shutdown-lead-reached';break}
            Write-Heartbeat 'running'
            $s=Host-Sample $previous;$previous=$s.cpu_ticks;$s.Remove('cpu_ticks')
            $deadlineState=Get-C2DeadlineState $script:deadline ([Diagnostics.Stopwatch]::GetTimestamp());$now=$deadlineState.elapsed_seconds;$dt=$now-$last;$last=$now
            $low=if($s.available_gib -lt $policy.low_available_gib){$low+$dt}else{0.0};$high=if($s.cpu_percent -and $s.cpu_percent -gt $policy.abort_cpu_percent){$high+$dt}else{0.0}
            if($deadlineState.request_stop){
                $fault=$s.available_gib -lt $policy.critical_available_gib -or $low -ge $policy.low_available_seconds -or $high -ge $policy.abort_cpu_seconds -or
                  $s.commit_limit_gib -le 0 -or $s.commit_gib/$s.commit_limit_gib -ge $policy.abort_commit_ratio -or $s.pagefile_used_mib-$samples[0].pagefile_used_mib -ge $policy.abort_pagefile_growth_mib
                $why=if($fault){'host-pressure-at-shutdown-lead'}else{'shutdown-lead-reached'}
                $s.elapsed_seconds=$now;$run.Add($s);break
            }
            $v=Get-OwnedVM;$aclOK=$true
            $watcher.Refresh();if($watcher.HasExited -or (Test-Path -LiteralPath $script:stopPath)){throw 'Independent watchdog stopped; aborting own VM.'}
            try{& $base {param($c) Assert-C2Acl $c.root -Directory;Assert-C2Acl (Join-Path $c.root 'journal.json');Assert-C2Acl (Join-Path $c.root 'operation.lock')} $c}catch{$aclOK=$false}
            $state=@{telemetry_ok=$true;ownership_ok=$true;control_acl_ok=$aclOK;low_memory_seconds=$low;high_cpu_seconds=$high;pagefile_growth_mib=$s.pagefile_used_mib-$samples[0].pagefile_used_mib}
            $view=@{network_adapters=@(Get-VMNetworkAdapter -VM $v).Count;assigned_gib=[double]$v.MemoryAssigned/1GB
                cpu_percent=[double]$v.CPUUsage;memory_demand_gib=[double]$v.MemoryDemand/1GB;memory_status=[string]$v.MemoryStatus}
            $why=Get-C2ProbeAbortReason $s $state $now $policy $view
            $s.vm=$view;$s.elapsed_seconds=$now;$run.Add($s)
            $events=@(Get-EventSummary $since);if(@($events|Where-Object{$_.level -in 1,2}).Count){$why='hyper-v-error-event'}
            $deadlineState=Get-C2DeadlineState $script:deadline ([Diagnostics.Stopwatch]::GetTimestamp())
            if($deadlineState.request_stop -and -not $why){$why='shutdown-lead-reached'}
            if($why -or $v.State -ne 'Running'){break};Start-Sleep -Milliseconds ([int](1000*[math]::Min(2,$deadlineState.seconds_until_stop)))
        }while($true)
        Reconcile-Off
        $temporal=Get-C2TemporalVerdict $script:deadline $script:timing
        Write-Private (Join-Path $guard 'deadline-result.json') @{temporal_verdict=$temporal;deadline=$script:deadline;timing=$script:timing}
        Write-Heartbeat 'done'
        $report=@{status=if($temporal -eq 'OFF_BY_DEADLINE' -and $why -eq 'shutdown-lead-reached'){'PROBE_FINISHED_OFF_WITHIN_DEADLINE'}else{'PROBE_FAILED_OFF_VERIFIED'}
            temporal_verdict=$temporal;deadline=$script:deadline;timing=$script:timing;stop_reason=$why;samples=$run.ToArray();baseline=$assessment;events=@(Get-EventSummary $since)
            journal_reconciled=$true;maximum_restored_gib=4;guest_installation=$false;ownership_authority=$false;guard_directory=$guard}
    }
}catch{
    $reason=$_.Exception.Message;$rollback='NOT_NEEDED'
    if($Mode -eq 'Watchdog' -and $script:ref){try{$rollback=Invoke-C2ProbeRollback @{Stop={Stop-OwnedVM};State={(Get-OwnedVM).State.ToString()};Restore={Restore-OwnedMaximum}}}catch{$rollback='UNRESOLVED'}}
    if($Mode -eq 'Probe' -and $script:ref -and $baselineCaptured){
        try{Reconcile-Off;$rollback='OFF_MAX4_CONFIGURED'}catch{$rollback='OFF_OR_CONTROL_RECONCILIATION_UNRESOLVED'}
        if($script:attempt -and (Get-Variable guard -ErrorAction SilentlyContinue)){
            try{Write-Heartbeat $(if($rollback -eq 'OFF_MAX4_CONFIGURED'){'done'}else{'unresolved'})}catch{}
        }
    }
    $report=@{status='BLOCKED_OR_ABORTED';reason=$reason;rollback=$rollback;boot_started=$started;ownership_authority=$false;timing=$script:timing;deadline=$script:deadline
      temporal_verdict=if($script:deadline){Get-C2TemporalVerdict $script:deadline $script:timing}else{'NO_BOOT_CLOCK'}}
    if($script:deadline -and (Get-Variable guard -ErrorAction SilentlyContinue)){
        try{Write-Private (Join-Path $guard 'deadline-result.json') @{status='FAILED';temporal_verdict=$report.temporal_verdict;timing=$script:timing;deadline=$script:deadline}}catch{}
    }
}finally{if($lock){$lock.Dispose()}}
$report.observed_at_utc=[DateTime]::UtcNow.ToString('o');$report.security_gates='all-18-unverified'
if(-not $receiptAllowed){$report|ConvertTo-Json -Depth 5;exit 2}
if($Mode -eq 'Watchdog'){try{Write-Private $ReceiptPath $report}catch{}}else{
    $f=[IO.FileStream]::new($ReceiptPath,'CreateNew','Write','Read')
    try{$bytes=[Text.UTF8Encoding]::new($false).GetBytes(($report|ConvertTo-Json -Depth 10));$f.Write($bytes,0,$bytes.Length);$f.Flush($true)}finally{$f.Dispose()}
}
if($report.status -in 'BLOCKED_OR_ABORTED','PROBE_FAILED_OFF_VERIFIED','WATCHDOG_FAILED_OFF_VERIFIED'){exit 2}
