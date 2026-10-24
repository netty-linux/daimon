@echo off
setlocal DisableDelayedExpansion
title DAIMON C2 - teste offline de independencia
set "C2_OUTPUT=%~dp0."
set "C2_REPORT=%~dp0C2-offline-result.json"
set "C2_STATUS=%~dp0C2-offline-status.txt"
set "C2_LOCK=%~dp0C2-offline-attempt.lock"
set "C2_PWSH=C:\Users\01 Bigode\.cache\codex-runtimes\codex-primary-runtime\dependencies\native\powershell\pwsh.exe"
set "C2_ENTRY=D:\Nullysh.DEV\Daimon-TRUE\scripts\harness2-c2-offline-entry.ps1"
echo Testes de helpers proprios. SEM VM, SEM UAC, SEM alteracao de politicas.
echo Execute este arquivo pelo Explorer, fora do ChatGPT Work e seu terminal.
if exist "%C2_REPORT%" goto existing
2>nul mkdir "%C2_LOCK%"
if errorlevel 1 goto locked
> "%C2_STATUS%" echo RUNNING - uma unica tentativa offline.
if not exist "%C2_PWSH%" goto runtime_blocked
if not exist "%C2_ENTRY%" goto runtime_blocked
rem A new console is intentional; do not use /B or share the CMD console.
start "C2 - helpers offline" /wait "%C2_PWSH%" -NoProfile -NonInteractive -File "%C2_ENTRY%" -OutputDirectory "%C2_OUTPUT%"
set "C2_EXIT=%ERRORLEVEL%"
if exist "%C2_REPORT%" goto reported
> "%C2_REPORT%" echo {"status":"BLOCKED","reason":"RUNNER_DID_NOT_PRODUCE_REPORT","engine_exit":%C2_EXIT%,"vm_access":false,"minimum_correction":"Review PowerShell policy/runtime or entry-file availability. No automatic elevation or policy bypass."}
> "%C2_STATUS%" echo BLOCKED - o runtime ou a politica impediu o relatorio. Nenhuma repeticao automatica.
goto reported
:runtime_blocked
set "C2_EXIT=2"
> "%C2_REPORT%" echo {"status":"BLOCKED","reason":"REVIEWED_RUNTIME_OR_ENTRY_MISSING","vm_access":false,"minimum_correction":"Restore the reviewed local files; do not install or bypass policy automatically."}
> "%C2_STATUS%" echo BLOCKED - runtime ou arquivo revisado ausente.
goto reported
:existing
echo Um relatorio ja existe. Os testes NAO serao repetidos.
set "C2_EXIT=2"
goto reported
:locked
echo BLOCKED - esta tentativa ja foi iniciada ou esta incompleta. Nao repetir.
echo Verifique os arquivos locais; nenhum lock sera removido automaticamente.
pause
exit /b 2
:reported
echo.
if exist "%C2_STATUS%" type "%C2_STATUS%"
echo Relatorio: "%C2_REPORT%"
echo Um PASS offline NAO autoriza boot. Solicite revisao do relatorio separadamente.
pause
exit /b %C2_EXIT%
