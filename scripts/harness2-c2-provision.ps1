#Requires -Version 7.4
# No automatic elevation, installation, guest input, networking or host servicing.
[CmdletBinding()]
param(
    [ValidateSet('Plan','Create','Start','Discard','Inspect')][string]$Mode = 'Plan',
    [Parameter(Mandatory)][string]$Root,
    [Parameter(Mandatory)][string]$IsoPath,
    [Parameter(Mandatory)][guid]$OperationId,
    [string]$Authorization,
    [string]$ExpectedVMId,
    [string]$ReceiptPath
)
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-lab.psm1') -Force
if ($Mode -eq 'Plan') {
    $c = Get-C2Contract $Root $IsoPath $OperationId
    [ordered]@{
        status = 'PROPOSAL_ONLY'; administrative_changes = 0; contract = $c
        vm = @{ generation = 2; vcpus = 2; memory_gib = @(3,3,4); dynamic_memory = $true
            vhdx_max_gib = 48; network_adapters = 0; automatic_start = $false
            secure_boot = 'MicrosoftUEFICertificateAuthority' }
        creation = 'powered off; private authenticated ISO copy; blank disk; no guest installation'
        boot = 'separate approval; at least 8 GiB available RAM; stock live installer only'
        hostile_testing = 'BLOCKED: Windows/ESU, gVisor, deployment closure and all 18 gates unverified'
        disposal = 'separate approval; exact VMId; Off; no checkpoints; exclusive disk ID; retain journal/root'
    } | ConvertTo-Json -Depth 5
    exit 0
}
$receipt = $null
if ($ReceiptPath) {
    # Advisory output for the unelevated operator; never accepted as ownership authority.
    $contract = Get-C2Contract $Root $IsoPath $OperationId
    $r = Get-C2Contract $ReceiptPath $IsoPath $OperationId
    if ($r.root.StartsWith($contract.root + '\', [StringComparison]::OrdinalIgnoreCase) -or
        $r.root -ieq $contract.root) { throw 'Receipt must be outside protected lab root.' }
    $p = [IO.FileInfo]::new($r.root)
    if (-not $p.Directory.Exists -or $p.Exists) { throw 'New receipt in existing operator directory required.' }
    # CreateNew cannot overwrite a pre-existing file; refuse reparse ancestors too.
    $ancestor = $p.Directory
    while ($null -ne $ancestor) {
        if ($ancestor.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Receipt reparse ancestry forbidden.' }
        $ancestor = $ancestor.Parent
    }
    $receipt = [IO.FileStream]::new($p.FullName, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
}
try {
    try {
        $result = Invoke-C2Lab -Mode $Mode -Root $Root -IsoPath $IsoPath -OperationId $OperationId `
            -Authorization $Authorization -ExpectedVMId $ExpectedVMId
        $report = [ordered]@{ status = 'SUCCESS'; mode = $Mode; journal = $result
            observed_at_utc = [DateTime]::UtcNow.ToString('o'); ownership_authority = $false }
    } catch {
        $report = [ordered]@{ status = 'REFUSED_OR_UNRESOLVED'; mode = $Mode
            reason = $_.Exception.Message; observed_at_utc = [DateTime]::UtcNow.ToString('o'); ownership_authority = $false }
    }
    $json = $report | ConvertTo-Json -Depth 5
    if ($receipt) {
        $bytes = [Text.UTF8Encoding]::new($false).GetBytes($json)
        $receipt.Write($bytes, 0, $bytes.Length); $receipt.Flush($true)
    }
    Write-Output $json
    if ($report.status -ne 'SUCCESS') { exit 2 }
} finally { if ($receipt) { $receipt.Dispose() } }
