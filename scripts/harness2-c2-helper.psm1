# Dedicated helper isolation and bounded, self-only heartbeat fault. No VM or service APIs.
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-deadline.psm1')
function Get-C2HelperIsolation {
    if(-not ('DaimonC2.HelperIsolation' -as [type])){
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace DaimonC2 {
 public static class HelperIsolation {
  [DllImport("kernel32.dll")] public static extern IntPtr GetCurrentProcess();
  [DllImport("kernel32.dll",SetLastError=true)] public static extern bool IsProcessInJob(IntPtr p,IntPtr j,out bool b);
  [DllImport("kernel32.dll",SetLastError=true)] public static extern uint GetConsoleProcessList(uint[] p,uint n);
  [DllImport("kernel32.dll")] public static extern IntPtr GetConsoleWindow();
 }
}
'@
    }
    $inJob=$true;$ok=[DaimonC2.HelperIsolation]::IsProcessInJob([DaimonC2.HelperIsolation]::GetCurrentProcess(),[IntPtr]::Zero,[ref]$inJob)
    $buffer=[uint32[]]::new(128);$count=[DaimonC2.HelperIsolation]::GetConsoleProcessList($buffer,128)
    $peers=@($buffer|Where-Object {$_ -gt 0 -and $_ -ne $PID})
    $console=($count -eq 0 -and [DaimonC2.HelperIsolation]::GetConsoleWindow() -eq [IntPtr]::Zero) -or ($count -eq 1 -and $peers.Count -eq 0 -and $buffer[0] -eq $PID)
    $self=Get-Process -Id $PID
    @{pid=$PID;started_utc=$self.StartTime.ToUniversalTime().ToString('o');job_query_ok=$ok;in_job=$inJob
      console_count=$count;console_peers=$peers;console_isolated=$console;runtime_path=$self.Path
      verified=($ok -and -not $inJob -and $console);scope='OS_JOB_AND_CONSOLE_ONLY_NOT_PROVIDER_OR_HOST_FAILURE'}
}
function Assert-C2HelperIsolation {
    $facts=Get-C2HelperIsolation
    if(-not $facts.verified){throw 'Helper isolation unproved: Job membership/shared or unknown console; no boot/fault.'}
    $facts
}
function Assert-C2DedicatedHelper([string]$EntryFile) {
    $facts=Assert-C2HelperIsolation
    $helperArgs=[Environment]::GetCommandLineArgs();$fileIndex=[array]::IndexOf($helperArgs,'-File')
    if($facts.runtime_path -ine (Join-Path $PSHOME 'pwsh.exe') -or $helperArgs -notcontains '-NonInteractive' -or $fileIndex -lt 0 -or
       $fileIndex+1 -ge $helperArgs.Count -or [IO.Path]::GetFullPath($helperArgs[$fileIndex+1]) -ine [IO.Path]::GetFullPath($EntryFile)){
        throw 'Fault requires its own noninteractive pwsh -File helper; never an embedded or interactive app.'
    }
    $facts
}
function Invoke-C2HeartbeatStall([string]$EntryFile,[scriptblock]$Before,[scriptblock]$After) {
    # Fixed duration, current helper thread only. No Stop-Process, service or arbitrary PID input.
    $facts=Assert-C2DedicatedHelper $EntryFile
    & $Before $facts
    [Threading.Thread]::Sleep(30000)
    & $After
}
function Get-C2EmergencyProofVerdict($Fault,$Watchdog,$Deadline) {
    if($Deadline.frequency -le 0 -or $Fault.last_heartbeat_ticks -lt $Deadline.origin_ticks){return 'FAIL_CLOCK'}
    if($Fault.method -cne 'HeartbeatStall30' -or -not $Fault.controller_isolation.verified -or -not $Watchdog.isolation.verified){return 'FAIL_ISOLATION_OR_FAULT_BINDING'}
    if($Fault.vm_id -cne $Watchdog.vm_id -or $Fault.operation_id -cne $Watchdog.operation_id){return 'FAIL_VM_BINDING'}
    if($Fault.controller_isolation.pid -eq $Watchdog.isolation.pid -or $Fault.watchdog_pid -ne $Watchdog.isolation.pid -or
       ([datetime]$Fault.watchdog_started_utc).ToUniversalTime().Ticks -ne ([datetime]$Watchdog.isolation.started_utc).ToUniversalTime().Ticks){return 'FAIL_HELPER_BINDING'}
    if($Watchdog.reason -cne 'controller-heartbeat-lost' -or -not $Watchdog.poweroff_command_issued -or $Watchdog.timing.state_before_stop -cne 'Running') {return 'FAIL_NO_EMERGENCY_POWEROFF'}
    if($Watchdog.temporal_verdict -cne 'OFF_BY_DEADLINE' -or (Get-C2TemporalVerdict $Deadline $Watchdog.timing) -cne 'OFF_BY_DEADLINE' -or -not $Watchdog.timing.confirmed_off){return 'FAIL_OFF_NOT_TIMELY_PROVEN'}
    $t=$Watchdog.timing
    if($t.stop_requested_ticks -lt $Fault.last_heartbeat_ticks -or $t.stop_command_invoked_ticks -lt $Fault.last_heartbeat_ticks -or $t.off_observed_ticks -lt $t.stop_command_invoked_ticks){return 'FAIL_EVENT_ORDER'}
    if(($t.stop_requested_ticks-$Fault.last_heartbeat_ticks)/[double]$Deadline.frequency -gt 14 -or
       ($t.off_observed_ticks-$Fault.last_heartbeat_ticks)/[double]$Deadline.frequency -gt 30){return 'FAIL_INTERVENTION_DEADLINE'}
    if(($t.off_observed_ticks-$Deadline.origin_ticks)/[double]$Deadline.frequency -gt 90){return 'FAIL_ABSOLUTE_DEADLINE'}
    'WATCHDOG_POWEROFF_PROVEN'
}
Export-ModuleMember -Function Get-C2HelperIsolation,Assert-C2HelperIsolation,Assert-C2DedicatedHelper,Invoke-C2HeartbeatStall,Get-C2EmergencyProofVerdict
