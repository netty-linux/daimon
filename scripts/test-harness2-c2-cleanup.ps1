# Own bounded offline fixtures only; no VM, service, or external-process operations.
[CmdletBinding()]param([string]$Mode='Run',[string]$CaseDirectory,[string]$ReportPath)
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
if($Mode -eq 'Payload'){
    Set-Content -LiteralPath (Join-Path $CaseDirectory 'ready') -Value $PID
    $clock=[Diagnostics.Stopwatch]::StartNew()
    while(-not (Test-Path -LiteralPath (Join-Path $CaseDirectory 'exit')) -and $clock.Elapsed.TotalSeconds -lt 20){Start-Sleep -Milliseconds 25}
    exit 0
}
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-test-cleanup.psm1')
$count=0;$evidence=@{vm_access=$false;external_processes_terminated=$false;tests=@();status='RUNNING'}
function Test($name,[scriptblock]$body){& $body;$script:count++;$script:evidence.tests+=@{name=$name;passed=$true};"PASS $name"}
function Snapshot($exit,$metadata='MATCH',$registered=$true,$handle=$true){@{registered=$registered;handle_valid=$handle;exited=$exit;metadata=$metadata}}
function Registration {@{registered=$true;completed=$false;identity=@{pid=42};registration=@{metadata='MATCH';handle_retained=$true}}}
try {
    foreach($case in @(
        @('exited original with retained verified handle', $true,'UNKNOWN',$true,$true,'EXITED_PROVEN'),
        @('live own instance',$false,'MATCH',$true,$true,'LIVE_OWN'),
        @('live divergent identity',$false,'MISMATCH',$true,$true,'LIVE_DIVERGENT'),
        @('unreadable metadata',$false,'UNKNOWN',$true,$true,'INDETERMINATE'),
        @('unknown state',$null,'MATCH',$true,$true,'INDETERMINATE'),
        @('PID missing without retained proof',$true,'MATCH',$false,$false,'INDETERMINATE'),
        @('closed handle',$true,'MATCH',$true,$false,'INDETERMINATE'))){
        Test $case[0] {if((Get-C2CleanupDecision (Snapshot $case[1] $case[2] $case[3] $case[4])) -ne $case[5]){throw 'wrong classification'}}
    }
    foreach($metadata in 'UNKNOWN','MISMATCH'){
        Test "never terminate $metadata identity" {
            $r=Registration;$script:kills=0
            $result=Invoke-C2OwnProcessCleanup $r -Observe {param($r)Snapshot $false $metadata} -Terminate {$script:kills++}
            if($script:kills -or $r.completed -or $result.status -notin 'LIVE_DIVERGENT','INDETERMINATE'){throw 'unsafe termination'}
        }
    }
    Test 'identity changes at final revalidation: no termination' {
        $r=Registration;$script:reads=0;$script:kills=0
        $result=Invoke-C2OwnProcessCleanup $r -Observe {param($r)$script:reads++;if($script:reads -eq 1){Snapshot $false}else{Snapshot $false 'MISMATCH'}} -Terminate {$script:kills++}
        if($script:kills -or $result.status -ne 'LIVE_DIVERGENT'){throw 'race accepted'}
    }
    Test 'exit during metadata query: original handle suffices, no kill' {
        $r=Registration;$script:reads=0;$script:kills=0
        $result=Invoke-C2OwnProcessCleanup $r -Observe {param($r)$script:reads++;Snapshot ($script:reads -gt 1) 'UNKNOWN'} -Terminate {$script:kills++}
        # First unknown observation is deliberately refused; cannot infer exit from a PID.
        if($script:kills -or $result.status -ne 'INDETERMINATE'){throw 'unknown observation accepted'}
        $result=Invoke-C2OwnProcessCleanup $r -Observe {param($r)Snapshot $true 'UNKNOWN'} -Terminate {$script:kills++}
        if($script:kills -or $result.status -ne 'EXITED_PROVEN'){throw 'retained exit refused'}
    }
    Test 'exit between revalidation and kill: evidence still required' {
        $r=Registration;$script:exited=$false
        $result=Invoke-C2OwnProcessCleanup $r -Observe {param($r)Snapshot $script:exited} -Terminate {$script:exited=$true;throw 'own instance already exited'} -Confirm {$true}
        if($result.status -ne 'EXITED_WITH_COMMAND_ERROR' -or -not $result.ContainsKey('error')){throw 'partial failure lost or promoted to PASS'}
        if((Invoke-C2OwnProcessCleanup $r).status -ne 'EXITED_WITH_COMMAND_ERROR'){throw 'duplicate cleanup erased earlier failure'}
    }
    Test 'kill timeout cannot pass' {
        $r=Registration
        $result=Invoke-C2OwnProcessCleanup $r -Observe {param($r)Snapshot $false} -Terminate {} -Confirm {$false}
        if($r.completed -or $result.status -ne 'INDETERMINATE'){throw 'unconfirmed exit accepted'}
    }
    Test 'duplicate cleanup never observes or terminates again' {
        $r=Registration;$r.completed=$true
        $result=Invoke-C2OwnProcessCleanup $r -Observe {throw 'duplicate observation'} -Terminate {throw 'duplicate kill'}
        if($result.status -ne 'ALREADY_CLEAN'){throw 'duplicate refused'}
    }
    Test 'PID reuse with live mismatched birth identity is refused' {
        $r=Registration;$script:kills=0
        $result=Invoke-C2OwnProcessCleanup $r -Observe {param($r)Snapshot $false 'MISMATCH'} -Terminate {$script:kills++}
        if($result.status -ne 'LIVE_DIVERGENT' -or $script:kills){throw 'replacement PID affected'}
    }
    Test 'original exited handle with reused numeric PID never targets replacement' {
        $r=Registration;$script:kills=0
        $result=Invoke-C2OwnProcessCleanup $r -Observe {param($r)Snapshot $true 'MISMATCH'} -Terminate {$script:kills++}
        if($result.status -ne 'EXITED_PROVEN' -or $script:kills){throw 'original exit misclassified'}
    }
    Test 'exit after first live check is accepted without kill' {
        $r=Registration;$script:reads=0;$script:kills=0
        $result=Invoke-C2OwnProcessCleanup $r -Observe {param($r)$script:reads++;Snapshot ($script:reads -gt 1)} -Terminate {$script:kills++}
        if($result.status -ne 'EXITED_PROVEN' -or $script:kills){throw 'exited own instance killed'}
    }
    $i=@{pid=42;creator_pid=8;path='C:\pwsh.exe';entry='C:\test.ps1';role='Payload';directory='C:\case-a'}
    $cmd='"C:\pwsh.exe" -NoProfile -NonInteractive -File "C:\test.ps1" -Mode Payload -CaseDirectory "C:\case-a"'
    foreach($field in 'creator','entry','case','role','pid','path','missing-command'){
        Test "metadata rejects wrong $field" {
            $m=@{ProcessId=42;ParentProcessId=8;ExecutablePath='C:\pwsh.exe';CommandLine=$cmd}
            switch($field){'creator'{$m.ParentProcessId=9};'entry'{$m.CommandLine=$cmd.Replace('test.ps1','other.ps1')};'case'{$m.CommandLine=$cmd.Replace('case-a','case-b')};'role'{$m.CommandLine=$cmd.Replace('Payload','Watcher')};'pid'{$m.ProcessId=43};'path'{$m.ExecutablePath='C:\other.exe'};'missing-command'{$m.CommandLine=$null}}
            if((Test-C2OwnMetadata $i $m) -eq 'MATCH'){throw 'foreign binding accepted'}
        }
    }
    $exe=(Get-Process -Id $PID).Path
    foreach($fixture in 'natural-exit','live-stop','wrong-creator'){
        Test "real own-process $fixture with retained handle" {
            $dir=Join-Path ([IO.Path]::GetTempPath()) ('c2-cleanup-'+[guid]::NewGuid().ToString('N'))
            $null=New-Item -ItemType Directory -Path $dir
            $p=Start-Process -FilePath $exe -ArgumentList @('-NoProfile','-NonInteractive','-File',('"'+$PSCommandPath+'"'),'-Mode','Payload','-CaseDirectory',('"'+$dir+'"')) -WindowStyle Hidden -PassThru
            $readyClock=[Diagnostics.Stopwatch]::StartNew()
            while(-not (Test-Path -LiteralPath (Join-Path $dir 'ready'))){if($readyClock.Elapsed.TotalSeconds -gt 5){throw 'Own fixture readiness timeout'};Start-Sleep -Milliseconds 25}
            if([int](Get-Content -LiteralPath (Join-Path $dir 'ready')) -ne $p.Id){throw 'Own fixture readiness PID mismatch'}
            $id=@{pid=$p.Id;started_utc=$p.StartTime.ToUniversalTime().ToString('o');path=$exe;entry=$PSCommandPath;directory=$dir;creator_pid=$PID;role='Payload'}
            $r=Register-C2OwnProcess $id $PSCommandPath $dir $exe $PID 'Payload'
            try {
                if($fixture -eq 'wrong-creator'){
                    $bad=$id.Clone();$bad.creator_pid++
                    $refused=$false;try{$null=Register-C2OwnProcess $bad $PSCommandPath $dir $exe $PID 'Payload'}catch{$refused=$true}
                    if(-not $refused -or $r.process.HasExited){throw 'wrong creator accepted or target affected'}
                }
                if($fixture -eq 'natural-exit'){
                    $null=New-Item -ItemType File -Path (Join-Path $dir 'exit')
                    if(-not $r.process.WaitForExit(5000)){throw 'own fixture exit timeout'}
                    $legacy=Get-CimInstance Win32_Process -Filter ('ProcessId = '+$id.pid) -OperationTimeoutSec 5
                    $evidence.legacy_exit_observation=@{original_pid=$id.pid;retained_handle_exited=$r.process.HasExited;cim_row_present=($null -ne $legacy);command_line_available=($null -ne $legacy -and [bool]$legacy.CommandLine)}
                }
                $result=Invoke-C2OwnProcessCleanup $r
                if($result.status -notin 'EXITED_PROVEN','STOPPED_OWN_PROVEN' -or -not $r.process.HasExited){throw 'real cleanup incomplete'}
                if((Invoke-C2OwnProcessCleanup $r).status -ne 'ALREADY_CLEAN'){throw 'duplicate real cleanup'}
                $evidence[$fixture]=$result
            }finally{$r.process.Dispose();$p.Dispose()}
        }
    }
    $evidence.status='PASS';$evidence.count=$count
    "PASS $count cleanup tests including three real bounded own-process fixtures; no VM."
}catch{$evidence.status='FAIL';$evidence.error=$_.Exception.Message;throw}
finally{if($ReportPath){$evidence|ConvertTo-Json -Depth 16|Set-Content -LiteralPath $ReportPath -Encoding utf8}}
