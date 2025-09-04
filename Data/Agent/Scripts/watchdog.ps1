param(
    [string]$SupervisorTaskName = 'Borealis Agent - Supervisor'
)

$ErrorActionPreference = 'SilentlyContinue'
$task = Get-ScheduledTask -TaskName $SupervisorTaskName -ErrorAction SilentlyContinue
if (-not $task) { exit }
$st = $task.State
if ($st -eq 'Disabled') { Enable-ScheduledTask -TaskName $SupervisorTaskName | Out-Null }
if ($st -ne 'Running') { Start-ScheduledTask -TaskName $SupervisorTaskName | Out-Null }

