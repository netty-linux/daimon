#Requires -Version 7.4
# Real OS processes, real heartbeat files, real native memory samples. Never imports Hyper-V.
[CmdletBinding()]
param([ValidateSet('Run','Payload','Watch','GateHolder','Producer')][string]$Mode='Run',[string]$CaseDirectory)
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-boot.psm1') -Force
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-deadline.psm1')
if($Mode -eq 'Payload'){Start-Sleep -Seconds 60;exit}
if($Mode -eq 'Producer'){
    $clock=[Diagnostics.Stopwatch]::StartNew()
    do{
        $tmp=Join-Path $CaseDirectory 'producer.tmp'
        @{utc=[datetime]::UtcNow.ToString('o');phase='running';monotonic_ticks=[Diagnostics.Stopwatch]::GetTimestamp()}|ConvertTo-Json|Set-Content -LiteralPath $tmp
        [IO.File]::Move($tmp,(Join-Path $CaseDirectory 'heartbeat.json'),$true)
        Set-Content -LiteralPath (Join-Path $CaseDirectory 'producer-ready') -Value $PID
        Start-Sleep -Milliseconds 100
    }while($clock.Elapsed.TotalSeconds -lt 60)
    exit
}
if($Mode -eq 'GateHolder'){
    $f=[IO.FileStream]::new((Join-Path $CaseDirectory 'transition.lock'),'Open','ReadWrite','None')
    try{
        Set-Content -LiteralPath (Join-Path $CaseDirectory 'stop-requested.json') -Value '{}';Set-Content -LiteralPath (Join-Path $CaseDirectory 'ready') -Value 'locked'
        $hold=[Diagnostics.Stopwatch]::StartNew()
        while(-not (Test-Path -LiteralPath (Join-Path $CaseDirectory 'release'))){if($hold.Elapsed.TotalSeconds -gt 15){throw 'Test release handshake timed out.'};Start-Sleep -Milliseconds 20}
    }finally{$f.Dispose()}
    exit
}
if($Mode -eq 'Watch'){
    $binding=Get-Content -LiteralPath (Join-Path $CaseDirectory 'binding.json') -Raw|ConvertFrom-Json
    $p=Get-C2BootBudget 2;$p.heartbeat_timeout_seconds=2;$p.watchdog_seconds=8
    if($binding.scenario -eq 'deadline'){$p.watchdog_seconds=1;$p.heartbeat_timeout_seconds=30}
    if($binding.scenario -eq 'native-pressure'){$p.critical_available_gib=1024}
    $null=Get-C2NativeSample
    Set-Content -LiteralPath (Join-Path $CaseDirectory 'ready') -Value $PID
    $arm=[Diagnostics.Stopwatch]::StartNew()
    while(-not (Test-Path -LiteralPath (Join-Path $CaseDirectory 'go'))){if($arm.Elapsed.TotalSeconds -gt 5){throw 'Test arming timed out.'};Start-Sleep -Milliseconds 20}
    $reason=Wait-C2WatchdogLease -Policy $p -PollMilliseconds 100 -ReadHeartbeat {
        Get-Content -LiteralPath (Join-Path $CaseDirectory 'heartbeat.json') -Raw|ConvertFrom-Json -AsHashtable
    } -ReadDeadline {
        if($binding.scenario -eq 'boot-deadline'){New-C2Deadline $binding.origin_ticks ([Diagnostics.Stopwatch]::Frequency) 90}
    } -Sample {Get-C2NativeSample} -VerifyDone {
        $t=Get-Process -Id $binding.pid -ErrorAction SilentlyContinue
        -not $t
    } -Stop {
        # Only the Process object created for this test, with exact PID and start time.
        $target=Get-Process -Id $binding.pid -ErrorAction Stop
        if($target.StartTime.ToUniversalTime().Ticks -ne ([datetime]$binding.start_utc).ToUniversalTime().Ticks){throw 'Test process ownership changed.'}
        $timing=@{}
        $adapter=@{Frequency=[Diagnostics.Stopwatch]::Frequency;Now={[Diagnostics.Stopwatch]::GetTimestamp()}
          State={$target.Refresh();if($target.HasExited){'Off'}else{'Running'}}.GetNewClosure()
          Send={$target.Kill();'process-kill-requested'}.GetNewClosure();Sleep={param($ms) Start-Sleep -Milliseconds $ms}}
        $null=Invoke-C2TimedStop $timing $adapter 5
        if(-not $timing.confirmed_off){throw 'Test process stop unverified.'}
        $timing|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $CaseDirectory 'stop-timing.json')
    }
    @{reason=$reason;utc=[DateTime]::UtcNow.ToString('o');scope='OWN_TEST_PROCESS_ONLY_NOT_VM_POWEROFF'}|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $CaseDirectory 'result.json')
    exit
}
$root=Join-Path ([IO.Path]::GetTempPath()) ('daimon-c2-watchdog-'+[guid]::NewGuid().ToString('N'))
$null=New-Item -ItemType Directory -Path $root
$exe=(Get-Process -Id $PID).Path;$results=@()
foreach($scenario in 'stale','deadline','malformed','native-pressure','done','done-live','future-tick','boot-deadline','controller-loss'){
    $dir=Join-Path $root $scenario;$null=New-Item -ItemType Directory -Path $dir
    $target=$null;$watch=$null;$producer=$null
    try {
        $target=Start-Process -FilePath $exe -ArgumentList @('-NoProfile','-File',('"'+$PSCommandPath+'"'),'-Mode','Payload') -WindowStyle Hidden -PassThru
        @{pid=$target.Id;start_utc=$target.StartTime.ToUniversalTime().ToString('o');scenario=$scenario;origin_ticks=[Diagnostics.Stopwatch]::GetTimestamp()-73*[Diagnostics.Stopwatch]::Frequency}|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $dir 'binding.json')
        @{utc=[DateTime]::UtcNow.ToString('o');monotonic_ticks=[Diagnostics.Stopwatch]::GetTimestamp();phase='running'}|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $dir 'heartbeat.json')
        if($scenario -eq 'controller-loss'){
            $producer=Start-Process -FilePath $exe -ArgumentList @('-NoProfile','-File',('"'+$PSCommandPath+'"'),'-Mode','Producer','-CaseDirectory',('"'+$dir+'"')) -WindowStyle Hidden -PassThru
            $arm=[Diagnostics.Stopwatch]::StartNew()
            while(-not (Test-Path -LiteralPath (Join-Path $dir 'producer-ready'))){if($producer.HasExited -or $arm.Elapsed.TotalSeconds -gt 10){throw 'Producer startup failed.'};Start-Sleep -Milliseconds 50}
        }
        $watch=Start-Process -FilePath $exe -ArgumentList @('-NoProfile','-File',('"'+$PSCommandPath+'"'),'-Mode','Watch','-CaseDirectory',('"'+$dir+'"')) -WindowStyle Hidden -PassThru -RedirectStandardError (Join-Path $dir 'stderr.txt') -RedirectStandardOutput (Join-Path $dir 'stdout.txt')
        $clock=[Diagnostics.Stopwatch]::StartNew()
        while(-not (Test-Path -LiteralPath (Join-Path $dir 'ready'))){if($watch.HasExited -or $clock.Elapsed.TotalSeconds -gt 15){throw 'Real watchdog startup failed.'};Start-Sleep -Milliseconds 50}
        # Atomic replacement avoids a transient torn file masquerading as a stale/deadline success.
        if($scenario -eq 'done'){$target.Kill();$null=$target.WaitForExit(5000)}
        if($producer){$producer.Kill();if(-not $producer.WaitForExit(5000)){throw 'Producer termination unverified.'}}
        $heartbeat=if($scenario -eq 'malformed'){'{broken'}else{@{utc=[DateTime]::UtcNow.ToString('o');monotonic_ticks=if($scenario -eq 'stale'){[Diagnostics.Stopwatch]::GetTimestamp()-3*[Diagnostics.Stopwatch]::Frequency}elseif($scenario -eq 'future-tick'){[Diagnostics.Stopwatch]::GetTimestamp()+10*[Diagnostics.Stopwatch]::Frequency}else{[Diagnostics.Stopwatch]::GetTimestamp()};phase=if($scenario -in 'done','done-live'){'done'}else{'running'}}|ConvertTo-Json}
        $tmp=Join-Path $dir 'next.json';Set-Content -LiteralPath $tmp -Value $heartbeat
        if($scenario -ne 'controller-loss'){[IO.File]::Move($tmp,(Join-Path $dir 'heartbeat.json'),$true)}
        Set-Content -LiteralPath (Join-Path $dir 'go') -Value 'armed'
        if(-not $watch.WaitForExit(15000) -or $watch.ExitCode -ne 0){throw ('Real watchdog failed or hung: '+(Get-Content -LiteralPath (Join-Path $dir 'stderr.txt') -Raw))}
        $r=Get-Content -LiteralPath (Join-Path $dir 'result.json') -Raw|ConvertFrom-Json
        $expected=switch($scenario){'stale'{'controller-heartbeat-lost'};'deadline'{'watchdog-deadline'};'malformed'{'watchdog-input-failed'};'native-pressure'{'watchdog-critical-memory'};'done'{'controller-finished'};'done-live'{'controller-done-unverified'};'future-tick'{'watchdog-input-failed'};'boot-deadline'{'watchdog-boot-deadline'};'controller-loss'{'controller-heartbeat-lost'}}
        if($r.reason -cne $expected){throw "Unexpected real watchdog result for $scenario : $($r.reason)"}
        $target.Refresh()
        if(-not $target.HasExited){throw 'Real payload stop not verified.'}
        $results+=@{scenario=$scenario;reason=$r.reason;seconds=$clock.Elapsed.TotalSeconds;passed=$true}
        "PASS real-process watchdog $scenario"
    }finally{
        # Process objects are held from Start-Process; no name-wide process selection.
        foreach($owned in @($producer,$watch,$target)){if($owned){$owned.Refresh();if(-not $owned.HasExited){$owned.Kill();$null=$owned.WaitForExit(5000)};$owned.Dispose()}}
    }
}
$dir=Join-Path $root 'transition';$null=New-Item -ItemType Directory -Path $dir
$path=Join-Path $dir 'transition.lock';[IO.File]::WriteAllText($path,'')
$holder=$null
try{
    $holder=Start-Process -FilePath $exe -ArgumentList @('-NoProfile','-File',('"'+$PSCommandPath+'"'),'-Mode','GateHolder','-CaseDirectory',('"'+$dir+'"')) -WindowStyle Hidden -PassThru
    $clock=[Diagnostics.Stopwatch]::StartNew()
    while(-not (Test-Path -LiteralPath (Join-Path $dir 'ready'))){if($holder.HasExited -or $clock.Elapsed.TotalSeconds -gt 15){throw 'Real gate holder failed.'};Start-Sleep -Milliseconds 50}
    $script:entered=$false;$failed=$false
    try{Invoke-C2TransitionGate $path {$script:entered=$true} -WaitSeconds 1}catch{if($_.Exception.Message -notmatch 'lock timed out'){throw};$failed=$true}
    if(-not $failed -or $script:entered){throw 'Busy real transition lock admitted action.'}
    "PASS real-process transition lock timeout"
    Set-Content -LiteralPath (Join-Path $dir 'release') -Value 'release'
    $self=Get-Process -Id $PID;$failed=$false
    try{Invoke-C2TransitionGate $path {Assert-C2StartLease $self (Join-Path $dir 'stop-requested.json');throw 'unsafe start admitted'} -WaitSeconds 10}catch{if($_.Exception.Message -notmatch 'stop vetoed'){throw};$failed=$true}
    if(-not $failed){throw 'Stop marker did not veto real transition.'}
    "PASS real-process stop veto persists after lock release"
    $null=$holder.WaitForExit(5000);$failed=$false
    try{Assert-C2StartLease $holder (Join-Path $dir 'absent-marker')}catch{if($_.Exception.Message -notmatch 'no boot'){throw};$failed=$true}
    if(-not $failed){throw 'Exited watchdog admitted start.'}
    "PASS real-process exited watchdog veto"
}finally{if($holder){$holder.Refresh();if(-not $holder.HasExited){$holder.Kill();$null=$holder.WaitForExit(5000)};$holder.Dispose()}}
@{tests=$results;transition_tests=3;vm_poweroff='NOT_RUN';evidence=$root}|ConvertTo-Json -Depth 5|Set-Content -LiteralPath (Join-Path $root 'summary.json')
"PASS 12 real-process watchdog/transition tests; VM poweroff NOT RUN; evidence: $root"
