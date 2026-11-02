# One resident task in the signed-in user's interactive desktop. Run in Windows
# PowerShell as that same user (including through SSH). Does not launch a match.
[CmdletBinding()]
param(
    [ValidateSet('Install', 'Start', 'Status', 'Stop')]
    [string]$Action = 'Status',
    [string]$Exe,
    [string]$StateDir = (Join-Path $env:LOCALAPPDATA 'GwentCoachControl'),
    [string]$StopFile,
    [string]$SessionConfig,
    [string]$TaskName = 'GwentCoachResident',
    [ValidateRange(1024, 65535)][int]$Port = 17391
)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$StateDir = [IO.Path]::GetFullPath($StateDir)
$configPath = Join-Path $StateDir 'host-config.json'
$tokenPath = Join-Path $StateDir 'token'

if ($Action -eq 'Install') {
    if (-not $Exe -or -not $StopFile) { throw 'Install requires -Exe and -StopFile' }
    if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
        throw 'Task already exists. Use Start/Status/Stop. To change configuration, stop and inspect it before unregistering and reinstalling.'
    }
    $Exe = (Resolve-Path -LiteralPath $Exe).Path
    $StopFile = [IO.Path]::GetFullPath($StopFile)
    if ($SessionConfig) { $SessionConfig = (Resolve-Path -LiteralPath $SessionConfig).Path }
    if (Test-Path -LiteralPath $StateDir) {
        throw 'Install requires a new state directory; existing receipts and claims must be preserved.'
    }
    New-Item -ItemType Directory -Path $StateDir | Out-Null
    # Windows ignores Unix file modes: protect tokens, captured images and logs
    # through an inheritable ACL before creating any sensitive files.
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in @($identity.User, (New-Object Security.Principal.SecurityIdentifier('S-1-5-18')))) {
        $rule = New-Object Security.AccessControl.FileSystemAccessRule($sid, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $StateDir -AclObject $acl
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    $token = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
    [IO.File]::WriteAllText($tokenPath, $token, (New-Object Text.UTF8Encoding($false)))
    $arguments = @('control', 'host', '-address', "127.0.0.1:$Port", '-state', (Join-Path $StateDir 'state'), '-token-file', $tokenPath, '-stop-file', $StopFile)
    if ($SessionConfig) { $arguments += @('-session-config', $SessionConfig) }
    @{ Exe = $Exe; Arguments = $arguments; Port = $Port } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $configPath -Encoding UTF8
    $launcher = Join-Path $StateDir 'host-launch.ps1'
    @'
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$config = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'host-config.json') -Raw -Encoding UTF8 | ConvertFrom-Json
Set-Location -LiteralPath (Split-Path -Parent $config.Exe)
$hostArgs = @($config.Arguments)
# Windows PowerShell 5 treats redirected native stderr as ErrorRecord objects.
# The host writes lifecycle diagnostics there; they must not abort the launcher.
$ErrorActionPreference = 'Continue'
& $config.Exe @hostArgs 2>&1 | Out-File -LiteralPath (Join-Path $PSScriptRoot 'host.log') -Encoding UTF8 -Append
exit $LASTEXITCODE
'@ | Set-Content -LiteralPath $launcher -Encoding UTF8
    if ($launcher.Contains('"')) { throw 'State directory cannot contain a quote' }
    $taskAction = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument ('-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + $launcher + '"') -WorkingDirectory (Split-Path -Parent $Exe)
    $principal = New-ScheduledTaskPrincipal -UserId $identity.Name -LogonType Interactive -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    # Explicit lifecycle: no logon trigger, automatic restart, or per-run tasks.
    Register-ScheduledTask -TaskName $TaskName -Action $taskAction -Principal $principal -Settings $settings | Out-Null
    Write-Output "Installed $TaskName. Token saved privately; use Start after signing into the interactive desktop."
    return
}

$config = Get-Content -LiteralPath $configPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ($Action -eq 'Start') {
    Start-ScheduledTask -TaskName $TaskName
    Write-Output 'Start requested. Use Status to verify the listener; inspect host.log and task result if unavailable.'
} elseif ($Action -eq 'Stop') {
    # shutdown persists stop and waits for the active task before exiting. Do not
    # use Stop-ScheduledTask here: killing input mid-delivery is ambiguous.
    & $config.Exe control shutdown -url "http://127.0.0.1:$($config.Port)" -token-file $tokenPath
    if ($LASTEXITCODE -ne 0) { throw 'Shutdown request failed; inspect status/receipt before any retry' }
    Write-Output 'Shutdown requested. Verify the task has exited; the stop latch remains for reconciliation.'
} else {
    Get-ScheduledTask -TaskName $TaskName | Select-Object TaskName, State
    Get-ScheduledTaskInfo -TaskName $TaskName | Select-Object LastRunTime, LastTaskResult
    & $config.Exe control status -url "http://127.0.0.1:$($config.Port)" -token-file $tokenPath
    if ($LASTEXITCODE -ne 0) { throw 'Host unavailable; inspect host.log and scheduled task result' }
}
