#Requires -Version 7.4
# Deterministic clocks/adapters only. No Hyper-V import, VM or host mutation.
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-deadline.psm1')
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-boot.psm1') -Force
$count=0
function Check($value,$message){if(-not $value){throw $message}}
function Test($name,[scriptblock]$body){& $body;$script:count++;"PASS $name"}
function Contract {New-C2Deadline 1000 1000 90}
function ObservedOff($seconds){@{confirmed_off=$true;off_observed_ticks=[long](1000+1000*$seconds)}}
Test 'unchanged absolute deadline has 20-second controller and 18-second watchdog lead'{
    $d=Contract;Check ($d.limit_seconds -eq 90 -and $d.controller_stop_at_seconds -eq 70 -and $d.watchdog_stop_at_seconds -eq 72) 'deadline changed'
}
Test 'stop is due at lead boundary before the next monitor sample'{
    $d=Contract;Check (-not (Get-C2DeadlineState $d 70999).request_stop) 'early lead';Check ((Get-C2DeadlineState $d 71000).request_stop) 'late lead'
}
Test 'blocking telemetry jump past lead immediately requests stop'{Check ((Get-C2DeadlineState (Contract) 76500).request_stop) 'jitter missed'}
Test 'reversed monotonic samples fail closed'{
    $d=Contract;$null=Get-C2DeadlineState $d 5000;$failed=$false
    try{Get-C2DeadlineState $d 4999}catch{$failed=$true};Check $failed 'clock rollback admitted'
}
foreach($off in 89.999,90,90.001,91.1312018){
    Test "Off at $off seconds is evaluated using confirmation, not stop request" {
        $t=ObservedOff $off;$t.stop_requested_ticks=71000;$t.stop_command_invoked_ticks=72000
        $v=Get-C2TemporalVerdict (Contract) $t
        Check ($v -eq $(if($off -le 90){'OFF_BY_DEADLINE'}else{'OFF_LATE'})) 'false timing pass'
    }
}
Test 'command completed without observed Off never passes'{Check ((Get-C2TemporalVerdict (Contract) @{stop_command_returned_ticks=72000}) -eq 'OFF_UNCONFIRMED') 'command completion became Off'}
Test 'Off timestamp without confirmed state never passes'{Check ((Get-C2TemporalVerdict (Contract) @{off_observed_ticks=80000;confirmed_off=$false}) -eq 'OFF_UNCONFIRMED') 'unverified timestamp accepted'}
Test 'inverted command and Off timestamps fail closed'{
    $t=ObservedOff 80;$t.stop_command_returned_ticks=85000;Check ((Get-C2TemporalVerdict (Contract) $t) -eq 'CLOCK_INVALID') 'inversion accepted'
}
Test 'earlier confirmation timeout remains failure after later recovery'{
    $t=ObservedOff 88;$t.confirmation_timeout=$true;Check ((Get-C2TemporalVerdict (Contract) $t) -eq 'STOP_CONFIRMATION_TIMEOUT') 'recovery erased timeout'
}
Test 'dispatch error remains failure even when another actor reaches Off'{
    $t=ObservedOff 80;$t.command_error='dispatch failed';Check ((Get-C2TemporalVerdict (Contract) $t) -eq 'STOP_COMMAND_ERROR') 'partial dispatch failure erased'
}
Test 'stop request cannot be later than command invocation'{
    $t=ObservedOff 80;$t.stop_requested_ticks=74000;$t.stop_command_invoked_ticks=73000
    Check ((Get-C2TemporalVerdict (Contract) $t) -eq 'CLOCK_INVALID') 'stage inversion accepted'
}
Test 'state-read failure cannot become an Off result'{
    $t=@{};$a=@{Frequency=1000;Now={71000};State={throw 'provider state unavailable'};Send={throw 'not reached'}};$failed=$false
    try{Invoke-C2TimedStop $t $a 1}catch{$failed=$true}
    Check ($failed -and (Get-C2TemporalVerdict (Contract) $t) -eq 'OFF_UNCONFIRMED') 'state failure became Off'
}
function FakeAdapter([int]$delay,[switch]$SendFailure,[switch]$Reverse){
    $model=@{now=71000L;sent=$false;off_at=[long](71000+$delay);sleep_count=0}
    $adapter=@{Frequency=1000;Now={if($Reverse -and $model.sent){$model.now-100}else{$model.now}}.GetNewClosure()
      State={if($model.sent -and $model.now -ge $model.off_at){'Off'}else{'Running'}}.GetNewClosure()
      Send={if($SendFailure){throw 'dispatch failed'};$model.sent=$true;'ticket'}.GetNewClosure()
      CommandState={'Completed'};Sleep={param($ms)$model.now+=$ms;$model.sleep_count++}.GetNewClosure()}
    @{model=$model;adapter=$adapter}
}
Test 'separate request, dispatch and Off observation with delayed transition'{
    $f=FakeAdapter 1400;$t=@{};$null=Invoke-C2TimedStop $t $f.adapter 15
    Check ($t.confirmed_off -and $t.stop_requested_ticks -eq 71000 -and $t.stop_command_invoked_ticks -eq 71000 -and $t.off_observed_ticks -eq 72400 -and $t.last_non_off_ticks -eq 72300) 'timing stages collapsed'
}
Test 'slow provider confirmation does not become temporal PASS'{
    $f=FakeAdapter 21000;$t=@{};$null=Invoke-C2TimedStop $t $f.adapter 30
    Check ((Get-C2TemporalVerdict (Contract) $t) -eq 'OFF_LATE') 'slow Off passed'
}
Test 'timeout stops observation and leaves Off unconfirmed'{
    $f=FakeAdapter 30000;$t=@{};$null=Invoke-C2TimedStop $t $f.adapter 15
    Check ($t.confirmation_timeout -and -not $t.confirmed_off -and $f.model.sleep_count -eq 150) 'unbounded or false Off'
}
Test 'a recovery observation cannot replace original dispatch timestamps or erase timeout'{
    $f=FakeAdapter 30000;$t=@{};$null=Invoke-C2TimedStop $t $f.adapter 15
    $f.model.off_at=$f.model.now+500;$null=Invoke-C2TimedStop $t $f.adapter 15
    Check ($t.confirmed_off -and $t.stop_requested_ticks -eq 71000 -and $t.stop_command_invoked_ticks -eq 71000 -and $t.stop_command_returned_ticks -eq 71000) 'recovery overwrote dispatch'
    Check ((Get-C2TemporalVerdict (Contract) $t) -eq 'STOP_CONFIRMATION_TIMEOUT') 'recovery erased failure'
}
Test 'dispatch failure plus completed status cannot prove shutdown'{
    $f=FakeAdapter 0 -SendFailure;$t=@{};$null=Invoke-C2TimedStop $t $f.adapter 1
    Check ($t.command_error -and -not $t.confirmed_off) 'dispatch failure passed'
}
Test 'reversed stop clock fails closed'{
    $f=FakeAdapter 100 -Reverse;$t=@{};$failed=$false;try{Invoke-C2TimedStop $t $f.adapter 1}catch{$failed=$true}
    Check ($failed -and $t.clock_invalid) 'reversed dispatch clock accepted'
}
Test 'already Off needs a fresh state observation, not another stop command'{
    $a=@{Frequency=1000;Now={75000};State={'Off'};Send={throw 'must not dispatch'}};$t=@{}
    $null=Invoke-C2TimedStop $t $a 1;Check ($t.confirmed_off -and -not $t.ContainsKey('stop_command_invoked_ticks')) 'redundant dispatch'
}
Test 'UTC jumps do not change monotonic heartbeat age'{
    $p=Get-C2BootBudget 2;$p.watchdog_seconds=1;$p.heartbeat_timeout_seconds=10
    $r=Wait-C2WatchdogLease -Policy $p -PollMilliseconds 20 -ReadHeartbeat {@{phase='done';utc='1900-01-01';monotonic_ticks=[Diagnostics.Stopwatch]::GetTimestamp()}} -Sample {throw 'not sampled after verified done'} -VerifyDone {$true} -Stop {throw 'must not stop'}
    Check ($r -eq 'controller-finished') 'UTC affected lease'
}
Test 'done heartbeat cannot suppress stop without Off verification'{
    $p=Get-C2BootBudget 2;$script:stopped=$false
    $r=Wait-C2WatchdogLease -Policy $p -ReadHeartbeat {@{phase='done';monotonic_ticks=[Diagnostics.Stopwatch]::GetTimestamp()}} -Sample {throw 'not needed'} -Stop {$script:stopped=$true}
    Check ($r -eq 'controller-done-unverified' -and $script:stopped) 'false done suppressed recovery'
}
Test 'watchdog boot clock preempts a still-fresh controller heartbeat'{
    $stamp=Get-C2MonotonicStamp;$d=New-C2Deadline ($stamp.ticks-73*$stamp.frequency) $stamp.frequency 90;$script:stopped=$false
    $r=Wait-C2WatchdogLease -Policy (Get-C2BootBudget 2) -ReadDeadline {$d}.GetNewClosure() -ReadHeartbeat {throw 'deadline checked first'} -Sample {throw 'deadline checked first'} -Stop {$script:stopped=$true}
    Check ($r -eq 'watchdog-boot-deadline' -and $script:stopped) 'fresh heartbeat defeated deadline'
}
"PASS $count deterministic deadline tests; VM NOT ACCESSED."
