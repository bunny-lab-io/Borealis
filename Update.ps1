[CmdletBinding()]
param(
    [switch]$Trace
)

$scriptDir = Split-Path $MyInvocation.MyCommand.Path -Parent
$script:BorealisHttpSecurityInitialized = $false
$script:AgentPythonHttpHelper = ''
$script:UpdateDebugEnabled = $Trace.IsPresent
$script:UpdaterLogPath = Join-Path $scriptDir 'Updater.log'
$script:UpdateFileLoggingWarned = $false
$script:UpdateLoggingInitialized = $false
$symbols = @{
    Success = [char]0x2705
    Running = [char]0x23F3
    Fail    = [char]0x274C
    Info    = [char]0x2139
}

function Write-UpdateFileLine {
    param(
        [string]$Line
    )

    if (-not $Line) { return }

    try {
        Add-Content -Path $script:UpdaterLogPath -Value $Line -Encoding UTF8 -ErrorAction Stop
    } catch {
        if (-not $script:UpdateFileLoggingWarned) {
            $script:UpdateFileLoggingWarned = $true
            Write-Warning ("Unable to write updater log to {0}: {1}" -f $script:UpdaterLogPath, $_.Exception.Message)
        }
    }
}

function Reset-UpdateLogFile {
    try {
        $parent = Split-Path -Path $script:UpdaterLogPath -Parent
        if ($parent -and -not (Test-Path $parent -PathType Container)) {
            New-Item -ItemType Directory -Path $parent -Force | Out-Null
        }
        Set-Content -Path $script:UpdaterLogPath -Value @() -Encoding UTF8 -ErrorAction Stop
        $script:UpdateFileLoggingWarned = $false
        return $true
    } catch {
        if (-not $script:UpdateFileLoggingWarned) {
            $script:UpdateFileLoggingWarned = $true
            Write-Warning ("Unable to reset updater log at {0}: {1}" -f $script:UpdaterLogPath, $_.Exception.Message)
        }
        return $false
    }
}

function Initialize-UpdateLogging {
    if ($script:UpdateLoggingInitialized) { return }

    $script:UpdateLoggingInitialized = $true
    [void](Reset-UpdateLogFile)
    $timestamp = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    Write-UpdateFileLine ("[{0}] [STEP] ===== Starting Update.ps1 session =====" -f $timestamp)
}

function Finalize-UpdateLogging {
    param(
        [string]$Level = 'SUCCESS',
        [string]$Message = '===== Update.ps1 session finished successfully ====='
    )

    $timestamp = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    $normalized = if ($Level) { $Level.ToUpperInvariant() } else { 'INFO' }
    Write-UpdateFileLine ("[{0}] [{1}] {2}" -f $timestamp, $normalized, $Message)
}

function New-UpdateSessionResult {
    param(
        [string]$Outcome,
        [string]$FinalLevel,
        [string]$FinalMessage
    )

    [pscustomobject]@{
        Outcome      = $Outcome
        FinalLevel   = $FinalLevel
        FinalMessage = $FinalMessage
    }
}

function Write-UpdateHost {
    param(
        [string]$Message,
        [string]$Color,
        [switch]$NoNewline,
        [string]$Level = 'INFO'
    )

    if (-not $Message) { return }

    $timestamp = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    $normalized = if ($Level) { $Level.ToUpperInvariant() } else { 'INFO' }
    $sanitized = (($Message -replace "[`r`n]+", ' ').Trim())
    if ($sanitized) {
        Write-UpdateFileLine ("[{0}] [{1}] {2}" -f $timestamp, $normalized, $sanitized)
    }

    if ($Color) {
        if ($NoNewline) {
            Write-Host $Message -ForegroundColor $Color -NoNewline
        } else {
            Write-Host $Message -ForegroundColor $Color
        }
    } else {
        if ($NoNewline) {
            Write-Host $Message -NoNewline
        } else {
            Write-Host $Message
        }
    }
}

function Write-UpdateLog {
    param(
        [string]$Message,
        [string]$Level = 'INFO',
        [string]$Color
    )

    if (-not $Message) { return }

    $timestamp = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    $normalized = if ($Level) { $Level } else { 'INFO' }
    $normalized = $normalized.ToUpperInvariant()
    $writeToConsole = $true
    if ($normalized -eq 'DEBUG' -and -not $script:UpdateDebugEnabled) {
        $writeToConsole = $false
    }

    if (-not $Color) {
        switch ($normalized) {
            'WARN' { $Color = 'Yellow' }
            'ERROR' { $Color = 'Red' }
            'STEP' { $Color = 'Cyan' }
            'SUCCESS' { $Color = 'Green' }
            default { $Color = $null }
        }
    }

    $line = "[{0}] [{1}] {2}" -f $timestamp, $normalized, $Message
    Write-UpdateFileLine $line
    if (-not $writeToConsole) {
        return
    }
    if ($Color) {
        Write-Host $line -ForegroundColor $Color
    } else {
        Write-Host $line
    }
}

function Test-IsAdmin {
    try {
        $id = [Security.Principal.WindowsIdentity]::GetCurrent()
        $principal = New-Object Security.Principal.WindowsPrincipal($id)
        return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    } catch {
        return $false
    }
}

$repositoryUrl = 'https://github.com/bunny-lab-io/Borealis.git'

function Write-ProgressStep {
    param (
        [string]$Message,
        [string]$Status = $symbols["Info"]
    )
    Write-UpdateHost -Message "`r$Status $Message... " -NoNewline -Level 'STEP'
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
            Write-UpdateHost -Message "`r$($symbols.Success) $Message                        " -Level 'SUCCESS'
            Write-UpdateLog ("{0} completed successfully." -f $Message) 'SUCCESS'
        } else {
            throw "Non-zero exit code"
        }
    } catch {
        Write-UpdateHost -Message "`r$($symbols.Fail) $Message - Failed: $_                        " -Color Red -Level 'ERROR'
        Write-UpdateLog ("{0} failed: {1}" -f $Message, $_.Exception.Message) 'ERROR'
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
    try {
        $gitCmd = Get-Command git.exe -ErrorAction SilentlyContinue
        if ($gitCmd -and $gitCmd.Source) { $candidates += $gitCmd.Source }
    } catch {}
    try {
        $gitCmd = Get-Command git -ErrorAction SilentlyContinue
        if ($gitCmd -and $gitCmd.Source) { $candidates += $gitCmd.Source }
    } catch {}

    foreach ($candidate in ($candidates | Select-Object -Unique)) {
        try {
            if (Test-Path $candidate -PathType Leaf) { return $candidate }
        } catch {}
    }

    return ''
}

function Resolve-BorealisRepositoryUrl {
    param(
        [string]$GitExe,
        [string]$ProjectRoot
    )

    $configuredRepo = ''
    try {
        if ($env:BOREALIS_UPDATE_REPO) {
            $configuredRepo = $env:BOREALIS_UPDATE_REPO.Trim()
        }
    } catch {}

    if ($configuredRepo) {
        if ($configuredRepo -match '^[A-Za-z][A-Za-z0-9+.-]*://') {
            return $configuredRepo
        }
        if ($configuredRepo -like 'git@*') {
            return $configuredRepo
        }
        return ("https://github.com/{0}.git" -f $configuredRepo)
    }

    if ($GitExe -and $ProjectRoot -and (Test-Path (Join-Path $ProjectRoot '.git'))) {
        try {
            $origin = Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('config', '--get', 'remote.origin.url')
            if ($origin) {
                $candidate = ($origin | Select-Object -Last 1)
                if ($candidate) {
                    $candidate = $candidate.Trim()
                    if ($candidate) {
                        return $candidate
                    }
                }
            }
        } catch {}
    }

    return $repositoryUrl
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
    $joined = ($Arguments | ForEach-Object { [string]$_ }) -join ' '
    Write-UpdateLog ("Running git command: git {0}" -f $joined) 'DEBUG'
    $output = & $GitExe @fullArgs 2>&1
    $exitCode = $LASTEXITCODE
    foreach ($line in @($output)) {
        if ($null -eq $line) { continue }
        $text = ([string]$line).Trim()
        if (-not $text) { continue }
        Write-UpdateLog ("git> {0}" -f $text) 'DEBUG'
    }
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

        Write-UpdateHost -Message "Stopping scheduled task: $name" -Color Yellow -Level 'WARN'
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
        Write-UpdateHost -Message "Restarting scheduled task: $name" -Color Green -Level 'INFO'
        try {
            Start-ScheduledTask -TaskName $name -ErrorAction Stop | Out-Null
            continue
        } catch {}

        try { schtasks.exe /Run /TN "$name" 2>$null | Out-Null } catch {}
    }
}

function Stop-AgentPythonProcesses {
    param(
        [string[]]$ProcessNames = @('python', 'pythonw'),
        [string]$ProjectRoot,
        [switch]$SkipEngine = $true
    )

    $enginePids = @()
    $engineRoot = ''
    $dataEngineRoot = ''
    if ($ProjectRoot) {
        try { $engineRoot = (Join-Path $ProjectRoot 'Engine') } catch {}
        try { $dataEngineRoot = (Join-Path $ProjectRoot 'Data\Engine') } catch {}
    }

    if ($SkipEngine) {
        try {
            $cims = Get-CimInstance -ClassName Win32_Process -Filter "Name='python.exe' OR Name='pythonw.exe'" -ErrorAction Stop
            foreach ($proc in $cims) {
                try {
                    $pid = [int]$proc.ProcessId
                } catch { continue }
                $cmd = ($proc.CommandLine -as [string])
                $exePath = ($proc.ExecutablePath -as [string])
                $isEngine = $false
                foreach ($marker in @($engineRoot, $dataEngineRoot, '\Engine\', '\Data\Engine\', 'Engine\server.py', 'Data\Engine\server.py')) {
                    if (-not $marker) { continue }
                    try {
                        if ($cmd -and $cmd.ToLowerInvariant().Contains($marker.ToLowerInvariant())) { $isEngine = $true; break }
                        if ($exePath -and $exePath.ToLowerInvariant().Contains($marker.ToLowerInvariant())) { $isEngine = $true; break }
                    } catch {}
                }
                if ($isEngine -and ($enginePids -notcontains $pid)) { $enginePids += $pid }
            }
        } catch {}
    }

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
            $procPath = $null
            try {
                $procId = $proc.Id
                $procName = $proc.ProcessName
                $procPath = $proc.Path
            } catch {}

            if ($procId -eq $null) { continue }

            $isEngineProc = $false
            try {
                if ($enginePids -and ($enginePids -contains $procId)) { $isEngineProc = $true }
                foreach ($marker in @($engineRoot, $dataEngineRoot)) {
                    if (-not $marker) { continue }
                    if ($procPath -and $procPath.ToLowerInvariant().StartsWith($marker.ToLowerInvariant())) { $isEngineProc = $true; break }
                }
            } catch {}

            if ($SkipEngine -and $isEngineProc) {
                Write-UpdateHost -Message "Skipping Engine python process: PID $procId ($procName)" -Color Cyan -Level 'INFO'
                continue
            }

            if (-not $procName) { $procName = $name }

            $stopped = $false
            Write-UpdateHost -Message "Stopping process: $procName (PID $procId)" -Color Yellow -Level 'WARN'

            try {
                Stop-Process -Id $procId -Force -ErrorAction Stop
                $stopped = $true
            } catch {
                Write-UpdateHost -Message "Unable to stop process via Stop-Process: $procName (PID $procId). $_" -Color DarkYellow -Level 'WARN'
            }

            if (-not $stopped) {
                try {
                    $taskkillOutput = taskkill.exe /PID $procId /F 2>&1
                    if ($LASTEXITCODE -eq 0) {
                        $stopped = $true
                    } else {
                        if ($taskkillOutput) {
                            Write-UpdateHost -Message "taskkill.exe returned exit code ${LASTEXITCODE} for PID ${procId}: $taskkillOutput" -Color DarkYellow -Level 'WARN'
                        }
                    }
                } catch {
                    Write-UpdateHost -Message "Unable to stop process via taskkill.exe: $procName (PID $procId). $_" -Color DarkYellow -Level 'WARN'
                }
            }

            if (-not $stopped) {
                Write-UpdateHost -Message "Process still running after termination attempts: $procName (PID $procId)" -Color DarkYellow -Level 'WARN'
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
            $settingsDir = Get-AgentSettingsDirectory -AgentRoot $AgentRoot
            if ($settingsDir) {
                $serverUrlFile = Join-Path $settingsDir 'server_url.txt'
                if (Test-Path $serverUrlFile -PathType Leaf) {
                    $content = Get-Content -Path $serverUrlFile -Raw -ErrorAction Stop
                    if ($content) { $serverBaseUrl = $content.Trim() }
                }
            }
        } catch {}
    }

    $resolved = Resolve-BorealisServerUrl -Url $serverBaseUrl
    if ([string]::IsNullOrWhiteSpace($resolved)) {
        return ''
    }

    Write-UpdateLog ("Resolved Borealis server URL: {0}" -f $resolved) 'INFO'
    return $resolved
}

function Resolve-BorealisServerUrl {
    param(
        [string]$Url
    )

    if ([string]::IsNullOrWhiteSpace($Url)) {
        return ''
    }

    $candidate = $Url.Trim()
    if ($candidate -notmatch '^[A-Za-z][A-Za-z0-9+.-]*://') {
        $candidate = "https://$candidate"
    }

    try {
        $builder = New-Object System.UriBuilder($candidate)
    } catch {
        return ''
    }

    if ($builder.Scheme -ne 'https') {
        return ''
    }

    $hostName = $builder.Host.ToLowerInvariant()
    if ([string]::IsNullOrWhiteSpace($hostName) -or $hostName -eq 'localhost') {
        return ''
    }
    $parsedIp = $null
    if ([System.Net.IPAddress]::TryParse($hostName, [ref]$parsedIp)) {
        return ''
    }

    return $builder.Uri.AbsoluteUri.TrimEnd('/')
}

function Get-AgentServerUrlFromSettings {
    param(
        [string]$AgentRoot
    )

    $settingsDir = Get-AgentSettingsDirectory -AgentRoot $AgentRoot
    if (-not $settingsDir) {
        $settingsDir = Get-CanonicalAgentSettingsDirectory -AgentRoot $AgentRoot
    }
    $serverUrlFile = Join-Path $settingsDir 'server_url.txt'
    if (-not (Test-Path $serverUrlFile -PathType Leaf)) {
        return [pscustomobject]@{
            Url    = ''
            Source = $serverUrlFile
        }
    }

    $raw = ''
    try {
        $raw = Get-Content -Path $serverUrlFile -Raw -ErrorAction Stop
    } catch {
        Write-UpdateLog ("Failed to read server_url.txt: {0}" -f $_.Exception.Message) 'WARN'
        return [pscustomobject]@{
            Url    = ''
            Source = $serverUrlFile
        }
    }

    $candidate = if ($raw) { $raw.Trim() } else { '' }
    if (-not $candidate) {
        return [pscustomobject]@{
            Url    = ''
            Source = $serverUrlFile
        }
    }

    $resolved = Resolve-BorealisServerUrl -Url $candidate
    if (-not $resolved) { $resolved = $candidate.TrimEnd('/') }

    return [pscustomobject]@{
        Url    = $resolved
        Source = $serverUrlFile
    }
}

function Initialize-BorealisTlsContext {
    param(
        [string]$AgentRoot,
        [string]$ServerBaseUrl
    )

    $null = $AgentRoot
    $null = $ServerBaseUrl
    if ($script:BorealisHttpSecurityInitialized) {
        return
    }

    try {
        $protocolType = [System.Net.SecurityProtocolType]
        $hasSystemDefault = [System.Enum]::IsDefined($protocolType, 'SystemDefault')
        if ($hasSystemDefault) {
            # Allow the OS to negotiate the strongest available protocol (TLS 1.3 on modern hosts).
            [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::SystemDefault
            Write-UpdateLog "SecurityProtocol configured to SystemDefault (OS-negotiated)." 'DEBUG'
        } else {
            $protocol = [System.Net.SecurityProtocolType]::Tls12 -bor [System.Net.SecurityProtocolType]::Tls11
            if ([System.Enum]::IsDefined($protocolType, 'Tls13')) {
                $tls13 = [System.Enum]::Parse($protocolType, 'Tls13')
                $protocol = $protocol -bor $tls13
            }
            [System.Net.ServicePointManager]::SecurityProtocol = $protocol
            Write-UpdateLog ("SecurityProtocol configured to legacy mask: {0}" -f $protocol) 'DEBUG'
        }
    } catch {}

    $script:BorealisHttpSecurityInitialized = $true
}

function Get-AgentPythonExecutable {
    param(
        [string]$AgentRoot
    )

    $candidates = @()
    if ($AgentRoot) {
        try {
            $agentParent = Split-Path $AgentRoot -Parent
            if ($agentParent) {
                $candidates += (Join-Path $agentParent 'Scripts\python.exe')
            }
        } catch {}
    }
    $candidates += (Join-Path $scriptDir 'Agent\Scripts\python.exe')
    $candidates += (Join-Path $scriptDir 'Dependencies\Python\python.exe')
    foreach ($candidate in ($candidates | Select-Object -Unique)) {
        try {
            if (Test-Path $candidate -PathType Leaf) {
                Write-UpdateLog ("Using Python executable for HTTP helper: {0}" -f $candidate) 'DEBUG'
                return $candidate
            }
        } catch {}
    }
    Write-UpdateLog "No Python executable found for HTTP helper fallback." 'WARN'
    return ''
}

function Invoke-AgentUpdateHelper {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments,

        [string]$AgentRoot,

        [switch]$ExpectJson
    )

    $pythonExe = Get-AgentPythonExecutable -AgentRoot $AgentRoot
    if (-not $pythonExe -or -not (Test-Path $pythonExe -PathType Leaf)) {
        throw 'Agent runtime Python executable not found.'
    }

    $helperScript = Join-Path $scriptDir 'Data\Agent\update_helper.py'
    if (-not (Test-Path $helperScript -PathType Leaf)) {
        throw "Agent update helper not found at '$helperScript'."
    }

    $output = & $pythonExe $helperScript @Arguments 2>&1
    $exitCode = $LASTEXITCODE
    $text = (($output | ForEach-Object { [string]$_ }) -join [Environment]::NewLine).Trim()
    if ($exitCode -ne 0) {
        if ($text) {
            throw "Agent update helper failed: $text"
        }
        throw 'Agent update helper failed without output.'
    }

    if (-not $ExpectJson) {
        return $text
    }

    if (-not $text) {
        throw 'Agent update helper returned empty JSON output.'
    }

    try {
        return ($text | ConvertFrom-Json -ErrorAction Stop)
    } catch {
        throw ("Failed to decode agent update helper JSON: {0}" -f $_.Exception.Message)
    }
}

function Get-AgentUpdaterStatus {
    param([string]$AgentRoot)

    return (Invoke-AgentUpdateHelper -AgentRoot $AgentRoot -Arguments @('status') -ExpectJson)
}

function Get-AgentUpdaterRepoInfo {
    param(
        [string]$AgentRoot,
        [switch]$Refresh
    )

    $arguments = @('repo-hash')
    if ($Refresh) {
        $arguments += '--refresh'
    }
    return (Invoke-AgentUpdateHelper -AgentRoot $AgentRoot -Arguments $arguments -ExpectJson)
}

function Sync-AgentInstalledBuildId {
    param([string]$AgentRoot)

    try {
        $result = Invoke-AgentUpdateHelper -AgentRoot $AgentRoot -Arguments @('sync-build-id')
        return ($result -as [string]).Trim()
    } catch {
        Write-UpdateLog ("Failed to sync installed build id: {0}" -f $_.Exception.Message) 'WARN'
        return ''
    }
}

function Invoke-BorealisAgentRuntimeRefresh {
    param(
        [string]$ProjectRoot
    )

    if (-not $ProjectRoot) { $ProjectRoot = $scriptDir }
    $scriptPath = Join-Path $ProjectRoot 'Borealis.ps1'
    if (-not (Test-Path $scriptPath -PathType Leaf)) {
        Write-UpdateLog ("Borealis.ps1 not found at '{0}'." -f $scriptPath) 'ERROR'
        return $false
    }

    $argTokens = @(
        '-NoProfile',
        '-ExecutionPolicy', 'Bypass',
        '-File', $scriptPath,
        '-Agent',
        '-RefreshAgentRuntime'
    )

    $argLine = ($argTokens | ForEach-Object {
        $text = [string]$_
        if ($text -match '\s') {
            '"' + ($text -replace '"','`"') + '"'
        } else {
            $text
        }
    }) -join ' '

    Write-UpdateLog ("Refreshing Borealis agent runtime via {0}." -f $scriptPath) 'STEP'
    try {
        $process = Start-Process -FilePath 'powershell.exe' -ArgumentList $argLine -WorkingDirectory $ProjectRoot -NoNewWindow -Wait -PassThru
    } catch {
        Write-UpdateLog ("Failed to start Borealis runtime refresh: {0}" -f $_.Exception.Message) 'ERROR'
        return $false
    }

    if (-not $process) {
        Write-UpdateLog 'Borealis runtime refresh process did not start.' 'ERROR'
        return $false
    }

    if ($process.ExitCode -ne 0) {
        Write-UpdateLog ("Borealis runtime refresh exited with code {0}." -f $process.ExitCode) 'ERROR'
        return $false
    }

    Write-UpdateLog 'Borealis agent runtime refresh completed successfully.' 'SUCCESS'
    return $true
}

function Invoke-BorealisRepoSync {
    param(
        [Parameter(Mandatory = $true)]
        [string]$GitExe,

        [Parameter(Mandatory = $true)]
        [string]$ProjectRoot,

        [Parameter(Mandatory = $true)]
        [string]$RepositoryUrl,

        [Parameter(Mandatory = $true)]
        [string]$TargetHash,

        [string]$BranchName = 'main'
    )

    if (-not (Test-Path (Join-Path $ProjectRoot '.git'))) {
        throw "Project root '$ProjectRoot' is not a git checkout."
    }

    try {
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('remote', 'set-url', 'origin', $RepositoryUrl) | Out-Null
    } catch {
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('remote', 'add', 'origin', $RepositoryUrl) | Out-Null
    }

    if (-not [string]::IsNullOrWhiteSpace($BranchName)) {
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('fetch', '--force', '--prune', 'origin', $BranchName) | Out-Null
    } else {
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('fetch', '--force', '--prune', 'origin') | Out-Null
    }

    $haveHash = $false
    try {
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('rev-parse', $TargetHash) | Out-Null
        $haveHash = $true
    } catch {
        $haveHash = $false
    }

    if (-not $haveHash) {
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('fetch', '--force', 'origin', $TargetHash) | Out-Null
    }

    if ([string]::IsNullOrWhiteSpace($BranchName)) {
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('checkout', '--force', $TargetHash) | Out-Null
    } else {
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('checkout', '--force', '-B', $BranchName, $TargetHash) | Out-Null
    }

    Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('reset', '--hard', $TargetHash) | Out-Null
    Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $ProjectRoot -Arguments @('clean', '-fd') | Out-Null
}

function Ensure-AgentPythonHttpHelper {
    if ($script:AgentPythonHttpHelper -and (Test-Path $script:AgentPythonHttpHelper -PathType Leaf)) {
        return $script:AgentPythonHttpHelper
    }

    $helperDir = Join-Path ([System.IO.Path]::GetTempPath()) 'BorealisUpdate'
    try {
        if (-not (Test-Path $helperDir -PathType Container)) {
            New-Item -ItemType Directory -Force -Path $helperDir | Out-Null
        }
    } catch {}

    $helperPath = Join-Path $helperDir 'agent_http_client.py'
    $helperSource = @"
import json
import ssl
import sys
import urllib.error
import urllib.request


def _build_context():
    ctx = ssl.create_default_context()
    minimum = getattr(ssl, "TLSVersion", None)
    if minimum is not None:
        try:
            ctx.minimum_version = ssl.TLSVersion.TLSv1_2
        except Exception:
            pass
    return ctx


def _read_payload():
    data = sys.stdin.buffer.read()
    if not data:
        return {}
    try:
        text = data.decode("utf-8-sig")
    except Exception:
        text = data.decode("utf-8", errors="ignore")
    return json.loads(text)


def main():
    try:
        payload = _read_payload()
    except Exception as exc:  # pragma: no cover - defensive
        json.dump({"error": "payload decode failed: %s" % (exc,)}, sys.stdout)
        return 1

    method = (payload.get("method") or "GET").upper()
    url = payload.get("url")
    headers = payload.get("headers") or {}
    body = payload.get("body")
    content_type = payload.get("content_type")
    timeout = payload.get("timeout") or 30

    if body is not None and not isinstance(body, (bytes, bytearray)):
        body = str(body).encode("utf-8")

    request = urllib.request.Request(url=url, data=body, method=method)
    for key, value in headers.items():
        if value is None:
            continue
        request.add_header(str(key), str(value))

    if content_type and all(k.lower() != "content-type" for k in request.headers):
        request.add_header("Content-Type", str(content_type))

    ctx = _build_context()

    try:
        with urllib.request.urlopen(request, timeout=float(timeout), context=ctx) as response:
            text = response.read().decode("utf-8", errors="replace")
            json.dump({"status": response.status, "body": text}, sys.stdout)
            return 0
    except urllib.error.HTTPError as exc:
        text = exc.read().decode("utf-8", errors="replace") if exc.fp else ""
        json.dump({"status": exc.code, "body": text}, sys.stdout)
        return 0
    except Exception as exc:  # pragma: no cover - defensive
        json.dump({"error": str(exc)}, sys.stdout)
        return 1


if __name__ == "__main__":  # pragma: no cover - defensive
    raise SystemExit(main())
"@

    try {
        Set-Content -Path $helperPath -Value $helperSource -Encoding UTF8 -Force
        $script:AgentPythonHttpHelper = $helperPath
        Write-UpdateLog ("Staged Python HTTP helper at {0}" -f $helperPath) 'DEBUG'
    } catch {
        $script:AgentPythonHttpHelper = ''
    }

    return $script:AgentPythonHttpHelper
}

function Invoke-AgentHttpRequest {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Method,

        [Parameter(Mandatory = $true)]
        [string]$Uri,

        [hashtable]$Headers,
        [string]$Body,
        [string]$ContentType,
        [string]$AgentRoot,
        [int]$TimeoutSeconds = 30
    )

    $headersToUse = @{}
    if ($Headers) {
        foreach ($key in $Headers.Keys) {
            $value = $Headers[$key]
            if ($null -ne $value -and $value.ToString()) {
                $headersToUse[$key] = $value
            }
        }
    }

    $invokeParams = @{
        Uri            = $Uri
        Method         = $Method
        Headers        = $headersToUse
        UseBasicParsing = $true
        ErrorAction    = 'Stop'
    }
    if ($Body) { $invokeParams['Body'] = $Body }
    if ($ContentType) { $invokeParams['ContentType'] = $ContentType }
    if ($TimeoutSeconds -gt 0) { $invokeParams['TimeoutSec'] = $TimeoutSeconds }

    Write-UpdateLog ("HTTP {0} {1}" -f $Method.ToUpperInvariant(), $Uri) 'DEBUG'
    try {
        $response = Invoke-WebRequest @invokeParams
        Write-UpdateLog ("Invoke-WebRequest succeeded (HTTP {0})." -f $response.StatusCode) 'DEBUG'
        return [pscustomobject]@{
            StatusCode = $response.StatusCode
            Content    = $response.Content
        }
    } catch {
        Write-Verbose ("Invoke-WebRequest failed for {0}: {1}" -f $Uri, $_.Exception.Message)
        Write-UpdateLog ("Invoke-WebRequest failed for {0}: {1}" -f $Uri, $_.Exception.Message) 'WARN'
    }

    $pythonExe = Get-AgentPythonExecutable -AgentRoot $AgentRoot
    if (-not $pythonExe) {
        Write-UpdateLog "Python executable for HTTP fallback not found." 'ERROR'
        return $null
    }

    $helperScript = Ensure-AgentPythonHttpHelper
    if (-not $helperScript) {
        Write-UpdateLog "Unable to stage Python HTTP helper script." 'ERROR'
        return $null
    }

    $payload = @{
        method       = $Method
        url          = $Uri
        headers      = $headersToUse
        body         = $Body
        content_type = $ContentType
        timeout      = $TimeoutSeconds
    } | ConvertTo-Json -Depth 6

    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $pythonExe
    $psi.Arguments = ('"{0}"' -f $helperScript)
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true

    try {
        $process = [System.Diagnostics.Process]::Start($psi)
    } catch {
        Write-Verbose ("Failed to start Python helper: {0}" -f $_.Exception.Message)
        Write-UpdateLog ("Failed to launch Python helper: {0}" -f $_.Exception.Message) 'ERROR'
        return $null
    }

    try {
        $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
        $bytes = $utf8NoBom.GetBytes($payload)
        $process.StandardInput.BaseStream.Write($bytes, 0, $bytes.Length)
        $process.StandardInput.BaseStream.Flush()
        $process.StandardInput.Close()
    } catch {
        Write-UpdateLog ("Failed to write payload to Python helper stdin: {0}" -f $_.Exception.Message) 'WARN'
        try { $process.StandardInput.Close() } catch {}
    }

    $stdout = $process.StandardOutput.ReadToEnd()
    $stderr = $process.StandardError.ReadToEnd()
    $process.WaitForExit()

    if ($stderr) {
        Write-Verbose ("Python helper stderr: {0}" -f $stderr.Trim())
        Write-UpdateLog ("Python helper stderr: {0}" -f $stderr.Trim()) 'DEBUG'
    }

    if (-not $stdout) {
        Write-UpdateLog "Python helper returned empty response." 'ERROR'
        return $null
    }

    try {
        $json = $stdout | ConvertFrom-Json -ErrorAction Stop
    } catch {
        Write-Verbose ("Unable to parse Python helper output: {0}" -f $_.Exception.Message)
        return $null
    }

    if ($json.error) {
        Write-Verbose ("Python helper reported error: {0}" -f $json.error)
        Write-UpdateLog ("Python helper error: {0}" -f $json.error) 'ERROR'
        return $null
    }

    Write-UpdateLog ("Python helper completed HTTP call with status {0}." -f $json.status) 'DEBUG'
    return [pscustomobject]@{
        StatusCode = $json.status
        Content    = $json.body
    }
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

function Get-AgentEnrollmentCode {
    param(
        [string]$AgentRoot
    )

    if (-not $AgentRoot) { $AgentRoot = $scriptDir }
    $settingsDir = Join-Path $AgentRoot 'Settings'
    $candidates = @(
        (Join-Path $settingsDir 'agent_settings_SYSTEM.json')
        (Join-Path $settingsDir 'agent_settings.json')
        (Join-Path $settingsDir 'agent_settings_CURRENTUSER.json')
        (Join-Path $settingsDir 'agent_settings_svc.json')
        (Join-Path $settingsDir 'agent_settings_user.json')
    )

    foreach ($path in $candidates) {
        try {
            if (-not (Test-Path $path -PathType Leaf)) { continue }
            $raw = Get-Content -Path $path -Raw -ErrorAction Stop
            if (-not $raw) { continue }
            $cfg = $raw | ConvertFrom-Json -ErrorAction Stop
            $value = ''
            $field = ''
            if ($cfg -and $cfg.PSObject.Properties.Name -contains 'enrollment_code' -and $cfg.enrollment_code) {
                $value = [string]$cfg.enrollment_code
                $field = 'enrollment_code'
            } elseif ($cfg -and $cfg.PSObject.Properties.Name -contains 'installer_code' -and $cfg.installer_code) {
                $value = [string]$cfg.installer_code
                $field = 'installer_code'
            }
            if ($value -and $value.Trim()) {
                return [pscustomobject]@{
                    Code   = $value.Trim()
                    Source = $path
                    Field  = $field
                }
            }
        } catch {}
    }

    return [pscustomobject]@{
        Code   = ''
        Source = ''
        Field  = ''
    }
}

function Invoke-BorealisAgentRedeploy {
    param(
        [string]$ProjectRoot,
        [string]$ServerUrl,
        [string]$EnrollmentCode
    )

    if (-not $ProjectRoot) { $ProjectRoot = $scriptDir }
    $scriptPath = Join-Path $ProjectRoot 'Borealis.ps1'
    if (-not (Test-Path $scriptPath -PathType Leaf)) {
        Write-UpdateLog ("Borealis.ps1 not found at '{0}'." -f $scriptPath) 'ERROR'
        return $false
    }

    if (-not (Test-IsAdmin)) {
        Write-UpdateLog "Updater is not running elevated; skipping agent redeploy to avoid interactive prompts." 'WARN'
        return $false
    }

    $normalizedUrl = if ($ServerUrl) { $ServerUrl.Trim() } else { '' }
    $normalizedCode = if ($EnrollmentCode) { $EnrollmentCode.Trim() } else { '' }

    if (-not $normalizedUrl) {
        Write-UpdateLog "Server URL missing; cannot re-deploy Borealis agent." 'ERROR'
        return $false
    }
    if (-not $normalizedCode) {
        Write-UpdateLog "Enrollment code missing; cannot re-deploy Borealis agent." 'ERROR'
        return $false
    }

    $argTokens = @(
        '-NoProfile',
        '-ExecutionPolicy', 'Bypass',
        '-File', $scriptPath,
        '-Agent',
        '-ServerUrl', $normalizedUrl,
        '-EnrollmentCode', $normalizedCode
    )

    $argLine = ($argTokens | ForEach-Object {
        $text = [string]$_
        if ($text -match '\s') {
            '"' + ($text -replace '"','`"') + '"'
        } else {
            $text
        }
    }) -join ' '

    Write-UpdateLog ("Launching Borealis agent redeploy via {0}." -f $scriptPath) 'STEP'
    try {
        $process = Start-Process -FilePath 'powershell.exe' -ArgumentList $argLine -WorkingDirectory $ProjectRoot -NoNewWindow -Wait -PassThru
    } catch {
        Write-UpdateLog ("Failed to start Borealis agent redeploy: {0}" -f $_.Exception.Message) 'ERROR'
        return $false
    }

    if (-not $process) {
        Write-UpdateLog "Borealis agent redeploy process did not start." 'ERROR'
        return $false
    }

    if ($process.ExitCode -ne 0) {
        Write-UpdateLog ("Borealis agent redeploy exited with code {0}." -f $process.ExitCode) 'ERROR'
        return $false
    }

    Write-UpdateLog "Borealis agent redeploy completed successfully." 'SUCCESS'
    return $true
}

function Get-AgentGuid {
    param(
        [string]$AgentRoot
    )

    $candidates = @()
    foreach ($root in (Get-AgentSettingsDirectoryCandidates -AgentRoot $AgentRoot)) {
        foreach ($leaf in @('Agent_GUID.txt', 'agent_GUID')) {
            try {
                $candidate = Join-Path $root $leaf
                if ($candidates -notcontains $candidate) { $candidates += $candidate }
            } catch {}
        }
    }

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

function Get-CanonicalAgentSettingsDirectory {
    param(
        [string]$AgentRoot
    )

    try {
        if ($env:BOREALIS_AGENT_SETTINGS_DIR) {
            $override = $env:BOREALIS_AGENT_SETTINGS_DIR.Trim()
            if ($override) { return $override }
        }
    } catch {}

    if (-not $AgentRoot) {
        $agentRootCandidate = Join-Path $scriptDir 'Agent\Borealis'
        if (Test-Path $agentRootCandidate -PathType Container) {
            $AgentRoot = $agentRootCandidate
        } else {
            $AgentRoot = $scriptDir
        }
    }

    try {
        return (Join-Path $AgentRoot 'Settings')
    } catch {
        return ''
    }
}

function Get-AgentSettingsDirectoryCandidates {
    param(
        [string]$AgentRoot
    )

    $candidates = @()
    $canonical = Get-CanonicalAgentSettingsDirectory -AgentRoot $AgentRoot
    if ($canonical -and ($candidates -notcontains $canonical)) { $candidates += $canonical }

    if ($AgentRoot) {
        if ($candidates -notcontains $AgentRoot) { $candidates += $AgentRoot }
        try {
            $agentParent = Split-Path -Path $AgentRoot -Parent
            if ($agentParent) {
                $legacySettings = Join-Path $agentParent 'Settings'
                if ($candidates -notcontains $legacySettings) { $candidates += $legacySettings }
            }
        } catch {}
    }

    foreach ($path in @(
        (Join-Path $scriptDir 'Agent\Borealis\Settings'),
        (Join-Path $scriptDir 'Agent\Borealis'),
        (Join-Path $scriptDir 'Agent\Settings')
    )) {
        if ($path -and ($candidates -notcontains $path)) { $candidates += $path }
    }

    return ($candidates | Select-Object -Unique)
}

function Copy-AgentSettingsFileIfMissing {
    param(
        [string]$Destination,
        [string]$Source,
        [switch]$Text
    )

    if (-not $Destination -or -not $Source) { return $false }
    if (Test-Path $Destination -PathType Leaf) { return $false }
    if (-not (Test-Path $Source -PathType Leaf)) { return $false }

    try {
        $parent = Split-Path -Path $Destination -Parent
        if ($parent -and -not (Test-Path $parent -PathType Container)) {
            New-Item -ItemType Directory -Path $parent -Force | Out-Null
        }
        if ($Text) {
            $content = Get-Content -Path $Source -Raw -ErrorAction Stop
            Set-Content -Path $Destination -Value $content -Encoding UTF8 -ErrorAction Stop
        } else {
            [System.IO.File]::WriteAllBytes($Destination, [System.IO.File]::ReadAllBytes($Source))
        }
        return $true
    } catch {
        return $false
    }
}

function Sync-AgentSettingsDirectoryMaterial {
    param(
        [string]$AgentRoot
    )

    $settingsDir = Get-CanonicalAgentSettingsDirectory -AgentRoot $AgentRoot
    if (-not $settingsDir) { return '' }

    try {
        if (-not (Test-Path $settingsDir -PathType Container)) {
            New-Item -ItemType Directory -Path $settingsDir -Force | Out-Null
        }
    } catch {
        return ''
    }

    $candidates = @(Get-AgentSettingsDirectoryCandidates -AgentRoot $AgentRoot | Where-Object {
        $_ -and ([string]$_).Trim() -and (([string]$_).TrimEnd('\') -ne $settingsDir.TrimEnd('\'))
    })

    $fileSpecs = @(
        @{ Destination = 'server_url.txt'; Sources = @('server_url.txt'); Text = $true },
        @{ Destination = 'Agent_GUID.txt'; Sources = @('Agent_GUID.txt', 'agent_GUID'); Text = $true },
        @{ Destination = 'refresh.token'; Sources = @('refresh.token'); Text = $false },
        @{ Destination = 'access.jwt'; Sources = @('access.jwt'); Text = $true },
        @{ Destination = 'access.meta.json'; Sources = @('access.meta.json'); Text = $true }
    )

    foreach ($spec in $fileSpecs) {
        $destination = Join-Path $settingsDir $spec.Destination
        if (Test-Path $destination -PathType Leaf) { continue }
        foreach ($candidateRoot in $candidates) {
            foreach ($sourceName in $spec.Sources) {
                try {
                    $source = Join-Path $candidateRoot $sourceName
                } catch {
                    continue
                }
                if (Copy-AgentSettingsFileIfMissing -Destination $destination -Source $source -Text:([bool]$spec.Text)) {
                    Write-UpdateLog ("Recovered agent settings file from legacy path: {0} -> {1}" -f $source, $destination) 'WARN'
                    break
                }
            }
            if (Test-Path $destination -PathType Leaf) { break }
        }
    }

    return $settingsDir
}

function Get-AgentSettingsDirectory {
    param(
        [string]$AgentRoot
    )

    $settingsDir = Sync-AgentSettingsDirectoryMaterial -AgentRoot $AgentRoot
    if ($settingsDir -and (Test-Path $settingsDir -PathType Container)) {
        return $settingsDir
    }
    return ''
}

function Get-ProtectedTokenString {
    param(
        [string]$Path
    )

    if (-not $Path -or -not (Test-Path $Path -PathType Leaf)) {
        return ''
    }

    try {
        $protected = [System.IO.File]::ReadAllBytes($Path)
        if (-not $protected -or $protected.Length -eq 0) { return '' }
    } catch {
        return ''
    }

    try {
        $rawJson = [System.Text.Encoding]::UTF8.GetString($protected)
        if ($rawJson) {
            $payload = $rawJson | ConvertFrom-Json -ErrorAction Stop
            if ($payload -and $payload.format -eq 'dpapi-multi' -and $payload.entries) {
                $preferredEntries = @()
                $preferredScope = ''
                try { $preferredScope = (($payload.preferred_scope) -as [string]).Trim().ToLowerInvariant() } catch {}
                if ($preferredScope -eq 'local_machine') {
                    $preferredEntries = @('local_machine', 'current_user')
                } else {
                    $preferredEntries = @('current_user', 'local_machine')
                }

                foreach ($entryName in $preferredEntries) {
                    try {
                        $encoded = (($payload.entries.$entryName) -as [string]).Trim()
                    } catch {
                        $encoded = ''
                    }
                    if (-not $encoded) { continue }
                    try {
                        $entryBytes = [Convert]::FromBase64String($encoded)
                    } catch {
                        continue
                    }
                    $entryScopes = if ($entryName -eq 'local_machine') {
                        @(
                            [System.Security.Cryptography.DataProtectionScope]::LocalMachine,
                            [System.Security.Cryptography.DataProtectionScope]::CurrentUser
                        )
                    } else {
                        @(
                            [System.Security.Cryptography.DataProtectionScope]::CurrentUser,
                            [System.Security.Cryptography.DataProtectionScope]::LocalMachine
                        )
                    }
                    foreach ($entryScope in $entryScopes) {
                        try {
                            $unprotected = [System.Security.Cryptography.ProtectedData]::Unprotect($entryBytes, $null, $entryScope)
                            if ($unprotected -and $unprotected.Length -gt 0) {
                                return [System.Text.Encoding]::UTF8.GetString($unprotected)
                            }
                        } catch {
                            continue
                        }
                    }
                }
            }
        }
    } catch {}

    $scopes = @(
        [System.Security.Cryptography.DataProtectionScope]::CurrentUser,
        [System.Security.Cryptography.DataProtectionScope]::LocalMachine
    )

    foreach ($scope in $scopes) {
        try {
            $unprotected = [System.Security.Cryptography.ProtectedData]::Unprotect($protected, $null, $scope)
            if ($unprotected -and $unprotected.Length -gt 0) {
                return [System.Text.Encoding]::UTF8.GetString($unprotected)
            }
        } catch {
            continue
        }
    }

    return ''
}

function Invoke-AgentTokenRefresh {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerBaseUrl,

        [Parameter(Mandatory = $true)]
        [string]$AgentGuid,

        [Parameter(Mandatory = $true)]
        [string]$RefreshToken,

        [string]$AgentRoot
    )

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl) -or [string]::IsNullOrWhiteSpace($AgentGuid) -or [string]::IsNullOrWhiteSpace($RefreshToken)) {
        Write-UpdateLog "Invoke-AgentTokenRefresh called with missing parameters." 'ERROR'
        return $null
    }

    Write-UpdateLog ("Requesting access token refresh for agent {0}" -f $AgentGuid) 'STEP'
    $base = $ServerBaseUrl.TrimEnd('/')
    $uri = "$base/api/agent/token/refresh"
    $payload = @{
        guid = $AgentGuid
        refresh_token = $RefreshToken
    } | ConvertTo-Json
    $headers = @{
        'User-Agent'    = 'borealis-agent-updater'
        'Content-Type'  = 'application/json'
    }

    $response = Invoke-AgentHttpRequest -Method 'POST' -Uri $uri -Headers $headers -Body $payload -ContentType 'application/json' -AgentRoot $AgentRoot -TimeoutSeconds 60
    if (-not $response) {
        Write-UpdateLog "Token refresh request produced no response." 'ERROR'
        return $null
    }

    try {
        $json = $response.Content | ConvertFrom-Json
    } catch {
        Write-UpdateLog ("Token refresh response decode failed: {0}" -f $_.Exception.Message) 'ERROR'
        return $null
    }

    if ($json -and $json.access_token) {
        $expiresIn = 900
        try {
            if ($json.expires_in) {
                $expiresIn = [int]$json.expires_in
            }
        } catch {}
        $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
        $expiresAt = $now + [Math]::Max(0, $expiresIn - 5)
        return [pscustomobject]@{
            AccessToken = ($json.access_token).Trim()
            ExpiresAt   = $expiresAt
        }
    }

    Write-UpdateLog "Token refresh response did not include access token." 'WARN'
    return $null
}

function Get-AgentAccessTokenContext {
    param(
        [string]$AgentRoot,
        [string]$ServerBaseUrl,
        [string]$AgentGuid
    )

    $settingsDir = Get-AgentSettingsDirectory -AgentRoot $AgentRoot
    if (-not $settingsDir) { return $null }

    Write-UpdateLog ("Loading agent access tokens from {0}" -f $settingsDir) 'DEBUG'
    $accessPath = Join-Path $settingsDir 'access.jwt'
    $metaPath   = Join-Path $settingsDir 'access.meta.json'
    $refreshPath = Join-Path $settingsDir 'refresh.token'

    $accessToken = ''
    $expiresAt = 0

    if (Test-Path $accessPath -PathType Leaf) {
        try {
            $accessToken = (Get-Content -Path $accessPath -Raw -ErrorAction Stop).Trim()
        } catch {
            $accessToken = ''
        }
    }

    if (Test-Path $metaPath -PathType Leaf) {
        try {
            $metaRaw = Get-Content -Path $metaPath -Raw -ErrorAction Stop
            if ($metaRaw) {
                $metaJson = $metaRaw | ConvertFrom-Json -ErrorAction Stop
                if ($metaJson -and $metaJson.access_expires_at) {
                    $expiresAt = [int]$metaJson.access_expires_at
                }
            }
        } catch {
            $expiresAt = 0
        }
    }

    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    if ($accessToken -and $expiresAt -gt ($now + 30)) {
        $secondsLeft = $expiresAt - $now
        Write-UpdateLog ("Using cached access token (expires in {0} seconds)." -f $secondsLeft) 'INFO'
        return [pscustomobject]@{
            AccessToken = $accessToken
            ExpiresAt   = $expiresAt
        }
    }

    $refreshToken = Get-ProtectedTokenString -Path $refreshPath
    if (-not $refreshToken) {
        Write-UpdateLog "Refresh token unavailable; cannot authenticate with server." 'ERROR'
        return $null
    }

    Write-UpdateLog "Cached token expired or missing; requesting refreshed access token." 'WARN'
    $refreshResult = Invoke-AgentTokenRefresh -ServerBaseUrl $ServerBaseUrl -AgentGuid $AgentGuid -RefreshToken $refreshToken -AgentRoot $AgentRoot
    if ($refreshResult -and $refreshResult.AccessToken) {
        Write-UpdateLog "Access token successfully refreshed." 'SUCCESS'
        return $refreshResult
    }

    Write-UpdateLog "Failed to refresh access token." 'ERROR'
    return $null
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

    if ($candidates.Count -gt 0) {
        Write-UpdateLog ("Evaluating repository hash from candidate roots: {0}" -f ([string]::Join(', ', $candidates))) 'DEBUG'
    }

    if ($GitExe -and (Test-Path $GitExe -PathType Leaf)) {
        foreach ($root in $candidates) {
            try {
                if (-not (Test-Path (Join-Path $root '.git') -PathType Container)) { continue }
                $revParse = Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $root -Arguments @('rev-parse','HEAD')
                if ($revParse) {
                    $candidate = ($revParse | Select-Object -Last 1)
                    if ($candidate) {
                        $result = $candidate.Trim()
                        if ($result) {
                            Write-UpdateLog ("Repository hash determined via git: {0}" -f $result) 'INFO'
                            return $result
                        }
                    }
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
        if ($stored) {
            Write-UpdateLog ("Using stored agent hash fallback: {0}" -f $stored) 'WARN'
            return $stored
        }
    }

    Write-UpdateLog "Unable to determine repository hash from any source." 'WARN'
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
        Write-UpdateLog ("Stored agent hash to {0}" -f $hashFile) 'DEBUG'
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
        Write-UpdateLog ("Wrote FETCH_HEAD in {0} to {1}" -f $gitDir, $CommitHash) 'DEBUG'
    } catch {}
}

function Get-ServerCurrentRepoHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerBaseUrl,
        [string]$AuthToken,
        [string]$AgentRoot
    )

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl)) { return $null }

    $base = $ServerBaseUrl.TrimEnd('/')
    $uri = "$base/api/repo/current_hash"
    $headers = @{ 'User-Agent' = 'borealis-agent-updater' }
    if ($AuthToken -and $AuthToken.Trim()) {
        $headers['Authorization'] = "Bearer $AuthToken"
    }

    $response = Invoke-AgentHttpRequest -Method 'GET' -Uri $uri -Headers $headers -AgentRoot $AgentRoot -TimeoutSeconds 40
    if ($response -and $response.StatusCode -ge 200 -and $response.StatusCode -lt 300) {
        try {
            $json = $response.Content | ConvertFrom-Json
            Write-UpdateLog ("Received repo hash payload from server (branch={0}, sha={1})." -f $json.branch, $json.sha) 'SUCCESS'
            return $json
        } catch {
            Write-Verbose ("Unable to decode repo hash response: {0}" -f $_.Exception.Message)
            Write-UpdateLog ("Failed to decode repo hash response JSON: {0}" -f $_.Exception.Message) 'ERROR'
            return $null
        }
    }

    if ($response) {
        Write-Verbose ("Repo hash request returned HTTP {0}: {1}" -f $response.StatusCode, $response.Content)
        Write-UpdateLog ("Repo hash request returned HTTP {0}: {1}" -f $response.StatusCode, $response.Content) 'WARN'
    } else {
        Write-Verbose ("Repo hash request to {0} returned no response." -f $uri)
        Write-UpdateLog ("Repo hash request to {0} returned no response." -f $uri) 'ERROR'
    }

    return $null
}

function Submit-AgentHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerBaseUrl,

        [Parameter(Mandatory = $true)]
        [string]$AgentId,

        [Parameter(Mandatory = $true)]
        [string]$AgentHash,

        [string]$AgentGuid,

        [string]$AuthToken,

        [string]$AgentRoot
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
    if ($AuthToken -and $AuthToken.Trim()) {
        $headers['Authorization'] = "Bearer $AuthToken"
    }

    $response = Invoke-AgentHttpRequest -Method 'POST' -Uri $uri -Headers $headers -Body $payload -ContentType 'application/json' -AgentRoot $AgentRoot -TimeoutSeconds 60
    if (-not $response) {
        Write-Verbose "Submit-AgentHash request returned no response."
        Write-UpdateLog "Agent hash submission produced no response from server." 'ERROR'
        return $null
    }

    Write-UpdateLog ("Agent hash submission HTTP status: {0}" -f $response.StatusCode) 'DEBUG'
    try {
        $json = $response.Content | ConvertFrom-Json
        return $json
    } catch {
        Write-Verbose ("Submit-AgentHash response decode failed: {0}" -f $_.Exception.Message)
        Write-UpdateLog ("Failed to parse agent hash submission response: {0}" -f $_.Exception.Message) 'ERROR'
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
        [string]$AuthToken = '',
        [string]$BranchName = 'main'
    )

    if ([string]::IsNullOrWhiteSpace($AgentHash)) { return }

    Write-UpdateLog ("Sync-AgentHashRecord invoked with hash {0}" -f $AgentHash) 'STEP'
    if ($ProjectRoot) {
        Set-GitFetchHeadHash -ProjectRoot $ProjectRoot -CommitHash $AgentHash -BranchName $BranchName
    }
    if ($AgentRoot) {
        Set-StoredAgentHash -AgentRoot $AgentRoot -AgentHash $AgentHash
    }

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl)) { return }

    Write-UpdateHost -Message ("Submitting agent hash to server: {0}" -f $AgentHash) -Level 'STEP'
    Write-UpdateLog ("Submitting agent hash to {0} (AgentId={1}, AgentGuid={2})" -f $ServerBaseUrl, $AgentId, $AgentGuid) 'STEP'

    if ([string]::IsNullOrWhiteSpace($AgentId) -and [string]::IsNullOrWhiteSpace($AgentGuid)) {
        Write-UpdateHost -Message "Agent identifier unavailable; skipping agent hash submission." -Color DarkYellow -Level 'WARN'
        Write-UpdateLog "Agent identifier unavailable; cannot submit hash to server." 'WARN'
        return
    }

    try {
        $submitResult = Submit-AgentHash -ServerBaseUrl $ServerBaseUrl -AgentId $AgentId -AgentHash $AgentHash -AgentGuid $AgentGuid -AuthToken $AuthToken -AgentRoot $AgentRoot
        if ($submitResult -and ($submitResult.status -eq 'ok')) {
            Write-UpdateHost -Message "The server-side agent hash database record was updated successfully." -Level 'SUCCESS'
            Write-UpdateLog "Server acknowledged agent hash update." 'SUCCESS'
        } elseif ($submitResult -and ($submitResult.status -eq 'ignored')) {
            Write-UpdateHost -Message "Server ignored the agent hash update (the agent is not enrolled with the server)." -Color DarkYellow -Level 'WARN'
            Write-UpdateLog "Server returned 'ignored' for agent hash submission." 'WARN'
        } elseif ($submitResult) {
            Write-UpdateHost -Message "Server agent_hash update response unrecognized.  We don't know what to do here. (Panic)" -Color DarkYellow -Level 'WARN'
            Write-UpdateLog ("Unexpected server response for agent hash submission: {0}" -f ($submitResult | ConvertTo-Json -Depth 5)) 'WARN'
        }
    } catch {
        Write-Verbose ("Failed to Submit Agent Hash: {0}" -f $_.Exception.Message)
        Write-UpdateLog ("Agent hash submission failed: {0}" -f $_.Exception.Message) 'ERROR'
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
        $cloneArgs = @('clone','--no-tags')
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

    Run-Step "Updating: Clean Up Update Staging Folder" {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $stagingPath
    }

    if (-not $Silent) {
        Write-UpdateHost -Message "Unattended Borealis update completed." -Color Green -Level 'SUCCESS'
    }
}

function Invoke-BorealisAgentUpdate {
    Write-UpdateHost -Message "==============================================="
    Write-UpdateHost -Message " Borealis - Automation Platform Updater Script "
    Write-UpdateHost -Message "==============================================="
    Write-UpdateLog "Starting Borealis updater execution." 'STEP'

    $agentRootCandidate = Join-Path $scriptDir 'Agent\Borealis'
    $agentRoot = $scriptDir
    if (Test-Path $agentRootCandidate -PathType Container) {
        try {
            $agentRoot = (Resolve-Path -Path $agentRootCandidate -ErrorAction Stop).Path
        } catch {
            $agentRoot = $agentRootCandidate
        }
    }
    Write-UpdateLog ("Agent root resolved to {0}" -f $agentRoot) 'INFO'
    [void](Sync-AgentSettingsDirectoryMaterial -AgentRoot $agentRoot)

    $agentGuid = Get-AgentGuid -AgentRoot $agentRoot
    if ($agentGuid) {
        Write-UpdateHost -Message ("Agent GUID: {0}" -f $agentGuid) -Level 'INFO'
        Write-UpdateLog ("Operating on agent GUID {0}" -f $agentGuid) 'INFO'
    } else {
        Write-UpdateHost -Message "Warning: No agent GUID detected - Please deploy the agent, associating it with a Borealis server then try running the updater script again." -Color Yellow -Level 'WARN'
        Write-UpdateHost -Message "⚠️ Borealis update aborted." -Color Yellow -Level 'ERROR'
        Write-UpdateLog "Agent GUID missing; aborting update." 'ERROR'
        return (New-UpdateSessionResult -Outcome 'aborted' -FinalLevel 'ERROR' -FinalMessage '===== Update.ps1 session aborted: agent GUID missing =====')
    }

    $gitExe = Get-GitExecutablePath -ProjectRoot $scriptDir
    if (-not $gitExe -or -not (Test-Path $gitExe -PathType Leaf)) {
        Write-UpdateHost -Message "Bundled or system Git was not found. Ensure Git is installed, then rerun the updater." -Color Yellow -Level 'WARN'
        Write-UpdateHost -Message "⚠️ Borealis update aborted." -Color Yellow -Level 'ERROR'
        Write-UpdateLog "Git executable not found; aborting update." 'ERROR'
        return (New-UpdateSessionResult -Outcome 'aborted' -FinalLevel 'ERROR' -FinalMessage '===== Update.ps1 session aborted: Git executable not found =====')
    }
    $resolvedRepositoryUrl = Resolve-BorealisRepositoryUrl -GitExe $gitExe -ProjectRoot $scriptDir
    Write-UpdateLog ("Repository origin resolved to {0}" -f $resolvedRepositoryUrl) 'INFO'

    $statusPayload = $null
    try {
        $statusPayload = Get-AgentUpdaterStatus -AgentRoot $agentRoot
    } catch {
        Write-UpdateLog ("Unable to load agent updater status: {0}" -f $_.Exception.Message) 'WARN'
    }

    $installedHash = ''
    $currentHash = ''
    $busyReasons = @()
    $deviceBusy = $false
    if ($statusPayload) {
        try { $installedHash = (($statusPayload.installed_build_id) -as [string]).Trim() } catch { $installedHash = '' }
        try { $currentHash = (($statusPayload.repo_build_id) -as [string]).Trim() } catch { $currentHash = '' }
        try {
            $deviceBusy = [bool]$statusPayload.busy
        } catch {
            $deviceBusy = $false
        }
        try {
            $busyReasons = @($statusPayload.reasons | ForEach-Object { ([string]$_).Trim() } | Where-Object { $_ })
        } catch {
            $busyReasons = @()
        }
    }
    if (-not $currentHash) {
        $currentHash = Get-RepositoryCommitHash -ProjectRoot $scriptDir -AgentRoot $agentRoot -GitExe $gitExe
    }

    $serverRepoInfo = $null
    try {
        Write-UpdateLog "Querying Borealis server for current repository hash." 'STEP'
        $serverRepoInfo = Get-AgentUpdaterRepoInfo -AgentRoot $agentRoot -Refresh
    } catch {
        Write-UpdateLog ("Agent helper repo-hash lookup failed: {0}" -f $_.Exception.Message) 'WARN'
    }

    if (-not $serverRepoInfo) {
        $serverBaseUrl = Get-BorealisServerUrl -AgentRoot $agentRoot
        if (-not $serverBaseUrl) {
            Write-UpdateHost -Message "The updater requires a configured public HTTPS FQDN in server_url.txt or BOREALIS_SERVER_URL." -Color Yellow -Level 'WARN'
            Write-UpdateLog "Server URL missing or invalid; updater cannot continue without a public HTTPS FQDN." 'ERROR'
            return (New-UpdateSessionResult -Outcome 'aborted' -FinalLevel 'ERROR' -FinalMessage '===== Update.ps1 session aborted: Borealis server URL missing or invalid =====')
        }
        Initialize-BorealisTlsContext -AgentRoot $agentRoot -ServerBaseUrl $serverBaseUrl
        $authContext = Get-AgentAccessTokenContext -AgentRoot $agentRoot -ServerBaseUrl $serverBaseUrl -AgentGuid $agentGuid
        if (-not $authContext -or -not $authContext.AccessToken) {
            Write-UpdateHost -Message "Unable to obtain agent authentication token. Ensure the agent is running and enrolled, then rerun the updater." -Color Yellow -Level 'WARN'
            Write-UpdateHost -Message "⚠️ Borealis update aborted." -Color Yellow -Level 'ERROR'
            Write-UpdateLog "Authentication context unavailable; aborting update." 'ERROR'
            return (New-UpdateSessionResult -Outcome 'aborted' -FinalLevel 'ERROR' -FinalMessage '===== Update.ps1 session aborted: authentication context unavailable =====')
        }
        $serverRepoInfo = Get-ServerCurrentRepoHash -ServerBaseUrl $serverBaseUrl -AuthToken $authContext.AccessToken -AgentRoot $agentRoot
    }

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
    Write-UpdateLog ("Updater mode: {0} (force={1})" -f $updateMode, $forceUpdate) 'INFO'

    if ($installedHash) {
        Write-UpdateHost -Message ("Installed Agent Hash: {0}" -f $installedHash) -Level 'INFO'
    } else {
        Write-UpdateHost -Message "Installed Agent Hash: unavailable" -Level 'INFO'
    }

    if ($currentHash) {
        Write-UpdateHost -Message ("Local Repo Hash: {0}" -f $currentHash) -Level 'INFO'
    } else {
        Write-UpdateHost -Message "Local Repo Hash: unavailable" -Level 'INFO'
    }

    if ($serverHash) {
        Write-UpdateHost -Message ("Borealis Server Hash: {0}" -f $serverHash) -Level 'INFO'
    } else {
        Write-UpdateHost -Message "Borealis Server Hash: unavailable" -Level 'INFO'
    }

    $normalizedInstalledHash = if ($installedHash) { $installedHash.Trim().ToLowerInvariant() } else { '' }
    $normalizedLocalHash = if ($currentHash) { $currentHash.Trim().ToLowerInvariant() } else { '' }
    $normalizedServerHash = if ($serverHash) { $serverHash.Trim().ToLowerInvariant() } else { '' }
    $runtimeNeedsUpdate = (-not $normalizedInstalledHash) -or (-not $normalizedServerHash) -or ($normalizedInstalledHash -ne $normalizedServerHash)
    $repoNeedsSync = (-not $normalizedLocalHash) -or (-not $normalizedServerHash) -or ($normalizedLocalHash -ne $normalizedServerHash)
    $needsUpdate = $forceUpdate -or $runtimeNeedsUpdate -or $repoNeedsSync

    if ($forceUpdate) {
        Write-UpdateHost -Message "Force update requested; skipping hash comparison." -Color Yellow -Level 'WARN'
        Write-UpdateLog "Force update requested; bypassing hash comparison." 'WARN'
    } elseif (-not $serverHash) {
        Write-UpdateHost -Message "Borealis server hash unavailable; cannot continue." -Color Yellow -Level 'WARN'
        Write-UpdateHost -Message "⚠️ Borealis update aborted." -Color Yellow -Level 'ERROR'
        Write-UpdateLog "Server hash unavailable; aborting." 'ERROR'
        return (New-UpdateSessionResult -Outcome 'aborted' -FinalLevel 'ERROR' -FinalMessage '===== Update.ps1 session aborted: server hash unavailable =====')
    } elseif (-not $needsUpdate) {
        Write-UpdateHost -Message "Local agent runtime already matches the server repository hash." -Color Green -Level 'SUCCESS'
        Write-UpdateLog "Installed agent build already matches the target hash." 'SUCCESS'
        [void](Sync-AgentInstalledBuildId -AgentRoot $agentRoot)
        Write-UpdateHost -Message "✅ Borealis - Automation Platform Already Up-to-Date" -Color Green -Level 'SUCCESS'
        return (New-UpdateSessionResult -Outcome 'up_to_date' -FinalLevel 'SUCCESS' -FinalMessage '===== Update.ps1 session finished: agent already up to date =====')
    } else {
        Write-UpdateHost -Message "Repository hash mismatch detected; update required." -Level 'WARN'
        Write-UpdateLog ("Repository hash mismatch detected (installed={0}, repo={1}, remote={2})." -f $installedHash, $currentHash, $serverHash) 'WARN'
    }

    if ($deviceBusy) {
        $reasonText = if ($busyReasons.Count -gt 0) { $busyReasons -join ', ' } else { 'unspecified activity' }
        Write-UpdateHost -Message ("Agent update deferred because the device is busy: {0}" -f $reasonText) -Color Yellow -Level 'WARN'
        Write-UpdateLog ("Device busy; deferring update. Reasons: {0}" -f $reasonText) 'WARN'
        return (New-UpdateSessionResult -Outcome 'deferred' -FinalLevel 'WARN' -FinalMessage ("===== Update.ps1 session deferred: device busy ({0}) =====" -f $reasonText))
    }

    $mutex = $null
    $gotMutex = $false
    $managedTasks = @()
    $refreshSucceeded = $false
    try {
        $mutex = New-Object System.Threading.Mutex($false, 'Global\BorealisUpdate')
        $gotMutex = $mutex.WaitOne(0)
        if (-not $gotMutex) {
            Write-Verbose 'Another update is already running (mutex held). Exiting quietly.'
            Write-UpdateHost -Message "⚠️ Borealis update already in progress on this device." -Color Yellow -Level 'WARN'
            return (New-UpdateSessionResult -Outcome 'in_progress' -FinalLevel 'WARN' -FinalMessage '===== Update.ps1 session skipped: another update is already running =====')
        }

        try {
            $statusPayload = Get-AgentUpdaterStatus -AgentRoot $agentRoot
            $deviceBusy = [bool]$statusPayload.busy
            $busyReasons = @($statusPayload.reasons | ForEach-Object { ([string]$_).Trim() } | Where-Object { $_ })
        } catch {
            $deviceBusy = $false
            $busyReasons = @()
        }
        if ($deviceBusy) {
            $reasonText = if ($busyReasons.Count -gt 0) { $busyReasons -join ', ' } else { 'unspecified activity' }
            Write-UpdateHost -Message ("Agent update deferred because the device became busy: {0}" -f $reasonText) -Color Yellow -Level 'WARN'
            Write-UpdateLog ("Device busy after mutex acquisition; deferring update. Reasons: {0}" -f $reasonText) 'WARN'
            return (New-UpdateSessionResult -Outcome 'deferred' -FinalLevel 'WARN' -FinalMessage ("===== Update.ps1 session deferred after lock: device busy ({0}) =====" -f $reasonText))
        }

        $managedTasks = Stop-AgentScheduledTasks -TaskNames @('Borealis Agent','Borealis Agent (UserHelper)')
        if ($managedTasks.Count -gt 0) {
            Write-UpdateLog ("Managed tasks stopped: {0}" -f ($managedTasks -join ', ')) 'INFO'
        } else {
            Write-UpdateLog "No managed tasks were running when update started." 'DEBUG'
        }
        Run-Step "Updating: Terminate Running Python Processes" { Stop-AgentPythonProcesses -ProjectRoot $scriptDir -SkipEngine }

        try {
            Write-UpdateLog ("Starting repository sync to commit {0} (branch={1})." -f $serverHash, $serverBranch) 'STEP'
            Invoke-BorealisRepoSync -GitExe $gitExe -ProjectRoot $scriptDir -RepositoryUrl $resolvedRepositoryUrl -TargetHash $serverHash -BranchName $serverBranch
            Write-UpdateLog "Repository sync completed successfully." 'SUCCESS'

            $newHash = Get-RepositoryCommitHash -ProjectRoot $scriptDir -AgentRoot $agentRoot -GitExe $gitExe
            $normalizedNewHash = if ($newHash) { $newHash.Trim().ToLowerInvariant() } else { '' }
            if ($normalizedServerHash -and $normalizedNewHash -and $normalizedNewHash -ne $normalizedServerHash) {
                throw ("Repository sync completed, but HEAD ({0}) does not match target hash ({1})." -f $newHash, $serverHash)
            }

            Run-Step "Updating: Refresh Borealis Agent Runtime" {
                $refreshOk = Invoke-BorealisAgentRuntimeRefresh -ProjectRoot $scriptDir
                if (-not $refreshOk) {
                    throw 'Borealis agent runtime refresh failed.'
                }
            }

            $syncedBuildId = Sync-AgentInstalledBuildId -AgentRoot $agentRoot
            if ($syncedBuildId) {
                Write-UpdateLog ("Installed build id synced to {0}." -f $syncedBuildId) 'INFO'
            }

            try {
                $statusPayload = Get-AgentUpdaterStatus -AgentRoot $agentRoot
                $installedHash = (($statusPayload.installed_build_id) -as [string]).Trim()
            } catch {
                $installedHash = $syncedBuildId
            }

            if ($normalizedServerHash -and $installedHash -and $installedHash.Trim().ToLowerInvariant() -ne $normalizedServerHash) {
                Write-UpdateLog ("Installed build id after refresh ({0}) does not yet match target hash ({1})." -f $installedHash, $serverHash) 'WARN'
            }

            $refreshSucceeded = $true
            Write-UpdateHost -Message "✅ Borealis - Automation Platform Successfully Updated" -Color Green -Level 'SUCCESS'
            Write-UpdateLog "Update workflow completed successfully." 'SUCCESS'
            return (New-UpdateSessionResult -Outcome 'success' -FinalLevel 'SUCCESS' -FinalMessage '===== Update.ps1 session finished successfully =====')
        } finally {
            if (-not $refreshSucceeded -and $managedTasks.Count -gt 0) {
                Start-AgentScheduledTasks -TaskNames $managedTasks
                Write-UpdateLog "Agent scheduled tasks restarted after unsuccessful refresh." 'INFO'
            }
        }

    } finally {
        if ($mutex -and $gotMutex) { $mutex.ReleaseMutex() | Out-Null }
        if ($mutex) { $mutex.Dispose() }
        Write-UpdateLog "Released update mutex and cleaned up resources." 'DEBUG'
    }
}

Initialize-UpdateLogging
try {
    $updateResult = @(Invoke-BorealisAgentUpdate) | Select-Object -Last 1
    if (-not $updateResult -or -not $updateResult.PSObject.Properties['FinalLevel'] -or -not $updateResult.PSObject.Properties['FinalMessage']) {
        $updateResult = New-UpdateSessionResult -Outcome 'aborted' -FinalLevel 'ERROR' -FinalMessage '===== Update.ps1 session ended without a final result ====='
    }
    Finalize-UpdateLogging -Level $updateResult.FinalLevel -Message $updateResult.FinalMessage
} catch {
    Write-UpdateLog ("Unhandled updater failure: {0}" -f $_.Exception.Message) 'ERROR'
    Finalize-UpdateLogging -Level 'ERROR' -Message ("===== Update.ps1 session failed: {0} =====" -f $_.Exception.Message)
    throw
}
