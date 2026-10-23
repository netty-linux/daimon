# Administrative entry point RESERVED for a separately reviewed, authenticated deployment.
# Do not run for provisioning. No Hyper-V or filesystem mutation is implemented.
[CmdletBinding()]
param([switch]$Apply, [switch]$Discard)
$ErrorActionPreference = 'Stop'
Write-Output 'PROPOSED ONLY: one Generation-2 VM; 2 vCPU; 4 GiB static RAM; 48 GiB maximum VHDX.'
Write-Output 'Private new directory; initially no NIC; no existing switch modified; no automatic start.'
Write-Output 'No resources created or deleted. No artifact installation is authorized by this script.'
if ($Apply -or $Discard) {
    throw 'BLOCKED: authenticated deployment closure and ownership-safe provisioning/disposal require review; no administrative action performed.'
}
throw 'BLOCKED: acquisition authentication is incomplete. See docs/HARNESS2_C2_PREFLIGHT.md.'
