param(
    [Parameter(Mandatory=$true)][ValidateSet('click','hold','escape')][string]$Action,
    [Parameter(Mandatory=$true)][string]$Reference,
    [int]$X = 0,
    [int]$Y = 0,
    [string]$Executable = 'C:\coach\coach-input-v2.exe'
)
$ErrorActionPreference = 'Stop'
# Run this script in an interactive task with a hidden PowerShell window.
# Do not activate Gwent here: losing focus must cancel the pending action.
& $Executable -out C:\coach\input-runs -action $Action -reference $Reference -x $X -y $Y -stop-file C:\coach\PAUSE
exit $LASTEXITCODE
