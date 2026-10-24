#Requires -Version 7.4
# Own helper processes only. No Hyper-V commands or VM access, including in elevated mode.
[CmdletBinding()]
param([ValidateSet('Run','Supervisor','Controller','Watcher','Payload')][string]$Mode='Run',
 [string]$CaseDirectory,[switch]$RequireIndependent,[string]$ReportPath)
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-helper.psm1')
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-boot.psm1')
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-deadline.psm1')
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-test-cleanup.psm1')
function Write-Data($path,$data){$tmp=$path+'.'+[guid]::NewGuid().ToString('N')+'.tmp';$data|ConvertTo-Json -Depth 12|Set-Content -LiteralPath $tmp;[IO.File]::Move($tmp,$path,$true)}
function Read-Data($path){Get-Content -LiteralPath $path -Raw|ConvertFrom-Json -AsHashtable}
function Wait-File($path,[double]$seconds=15){$c=[Diagnostics.Stopwatch]::StartNew();while(-not (Test-Path -LiteralPath $path)){if($c.Elapsed.TotalSeconds -gt $seconds){throw ('Missing test evidence: '+[IO.Path]::GetFileName($path))};Start-Sleep -Milliseconds 50}}
function New-TestDirectory([string]$path){
    if(Test-Path -LiteralPath $path){throw 'Fresh own-user test directory required.'}
    $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl=[Security.AccessControl.DirectorySecurity]::new();$acl.SetAccessRuleProtection($true,$false);$acl.SetOwner($sid)
    foreach($identity in @($sid,[Security.Principal.SecurityIdentifier]::new('S-1-5-18'))){
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($identity,'FullControl','ContainerInherit, ObjectInherit','None','Allow'))
    }
    [IO.FileSystemAclExtensions]::Create([IO.DirectoryInfo]::new($path),$acl)
}
$exe=(Get-Process -Id $PID).Path
function Spawn($role,$dir){Start-Process -FilePath $exe -ArgumentList @('-NoProfile','-NonInteractive','-File',('"'+$PSCommandPath+'"'),'-Mode',$role,'-CaseDirectory',('"'+$dir+'"')) -WindowStyle Hidden -PassThru -RedirectStandardError (Join-Path $dir ($role+'.stderr.txt')) -RedirectStandardOutput (Join-Path $dir ($role+'.stdout.txt'))}
function Identity($p,$role,$dir){@{pid=$p.Id;started_utc=$p.StartTime.ToUniversalTime().ToString('o');path=$exe;creator_pid=$PID;directory=$dir;entry=$PSCommandPath;role=$role}}
function Bound-Process($i){
    $p=Get-Process -Id $i.pid -ErrorAction Stop
    if($i.path -ine $exe -or $i.entry -ine $PSCommandPath -or $p.StartTime.ToUniversalTime().Ticks -ne ([datetime]$i.started_utc).ToUniversalTime().Ticks -or $p.Path -ine $exe){throw 'Own test PID/start/path binding failed.'}
    $metadata=Get-CimInstance Win32_Process -Filter ('ProcessId = '+[int]$i.pid) -OperationTimeoutSec 5 -ErrorAction Stop
    if($metadata.ParentProcessId -ne $i.creator_pid -or -not $metadata.CommandLine -or $metadata.CommandLine.IndexOf($PSCommandPath,[StringComparison]::OrdinalIgnoreCase) -lt 0 -or $metadata.CommandLine.IndexOf($i.directory,[StringComparison]::OrdinalIgnoreCase) -lt 0){throw 'Own helper creator/entry/case binding failed.'}
    $p
}
if($Mode -eq 'Payload'){Start-Sleep -Seconds 90;exit}
if($Mode -eq 'Controller'){
    $identity=Assert-C2DedicatedHelper $PSCommandPath
    $b=Read-Data (Join-Path $CaseDirectory 'binding.json')
    Write-Data (Join-Path $CaseDirectory 'heartbeat.json') @{phase='arming';monotonic_ticks=[Diagnostics.Stopwatch]::GetTimestamp()}
    Wait-File (Join-Path $CaseDirectory 'watch-ready.json')
    # The root must hold verified handles before either helper is allowed to exit.
    Wait-File (Join-Path $CaseDirectory 'cleanup-handles-ready.json')
    $ready=Read-Data (Join-Path $CaseDirectory 'watch-ready.json')
    if(-not $ready.verified){throw 'Test watchdog not isolated.'}
    $stamp=Get-C2MonotonicStamp
    $script:fault=@{method='HeartbeatStall30';vm_id='test-payload';operation_id=$b.operation_id;controller_isolation=$identity;last_heartbeat_ticks=$stamp.ticks
      watchdog_pid=$ready.pid;watchdog_started_utc=$ready.started_utc;observed_vm_state='Running'}
    Write-Data (Join-Path $CaseDirectory 'fault.json') $script:fault
    Write-Data (Join-Path $CaseDirectory 'heartbeat.json') @{phase='running';monotonic_ticks=$stamp.ticks}
    if($b.scenario -eq 'supervisor-exit'){
        $script:fault.method='OwnHelperExit';Write-Data (Join-Path $CaseDirectory 'fault.json') $script:fault
        Write-Data (Join-Path $CaseDirectory 'fault-active.json') @{method='Own controller exit';pid=$PID}
        [Environment]::Exit(86)
    }
    Invoke-C2HeartbeatStall -EntryFile $PSCommandPath -Before {
        param($fresh)
        if($fresh.pid -ne $script:fault.controller_isolation.pid){throw 'Controller identity changed.'}
        Write-Data (Join-Path $CaseDirectory 'fault-active.json') @{method='HeartbeatStall30';pid=$PID}
    } -After {Write-Data (Join-Path $CaseDirectory 'fault-return.json') @{ticks=[Diagnostics.Stopwatch]::GetTimestamp();pid=$PID}}
    exit
}
if($Mode -eq 'Watcher'){
    $isolation=Assert-C2DedicatedHelper $PSCommandPath
    $b=Read-Data (Join-Path $CaseDirectory 'binding.json');$target=Bound-Process $b.payload
    $null=Get-C2NativeSample
    Write-Data (Join-Path $CaseDirectory 'watch-ready.json') $isolation
    $timing=@{};$policy=Get-C2BootBudget 2
    $reason=Wait-C2WatchdogLease -Policy $policy -PollMilliseconds 100 -ReadHeartbeat {Read-Data (Join-Path $CaseDirectory 'heartbeat.json')} -Sample {Get-C2NativeSample} -Stop {
        $target=Bound-Process $b.payload;$started=$target.StartTime.ToUniversalTime().Ticks
        $adapter=@{Frequency=[Diagnostics.Stopwatch]::Frequency;Now={[Diagnostics.Stopwatch]::GetTimestamp()}
          State={$target.Refresh();if($target.HasExited){'Off'}else{'Running'}}.GetNewClosure()
          Send={if($target.StartTime.ToUniversalTime().Ticks -ne $started -or $target.Path -ine $exe){throw 'Own target changed.'};$target.Kill();'own-test-payload-kill'}.GetNewClosure();Sleep={param($ms)Start-Sleep -Milliseconds $ms}}
        $null=Invoke-C2TimedStop $timing $adapter 15
        if(-not $timing.confirmed_off){throw 'Test payload Off unproved.'}
    }
    $deadline=New-C2Deadline $b.origin_ticks ([Diagnostics.Stopwatch]::Frequency) 90
    $result=@{reason=$reason;timing=$timing;temporal_verdict=Get-C2TemporalVerdict $deadline $timing;poweroff_command_issued=$timing.ContainsKey('stop_command_invoked_ticks')
      vm_id='test-payload';operation_id=$b.operation_id;isolation=$isolation;scope='OWN_PROCESS_ONLY_NOT_VM'}
    Write-Data (Join-Path $CaseDirectory 'watch-result.json') $result
    exit
}
if($Mode -eq 'Supervisor'){
    $isolation=Assert-C2DedicatedHelper $PSCommandPath
    $config=Read-Data (Join-Path $CaseDirectory 'config.json')
    $payload=Spawn 'Payload' $CaseDirectory
    $binding=@{operation_id=[guid]::NewGuid().ToString();origin_ticks=[Diagnostics.Stopwatch]::GetTimestamp();payload=Identity $payload 'Payload' $CaseDirectory;scenario=$config.scenario;supervisor=$isolation}
    Write-Data (Join-Path $CaseDirectory 'binding.json') $binding
    $controller=Spawn 'Controller' $CaseDirectory
    Wait-File (Join-Path $CaseDirectory 'heartbeat.json')
    $watcher=Spawn 'Watcher' $CaseDirectory
    Write-Data (Join-Path $CaseDirectory 'children.json') @{payload=Identity $payload 'Payload' $CaseDirectory;controller=Identity $controller 'Controller' $CaseDirectory;watcher=Identity $watcher 'Watcher' $CaseDirectory}
    Wait-File (Join-Path $CaseDirectory 'fault-active.json')
    if($config.scenario -eq 'supervisor-exit'){
        Write-Data (Join-Path $CaseDirectory 'supervisor-exit.json') @{pid=$PID;isolation=$isolation;utc=[datetime]::UtcNow.ToString('o')}
        [Environment]::Exit(0)
    }
    if(-not $controller.WaitForExit(40000)){throw 'Bounded controller stall did not return.'}
    if($controller.ExitCode -ne 0){throw 'Controller test failed.'}
    Wait-File (Join-Path $CaseDirectory 'watch-result.json') 10
    exit
}
$report=@{status='RUNNING';vm_access=$false;tests=@();independence='UNPROVEN';utc=[datetime]::UtcNow.ToString('o')}
try{
    $isolation=Get-C2HelperIsolation;$report.launcher_isolation=$isolation
    if(-not $isolation.verified){
        $refused=$false;try{$null=Assert-C2HelperIsolation}catch{$refused=$true}
        if(-not $refused){throw 'Attached helper was not refused.'}
        $report.status='BLOCKED';$report.independence='BLOCKED_ENVIRONMENT_JOB_OR_CONSOLE';$report.tests+=@{name='fail-closed job/console refusal';passed=$true}
        if($RequireIndependent){throw 'Detached helper proof blocked by environment.'}
        'PASS fail-closed isolation refusal; detached proof NOT RUN in this environment.'
    }else{
        $root=Join-Path ([IO.Path]::GetTempPath()) ('daimon-c2-emergency-'+[guid]::NewGuid().ToString('N'))
        New-TestDirectory $root
        $report.private_evidence_root=$root
        foreach($scenario in 'stall','supervisor-exit'){
            $dir=Join-Path $root $scenario;New-TestDirectory $dir
            Write-Data (Join-Path $dir 'config.json') @{scenario=$scenario}
            $supervisor=$null;$registrations=@{};$caseResult=$null
            try{
                $supervisor=Spawn 'Supervisor' $dir
                $registrations.supervisor=Register-C2OwnProcess (Identity $supervisor 'Supervisor' $dir) $PSCommandPath $dir $exe $PID 'Supervisor'
                Wait-File (Join-Path $dir 'children.json')
                $manifest=Read-Data (Join-Path $dir 'children.json')
                foreach($role in 'payload','controller','watcher'){
                    $expectedRole=@{payload='Payload';controller='Controller';watcher='Watcher'}[$role]
                    $registrations[$role]=Register-C2OwnProcess $manifest[$role] $PSCommandPath $dir $exe $supervisor.Id $expectedRole
                }
                Write-Data (Join-Path $dir 'cleanup-handles-ready.json') @{verified=$true;count=$registrations.Count}
                if(-not $supervisor.WaitForExit(45000)){throw 'Own supervisor timeout.'}
                if($supervisor.ExitCode -ne 0){throw 'Own supervisor failed; inspect private stderr.'}
                Wait-File (Join-Path $dir 'watch-result.json') 35
                $fault=Read-Data (Join-Path $dir 'fault.json');$watch=Read-Data (Join-Path $dir 'watch-result.json');$b=Read-Data (Join-Path $dir 'binding.json')
                $d=New-C2Deadline $b.origin_ticks ([Diagnostics.Stopwatch]::Frequency) 90
                if($scenario -eq 'stall'){
                    $verdict=Get-C2EmergencyProofVerdict $fault $watch $d
                    if($verdict -ne 'WATCHDOG_POWEROFF_PROVEN'){throw ('Shared proof validator refused: '+$verdict)}
                    $returned=Read-Data (Join-Path $dir 'fault-return.json')
                    if($watch.timing.off_observed_ticks -ge $returned.ticks){throw 'Watchdog did not act while controller was stalled.'}
                }else{
                    if(-not (Test-Path -LiteralPath (Join-Path $dir 'supervisor-exit.json'))){throw 'Supervisor exit missing.'}
                    if($watch.reason -ne 'controller-heartbeat-lost' -or -not $watch.timing.confirmed_off -or $watch.timing.state_before_stop -ne 'Running' -or -not $watch.isolation.verified -or -not $watch.poweroff_command_issued){throw 'Independent process stop unproved.'}
                    $children=Read-Data (Join-Path $dir 'children.json')
                    if(-not $registrations.controller.process.HasExited){throw 'Original controller did not exit.'}
                    $verdict='WATCHDOG_SURVIVED_OWN_CONTROLLER_AND_SUPERVISOR_EXIT'
                }
                $caseResult=@{name=$scenario;passed=$false;verdict=$verdict;scope='OWN_PROCESS_NOT_VM';fault=$fault;watchdog=$watch;supervisor=$b.supervisor;deadline=$d;cleanup=@()}
                $report.tests+=$caseResult
            }finally{
                $cleanup=@();$unsafe=$false
                foreach($role in $registrations.Keys){
                    $r=$registrations[$role]
                    try {$e=Invoke-C2OwnProcessCleanup $r;$cleanup+=$e;if($e.status -notin 'EXITED_PROVEN','STOPPED_OWN_PROVEN','ALREADY_CLEAN'){$unsafe=$true}}
                    catch {$unsafe=$true;$cleanup+=@{status='INDETERMINATE';identity=$r.identity;error=$_.Exception.Message;kill_issued=$false}}
                    finally {$r.process.Dispose()}
                }
                if($supervisor){$supervisor.Dispose()}
                Write-Data (Join-Path $dir 'cleanup-result.json') @{entries=$cleanup;complete=($registrations.Count -eq 4 -and -not $unsafe);external_processes_terminated=$false}
                if($caseResult){$caseResult.cleanup=$cleanup;$caseResult.cleanup_complete=($registrations.Count -eq 4 -and -not $unsafe);$caseResult.external_processes_terminated=$false}
                if($unsafe){throw 'Own helper cleanup identity divergent or indeterminate; no unverified process terminated. Inspect cleanup-result.json.'}
            }
            if(-not $caseResult.cleanup_complete){throw 'Own helper cleanup incomplete.'}
            $caseResult.passed=$true
            "PASS real independent process scenario $scenario including verified cleanup"
        }
        $report.status='PASS';$report.independence='PROVEN_FOR_OWN_OS_PROCESSES_NO_SHARED_JOB_OR_CONSOLE';'PASS 2 detached real-process emergency scenarios; VM NOT ACCESSED.'
    }
}catch{$report.failure=$_.Exception.Message;if($report.status -ne 'BLOCKED'){$report.status='FAIL'};$report.independence='UNPROVEN';throw}
finally{if($ReportPath){$report|ConvertTo-Json -Depth 18|Set-Content -LiteralPath $ReportPath -Encoding utf8}}
