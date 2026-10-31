$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()
$computer = Get-CimInstance Win32_ComputerSystem
$tasks = @(Get-ScheduledTask -TaskName 'GwentCoachMVP-Observe' -ErrorAction SilentlyContinue | Select-Object TaskName, State, Principal, Actions)
[pscustomobject]@{
    InteractiveUser = $computer.UserName
    Caller = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
    Games = @(Get-Process Gwent -ErrorAction SilentlyContinue | Select-Object Id, SessionId)
    ExistingTask = $tasks
    DeploymentExists = Test-Path 'C:\coach-mvp'
} | ConvertTo-Json -Depth 5
