[CmdletBinding()]
param()

$scriptDir = Split-Path $MyInvocation.MyCommand.Path -Parent
$symbols = @{
    Success = [char]0x2705
    Running = [char]0x23F3
    Fail    = [char]0x274C
    Info    = [char]0x2139
}

function Write-ProgressStep {
    param (
        [string]$Message,
        [string]$Status = $symbols["Info"]
    )
    Write-Host "`r$Status $Message... " -NoNewline
}

function Run-Step {
    param (
        [string]     $Message,
        [scriptblock]$Script
    )
    Write-ProgressStep -Message $Message -Status "$($symbols.Running)"
    try {
        & $Script
        if ($LASTEXITCODE -eq 0 -or $?) {
            Write-Host "`r$($symbols.Success) $Message                        "
        } else {
            throw "Non-zero exit code"
        }
    } catch {
        Write-Host "`r$($symbols.Fail) $Message - Failed: $_                        " -ForegroundColor Red
        throw
    }
}

function Stop-AgentScheduledTasks {
    param(
        [string[]]$TaskNames
    )

    $stopped = @()
    foreach ($name in $TaskNames) {
        $taskExists = $false
        try {
            $null = Get-ScheduledTask -TaskName $name -ErrorAction Stop
            $taskExists = $true
        } catch {
            try {
                schtasks.exe /Query /TN "$name" 2>$null | Out-Null
                if ($LASTEXITCODE -eq 0) { $taskExists = $true }
            } catch {}
        }

        if (-not $taskExists) { continue }

        Write-Host "Stopping scheduled task: $name" -ForegroundColor Yellow
        $stopped += $name
        try { Stop-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue } catch {}
        try { schtasks.exe /End /TN "$name" /F 2>$null | Out-Null } catch {}
        try {
            for ($i = 0; $i -lt 20; $i++) {
                $info = Get-ScheduledTaskInfo -TaskName $name -ErrorAction Stop
                if ($info.State -ne 'Running' -and $info.State -ne 'Queued') { break }
                Start-Sleep -Milliseconds 500
            }
        } catch {}
    }

    return ,$stopped
}

function Start-AgentScheduledTasks {
    param(
        [string[]]$TaskNames
    )

    foreach ($name in $TaskNames) {
        Write-Host "Restarting scheduled task: $name" -ForegroundColor Green
        try {
            Start-ScheduledTask -TaskName $name -ErrorAction Stop | Out-Null
            continue
        } catch {}

        try { schtasks.exe /Run /TN "$name" 2>$null | Out-Null } catch {}
    }
}

function Get-BorealisServerUrl {
    param(
        [string]$AgentRoot
    )

    $serverBaseUrl = $env:BOREALIS_SERVER_URL
    if (-not $serverBaseUrl) {
        try {
            if (-not $AgentRoot) { $AgentRoot = $scriptDir }
            $settingsDir = Join-Path $AgentRoot 'Settings'
            $serverUrlFile = Join-Path $settingsDir 'server_url.txt'
            if (Test-Path $serverUrlFile -PathType Leaf) {
                $content = Get-Content -Path $serverUrlFile -Raw -ErrorAction Stop
                if ($content) { $serverBaseUrl = $content.Trim() }
            }
        } catch {}
    }

    if (-not $serverBaseUrl) { $serverBaseUrl = 'http://localhost:5000' }
    return $serverBaseUrl.Trim()
}

function Get-AgentServiceId {
    param(
        [string]$AgentRoot
    )

    if (-not $AgentRoot) { $AgentRoot = $scriptDir }
    $settingsDir = Join-Path $AgentRoot 'Settings'
    $candidates = @(
        (Join-Path $settingsDir 'agent_settings_svc.json')
        (Join-Path $settingsDir 'agent_settings.json')
    )

    foreach ($path in $candidates) {
        try {
            if (Test-Path $path -PathType Leaf) {
                $raw = Get-Content -Path $path -Raw -ErrorAction Stop
                if (-not $raw) { continue }
                $cfg = $raw | ConvertFrom-Json -ErrorAction Stop
                $value = ($cfg.agent_id)
                if ($value) { return ($value.ToString()).Trim() }
            }
        } catch {}
    }

    return ''
}

function Invoke-AgentUpdateCheck {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerBaseUrl,

        [Parameter(Mandatory = $true)]
        [string]$AgentId
    )

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl)) {
        throw 'Server URL is blank; cannot perform update check.'
    }

    $base = $ServerBaseUrl.TrimEnd('/')
    $uri = "$base/api/agent/update_check"
    $payload = @{ agent_id = $AgentId } | ConvertTo-Json -Depth 3
    $headers = @{ 'User-Agent' = 'borealis-agent-updater' }

    $resp = Invoke-WebRequest -Uri $uri -Method Post -Headers $headers -Body $payload -ContentType 'application/json' -UseBasicParsing -ErrorAction Stop
    $json = $resp.Content | ConvertFrom-Json

    if ($resp.StatusCode -ne 200) {
        $message = $json.error
        if (-not $message) { $message = "HTTP $($resp.StatusCode)" }
        throw "Borealis server responded with an error: $message"
    }

    return $json
}

function Get-RepositoryCommitHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ProjectRoot
    )

    $candidates = @($ProjectRoot)
    $agentRootCandidate = Join-Path $ProjectRoot 'Agent\Borealis'
    if (Test-Path $agentRootCandidate -PathType Container) { $candidates += $agentRootCandidate }

    foreach ($root in $candidates) {
        try {
            $gitDir = Join-Path $root '.git'
            $headPath = Join-Path $gitDir 'HEAD'
            if (-not (Test-Path $headPath -PathType Leaf)) { continue }
            $head = (Get-Content -Path $headPath -Raw -ErrorAction Stop).Trim()
            if (-not $head) { continue }

            if ($head -match '^ref:\s*(.+)$') {
                $ref = $Matches[1].Trim()
                if ($ref) {
                    $refPath = $gitDir
                    foreach ($part in ($ref -split '/')) {
                        if ($part) { $refPath = Join-Path $refPath $part }
                    }
                    if (Test-Path $refPath -PathType Leaf) {
                        $commit = (Get-Content -Path $refPath -Raw -ErrorAction Stop).Trim()
                        if ($commit) { return $commit }
                    }
                    $packedRefs = Join-Path $gitDir 'packed-refs'
                    if (Test-Path $packedRefs -PathType Leaf) {
                        foreach ($line in Get-Content -Path $packedRefs -ErrorAction Stop) {
                            $trim = ($line).Trim()
                            if (-not $trim -or $trim.StartsWith('#') -or $trim.StartsWith('^')) { continue }
                            $parts = $trim.Split(' ', 2)
                            if ($parts.Count -ge 2 -and $parts[1].Trim() -eq $ref) {
                                $candidate = $parts[0].Trim()
                                if ($candidate) { return $candidate }
                            }
                        }
                    }
                }
            } else {
                $detached = $head.Split([Environment]::NewLine, [StringSplitOptions]::RemoveEmptyEntries)
                if ($detached.Length -gt 0) {
                    $candidate = $detached[0].Trim()
                    if ($candidate) { return $candidate }
                }
            }

            $fetchHead = Join-Path $gitDir 'FETCH_HEAD'
            if (Test-Path $fetchHead -PathType Leaf) {
                foreach ($line in Get-Content -Path $fetchHead -ErrorAction Stop) {
                    $trim = ($line).Trim()
                    if (-not $trim -or $trim.StartsWith('#')) { continue }
                    $split = $trim.Split(@("`t", ' '), [StringSplitOptions]::RemoveEmptyEntries)
                    if ($split.Count -gt 0) {
                        $candidate = $split[0].Trim()
                        if ($candidate) { return $candidate }
                    }
                }
            }
        } catch {}
    }

    return ''
}

function Get-StoredAgentHash {
    param(
        [string]$AgentRoot
    )

    if (-not $AgentRoot) { return '' }

    try {
        $settingsDir = Join-Path $AgentRoot 'Settings'
        $hashFile = Join-Path $settingsDir 'agent_hash.txt'
        if (Test-Path $hashFile -PathType Leaf) {
            $value = (Get-Content -Path $hashFile -Raw -ErrorAction Stop).Trim()
            return $value
        }
    } catch {}

    return ''
}

function Set-StoredAgentHash {
    param(
        [string]$AgentRoot,
        [string]$AgentHash
    )

    if ([string]::IsNullOrWhiteSpace($AgentRoot) -or [string]::IsNullOrWhiteSpace($AgentHash)) { return }

    try {
        $settingsDir = Join-Path $AgentRoot 'Settings'
        if (-not (Test-Path $settingsDir -PathType Container)) {
            New-Item -ItemType Directory -Force -Path $settingsDir | Out-Null
        }
        $hashFile = Join-Path $settingsDir 'agent_hash.txt'
        Set-Content -Path $hashFile -Value $AgentHash.Trim() -Encoding UTF8
    } catch {}
}

function Get-ServerRepositoryHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerBaseUrl
    )

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl)) { return $null }

    $base = $ServerBaseUrl.TrimEnd('/')
    $uri = "$base/api/agent/repo_hash"
    $headers = @{ 'User-Agent' = 'borealis-agent-updater' }

    try {
        $resp = Invoke-WebRequest -Uri $uri -Method Get -Headers $headers -UseBasicParsing -ErrorAction Stop
        $json = $resp.Content | ConvertFrom-Json
        return $json
    } catch {
        return $null
    }
}

function Submit-AgentHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerBaseUrl,

        [Parameter(Mandatory = $true)]
        [string]$AgentId,

        [Parameter(Mandatory = $true)]
        [string]$AgentHash
    )

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl) -or [string]::IsNullOrWhiteSpace($AgentId) -or [string]::IsNullOrWhiteSpace($AgentHash)) {
        return
    }

    $base = $ServerBaseUrl.TrimEnd('/')
    $uri = "$base/api/agent/agent_hash"
    $payload = @{ agent_id = $AgentId; agent_hash = $AgentHash } | ConvertTo-Json -Depth 3
    $headers = @{ 'User-Agent' = 'borealis-agent-updater' }

    $resp = Invoke-WebRequest -Uri $uri -Method Post -Headers $headers -Body $payload -ContentType 'application/json' -UseBasicParsing -ErrorAction Stop
    try {
        $json = $resp.Content | ConvertFrom-Json
        return $json
    } catch {
        return $null
    }
}

function Invoke-BorealisUpdate {
    param(
        [switch]$Silent
    )

    $updateZip = Join-Path $scriptDir "Update_Staging\main.zip"
    $updateDir = Join-Path $scriptDir "Update_Staging\Borealis-main"
    $preservePath = Join-Path $scriptDir "Data\Server\Python_API_Endpoints\Tesseract-OCR"
    $preserveBackupPath = Join-Path $scriptDir "Update_Staging\Tesseract-OCR"

    Run-Step "Updating: Move Tesseract-OCR Folder Somewhere Safe to Restore Later" {
        if (Test-Path $preservePath) {
            $stagingPath = Join-Path $scriptDir "Update_Staging"
            if (-not (Test-Path $stagingPath)) { New-Item -ItemType Directory -Force -Path $stagingPath | Out-Null }
            Move-Item -Path $preservePath -Destination $preserveBackupPath -Force
        }
    }

    Run-Step "Updating: Clean Up Folders to Prepare for Update" {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue `
            (Join-Path $scriptDir "Data"), `
            (Join-Path $scriptDir "Server\web-interface\src"), `
            (Join-Path $scriptDir "Server\web-interface\build"), `
            (Join-Path $scriptDir "Server\web-interface\public"), `
            (Join-Path $scriptDir "Server\Borealis")
    }

    Run-Step "Updating: Create Update Staging Folder" {
        $stagingPath = Join-Path $scriptDir "Update_Staging"
        if (-not (Test-Path $stagingPath)) { New-Item -ItemType Directory -Force -Path $stagingPath | Out-Null }
        Set-Variable -Name updateZip -Scope 1 -Value (Join-Path $stagingPath "main.zip")
        Set-Variable -Name updateDir -Scope 1 -Value (Join-Path $stagingPath "Borealis-main")
    }

    Run-Step "Updating: Download Update" { Invoke-WebRequest -Uri "https://github.com/bunny-lab-io/Borealis/archive/refs/heads/main.zip" -OutFile $updateZip }
    Run-Step "Updating: Extract Update Files" { Expand-Archive -Path $updateZip -DestinationPath (Join-Path $scriptDir "Update_Staging") -Force }
    Run-Step "Updating: Copy Update Files into Production Borealis Root Folder" { Copy-Item "$updateDir\*" $scriptDir -Recurse -Force }

    Run-Step "Updating: Restore Tesseract-OCR Folder" {
        $restorePath = Join-Path $scriptDir "Data\Server\Python_API_Endpoints"
        if (Test-Path $preserveBackupPath) {
            if (-not (Test-Path $restorePath)) { New-Item -ItemType Directory -Force -Path $restorePath | Out-Null }
            Move-Item -Path $preserveBackupPath -Destination $restorePath -Force
        }
    }

    Run-Step "Updating: Clean Up Update Staging Folder" { Remove-Item -Recurse -Force -ErrorAction SilentlyContinue (Join-Path $scriptDir "Update_Staging") }

    if (-not $Silent) {
        Write-Host "Unattended Borealis update completed." -ForegroundColor Green
    }
}

function Invoke-BorealisAgentUpdate {
    Write-Host "==================================="
    Write-Host "Borealis - Automation Platform Updater Script"
    Write-Host "==================================="

    $agentRootCandidate = Join-Path $scriptDir 'Agent\Borealis'
    $agentRoot = $scriptDir
    if (Test-Path $agentRootCandidate -PathType Container) {
        try {
            $agentRoot = (Resolve-Path -Path $agentRootCandidate -ErrorAction Stop).Path
        } catch {
            $agentRoot = $agentRootCandidate
        }
    }

    $currentHash = Get-RepositoryCommitHash -ProjectRoot $scriptDir
    if ($currentHash) {
        Set-StoredAgentHash -AgentRoot $agentRoot -AgentHash $currentHash
    } else {
        $storedHash = Get-StoredAgentHash -AgentRoot $agentRoot
        if ($storedHash) { $currentHash = $storedHash }
    }

    $serverBaseUrl = Get-BorealisServerUrl -AgentRoot $agentRoot
    $agentId = Get-AgentServiceId -AgentRoot $agentRoot

    $serverRepoInfo = Get-ServerRepositoryHash -ServerBaseUrl $serverBaseUrl
    $serverHash = ''
    if ($serverRepoInfo) {
        try { $serverHash = (($serverRepoInfo.sha) -as [string]).Trim() } catch { $serverHash = '' }
    }

    $updateMode = $env:update_mode
    if ($updateMode) { $updateMode = $updateMode.ToLowerInvariant() } else { $updateMode = 'update' }
    $forceUpdate = $updateMode -eq 'force_update'

    $updateInfo = $null
    $shouldUpdate = $forceUpdate

    if (-not $forceUpdate) {
        if (-not $agentId) {
            Write-Host "Agent ID unavailable; cannot request update status from server." -ForegroundColor Yellow
            Write-Host "⚠️ Borealis update aborted."
            return
        }

        try {
            $updateInfo = Invoke-AgentUpdateCheck -ServerBaseUrl $serverBaseUrl -AgentId $agentId
            $shouldUpdate = [bool]($updateInfo.update_available)
            if (-not $serverHash -and $updateInfo.repo_hash) { $serverHash = ($updateInfo.repo_hash).ToString().Trim() }
        } catch {
            Write-Host ("Failed to contact Borealis server for update status: {0}" -f $_.Exception.Message) -ForegroundColor Yellow
            Write-Host "⚠️ Borealis update aborted."
            return
        }
    }

    if ($currentHash) {
        Write-Host ("Local Agent Hash: {0}" -f $currentHash)
    } else {
        Write-Host "Local Agent Hash: unavailable"
    }

    if ($serverHash) {
        Write-Host ("Borealis Server Hash: {0}" -f $serverHash)
    } else {
        Write-Host "Borealis Server Hash: unavailable"
    }

    if ($forceUpdate) {
        Write-Host "Server update check bypassed (force update requested)."
    } elseif ($updateInfo) {
        if ($shouldUpdate) {
            Write-Host "Server reports agent hash mismatch (update required)."
        } else {
            Write-Host "Server reports agent is current." -ForegroundColor Green
            if ($serverHash) {
                Set-StoredAgentHash -AgentRoot $agentRoot -AgentHash $serverHash
            }
            Write-Host "✅ Borealis - Automation Platform Already Up-to-Date"
            return
        }
    } else {
        Write-Host "Server response unavailable; cannot continue." -ForegroundColor Yellow
        Write-Host "⚠️ Borealis update aborted."
        return
    }

    $mutex = $null
    $gotMutex = $false
    $managedTasks = @()
    try {
        $mutex = New-Object System.Threading.Mutex($false, 'Global\BorealisUpdate')
        $gotMutex = $mutex.WaitOne(0)
        if (-not $gotMutex) {
            Write-Verbose 'Another update is already running (mutex held). Exiting quietly.'
            Write-Host "⚠️ Borealis update already in progress on this device."
            return
        }

        $staging = Join-Path $scriptDir 'Update_Staging'
        $zipPath = Join-Path $staging 'main.zip'
        if (Test-Path $zipPath) {
            Write-Verbose "Pre-flight: removing existing $zipPath"
            for ($i = 1; $i -le 10; $i++) {
                try {
                    Remove-Item -LiteralPath $zipPath -Force -ErrorAction Stop
                    break
                } catch {
                    Start-Sleep -Milliseconds (100 * $i)
                    if ($i -eq 10) {
                        throw "Pre-flight delete failed; $zipPath appears locked by another process."
                    }
                }
            }
        }

        $managedTasks = Stop-AgentScheduledTasks -TaskNames @('Borealis Agent','Borealis Agent (UserHelper)')

        $updateSucceeded = $false
        try {
            Invoke-BorealisUpdate -Silent
            $updateSucceeded = $true
        } finally {
            if ($managedTasks.Count -gt 0) {
                Start-AgentScheduledTasks -TaskNames $managedTasks
            }
        }

        if (-not $updateSucceeded) {
            throw 'Borealis update failed.'
        }

        $newHash = Get-RepositoryCommitHash -ProjectRoot $scriptDir
        if (-not $newHash -and $updateInfo -and $updateInfo.repo_hash) {
            $newHash = ($updateInfo.repo_hash).ToString().Trim()
        }
        if (-not $newHash -and $agentId) {
            try {
                $postUpdateInfo = Invoke-AgentUpdateCheck -ServerBaseUrl $serverBaseUrl -AgentId $agentId
                if ($postUpdateInfo -and $postUpdateInfo.repo_hash) {
                    $newHash = ($postUpdateInfo.repo_hash).ToString().Trim()
                }
            } catch {
                Write-Verbose ("Post-update hash retrieval failed: {0}" -f $_.Exception.Message)
            }
        }

        if ($newHash) {
            Set-StoredAgentHash -AgentRoot $agentRoot -AgentHash $newHash
            Write-Host ("Submitting agent hash to server: {0}" -f $newHash)
            try {
                if ($agentId) {
                    $submitResult = Submit-AgentHash -ServerBaseUrl $serverBaseUrl -AgentId $agentId -AgentHash $newHash
                    if ($submitResult -and ($submitResult.status -eq 'ok')) {
                        Write-Host "Server agent_hash database record updated successfully."
                    } elseif ($submitResult -and ($submitResult.status -eq 'ignored')) {
                        Write-Host "Server ignored agent_hash update (agent not registered)." -ForegroundColor DarkYellow
                    } else {
                        Write-Host "Server agent_hash update response unrecognized." -ForegroundColor DarkYellow
                    }
                } else {
                    Write-Host "Agent ID unavailable; skipping agent hash submission." -ForegroundColor DarkYellow
                }
            } catch {
                Write-Verbose ("Failed to submit agent hash: {0}" -f $_.Exception.Message)
            }
        } elseif ($serverHash) {
            Set-StoredAgentHash -AgentRoot $agentRoot -AgentHash $serverHash
        } else {
            Write-Host "Unable to determine repository hash for submission; server hash not updated." -ForegroundColor DarkYellow
        }

        Write-Host "✅ Borealis - Automation Platform Successfully Updated"
    } finally {
        if ($mutex -and $gotMutex) { $mutex.ReleaseMutex() | Out-Null }
        if ($mutex) { $mutex.Dispose() }
    }
}

Invoke-BorealisAgentUpdate
