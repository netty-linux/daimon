# No OS mutations, VM calls or fault injection. Deterministic proof rejection tests.
$ErrorActionPreference='Stop';Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'harness2-c2-helper.psm1')
$count=0
function Test($name,[scriptblock]$body){& $body;$script:count++;"PASS $name"}
function Fixture {
    $date='2026-10-24T00:00:00Z'
    @{d=@{origin_ticks=1000;frequency=1000;limit_seconds=90}
      f=@{method='HeartbeatStall30';controller_isolation=@{verified=$true;pid=10};watchdog_pid=20;watchdog_started_utc=$date;vm_id='own';operation_id='op';last_heartbeat_ticks=4000}
      w=@{reason='controller-heartbeat-lost';poweroff_command_issued=$true;isolation=@{verified=$true;pid=20;started_utc=$date};vm_id='own';operation_id='op';temporal_verdict='OFF_BY_DEADLINE'
        timing=@{state_before_stop='Running';confirmed_off=$true;stop_requested_ticks=15000;stop_command_invoked_ticks=15100;stop_command_returned_ticks=15200;off_observed_ticks=16000}}}
}
Test 'own running target plus independent watchdog command and timely Off can prove emergency route'{
    $x=Fixture;if((Get-C2EmergencyProofVerdict $x.f $x.w $x.d) -ne 'WATCHDOG_POWEROFF_PROVEN'){throw 'healthy proof refused'}
}
foreach($case in 'controller-job','watcher-job','foreign-vm','foreign-op','same-process','wrong-pid','wrong-start','wrong-reason','no-command','already-off','late','unconfirmed','request-before-fault','request-too-late','off-too-late','absolute-late','clock','timeout','dispatch-error'){
    Test "emergency proof refuses $case" {
        $x=Fixture
        switch($case){
          'controller-job'{$x.f.controller_isolation.verified=$false};'watcher-job'{$x.w.isolation.verified=$false}
          'foreign-vm'{$x.w.vm_id='foreign'};'foreign-op'{$x.w.operation_id='foreign'};'same-process'{$x.f.controller_isolation.pid=20}
          'wrong-pid'{$x.w.isolation.pid=21};'wrong-start'{$x.w.isolation.started_utc='2026-10-25T00:00:00Z'}
          'wrong-reason'{$x.w.reason='watchdog-boot-deadline'};'no-command'{$x.w.poweroff_command_issued=$false}
          'already-off'{$x.w.timing.state_before_stop='Off'};'late'{$x.w.temporal_verdict='OFF_LATE'};'unconfirmed'{$x.w.timing.confirmed_off=$false}
          'request-before-fault'{$x.w.timing.stop_requested_ticks=3000};'request-too-late'{$x.w.timing.stop_requested_ticks=18001;$x.w.timing.stop_command_invoked_ticks=18100;$x.w.timing.stop_command_returned_ticks=18200;$x.w.timing.off_observed_ticks=19000}
          'off-too-late'{$x.w.timing.off_observed_ticks=34001}
          'absolute-late'{$x.f.last_heartbeat_ticks=81000;$x.w.timing.stop_requested_ticks=92000;$x.w.timing.stop_command_invoked_ticks=92100;$x.w.timing.stop_command_returned_ticks=92200;$x.w.timing.off_observed_ticks=93000}
          'clock'{$x.d.frequency=0};'timeout'{$x.w.timing.confirmation_timeout=$true};'dispatch-error'{$x.w.timing.command_error='failed'}
        }
        if((Get-C2EmergencyProofVerdict $x.f $x.w $x.d) -eq 'WATCHDOG_POWEROFF_PROVEN'){throw 'unsafe proof accepted'}
    }
}
Test 'emergency CLI requires different explicit acknowledgement before any imports or VM action'{
    $failed=$false
    try{& (Join-Path $PSScriptRoot 'harness2-c2-firstboot.ps1') -Root 'D:\never-mutated' -IsoPath 'D:\absent.iso' -OperationId ([guid]::Empty) -VMId ([guid]::Empty) -FaultInjection HeartbeatStall30 -ReceiptPath 'D:\never-written.json'}catch{if($_.Exception.Message -match 'Specific boot/cap/poweroff authorization'){$failed=$true}else{throw}}
    if(-not $failed){throw 'fault accepted without new approval'}
}
Test 'supervisor cannot launch helpers or inspect VM without separate approval'{
    $failed=$false
    try{& (Join-Path $PSScriptRoot 'harness2-c2-emergency-probe.ps1') -Root 'D:\never-mutated' -IsoPath 'D:\absent.iso' -OperationId ([guid]::Empty) -VMId ([guid]::Empty) -ReceiptPath 'D:\never-written.json'}catch{if($_.Exception.Message -match 'Separate human emergency-trial approval'){$failed=$true}else{throw}}
    if(-not $failed){throw 'supervisor accepted missing approval'}
}
"PASS $count emergency policy tests; VM NOT ACCESSED."
