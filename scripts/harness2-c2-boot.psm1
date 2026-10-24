# Read-only host sensors and bounded first-boot admission/abort policy.
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
function Initialize-C2HostSensors {
    if (-not ('DaimonC2.HostSensors' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
namespace DaimonC2 {
 public static class HostSensors {
  [StructLayout(LayoutKind.Sequential)] public struct PerformanceInfo {
   public uint Size;
   public UIntPtr CommitTotal, CommitLimit, CommitPeak, PhysicalTotal, PhysicalAvailable;
   public UIntPtr SystemCache, KernelTotal, KernelPaged, KernelNonpaged, PageSize;
   public uint Handles, Processes, Threads;
  }
  [StructLayout(LayoutKind.Sequential)] public struct FileTime { public uint Low, High; public ulong Value => ((ulong)High << 32) | Low; }
  [DllImport("psapi.dll", SetLastError=true)] [return:MarshalAs(UnmanagedType.Bool)]
  static extern bool GetPerformanceInfo(out PerformanceInfo info, uint size);
  [DllImport("kernel32.dll", SetLastError=true)] [return:MarshalAs(UnmanagedType.Bool)]
  static extern bool GetSystemTimes(out FileTime idle, out FileTime kernel, out FileTime user);
  public static PerformanceInfo Memory() {
   PerformanceInfo p; if(!GetPerformanceInfo(out p,(uint)Marshal.SizeOf<PerformanceInfo>())) throw new Win32Exception(Marshal.GetLastWin32Error()); return p;
  }
  public static ulong[] Cpu() {
   FileTime idle,kernel,user; if(!GetSystemTimes(out idle,out kernel,out user)) throw new Win32Exception(Marshal.GetLastWin32Error());
   return new ulong[]{idle.Value,kernel.Value,user.Value};
  }
 }
}
'@
    }
}
function Get-C2NativeSample($PreviousCpu) {
    Initialize-C2HostSensors
    $p=[DaimonC2.HostSensors]::Memory();$cpu=[DaimonC2.HostSensors]::Cpu();$page=[double]$p.PageSize.ToUInt64()
    $load=$null
    if ($PreviousCpu) {
        $total=([double]$cpu[1]-[double]$PreviousCpu[1])+([double]$cpu[2]-[double]$PreviousCpu[2])
        if($total -le 0){throw 'Non-monotonic CPU telemetry.'}
        $load=[math]::Max(0,[math]::Min(100,100*(1-([double]$cpu[0]-[double]$PreviousCpu[0])/$total)))
    }
    @{utc=[DateTime]::UtcNow.ToString('o');available_gib=$p.PhysicalAvailable.ToUInt64()*$page/1GB
      physical_gib=$p.PhysicalTotal.ToUInt64()*$page/1GB;commit_gib=$p.CommitTotal.ToUInt64()*$page/1GB
      commit_limit_gib=$p.CommitLimit.ToUInt64()*$page/1GB;cpu_percent=$load;cpu_ticks=$cpu}
}
function Resolve-C2CounterPath([string]$Object,[string]$Counter) {
    # Names are discovered from registered sets. No lodctr, registry repair or counter enablement.
    $objects=@($Object);$counters=@($Counter)
    if($Object -eq 'Hyper-V Dynamic Memory Balancer'){$objects+='Balanceador de Memória Dinâmica do Hyper-V'}
    switch($Counter){
        'Available Memory For Balancing'{$counters+='Memória Disponível para Balanceamento'}
        'Current System Pressure'{$counters+='Pressão Atual do Sistema'}
    }
    foreach($name in $objects){
        try{$set=Get-Counter -ListSet $name -ErrorAction Stop}catch{continue}
        foreach($candidate in $set.Paths){foreach($counterName in $counters){
            if($candidate.EndsWith('\'+$counterName,[StringComparison]::OrdinalIgnoreCase)){return $candidate}
        }}
    }
    throw 'Observed counter path unavailable in supported English/Portuguese sets.'
}
function Get-C2CounterValue([string]$Path) {
    $sample=Get-Counter -Counter $Path -MaxSamples 1 -ErrorAction Stop
    $valid=@($sample.CounterSamples|Where-Object{$_.Status -eq 0})
    if($valid.Count -ne 1){throw 'Expected one valid host balancer instance.'}
    [double]$valid[0].CookedValue
}
function Get-C2BootBudget([ValidateScript({$_ -in @(2,2.5,3,4)})][double]$GuestMaximumGiB=4) {
    # Separate clean-boot calibration policy; never an adversarial quota or upstream guarantee.
    # The 2/2.5-GiB profiles explicitly cap startup/minimum/maximum; no reclaim assumption.
    $light=$GuestMaximumGiB -lt 3
    $reserve=if($light){3.5}else{4.0}
    @{version=2;profile=('bounded-max'+$GuestMaximumGiB.ToString([cultureinfo]::InvariantCulture));guest_startup_gib=if($light){$GuestMaximumGiB}else{3.0};guest_max_gib=$GuestMaximumGiB
      host_overhead_allowance_gib=0.5;host_reserve_gib=$reserve;required_available_gib=($GuestMaximumGiB+$reserve+0.5)
      required_commit_headroom_gib=($GuestMaximumGiB+2.5);required_balancer_gib=($GuestMaximumGiB+0.5);max_commit_ratio=0.8
      baseline_seconds=120;baseline_min_samples=40;max_baseline_cpu_p95=60.0
      probe_seconds=if($light){90}else{180};watchdog_seconds=if($light){120}else{210};heartbeat_timeout_seconds=10;sample_seconds=2
      low_available_gib=if($light){3.75}else{4.0};low_available_seconds=if($light){2}else{5};critical_available_gib=if($light){3.5}else{2.0}
      abort_commit_ratio=0.9;abort_cpu_percent=85.0;abort_cpu_seconds=10;abort_pagefile_growth_mib=256}
}
function Get-C2BootAssessment($Samples,[double]$ElapsedSeconds,$Capabilities,[ValidateScript({$_ -in @(2,2.5,3,4)})][double]$GuestMaximumGiB=4) {
    $p=Get-C2BootBudget $GuestMaximumGiB;$reasons=[Collections.Generic.List[string]]::new()
    if($Samples.Count -lt $p.baseline_min_samples -or $ElapsedSeconds -lt $p.baseline_seconds){$reasons.Add('baseline-incomplete')}
    if(-not $Capabilities.vm_validated){$reasons.Add('vm-or-journal-validation-incomplete')}
    if(-not $Capabilities.iso_verified){$reasons.Add('iso-verification-incomplete')}
    if(-not $Capabilities.stop_interface){$reasons.Add('poweroff-interface-unavailable')}
    if(-not $Capabilities.events_readable){$reasons.Add('hyper-v-event-read-unavailable')}
    $a=@();$head=@();$ratios=@();$cpus=@();$balances=@();$pagefiles=@()
    foreach($s in $Samples){
        if($null -eq $s.available_gib -or $s.available_gib -lt 0 -or $s.commit_limit_gib -le 0 -or $null -eq $s.commit_gib -or $s.commit_gib -lt 0 -or $null -eq $s.balancer_available_gib -or $null -eq $s.pagefile_used_mib){$reasons.Add('required-telemetry-unavailable');continue}
        $a+=[double]$s.available_gib;$head+=([double]$s.commit_limit_gib-[double]$s.commit_gib)
        $ratios+=([double]$s.commit_gib/[double]$s.commit_limit_gib);$balances+=[double]$s.balancer_available_gib
        $pagefiles+=[double]$s.pagefile_used_mib;if($null -ne $s.cpu_percent){$cpus+=[double]$s.cpu_percent}
    }
    if(-not $a.Count -or $cpus.Count -lt ($p.baseline_min_samples-1)){$reasons.Add('required-telemetry-unavailable')}
    $minimum=if($a.Count){($a|Measure-Object -Minimum).Minimum}else{$null}
    $commitHead=if($head.Count){($head|Measure-Object -Minimum).Minimum}else{$null}
    $balance=if($balances.Count){($balances|Measure-Object -Minimum).Minimum}else{$null}
    $p95=$null
    if($cpus.Count){$sorted=@($cpus|Sort-Object);$p95=$sorted[[math]::Max(0,[math]::Ceiling($sorted.Count*0.95)-1)]}
    $growth=if($pagefiles.Count){($pagefiles|Measure-Object -Maximum).Maximum-$pagefiles[0]}else{$null}
    if($null -ne $minimum -and $minimum -lt $p.required_available_gib){$reasons.Add('physical-budget-insufficient')}
    if($null -ne $commitHead -and $commitHead -lt $p.required_commit_headroom_gib){$reasons.Add('commit-headroom-insufficient')}
    if($null -ne $balance -and $balance -lt $p.required_balancer_gib){$reasons.Add('hyper-v-balancer-headroom-insufficient')}
    if($ratios.Count -and ($ratios|Measure-Object -Maximum).Maximum -gt $p.max_commit_ratio){$reasons.Add('host-commit-pressure')}
    if($null -ne $p95 -and $p95 -gt $p.max_baseline_cpu_p95){$reasons.Add('baseline-cpu-pressure')}
    if($null -ne $growth -and $growth -gt 128){$reasons.Add('baseline-pagefile-growth')}
    @{status=if($reasons.Count){'BLOCKED'}else{'GO_FOR_SEPARATE_AUTHORIZATION'};reasons=@($reasons|Sort-Object -Unique)
      samples=$Samples.Count;elapsed_seconds=$ElapsedSeconds;minimum_available_gib=$minimum
      minimum_commit_headroom_gib=$commitHead;minimum_balancer_gib=$balance;cpu_p95=$p95
      pagefile_growth_mib=$growth;paging_io_rate_available=$Capabilities.paging_io_rate
      configuration_change_required=($GuestMaximumGiB -ne 4)
      scope=('ONE_OFF_'+$p.probe_seconds+'S_STOCK_ISO_BOOT_ONLY_NO_INSTALL_OR_WORKLOAD');budget=$p}
}
function Wait-C2WatchdogLease([scriptblock]$ReadHeartbeat,[scriptblock]$Sample,[scriptblock]$Stop,
    [hashtable]$Policy,[ValidateRange(20,2000)][int]$PollMilliseconds=2000) {
    # Shared by the real VM watchdog and real-process offline smoke. No simulated clock.
    $clock=[Diagnostics.Stopwatch]::StartNew();$reason=''
    try {
        do {
            $h=& $ReadHeartbeat
            if($h.phase -notin 'arming','running','done','unresolved'){throw 'Invalid heartbeat phase.'}
            $age=([DateTime]::UtcNow-([datetime]$h.utc).ToUniversalTime()).TotalSeconds
            if($age -lt -2){throw 'Future heartbeat.'}
            if($h.phase -eq 'done'){return 'controller-finished'}
            if($age -gt $Policy.heartbeat_timeout_seconds){$reason='controller-heartbeat-lost';break}
            $s=& $Sample
            if($null -eq $s.available_gib -or $s.commit_limit_gib -le 0 -or $null -eq $s.commit_gib){throw 'Invalid watchdog telemetry.'}
            if($s.available_gib -lt $Policy.critical_available_gib){$reason='watchdog-critical-memory';break}
            if($s.commit_gib/$s.commit_limit_gib -ge $Policy.abort_commit_ratio){$reason='watchdog-commit-pressure';break}
            if($clock.Elapsed.TotalSeconds -ge $Policy.watchdog_seconds){$reason='watchdog-deadline';break}
            Start-Sleep -Milliseconds $PollMilliseconds
        }while($true)
    }catch{$reason='watchdog-input-failed'}
    & $Stop
    $reason
}
function Get-C2ProbeAbortReason($Sample,$State,[double]$Seconds,$Policy,$Vm) {
    if(-not $State.telemetry_ok){return 'telemetry-unavailable'}
    if($Seconds -ge $Policy.probe_seconds){return 'bounded-probe-complete'}
    if(-not $State.ownership_ok){return 'ownership-drift'}
    if(-not $State.control_acl_ok){return 'control-domain-drift'}
    if($Vm.network_adapters -ne 0 -or $Vm.assigned_gib -gt $Policy.guest_max_gib){return 'vm-resource-drift'}
    if($Sample.available_gib -lt $Policy.critical_available_gib){return 'critical-host-memory'}
    if($Sample.commit_limit_gib -le 0 -or $Sample.commit_gib/$Sample.commit_limit_gib -ge $Policy.abort_commit_ratio){return 'host-commit-pressure'}
    if($State.low_memory_seconds -ge $Policy.low_available_seconds){return 'low-host-memory'}
    if($State.high_cpu_seconds -ge $Policy.abort_cpu_seconds){return 'host-cpu-pressure'}
    if($State.pagefile_growth_mib -ge $Policy.abort_pagefile_growth_mib){return 'pagefile-growth'}
    $null
}
function Invoke-C2ProbeRollback([hashtable]$Adapter,[switch]$ReconcileJournal) {
    # The real adapter authenticates the captured VM reference on EVERY action. No PID/name-wide kills.
    & $Adapter.Stop
    if((& $Adapter.State) -ne 'Off'){throw 'Poweroff not verified; no restore or journal reconciliation.'}
    & $Adapter.Restore
    if($ReconcileJournal){& $Adapter.Journal;return 'OFF_MAX4_CONFIGURED'}
    'OFF_MAX4_JOURNAL_PENDING'
}
Export-ModuleMember -Function Get-C2NativeSample,Resolve-C2CounterPath,Get-C2CounterValue,Get-C2BootBudget,Get-C2BootAssessment,Get-C2ProbeAbortReason,Invoke-C2ProbeRollback,Wait-C2WatchdogLease
