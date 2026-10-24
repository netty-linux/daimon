#Requires -Version 7.4
# Double-click entry runner: offline own-process suite only. No UAC or policy changes.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$OutputDirectory,[switch]$VerifyOnly)
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
$output=[IO.Path]::GetFullPath($OutputDirectory)
if(-not (Test-Path -LiteralPath $output -PathType Container)){throw 'Existing local output directory required.'}
$reportPath=Join-Path $output 'C2-offline-result.json';$statusPath=Join-Path $output 'C2-offline-status.txt'
$suitePath=Join-Path $output 'C2-offline-suite.json';$logPath=Join-Path $output 'C2-offline-run.log'
$report=@{status='BLOCKED';reason='ENTRY_NOT_COMPLETED';minimum_correction='Review the local report/log without retrying automatically.';vm_access=$false;administrative_changes=$false;policy_changes=$false;external_processes_terminated=$false;gates='all-18-unverified'}
$exitCode=2
try {
    if(Test-Path -LiteralPath $reportPath){throw 'Existing result: review it; do not automatically repeat.'}
    # Generated reviewed pins are embedded by the artifact builder, not accepted from user input.
    $pinsPath=Join-Path $PSScriptRoot 'harness2-c2-offline-pins.json'
    $pins=Get-Content -LiteralPath $pinsPath -Raw|ConvertFrom-Json -AsHashtable
    foreach($name in $pins.Keys){
        if([IO.Path]::GetFileName($name) -cne $name){throw 'Only sibling source files may be pinned.'}
        $sourcePath=Join-Path $PSScriptRoot $name
        if((Get-Item -LiteralPath $sourcePath).Length -gt 2MB){throw 'Bounded offline source size exceeded.'}
        $text=[IO.File]::ReadAllText($sourcePath).Replace("`r`n","`n")
        $hash=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.UTF8Encoding]::new($false).GetBytes($text)))
        if($hash -cne $pins[$name]){throw 'Reviewed offline source hash mismatch.'}
    }
    $report.source_pins=$pins
    if($VerifyOnly){Write-Output 'PASS static source verification only; no suite, isolation query or VM action.';return}
    Import-Module (Join-Path $PSScriptRoot 'harness2-c2-offline-result.psm1')
    if((Test-Path -LiteralPath $suitePath) -or (Test-Path -LiteralPath $logPath)){throw 'Prior attempt artifacts exist; review them instead of rerunning.'}
    $run=@{};$run.runtime=(Get-Process -Id $PID).Path;$run.started_utc=[datetime]::UtcNow.ToString('o')
    & (Join-Path $PSScriptRoot 'test-harness2-c2-emergency.ps1') -RequireIndependent -ReportPath $suitePath *> $logPath
    $suiteExit=0
    $nativeExit=Get-Variable LASTEXITCODE -ErrorAction SilentlyContinue
    if($nativeExit -and $nativeExit.Value){$suiteExit=[int]$nativeExit.Value}
    $suite=Get-Content -LiteralPath $suitePath -Raw|ConvertFrom-Json -AsHashtable
    $verdict=Get-C2OfflineResult $suite $suiteExit
    $report.status=$verdict.status;$report.reason=$verdict.reason;$report.minimum_correction=$verdict.correction;$report.suite=$suite;$report.run=$run
    $exitCode=if($report.status -eq 'PASS'){0}elseif($report.status -eq 'FAIL'){1}else{2}
}catch {
    $report.error=$_.Exception.Message
    if(Test-Path -LiteralPath $suitePath){
        try{$suite=Get-Content -LiteralPath $suitePath -Raw|ConvertFrom-Json -AsHashtable;$report.suite=$suite;$verdict=Get-C2OfflineResult $suite 1;$report.status=$verdict.status;$report.reason=$verdict.reason;$report.minimum_correction=$verdict.correction}catch{$report.reason='UNREADABLE_OR_INVALID_SUITE_PROOF'}
    }else{$report.reason='RUNTIME_POLICY_SOURCE_OR_PATH_BLOCKED';$report.minimum_correction='Review the launcher log/source hashes and file permissions. No automatic elevation or ExecutionPolicy bypass.'}
    $exitCode=if($report.status -eq 'FAIL'){1}else{2}
}finally{
    if(-not $VerifyOnly){
        $report.finished_utc=[datetime]::UtcNow.ToString('o')
        if($report.status -eq 'PASS'){$report.next_step='Prepare a separate request for one real emergency VM trial after fresh preflight. This report does not authorize any boot.'}
        $bytes=[Text.UTF8Encoding]::new($false).GetBytes(($report|ConvertTo-Json -Depth 24))
        $f=[IO.FileStream]::new($reportPath,'CreateNew','Write','Read')
        try{$f.Write($bytes,0,$bytes.Length);$f.Flush($true)}finally{$f.Dispose()}
        $human=switch($report.status){
            'PASS'{'PASS - Os dois cenarios positivos offline foram comprovados. SEM VM.'}
            'FAIL'{'FAIL - A prova offline falhou. Consulte relatorio e log; nao tente boot.'}
            default{'BLOCKED - O ambiente ou os arquivos impediram a prova. Nao repetir automaticamente.'}
        }
        @($human,$report.reason,'Nenhuma VM, elevacao ou politica do Windows foi alterada.')|Set-Content -LiteralPath $statusPath -Encoding utf8
        Write-Output ($report.status+' — '+$report.reason)
    }
}
exit $exitCode
