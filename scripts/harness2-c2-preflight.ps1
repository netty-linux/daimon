# Read-only. Does not elevate, start services, acquire images or change Hyper-V.
[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$LabRoot,
    [Parameter(Mandatory=$true)][string]$IsoPath,
    [ValidatePattern('^[A-Za-z0-9-]{1,48}$')][string]$VMName = 'daimon-c2-lab',
    [ValidateSet('Hostile','CleanCreate','CleanBoot')][string]$Milestone = 'Hostile'
)
$ErrorActionPreference = 'Stop'
$blocked = [System.Collections.Generic.List[string]]::new()
if (-not [IO.Path]::IsPathRooted($LabRoot) -or -not [IO.Path]::IsPathRooted($IsoPath)) {
    throw 'Explicit absolute local paths required.'
}
$target = [IO.Path]::GetFullPath($LabRoot)
if ($target.StartsWith('\\') -or $target -notmatch '^[A-Za-z]:\\') { throw 'Local volume required.' }
if ($Milestone -ne 'CleanBoot' -and (Test-Path -LiteralPath $target)) { $blocked.Add('lab-directory-exists-use-owned-executor-to-resume') }
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
$build = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'
# These observations never establish consumer enrollment or authorize servicing.
# Select only counts/status; do not emit license IDs, partial keys or accounts.
$esuEntries = $null
$esuLicensed = $null
$esuQuery = 'UNAVAILABLE'
try { if ($Milestone -eq 'Hostile') {
    $esu = @(Get-CimInstance SoftwareLicensingProduct -Filter "Name LIKE '%ESU%'" -OperationTimeoutSec 15 -ErrorAction Stop)
    $esuEntries = $esu.Count
    $esuLicensed = @($esu | Where-Object LicenseStatus -eq 1).Count
    $esuQuery = 'OBSERVED_NOT_ENROLLMENT_PROOF'
} } catch { $blocked.Add('esu-license-query-unavailable') }
$cbsReboot = Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending'
$updateReboot = Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired'
if ($cbsReboot -or $updateReboot) { $blocked.Add('servicing-reboot-indicator-present') }
$freeGiB = [math]::Round(([double]$os.FreePhysicalMemory * 1KB / 1GB), 2)
$volume = Get-Volume -DriveLetter $target.Substring(0,1)
$diskGiB = [math]::Round(($volume.SizeRemaining / 1GB), 2)
if ($Milestone -ne 'CleanCreate' -and $freeGiB -lt 8) { $blocked.Add('available-memory-below-8-GiB') }
if ($diskGiB -lt 120) { $blocked.Add('available-disk-below-120-GiB') }
if ($machine.NumberOfLogicalProcessors -lt 2) { $blocked.Add('insufficient-logical-cpus') }
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) { $blocked.Add('administrative-session-unavailable') }
$vmCount = $null
$switchCount = $null
$inventoryStatus = 'UNAVAILABLE'
try {
    $vms = @(Get-VM -ErrorAction Stop)
    $vmCount = $vms.Count
    if ($Milestone -ne 'CleanBoot' -and @($vms | Where-Object Name -eq $VMName).Count) { $blocked.Add('vm-name-conflict') }
    $switchCount = @(Get-VMSwitch -ErrorAction Stop).Count
    $inventoryStatus = 'OBSERVED_NOT_CREATION_AUTHORITY'
} catch { $blocked.Add('hyper-v-inventory-unavailable') }
$isoPresent = Test-Path -LiteralPath $IsoPath -PathType Leaf
if (-not $isoPresent) { $blocked.Add('iso-not-found-at-explicit-path') }
# These cannot be discharged by an input boolean, checksum or this availability audit.
if ($Milestone -eq 'Hostile') {
    $blocked.Add('windows-security-support-and-patch-review-required')
    $blocked.Add('independent-artifact-authentication-incomplete')
    $blocked.Add('deployment-package-closure-incomplete')
}
if ($Milestone -eq 'CleanBoot') { $blocked.Add('exact-owned-journal-and-vm-configuration-review-required') }
$status = if ($blocked.Count) { 'BLOCKED' } else { 'READY_FOR_SEPARATE_AUTHORIZATION' }
[ordered]@{
    schema_version = 1; status = $status; milestone = $Milestone; administrative_changes = 0
    warning = 'ESU/host trust unverified: clean offline lab only after explicit risk acceptance; hostile execution blocked.'
    iso_authentication = 'Presence only here; executor verifies pinned size/SHA256 and protected copy before VM creation/boot.'
    available_memory_gib = $freeGiB; available_disk_gib = $diskGiB
    logical_cpus = $machine.NumberOfLogicalProcessors; administrator = $admin
    observed_at_utc = [DateTime]::UtcNow.ToString('o')
    windows = [ordered]@{
        caption = $os.Caption; build = ('{0}.{1}' -f $build.CurrentBuild, $build.UBR)
        esu_query = $esuQuery; esu_entry_count = $esuEntries
        esu_licensed_count = $esuLicensed; coverage = 'UNVERIFIED'
        cbs_reboot_indicator = $cbsReboot; update_reboot_indicator = $updateReboot
        online_update_search = 'NOT_RUN'
    }
    hyper_v_inventory = $inventoryStatus
    vm_count = $vmCount; switch_count = $switchCount; iso_present = $isoPresent
    blockers = @($blocked); security_gates = 'unverified'; enforcement = 'NOT_RUN'
} | ConvertTo-Json -Depth 4
if ($blocked.Count) { exit 3 }
exit 0
