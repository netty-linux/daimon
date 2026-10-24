#Requires -Version 7.4
# Real OS processes, real heartbeat files, real native memory samples. Never imports Hyper-V.
[CmdletBinding()]
param([ValidateSet('Run','Payload','Watch','GateHolder')][string]$Mode='Run',[string]$CaseDirectory)
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-boot.psm1') -Force
if($Mode -eq 'Payload'){Start-Sleep -Seconds 60;exit}
if($Mode -eq 'GateHolder'){
    $f=[IO.FileStream]::new((Join-Path $CaseDirectory 'transition.lock'),'Open','ReadWrite','None')
    try{Set-Content -LiteralPath (Join-Path $CaseDirectory 'stop-requested.json') -Value '{}';Set-Content -LiteralPath (Join-Path $CaseDirectory 'ready') -Value 'locked';Start-Sleep -Seconds 3}finally{$f.Dispose()}
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
    } -Sample {Get-C2NativeSample} -Stop {
        # Only the Process object created for this test, with exact PID and start time.
        $target=Get-Process -Id $binding.pid -ErrorAction Stop
        if($target.StartTime.ToUniversalTime().Ticks -ne ([datetime]$binding.start_utc).ToUniversalTime().Ticks){throw 'Test process ownership changed.'}
        $target.Kill();if(-not $target.WaitForExit(5000)){throw 'Test process stop unverified.'}
    }
    @{reason=$reason;utc=[DateTime]::UtcNow.ToString('o');scope='OWN_TEST_PROCESS_ONLY_NOT_VM_POWEROFF'}|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $CaseDirectory 'result.json')
    exit
}
$root=Join-Path ([IO.Path]::GetTempPath()) ('daimon-c2-watchdog-'+[guid]::NewGuid().ToString('N'))
$null=New-Item -ItemType Directory -Path $root
$exe=(Get-Process -Id $PID).Path;$results=@()
foreach($scenario in 'stale','deadline','malformed','native-pressure','done'){
    $dir=Join-Path $root $scenario;$null=New-Item -ItemType Directory -Path $dir
    $target=$null;$watch=$null
    try {
        $target=Start-Process -FilePath $exe -ArgumentList @('-NoProfile','-File',('"'+$PSCommandPath+'"'),'-Mode','Payload') -WindowStyle Hidden -PassThru
        @{pid=$target.Id;start_utc=$target.StartTime.ToUniversalTime().ToString('o');scenario=$scenario}|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $dir 'binding.json')
        @{utc=[DateTime]::UtcNow.AddSeconds(5).ToString('o');phase='running'}|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $dir 'heartbeat.json')
        $watch=Start-Process -FilePath $exe -ArgumentList @('-NoProfile','-File',('"'+$PSCommandPath+'"'),'-Mode','Watch','-CaseDirectory',('"'+$dir+'"')) -WindowStyle Hidden -PassThru -RedirectStandardError (Join-Path $dir 'stderr.txt') -RedirectStandardOutput (Join-Path $dir 'stdout.txt')
        $clock=[Diagnostics.Stopwatch]::StartNew()
        while(-not (Test-Path -LiteralPath (Join-Path $dir 'ready'))){if($watch.HasExited -or $clock.Elapsed.TotalSeconds -gt 15){throw 'Real watchdog startup failed.'};Start-Sleep -Milliseconds 50}
        # Atomic replacement avoids a transient torn file masquerading as a stale/deadline success.
        $heartbeat=if($scenario -eq 'malformed'){'{broken'}else{@{utc=if($scenario -eq 'stale'){[DateTime]::UtcNow.AddSeconds(-3).ToString('o')}else{[DateTime]::UtcNow.ToString('o')};phase=if($scenario -eq 'done'){'done'}else{'running'}}|ConvertTo-Json}
        $tmp=Join-Path $dir 'next.json';Set-Content -LiteralPath $tmp -Value $heartbeat
        [IO.File]::Move($tmp,(Join-Path $dir 'heartbeat.json'),$true)
        Set-Content -LiteralPath (Join-Path $dir 'go') -Value 'armed'
        if(-not $watch.WaitForExit(15000) -or $watch.ExitCode -ne 0){throw ('Real watchdog failed or hung: '+(Get-Content -LiteralPath (Join-Path $dir 'stderr.txt') -Raw))}
        $r=Get-Content -LiteralPath (Join-Path $dir 'result.json') -Raw|ConvertFrom-Json
        $expected=switch($scenario){'stale'{'controller-heartbeat-lost'};'deadline'{'watchdog-deadline'};'malformed'{'watchdog-input-failed'};'native-pressure'{'watchdog-critical-memory'};'done'{'controller-finished'}}
        if($r.reason -cne $expected){throw "Unexpected real watchdog result for $scenario : $($r.reason)"}
        $target.Refresh()
        if($scenario -eq 'done'){if($target.HasExited){throw 'Done lease killed payload.'}}elseif(-not $target.HasExited){throw 'Real payload stop not verified.'}
        $results+=@{scenario=$scenario;reason=$r.reason;seconds=$clock.Elapsed.TotalSeconds;passed=$true}
        "PASS real-process watchdog $scenario"
    }finally{
        # Process objects are held from Start-Process; no name-wide process selection.
        foreach($owned in @($watch,$target)){if($owned){$owned.Refresh();if(-not $owned.HasExited){$owned.Kill();$null=$owned.WaitForExit(5000)};$owned.Dispose()}}
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
"PASS 8 real-process watchdog/transition tests; VM poweroff NOT RUN; evidence: $root"
