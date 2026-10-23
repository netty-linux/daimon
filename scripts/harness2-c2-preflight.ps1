# Read-only. Does not elevate, start services, acquire images or change Hyper-V.
[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$LabRoot,
    [Parameter(Mandatory=$true)][string]$IsoPath,
    [ValidatePattern('^[A-Za-z0-9-]{1,48}$')][string]$VMName = 'daimon-c2-lab'
)
$ErrorActionPreference = 'Stop'
$blocked = [System.Collections.Generic.List[string]]::new()
if (-not [IO.Path]::IsPathRooted($LabRoot) -or -not [IO.Path]::IsPathRooted($IsoPath)) {
    throw 'Explicit absolute local paths required.'
}
$target = [IO.Path]::GetFullPath($LabRoot)
if ($target.StartsWith('\\') -or $target -notmatch '^[A-Za-z]:\\') { throw 'Local volume required.' }
if (Test-Path -LiteralPath $target) { $blocked.Add('lab-directory-exists') }
# Reject existing reparse points anywhere in the future directory ancestry.
$ancestor = $target
while ($ancestor) {
    if (Test-Path -LiteralPath $ancestor) {
        if ((Get-Item -LiteralPath $ancestor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
            $blocked.Add('lab-ancestry-reparse-point')
        }
    }
    $parent = [IO.Path]::GetDirectoryName($ancestor)
    if ($parent -eq $ancestor) { break }
    $ancestor = $parent
}
$os = Get-CimInstance Win32_OperatingSystem
$machine = Get-CimInstance Win32_ComputerSystem
$freeGiB = [math]::Round(([double]$os.FreePhysicalMemory * 1KB / 1GB), 2)
$volume = Get-Volume -DriveLetter $target.Substring(0,1)
$diskGiB = [math]::Round(($volume.SizeRemaining / 1GB), 2)
if ($freeGiB -lt 8) { $blocked.Add('available-memory-below-8-GiB') }
if ($diskGiB -lt 120) { $blocked.Add('available-disk-below-120-GiB') }
if ($machine.NumberOfLogicalProcessors -lt 2) { $blocked.Add('insufficient-logical-cpus') }
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) { $blocked.Add('administrative-session-unavailable') }
$vmCount = $null
$switchCount = $null
try {
    $vms = @(Get-VM -ErrorAction Stop)
    $vmCount = $vms.Count
    if (@($vms | Where-Object Name -eq $VMName).Count) { $blocked.Add('vm-name-conflict') }
    $switchCount = @(Get-VMSwitch -ErrorAction Stop).Count
} catch { $blocked.Add('hyper-v-inventory-unavailable') }
$isoPresent = Test-Path -LiteralPath $IsoPath -PathType Leaf
if (-not $isoPresent) { $blocked.Add('iso-not-found-at-explicit-path') }
# These cannot be discharged by an input boolean, checksum or this availability audit.
$blocked.Add('windows-security-support-and-patch-review-required')
$blocked.Add('independent-artifact-authentication-incomplete')
$blocked.Add('deployment-package-closure-incomplete')
[ordered]@{
    schema_version = 1; status = 'BLOCKED'; administrative_changes = 0
    available_memory_gib = $freeGiB; available_disk_gib = $diskGiB
    logical_cpus = $machine.NumberOfLogicalProcessors; administrator = $admin
    vm_count = $vmCount; switch_count = $switchCount; iso_present = $isoPresent
    blockers = @($blocked); security_gates = 'unverified'; enforcement = 'NOT_RUN'
} | ConvertTo-Json -Depth 4
exit 3
