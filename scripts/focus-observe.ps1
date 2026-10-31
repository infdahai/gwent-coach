$ErrorActionPreference = 'Stop'
$game = Get-Process Gwent | Select-Object -First 1
$shell = New-Object -ComObject WScript.Shell
if (-not $shell.AppActivate($game.Id)) { throw 'Unable to activate Gwent window.' }
Start-Sleep -Milliseconds 800
& C:\coach\coach.exe -out C:\coach\runs
exit $LASTEXITCODE
