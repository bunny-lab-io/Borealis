param(
    [Parameter(Mandatory=$true)] [string]$SupName,
    [Parameter(Mandatory=$true)] [string]$PythonExe,
    [Parameter(Mandatory=$true)] [string]$SupScript,
    [Parameter(Mandatory=$true)] [string]$WdName,
    [Parameter(Mandatory=$true)] [string]$WdSource,
    # Optional per-user logon task (to avoid a second UAC prompt elsewhere)
    [string]$UserTaskName = 'Borealis Agent',
    [string]$UserExe = $null,
    [string]$UserScript = $null,
    [string]$UserPrincipal = $null
)

$ErrorActionPreference = 'Continue'

try {
    # Prepare principal
    $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest

    # Supervisor task
    try { Unregister-ScheduledTask -TaskName $SupName -Confirm:$false -ErrorAction SilentlyContinue } catch {}
    $supArg = ('-W ignore::SyntaxWarning "{0}"' -f $SupScript)
    $supAction = New-ScheduledTaskAction -Execute $PythonExe -Argument $supArg
    $supTrigger = New-ScheduledTaskTrigger -AtStartup
    $supSettings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -Hidden -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
    Register-ScheduledTask -TaskName $SupName -Action $supAction -Trigger $supTrigger -Settings $supSettings -Principal $principal -Force | Out-Null

    # Watchdog script deployment
    $wdDest = Join-Path $env:ProgramData 'Borealis\\watchdog.ps1'
    New-Item -ItemType Directory -Force -Path (Split-Path $wdDest -Parent) | Out-Null
    Copy-Item -Path $WdSource -Destination $wdDest -Force

    # Watchdog task (5-min repetition for 1 year)
    try { Unregister-ScheduledTask -TaskName $WdName -Confirm:$false -ErrorAction SilentlyContinue } catch {}
    $wdArg = ('-NoProfile -ExecutionPolicy Bypass -File "{0}" -SupervisorTaskName "{1}"' -f $wdDest, $SupName)
    $wdAction = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $wdArg
    $wdTrigger = New-ScheduledTaskTrigger -Once -At ([datetime]::Now.AddMinutes(1)) -RepetitionInterval (New-TimeSpan -Minutes 5) -RepetitionDuration (New-TimeSpan -Days 365)
    $wdSettings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -Hidden -ExecutionTimeLimit ([TimeSpan]::Zero)
    Register-ScheduledTask -TaskName $WdName -Action $wdAction -Trigger $wdTrigger -Settings $wdSettings -Principal $principal -Force | Out-Null

    # Ensure supervisor is running
    Start-ScheduledTask -TaskName $SupName | Out-Null

    # Optionally ensure a per-user logon task for the tray helper without a separate elevation
    if ($UserExe -and $UserScript) {
        try {
            $targetUser = $UserPrincipal
            if (-not $targetUser -or $targetUser -eq '') {
                $targetUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
            }
            try { Unregister-ScheduledTask -TaskName $UserTaskName -Confirm:$false -ErrorAction SilentlyContinue } catch {}
            $usrArg   = ('-W ignore::SyntaxWarning "{0}"' -f $UserScript)
            $usrAction = New-ScheduledTaskAction -Execute $UserExe -Argument $usrArg
            $usrTrig   = New-ScheduledTaskTrigger -AtLogOn
            $usrSet    = New-ScheduledTaskSettingsSet -Hidden -ExecutionTimeLimit ([TimeSpan]::Zero)
            $usrPrin   = New-ScheduledTaskPrincipal -UserId $targetUser -LogonType Interactive -RunLevel Limited
            Register-ScheduledTask -TaskName $UserTaskName -Action $usrAction -Trigger $usrTrig -Settings $usrSet -Principal $usrPrin -Force | Out-Null
            Start-ScheduledTask -TaskName $UserTaskName | Out-Null
        } catch {
            Write-Warning "Failed to register per-user logon task '$UserTaskName': $_"
        }
    }

} catch {
    Write-Error $_
    exit 1
}
