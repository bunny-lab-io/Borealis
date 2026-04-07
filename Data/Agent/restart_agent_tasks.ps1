[CmdletBinding()]
param(
    [int]$InitialDelaySeconds = 4,
    [int]$RetryIntervalSeconds = 3,
    [int]$MaxWaitSeconds = 60
)

$ErrorActionPreference = 'Continue'

try {
    $scriptDir = Split-Path -Path $PSCommandPath -Parent
    $projectRoot = Resolve-Path (Join-Path $scriptDir '..\..')
    $logsDir = Join-Path $projectRoot 'Agent\Logs'
    if (-not (Test-Path $logsDir)) { New-Item -ItemType Directory -Path $logsDir -Force | Out-Null }
    $logPath = Join-Path $logsDir 'task_restart.log'

    function Write-RestartLog {
        param([string]$Message)
        if (-not $Message) { return }
        try {
            "[{0}] {1}" -f (Get-Date -Format s), $Message | Out-File -FilePath $logPath -Append -Encoding utf8
        } catch {}
    }

    function Invoke-TaskStart {
        param([string]$TaskName)
        if (-not $TaskName) { return $false }

        try {
            Start-ScheduledTask -TaskName $TaskName -ErrorAction Stop | Out-Null
            Write-RestartLog ("Started scheduled task '{0}' via Start-ScheduledTask." -f $TaskName)
            return $true
        } catch {
            Write-RestartLog ("Start-ScheduledTask failed for '{0}': {1}" -f $TaskName, $_.Exception.Message)
        }

        try {
            & schtasks.exe /Run /TN $TaskName 2>&1 | Out-Null
            if ($LASTEXITCODE -eq 0) {
                Write-RestartLog ("Started scheduled task '{0}' via schtasks.exe." -f $TaskName)
                return $true
            }
            Write-RestartLog ("schtasks.exe failed for '{0}' with exit code {1}." -f $TaskName, $LASTEXITCODE)
        } catch {
            Write-RestartLog ("schtasks.exe threw for '{0}': {1}" -f $TaskName, $_.Exception.Message)
        }

        return $false
    }

    Write-RestartLog ("Queued Borealis task restart helper with InitialDelaySeconds={0}, RetryIntervalSeconds={1}, MaxWaitSeconds={2}." -f $InitialDelaySeconds, $RetryIntervalSeconds, $MaxWaitSeconds)
    if ($InitialDelaySeconds -gt 0) { Start-Sleep -Seconds $InitialDelaySeconds }

    $pendingTasks = @('Borealis Agent', 'Borealis Agent (UserHelper)')
    $deadline = (Get-Date).AddSeconds([Math]::Max(1, $MaxWaitSeconds))

    while ($pendingTasks.Count -gt 0 -and (Get-Date) -lt $deadline) {
        $remaining = @()
        foreach ($taskName in $pendingTasks) {
            if (-not (Invoke-TaskStart -TaskName $taskName)) {
                $remaining += $taskName
            }
        }
        $pendingTasks = @($remaining)
        if ($pendingTasks.Count -le 0) { break }
        Start-Sleep -Seconds ([Math]::Max(1, $RetryIntervalSeconds))
    }

    if ($pendingTasks.Count -gt 0) {
        Write-RestartLog ("Timed out waiting to restart Borealis tasks: {0}" -f ($pendingTasks -join ', '))
        exit 1
    }

    Write-RestartLog 'Borealis scheduled-task restart helper completed successfully.'
    exit 0
} catch {
    try {
        $fallbackDir = Join-Path $env:ProgramData 'Borealis'
        if (-not (Test-Path $fallbackDir)) { New-Item -ItemType Directory -Path $fallbackDir -Force | Out-Null }
        $fallbackLog = Join-Path $fallbackDir 'task_restart.log'
        "[{0}] {1}" -f (Get-Date -Format s), $_.Exception.Message | Out-File -FilePath $fallbackLog -Append -Encoding utf8
    } catch {}
    exit 1
}
