# Offline fault injection. No real Hyper-V mutation or elevation; no Pester dependency.
#Requires -Version 7.4
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-lab.psm1') -Force
$module = Get-Module harness2-c2-lab
$script:passed = 0
function Check($condition, $message) { if (-not $condition) { throw $message } }
function Reject([scriptblock]$body, [string]$pattern = '.') {
    $caught = $null
    try { & $body | Out-Null } catch { $caught = $_ }
    if (-not $caught -or $caught.Exception.Message -notmatch $pattern) { throw "Expected refusal matching: $pattern; got: $caught" }
}
function Test([string]$name, [scriptblock]$body) {
    & $body
    $script:passed++
    Write-Output "PASS $name"
}
function New-Mock {
    $s = @{ journal = $null; fresh = $true; vm = $false; disk = $false; iso = $false
        vmState = 'Off'; denied = $false; lowMemory = $false; drift = $false
        shared = $false; fail = ''; persistFail = ''; events = [Collections.Generic.List[string]]::new(); closes = 0 }
    $b = @{
        Host = { param($mode,$c) if ($s.denied) { throw 'host denied' } }.GetNewClosure()
        Memory = { if ($s.lowMemory) { throw 'memory below floor' } }.GetNewClosure()
        Open = { param($c,$create) $h = @{ Fresh = $s.fresh }; $s.fresh = $false; $h }.GetNewClosure()
        Read = { param($c) $s.journal | ConvertTo-Json -Compress | ConvertFrom-Json -AsHashtable }.GetNewClosure()
        Write = { param($j)
            $s.events.Add('persist:' + $j.state)
            if ($s.persistFail -eq $j.state) { throw 'journal persist failure' }
            $s.journal = $j | ConvertTo-Json -Compress | ConvertFrom-Json -AsHashtable
        }.GetNewClosure()
        Iso = { param($j) $s.events.Add('iso'); if ($s.fail -eq 'iso') { throw 'ISO failure' }; $s.iso = $true }.GetNewClosure()
        VerifyIso = { param($j) if (-not $s.iso -or $s.fail -eq 'hash') { throw 'hash mismatch' } }.GetNewClosure()
        Disk = { param($j) $s.events.Add('disk'); $s.disk = $true; if ($s.fail -eq 'disk') { throw 'disk create failed' }; '6abc46df-80ce-46ae-8aa5-bb536b3c3505' }.GetNewClosure()
        VM = { param($j) $s.events.Add('vm'); $s.vm = $true; if ($s.fail -eq 'vm') { throw 'VM create failed' }; '582f9057-03bf-4b8e-bfb3-a349e11837f4' }.GetNewClosure()
        Configure = { param($j) $s.events.Add('configure'); if ($s.fail -eq 'configure') { throw 'config failure' } }.GetNewClosure()
        Validate = { param($j,$running)
            $state = if ($running) { 'Running' } else { 'Off' }
            if (-not $s.vm -or $s.vmState -ne $state -or $s.drift) { throw 'configuration/state drift' }
        }.GetNewClosure()
        Start = { param($j) $s.events.Add('start'); $s.vmState = 'Running'; if ($s.fail -eq 'start') { throw 'start result lost' } }.GetNewClosure()
        RemoveVM = { param($j)
            $s.events.Add('remove-vm')
            if ($s.vmState -ne 'Off') { throw 'VM must be Off' }
            $s.vm = $false
            if ($s.fail -eq 'remove-vm') { throw 'remove VM result lost' }
        }.GetNewClosure()
        RemoveDisk = { param($j)
            $s.events.Add('remove-disk')
            if ($s.shared) { throw 'shared disk' }
            $s.disk = $false
            if ($s.fail -eq 'remove-disk') { throw 'remove disk result lost' }
        }.GetNewClosure()
        RemoveIso = { param($j) $s.events.Add('remove-iso'); $s.iso = $false }.GetNewClosure()
        Close = { param($h) $s.closes++ }.GetNewClosure()
    }
    @{ state = $s; backend = $b }
}
$base = @{ Root = 'D:\c2-test-never-created'; IsoPath = 'D:\c2-fixture.iso'
    OperationId = [guid]'ace3eaf4-4e8d-45d0-ae5e-f8029c326a16' }
function Run($f, [string]$mode, [string]$id = '') {
    Invoke-C2Lab @base -Mode $mode -Authorization ($mode.ToUpperInvariant() + ':' + $base.OperationId) -ExpectedVMId $id -Backend $f.backend
}

Test 'action acknowledgement refuses before backend' {
    $f = New-Mock
    Reject { Invoke-C2Lab @base -Mode Create -Authorization 'wrong' -Backend $f.backend } 'acknowledgement'
    Check ($f.state.events.Count -eq 0 -and $f.state.fresh) 'backend was reached'
}
Test 'host admission fails without side effects' {
    $f = New-Mock; $f.state.denied = $true
    Reject { Run $f Create } 'denied'
    Check $f.state.fresh 'root opened after denied host'
}
Test 'create is powered off and repeat does not recreate' {
    $f = New-Mock; $j = Run $f Create; $again = Run $f Create
    Check ($j.state -eq 'configured' -and $again.vm_id -eq $j.vm_id) 'creation not stable'
    Check (@($f.state.events | Where-Object { $_ -eq 'vm' }).Count -eq 1) 'VM recreated'
    Check (-not $f.state.events.Contains('start')) 'creation booted VM'
    Check ($f.state.events.IndexOf('persist:vm-pending') -lt $f.state.events.IndexOf('vm')) 'missing durable intention'
}
Test 'low RAM blocks boot but not powered-off creation' {
    $f = New-Mock; $f.state.lowMemory = $true; $j = Run $f Create
    Reject { Run $f Start $j.vm_id } 'memory'
    Check (-not $f.state.events.Contains('start')) 'low RAM boot occurred'
}
Test 'boot requires exact VMId' {
    $f = New-Mock; $null = Run $f Create
    Reject { Run $f Start 'a9c20c3f-8b6e-4281-847c-b26ef7d47563' } 'expected VM ID'
}
Test 'hash failure prevents boot' {
    $f = New-Mock; $j = Run $f Create; $f.state.fail = 'hash'
    Reject { Run $f Start $j.vm_id } 'hash'
    Check (-not $f.state.events.Contains('start')) 'bad image booted'
}
Test 'configuration drift prevents boot' {
    $f = New-Mock; $j = Run $f Create; $f.state.drift = $true
    Reject { Run $f Start $j.vm_id } 'drift'
}
Test 'boot succeeds once and repeat only observes' {
    $f = New-Mock; $j = Run $f Create; $null = Run $f Start $j.vm_id
    $f.state.lowMemory = $true; $again = Run $f Start $j.vm_id
    Check ($again.state -eq 'running' -and @($f.state.events | Where-Object { $_ -eq 'start' }).Count -eq 1) 'repeat boot was not idempotent'
}
Test 'lost boot response reconciles Running without second Start' {
    $f = New-Mock; $j = Run $f Create; $f.state.fail = 'start'
    Reject { Run $f Start $j.vm_id } 'lost'
    Check ($f.state.journal.state -eq 'start-pending') 'lost pending state'
    $f.state.fail = ''; $null = Run $f Start $j.vm_id
    Check (@($f.state.events | Where-Object { $_ -eq 'start' }).Count -eq 1) 'blind Start retry'
}
Test 'interrupted boot still Off is unresolved' {
    $f = New-Mock; $j = Run $f Create; $f.state.journal.state = 'start-pending'
    Reject { Run $f Start $j.vm_id } 'drift'
    Check (-not $f.state.events.Contains('start')) 'unresolved boot retried'
}
Test 'discard requires exact VMId' {
    $f = New-Mock; $null = Run $f Create
    Reject { Run $f Discard 'wrong' } 'expected VM ID'
    Check (-not $f.state.events.Contains('remove-vm')) 'foreign removal attempted'
}
Test 'discard never stops running VM' {
    $f = New-Mock; $j = Run $f Create; $null = Run $f Start $j.vm_id
    Reject { Run $f Discard $j.vm_id } 'Off'
    Check ($f.state.vm -and $f.state.disk) 'running resources removed'
}
Test 'discard removes owned resources once and retains tombstone' {
    $f = New-Mock; $j = Run $f Create; $end = Run $f Discard $j.vm_id; $null = Run $f Discard $j.vm_id
    Check ($end.state -eq 'discarded' -and -not $f.state.vm -and -not $f.state.disk -and -not $f.state.iso) 'discard incomplete'
    Check (@($f.state.events | Where-Object { $_ -eq 'remove-vm' }).Count -eq 1) 'removal repeated'
}
Test 'shared disk stops cleanup and preserves ISO/evidence' {
    $f = New-Mock; $j = Run $f Create; $f.state.shared = $true
    Reject { Run $f Discard $j.vm_id } 'shared'
    Check ($f.state.disk -and $f.state.iso -and $f.state.journal.state -eq 'disk-remove-pending') 'unsafe cleanup continued'
}
Test 'configuration failure resumes exact recorded VM' {
    $f = New-Mock; $f.state.fail = 'configure'
    Reject { Run $f Create } 'config failure'
    Check ($f.state.journal.state -eq 'vm-recorded') 'identity not recorded'
    $f.state.fail = ''; $null = Run $f Create
    Check (@($f.state.events | Where-Object { $_ -eq 'vm' }).Count -eq 1) 'created another VM'
}
foreach ($fault in 'disk','vm') {
    Test "uncertain $fault creation is never adopted/deleted" {
        $f = New-Mock; $f.state.fail = $fault
        Reject { Run $f Create } 'create failed'
        $f.state.fail = ''
        Reject { Run $f Create } 'Unresolved'
        Reject { Run $f Discard } 'manual reconciliation'
        Check (-not $f.state.events.Contains('remove-disk') -and -not $f.state.events.Contains('remove-vm')) 'uncertain cleanup attempted'
    }
}
Test 'journal persist failure after VM creation preserves uncertainty' {
    $f = New-Mock; $f.state.persistFail = 'vm-recorded'
    Reject { Run $f Create } 'persist failure'
    Check ($f.state.vm -and $f.state.journal.state -eq 'vm-pending') 'lost durable pending evidence'
    $f.state.persistFail = ''
    Reject { Run $f Discard } 'manual reconciliation'
}
Test 'journal failure before disk creation has no disk effect' {
    $f = New-Mock; $f.state.persistFail = 'disk-pending'
    Reject { Run $f Create } 'persist failure'
    Check (-not $f.state.disk) 'disk created before durable intent'
}
foreach ($fault in 'remove-vm','remove-disk') {
    Test "lost $fault response can reconcile safely" {
        $f = New-Mock; $j = Run $f Create; $f.state.fail = $fault
        Reject { Run $f Discard $j.vm_id } 'lost'
        $f.state.fail = ''; $end = Run $f Discard $j.vm_id
        Check ($end.state -eq 'discarded') 'cleanup did not recover'
    }
}
Test 'journal contract tampering is refused' {
    $f = New-Mock; $null = Run $f Create; $f.state.journal.disk = 'D:\personal.vhdx'
    Reject { Run $f Discard } 'contract mismatch'
    Check (-not $f.state.events.Contains('remove-disk')) 'tampered journal used'
}
Test 'all exits after opening release operation lock' {
    $f = New-Mock; $f.state.fail = 'iso'
    Reject { Run $f Create } 'ISO failure'
    Check ($f.state.closes -eq 1) 'operation lock leaked'
}
Test 'unsafe paths and reserved Windows aliases refused' {
    foreach ($path in 'relative','\\host\share\lab','D:\x\..\y','D:\x.\y','D:\x\CON','D:\x:ads','D:\') {
        Reject { Get-C2Contract $path 'D:\iso' $base.OperationId }
    }
}
Test 'native private mkdir refuses existing directory without changing ACL' {
    $before = (Get-Acl -LiteralPath $PSScriptRoot).Sddl
    Reject { & $module { param($p) New-C2Directory $p } $PSScriptRoot } 'exists'
    Check ((Get-Acl -LiteralPath $PSScriptRoot).Sddl -ceq $before) 'existing directory ACL changed'
}

Test 'real parser refuses duplicate, oversized and incomplete journal' {
    foreach ($raw in '{"version":2,"version":2}', (' ' * 65537), '{}', '[]') {
        Reject { & $module { param($text,$params)
            $c = Get-C2Contract @params
            ConvertFrom-C2JournalText $text $c
        } $raw $base }
    }
}
Test 'real parser accepts exact bounded contract' {
    $contract = Get-C2Contract @base
    $j = & $module { param($raw,$c) ConvertFrom-C2JournalText $raw $c } ($contract | ConvertTo-Json -Compress) $contract
    Check ($j.operation_id -eq $contract.operation_id) 'roundtrip failed'
}

# Exercise the real Hyper-V adapter's ownership validators with module-local read-only fakes.
& $module {
    $script:Fixture = @{ vms = @(); refs = @{}; snapshots = @{}; disk = $null }
    function script:Get-VM { param($ErrorAction) $script:Fixture.vms }
    function script:Get-VMHardDiskDrive { param($VM,$VMSnapshot,$ErrorAction)
        if ($VMSnapshot) { $script:Fixture.refs[$VMSnapshot] } else { $script:Fixture.refs[$VM.Id.ToString()] }
    }
    function script:Get-VMSnapshot { param($VM,$ErrorAction) $script:Fixture.snapshots[$VM.Id.ToString()] }
    function script:Get-VHD { param($Path,$ErrorAction) $script:Fixture.disk }
    function script:Get-VMDvdDrive { param($VM,$VMSnapshot,$ErrorAction)
        if ($script:Fixture.Contains('foreignIso') -and $script:Fixture.foreignIso) { $script:Fixture.foreignIso }
        else { $script:Fixture.dvds }
    }
    function script:Get-VMNetworkAdapter { param($VM,$ErrorAction) $script:Fixture.nics }
    function script:Get-VMMemory { param($VM,$ErrorAction) $script:Fixture.memory }
    function script:Get-VMProcessor { param($VM,$ErrorAction) $script:Fixture.cpu }
    function script:Get-VMFirmware { param($VM,$ErrorAction) $script:Fixture.firmware }
    function script:Get-VMIntegrationService { param($VM,$ErrorAction) $script:Fixture.services }
    function script:Reset-ValidationFixture {
        $j = Get-C2Contract 'D:\lab' 'D:\image.iso' 'ace3eaf4-4e8d-45d0-ae5e-f8029c326a16'
        $j.vm_id = '582f9057-03bf-4b8e-bfb3-a349e11837f4'; $j.disk_id = '6abc46df-80ce-46ae-8aa5-bb536b3c3505'
        $vmDirectory = [IO.Path]::Combine($j.config, $j.vm_name)
        $script:Fixture.vms = @([pscustomobject]@{ Id = [guid]$j.vm_id; Name = $j.vm_name; Generation = 2; ConfigurationLocation = $vmDirectory; SmartPagingFilePath = $vmDirectory; SnapshotFileLocation = $vmDirectory
            Notes = 'DAIMON-C2:' + $j.operation_id; State = 'Off'; AutomaticStartAction = 'Nothing'; AutomaticStopAction = 'TurnOff'; AutomaticCheckpointsEnabled = $false; CheckpointType = 'Disabled' })
        $script:Fixture.refs = @{ $j.vm_id = @([pscustomobject]@{ Path = $j.disk }) }; $script:Fixture.snapshots = @{ $j.vm_id = @() }
        $script:Fixture.dvds = @([pscustomobject]@{ Path = $j.iso }); $script:Fixture.nics = @(); $script:Fixture.services = @()
        $script:Fixture.memory = [pscustomobject]@{ DynamicMemoryEnabled = $true; Minimum = 3GB; Startup = 3GB; Maximum = 4GB }
        $script:Fixture.cpu = [pscustomobject]@{ Count = 2; ExposeVirtualizationExtensions = $false }
        $script:Fixture.firmware = [pscustomobject]@{ SecureBoot = 'On'; SecureBootTemplate = 'MicrosoftUEFICertificateAuthority'
            BootOrder = @([pscustomobject]@{ BootType = 'Drive'; Device = [pscustomobject]@{ Path = $j.iso } }) }
        $script:Fixture.disk = [pscustomobject]@{ DiskIdentifier = [guid]$j.disk_id; VhdType = 'Dynamic'; VhdFormat = 'VHDX'; Size = 48GB; ParentPath = '' }
        $j
    }
}
Test 'real validator accepts complete offline configuration' {
    & $module { $j = Reset-ValidationFixture; $null = Assert-C2VM $j -Complete }
}
Test '3 GiB probe ceiling requires explicit opt-in; default still requires 4 GiB' {
    & $module {
        $j=Reset-ValidationFixture;$script:Fixture.memory.Maximum=3GB
        try{$null=Assert-C2VM $j -Complete;throw 'default accepted cap'}catch{if($_.Exception.Message -notmatch 'configuration drift'){throw}}
        $null=Assert-C2VM $j -Complete -ProbeMaximumMemoryGiB 3
    }
}
Test 'real validator refuses parent container in place of exact Hyper-V child directory' {
    Reject { & $module {
        $j = Reset-ValidationFixture
        $script:Fixture.vms[0].ConfigurationLocation = $j.config
        $null = Get-C2VM $j
    } } 'ownership/location'
}
Test 'real adapter normalizes braced upper-case VHD GUID before recording' {
    & $module {
        $j = Reset-ValidationFixture
        $script:Fixture.disk.DiskIdentifier = '{6ABC46DF-80CE-46AE-8AA5-BB536B3C3505}'
        function script:New-VHD { param($Path,[switch]$Dynamic,$SizeBytes,$ErrorAction) $script:Fixture.disk }
        $backend = New-C2Backend
        $captured = & $backend.Disk $j
        if ($captured -cne $j.disk_id) { throw 'Noncanonical ID returned from real adapter.' }
        $null = Assert-C2Disk $j
    }
}
Test 'resource normalization still rejects empty and malformed identities' {
    foreach ($value in '', 'not-a-guid', '00000000-0000-0000-0000-000000000000') {
        Reject { & $module { param($v) ConvertTo-C2ResourceId $v } $value } 'invalid nonzero'
    }
}
foreach ($change in 'nic','integration','memory','cpu','secureboot','bootorder','notes','checkpoint','foreigndisk','externalpaging') {
    Test "real validator rejects $change drift" {
        Reject { & $module { param($change)
            $j = Reset-ValidationFixture
            switch ($change) {
                'nic' { $script:Fixture.nics = @([pscustomobject]@{ Name = 'unexpected' }) }
                'integration' { $script:Fixture.services = @([pscustomobject]@{ Enabled = $true }) }
                'memory' { $script:Fixture.memory.Maximum = 8GB }
                'cpu' { $script:Fixture.cpu.ExposeVirtualizationExtensions = $true }
                'secureboot' { $script:Fixture.firmware.SecureBoot = 'Off' }
                'bootorder' { $script:Fixture.firmware.BootOrder[0].Device.Path = 'D:\unapproved.iso' }
                'notes' { $script:Fixture.vms[0].Notes = 'another operation' }
                'checkpoint' { $script:Fixture.snapshots[$j.vm_id] = @('checkpoint') }
                'foreigndisk' { $script:Fixture.refs[$j.vm_id] = @([pscustomobject]@{ Path = 'D:\personal.vhdx' }) }
                'externalpaging' { $script:Fixture.vms[0].SmartPagingFilePath = 'D:\outside' }
            }
            $null = Assert-C2VM $j -Complete
        } $change } 'drift|Checkpoints|Unexpected disk|auxiliary path outside'
    }
}
Test 'real adapter rejects wrong VM identity/location' {
    & $module {
        $j = Get-C2Contract 'D:\lab' 'D:\image.iso' 'ace3eaf4-4e8d-45d0-ae5e-f8029c326a16'
        $j.vm_id = '582f9057-03bf-4b8e-bfb3-a349e11837f4'
        $script:Fixture.vms = @([pscustomobject]@{ Id = [guid]$j.vm_id; Name = $j.vm_name; Generation = 2; ConfigurationLocation = 'D:\other' })
        try { $null = Get-C2VM $j; throw 'validator accepted foreign location' }
        catch { if ($_.Exception.Message -notmatch 'ownership/location') { throw } }
    }
}
Test 'real adapter detects foreign checkpoint reference' {
    & $module {
        $j = Get-C2Contract 'D:\lab' 'D:\image.iso' 'ace3eaf4-4e8d-45d0-ae5e-f8029c326a16'
        $j.vm_id = '582f9057-03bf-4b8e-bfb3-a349e11837f4'
        $id = 'a9c20c3f-8b6e-4281-847c-b26ef7d47563'
        $script:Fixture.vms = @([pscustomobject]@{ Id = [guid]$id })
        $script:Fixture.refs[$id] = @(); $script:Fixture.snapshots[$id] = @('snapshot')
        $script:Fixture.refs['snapshot'] = @([pscustomobject]@{ Path = $j.disk })
        try { Assert-C2ExclusiveDisk $j -AllowOwned; throw 'validator accepted foreign reference' }
        catch { if ($_.Exception.Message -notmatch 'another live/checkpoint') { throw } }
    }
}
Test 'real adapter rejects replaced VHD identity' {
    & $module {
        $j = Get-C2Contract 'D:\lab' 'D:\image.iso' 'ace3eaf4-4e8d-45d0-ae5e-f8029c326a16'
        $j.disk_id = '6abc46df-80ce-46ae-8aa5-bb536b3c3505'
        $script:Fixture.disk = [pscustomobject]@{ DiskIdentifier = [guid]::NewGuid(); VhdType = 'Dynamic'; VhdFormat = 'VHDX'; Size = 48GB; ParentPath = '' }
        try { $null = Assert-C2Disk $j; throw 'validator accepted replaced disk' }
        catch { if ($_.Exception.Message -notmatch 'identity/layout') { throw } }
    }
}
Test 'real adapter refuses deleting ISO referenced by another VM' {
    & $module {
        $j = Reset-ValidationFixture
        $id = 'a9c20c3f-8b6e-4281-847c-b26ef7d47563'
        $script:Fixture.vms = @([pscustomobject]@{ Id = [guid]$id })
        $script:Fixture.snapshots[$id] = @()
        $script:Fixture.foreignIso = @([pscustomobject]@{ Path = $j.iso })
        try { Assert-C2ExclusiveIso $j; throw 'accepted shared ISO deletion' }
        catch { if ($_.Exception.Message -notmatch 'another live/checkpoint reference') { throw } }
    }
}
Test 'real root ACL validator rejects extra user access' {
    & $module {
        $script:Fixture.acl = New-C2Acl
        function script:Get-Acl { param($LiteralPath) $script:Fixture.acl }
        Assert-C2Acl 'D:\c2-test-acl-not-created' -Directory
        $script:Fixture.acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new(
            [Security.Principal.SecurityIdentifier]::new('S-1-5-32-545'), 'Read', 'Allow'))
        try { Assert-C2Acl 'D:\c2-test-acl-not-created' -Directory; throw 'accepted broad ACL' }
        catch { if ($_.Exception.Message -notmatch 'Unexpected journal/root access') { throw } }
    }
}
Test 'media descriptor accepts Windows AI metadata with identical protected explicit ACEs' {
    & $module {
        $expected = [Security.AccessControl.FileSecurity]::new()
        $expected.SetSecurityDescriptorSddlForm('O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;S-1-5-83-1-1-2-3-4)')
        $actual = [Security.AccessControl.FileSecurity]::new()
        $actual.SetSecurityDescriptorSddlForm('O:BAD:PAI(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;S-1-5-83-1-1-2-3-4)')
        Assert-C2MediaDescriptor $actual $expected
    }
}
foreach ($change in 'write','foreignsid','deny','extra','unprotected','owner','inherited') {
    Test "media descriptor still rejects $change change" {
        Reject { & $module { param($change)
            $sddl = 'O:BAD:PAI(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;S-1-5-83-1-1-2-3-4)'
            $expected = [Security.AccessControl.FileSecurity]::new()
            $expected.SetSecurityDescriptorSddlForm($sddl)
            switch ($change) {
                'write' { $sddl = $sddl.Replace(';;FR;', ';;FA;') }
                'foreignsid' { $sddl = $sddl.Replace('-1-1-2-3-4)', '-1-1-2-3-5)') }
                'deny' { $sddl = $sddl.Replace('(A;;FR;', '(D;;FR;') }
                'extra' { $sddl += '(A;;FR;;;BU)' }
                'unprotected' { $sddl = $sddl.Replace('D:PAI', 'D:AI') }
                'owner' { $sddl = $sddl.Replace('O:BA', 'O:BU') }
                'inherited' { $sddl = $sddl.Replace('(A;;FR;', '(A;ID;FR;') }
            }
            $actual = [Security.AccessControl.FileSecurity]::new()
            $actual.SetSecurityDescriptorSddlForm($sddl)
            Assert-C2MediaDescriptor $actual $expected
        } $change } 'Media.*changed'
    }
}
Test 'control candidate revokes only the exact own VM grant and restores private rules' {
    & $module {
        $expected = New-C2Acl
        $actual = New-C2Acl
        $sid = [Security.Principal.SecurityIdentifier]::new('S-1-5-83-1-1-2-3-4')
        $actual.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid,'FullControl','Allow'))
        $candidate = Get-C2ControlCandidate $actual $expected $sid
        if ($candidate.revoked_rules -ne 1) { throw 'Exact VM grant not removed.' }
        Assert-C2MediaDescriptor $candidate.acl $expected
        if (@($actual.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])).Count -ne 3) { throw 'Candidate mutated input before validation.' }
    }
}
foreach ($change in 'foreign','broadgroup','deny','owner','unprotected','adminrights') {
    Test "control candidate refuses $change instead of resetting arbitrary permissions" {
        Reject { & $module { param($change)
            $expected = New-C2FileAcl
            $actual = New-C2FileAcl
            $sid = [Security.Principal.SecurityIdentifier]::new('S-1-5-83-1-1-2-3-4')
            switch ($change) {
                'foreign' { $actual.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-5-83-1-4-3-2-1'),'Read','Allow')) }
                'broadgroup' { $actual.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-5-32-545'),'Read','Allow')) }
                'deny' { $actual.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid,'Read','Deny')) }
                'owner' { $actual.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-545')) }
                'unprotected' { $actual.SetAccessRuleProtection($false,$true) }
                'adminrights' { $actual.SetAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'),'Read','Allow')) }
            }
            $null = Get-C2ControlCandidate $actual $expected $sid
        } $change }
    }
}
Test 'control repair refuses running or uncommitted state before changing ACLs' {
    & $module {
        $j = Reset-ValidationFixture
        $script:Fixture.foreignIso = @()
        $script:Fixture.acl = New-C2FileAcl
        $j.state = 'running'
        try { Repair-C2OwnedControl $j; throw 'accepted running journal' }
        catch { if ($_.Exception.Message -notmatch 'pre-boot ownership') { throw } }
        $j.state = 'vm-recorded'; $script:Fixture.vms[0].State = 'Running'
        try { Repair-C2OwnedControl $j; throw 'accepted Running VM' }
        catch { if ($_.Exception.Message -notmatch 'must be Off') { throw } }
    }
}
Test 'real NTFS journal replacement survives successive durable states' {
    # Actual FileStream/Flush/Replace/parser; only ACL authority is simulated for unelevated CI/local tests.
    $scratchParent = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\.git'))
    $scratch = Join-Path $scratchParent ('c2-journal-test-' + [guid]::NewGuid().ToString('N'))
    $null = [IO.Directory]::CreateDirectory($scratch)
    try {
        & $module { param($root)
            $script:Fixture.acl = New-C2Acl
            function script:New-C2File([string]$Path) {
                [IO.FileStream]::new($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::ReadWrite,
                    [IO.FileShare]::None, 4096, [IO.FileOptions]::WriteThrough)
            }
            $j = Get-C2Contract $root 'D:\c2-fixture.iso' 'ace3eaf4-4e8d-45d0-ae5e-f8029c326a16'
            Write-C2Journal $j
            $j.state = 'iso-pending'; Write-C2Journal $j
            $actual = Read-C2Journal (Get-C2Contract $root 'D:\c2-fixture.iso' ([guid]$j.operation_id))
            if ($actual.state -ne 'iso-pending') { throw 'Durable replacement not observed.' }
            $j.state = 'iso-ready'
            $held = [IO.FileStream]::new((Join-Path $root 'journal.json'), 'Open', 'Read', 'Read')
            $failed = $false
            try { Write-C2Journal $j } catch { $failed = $true } finally { $held.Dispose() }
            if (-not $failed) { throw 'Locked destination did not prevent replacement.' }
            $actual = Read-C2Journal (Get-C2Contract $root 'D:\c2-fixture.iso' ([guid]$j.operation_id))
            if ($actual.state -ne 'iso-pending') { throw 'Failed replacement corrupted prior journal.' }
            Write-C2Journal $j
            $actual = Read-C2Journal (Get-C2Contract $root 'D:\c2-fixture.iso' ([guid]$j.operation_id))
            if ($actual.state -ne 'iso-ready') { throw 'Second replacement not observed.' }
        } $scratch
    } finally {
        # Exact generated test directory, checked inside the checkout's .git; never delete supplied roots.
        $resolved = [IO.Path]::GetFullPath($scratch)
        if (-not $resolved.StartsWith($scratchParent + '\', [StringComparison]::OrdinalIgnoreCase) -or
            [IO.Path]::GetFileName($resolved) -notmatch '^c2-journal-test-[a-f0-9]{32}$' -or
            ((Get-Item -LiteralPath $resolved).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Unsafe test cleanup target.' }
        [IO.Directory]::Delete($resolved, $true)
    }
}
Remove-Module harness2-c2-lab
Write-Output "PASS $script:passed offline tests; real VM/ACL lifecycle NOT RUN."
