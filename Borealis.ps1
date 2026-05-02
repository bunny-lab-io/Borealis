#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Borealis.ps1

[CmdletBinding()]
param(
    [switch]$Agent,
    [switch]$RefreshAgentRuntime,
    [Alias('DeleteServerTrust', 'ForceReEnroll')]
    [switch]$NewEngine,
    [string]$EnrollmentCode = '',
    [string]$ServerUrl = ''
)

function Test-BorealisTruthyValue {
    param(
        [AllowNull()]
        [object]$Value
    )

    try {
        $normalized = [string]$Value
    } catch {
        return $false
    }
    if ([string]::IsNullOrWhiteSpace($normalized)) {
        return $false
    }
    switch ($normalized.Trim().ToLowerInvariant()) {
        '1' { return $true }
        'true' { return $true }
        'yes' { return $true }
        'on' { return $true }
        default { return $false }
    }
}

if (-not $NewEngine -and $Agent -and (Test-BorealisTruthyValue $env:BOREALIS_BOOTSTRAP_NEW_ENGINE)) {
    $NewEngine = $true
}

# Admin/Elevation helpers for Borealis runtime
$script:BorealisElevatedExitCode = $null

function Test-IsAdmin {
    try {
        $id = [Security.Principal.WindowsIdentity]::GetCurrent()
        $p  = New-Object Security.Principal.WindowsPrincipal($id)
        return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    } catch { return $false }
}

function Request-BorealisElevation {
    param(
        [string]$ScriptPath,
        [hashtable]$BoundParameters,
        [string[]]$ExtraArgs
    )
    if (Test-IsAdmin) { return $true }

    Write-Host ""  # spacer
    Write-Host "Borealis requires Administrator permissions for Agent deployment." -ForegroundColor Yellow -BackgroundColor Black
    Write-Host "Grant elevated permissions now? (Y/N)" -ForegroundColor Yellow -BackgroundColor Black
    $resp = Read-Host
    if ($resp -notin @('y','Y','yes','YES')) { return $false }

    $argTokens = @('-NoProfile','-ExecutionPolicy','Bypass','-File', $ScriptPath)
    $boundParameterKeys = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
    if ($BoundParameters) {
        foreach ($entry in $BoundParameters.GetEnumerator()) {
            $key = $entry.Key
            $value = $entry.Value
            [void]$boundParameterKeys.Add([string]$key)
            if ($value -is [System.Management.Automation.SwitchParameter]) {
                if ($value.IsPresent) { $argTokens += "-$key" }
                continue
            }
            if ($value -is [bool]) {
                if ($value) { $argTokens += "-$key" }
                continue
            }
            if ($null -ne $value -and "$value" -ne "") {
                $argTokens += "-$key"
                $argTokens += "$value"
            }
        }
    }

    if (-not $boundParameterKeys.Contains('ServerUrl') -and -not [string]::IsNullOrWhiteSpace($env:BOREALIS_SERVER_URL)) {
        $argTokens += '-ServerUrl'
        $argTokens += $env:BOREALIS_SERVER_URL.Trim()
    }
    if (-not $boundParameterKeys.Contains('EnrollmentCode') -and -not [string]::IsNullOrWhiteSpace($env:BOREALIS_ENROLLMENT_CODE)) {
        $argTokens += '-EnrollmentCode'
        $argTokens += $env:BOREALIS_ENROLLMENT_CODE.Trim()
    }

    if ($ExtraArgs) { $argTokens += $ExtraArgs }

    $argLine = ($argTokens | ForEach-Object {
        $text = [string]$_
        if ($text -match '\s') {
            '"' + ($text -replace '"','`"') + '"'
        } else {
            $text
        }
    }) -join ' '

    try {
        $proc = Start-Process -FilePath 'powershell.exe' -Verb RunAs -ArgumentList $argLine -WindowStyle Normal -PassThru
        if ($proc) {
            $proc.WaitForExit()
            try { $script:BorealisElevatedExitCode = [int]$proc.ExitCode } catch { $script:BorealisElevatedExitCode = 0 }
        }
        return $false  # stop current non-elevated instance
    } catch {
        Write-Host "Elevation was denied or failed." -ForegroundColor Red
        $script:BorealisElevatedExitCode = 1
        return $false
    }
}

$scriptPath = $PSCommandPath
if (-not $scriptPath -or $scriptPath -eq '') { $scriptPath = $MyInvocation.MyCommand.Definition }
if (-not (Request-BorealisElevation -ScriptPath $scriptPath -BoundParameters $PSBoundParameters -ExtraArgs $MyInvocation.UnboundArguments)) {
    if ($null -ne $script:BorealisElevatedExitCode) {
        exit ([int]$script:BorealisElevatedExitCode)
    }
    exit 0
}

$scriptDir = Split-Path $MyInvocation.MyCommand.Path -Parent
$host.UI.RawUI.WindowTitle = "Borealis"
Clear-Host

# ---------------------- ASCII Art Terminal Required Changes ----------------------
# Set the .NET Console output encoding to UTF8
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

# Change the Windows OEM code page to 65001 (UTF-8)
chcp.com 65001 > $null

# ---------------------- Add Common Functions Used Throughout Script ----------------------
$symbols = @{
    Success = [char]0x2705
    Running = [char]0x23F3
    Fail    = [char]0x274C
    Info    = [char]0x2139
}

function Set-FileUtf8Content {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter()]
        [AllowNull()]
        [object]$Value = ''
    )

    $text = if ($null -eq $Value) { '' } else { [string]$Value }
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)

    try {
        Set-Content -Path $Path -Value $text -Encoding UTF8NoBOM -ErrorAction Stop
    } catch [System.Management.Automation.ParameterBindingException] {
        [System.IO.File]::WriteAllText($Path, $text, $utf8NoBom)
    } catch {
        [System.IO.File]::WriteAllText($Path, $text, $utf8NoBom)
    }
}

function Expand-ZipArchiveWithFallback {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ArchivePath,

        [Parameter(Mandatory = $true)]
        [string]$DestinationPath,

        [Parameter()]
        [string]$SevenZipPath = '',

        [Parameter()]
        [switch]$ClearDestination
    )

    if (-not (Test-Path $ArchivePath -PathType Leaf)) {
        throw "Archive file not found: $ArchivePath"
    }

    if ($ClearDestination -and (Test-Path $DestinationPath)) {
        Remove-Item -Path $DestinationPath -Recurse -Force -ErrorAction SilentlyContinue
    }
    if (-not (Test-Path $DestinationPath)) {
        New-Item -ItemType Directory -Path $DestinationPath -Force | Out-Null
    }

    if ([string]::IsNullOrWhiteSpace($SevenZipPath) -or -not (Test-Path $SevenZipPath -PathType Leaf)) {
        throw "7-Zip CLI not found at: $SevenZipPath"
    }

    & $SevenZipPath x $ArchivePath "-o$DestinationPath" -y | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "7-Zip extraction failed for '$ArchivePath' with exit code $LASTEXITCODE."
    }
}

# Ensure log directories
function Ensure-AgentLogDir {
    $agentRoot = Join-Path $scriptDir 'Agent'
    if (-not (Test-Path $agentRoot)) { New-Item -ItemType Directory -Path $agentRoot -Force | Out-Null }
    $agentLogDir = Join-Path $agentRoot 'Logs'
    if (-not (Test-Path $agentLogDir)) { New-Item -ItemType Directory -Path $agentLogDir -Force | Out-Null }
    return $agentLogDir
}

function Ensure-AgentTrayFolderPermissions {
    param(
        [Parameter(Mandatory = $true)]
        [string]$TrayDir,

        [Parameter()]
        [string]$LogName = 'Install.log'
    )

    if (-not $TrayDir) { return }
    if (-not (Test-Path $TrayDir -PathType Container)) {
        New-Item -Path $TrayDir -ItemType Directory -Force | Out-Null
    }

    $resolvedTrayDir = $TrayDir
    try {
        $resolvedTrayDir = (Resolve-Path -Path $TrayDir -ErrorAction Stop).ProviderPath
    } catch {
        try { $resolvedTrayDir = [System.IO.Path]::GetFullPath($TrayDir) } catch {}
    }

    Write-AgentLog -FileName $LogName -Message ("[TRAY-ACL] Hardening tray folder permissions at '{0}'." -f $resolvedTrayDir)

    $aclArgs = @(
        $resolvedTrayDir,
        '/inheritance:r',
        '/grant:r', '*S-1-5-32-544:(OI)(CI)(F)',
        '/grant:r', '*S-1-5-18:(OI)(CI)(F)',
        '/grant:r', '*S-1-5-11:(OI)(CI)(M)',
        '/grant:r', '*S-1-5-32-545:(OI)(CI)(RX)',
        '/t',
        '/c',
        '/q'
    )

    $aclOutput = & icacls.exe @aclArgs 2>&1
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        $detail = (($aclOutput | ForEach-Object { $_.ToString().Trim() }) | Where-Object { $_ } | Select-Object -First 5) -join '; '
        if (-not $detail) { $detail = 'icacls returned an error without diagnostic output.' }
        throw ("Failed to harden tray folder permissions at '{0}' (exit code {1}): {2}" -f $resolvedTrayDir, $exitCode, $detail)
    }
}

function Write-AgentLog {
    param(
        [string]$FileName,
        [string]$Message
    )
    $dir = Ensure-AgentLogDir
    $path = Join-Path $dir $FileName
    $ts = Get-Date -Format s
    "[$ts] $Message" | Out-File -FilePath $path -Append -Encoding UTF8
}

$script:Utf8CodePageChanged = $false

function Ensure-SystemUtf8CodePage {
    param([string]$LogName = 'Install.log')

    $codePageKey = 'HKLM:\SYSTEM\CurrentControlSet\Control\Nls\CodePage'
    $target = '65001'
    try {
        $props = Get-ItemProperty -Path $codePageKey -ErrorAction Stop
        $currentAcp = ($props.ACP | ForEach-Object { $_.ToString() })
        $currentOem = ($props.OEMCP | ForEach-Object { $_.ToString() })
        Write-AgentLog -FileName $LogName -Message ("[UTF8] Detected ACP={0} OEMCP={1}" -f $currentAcp,$currentOem)
    } catch {
        Write-AgentLog -FileName $LogName -Message ("[UTF8] Failed to read code page info: {0}" -f $_.Exception.Message)
        return
    }

    if ($currentAcp -eq $target -and $currentOem -eq $target) {
        Write-AgentLog -FileName $LogName -Message '[UTF8] System code pages already set to 65001 (UTF-8).'
        return
    }

    Write-AgentLog -FileName $LogName -Message '[UTF8] Updating system code pages to UTF-8 (65001). Requires reboot to finalize.'
    try {
        Set-ItemProperty -Path $codePageKey -Name 'ACP' -Value $target -Force
        Set-ItemProperty -Path $codePageKey -Name 'OEMCP' -Value $target -Force
        try { Set-ItemProperty -Path $codePageKey -Name 'MACCP' -Value $target -Force } catch {}
        $script:Utf8CodePageChanged = $true
        Write-AgentLog -FileName $LogName -Message '[UTF8] Code page registry values updated successfully.'
    } catch {
        Write-AgentLog -FileName $LogName -Message ("[UTF8] Failed to update code pages: {0}" -f $_.Exception.Message)
    }
}

function Ensure-SoftwareSASGeneration {
    param([string]$LogName = 'Install.log')

    $policyPath = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System'
    $valueName = 'SoftwareSASGeneration'
    $current = $null
    try {
        $props = Get-ItemProperty -Path $policyPath -Name $valueName -ErrorAction SilentlyContinue
        if ($props -and ($props.PSObject.Properties.Name -contains $valueName)) {
            $current = [int]$props.$valueName
        }
    } catch {
        $current = $null
    }

    if ($null -ne $current -and $current -ge 1) {
        Write-AgentLog -FileName $LogName -Message ("[SAS] {0} already set to {1}." -f $valueName, $current)
        return
    }

    try {
        if (-not (Test-Path $policyPath)) { New-Item -Path $policyPath -Force | Out-Null }
        New-ItemProperty -Path $policyPath -Name $valueName -PropertyType DWord -Value 1 -Force | Out-Null
        Write-AgentLog -FileName $LogName -Message ("[SAS] Set {0}=1 to allow software SAS/CAD." -f $valueName)
    } catch {
        Write-AgentLog -FileName $LogName -Message ("[SAS] Failed to set {0}: {1}" -f $valueName, $_.Exception.Message)
    }
}

# Forcefully remove legacy and current Borealis services and tasks
function Remove-BorealisServicesAndTasks {
    param([string]$LogName)
    $svcNames = @('BorealisAgent','BorealisScriptService','BorealisScriptAgent')
    foreach ($n in $svcNames) {
        Write-AgentLog -FileName $LogName -Message "Attempting to stop service: $n"
        try { sc.exe stop $n 2>$null | Out-Null } catch {}
        Start-Sleep -Milliseconds 300
        Write-AgentLog -FileName $LogName -Message "Attempting to delete service: $n"
        try { sc.exe delete $n 2>$null | Out-Null } catch {}
    }
    # Remove all Borealis scheduled tasks (supervisor/watchdog/legacy/user helper)
    try {
        $tasks = @()
        try { $tasks = Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object { $_.TaskName -like 'Borealis Agent*' -or $_.TaskName -like 'Borealis*Supervisor*' -or $_.TaskName -like 'Borealis*Watchdog*' } } catch {}
        foreach ($t in $tasks) {
            Write-AgentLog -FileName $LogName -Message ("Deleting scheduled task: {0}" -f $t.TaskName)
            try { Unregister-ScheduledTask -TaskName $t.TaskName -Confirm:$false -ErrorAction SilentlyContinue } catch {}
        }
        # Fallback to schtasks for machines without the ScheduledTasks module
        foreach ($tn in @('Borealis Agent','Borealis Agent (UserHelper)','Borealis Agent - Supervisor','Borealis Agent - Watchdog')) {
            try { schtasks.exe /Delete /TN "$tn" /F 2>$null | Out-Null } catch {}
        }
    } catch {}

    # Gracefully stop only Agent venv Python processes (avoid killing dev web UI/node)
    Write-Host "Stopping Agent Python processes scoped to Agent venv..." -ForegroundColor Yellow
    Write-AgentLog -FileName $LogName -Message "Stopping Agent Python processes in Agent\\*"
    try {
        Get-Process python,pythonw -ErrorAction SilentlyContinue |
            Where-Object { $_.Path -like (Join-Path $scriptDir 'Agent\*') } |
            ForEach-Object { try { $_ | Stop-Process -Force } catch {} }
    } catch {}
    # Remove legacy watchdog script if present
    try { Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $env:ProgramData 'Borealis\watchdog.ps1') } catch {}
}

function Clear-AgentEnrollmentState {
    param([string]$LogName = 'Install.log')

    $settingsDirs = @(
        (Join-Path $scriptDir 'Agent\Borealis\Settings'),
        (Join-Path $scriptDir 'Agent\Settings')
    )
    $filesToRemove = @(
        'Agent_GUID.txt',
        'access.jwt',
        'access.meta.json',
        'refresh.token',
        'server_signing_key.pub',
        'installer_code.shared.json'
    )

    Write-AgentLog -FileName $LogName -Message '[REENROLL] Force reenroll requested; clearing persisted enrollment state while preserving the device identity keypair.'

    foreach ($settingsDir in $settingsDirs) {
        foreach ($fileName in $filesToRemove) {
            $targetPath = Join-Path $settingsDir $fileName
            if (Test-Path $targetPath -PathType Leaf) {
                try {
                    Remove-Item -Path $targetPath -Force -ErrorAction Stop
                    Write-AgentLog -FileName $LogName -Message ("[REENROLL] Removed persisted enrollment artifact '{0}'." -f $targetPath)
                } catch {
                    Write-AgentLog -FileName $LogName -Message ("[REENROLL] Failed to remove '{0}': {1}" -f $targetPath, $_.Exception.Message)
                }
            }
        }
    }

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

$script:BorealisCurlExe = $null

function Get-BorealisCurlExe {
    param([switch]$Refresh)

    if ($Refresh) {
        $script:BorealisCurlExe = $null
    }
    if ($script:BorealisCurlExe -and (Test-Path $script:BorealisCurlExe -PathType Leaf)) {
        return $script:BorealisCurlExe
    }

    $candidates = New-Object System.Collections.Generic.List[string]
    if (-not [string]::IsNullOrWhiteSpace($env:BOREALIS_CURL_EXE)) {
        $candidates.Add($env:BOREALIS_CURL_EXE.Trim())
    }
    if (-not [string]::IsNullOrWhiteSpace($scriptDir)) {
        $candidates.Add((Join-Path $scriptDir 'Dependencies\curl\bin\curl.exe'))
    }
    if (-not [string]::IsNullOrWhiteSpace($depsRoot)) {
        $candidates.Add((Join-Path $depsRoot 'curl\bin\curl.exe'))
    }
    $systemCurl = Get-Command curl.exe -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -First 1
    if (-not [string]::IsNullOrWhiteSpace($systemCurl)) {
        $candidates.Add($systemCurl)
    }

    foreach ($candidate in ($candidates | Select-Object -Unique)) {
        if (-not [string]::IsNullOrWhiteSpace($candidate) -and (Test-Path $candidate -PathType Leaf)) {
            $script:BorealisCurlExe = $candidate
            break
        }
    }

    return $script:BorealisCurlExe
}

function Invoke-BorealisDownload {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Url,

        [Parameter(Mandatory = $true)]
        [string]$DestinationPath,

        [Parameter()]
        [int]$MaxAttempts = 3,

        [Parameter()]
        [int]$InitialDelaySeconds = 2,

        [Parameter()]
        [string]$LogName = 'Install.log',

        [Parameter()]
        [string]$LogPrefix = '[Download]'
    )

    if ([string]::IsNullOrWhiteSpace($Url)) {
        throw "$LogPrefix Download URL cannot be empty."
    }
    if ([string]::IsNullOrWhiteSpace($DestinationPath)) {
        throw "$LogPrefix Destination path cannot be empty."
    }

    $destDir = Split-Path -Path $DestinationPath -Parent
    if (-not [string]::IsNullOrWhiteSpace($destDir) -and -not (Test-Path $destDir)) {
        New-Item -ItemType Directory -Path $destDir -Force | Out-Null
    }

    $lastError = $null
    for ($attempt = 1; $attempt -le $MaxAttempts; $attempt++) {
        try {
            if (Test-Path $DestinationPath -PathType Leaf) {
                Remove-Item -Path $DestinationPath -Force -ErrorAction SilentlyContinue
            }

            Write-AgentLog -FileName $LogName -Message ("{0} Download attempt {1}/{2} from {3}" -f $LogPrefix, $attempt, $MaxAttempts, $Url)
            $downloaded = $false
            $methodErrors = New-Object System.Collections.Generic.List[string]

            $curlExe = Get-BorealisCurlExe
            if (-not [string]::IsNullOrWhiteSpace($curlExe)) {
                try {
                    $curlArgs = @(
                        '--fail',
                        '--location',
                        '--retry', '3',
                        '--retry-all-errors',
                        '--connect-timeout', '20',
                        '--output', $DestinationPath,
                        $Url
                    )
                    & $curlExe @curlArgs | Out-Null
                    if ($LASTEXITCODE -ne 0) {
                        throw "curl exited with code $LASTEXITCODE."
                    }
                    $downloaded = $true
                } catch {
                    $methodErrors.Add("curl: $($_.Exception.Message)")
                }
            } else {
                $methodErrors.Add('curl: no curl executable found')
            }

            if (-not $downloaded) {
                $bitsCommand = Get-Command Start-BitsTransfer -ErrorAction SilentlyContinue
                if ($bitsCommand) {
                    try {
                        Start-BitsTransfer -Source $Url -Destination $DestinationPath -ErrorAction Stop
                        $downloaded = $true
                    } catch {
                        $methodErrors.Add("BITS: $($_.Exception.Message)")
                    }
                } else {
                    $methodErrors.Add('BITS: Start-BitsTransfer unavailable')
                }
            }

            if (-not $downloaded) {
                try {
                    $iwrParams = @{
                        Uri = $Url
                        OutFile = $DestinationPath
                        ErrorAction = 'Stop'
                    }
                    $iwrCommand = Get-Command Invoke-WebRequest -ErrorAction Stop
                    if ($iwrCommand.Parameters.ContainsKey('UseBasicParsing')) {
                        $iwrParams['UseBasicParsing'] = $true
                    }
                    $oldProgressPreference = $ProgressPreference
                    $ProgressPreference = 'SilentlyContinue'
                    try {
                        Invoke-WebRequest @iwrParams
                    } finally {
                        $ProgressPreference = $oldProgressPreference
                    }
                    $downloaded = $true
                } catch {
                    $methodErrors.Add("Invoke-WebRequest: $($_.Exception.Message)")
                }
            }

            if (-not $downloaded) {
                throw "All download methods failed. $($methodErrors -join '; ')"
            }
            if (-not (Test-Path $DestinationPath -PathType Leaf)) {
                throw "Download completed but '$DestinationPath' was not created."
            }
            $fileInfo = Get-Item -LiteralPath $DestinationPath -ErrorAction Stop
            if ($fileInfo.Length -le 0) {
                throw "Downloaded file '$DestinationPath' is empty."
            }

            return
        } catch {
            $lastError = $_
            Write-AgentLog -FileName $LogName -Message ("{0} Download attempt failed: {1}" -f $LogPrefix, $_.Exception.Message)
            if ($attempt -lt $MaxAttempts) {
                $delaySeconds = [Math]::Min(30, [int]([Math]::Pow(2, $attempt - 1) * $InitialDelaySeconds))
                Start-Sleep -Seconds $delaySeconds
            }
        }
    }

    if ($lastError) {
        throw $lastError
    }
    throw "$LogPrefix Failed to download '$Url'."
}

function Get-BorealisFileSha256 {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    if (-not (Test-Path $Path -PathType Leaf)) {
        throw "Cannot hash missing file '$Path'."
    }
    $hashInfo = Get-FileHash -LiteralPath $Path -Algorithm SHA256 -ErrorAction Stop
    return ($hashInfo.Hash | ForEach-Object { $_.ToLowerInvariant() })
}

function Assert-BorealisFileSha256 {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [string]$ExpectedSha256,

        [Parameter()]
        [string]$LogName = 'Install.log',

        [Parameter()]
        [string]$LogPrefix = '[Verify]'
    )

    $expected = ($ExpectedSha256 | ForEach-Object { $_.Trim().ToLowerInvariant() })
    if ([string]::IsNullOrWhiteSpace($expected)) {
        throw "$LogPrefix Expected SHA256 cannot be empty."
    }
    $actual = Get-BorealisFileSha256 -Path $Path
    if ($actual -ne $expected) {
        throw "$LogPrefix SHA256 mismatch for '$Path'. Expected $expected but found $actual."
    }
    Write-AgentLog -FileName $LogName -Message ("{0} SHA256 verified for {1}" -f $LogPrefix, $Path)
}

function Assert-BorealisAuthenticode {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter()]
        [string[]]$PublisherPatterns = @(),

        [Parameter()]
        [string]$LogName = 'Install.log',

        [Parameter()]
        [string]$LogPrefix = '[Verify]'
    )

    if (-not (Test-Path $Path -PathType Leaf)) {
        throw "$LogPrefix Cannot verify missing file '$Path'."
    }

    $signature = Get-AuthenticodeSignature -FilePath $Path -ErrorAction Stop
    if ($signature.Status -ne 'Valid') {
        throw "$LogPrefix Authenticode validation failed for '$Path' (status=$($signature.Status))."
    }

    $subject = ''
    $issuer = ''
    if ($signature.SignerCertificate) {
        $subject = [string]$signature.SignerCertificate.Subject
        $issuer = [string]$signature.SignerCertificate.Issuer
    }
    if ($PublisherPatterns -and $PublisherPatterns.Count -gt 0) {
        $matched = $false
        foreach ($pattern in $PublisherPatterns) {
            if ([string]::IsNullOrWhiteSpace($pattern)) { continue }
            $escaped = [regex]::Escape($pattern)
            if ($subject -match $escaped -or $issuer -match $escaped) {
                $matched = $true
                break
            }
        }
        if (-not $matched) {
            throw "$LogPrefix Authenticode publisher mismatch for '$Path' (subject='$subject', issuer='$issuer')."
        }
    }

    Write-AgentLog -FileName $LogName -Message (
        "{0} Authenticode verified for {1} (subject={2})" -f $LogPrefix, $Path, ($subject -replace '\s+', ' ').Trim()
    )
}

function Invoke-BorealisVerifiedDownload {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Url,

        [Parameter(Mandatory = $true)]
        [string]$DestinationPath,

        [Parameter()]
        [string]$ExpectedSha256 = '',

        [Parameter()]
        [switch]$RequireAuthenticode,

        [Parameter()]
        [string[]]$PublisherPatterns = @(),

        [Parameter()]
        [string]$LogName = 'Install.log',

        [Parameter()]
        [string]$LogPrefix = '[Download]'
    )

    $verified = $false
    if (Test-Path $DestinationPath -PathType Leaf) {
        try {
            if (-not [string]::IsNullOrWhiteSpace($ExpectedSha256)) {
                Assert-BorealisFileSha256 -Path $DestinationPath -ExpectedSha256 $ExpectedSha256 -LogName $LogName -LogPrefix $LogPrefix
            }
            if ($RequireAuthenticode) {
                Assert-BorealisAuthenticode -Path $DestinationPath -PublisherPatterns $PublisherPatterns -LogName $LogName -LogPrefix $LogPrefix
            }
            $verified = $true
        } catch {
            Write-AgentLog -FileName $LogName -Message ("{0} Cached artifact failed verification and will be re-downloaded: {1}" -f $LogPrefix, $_.Exception.Message)
            Remove-Item -LiteralPath $DestinationPath -Force -ErrorAction SilentlyContinue
        }
    }

    if (-not $verified) {
        Invoke-BorealisDownload -Url $Url -DestinationPath $DestinationPath -LogName $LogName -LogPrefix $LogPrefix
        if (-not [string]::IsNullOrWhiteSpace($ExpectedSha256)) {
            Assert-BorealisFileSha256 -Path $DestinationPath -ExpectedSha256 $ExpectedSha256 -LogName $LogName -LogPrefix $LogPrefix
        }
        if ($RequireAuthenticode) {
            Assert-BorealisAuthenticode -Path $DestinationPath -PublisherPatterns $PublisherPatterns -LogName $LogName -LogPrefix $LogPrefix
        }
    }
}

# ---------------------- Bundle Executables Setup ----------------------
$scriptDir  = Split-Path $MyInvocation.MyCommand.Path -Parent
$depsRoot   = Join-Path $scriptDir 'Dependencies'
$pythonExe  = Join-Path $depsRoot 'Python\python.exe'
$sevenZipExe = Join-Path $depsRoot '7zip\7z.exe'
$gitVersionTag  = 'v2.47.1.windows.1'
$gitPackageName = 'MinGit-2.47.1-64-bit.zip'
$gitZipUrl      = "https://github.com/git-for-windows/git/releases/download/$gitVersionTag/$gitPackageName"
$gitZipPath     = Join-Path $depsRoot $gitPackageName
$gitInstallDir  = Join-Path $depsRoot 'git'
$gitExePath     = Join-Path $gitInstallDir 'cmd\git.exe'
$wireGuardDownloadRoot     = "https://download.wireguard.com/windows-client/"
$wireGuardInstallerDir     = Join-Path $depsRoot 'VPN_Tunnel_Adapter'
$wireGuardBootstrapperName = 'wireguard-installer.exe'
$wireGuardBootstrapperPath = Join-Path $wireGuardInstallerDir $wireGuardBootstrapperName
$wireGuardMsiVersion       = '0.5.3'
$wireGuardTunnelLegacyName   = 'BorealisWireGuardTunnel'
$wireGuardTunnelNameInternal = 'Borealis'
$wireGuardTunnelNameFriendly = 'Borealis'
$wireGuardTunnelBootstrapAddress = '169.254.255.254/32'
$wireGuardMsiFiles = @{
    'X64'   = "wireguard-amd64-$wireGuardMsiVersion.msi"
    'AMD64' = "wireguard-amd64-$wireGuardMsiVersion.msi"
    'ARM64' = "wireguard-arm64-$wireGuardMsiVersion.msi"
    'X86'   = "wireguard-x86-$wireGuardMsiVersion.msi"
}
$wintunVersion        = '0.14.1'
$wintunZipName        = "wintun-$wintunVersion.zip"
$wintunDownloadUrl    = "https://www.wintun.net/builds/$wintunZipName"
$wintunZipPath        = Join-Path $wireGuardInstallerDir $wintunZipName

# ---------------------- Dependency Installation Functions ----------------------
function Install_Shared_Dependencies {
    # Python bootstrap for the Borealis Agent runtime.
    Run-Step "Dependency: Python" {
        $pythonInstallDir = Join-Path $scriptDir "Dependencies\Python"
        $localPythonExe   = Join-Path $pythonInstallDir "python.exe"

        $pythonMsiBaseUrl = "https://www.python.org/ftp/python/3.13.3/amd64/"
        $pythonMsiFiles = @(
            "core.msi",
            "exe.msi",
            "lib.msi",
            "pip.msi",
            "dev.msi"
        )

        if (-not (Test-Path $localPythonExe)) {
            if (-not (Test-Path $pythonInstallDir)) {
                New-Item -ItemType Directory -Path $pythonInstallDir | Out-Null
            }

            foreach ($file in $pythonMsiFiles) {
                $url = "$pythonMsiBaseUrl$file"
                $localPath = Join-Path $scriptDir "Dependencies\$file"

                # Download if missing
                if (-not (Test-Path $localPath)) {
                    Invoke-BorealisDownload -Url $url -DestinationPath $localPath -LogName 'Install.log' -LogPrefix '[Python]'
                }

                # Extract MSI into install directory
                Start-Process -Wait -NoNewWindow -FilePath "msiexec.exe" `
                    -ArgumentList "/a `"$localPath`" /qn TARGETDIR=`"$pythonInstallDir`""
            }

            # Clean up downloaded MSIs
            foreach ($file in $pythonMsiFiles) {
                $localPath = Join-Path $scriptDir "Dependencies\$file"
                Remove-Item $localPath -Force -ErrorAction SilentlyContinue
            }

            # Validate success
            if (-not (Test-Path $localPythonExe)) {
                throw "Python executable not found after MSI extraction."
            }
        }
    }
}

function Install_Agent_Dependencies {
    function Get-WireGuardMsiName {
        param([string]$ArchitectureTag)

        if (-not $ArchitectureTag) { return $wireGuardMsiFiles['X64'] }
        $normalized = $ArchitectureTag.ToUpperInvariant()
        if ($wireGuardMsiFiles.ContainsKey($normalized)) {
            return $wireGuardMsiFiles[$normalized]
        }
        return $wireGuardMsiFiles['X64']
    }

    function Ensure-WireGuardInstallerFile {
        param(
            [string]$Url,
            [string]$DestinationPath,
            [string]$LogName = 'Install.log'
        )

        if (-not $Url -or -not $DestinationPath) { return }
        $destDir = Split-Path $DestinationPath -Parent
        if (-not (Test-Path $destDir)) {
            try {
                New-Item -ItemType Directory -Path $destDir -Force | Out-Null
                Write-AgentLog -FileName $LogName -Message ("[WireGuard] Created installer cache at {0}" -f $destDir)
            } catch {}
        }

        if (Test-Path $DestinationPath -PathType Leaf) {
            Write-AgentLog -FileName $LogName -Message ("[WireGuard] Installer already cached at {0}" -f $DestinationPath)
            return
        }

        Write-AgentLog -FileName $LogName -Message ("[WireGuard] Downloading installer from {0}" -f $Url)
        Invoke-BorealisDownload -Url $Url -DestinationPath $DestinationPath -LogName $LogName -LogPrefix '[WireGuard]'
        Write-AgentLog -FileName $LogName -Message ("[WireGuard] Cached installer at {0}" -f $DestinationPath)
    }

    function Get-WireGuardInstallState {
        $state = [ordered]@{
            Installed      = $false
            Version        = $null
            ExePath        = $null
            ServicePresent = $false
            DriverPresent  = $false
            DriverPaths    = @()
            AdapterPresent = $false
            AdapterNames   = @()
            DedicatedAdapterPresent = $false
            DedicatedAdapterNames   = @()
        }

        $exeCandidates = @(
            (Join-Path $env:ProgramFiles 'WireGuard\wireguard.exe'),
            (Join-Path $env:ProgramFiles 'WireGuard\wg.exe'),
            (Join-Path ${env:ProgramFiles(x86)} 'WireGuard\wireguard.exe'),
            (Join-Path ${env:ProgramFiles(x86)} 'WireGuard\wg.exe')
        ) | Where-Object { $_ }

        foreach ($candidate in $exeCandidates) {
            if (Test-Path $candidate -PathType Leaf) {
                $state.Installed = $true
                if (-not $state.ExePath) { $state.ExePath = $candidate }
            }
        }

        try {
            $svc = Get-Service -Name 'WireGuardManager' -ErrorAction Stop
            if ($svc) { $state.ServicePresent = $true; $state.Installed = $true }
        } catch {}

        $driverCandidates = @()
        if ($env:WINDIR) {
            $driverCandidates += (Join-Path $env:WINDIR 'System32\drivers\wintun.sys')
            $driverCandidates += (Join-Path $env:WINDIR 'System32\drivers\*wintun*.sys')
            $driverCandidates += (Join-Path $env:WINDIR 'Sysnative\drivers\wintun.sys')
            $driverCandidates += (Join-Path $env:WINDIR 'Sysnative\drivers\*wintun*.sys')
        }
        $driverCandidates = $driverCandidates | Where-Object { $_ }
        $driverHits = @()
        foreach ($driver in $driverCandidates) {
            try {
                $items = Get-ChildItem -Path $driver -ErrorAction SilentlyContinue -Force
                foreach ($item in $items) {
                    if ($item -and $item.PSIsContainer -eq $false) {
                        $driverHits += $item.FullName
                    }
                }
            } catch {}
        }
        if ($driverHits.Count -gt 0) {
            $state.DriverPresent = $true
            $state.Installed = $true
            $state.DriverPaths = $driverHits | Select-Object -Unique
        }

        try {
            $adapters = Get-WireGuardAdapters
            if ($adapters) {
                $state.AdapterPresent = $true
                $state.AdapterNames = $adapters | Select-Object -ExpandProperty Name -Unique
                $state.DriverPresent = $true
                $state.Installed = $true
            }

            $dedicated = @()
            foreach ($adapter in ($adapters | Where-Object { $_ })) {
                if (Test-WireGuardAdapterName -Adapter $adapter -ExpectedName $wireGuardTunnelNameFriendly) {
                    $dedicated += $adapter
                }
            }
            if ($dedicated.Count -gt 0) {
                $state.DedicatedAdapterPresent = $true
                $state.DedicatedAdapterNames = $dedicated | Select-Object -ExpandProperty Name -Unique
                $state.DriverPresent = $true
                $state.Installed = $true
            }
        } catch {}

        $uninstallRoots = @(
            'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall',
            'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall'
        )
        foreach ($root in $uninstallRoots) {
            try {
                $items = Get-ChildItem -Path $root -ErrorAction Stop
                foreach ($item in $items) {
                    try {
                        $props = Get-ItemProperty -Path $item.PSPath -ErrorAction Stop
                        $name = $props.DisplayName
                        if ($name -and $name -like 'WireGuard*') {
                            $state.Installed = $true
                            if ($props.DisplayVersion) {
                                $state.Version = $props.DisplayVersion
                            }
                            break
                        }
                    } catch {}
                }
            } catch {}
            if ($state.Version) { break }
        }

        return [pscustomobject]$state
    }

    function Install-WireGuardMsi {
        param(
            [string]$InstallerPath,
            [string]$BootstrapperPath,
            [string]$LogName = 'Install.log'
        )

        $logPrefix = '[WireGuard]'
        if (-not (Test-IsAdmin)) {
            $msg = "$logPrefix Admin rights are required to install WireGuard."
            Write-AgentLog -FileName $LogName -Message $msg
            throw $msg
        }

        if (-not (Test-Path $InstallerPath -PathType Leaf)) {
            $msg = "$logPrefix Installer not found at $InstallerPath"
            Write-AgentLog -FileName $LogName -Message $msg
            throw $msg
        }

        Write-AgentLog -FileName $LogName -Message ("$logPrefix Installing WireGuard from {0}" -f $InstallerPath)
        $args = "/i `"$InstallerPath`" /qn /norestart"
        try {
            $proc = Start-Process -FilePath 'msiexec.exe' -ArgumentList $args -Wait -PassThru -WindowStyle Hidden -ErrorAction Stop
            $exitCode = $proc.ExitCode
            Write-AgentLog -FileName $LogName -Message ("$logPrefix msiexec exit code: {0}" -f $exitCode)
            if ($exitCode -eq 0) { return }

            $fallbackReason = "WireGuard MSI install returned exit code $exitCode"
            Write-AgentLog -FileName $LogName -Message ("$logPrefix $fallbackReason")

            if ($BootstrapperPath -and (Test-Path $BootstrapperPath -PathType Leaf)) {
                Write-AgentLog -FileName $LogName -Message ("$logPrefix Falling back to bootstrapper at {0}" -f $BootstrapperPath)
                try {
                    $bp = Start-Process -FilePath $BootstrapperPath -ArgumentList '/install','/quiet' -Wait -PassThru -WindowStyle Hidden -ErrorAction Stop
                    $bpExit = $bp.ExitCode
                    Write-AgentLog -FileName $LogName -Message ("$logPrefix Bootstrapper exit code: {0}" -f $bpExit)
                    if ($bpExit -eq 0) { return }
                    throw "$logPrefix Bootstrapper returned exit code $bpExit"
                } catch {
                    $err = $_.Exception.Message
                    Write-AgentLog -FileName $LogName -Message ("$logPrefix Bootstrapper install failed: {0}" -f $err)
                    throw
                }
            }

            throw $fallbackReason
        } catch {
            $err = $_.Exception.Message
            Write-AgentLog -FileName $LogName -Message ("$logPrefix Installation failed: {0}" -f $err)
            throw
        }
    }

    function New-WireGuardKeyPair {
        param(
            [string]$WireGuardExe,
            [string]$WgExe
        )

        $pair = @{ PrivateKey = $null; PublicKey = $null; Source = $null }
        $wgCli = $null
        if ($WgExe -and (Test-Path $WgExe -PathType Leaf)) { $wgCli = $WgExe }

        $priv = $null
        if ($wgCli) {
            try { $priv = (& $wgCli genkey) } catch {}
            if ($priv) {
                $priv = $priv.Trim()
                $pair.PrivateKey = $priv
                $pair.Source = $wgCli
                try {
                    $psi = New-Object System.Diagnostics.ProcessStartInfo
                    $psi.FileName = $wgCli
                    $psi.Arguments = 'pubkey'
                    $psi.RedirectStandardInput = $true
                    $psi.RedirectStandardOutput = $true
                    $psi.RedirectStandardError = $true
                    $psi.UseShellExecute = $false
                    $psi.CreateNoWindow = $true
                    if ($psi.PSObject.Properties.Name -contains 'StandardInputEncoding') {
                        $psi.StandardInputEncoding = [System.Text.Encoding]::ASCII
                    }
                    if ($psi.PSObject.Properties.Name -contains 'StandardOutputEncoding') {
                        $psi.StandardOutputEncoding = [System.Text.Encoding]::ASCII
                    }
                    $proc = New-Object System.Diagnostics.Process
                    $proc.StartInfo = $psi
                    $proc.Start() | Out-Null
                    $proc.StandardInput.WriteLine($priv)
                    $proc.StandardInput.Close()
                    $pub = $proc.StandardOutput.ReadToEnd()
                    $proc.WaitForExit()
                    if ($proc.ExitCode -eq 0 -and $pub) {
                        $pair.PublicKey = $pub.Trim()
                    }
                } catch {}
            }
        }

        if (-not $pair.PrivateKey -and $WireGuardExe -and (Test-Path $WireGuardExe -PathType Leaf)) {
            try {
                $priv = (& $WireGuardExe genkey)
                if ($priv) {
                    $pair.PrivateKey = $priv.Trim()
                    $pair.Source = $WireGuardExe
                }
            } catch {}
        }

        if (-not $pair.PrivateKey) {
            try {
                $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
                $bytes = New-Object byte[] 32
                $rng.GetBytes($bytes)
                $priv = [System.Convert]::ToBase64String($bytes)
                $pair.PrivateKey = $priv
                $pair.PublicKey = $null
                $pair.Source = 'rng'
            } catch {}
        }

        return $pair
    }

    function Find-WireGuardDriverInf {
        param(
            [Parameter()][string]$LogName = 'Install.log'
        )

        $logPrefix = '[WireGuard]'
        $output = $null
        try {
            $output = & pnputil.exe /enum-drivers
        } catch {
            Write-AgentLog -FileName $LogName -Message ("$logPrefix pnputil /enum-drivers failed: {0}" -f $_.Exception.Message)
            return $null
        }

        if (-not $output) { return $null }
        $published = $null
        $original = $null
        $provider = $null
        foreach ($line in ($output -split "`r?`n")) {
            if (-not $line.Trim()) {
                if ($published -and ($original -match '^(wireguard|wintun)\.inf$' -or $provider -like '*WireGuard*')) {
                    return $published
                }
                $published = $null
                $original = $null
                $provider = $null
                continue
            }
            if ($line -match 'Published Name\s*:\s*(\S+)') {
                $published = $Matches[1].Trim()
                continue
            }
            if ($line -match 'Original Name\s*:\s*(\S+)') {
                $original = $Matches[1].Trim()
                continue
            }
            if ($line -match 'Provider Name\s*:\s*(.+)') {
                $provider = $Matches[1].Trim()
                continue
            }
        }

        if ($published -and ($original -match '^(wireguard|wintun)\.inf$' -or $provider -like '*WireGuard*')) {
            return $published
        }
        return $null
    }

    function Get-WireGuardAdapters {
        param([string]$NameFilter)

        $args = @{
            Namespace   = 'root/StandardCimv2'
            ClassName   = 'MSFT_NetAdapter'
            ErrorAction = 'SilentlyContinue'
        }
        if ($NameFilter) {
            $args.Filter = "Name='$NameFilter'"
        } else {
            $args.Filter = "InterfaceDescription LIKE '%WireGuard%'"
        }
        try {
            $opTimeout = (Get-Command Get-CimInstance).Parameters.ContainsKey('OperationTimeoutSec')
            if ($opTimeout) { $args.OperationTimeoutSec = 10 }
        } catch {}

        try {
            return Get-CimInstance @args
        } catch {
            return $null
        }
    }

    function Test-WireGuardAdapterName {
        param(
            [Parameter()][object]$Adapter,
            [Parameter(Mandatory = $true)][string]$ExpectedName
        )

        if (-not $Adapter -or -not $ExpectedName) { return $false }
        $adapterName = $Adapter.Name
        if (-not $adapterName) { return $false }
        if ($adapterName.ToString().Trim().ToLowerInvariant() -ne $ExpectedName.ToLowerInvariant()) {
            return $false
        }
        $desc = $Adapter.InterfaceDescription
        if (-not $desc) {
            return $false
        }
        if ($desc.ToString() -notlike '*WireGuard*') {
            return $false
        }
        return $true
    }

    function Get-WireGuardAdapterByName {
        param([string]$AdapterName)

        if (-not $AdapterName) { return $null }
        try {
            $adapters = Get-WireGuardAdapters -NameFilter $AdapterName
            if ($adapters) {
                foreach ($adapter in @($adapters)) {
                    if (Test-WireGuardAdapterName -Adapter $adapter -ExpectedName $AdapterName) {
                        return $adapter
                    }
                }
            }
        } catch {}
        return $null
    }

    function Rename-WireGuardAdapterName {
        param(
            [Parameter(Mandatory = $true)][string]$OldName,
            [Parameter(Mandatory = $true)][string]$NewName,
            [Parameter()][string]$LogName = 'Install.log'
        )

        $logPrefix = '[WireGuard]'
        if ($OldName -eq $NewName) { return $true }
        $args = "interface set interface name=`"$OldName`" newname=`"$NewName`""
        $proc = Start-Process -FilePath 'netsh.exe' -ArgumentList $args -PassThru -WindowStyle Hidden -ErrorAction SilentlyContinue
        if (-not $proc) {
            Write-AgentLog -FileName $LogName -Message "$logPrefix netsh rename failed to start."
            return $false
        }
        if (-not $proc.WaitForExit(10000)) {
            try { $proc.Kill() } catch {}
            Write-AgentLog -FileName $LogName -Message "$logPrefix netsh rename timed out."
            return $false
        }
        if ($proc.ExitCode -ne 0) {
            Write-AgentLog -FileName $LogName -Message ("$logPrefix netsh rename failed with exit code {0}." -f $proc.ExitCode)
            return $false
        }
        Write-AgentLog -FileName $LogName -Message ("$logPrefix Renamed adapter {0} -> {1}" -f $OldName, $NewName)
        return $true
    }

    function Remove-WireGuardTunnelService {
        param(
            [Parameter(Mandatory = $true)][string]$TunnelName,
            [Parameter(Mandatory = $true)][string]$WireGuardExe,
            [Parameter()][string]$LogName = 'Install.log'
        )

        if (-not $TunnelName) { return }
        $logPrefix = '[WireGuard]'
        $serviceName = 'WireGuardTunnel$' + $TunnelName
        Write-AgentLog -FileName $LogName -Message ("$logPrefix Cleaning tunnel service {0}" -f $serviceName)
        $serviceExists = $false
        try {
            $svc = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
            if ($svc) { $serviceExists = $true }
        } catch {}

        if ($serviceExists) {
            try {
                $proc = Start-Process -FilePath 'sc.exe' -ArgumentList 'stop', $serviceName -PassThru -WindowStyle Hidden -ErrorAction SilentlyContinue
                if ($proc) {
                    if (-not $proc.WaitForExit(10000)) {
                        try { $proc.Kill() } catch {}
                    }
                }
            } catch {}
        } else {
            Write-AgentLog -FileName $LogName -Message ("$logPrefix Tunnel service {0} not present; skipping stop/uninstall." -f $serviceName)
        }

        if ($serviceExists -and $WireGuardExe -and (Test-Path $WireGuardExe -PathType Leaf)) {
            try {
                $proc = Start-Process -FilePath $WireGuardExe -ArgumentList @('/uninstalltunnelservice', $TunnelName) -PassThru -WindowStyle Hidden -ErrorAction SilentlyContinue
                if ($proc) {
                    if (-not $proc.WaitForExit(15000)) {
                        try { $proc.Kill() } catch {}
                        Write-AgentLog -FileName $LogName -Message ("$logPrefix /uninstalltunnelservice timed out for {0}" -f $TunnelName)
                    } else {
                        Write-AgentLog -FileName $LogName -Message ("$logPrefix /uninstalltunnelservice exit code for {0}: {1}" -f $TunnelName, $proc.ExitCode)
                    }
                }
            } catch {}
        }

        if ($serviceExists) {
            try {
                $proc = Start-Process -FilePath 'sc.exe' -ArgumentList 'delete', $serviceName -PassThru -WindowStyle Hidden -ErrorAction SilentlyContinue
                if ($proc) {
                    if (-not $proc.WaitForExit(10000)) {
                        try { $proc.Kill() } catch {}
                    } else {
                        Write-AgentLog -FileName $LogName -Message ("$logPrefix sc delete exit code for {0}: {1}" -f $serviceName, $proc.ExitCode)
                    }
                }
            } catch {}
        }

        try {
            $confPaths = Get-WireGuardConfigPaths -TunnelName $TunnelName
            foreach ($confPath in $confPaths) {
                if ($confPath -and (Test-Path $confPath -PathType Leaf)) {
                    Remove-Item -Path $confPath -Force -ErrorAction SilentlyContinue
                    Write-AgentLog -FileName $LogName -Message ("$logPrefix Removed tunnel config {0}" -f $confPath)
                }
            }
        } catch {}
    }

    function Get-WireGuardConfigPaths {
        param([string]$TunnelName)

        if (-not $TunnelName) { return @() }
        $paths = @()
        $borealisConfigDir = $null
        try {
            if ($scriptDir) {
                $borealisConfigDir = Join-Path $scriptDir 'Agent\Borealis\Settings\WireGuard'
            }
        } catch {}
        if ($borealisConfigDir) {
            $paths += $borealisConfigDir
        }
        if ($env:ProgramFiles) {
            $paths += (Join-Path $env:ProgramFiles 'WireGuard\Data\Configurations')
        }
        $programDataRoot = if ($env:ProgramData) { $env:ProgramData } else { 'C:\ProgramData' }
        if ($programDataRoot) {
            $paths += (Join-Path $programDataRoot 'Borealis\WireGuard\Configurations')
        }
        if ($wireGuardInstallerDir) {
            $paths += $wireGuardInstallerDir
        }
        $paths = $paths | Where-Object { $_ }
        $confPaths = @()
        foreach ($dir in $paths) {
            $confPaths += (Join-Path $dir "$TunnelName.conf")
        }
        return $confPaths | Select-Object -Unique
    }

    function Get-WireGuardConfigPath {
        param([string]$TunnelName)

        if (-not $TunnelName) { return $null }
        $confPaths = Get-WireGuardConfigPaths -TunnelName $TunnelName
        foreach ($confPath in $confPaths) {
            if (-not $confPath) { continue }
            $dir = Split-Path $confPath -Parent
            try {
                if ($dir -and -not (Test-Path $dir)) {
                    New-Item -ItemType Directory -Path $dir -Force | Out-Null
                }
            } catch {}
            if ($dir -and (Test-Path $dir)) {
                return $confPath
            }
        }
        return $confPaths | Select-Object -First 1
    }

    function Get-WireGuardTunnelServiceConfigPath {
        param([string]$ServiceName)

        if (-not $ServiceName) { return $null }
        $regPath = "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName"
        try {
            $imagePath = (Get-ItemProperty -Path $regPath -Name ImagePath -ErrorAction Stop).ImagePath
        } catch {
            return $null
        }
        if (-not $imagePath) { return $null }
        $text = $imagePath.ToString()
        if ($text -match '(?i)/tunnelservice\s+"([^"]+)"') {
            return $Matches[1]
        }
        if ($text -match '(?i)/tunnelservice\s+(\S+)') {
            return $Matches[1]
        }
        return $null
    }

    function Ensure-WireGuardTunnelAdapter {
        param(
            [Parameter(Mandatory = $true)][string]$WireGuardExe,
            [Parameter()][string]$LogName = 'Install.log'
        )

        $logPrefix = '[WireGuard]'
        $friendlyName = $wireGuardTunnelNameFriendly
        $internalName = $wireGuardTunnelNameInternal
        $serviceName = 'WireGuardTunnel$' + $internalName

        Write-AgentLog -FileName $LogName -Message ("$logPrefix Ensuring tunnel adapter: {0}" -f $friendlyName)
        $existing = Get-WireGuardAdapterByName -AdapterName $friendlyName
        if ($existing) {
            Write-AgentLog -FileName $LogName -Message ("$logPrefix Adapter already present: {0}" -f $friendlyName)
            return $true
        }

        $internalAdapter = Get-WireGuardAdapterByName -AdapterName $internalName
        if ($internalAdapter) {
            if ($friendlyName -and $friendlyName -ne $internalName) {
                $renamed = Rename-WireGuardAdapterName -OldName $internalName -NewName $friendlyName -LogName $LogName
                if ($renamed) { return $true }
                return $false
            }
            return $true
        }

        try {
            $wgCliExe = $null
            try {
                $wgCliCandidate = Join-Path (Split-Path $WireGuardExe -Parent) 'wg.exe'
                if (Test-Path $wgCliCandidate -PathType Leaf) { $wgCliExe = $wgCliCandidate }
            } catch {}

            $serviceConfigPath = Get-WireGuardTunnelServiceConfigPath -ServiceName $serviceName
            $existingService = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
            $servicePresent = $false
            if ($existingService -or $serviceConfigPath) { $servicePresent = $true }
            if ($servicePresent) {
                $serviceConfigPath = Get-WireGuardTunnelServiceConfigPath -ServiceName $serviceName
                if ($serviceConfigPath -and -not (Test-Path $serviceConfigPath -PathType Leaf)) {
                    $keyPair = New-WireGuardKeyPair -WireGuardExe $WireGuardExe -WgExe $wgCliExe
                    if ($keyPair.PrivateKey) {
                        $conf = @"
[Interface]
PrivateKey = $($keyPair.PrivateKey.Trim())
Address = $wireGuardTunnelBootstrapAddress
ListenPort = 0
"@
                        try {
                            $dir = Split-Path $serviceConfigPath -Parent
                            if ($dir -and -not (Test-Path $dir)) {
                                New-Item -ItemType Directory -Path $dir -Force | Out-Null
                            }
                            Set-Content -Path $serviceConfigPath -Value $conf -Encoding ASCII -Force
                            if (Test-Path $serviceConfigPath -PathType Leaf) {
                                Write-AgentLog -FileName $LogName -Message ("$logPrefix Created adapter config at {0}" -f $serviceConfigPath)
                            }
                        } catch {
                            Write-AgentLog -FileName $LogName -Message ("$logPrefix Failed to write adapter config at {0}: {1}" -f $serviceConfigPath, $_.Exception.Message)
                        }
                    }
                }

                Write-AgentLog -FileName $LogName -Message "$logPrefix Tunnel service already present; restarting to provision adapter."
                try { Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue } catch {}
                try { Start-Service -Name $serviceName -ErrorAction SilentlyContinue } catch {}
                for ($i = 0; $i -lt 10; $i++) {
                    Start-Sleep -Milliseconds 500
                    if (Get-WireGuardAdapterByName -AdapterName $friendlyName) { return $true }
                }
                Write-AgentLog -FileName $LogName -Message "$logPrefix Tunnel service present but adapter missing; uninstalling before reinstall."
            }

            Remove-WireGuardTunnelService -TunnelName $internalName -WireGuardExe $WireGuardExe -LogName $LogName
            if ($wireGuardTunnelLegacyName -and $wireGuardTunnelLegacyName -ne $internalName) {
                Remove-WireGuardTunnelService -TunnelName $wireGuardTunnelLegacyName -WireGuardExe $WireGuardExe -LogName $LogName
            }
            for ($i = 0; $i -lt 10; $i++) {
                Start-Sleep -Milliseconds 500
                $stillPresent = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
                $stillConfig = Get-WireGuardTunnelServiceConfigPath -ServiceName $serviceName
                if (-not $stillPresent -and -not $stillConfig) { break }
            }
            $serviceStillPresent = $false
            if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) { $serviceStillPresent = $true }
            if (Get-WireGuardTunnelServiceConfigPath -ServiceName $serviceName) { $serviceStillPresent = $true }
            if ($serviceStillPresent) {
                Write-AgentLog -FileName $LogName -Message "$logPrefix Tunnel service still present after uninstall; skipping reinstall to avoid WireGuard dialog."
                return $false
            }

            try {
                if (-not (Test-Path $wireGuardInstallerDir)) {
                    New-Item -ItemType Directory -Path $wireGuardInstallerDir -Force | Out-Null
                }
            } catch {}

            $keyPair = New-WireGuardKeyPair -WireGuardExe $WireGuardExe -WgExe $wgCliExe
            if (-not $keyPair.PrivateKey) {
                Write-AgentLog -FileName $LogName -Message "$logPrefix Failed to generate WireGuard keypair for adapter provision."
                return $false
            }

            $conf = @"
[Interface]
PrivateKey = $($keyPair.PrivateKey.Trim())
Address = $wireGuardTunnelBootstrapAddress
ListenPort = 0
"@
            $confPath = $null
            $confPaths = Get-WireGuardConfigPaths -TunnelName $internalName
            if ($serviceConfigPath) {
                $confPaths = @($serviceConfigPath) + ($confPaths | Where-Object { $_ -ne $serviceConfigPath })
            }
            foreach ($candidate in $confPaths) {
                if (-not $candidate) { continue }
                $dir = Split-Path $candidate -Parent
                try {
                    if ($dir -and -not (Test-Path $dir)) {
                        New-Item -ItemType Directory -Path $dir -Force | Out-Null
                    }
                } catch {}
                try {
                    Set-Content -Path $candidate -Value $conf -Encoding ASCII -Force
                    if (Test-Path $candidate -PathType Leaf) {
                        $confPath = $candidate
                        Write-AgentLog -FileName $LogName -Message ("$logPrefix Created adapter config at {0}" -f $confPath)
                        $borealisConfigDir = $null
                        try {
                            if ($scriptDir) {
                                $borealisConfigDir = Join-Path $scriptDir 'Agent\Borealis\Settings\WireGuard'
                            }
                        } catch {}
                        if ($borealisConfigDir) {
                            try {
                                $confFull = [System.IO.Path]::GetFullPath($confPath)
                                $borealisFull = [System.IO.Path]::GetFullPath($borealisConfigDir)
                                if ($confFull.ToLowerInvariant().StartsWith($borealisFull.ToLowerInvariant()) -eq $false) {
                                    Write-AgentLog -FileName $LogName -Message ("$logPrefix Adapter config stored outside Borealis settings: {0}" -f $confPath)
                                }
                            } catch {}
                        }
                        break
                    }
                    Write-AgentLog -FileName $LogName -Message ("$logPrefix Adapter config not found after write: {0}" -f $candidate)
                } catch {
                    Write-AgentLog -FileName $LogName -Message ("$logPrefix Failed to write adapter config at {0}: {1}" -f $candidate, $_.Exception.Message)
                }
            }

            if (-not $confPath) {
                Write-AgentLog -FileName $LogName -Message "$logPrefix Failed to write adapter config to any candidate path."
                return $false
            }

            $svcPreInstall = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
            $svcConfig = Get-WireGuardTunnelServiceConfigPath -ServiceName $serviceName
            if (-not $svcPreInstall -and -not $svcConfig) {
                Write-AgentLog -FileName $LogName -Message ("$logPrefix Installing tunnel service {0} for adapter provisioning with config {1}." -f $serviceName, $confPath)
                $installArgs = "/installtunnelservice `"$confPath`""
                $proc = Start-Process -FilePath $WireGuardExe -ArgumentList $installArgs -PassThru -WindowStyle Hidden -ErrorAction SilentlyContinue
                if (-not $proc) {
                    Write-AgentLog -FileName $LogName -Message "$logPrefix /installtunnelservice failed to start."
                    return $false
                }
                $installTimeoutMs = 60000
                if (-not $proc.WaitForExit($installTimeoutMs)) {
                    try { $proc.Kill() } catch {}
                    Write-AgentLog -FileName $LogName -Message "$logPrefix /installtunnelservice timed out."
                    $svcAfterTimeout = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
                    if (-not $svcAfterTimeout) {
                        return $false
                    }
                    Write-AgentLog -FileName $LogName -Message "$logPrefix Tunnel service present after /installtunnelservice timeout."
                } else {
                    $installExit = $proc.ExitCode
                    Write-AgentLog -FileName $LogName -Message ("$logPrefix /installtunnelservice exit code: {0}" -f $installExit)
                    if ($installExit -ne 0) {
                        $svcAfterExit = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
                        if (-not $svcAfterExit) {
                            return $false
                        }
                        Write-AgentLog -FileName $LogName -Message "$logPrefix Tunnel service present despite non-zero /installtunnelservice exit code."
                    }
                }
            } else {
                Write-AgentLog -FileName $LogName -Message "$logPrefix Tunnel service already present; skipping /installtunnelservice."
            }
            try { Start-Service -Name $serviceName -ErrorAction SilentlyContinue } catch {}
        } catch {
            Write-AgentLog -FileName $LogName -Message ("$logPrefix Adapter provisioning failed: {0}" -f $_.Exception.Message)
            return $false
        }

        $finalAdapter = $null
        $internalAdapter = $null
        $attempts = 60
        for ($i = 0; $i -lt $attempts; $i++) {
            Start-Sleep -Milliseconds 500
            $finalAdapter = Get-WireGuardAdapterByName -AdapterName $friendlyName
            if ($finalAdapter) { return $true }
            $internalAdapter = Get-WireGuardAdapterByName -AdapterName $internalName
            if ($internalAdapter -and $friendlyName -and $friendlyName -ne $internalName) {
                Rename-WireGuardAdapterName -OldName $internalName -NewName $friendlyName -LogName $LogName | Out-Null
                $finalAdapter = Get-WireGuardAdapterByName -AdapterName $friendlyName
                if ($finalAdapter) { return $true }
            }
        }

        if ($internalAdapter -and -not $finalAdapter) {
            Write-AgentLog -FileName $LogName -Message ("$logPrefix Tunnel adapter present as {0}, but rename to {1} did not complete." -f $internalName, $friendlyName)
        }
        return $false
    }

    function Ensure-WireGuardDriver {
        param(
            [Parameter(Mandatory = $true)][string]$WireGuardExe,
            [Parameter()][string]$LogName = 'Install.log'
        )

        $logPrefix = '[WireGuard]'
        $state = Get-WireGuardInstallState
        if ($state.DriverPresent) {
            Write-AgentLog -FileName $LogName -Message "$logPrefix Driver already present."
            return
        }

        if (-not (Test-Path $WireGuardExe -PathType Leaf)) {
            $msg = "$logPrefix Cannot install driver: wireguard.exe not found at $WireGuardExe"
            Write-AgentLog -FileName $LogName -Message $msg
            throw $msg
        }

        # Try installing/refreshing the manager service (also stages the driver)
        try {
            Write-AgentLog -FileName $LogName -Message "$logPrefix Invoking wireguard.exe /installmanagerservice to seed driver."
            & $WireGuardExe /installmanagerservice | Out-Null
            $wgExit = $LASTEXITCODE
            Write-AgentLog -FileName $LogName -Message ("$logPrefix /installmanagerservice exit code: {0}" -f $wgExit)
        } catch {
            Write-AgentLog -FileName $LogName -Message ("$logPrefix /installmanagerservice failed: {0}" -f $_.Exception.Message)
        }

        $stateAfterManager = Get-WireGuardInstallState
        if ($stateAfterManager.DriverPresent) {
            Write-AgentLog -FileName $LogName -Message "$logPrefix Driver present after /installmanagerservice."
            return
        }
        $state = $stateAfterManager

        try {
            $adapterProvisioned = Ensure-WireGuardTunnelAdapter -WireGuardExe $WireGuardExe -LogName $LogName
            if (-not $adapterProvisioned) {
                Write-AgentLog -FileName $LogName -Message "$logPrefix Adapter provisioning did not complete."
            }
        } catch {
            Write-AgentLog -FileName $LogName -Message ("$logPrefix Adapter provisioning failed: {0}" -f $_.Exception.Message)
        }

        $post = Get-WireGuardInstallState
        $postDriver = $post.DriverPresent
        $postMsg = "[WireGuard] Driver presence after adapter provisioning: $postDriver (Exe: $WireGuardExe)"
        Write-AgentLog -FileName $LogName -Message $postMsg
        if ($postDriver) { return }

        $publishedInf = Find-WireGuardDriverInf -LogName $LogName
        if ($publishedInf) {
            $infPath = Join-Path $env:WINDIR "INF\$publishedInf"
            if (Test-Path $infPath -PathType Leaf) {
                try {
                    Write-AgentLog -FileName $LogName -Message ("$logPrefix Installing WireGuard driver via pnputil: {0}" -f $infPath)
                    pnputil.exe /add-driver "`"$infPath`"" /install | Out-Null
                } catch {
                    Write-AgentLog -FileName $LogName -Message ("$logPrefix pnputil driver install failed: {0}" -f $_.Exception.Message)
                }
            }
        }

        $postPnP = Get-WireGuardInstallState
        $postPnPDriver = $postPnP.DriverPresent
        Write-AgentLog -FileName $LogName -Message ("$logPrefix Driver presence after pnputil install: {0}" -f $postPnPDriver)
        if ($postPnPDriver) { return }

        throw "$logPrefix Driver still missing after adapter provisioning and pnputil fallback."
    }

    # UltraVNC Server (always-on VNC backend for Apache Guacamole)
    Run-Step "Dependency: UltraVNC Server" {
        $uvncZipUrl = $env:BOREALIS_ULTRAVNC_ZIP_URL
        if (-not $uvncZipUrl) {
            $uvncZipUrl = "https://uvnc.eu/download/1640/UltraVNC_1640.zip"
        }
        $uvncZipSha256 = $env:BOREALIS_ULTRAVNC_ZIP_SHA256
        if (-not $uvncZipSha256) {
            $uvncZipSha256 = "910ab4df4441c4f415c59007501e23ea2db331aa5dfa6ecbd5e583f34362d975"
        }
        $uvncMsiUrl = $env:BOREALIS_ULTRAVNC_MSI_URL
        if (-not $uvncMsiUrl) {
            $uvncMsiUrl = "https://uvnc.eu/download/1640/UltraVNC_1640_x64_Setup.msi"
        }
        $uvncMsiSha256 = $env:BOREALIS_ULTRAVNC_MSI_SHA256
        if (-not $uvncMsiSha256) {
            $uvncMsiSha256 = "3a052b8b73dfc0b740cbeac95e550bba5c4e2cd3083693f4b40cb6a4c8d1974b"
        }
        $uvncInstallerUrl = $env:BOREALIS_ULTRAVNC_URL
        if (-not $uvncInstallerUrl) {
            $uvncInstallerUrl = "https://uvnc.eu/download/1640/UltraVNC_1640_x64_Setup.exe"
        }
        $uvncInstallerSha256 = $env:BOREALIS_ULTRAVNC_SETUP_SHA256
        if (-not $uvncInstallerSha256) {
            $uvncInstallerSha256 = "434853e116eeb132cfdf47fdf6ba489d30c67a38147aff6b9bd0ec2f4d0f1919"
        }
        $uvncX64WinvncSha256 = $env:BOREALIS_ULTRAVNC_X64_WINVNC_SHA256
        if (-not $uvncX64WinvncSha256) {
            $uvncX64WinvncSha256 = "f24c4fbe8f0a85995e46d0202cd12f6eef61a2250da94fa8ddb26115929a0cb9"
        }
        $uvncX86WinvncSha256 = $env:BOREALIS_ULTRAVNC_X86_WINVNC_SHA256
        if (-not $uvncX86WinvncSha256) {
            $uvncX86WinvncSha256 = "60151cac9101d97bc1d6bbe878238ce4fce8835ba773b4d129fe7f61bea8e2b2"
        }
        $passwordToolZipSha256 = $env:BOREALIS_VNC_PASSWORD_TOOL_SHA256
        if (-not $passwordToolZipSha256) {
            $passwordToolZipSha256 = "19cde023e7b97171a9b30f7954dd3b1d9eda07cb60d604526d6588abbb7a8410"
        }
        $passwordToolExeSha256 = $env:BOREALIS_VNC_PASSWORD_TOOL_EXE_SHA256
        if (-not $passwordToolExeSha256) {
            $passwordToolExeSha256 = "c3369fd7b1be499a3e7b3a8a6922f745c6ef723add6cc67c751c57f8e17ae4bc"
        }
        $uvncPublisherPatterns = @('UltraVNC', 'uvnc')
        $uvncRoot = Join-Path $depsRoot "UltraVNC_Server"
        $uvncPayloadRoot = Join-Path $uvncRoot "payload"
        $uvncZipPath = Join-Path $uvncRoot "UltraVNC_1640.zip"
        $uvncMsiPath = Join-Path $uvncRoot "UltraVNC_1640_x64_Setup.msi"
        $uvncInstallerPath = Join-Path $uvncRoot "UltraVNC_1640_x64_Setup.exe"

        if (-not (Test-Path $uvncRoot)) {
            New-Item -ItemType Directory -Path $uvncRoot -Force | Out-Null
        }

        $uvncExe = Get-ChildItem -Path $uvncRoot -Recurse -Filter "winvnc*.exe" -ErrorAction SilentlyContinue |
            Select-Object -First 1

        if (-not $uvncExe) {
            try {
                Invoke-BorealisVerifiedDownload `
                    -Url $uvncZipUrl `
                    -DestinationPath $uvncZipPath `
                    -ExpectedSha256 $uvncZipSha256 `
                    -LogName 'Install.log' `
                    -LogPrefix '[UltraVNC]'
                Expand-ZipArchiveWithFallback -ArchivePath $uvncZipPath -DestinationPath $uvncPayloadRoot -SevenZipPath $sevenZipExe -ClearDestination
                $uvncX64Exe = Join-Path $uvncPayloadRoot 'x64\winvnc.exe'
                if (Test-Path $uvncX64Exe -PathType Leaf) {
                    Assert-BorealisFileSha256 -Path $uvncX64Exe -ExpectedSha256 $uvncX64WinvncSha256 -LogName 'Install.log' -LogPrefix '[UltraVNC]'
                }
                $uvncX86Exe = Join-Path $uvncPayloadRoot 'x86\winvnc.exe'
                if (Test-Path $uvncX86Exe -PathType Leaf) {
                    Assert-BorealisFileSha256 -Path $uvncX86Exe -ExpectedSha256 $uvncX86WinvncSha256 -LogName 'Install.log' -LogPrefix '[UltraVNC]'
                }
            } catch {
                Write-Host "UltraVNC zip download/extract failed. Trying MSI fallback." -ForegroundColor Yellow
            }
            $uvncExe = Get-ChildItem -Path $uvncPayloadRoot -Recurse -Filter "winvnc*.exe" -ErrorAction SilentlyContinue |
                Select-Object -First 1
        }

        if (-not $uvncExe) {
            try {
                Invoke-BorealisVerifiedDownload `
                    -Url $uvncMsiUrl `
                    -DestinationPath $uvncMsiPath `
                    -ExpectedSha256 $uvncMsiSha256 `
                    -RequireAuthenticode `
                    -PublisherPatterns $uvncPublisherPatterns `
                    -LogName 'Install.log' `
                    -LogPrefix '[UltraVNC]'
                $msiExtractRoot = Join-Path $uvncPayloadRoot "msi_extract"
                if (Test-Path $msiExtractRoot) {
                    Remove-Item $msiExtractRoot -Recurse -Force -ErrorAction SilentlyContinue
                }
                New-Item -ItemType Directory -Path $msiExtractRoot -Force | Out-Null
                $msiArgs = @(
                    "/a",
                    "`"$uvncMsiPath`"",
                    "/qn",
                    "TARGETDIR=`"$msiExtractRoot`""
                )
                $msiProc = Start-Process -FilePath "msiexec.exe" -ArgumentList $msiArgs -Wait -NoNewWindow -PassThru
                if ($msiProc.ExitCode -ne 0) {
                    Write-Host "UltraVNC MSI extraction failed with code $($msiProc.ExitCode). Trying installer fallback." -ForegroundColor Yellow
                }
            } catch {
                Write-Host "UltraVNC MSI extraction failed. Trying installer fallback." -ForegroundColor Yellow
            }
            $uvncExe = Get-ChildItem -Path $uvncPayloadRoot -Recurse -Filter "winvnc*.exe" -ErrorAction SilentlyContinue |
                Select-Object -First 1
        }

        if (-not $uvncExe) {
            Invoke-BorealisVerifiedDownload `
                -Url $uvncInstallerUrl `
                -DestinationPath $uvncInstallerPath `
                -ExpectedSha256 $uvncInstallerSha256 `
                -RequireAuthenticode `
                -PublisherPatterns $uvncPublisherPatterns `
                -LogName 'Install.log' `
                -LogPrefix '[UltraVNC]'
            if (Test-Path $sevenZipExe -PathType Leaf) {
                try {
                    if (-not (Test-Path $uvncPayloadRoot)) {
                        New-Item -ItemType Directory -Path $uvncPayloadRoot -Force | Out-Null
                    }
                    & $sevenZipExe x $uvncInstallerPath "-o$uvncPayloadRoot" -y | Out-Null
                    if ($LASTEXITCODE -ne 0) {
                        throw "7-Zip extraction returned exit code $LASTEXITCODE."
                    }
                } catch {
                    Write-Host "UltraVNC installer extraction failed." -ForegroundColor Yellow
                }
            } else {
                Write-Host "7-Zip CLI not found. Skipping UltraVNC installer extraction fallback." -ForegroundColor Yellow
            }
            $uvncExe = Get-ChildItem -Path $uvncPayloadRoot -Recurse -Filter "winvnc*.exe" -ErrorAction SilentlyContinue |
                Select-Object -First 1
        }

        $passwordTool = Get-ChildItem -Path $uvncRoot -Recurse -Filter "createpassword.exe" -ErrorAction SilentlyContinue |
            Select-Object -First 1
        if (-not $passwordTool) {
            $passwordToolUrl = $env:BOREALIS_VNC_PASSWORD_TOOL_URL
            if (-not $passwordToolUrl) {
                $passwordToolUrl = "https://uvnc.eu/download/133/createpassword.zip"
            }
            $passwordToolZip = Join-Path $uvncRoot "createpassword.zip"
            try {
                Invoke-BorealisVerifiedDownload `
                    -Url $passwordToolUrl `
                    -DestinationPath $passwordToolZip `
                    -ExpectedSha256 $passwordToolZipSha256 `
                    -LogName 'Install.log' `
                    -LogPrefix '[UltraVNC]'
                $toolDir = Join-Path $uvncRoot "tools"
                Expand-ZipArchiveWithFallback -ArchivePath $passwordToolZip -DestinationPath $toolDir -SevenZipPath $sevenZipExe -ClearDestination
                $passwordToolExe = Join-Path $toolDir 'createpassword.exe'
                if (Test-Path $passwordToolExe -PathType Leaf) {
                    Assert-BorealisFileSha256 -Path $passwordToolExe -ExpectedSha256 $passwordToolExeSha256 -LogName 'Install.log' -LogPrefix '[UltraVNC]'
                }
            } catch {
                Write-Host "UltraVNC createpassword tool download failed. Set BOREALIS_VNC_PASSWORD_TOOL_URL to override." -ForegroundColor Yellow
            }
        }

        if (-not $uvncExe) {
            Write-Host "UltraVNC server binary not found. Ensure winvnc.exe exists under Dependencies\\UltraVNC_Server." -ForegroundColor Yellow
        } else {
            $uvncServiceName = $env:BOREALIS_ULTRAVNC_SERVICE
            if (-not $uvncServiceName) {
                $uvncServiceName = "uvnc_service"
            }
            try {
                $uvncService = Get-Service -Name $uvncServiceName -ErrorAction SilentlyContinue
                if (-not $uvncService) {
                    $uvncService = Get-Service -ErrorAction SilentlyContinue | Where-Object {
                        $_.Name -like "*uvnc*" -or $_.DisplayName -like "*UltraVNC*"
                    } | Select-Object -First 1
                }
                if ($uvncService) {
                    sc.exe config $uvncService.Name start= auto | Out-Null
                    if ($uvncService.Status -ne "Stopped") {
                        sc.exe stop $uvncService.Name | Out-Null
                    }
                } else {
                    Write-Host "UltraVNC service will be created and kept running by the agent." -ForegroundColor Yellow
                }
            } catch {
                Write-Host "UltraVNC service setup failed: $($_.Exception.Message)" -ForegroundColor Yellow
            }
        }
    }

    Run-Step "Dependency: AutoHotKey" {
        $ahkVersion    = "2.0.19"
        $ahkVersionTag = "v$ahkVersion"
        $ahkZipName    = "AutoHotkey_$ahkVersion.zip"
        $ahkZipUrl     = "https://github.com/AutoHotkey/AutoHotkey/releases/download/$ahkVersionTag/$ahkZipName"
        $ahkZipPath    = Join-Path $depsRoot $ahkZipName
        $ahkInstallDir = Join-Path $depsRoot "AutoHotKey"
        $ahkExePath    = Join-Path $ahkInstallDir "AutoHotkey64.exe"

        if (-not (Test-Path $ahkExePath)) {
            if (-not (Test-Path $ahkZipPath)) {
                Invoke-BorealisDownload -Url $ahkZipUrl -DestinationPath $ahkZipPath -LogName 'Install.log' -LogPrefix '[AutoHotKey]'
            }

            Expand-ZipArchiveWithFallback -ArchivePath $ahkZipPath -DestinationPath $ahkInstallDir -SevenZipPath $sevenZipExe -ClearDestination

            Remove-Item $ahkZipPath -Force -ErrorAction SilentlyContinue

            if (-not (Test-Path $ahkExePath)) {
                throw "AutoHotKey executable not found after extraction."
            }
        }
    }

    # Portable Git client for agent updates
    Run-Step "Dependency: Git CLI" {
        if (-not (Test-Path $gitExePath)) {
            if (-not (Test-Path $gitZipPath)) {
                Invoke-BorealisDownload -Url $gitZipUrl -DestinationPath $gitZipPath -LogName 'Install.log' -LogPrefix '[GitCLI]'
            }

            Expand-ZipArchiveWithFallback -ArchivePath $gitZipPath -DestinationPath $gitInstallDir -SevenZipPath $sevenZipExe -ClearDestination

            Remove-Item $gitZipPath -Force -ErrorAction SilentlyContinue

            if (-not (Test-Path $gitExePath)) {
                throw "Git executable not found after extraction."
            }
        }
    }

    Run-Step "Dependency: WireGuard VPN Adapter" {
        $logName = 'Install.log'
        $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
        $archKey = $null
        try { $archKey = $arch.ToString().ToUpperInvariant() } catch { $archKey = 'X64' }
        $msiName = Get-WireGuardMsiName -ArchitectureTag $archKey
        $msiPath = $null
        if ($msiName) {
            $msiPath = Join-Path $wireGuardInstallerDir $msiName
            Ensure-WireGuardInstallerFile -Url ("$wireGuardDownloadRoot$msiName") -DestinationPath $msiPath -LogName $logName
        } else {
            Write-AgentLog -FileName $logName -Message ("[WireGuard] Unable to resolve MSI name for architecture '{0}'. Defaulting to cached bootstrapper only." -f $archKey)
        }

        Ensure-WireGuardInstallerFile -Url ("$wireGuardDownloadRoot$wireGuardBootstrapperName") -DestinationPath $wireGuardBootstrapperPath -LogName $logName

        $state = Get-WireGuardInstallState
        $stateVersion = if ($state.Version) { $state.Version } else { 'unknown' }
        $stateExe = if ($state.ExePath) { $state.ExePath } else { 'n/a' }
        $stateSummary = "[WireGuard] Detected install state: Installed={0}; Version={1}; Service={2}; Driver={3}; Adapter={4}; BorealisAdapter={5}; Exe={6}" -f `
            $state.Installed, $stateVersion, $state.ServicePresent, $state.DriverPresent, $state.AdapterPresent, $state.DedicatedAdapterPresent, $stateExe
        Write-AgentLog -FileName $logName -Message $stateSummary
        if ($state.DriverPaths -and $state.DriverPaths.Count -gt 0) {
            Write-AgentLog -FileName $logName -Message ("[WireGuard] Driver paths: {0}" -f ($state.DriverPaths -join '; '))
        }
        if ($state.AdapterNames -and $state.AdapterNames.Count -gt 0) {
            Write-AgentLog -FileName $logName -Message ("[WireGuard] Adapter names: {0}" -f ($state.AdapterNames -join '; '))
        }
        if ($state.DedicatedAdapterNames -and $state.DedicatedAdapterNames.Count -gt 0) {
            Write-AgentLog -FileName $logName -Message ("[WireGuard] Borealis adapter names: {0}" -f ($state.DedicatedAdapterNames -join '; '))
        }

        if (-not ($state.Installed -and $state.DriverPresent -and $state.ServicePresent)) {
            $installerCandidate = $null
            if ($msiPath -and (Test-Path $msiPath -PathType Leaf)) {
                $installerCandidate = $msiPath
            } elseif (Test-Path $wireGuardBootstrapperPath -PathType Leaf) {
                $installerCandidate = $wireGuardBootstrapperPath
            }

            if (-not $installerCandidate) {
                throw "WireGuard installer cache missing; expected $msiPath or $wireGuardBootstrapperPath"
            }

            if ($installerCandidate.ToLowerInvariant().EndsWith('.msi')) {
                Install-WireGuardMsi -InstallerPath $installerCandidate -BootstrapperPath $wireGuardBootstrapperPath -LogName $logName
            } else {
                $logPrefix = '[WireGuard]'
                Write-AgentLog -FileName $logName -Message ("$logPrefix Installing via bootstrapper at {0}" -f $installerCandidate)
                $bootstrapArgs = '/install /quiet'
                try {
                    $proc = Start-Process -FilePath $installerCandidate -ArgumentList $bootstrapArgs -Wait -PassThru -WindowStyle Hidden -ErrorAction Stop
                    Write-AgentLog -FileName $logName -Message ("$logPrefix Bootstrapper exit code: {0}" -f $proc.ExitCode)
                    if ($proc.ExitCode -ne 0) {
                        throw "WireGuard bootstrapper returned exit code $($proc.ExitCode)"
                    }
                } catch {
                    $err = $_.Exception.Message
                    Write-AgentLog -FileName $logName -Message ("$logPrefix Bootstrapper install failed: {0}" -f $err)
                    throw
                }
            }

            $state = Get-WireGuardInstallState
            $postVersion = if ($state.Version) { $state.Version } else { 'unknown' }
            $postExe = if ($state.ExePath) { $state.ExePath } else { 'n/a' }
            $postSummary = "[WireGuard] Post-install state: Installed={0}; Version={1}; Service={2}; Driver={3}; Adapter={4}; BorealisAdapter={5}; Exe={6}" -f `
                $state.Installed, $postVersion, $state.ServicePresent, $state.DriverPresent, $state.AdapterPresent, $state.DedicatedAdapterPresent, $postExe
            Write-AgentLog -FileName $logName -Message $postSummary
            if ($state.Installed -and $state.DriverPresent) {
                Write-Host "WireGuard installed and verified (version: $($state.Version))." -ForegroundColor Green
            } else {
                Write-Host "WireGuard installed (driver pending bootstrap)." -ForegroundColor Yellow
            }
        } else {
            if ($state.DedicatedAdapterPresent) {
                Write-Host "WireGuard already installed (version: $($state.Version))." -ForegroundColor Green
            } else {
                Write-Host "WireGuard installed (version: $($state.Version)); provisioning Borealis adapter." -ForegroundColor Yellow
            }
        }

        # Ensure WireGuard driver and Borealis tunnel adapter are provisioned
        $wgExe = $state.ExePath
        if (-not $wgExe -or -not (Test-Path $wgExe -PathType Leaf)) {
            # try default install path
            $wgExe = Join-Path $env:ProgramFiles 'WireGuard\wireguard.exe'
        }
        if ($wgExe -and (Test-Path $wgExe -PathType Leaf)) {
            try {
                Ensure-WireGuardDriver -WireGuardExe $wgExe -LogName $logName
                $adapterOk = Ensure-WireGuardTunnelAdapter -WireGuardExe $wgExe -LogName $logName
                $finalState = Get-WireGuardInstallState
                if (-not ($finalState.Installed -and $finalState.DriverPresent)) {
                    throw "WireGuard driver still missing after provisioning attempts."
                }
                if (-not $finalState.DedicatedAdapterPresent) {
                    throw "Borealis tunnel adapter still missing after provisioning attempts."
                }
                if (-not $adapterOk) {
                    Write-AgentLog -FileName $logName -Message "[WireGuard] Borealis tunnel adapter provisioning returned false."
                }
                Write-Host "WireGuard driver verified." -ForegroundColor Green
            } catch {
                Write-AgentLog -FileName $logName -Message ("[WireGuard] Driver bootstrap failed: {0}" -f $_.Exception.Message)
                throw
            }
        } else {
            $msg = "[WireGuard] Unable to locate wireguard.exe after installation."
            Write-AgentLog -FileName $logName -Message $msg
            throw $msg
        }
    }
}

function Ensure-AgentTasks {
    param([string]$ScriptRoot)
    $pyw         = Join-Path $ScriptRoot 'Agent\Scripts\pythonw.exe'
    $py          = Join-Path $ScriptRoot 'Agent\Scripts\python.exe'
    $agentPy     = Join-Path $ScriptRoot 'Agent\Borealis\agent.py'
    $svcWrapper  = Join-Path $ScriptRoot 'Agent\Borealis\launch_service.ps1'
    $updateScript= Join-Path $ScriptRoot 'Update.ps1'
    if (-not (Test-Path $pyw))      { Write-Host "pythonw.exe not found under Agent\Scripts" -ForegroundColor Yellow; return }
    if (-not (Test-Path $py))       { Write-Host "python.exe not found under Agent\Scripts" -ForegroundColor Yellow; return }
    if (-not (Test-Path $agentPy))  { Write-Host "Agent script not found under Agent\Borealis" -ForegroundColor Yellow; return }
    if (-not (Test-Path $svcWrapper)) { Write-Host "launch_service.ps1 not found under Agent\Borealis" -ForegroundColor Yellow; return }
    if (-not (Test-Path $updateScript)) { Write-Host "Update.ps1 not found under project root" -ForegroundColor Yellow; return }

    $taskSettings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
    $principalSystem = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest

    # SYSTEM startup task
    $sysArg     = ('-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "{0}"' -f $svcWrapper)
    $sysAction  = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $sysArg -WorkingDirectory (Split-Path $svcWrapper -Parent)
    $sysTrigger = New-ScheduledTaskTrigger -AtStartup
    Register-ScheduledTask -TaskName 'Borealis Agent' -Action $sysAction -Trigger $sysTrigger -Settings $taskSettings -Principal $principalSystem -Force | Out-Null
    try { Start-ScheduledTask -TaskName 'Borealis Agent' | Out-Null } catch {}
    try { Unregister-ScheduledTask -TaskName 'Borealis Agent (UserHelper)' -Confirm:$false -ErrorAction SilentlyContinue | Out-Null } catch {}
    try { schtasks.exe /Delete /TN 'Borealis Agent (UserHelper)' /F 2>$null | Out-Null } catch {}

    $autoUpdaterName = 'Borealis Agent (AutoUpdater)'
    $hourlyTrigger = New-ScheduledTaskTrigger -Once -At (Get-Date) -RepetitionInterval (New-TimeSpan -Hours 1) -RandomDelay (New-TimeSpan -Minutes 15)
    $updateArg = ('-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "{0}"' -f $updateScript)
    $updateAction = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $updateArg -WorkingDirectory $ScriptRoot
    Register-ScheduledTask -TaskName $autoUpdaterName -Action $updateAction -Trigger $hourlyTrigger -Settings $taskSettings -Principal $principalSystem -Force | Out-Null
}
function InstallOrUpdate-BorealisAgent {
    $isRefreshRuntime = $RefreshAgentRuntime.IsPresent
    Write-Host "Ensuring Agent Dependencies Exist..." -ForegroundColor DarkCyan
    Install_Shared_Dependencies
    Install_Agent_Dependencies
    if (-not (Test-Path $pythonExe)) {
        Write-Host "`r$($symbols.Fail) Bundled Python not found at '$pythonExe'." -ForegroundColor Red
        exit 1
    }
    $env:PATH = '{0};{1}' -f (Split-Path $pythonExe), $env:PATH
    Write-Host "Cleaning previous agent tasks/processes..." -ForegroundColor Yellow
    if ($isRefreshRuntime) {
        try { Stop-ScheduledTask -TaskName 'Borealis Agent' -ErrorAction SilentlyContinue } catch {}
        try { Stop-ScheduledTask -TaskName 'Borealis Agent (UserHelper)' -ErrorAction SilentlyContinue } catch {}
        try {
            Get-Process python,pythonw -ErrorAction SilentlyContinue |
                Where-Object { $_.Path -like (Join-Path $scriptDir 'Agent\*') } |
                ForEach-Object { try { $_ | Stop-Process -Force } catch {} }
        } catch {}
    } else {
        Remove-BorealisServicesAndTasks -LogName 'Install.log'
    }
    Ensure-SystemUtf8CodePage -LogName 'Install.log'
    Ensure-SoftwareSASGeneration -LogName 'Install.log'
    if ($isRefreshRuntime) {
        Write-Host "Refreshing Borealis Agent runtime..." -ForegroundColor Blue
    } else {
        Write-Host "Deploying Borealis Agent..." -ForegroundColor Blue
    }

    # Resolve all paths relative to the script directory to avoid CWD issues
    $venvFolderPath         = Join-Path $scriptDir 'Agent'
    $agentSourceRoot        = Join-Path $scriptDir 'Data\Agent'
    $agentSourcePath        = Join-Path $agentSourceRoot 'agent.py'
    $agentRequirements      = Join-Path $agentSourceRoot 'agent-requirements.txt'
    $agentDestinationFolder = Join-Path $venvFolderPath 'Borealis'
    $agentDestinationFile   = Join-Path $agentDestinationFolder 'agent.py'
    $venvPython             = Join-Path $venvFolderPath 'Scripts\python.exe'
    $existingServerUrl      = $null

    if ($NewEngine) {
        Run-Step "Clear Persisted Borealis Agent Enrollment State" {
            Clear-AgentEnrollmentState -LogName 'Install.log'
        }
    }

    Run-Step "Create Virtual Python Environment" {
        $venvActivate = Join-Path $venvFolderPath 'Scripts\Activate'
        $pyvenvCfg    = Join-Path $venvFolderPath 'pyvenv.cfg'
        $pythonForVenv = $pythonExe
        if (-not (Test-Path $pythonForVenv)) {
            $pyCmd = Get-Command py -ErrorAction SilentlyContinue
            $pythonCmd = Get-Command python -ErrorAction SilentlyContinue
            if ($pyCmd) { $pythonForVenv = $pyCmd.Source }
            elseif ($pythonCmd) { $pythonForVenv = $pythonCmd.Source }
            else {
                Write-Host "Python bootstrap not found. Ensure Dependencies\\Python is present or install Python 3." -ForegroundColor Red
                exit 1
            }
        }

        $expectedPython     = $pythonForVenv
        $expectedPythonNorm = $null
        $expectedHomeNorm   = $null
        try {
            if (Test-Path $expectedPython -PathType Leaf) {
                $expectedPython = (Resolve-Path $expectedPython -ErrorAction Stop).ProviderPath
            }
        } catch { $expectedPython = $pythonForVenv }
        if ($expectedPython) {
            $expectedPythonNorm = $expectedPython.ToLowerInvariant()
            try {
                $expectedHome = Split-Path -Path $expectedPython -Parent
            } catch { $expectedHome = $null }
            if ($expectedHome) { $expectedHomeNorm = $expectedHome.ToLowerInvariant() }
        }

        $venvNeedsUpgrade = $false
        if (Test-Path $pyvenvCfg -PathType Leaf) {
            try {
                $cfgLines = Get-Content -Path $pyvenvCfg -ErrorAction Stop
                $cfgMap = @{}
                foreach ($line in $cfgLines) {
                    $trimmed = $line.Trim()
                    if (-not $trimmed -or $trimmed.StartsWith('#')) { continue }
                    $parts = $trimmed -split '=', 2
                    if ($parts.Count -ne 2) { continue }
                    $cfgMap[$parts[0].Trim().ToLowerInvariant()] = $parts[1].Trim()
                }

                $cfgExecutable = $cfgMap['executable']
                $cfgHome = $cfgMap['home']

                if ($cfgExecutable -and -not (Test-Path $cfgExecutable -PathType Leaf)) {
                    $venvNeedsUpgrade = $true
                } elseif ($cfgHome -and -not (Test-Path $cfgHome -PathType Container)) {
                    $venvNeedsUpgrade = $true
                } else {
                    if ($cfgExecutable -and $expectedPythonNorm) {
                        try { $resolvedExe = (Resolve-Path $cfgExecutable -ErrorAction Stop).ProviderPath } catch { $resolvedExe = $cfgExecutable }
                        $resolvedExeNorm = if ($resolvedExe) { $resolvedExe.ToLowerInvariant() } else { $null }
                        if ($resolvedExeNorm -and $resolvedExeNorm -ne $expectedPythonNorm) { $venvNeedsUpgrade = $true }
                    }
                    if (-not $venvNeedsUpgrade -and $cfgHome -and $expectedHomeNorm) {
                        try { $resolvedHome = (Resolve-Path $cfgHome -ErrorAction Stop).ProviderPath } catch { $resolvedHome = $cfgHome }
                        $resolvedHomeNorm = if ($resolvedHome) { $resolvedHome.ToLowerInvariant() } else { $null }
                        if ($resolvedHomeNorm -and $resolvedHomeNorm -ne $expectedHomeNorm) { $venvNeedsUpgrade = $true }
                    }
                }
            } catch { $venvNeedsUpgrade = $true }
        }

        if (-not (Test-Path $venvActivate)) {
            & $pythonForVenv -m venv $venvFolderPath
        } elseif ($venvNeedsUpgrade) {
            Write-Host "Detected relocated Agent virtual environment. Rebuilding interpreter bindings..." -ForegroundColor Yellow
            & $pythonForVenv -m venv --upgrade $venvFolderPath
        }
        if (Test-Path $agentSourcePath) {
            # Preserve enrolled runtime state and replace only the staged code payload.
            $existingServerUrlPath = Join-Path $agentDestinationFolder 'Settings\server_url.txt'
            if (Test-Path $existingServerUrlPath) {
                try {
                    $candidateUrl = (Get-Content -Path $existingServerUrlPath -ErrorAction SilentlyContinue | Select-Object -First 1)
                } catch {
                    $candidateUrl = $null
                }
                if ($candidateUrl) {
                    $candidateUrl = $candidateUrl.Trim()
                }
                if ($candidateUrl) {
                    $existingServerUrl = $candidateUrl
                }
            }
            New-Item -Path $agentDestinationFolder -ItemType Directory -Force | Out-Null

            # Copy Agent Files to Virtual Python Environment
            $coreAgentFiles = @(
                (Join-Path $agentSourceRoot 'Python_API_Endpoints'),
                (Join-Path $agentSourceRoot 'Roles'),
                (Join-Path $agentSourceRoot 'Scripts'),
                (Join-Path $agentSourceRoot 'agent_deployment.py'),
                (Join-Path $agentSourceRoot 'agent.py'),
                (Join-Path $agentSourceRoot 'Borealis.ico'),
                (Join-Path $agentSourceRoot 'desktop_environment.py'),
                (Join-Path $agentSourceRoot 'fcntl_stub.py'),
                (Join-Path $agentSourceRoot 'launch_service.ps1'),
                (Join-Path $agentSourceRoot 'qt_compat.py'),
                (Join-Path $agentSourceRoot 'role_health.py'),
                (Join-Path $agentSourceRoot 'role_manager.py'),
                (Join-Path $agentSourceRoot 'runtime_paths.py'),
                (Join-Path $agentSourceRoot 'restart_agent_tasks.ps1'),
                (Join-Path $agentSourceRoot 'security.py'),
                (Join-Path $agentSourceRoot 'session_runtime.py'),
                (Join-Path $agentSourceRoot 'signature_utils.py'),
                (Join-Path $agentSourceRoot 'sitecustomize.py'),
                (Join-Path $agentSourceRoot 'termios_stub.py'),
                (Join-Path $agentSourceRoot 'tray_state.py'),
                (Join-Path $agentSourceRoot 'update_helper.py'),
                (Join-Path $agentSourceRoot 'update_state.py')
            )

            foreach ($coreItem in $coreAgentFiles) {
                if (-not $coreItem) { continue }
                $targetItem = Join-Path $agentDestinationFolder (Split-Path -Path $coreItem -Leaf)
                Remove-Item $targetItem -Recurse -Force -ErrorAction SilentlyContinue
            }
            Copy-Item $coreAgentFiles -Destination $agentDestinationFolder -Recurse -Force

            # Stage UltraVNC payload + password tool into agent runtime so we don't write under Dependencies at runtime.
            $uvncServiceName = $env:BOREALIS_ULTRAVNC_SERVICE
            if (-not $uvncServiceName) { $uvncServiceName = "uvnc_service" }
            $uvncWasRunning = $false
            try {
                $svc = Get-Service -Name $uvncServiceName -ErrorAction SilentlyContinue
                if ($svc -and $svc.Status -eq "Running") {
                    $uvncWasRunning = $true
                }
            } catch {
                $uvncWasRunning = $false
            }

            if ($uvncWasRunning) {
                try { & sc.exe stop $uvncServiceName | Out-Null } catch {}
                $stopDeadline = (Get-Date).AddSeconds(10)
                while ((Get-Date) -lt $stopDeadline) {
                    $svc = Get-Service -Name $uvncServiceName -ErrorAction SilentlyContinue
                    if (-not $svc -or $svc.Status -eq "Stopped") { break }
                    Start-Sleep -Milliseconds 500
                }
                $svc = Get-Service -Name $uvncServiceName -ErrorAction SilentlyContinue
                if ($svc -and $svc.Status -ne "Stopped") {
                    Write-AgentLog -FileName 'Install.log' -Message "[VNC] Forcing winvnc shutdown for staging."
                    try { & taskkill.exe /IM winvnc.exe /F | Out-Null } catch {}
                    Start-Sleep -Seconds 1
                }
            }
            try {
                $svc = Get-Service -Name $uvncServiceName -ErrorAction SilentlyContinue
                if ($svc) {
                    & sc.exe config $uvncServiceName start= auto | Out-Null
                }
            } catch {
                # ignore start-mode updates here
            }

            try {
                $uvncPayloadRoot = Join-Path $scriptDir 'Dependencies\UltraVNC_Server\payload\x64'
                if (Test-Path $uvncPayloadRoot) {
                    $uvncServerDestDir = Join-Path $agentDestinationFolder 'Tools\UltraVNC\Server'
                    if (-not (Test-Path $uvncServerDestDir)) {
                        New-Item -Path $uvncServerDestDir -ItemType Directory -Force | Out-Null
                    }
                    Copy-Item -Path (Join-Path $uvncPayloadRoot '*') -Destination $uvncServerDestDir -Recurse -Force
                    Write-AgentLog -FileName 'Install.log' -Message "[VNC] Staged UltraVNC server payload to Agent\\Borealis\\Tools\\UltraVNC\\Server."
                } else {
                    Write-AgentLog -FileName 'Install.log' -Message "[VNC] UltraVNC payload not found under Dependencies\\UltraVNC_Server\\payload\\x64."
                }
            } catch {
                Write-AgentLog -FileName 'Install.log' -Message ("[VNC] Failed to stage UltraVNC server payload: {0}" -f $_.Exception.Message)
            }

            try {
                $uvncToolSource = Get-ChildItem -Path (Join-Path $scriptDir 'Dependencies\UltraVNC_Server') `
                    -Recurse -Filter "createpassword.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
                if ($uvncToolSource) {
                    $uvncToolDestDir = Join-Path $agentDestinationFolder 'Tools\UltraVNC'
                    if (-not (Test-Path $uvncToolDestDir)) {
                        New-Item -Path $uvncToolDestDir -ItemType Directory -Force | Out-Null
                    }
                    Copy-Item -Path $uvncToolSource.FullName -Destination (Join-Path $uvncToolDestDir 'createpassword.exe') -Force
                    Write-AgentLog -FileName 'Install.log' -Message "[VNC] Staged UltraVNC password tool to Agent\\Borealis\\Tools\\UltraVNC."
                } else {
                    Write-AgentLog -FileName 'Install.log' -Message "[VNC] createpassword.exe not found under Dependencies\\UltraVNC_Server."
                }
            } catch {
                Write-AgentLog -FileName 'Install.log' -Message ("[VNC] Failed to stage UltraVNC password tool: {0}" -f $_.Exception.Message)
            }

            # Do not restart UltraVNC during deployment; the agent keeps it running.
            
        }
        . (Join-Path $venvFolderPath 'Scripts\Activate')
    }

    Run-Step "Install Python Dependencies" {
        if (Test-Path $agentRequirements) {
            & $venvPython -m pip install --disable-pip-version-check -q -r $agentRequirements | Out-Null
        }

        $stubSource = Join-Path $agentSourceRoot 'fcntl_stub.py'
        if (Test-Path $stubSource) {
            $stubDest = Join-Path $venvFolderPath 'Lib\site-packages\fcntl.py'
            Write-AgentLog -FileName 'Install.log' -Message '[UTF8] Ensuring Windows fcntl shim is installed.'
            Copy-Item $stubSource $stubDest -Force
        }

        $termiosSource = Join-Path $agentSourceRoot 'termios_stub.py'
        if (Test-Path $termiosSource) {
            $termiosDest = Join-Path $venvFolderPath 'Lib\site-packages\termios.py'
            Write-AgentLog -FileName 'Install.log' -Message '[UTF8] Ensuring Windows termios shim is installed.'
            Copy-Item $termiosSource $termiosDest -Force
        }

        $siteCustomSource = Join-Path $agentSourceRoot 'sitecustomize.py'
        if (Test-Path $siteCustomSource) {
            $siteCustomDest = Join-Path $venvFolderPath 'Lib\site-packages\sitecustomize.py'
            Write-AgentLog -FileName 'Install.log' -Message '[UTF8] Ensuring sitecustomize shim is installed.'
            Copy-Item $siteCustomSource $siteCustomDest -Force
        }
    }

    Run-Step "Configure Agent Settings" {
        $settingsDir = Join-Path $scriptDir 'Agent\Borealis\Settings'
        $oldSettingsDir = Join-Path $scriptDir 'Agent\Settings'
        if (-not (Test-Path $settingsDir)) { New-Item -Path $settingsDir -ItemType Directory -Force | Out-Null }
        $traySettingsDir = Join-Path $settingsDir 'Tray'
        Ensure-AgentTrayFolderPermissions -TrayDir $traySettingsDir -LogName 'Install.log'
        $serverUrlPath = Join-Path $settingsDir 'server_url.txt'
        $configPath = Join-Path $settingsDir 'agent_settings.json'
        $defaultUrl = ''
        $currentUrl = ''
        if ($existingServerUrl -and $existingServerUrl.Trim()) {
            $currentUrl = $existingServerUrl.Trim()
        } elseif (Test-Path $serverUrlPath) {
            try { $txt = (Get-Content -Path $serverUrlPath -ErrorAction SilentlyContinue | Select-Object -First 1) } catch { $txt = '' }
            if ($txt -and $txt.Trim()) { $currentUrl = $txt.Trim() }
        }
        $providedServerUrl = ''
        if ($ServerUrl -and $ServerUrl.Trim()) {
            $providedServerUrl = $ServerUrl.Trim()
        } elseif ($env:BOREALIS_SERVER_URL -and $env:BOREALIS_SERVER_URL.Trim()) {
            $providedServerUrl = $env:BOREALIS_SERVER_URL.Trim()
        }
        if ($providedServerUrl) {
            $inputUrl = $providedServerUrl
        } elseif ($isRefreshRuntime) {
            $inputUrl = $currentUrl
        } else {
            Write-Host ""; Write-Host "Set Borealis Server URL" -ForegroundColor DarkYellow
            $prompt = if ($currentUrl) { "Server URL [$currentUrl]" } else { "Server URL" }
            $inputUrl = Read-Host $prompt
            if (-not $inputUrl) { $inputUrl = $currentUrl }
            $inputUrl = $inputUrl.Trim()
        }
        if (-not $inputUrl) {
            throw "Borealis agent runtime requires a configured public HTTPS FQDN in server_url.txt or -ServerUrl."
        }
        
        # Write UTF-8 without BOM to avoid BOM being read into the URL
        $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
        [System.IO.File]::WriteAllText($serverUrlPath, $inputUrl, $utf8NoBom)

        $configDefaults = [ordered]@{
            config_file_watcher_interval = 2
            agent_id = ''
            regions = @{}
            enrollment_code = ''
            installer_code = ''
        }
        $config = [ordered]@{}
        foreach ($entry in $configDefaults.GetEnumerator()) {
            $config[$entry.Key] = $entry.Value
        }
        if (Test-Path $configPath) {
            try {
                $existingRaw = Get-Content -Path $configPath -Raw -ErrorAction Stop
                if ($existingRaw -and $existingRaw.Trim()) {
                    $existingJson = $existingRaw | ConvertFrom-Json -ErrorAction Stop
                    foreach ($prop in $existingJson.PSObject.Properties) {
                        $config[$prop.Name] = $prop.Value
                    }
                }
            } catch {
                Write-AgentLog -FileName 'Install.log' -Message ("[CONFIG] Failed to parse agent_settings.json: {0}" -f $_.Exception.Message)
            }
        }

        if ('regions' -notin $config.Keys -or $null -eq $config['regions']) {
            $config['regions'] = @{}
        }

        $existingEnrollmentCode = ''
        if ('enrollment_code' -in $config.Keys -and $null -ne $config['enrollment_code']) {
            $existingEnrollmentCode = [string]$config['enrollment_code']
        } elseif ('installer_code' -in $config.Keys -and $null -ne $config['installer_code']) {
            $existingEnrollmentCode = [string]$config['installer_code']
        }

        $providedEnrollmentCode = ''
        if ($EnrollmentCode -and $EnrollmentCode.Trim()) {
            $providedEnrollmentCode = $EnrollmentCode.Trim()
        } elseif ($env:BOREALIS_ENROLLMENT_CODE -and $env:BOREALIS_ENROLLMENT_CODE.Trim()) {
            $providedEnrollmentCode = $env:BOREALIS_ENROLLMENT_CODE.Trim()
        }

        if (-not $providedEnrollmentCode) {
            $defaultDisplay = if ($existingEnrollmentCode) { $existingEnrollmentCode } else { '' }
            if ($isRefreshRuntime) {
                if ($defaultDisplay) {
                    $providedEnrollmentCode = $defaultDisplay
                } else {
                    $providedEnrollmentCode = ''
                }
            } else {
                Write-Host ""; Write-Host "Set an enrollment code for agent enrollment." -ForegroundColor DarkYellow
                $inputCode = Read-Host ("Enrollment Code [{0}] (e.g. A4E1-••••-••••-••••-••••-••••-••••-350A)" -f $defaultDisplay)
                if ($inputCode -and $inputCode.Trim()) {
                    $providedEnrollmentCode = $inputCode.Trim()
                } elseif ($defaultDisplay) {
                    $providedEnrollmentCode = $defaultDisplay
                } else {
                    $providedEnrollmentCode = ''
                }
            }
        }

        $config['enrollment_code'] = $providedEnrollmentCode
        # Retain legacy key to avoid breaking existing agent readers
        $config['installer_code'] = $providedEnrollmentCode
        if ($providedEnrollmentCode) {
            $env:BOREALIS_ENROLLMENT_CODE = $providedEnrollmentCode
            $env:BOREALIS_INSTALLER_CODE = $providedEnrollmentCode
        }

        try {
            $configJson = $config | ConvertTo-Json -Depth 10
            [System.IO.File]::WriteAllText($configPath, $configJson, $utf8NoBom)
            if ($providedEnrollmentCode) {
                Write-Host "Enrollment code saved to agent_settings.json." -ForegroundColor Green
            } else {
                Write-Host "Enrollment code cleared in agent_settings.json." -ForegroundColor Yellow
            }
        } catch {
            Write-AgentLog -FileName 'Install.log' -Message ("[CONFIG] Failed to persist agent_settings.json: {0}" -f $_.Exception.Message)
            Write-Host "Failed to update agent_settings.json. Check Agent/Logs/install.log for details." -ForegroundColor Red
        }

        $systemConfigPath = Join-Path $settingsDir 'agent_settings_SYSTEM.json'
        $systemConfig = [ordered]@{}
        foreach ($entry in $configDefaults.GetEnumerator()) {
            $systemConfig[$entry.Key] = $entry.Value
        }
        if (Test-Path $systemConfigPath) {
            try {
                $existingSystemRaw = Get-Content -Path $systemConfigPath -Raw -ErrorAction Stop
                if ($existingSystemRaw -and $existingSystemRaw.Trim()) {
                    $existingSystemJson = $existingSystemRaw | ConvertFrom-Json -ErrorAction Stop
                    foreach ($prop in $existingSystemJson.PSObject.Properties) {
                        $systemConfig[$prop.Name] = $prop.Value
                    }
                }
            } catch {
                Write-AgentLog -FileName 'Install.log' -Message ("[CONFIG] Failed to parse agent_settings_SYSTEM.json: {0}" -f $_.Exception.Message)
            }
        }
        if ('regions' -notin $systemConfig.Keys -or $null -eq $systemConfig['regions']) {
            $systemConfig['regions'] = @{}
        }
        $systemConfig['enrollment_code'] = $providedEnrollmentCode
        # Retain legacy key to avoid breaking existing agent readers
        $systemConfig['installer_code'] = $providedEnrollmentCode
        try {
            $systemConfigJson = $systemConfig | ConvertTo-Json -Depth 10
            [System.IO.File]::WriteAllText($systemConfigPath, $systemConfigJson, $utf8NoBom)
            Write-AgentLog -FileName 'Install.log' -Message '[CONFIG] Enrollment code mirrored to agent_settings_SYSTEM.json.'
        } catch {
            Write-AgentLog -FileName 'Install.log' -Message ("[CONFIG] Failed to persist agent_settings_SYSTEM.json: {0}" -f $_.Exception.Message)
            Write-Host "Failed to update agent_settings_SYSTEM.json. Check Agent/Logs/install.log for details." -ForegroundColor Red
        }
    }

    Write-Host "`nConfiguring Borealis Agent (tasks)..." -ForegroundColor Blue
    Write-Host "===================================================================================="
    Ensure-AgentTasks -ScriptRoot $scriptDir
    if ($script:Utf8CodePageChanged) {
        $msg = 'System code pages set to UTF-8. A reboot is required to finalize the change.'
        Write-AgentLog -FileName 'Install.log' -Message ("[UTF8] {0}" -f $msg)
        Write-Host "`n$msg" -ForegroundColor Yellow
    }
}

# ---------------------- Main -----------------------
$Host.UI.RawUI.BackgroundColor = 'Black'
Clear-Host
$host.UI.RawUI.WindowTitle = 'Borealis Agent'
Write-Host ''
if (-not (Test-IsAdmin)) {
    Write-Host 'Administrator permissions are required to deploy the Borealis Agent.' -ForegroundColor Red
    exit 1
}
Write-Host 'Escalated Permissions Granted > Agent is Eligible for Deployment.' -ForegroundColor Green
Write-Host 'Deploying Borealis Agent (fresh install/update path)...' -ForegroundColor Cyan
InstallOrUpdate-BorealisAgent
