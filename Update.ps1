[CmdletBinding()]
param()

$scriptDir = Split-Path $MyInvocation.MyCommand.Path -Parent
$symbols = @{
    Success = [char]0x2705
    Running = [char]0x23F3
    Fail    = [char]0x274C
    Info    = [char]0x2139
}

$repositoryUrl = 'https://github.com/bunny-lab-io/Borealis.git'

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

function Get-GitExecutablePath {
    param(
        [string]$ProjectRoot
    )

    $candidates = @()
    if ($ProjectRoot) {
        $candidates += (Join-Path $ProjectRoot 'Dependencies\git\cmd\git.exe')
        $candidates += (Join-Path $ProjectRoot 'Dependencies\git\bin\git.exe')
    }

    foreach ($candidate in ($candidates | Select-Object -Unique)) {
        try {
            if (Test-Path $candidate -PathType Leaf) { return $candidate }
        } catch {}
    }

    return ''
}

function Invoke-GitCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$GitExe,

        [Parameter(Mandatory = $true)]
        [string]$WorkingDirectory,

        [Parameter(Mandatory = $true)]
        [string[]]$Arguments
    )

    if ([string]::IsNullOrWhiteSpace($GitExe) -or -not (Test-Path $GitExe -PathType Leaf)) {
        throw "Git executable not found at '$GitExe'"
    }

    if (-not (Test-Path $WorkingDirectory -PathType Container)) {
        throw "Working directory '$WorkingDirectory' does not exist."
    }

    $fullArgs = @('-C', $WorkingDirectory) + $Arguments
    $output = & $GitExe @fullArgs 2>&1
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        $joined = ($Arguments -join ' ')
        $message = "git $joined failed with exit code $exitCode."
        if ($output) {
            $message = "$message Output: $output"
        }
        throw $message
    }

    return $output
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

function Stop-AgentPythonProcesses {
    param(
        [string[]]$ProcessNames = @('python', 'pythonw')
    )

    foreach ($name in ($ProcessNames | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -Unique)) {
        $name = $name.Trim()
        if (-not $name) { continue }

        $processes = @()
        try {
            $processes = Get-Process -Name $name -ErrorAction Stop
        } catch {
            $processes = @()
        }

        foreach ($proc in $processes) {
            $procId = $null
            $procName = $null
            try {
                $procId = $proc.Id
                $procName = $proc.ProcessName
            } catch {}

            if ($procId -eq $null) { continue }

            if (-not $procName) { $procName = $name }

            $stopped = $false
            Write-Host "Stopping process: $procName (PID $procId)" -ForegroundColor Yellow

            try {
                Stop-Process -Id $procId -Force -ErrorAction Stop
                $stopped = $true
            } catch {
                Write-Host "Unable to stop process via Stop-Process: $procName (PID $procId). $_" -ForegroundColor DarkYellow
            }

            if (-not $stopped) {
                try {
                    $taskkillOutput = taskkill.exe /PID $procId /F 2>&1
                    if ($LASTEXITCODE -eq 0) {
                        $stopped = $true
                    } else {
                        if ($taskkillOutput) {
                            Write-Host "taskkill.exe returned exit code $LASTEXITCODE for PID $procId: $taskkillOutput" -ForegroundColor DarkYellow
                        }
                    }
                } catch {
                    Write-Host "Unable to stop process via taskkill.exe: $procName (PID $procId). $_" -ForegroundColor DarkYellow
                }
            }

            if (-not $stopped) {
                Write-Host "Process still running after termination attempts: $procName (PID $procId)" -ForegroundColor DarkYellow
            }
        }
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
        (Join-Path $settingsDir 'agent_settings_SYSTEM.json')
        (Join-Path $settingsDir 'agent_settings_CURRENTUSER.json')
        (Join-Path $settingsDir 'agent_settings_svc.json')
        (Join-Path $settingsDir 'agent_settings_user.json')
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

function Get-AgentGuid {
    param(
        [string]$AgentRoot
    )

    $candidates = @()
    if (-not $AgentRoot) { $AgentRoot = $scriptDir }
    if ($AgentRoot) { $candidates += (Join-Path $AgentRoot 'agent_GUID') }
    $defaultPath = Join-Path $scriptDir 'Agent\Borealis\agent_GUID'
    if ($defaultPath -and ($candidates -notcontains $defaultPath)) { $candidates += $defaultPath }

    foreach ($path in ($candidates | Select-Object -Unique)) {
        try {
            if (Test-Path $path -PathType Leaf) {
                $value = (Get-Content -Path $path -Raw -ErrorAction Stop)
                if ($value) { return $value.Trim() }
            }
        } catch {}
    }

    return ''
}

function Get-RepositoryCommitHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ProjectRoot,

        [string]$AgentRoot,

        [string]$GitExe
    )

    $candidates = @()
    if ($ProjectRoot -and ($candidates -notcontains $ProjectRoot)) { $candidates += $ProjectRoot }
    if ($AgentRoot -and ($candidates -notcontains $AgentRoot)) { $candidates += $AgentRoot }
    if ($ProjectRoot) {
        $agentRootCandidate = Join-Path $ProjectRoot 'Agent\Borealis'
        if ((Test-Path $agentRootCandidate -PathType Container) -and ($candidates -notcontains $agentRootCandidate)) {
            $candidates += $agentRootCandidate
        }
    }

    if ($GitExe -and (Test-Path $GitExe -PathType Leaf)) {
        foreach ($root in $candidates) {
            try {
                if (-not (Test-Path (Join-Path $root '.git') -PathType Container)) { continue }
                $revParse = Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $root -Arguments @('rev-parse','HEAD')
                if ($revParse) {
                    $candidate = ($revParse | Select-Object -Last 1)
                    if ($candidate) { return ($candidate.Trim()) }
                }
            } catch {}
        }
    }

    foreach ($root in $candidates) {
        try {
            $gitDir = Join-Path $root '.git'
            $fetchHead = Join-Path $gitDir 'FETCH_HEAD'
            if (-not (Test-Path $fetchHead -PathType Leaf)) { continue }
            foreach ($line in Get-Content -Path $fetchHead -ErrorAction Stop) {
                $trim = ($line).Trim()
                if (-not $trim -or $trim.StartsWith('#')) { continue }
                $split = $trim.Split(@("`t", ' '), [StringSplitOptions]::RemoveEmptyEntries)
                if ($split.Count -gt 0) {
                    $candidate = $split[0].Trim()
                    if ($candidate) { return $candidate }
                }
            }
        } catch {}
    }

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
        } catch {}
    }

    if ($AgentRoot) {
        $stored = Get-StoredAgentHash -AgentRoot $AgentRoot
        if ($stored) { return $stored }
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

function Set-GitFetchHeadHash {
    param(
        [string]$ProjectRoot,
        [string]$CommitHash,
        [string]$BranchName = 'main'
    )

    if ([string]::IsNullOrWhiteSpace($ProjectRoot) -or [string]::IsNullOrWhiteSpace($CommitHash)) { return }

    try {
        $gitDir = Join-Path $ProjectRoot '.git'
        if (-not (Test-Path $gitDir -PathType Container)) {
            New-Item -ItemType Directory -Force -Path $gitDir | Out-Null
        }
        $fetchHead = Join-Path $gitDir 'FETCH_HEAD'
        $branchSegment = if ([string]::IsNullOrWhiteSpace($BranchName)) { '' } else { "`tbranch '$BranchName'" }
        $content = "{0}{1}" -f ($CommitHash.Trim()), $branchSegment
        Set-Content -Path $fetchHead -Value $content -Encoding UTF8
    } catch {}
}

function Get-ServerCurrentRepoHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerBaseUrl
    )

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl)) { return $null }

    $base = $ServerBaseUrl.TrimEnd('/')
    $uri = "$base/api/repo/current_hash"
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
        [string]$AgentHash,

        [string]$AgentGuid
    )

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl) -or [string]::IsNullOrWhiteSpace($AgentHash)) {
        return
    }

    $base = $ServerBaseUrl.TrimEnd('/')
    $uri = "$base/api/agent/hash"
    $payloadBody = @{ agent_hash = $AgentHash }
    if (-not [string]::IsNullOrWhiteSpace($AgentId)) { $payloadBody.agent_id = $AgentId }
    if (-not [string]::IsNullOrWhiteSpace($AgentGuid)) { $payloadBody.agent_guid = $AgentGuid }
    $payload = $payloadBody | ConvertTo-Json -Depth 3
    $headers = @{ 'User-Agent' = 'borealis-agent-updater' }

    $resp = Invoke-WebRequest -Uri $uri -Method Post -Headers $headers -Body $payload -ContentType 'application/json' -UseBasicParsing -ErrorAction Stop
    try {
        $json = $resp.Content | ConvertFrom-Json
        return $json
    } catch {
        return $null
    }
}

function Sync-AgentHashRecord {
    param(
        [string]$ProjectRoot,
        [string]$AgentRoot,
        [string]$AgentHash,
        [string]$ServerBaseUrl,
        [string]$AgentId,
        [string]$AgentGuid,
        [string]$BranchName = 'main'
    )

    if ([string]::IsNullOrWhiteSpace($AgentHash)) { return }

    if ($ProjectRoot) {
        Set-GitFetchHeadHash -ProjectRoot $ProjectRoot -CommitHash $AgentHash -BranchName $BranchName
    }
    if ($AgentRoot) {
        Set-StoredAgentHash -AgentRoot $AgentRoot -AgentHash $AgentHash
    }

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl)) { return }

    Write-Host ("Submitting agent hash to server: {0}" -f $AgentHash)

    if ([string]::IsNullOrWhiteSpace($AgentId) -and [string]::IsNullOrWhiteSpace($AgentGuid)) {
        Write-Host "Agent identifier unavailable; skipping agent hash submission." -ForegroundColor DarkYellow
        return
    }

    try {
        $submitResult = Submit-AgentHash -ServerBaseUrl $ServerBaseUrl -AgentId $AgentId -AgentHash $AgentHash -AgentGuid $AgentGuid
        if ($submitResult -and ($submitResult.status -eq 'ok')) {
            Write-Host "Server agent_hash database record updated successfully."
        } elseif ($submitResult -and ($submitResult.status -eq 'ignored')) {
            Write-Host "Server ignored agent_hash update (agent not registered)." -ForegroundColor DarkYellow
        } elseif ($submitResult) {
            Write-Host "Server agent_hash update response unrecognized." -ForegroundColor DarkYellow
        }
    } catch {
        Write-Verbose ("Failed to submit agent hash: {0}" -f $_.Exception.Message)
    }
}

function Invoke-BorealisUpdate {
    param(
        [Parameter(Mandatory = $true)]
        [string]$GitExe,

        [Parameter(Mandatory = $true)]
        [string]$RepositoryUrl,

        [Parameter(Mandatory = $true)]
        [string]$TargetHash,

        [string]$BranchName = 'main',

        [switch]$Silent
    )

    if ([string]::IsNullOrWhiteSpace($TargetHash)) {
        throw 'Target commit hash is required for Borealis update.'
    }

    $preservePath = Join-Path $scriptDir "Data\Server\Python_API_Endpoints\Tesseract-OCR"
    $preserveBackupPath = Join-Path $scriptDir "Update_Staging\Tesseract-OCR"
    $ansibleEePath = Join-Path $scriptDir "Agent\Ansible_EE"
    $ansibleEeBackupPath = Join-Path $scriptDir "Update_Staging\Ansible_EE"

    Run-Step "Updating: Move Tesseract-OCR Folder Somewhere Safe to Restore Later" {
        if (Test-Path $preservePath) {
            $stagingPath = Join-Path $scriptDir "Update_Staging"
            if (-not (Test-Path $stagingPath)) { New-Item -ItemType Directory -Force -Path $stagingPath | Out-Null }
            Move-Item -Path $preservePath -Destination $preserveBackupPath -Force
        }
    }

    Run-Step "Updating: Preserve Ansible Execution Environment" {
        if (Test-Path $ansibleEePath) {
            $stagingPath = Join-Path $scriptDir "Update_Staging"
            if (-not (Test-Path $stagingPath)) { New-Item -ItemType Directory -Force -Path $stagingPath | Out-Null }
            if (Test-Path $ansibleEeBackupPath) {
                Remove-Item -Path $ansibleEeBackupPath -Recurse -Force -ErrorAction SilentlyContinue
            }
            Move-Item -Path $ansibleEePath -Destination $ansibleEeBackupPath -Force
        }
    }

    Run-Step "Updating: Clean Up Folders to Prepare for Update" {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue `
            (Join-Path $scriptDir "Data"), `
            (Join-Path $scriptDir "Server\web-interface\src"), `
            (Join-Path $scriptDir "Server\web-interface\build"), `
            (Join-Path $scriptDir "Server\web-interface\public"), `
            (Join-Path $scriptDir "Server\Borealis"), `
            (Join-Path $scriptDir '.git')
    }

    $stagingPath = Join-Path $scriptDir "Update_Staging"
    $cloneDir = Join-Path $stagingPath 'repo'

    Run-Step "Updating: Create Update Staging Folder" {
        if (-not (Test-Path $stagingPath)) { New-Item -ItemType Directory -Force -Path $stagingPath | Out-Null }
        if (Test-Path $cloneDir) {
            Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $cloneDir
        }
    }

    Run-Step "Updating: Clone Repository Source" {
        $cloneArgs = @('clone','--no-tags','--depth','1')
        if (-not [string]::IsNullOrWhiteSpace($BranchName)) {
            $cloneArgs += @('--branch', $BranchName)
        }
        $cloneArgs += @($RepositoryUrl, $cloneDir)
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $stagingPath -Arguments $cloneArgs | Out-Null
    }

    Run-Step "Updating: Checkout Target Revision" {
        $normalizedHash = $TargetHash.Trim()
        $haveHash = $false
        try {
            Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $cloneDir -Arguments @('rev-parse', $normalizedHash) | Out-Null
            $haveHash = $true
        } catch {
            $haveHash = $false
        }

        if (-not $haveHash) {
            Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $cloneDir -Arguments @('fetch','origin',$normalizedHash) | Out-Null
        }

        if ([string]::IsNullOrWhiteSpace($BranchName)) {
            Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $cloneDir -Arguments @('checkout', $normalizedHash) | Out-Null
        } else {
            Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $cloneDir -Arguments @('checkout','-B',$BranchName,$normalizedHash) | Out-Null
        }
    }

    Run-Step "Updating: Copy Update Files into Production Borealis Root Folder" {
        Get-ChildItem -Path $cloneDir -Force | ForEach-Object {
            $destination = Join-Path $scriptDir $_.Name
            if ($_.PSIsContainer) {
                Copy-Item -Path $_.FullName -Destination $destination -Recurse -Force
            } else {
                Copy-Item -Path $_.FullName -Destination $scriptDir -Force
            }
        }
    }

    Run-Step "Updating: Restore Tesseract-OCR Folder" {
        $restorePath = Join-Path $scriptDir "Data\Server\Python_API_Endpoints"
        if (Test-Path $preserveBackupPath) {
            if (-not (Test-Path $restorePath)) { New-Item -ItemType Directory -Force -Path $restorePath | Out-Null }
            Move-Item -Path $preserveBackupPath -Destination $restorePath -Force
        }
    }

    Run-Step "Updating: Restore Ansible Execution Environment" {
        $restorePath = Join-Path $scriptDir "Agent"
        if (Test-Path $ansibleEeBackupPath) {
            if (-not (Test-Path $restorePath)) { New-Item -ItemType Directory -Force -Path $restorePath | Out-Null }
            Move-Item -Path $ansibleEeBackupPath -Destination $restorePath -Force
        }
    }

    Run-Step "Updating: Clean Up Update Staging Folder" {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $stagingPath
    }

    if (-not $Silent) {
        Write-Host "Unattended Borealis update completed." -ForegroundColor Green
    }
}

function Invoke-BorealisAgentUpdate {
    Write-Host "==============================================="
    Write-Host " Borealis - Automation Platform Updater Script "
    Write-Host "==============================================="

    $agentRootCandidate = Join-Path $scriptDir 'Agent\Borealis'
    $agentRoot = $scriptDir
    if (Test-Path $agentRootCandidate -PathType Container) {
        try {
            $agentRoot = (Resolve-Path -Path $agentRootCandidate -ErrorAction Stop).Path
        } catch {
            $agentRoot = $agentRootCandidate
        }
    }

    $agentGuid = Get-AgentGuid -AgentRoot $agentRoot
    if ($agentGuid) {
        Write-Host ("Agent GUID: {0}" -f $agentGuid)
    } else {
        Write-Host "Warning: No agent GUID detected - Please deploy the agent, associating it with a Borealis server then try running the updater script again." -ForegroundColor Yellow
        Write-Host "⚠️ Borealis update aborted."
        return
    }

    $gitExe = Get-GitExecutablePath -ProjectRoot $scriptDir
    $currentHash = Get-RepositoryCommitHash -ProjectRoot $scriptDir -AgentRoot $agentRoot -GitExe $gitExe
    $serverBaseUrl = Get-BorealisServerUrl -AgentRoot $agentRoot
    $agentId = Get-AgentServiceId -AgentRoot $agentRoot

    $serverRepoInfo = Get-ServerCurrentRepoHash -ServerBaseUrl $serverBaseUrl
    $serverHash = ''
    $serverBranch = 'main'
    if ($serverRepoInfo) {
        try { $serverHash = (($serverRepoInfo.sha) -as [string]).Trim() } catch { $serverHash = '' }
        try {
            $branchCandidate = (($serverRepoInfo.branch) -as [string]).Trim()
            if ($branchCandidate) { $serverBranch = $branchCandidate }
        } catch { $serverBranch = 'main' }
    }

    $updateMode = $env:update_mode
    if ($updateMode) { $updateMode = $updateMode.ToLowerInvariant() } else { $updateMode = 'update' }
    $forceUpdate = $updateMode -eq 'force_update'

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

    $normalizedLocalHash = if ($currentHash) { $currentHash.Trim().ToLowerInvariant() } else { '' }
    $normalizedServerHash = if ($serverHash) { $serverHash.Trim().ToLowerInvariant() } else { '' }
    $hashesMatch = ($normalizedLocalHash -and $normalizedServerHash -and ($normalizedLocalHash -eq $normalizedServerHash))
    $needsUpdate = $forceUpdate -or (-not $hashesMatch)

    if ($forceUpdate) {
        Write-Host "Force update requested; skipping hash comparison." -ForegroundColor Yellow
    } elseif (-not $serverHash) {
        Write-Host "Borealis server hash unavailable; cannot continue." -ForegroundColor Yellow
        Write-Host "⚠️ Borealis update aborted."
        return
    } elseif (-not $needsUpdate) {
        Write-Host "Local agent files already match the server repository hash." -ForegroundColor Green
        Sync-AgentHashRecord -ProjectRoot $scriptDir -AgentRoot $agentRoot -AgentHash $serverHash -ServerBaseUrl $serverBaseUrl -AgentId $agentId -AgentGuid $agentGuid -BranchName $serverBranch
        Write-Host "✅ Borealis - Automation Platform Already Up-to-Date"
        return
    } else {
        Write-Host "Repository hash mismatch detected; update required."
    }

    if (-not ($gitExe) -or -not (Test-Path $gitExe -PathType Leaf)) {
        Write-Host "Bundled Git dependency not found. Run '.\\Borealis.ps1 -Agent -AgentAction repair' to bootstrap dependencies and try again." -ForegroundColor Yellow
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

        $managedTasks = Stop-AgentScheduledTasks -TaskNames @('Borealis Agent','Borealis Agent (UserHelper)')
        Run-Step "Updating: Terminate Running Python Processes" { Stop-AgentPythonProcesses }

        $updateSucceeded = $false
        try {
            Invoke-BorealisUpdate -GitExe $gitExe -RepositoryUrl $repositoryUrl -TargetHash $serverHash -BranchName $serverBranch -Silent
            $updateSucceeded = $true
        } finally {
            if ($managedTasks.Count -gt 0) {
                Start-AgentScheduledTasks -TaskNames $managedTasks
            }
        }

        if (-not $updateSucceeded) {
            throw 'Borealis update failed.'
        }

        $postUpdateInfo = Get-ServerCurrentRepoHash -ServerBaseUrl $serverBaseUrl
        if ($postUpdateInfo) {
            try {
                $refreshedSha = (($postUpdateInfo.sha) -as [string]).Trim()
                if ($refreshedSha) { $serverHash = $refreshedSha }
            } catch {}
            try {
                $branchCandidate = (($postUpdateInfo.branch) -as [string]).Trim()
                if ($branchCandidate) { $serverBranch = $branchCandidate }
            } catch {}
        }

        $newHash = Get-RepositoryCommitHash -ProjectRoot $scriptDir -AgentRoot $agentRoot -GitExe $gitExe

        $normalizedNewHash = if ($newHash) { $newHash.Trim().ToLowerInvariant() } else { '' }
        $normalizedServerHash = if ($serverHash) { $serverHash.Trim().ToLowerInvariant() } else { '' }

        if ($normalizedServerHash -and (-not $normalizedNewHash -or $normalizedNewHash -ne $normalizedServerHash)) {
            $newHash = $serverHash
            $normalizedNewHash = $normalizedServerHash
        } elseif (-not $newHash -and $serverHash) {
            $newHash = $serverHash
        }

        if ($newHash) {
            Sync-AgentHashRecord -ProjectRoot $scriptDir -AgentRoot $agentRoot -AgentHash $newHash -ServerBaseUrl $serverBaseUrl -AgentId $agentId -AgentGuid $agentGuid -BranchName $serverBranch
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
