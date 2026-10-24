# Timing only. No Hyper-V commands, wall-clock decisions or ambient authority.
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
function Get-C2MonotonicStamp {
    @{ticks=[Diagnostics.Stopwatch]::GetTimestamp();frequency=[Diagnostics.Stopwatch]::Frequency;utc=[datetime]::UtcNow.ToString('o')}
}
function Assert-C2AsyncStopCapability($Command) {
    if($Command.Name -cne 'Stop-VM' -or $Command.ModuleName -cne 'Hyper-V'){throw 'Native Hyper-V Stop-VM metadata required before boot.'}
    foreach($parameter in 'VM','TurnOff','Force','AsJob'){
        if(-not $Command.Parameters.ContainsKey($parameter)){throw 'Required asynchronous poweroff interface unavailable; no boot.'}
    }
}
function New-C2Deadline([long]$OriginTicks,[long]$Frequency,[double]$LimitSeconds=90) {
    if($OriginTicks -le 0 -or $Frequency -le 0 -or $LimitSeconds -lt 30){throw 'Invalid monotonic deadline contract.'}
    @{version=1;origin_ticks=$OriginTicks;frequency=$Frequency;limit_seconds=$LimitSeconds
      controller_stop_at_seconds=$LimitSeconds-20;watchdog_stop_at_seconds=$LimitSeconds-18
      confirmation_timeout_seconds=15;last_ticks=$OriginTicks}
}
function Get-C2DeadlineState([hashtable]$Deadline,[long]$NowTicks) {
    if($Deadline.frequency -le 0 -or $Deadline.origin_ticks -le 0 -or $NowTicks -lt $Deadline.last_ticks -or $Deadline.last_ticks -lt $Deadline.origin_ticks){throw 'Monotonic clock invalid or reversed.'}
    $Deadline.last_ticks=$NowTicks
    $elapsed=([double]$NowTicks-[double]$Deadline.origin_ticks)/$Deadline.frequency
    @{elapsed_seconds=$elapsed;request_stop=($elapsed -ge $Deadline.controller_stop_at_seconds)
      watchdog_stop=($elapsed -ge $Deadline.watchdog_stop_at_seconds);expired=($elapsed -gt $Deadline.limit_seconds)
      seconds_until_stop=[math]::Max(0,$Deadline.controller_stop_at_seconds-$elapsed)}
}
function Get-C2TemporalVerdict([hashtable]$Deadline,[hashtable]$Timing) {
    if($Timing.ContainsKey('clock_invalid') -and $Timing.clock_invalid){return 'CLOCK_INVALID'}
    if(-not $Timing.ContainsKey('off_observed_ticks') -or $null -eq $Timing.off_observed_ticks -or -not $Timing.ContainsKey('confirmed_off') -or -not $Timing.confirmed_off){return 'OFF_UNCONFIRMED'}
    $off=[long]$Timing.off_observed_ticks
    if($Deadline.frequency -le 0 -or $off -lt $Deadline.origin_ticks){return 'CLOCK_INVALID'}
    $previous=[long]$Deadline.origin_ticks
    foreach($stage in 'stop_requested_ticks','stop_command_invoked_ticks','stop_command_returned_ticks'){
        if($Timing.ContainsKey($stage) -and $null -ne $Timing[$stage] -and ($Timing[$stage] -lt $Deadline.origin_ticks -or $Timing[$stage] -gt $off)){return 'CLOCK_INVALID'}
        if($Timing.ContainsKey($stage) -and $null -ne $Timing[$stage]){if($Timing[$stage] -lt $previous){return 'CLOCK_INVALID'};$previous=[long]$Timing[$stage]}
    }
    if($Timing.ContainsKey('confirmation_timeout') -and $Timing.confirmation_timeout){return 'STOP_CONFIRMATION_TIMEOUT'}
    if($Timing.ContainsKey('command_error') -and $Timing.command_error){return 'STOP_COMMAND_ERROR'}
    if(($off-$Deadline.origin_ticks)/[double]$Deadline.frequency -gt $Deadline.limit_seconds){return 'OFF_LATE'}
    'OFF_BY_DEADLINE'
}
function Invoke-C2TimedStop([hashtable]$Timing,[hashtable]$Adapter,[double]$TimeoutSeconds=15) {
    # Send must dispatch asynchronously. The observer timeout includes dispatch latency.
    # A hanging provider still cannot be forcibly bounded here: no Off observation means no PASS.
    if($TimeoutSeconds -le 0){throw 'Positive stop confirmation timeout required.'}
    $last=[long](& $Adapter.Now);$frequency=[long]$Adapter.Frequency
    if($frequency -le 0){throw 'Invalid stop clock frequency.'}
    if(-not $Timing.ContainsKey('stop_requested_ticks')){$Timing.stop_requested_ticks=$last}
    $Timing.state_before_stop=[string](& $Adapter.State)
    $Timing.state_before_stop_ticks=[long](& $Adapter.Now)
    if($Timing.state_before_stop_ticks -lt $last){$Timing.clock_invalid=$true;throw 'Stop clock reversed.'}
    $last=$Timing.state_before_stop_ticks
    if($Timing.state_before_stop -eq 'Off'){
        $now=[long](& $Adapter.Now)
        if($now -lt $last){$Timing.clock_invalid=$true;throw 'Stop clock reversed.'}
        if(-not $Timing.ContainsKey('off_observed_ticks')){$Timing.off_observed_ticks=$now}
        $Timing.confirmed_off=$true;return $Timing
    }
    $sent=[long](& $Adapter.Now)
    if($sent -lt $last){$Timing.clock_invalid=$true;throw 'Stop clock reversed.'}
    if(-not $Timing.ContainsKey('stop_command_invoked_ticks')){$Timing.stop_command_invoked_ticks=$sent}
    $Timing.last_non_off_ticks=$Timing.state_before_stop_ticks
    try{$ticket=& $Adapter.Send}catch{$Timing.command_error=$_.Exception.Message;$ticket=$null}
    $returned=[long](& $Adapter.Now)
    if($returned -lt $sent){$Timing.clock_invalid=$true;throw 'Stop clock reversed.'}
    if(-not $Timing.ContainsKey('stop_command_returned_ticks')){$Timing.stop_command_returned_ticks=$returned}
    $last=$returned
    do {
        $state=[string](& $Adapter.State);$now=[long](& $Adapter.Now)
        if($now -lt $last){$Timing.clock_invalid=$true;throw 'Stop clock reversed.'};$last=$now
        $Timing.last_observed_state=$state;$Timing.last_observed_ticks=$now
        if($state -eq 'Off'){
            if(-not $Timing.ContainsKey('off_observed_ticks')){$Timing.off_observed_ticks=$now}
            $Timing.confirmed_off=$true;return $Timing
        }
        $Timing.last_non_off_ticks=$now
        if($ticket -and $Adapter.ContainsKey('CommandState')){$Timing.stop_job_state=[string](& $Adapter.CommandState $ticket)}
        if(($now-$sent)/[double]$frequency -ge $TimeoutSeconds){break}
        & $Adapter.Sleep 100
    }while($true)
    $Timing.confirmed_off=$false;$Timing.confirmation_timeout=$true
    $Timing
}
Export-ModuleMember -Function Get-C2MonotonicStamp,Assert-C2AsyncStopCapability,New-C2Deadline,Get-C2DeadlineState,Get-C2TemporalVerdict,Invoke-C2TimedStop
