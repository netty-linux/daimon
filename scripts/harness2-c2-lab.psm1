# Clean/offline Hyper-V lab only; no guest commands, installation or host servicing.
# Windows, PowerShell 7.4+. Administrators/SYSTEM remain trusted authorities.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$script:IsoHash = '97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8'
$script:IsoSize = 4080486400L

function Get-C2Path([string]$Path) {
    if ($Path -notmatch '^[A-Za-z]:\\' -or $Path.Substring(2).Contains(':') -or $Path -match '[*?\[\]]') { throw 'Explicit local drive path required.' }
    $p = $Path.TrimEnd('\')
    if ($p.Length -le 3) { throw 'Volume root forbidden.' }
    foreach ($part in $p.Substring(3).Split('\')) {
        if (-not $part -or $part -in '.', '..' -or $part -match '[. ]$' -or
            $part -match '^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])(\.|$)') { throw 'Unsafe path component.' }
    }
    [IO.Path]::GetFullPath($p)
}

function Assert-C2NoLinks([string]$Path) {
    $p = Get-C2Path $Path
    while ($p) {
        if (Test-Path -LiteralPath $p) {
            if ((Get-Item -Force -LiteralPath $p).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Reparse point forbidden.' }
        }
        $p = [IO.Path]::GetDirectoryName($p)
    }
}

function Get-C2Contract([string]$Root, [string]$IsoPath, [guid]$OperationId) {
    $r = Get-C2Path $Root
    $iso = Get-C2Path $IsoPath
    if ($OperationId -eq [guid]::Empty) { throw 'Nonzero operation ID required.' }
    if ($iso.StartsWith($r + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Source ISO must be outside new root.' }
    [ordered]@{
        version = 2; purpose = 'clean-offline-lab-only'; operation_id = $OperationId.ToString()
        host_security = 'UNVERIFIED_NOT_FOR_HOSTILE_USE'; guest_installation = 'NOT_AUTHORIZED'
        root = $r; source_iso = $iso; iso = [IO.Path]::Combine($r, 'installation.iso')
        disk = [IO.Path]::Combine($r, 'lab.vhdx'); config = [IO.Path]::Combine($r, 'vm')
        iso_sha256 = $script:IsoHash; vm_name = 'daimon-c2-' + $OperationId.ToString('N')
        vm_id = $null; disk_id = $null; state = 'intent'
    }
}

function Assert-C2Journal($Journal, $Contract) {
    $states = @('intent','iso-pending','iso-ready','disk-pending','disk-ready','vm-pending',
        'vm-recorded','configured','start-pending','running','vm-remove-pending','vm-removed',
        'disk-remove-pending','disk-removed','iso-remove-pending','discarded')
    if ($Journal.Count -ne $Contract.Count -or $Journal.state -notin $states) { throw 'Invalid journal shape/state.' }
    foreach ($key in $Contract.Keys) {
        if (-not $Journal.Contains($key)) { throw 'Missing journal field.' }
        if ($key -notin 'vm_id','disk_id','state' -and $Journal[$key] -cne $Contract[$key]) { throw 'Journal contract mismatch.' }
    }
    foreach ($key in 'vm_id','disk_id') {
        if ($null -ne $Journal[$key] -and (([guid]$Journal[$key]).ToString() -cne $Journal[$key] -or [guid]$Journal[$key] -eq [guid]::Empty)) { throw 'Invalid resource ID.' }
    }
    if ($Journal.state -in @('disk-ready','vm-pending','vm-recorded','configured','start-pending','running','vm-remove-pending') -and -not $Journal.disk_id) { throw 'Disk identity missing.' }
    if ($Journal.state -in @('vm-recorded','configured','start-pending','running','vm-remove-pending') -and -not $Journal.vm_id) { throw 'VM identity missing.' }
}

function New-C2Acl {
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $acl.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
    foreach ($sid in 'S-1-5-32-544','S-1-5-18') {
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new(
            [Security.Principal.SecurityIdentifier]::new($sid), 'FullControl',
            'ContainerInherit, ObjectInherit', 'None', 'Allow'))
    }
    $acl
}

function New-C2Directory([string]$Path) {
    # CreateDirectoryW fails on an existing path; unlike mkdir it cannot adopt a raced directory.
    if (-not ('DaimonC2.NativeDirectory' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
namespace DaimonC2 {
    public static class NativeDirectory {
        [StructLayout(LayoutKind.Sequential)]
        private struct SecurityAttributes {
            public int Length;
            public IntPtr Descriptor;
            [MarshalAs(UnmanagedType.Bool)] public bool Inherit;
        }
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        private static extern bool CreateDirectoryW(string path, ref SecurityAttributes attributes);
        public static void Create(string path, byte[] descriptor) {
            var pin = GCHandle.Alloc(descriptor, GCHandleType.Pinned);
            try {
                var attrs = new SecurityAttributes {
                    Length = Marshal.SizeOf<SecurityAttributes>(),
                    Descriptor = pin.AddrOfPinnedObject(), Inherit = false
                };
                if (!CreateDirectoryW(path, ref attrs)) throw new Win32Exception(Marshal.GetLastWin32Error());
            } finally { pin.Free(); }
        }
    }
}
'@
    }
    try { [DaimonC2.NativeDirectory]::Create($Path, (New-C2Acl).GetSecurityDescriptorBinaryForm()) }
    catch {
        if ($_.Exception.InnerException -is [ComponentModel.Win32Exception] -and $_.Exception.InnerException.NativeErrorCode -eq 183) { throw 'Approved root already exists.' }
        throw
    }
}

function New-C2File([string]$Path) {
    $acl = [Security.AccessControl.FileSecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $acl.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
    foreach ($sid in 'S-1-5-32-544','S-1-5-18') {
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new(
            [Security.Principal.SecurityIdentifier]::new($sid), 'FullControl', 'Allow'))
    }
    [IO.FileSystemAclExtensions]::Create([IO.FileInfo]::new($Path), [IO.FileMode]::CreateNew,
        [Security.AccessControl.FileSystemRights]::FullControl, [IO.FileShare]::None,
        4096, [IO.FileOptions]::WriteThrough, $acl)
}

function Assert-C2Acl([string]$Path, [switch]$Directory) {
    Assert-C2NoLinks $Path
    $acl = Get-Acl -LiteralPath $Path
    if ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin 'S-1-5-32-544','S-1-5-18') { throw 'Untrusted owner.' }
    if ($Directory -and -not $acl.AreAccessRulesProtected) { throw 'Root DACL must be protected.' }
    $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
    if ($rules.Count -ne 2) { throw 'Unexpected journal/root access rules.' }
    foreach ($sid in 'S-1-5-32-544','S-1-5-18') {
        $rule = @($rules | Where-Object { $_.IdentityReference.Value -eq $sid })
        if ($rule.Count -ne 1 -or $rule[0].AccessControlType -ne 'Allow' -or
            $rule[0].FileSystemRights -ne [Security.AccessControl.FileSystemRights]::FullControl -or
            $rule[0].PropagationFlags -ne 'None' -or
            ($Directory -and $rule[0].InheritanceFlags -ne 'ContainerInherit, ObjectInherit')) { throw 'Unexpected journal/root permissions.' }
    }
}

function Get-C2MediaAcl($j, [string]$Kind) {
    $acl = [Security.AccessControl.FileSecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $acl.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
    foreach ($sid in 'S-1-5-32-544','S-1-5-18') {
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new(
            [Security.Principal.SecurityIdentifier]::new($sid), 'FullControl', 'Allow'))
    }
    $vmSid = [Security.Principal.NTAccount]::new('NT VIRTUAL MACHINE', $j.vm_id).Translate([Security.Principal.SecurityIdentifier])
    $rights = if ($Kind -eq 'iso') { 'Read' } elseif ($Kind -eq 'disk') { 'FullControl' } else { throw 'Unknown media role.' }
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($vmSid, $rights, 'Allow'))
    $acl
}

function Assert-C2MediaAcl($j, [string]$Kind) {
    $path = $j[$Kind]
    if (-not $j.vm_id) { Assert-C2Acl $path; return }
    Assert-C2NoLinks $path
    $actual = Get-Acl -LiteralPath $path
    $expected = Get-C2MediaAcl $j $Kind
    # Ignore SACL; compare owner/group-independent DACL and owner separately.
    if ($actual.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne 'S-1-5-32-544' -or
        $actual.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access) -cne
        $expected.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access)) { throw 'Media access rules changed.' }
}

function ConvertFrom-C2JournalText([string]$raw, $c) {
    if ([Text.Encoding]::UTF8.GetByteCount($raw) -gt 65536) { throw 'Journal too large.' }
    $doc = [Text.Json.JsonDocument]::Parse($raw)
    try {
        if ($doc.RootElement.ValueKind -ne 'Object') { throw 'Journal must be an object.' }
        $keys = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
        foreach ($property in $doc.RootElement.EnumerateObject()) {
            if (-not $keys.Add($property.Name)) { throw 'Duplicate journal key.' }
        }
    } finally { $doc.Dispose() }
    $journal = ConvertFrom-Json -InputObject $raw -AsHashtable
    Assert-C2Journal $journal $c
    $journal
}

function Read-C2Journal($c) {
    $path = Join-Path $c.root 'journal.json'
    Assert-C2Acl $path
    if ((Get-Item -LiteralPath $path).Length -gt 65536) { throw 'Journal too large.' }
    ConvertFrom-C2JournalText ([IO.File]::ReadAllText($path)) $c
}

function Write-C2Journal($j) {
    Assert-C2Journal $j (Get-C2Contract $j.root $j.source_iso ([guid]$j.operation_id))
    Assert-C2Acl $j.root -Directory
    $path = Join-Path $j.root 'journal.json'
    $tmp = Join-Path $j.root ('journal-' + [guid]::NewGuid().ToString('N') + '.tmp')
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($j | ConvertTo-Json -Compress))
    $stream = New-C2File $tmp
    try { $stream.Write($bytes, 0, $bytes.Length); $stream.Flush($true) } finally { $stream.Dispose() }
    Assert-C2Acl $tmp
    # PowerShell marshals $null as an empty string for this overload; NullString is a real null backup path.
    if (Test-Path -LiteralPath $path) { Assert-C2Acl $path; [IO.File]::Replace($tmp, $path, [System.Management.Automation.Language.NullString]::Value) }
    else { [IO.File]::Move($tmp, $path) }
    Assert-C2Acl $path
}

function Assert-C2Iso([string]$Path) {
    Assert-C2NoLinks $Path
    $f = [IO.FileStream]::new($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        if ($f.Length -ne $script:IsoSize) { throw 'ISO size mismatch.' }
        $hash = [Security.Cryptography.SHA256]::Create()
        try { $digest = [Convert]::ToHexString($hash.ComputeHash($f)).ToLowerInvariant() } finally { $hash.Dispose() }
        if ($digest -cne $script:IsoHash) { throw 'ISO digest mismatch.' }
    } finally { $f.Dispose() }
}

function Assert-C2Host([string]$Mode, $c) {
    if (-not $IsWindows -or $PSVersionTable.PSVersion -lt [version]'7.4') { throw 'Windows PowerShell 7.4+ required.' }
    $p = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Elevated administrator required; no automatic elevation.' }
    Assert-C2NoLinks $c.root
    $vol = Get-Volume -DriveLetter $c.root.Substring(0,1) -ErrorAction Stop
    if ($vol.FileSystemType -ne 'NTFS') { throw 'Local NTFS volume required.' }
    if ($Mode -eq 'Create' -and $vol.SizeRemaining -lt 120GB) { throw '120 GiB free disk required.' }
    $vms = @(Get-VM -ErrorAction Stop)
    if ($Mode -eq 'Create' -and -not (Test-Path -LiteralPath $c.root) -and
        @($vms | Where-Object Name -eq $c.vm_name).Count) { throw 'VM name conflict before allocation; no adoption.' }
}

function Get-C2VM($j, [switch]$AllowAbsent) {
    Assert-C2NoLinks $j.config
    $all = @(Get-VM -ErrorAction Stop)
    $matches = @($all | Where-Object { $_.Id.ToString() -eq $j.vm_id })
    if ($matches.Count -eq 0 -and $AllowAbsent) { return $null }
    if ($matches.Count -ne 1) { throw 'Exact owned VM unavailable.' }
    $vm = $matches[0]
    if ($vm.Name -cne $j.vm_name -or $vm.Generation -ne 2 -or (Get-C2Path $vm.ConfigurationLocation) -ine $j.config) { throw 'VM ownership/location mismatch.' }
    foreach ($path in @($vm.SmartPagingFilePath, $vm.SnapshotFileLocation)) {
        $canonical = Get-C2Path $path
        if ($canonical -ine $j.config -and -not $canonical.StartsWith($j.config + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'VM auxiliary path outside owned configuration root.' }
        Assert-C2NoLinks $canonical
    }
    $vm
}

function Assert-C2Disk($j) {
    Assert-C2NoLinks $j.disk
    $disk = Get-VHD -Path $j.disk -ErrorAction Stop
    if ($disk.DiskIdentifier.ToString() -ne $j.disk_id -or $disk.VhdType -ne 'Dynamic' -or
        $disk.VhdFormat -ne 'VHDX' -or $disk.Size -ne 48GB -or $disk.ParentPath) { throw 'Disk identity/layout mismatch.' }
    $disk
}

function Assert-C2ExclusiveDisk($j, [switch]$AllowOwned) {
    foreach ($vm in @(Get-VM -ErrorAction Stop)) {
        $refs = @(Get-VMHardDiskDrive -VM $vm -ErrorAction Stop)
        foreach ($snapshot in @(Get-VMSnapshot -VM $vm -ErrorAction Stop)) {
            $refs += @(Get-VMHardDiskDrive -VMSnapshot $snapshot -ErrorAction Stop)
        }
        foreach ($ref in $refs) {
            if ($ref.Path -and (Get-C2Path $ref.Path) -ieq $j.disk) {
                if (-not $AllowOwned -or $vm.Id.ToString() -ne $j.vm_id) { throw 'Disk has another live/checkpoint reference.' }
            }
        }
    }
}

function Assert-C2ExclusiveIso($j, [switch]$AllowOwned) {
    foreach ($vm in @(Get-VM -ErrorAction Stop)) {
        $refs = @(Get-VMDvdDrive -VM $vm -ErrorAction Stop)
        foreach ($snapshot in @(Get-VMSnapshot -VM $vm -ErrorAction Stop)) {
            $refs += @(Get-VMDvdDrive -VMSnapshot $snapshot -ErrorAction Stop)
        }
        foreach ($ref in $refs) {
            if ($ref.Path -and (Get-C2Path $ref.Path) -ieq $j.iso -and
                (-not $AllowOwned -or $vm.Id.ToString() -ne $j.vm_id)) { throw 'ISO has another live/checkpoint reference.' }
        }
    }
}

function Assert-C2VM($j, [switch]$Complete, [switch]$Running) {
    $vm = Get-C2VM $j
    $expected = if ($Running) { 'Running' } else { 'Off' }
    if ($vm.State -ne $expected) { throw "Owned VM must be $expected." }
    if (@(Get-VMSnapshot -VM $vm -ErrorAction Stop).Count) { throw 'Checkpoints forbid automated cleanup.' }
    $disks = @(Get-VMHardDiskDrive -VM $vm -ErrorAction Stop)
    if ($disks.Count -gt 1 -or ($disks.Count -and $disks[0].Path -ine $j.disk)) { throw 'Unexpected disk attachment.' }
    $dvds = @(Get-VMDvdDrive -VM $vm -ErrorAction Stop)
    if ($dvds.Count -gt 1 -or ($dvds.Count -and $dvds[0].Path -and $dvds[0].Path -ine $j.iso)) { throw 'Unexpected DVD attachment.' }
    if ($Complete) {
        $mem = Get-VMMemory -VM $vm -ErrorAction Stop
        $cpu = Get-VMProcessor -VM $vm -ErrorAction Stop
        $fw = Get-VMFirmware -VM $vm -ErrorAction Stop
        $boot = @($fw.BootOrder)
        if ($vm.Notes -cne ('DAIMON-C2:' + $j.operation_id) -or $disks.Count -ne 1 -or
            $dvds.Count -ne 1 -or $dvds[0].Path -ine $j.iso -or
            @(Get-VMNetworkAdapter -VM $vm -ErrorAction Stop).Count -ne 0 -or
            $vm.AutomaticStartAction -ne 'Nothing' -or $vm.AutomaticStopAction -ne 'TurnOff' -or $vm.AutomaticCheckpointsEnabled -or
            $vm.CheckpointType -ne 'Disabled' -or $cpu.Count -ne 2 -or $cpu.ExposeVirtualizationExtensions -or
            -not $mem.DynamicMemoryEnabled -or $mem.Minimum -ne 3GB -or $mem.Startup -ne 3GB -or $mem.Maximum -ne 4GB -or
            $fw.SecureBoot -ne 'On' -or $fw.SecureBootTemplate -ne 'MicrosoftUEFICertificateAuthority' -or
            $boot.Count -lt 1 -or $boot[0].BootType -ne 'Drive' -or $null -eq $boot[0].Device -or $boot[0].Device.Path -ine $j.iso -or
            @(Get-VMIntegrationService -VM $vm -ErrorAction Stop | Where-Object Enabled).Count) { throw 'Clean-lab configuration drift.' }
    }
    $null = Assert-C2Disk $j
    Assert-C2ExclusiveDisk $j -AllowOwned
    Assert-C2ExclusiveIso $j -AllowOwned
    $vm
}

function New-C2Backend {
    @{
        Host = { param($mode,$c) Assert-C2Host $mode $c }
        Memory = {
            $os = Get-CimInstance Win32_OperatingSystem -OperationTimeoutSec 15
            if ([double]$os.FreePhysicalMemory * 1KB -lt 8GB) { throw '8 GiB available RAM required before boot.' }
        }
        Open = { param($c,$create)
            $fresh = -not (Test-Path -LiteralPath $c.root)
            if ($fresh) {
                if (-not $create -or -not (Test-Path -LiteralPath ([IO.Path]::GetDirectoryName($c.root)) -PathType Container)) { throw 'Existing parent/new approved root required.' }
                Assert-C2NoLinks $c.root
                New-C2Directory $c.root
            }
            Assert-C2Acl $c.root -Directory
            $lockPath = Join-Path $c.root 'operation.lock'
            if (Test-Path -LiteralPath $lockPath) {
                Assert-C2Acl $lockPath
                $lock = [IO.FileStream]::new($lockPath, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
            } else { $lock = New-C2File $lockPath }
            try {
                Assert-C2Acl $c.root -Directory
                if (-not $fresh -and -not (Test-Path -LiteralPath (Join-Path $c.root 'journal.json'))) { throw 'Unresolved root without journal; no adoption.' }
                @{ Lock = $lock; Fresh = $fresh }
            } catch { $lock.Dispose(); throw }
        }
        Read = { param($c) Read-C2Journal $c }
        Write = { param($j) Write-C2Journal $j }
        Iso = { param($j)
            Assert-C2Iso $j.source_iso
            Assert-C2Acl $j.root -Directory
            if (Test-Path -LiteralPath $j.iso) { throw 'ISO copy already exists; no overwrite.' }
            $src = [IO.FileStream]::new($j.source_iso, 'Open', 'Read', 'Read')
            try {
                $dst = New-C2File $j.iso
                try { $src.CopyTo($dst); $dst.Flush($true) } finally { $dst.Dispose() }
            } finally { $src.Dispose() }
            Assert-C2Acl $j.iso
            Assert-C2Iso $j.iso
        }
        VerifyIso = { param($j)
            # Before attachment: Admin/SYSTEM only. After attachment: also the exact VM SID, read-only.
            try { Assert-C2Acl $j.iso } catch { Assert-C2MediaAcl $j 'iso' }
            Assert-C2Iso $j.iso
        }
        Disk = { param($j)
            Assert-C2NoLinks $j.disk
            if (Test-Path -LiteralPath $j.disk) { throw 'Disk path already exists.' }
            $vhd = New-VHD -Path $j.disk -Dynamic -SizeBytes 48GB -ErrorAction Stop
            $vhd.DiskIdentifier.ToString()
        }
        VM = { param($j)
            if (@(Get-VM -ErrorAction Stop | Where-Object Name -eq $j.vm_name).Count) { throw 'VM name conflict; no adoption.' }
            Assert-C2NoLinks $j.config
            if (Test-Path -LiteralPath $j.config) { throw 'New configuration directory required; no adoption.' }
            $vm = New-VM -Name $j.vm_name -Generation 2 -MemoryStartupBytes 3GB -NoVHD -Path $j.config -ErrorAction Stop
            $vm.Id.ToString()
        }
        Configure = { param($j)
            $vm = Assert-C2VM $j
            Set-VM -VM $vm -Notes ('DAIMON-C2:' + $j.operation_id) -AutomaticStartAction Nothing -AutomaticStopAction TurnOff -AutomaticCheckpointsEnabled $false -CheckpointType Disabled -SmartPagingFilePath $j.config -SnapshotFileLocation $j.config -ErrorAction Stop
            Set-VMProcessor -VM $vm -Count 2 -ExposeVirtualizationExtensions $false -ErrorAction Stop
            Set-VMMemory -VM $vm -DynamicMemoryEnabled $true -MinimumBytes 3GB -StartupBytes 3GB -MaximumBytes 4GB -ErrorAction Stop
            foreach ($nic in @(Get-VMNetworkAdapter -VM $vm -ErrorAction Stop)) {
                if ($nic.SwitchId -and $nic.SwitchId -ne [guid]::Empty) { throw 'Unexpected connected NIC; manual review required.' }
                Remove-VMNetworkAdapter -VMNetworkAdapter $nic -Confirm:$false -ErrorAction Stop
            }
            if (-not @(Get-VMHardDiskDrive -VM $vm -ErrorAction Stop).Count) { Add-VMHardDiskDrive -VM $vm -Path $j.disk -ErrorAction Stop }
            $dvds = @(Get-VMDvdDrive -VM $vm -ErrorAction Stop)
            if (-not $dvds.Count) { Add-VMDvdDrive -VM $vm -Path $j.iso -ErrorAction Stop }
            elseif (-not $dvds[0].Path) { Set-VMDvdDrive -VMDvdDrive $dvds[0] -Path $j.iso -ErrorAction Stop }
            $dvd = @(Get-VMDvdDrive -VM $vm -ErrorAction Stop)[0]
            # Permissions only on this operation's two exact files, never on an existing VM/root.
            Set-Acl -LiteralPath $j.disk -AclObject (Get-C2MediaAcl $j 'disk') -ErrorAction Stop
            Set-Acl -LiteralPath $j.iso -AclObject (Get-C2MediaAcl $j 'iso') -ErrorAction Stop
            Set-VMFirmware -VM $vm -EnableSecureBoot On -SecureBootTemplate MicrosoftUEFICertificateAuthority -FirstBootDevice $dvd -ErrorAction Stop
            foreach ($service in @(Get-VMIntegrationService -VM $vm -ErrorAction Stop)) { Disable-VMIntegrationService -VMIntegrationService $service -ErrorAction Stop }
        }
        Validate = { param($j,$running)
            Assert-C2MediaAcl $j 'iso'; Assert-C2MediaAcl $j 'disk'
            $null = Assert-C2VM $j -Complete -Running:$running
        }
        Start = { param($j) $vm = Assert-C2VM $j -Complete; Start-VM -VM $vm -ErrorAction Stop }
        RemoveVM = { param($j)
            $vm = Get-C2VM $j -AllowAbsent
            if ($null -ne $vm) { $null = Assert-C2VM $j; Remove-VM -VM $vm -Force -Confirm:$false -ErrorAction Stop }
            if ($null -ne (Get-C2VM $j -AllowAbsent)) { throw 'VM removal unresolved.' }
        }
        RemoveDisk = { param($j)
            Assert-C2ExclusiveDisk $j
            if (Test-Path -LiteralPath $j.disk) {
                if (-not $j.disk_id) { throw 'Unrecorded disk identity; manual reconciliation required.' }
                $disk = Assert-C2Disk $j
                if ($disk.Attached) { throw 'Disk still attached.' }
                Remove-Item -LiteralPath $j.disk -Force -ErrorAction Stop
            }
        }
        RemoveIso = { param($j)
            Assert-C2Acl $j.root -Directory
            Assert-C2NoLinks $j.iso
            Assert-C2ExclusiveIso $j
            if (Test-Path -LiteralPath $j.iso) { Remove-Item -LiteralPath $j.iso -Force -ErrorAction Stop }
        }
        Close = { param($handle) $handle.Lock.Dispose() }
    }
}

function Invoke-C2Lab {
    [CmdletBinding()]
    param(
        [ValidateSet('Create','Start','Discard','Inspect')][string]$Mode,
        [string]$Root, [string]$IsoPath, [guid]$OperationId,
        [string]$Authorization, [string]$ExpectedVMId, [hashtable]$Backend
    )
    $c = Get-C2Contract $Root $IsoPath $OperationId
    # Operator acknowledgement, not a human-approval capability. Obtain specific approval before calling.
    if ($Mode -ne 'Inspect' -and $Authorization -cne ($Mode.ToUpperInvariant() + ':' + $c.operation_id)) { throw 'Exact action acknowledgement required.' }
    if (-not $Backend) { $Backend = New-C2Backend }
    & $Backend.Host $Mode $c
    $handle = & $Backend.Open $c ($Mode -eq 'Create')
    try {
        if ($handle.Fresh) { $j = $c; & $Backend.Write $j }
        else { $j = & $Backend.Read $c; Assert-C2Journal $j $c }
        if ($Mode -eq 'Inspect') { return $j }
        if ($Mode -eq 'Create') {
            switch ($j.state) {
                'intent' { $j.state = 'iso-pending'; & $Backend.Write $j; & $Backend.Iso $j; $j.state = 'iso-ready'; & $Backend.Write $j }
                'iso-pending' { throw 'Interrupted ISO acquisition: discard partial operation, do not overwrite.' }
            }
            if ($j.state -eq 'iso-ready') {
                & $Backend.VerifyIso $j
                $j.state = 'disk-pending'; & $Backend.Write $j
                $j.disk_id = & $Backend.Disk $j
                $j.state = 'disk-ready'; & $Backend.Write $j
            }
            if ($j.state -eq 'disk-ready') {
                $j.state = 'vm-pending'; & $Backend.Write $j
                $j.vm_id = & $Backend.VM $j
                $j.state = 'vm-recorded'; & $Backend.Write $j
            }
            if ($j.state -eq 'vm-recorded') {
                & $Backend.VerifyIso $j; & $Backend.Configure $j; & $Backend.Validate $j $false
                $j.state = 'configured'; & $Backend.Write $j
            }
            if ($j.state -ne 'configured') { throw 'Unresolved creation state; no blind retry/adoption.' }
            & $Backend.Validate $j $false
        } elseif ($Mode -eq 'Start') {
            if ($j.state -notin 'configured','start-pending','running') { throw 'Creation must be complete before boot.' }
            if ($ExpectedVMId -cne $j.vm_id) { throw 'Exact expected VM ID required.' }
            & $Backend.VerifyIso $j
            if ($j.state -eq 'configured') {
                & $Backend.Memory
                & $Backend.Validate $j $false
                $j.state = 'start-pending'; & $Backend.Write $j; & $Backend.Start $j
            }
            # Interrupted Start is observed, never blindly retried; Validate(true) requires Running.
            & $Backend.Validate $j $true
            $j.state = 'running'; & $Backend.Write $j
        } elseif ($Mode -eq 'Discard') {
            if ($j.state -eq 'discarded') { return $j }
            if ($j.state -in 'disk-pending','vm-pending') { throw 'Unrecorded resource creation; manual reconciliation required. Nothing deleted.' }
            if ($j.vm_id -and $ExpectedVMId -cne $j.vm_id) { throw 'Exact expected VM ID required.' }
            if ($j.vm_id -and $j.state -notin 'vm-removed','disk-remove-pending','disk-removed','iso-remove-pending') {
                $j.state = 'vm-remove-pending'; & $Backend.Write $j; & $Backend.RemoveVM $j
                $j.state = 'vm-removed'; & $Backend.Write $j
            }
            if ($j.state -notin 'disk-removed','iso-remove-pending') {
                $j.state = 'disk-remove-pending'; & $Backend.Write $j; & $Backend.RemoveDisk $j
                $j.state = 'disk-removed'; & $Backend.Write $j
            }
            $j.state = 'iso-remove-pending'; & $Backend.Write $j; & $Backend.RemoveIso $j
            $j.state = 'discarded'; & $Backend.Write $j
        }
        $j
    } finally { & $Backend.Close $handle }
}

Export-ModuleMember -Function Invoke-C2Lab, Get-C2Contract, Assert-C2Journal, New-C2Backend, Assert-C2Iso
