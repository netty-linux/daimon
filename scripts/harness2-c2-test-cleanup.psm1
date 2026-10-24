# Offline test helpers only. Retained process handles identify instances, never PID alone.
Set-StrictMode -Version Latest
function Get-C2CleanupDecision($Snapshot) {
    if(-not $Snapshot.registered -or -not $Snapshot.handle_valid){return 'INDETERMINATE'}
    if($Snapshot.exited -eq $true){return 'EXITED_PROVEN'}
    if($Snapshot.exited -ne $false){return 'INDETERMINATE'}
    if($Snapshot.metadata -eq 'MATCH'){return 'LIVE_OWN'}
    if($Snapshot.metadata -eq 'MISMATCH'){return 'LIVE_DIVERGENT'}
    return 'INDETERMINATE'
}
function Test-C2OwnMetadata($Identity,$Metadata) {
    if(-not $Metadata -or -not $Metadata.CommandLine -or -not $Metadata.ExecutablePath){return 'UNKNOWN'}
    $suffix='-NoProfile -NonInteractive -File "'+$Identity.entry+'" -Mode '+$Identity.role+' -CaseDirectory "'+$Identity.directory+'"'
    if($Metadata.ProcessId -ne $Identity.pid -or $Metadata.ParentProcessId -ne $Identity.creator_pid -or
       $Metadata.ExecutablePath -ine $Identity.path -or -not $Metadata.CommandLine.EndsWith($suffix,[StringComparison]::OrdinalIgnoreCase)){return 'MISMATCH'}
    return 'MATCH'
}
function Register-C2OwnProcess($Identity,[string]$ExpectedEntry,[string]$ExpectedDirectory,[string]$ExpectedExe,[int]$ExpectedCreator,[string]$ExpectedRole) {
    if($Identity.entry -ine $ExpectedEntry -or $Identity.directory -ine $ExpectedDirectory -or $Identity.path -ine $ExpectedExe -or
       $Identity.creator_pid -ne $ExpectedCreator -or $Identity.role -cne $ExpectedRole){throw 'Own helper manifest provenance mismatch.'}
    $p=[Diagnostics.Process]::GetProcessById([int]$Identity.pid)
    try {
        # Acquire and retain a kernel handle BEFORE reading identity or allowing the fault.
        $null=$p.SafeHandle
        if($p.HasExited -or $p.StartTime.ToUniversalTime().Ticks -ne ([datetime]$Identity.started_utc).ToUniversalTime().Ticks -or $p.Path -ine $ExpectedExe){throw 'Own helper registration PID/start/path mismatch or early exit.'}
        $m=Get-CimInstance Win32_Process -Filter ('ProcessId = '+[int]$Identity.pid) -OperationTimeoutSec 5 -ErrorAction Stop
        $match=Test-C2OwnMetadata $Identity $m
        if($match -ne 'MATCH' -or $p.HasExited){throw ('Own helper registration metadata '+$match+' or early exit.')}
        return @{process=$p;identity=$Identity;registered=$true;completed=$false;registration=@{metadata='MATCH';utc=[datetime]::UtcNow.ToString('o');handle_retained=$true}}
    } catch {$p.Dispose();throw}
}
function Get-C2OwnProcessSnapshot($Registration) {
    $s=@{registered=$Registration.registered;handle_valid=$false;exited=$null;metadata='UNKNOWN'}
    try {
        $p=$Registration.process
        $s.handle_valid=(-not $p.SafeHandle.IsClosed -and -not $p.SafeHandle.IsInvalid)
        $s.exited=$p.HasExited
        if($s.exited){return $s}
        if($p.StartTime.ToUniversalTime().Ticks -ne ([datetime]$Registration.identity.started_utc).ToUniversalTime().Ticks -or $p.Path -ine $Registration.identity.path){$s.metadata='MISMATCH';return $s}
        try {
            $m=Get-CimInstance Win32_Process -Filter ('ProcessId = '+[int]$Registration.identity.pid) -OperationTimeoutSec 5 -ErrorAction Stop
            $s.metadata=Test-C2OwnMetadata $Registration.identity $m
            if($m){$s.observed_metadata=@{pid=$m.ProcessId;creator_pid=$m.ParentProcessId;path=$m.ExecutablePath;command_line=$m.CommandLine}}
        } catch {$s.error=$_.Exception.Message}
        # A process can exit during CIM. Its retained, previously verified handle is
        # authoritative for that original instance; no lookup/kill of a replacement PID.
        $s.exited=$p.HasExited
    } catch {$s.error=$_.Exception.Message}
    return $s
}
function Invoke-C2OwnProcessCleanup($Registration,[scriptblock]$Observe,[scriptblock]$Terminate,[scriptblock]$Confirm) {
    if($Registration.completed){
        $prior=@{status='ALREADY_CLEAN';pid=$Registration.identity.pid;identity=$Registration.identity;registration=$Registration.registration;kill_issued=$false}
        if($Registration.ContainsKey('completion_error')){$prior.status='EXITED_WITH_COMMAND_ERROR';$prior.error=$Registration.completion_error}
        return $prior
    }
    if(-not $Observe){$Observe={param($r)Get-C2OwnProcessSnapshot $r}}
    if(-not $Terminate){$Terminate={param($r)$r.process.Kill()}}
    if(-not $Confirm){$Confirm={param($r)$r.process.WaitForExit(5000)}}
    $snapshot=& $Observe $Registration;$decision=Get-C2CleanupDecision $snapshot
    $result=@{status=$decision;pid=$Registration.identity.pid;identity=$Registration.identity;registration=$Registration.registration;snapshot=$snapshot;kill_issued=$false}
    if($decision -eq 'EXITED_PROVEN'){$Registration.completed=$true;return $result}
    if($decision -ne 'LIVE_OWN'){return $result}
    # Revalidate immediately before termination; never kill by PID, only the held handle.
    $snapshot=& $Observe $Registration;$decision=Get-C2CleanupDecision $snapshot
    $result.snapshot=$snapshot;$result.status=$decision
    if($decision -eq 'EXITED_PROVEN'){$Registration.completed=$true;return $result}
    if($decision -ne 'LIVE_OWN'){return $result}
    try {$result.kill_issued=$true;& $Terminate $Registration;$null=& $Confirm $Registration}
    catch {$result.error=$_.Exception.Message}
    $result.after=& $Observe $Registration
    if((Get-C2CleanupDecision $result.after) -eq 'EXITED_PROVEN'){
        $Registration.completed=$true;$result.status=if($result.ContainsKey('error')){'EXITED_WITH_COMMAND_ERROR'}else{'STOPPED_OWN_PROVEN'}
        if($result.ContainsKey('error')){$Registration.completion_error=$result.error}
    }else{$result.status='INDETERMINATE'}
    return $result
}
Export-ModuleMember -Function Get-C2CleanupDecision,Test-C2OwnMetadata,Register-C2OwnProcess,Get-C2OwnProcessSnapshot,Invoke-C2OwnProcessCleanup
