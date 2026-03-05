# Borealis Windows bootstrapper:
# - Ensures portable Git is available
# - Uses Git to fetch/checkout Borealis into C:\Borealis (or --install-dir)
# - Executes Borealis.ps1, forwarding agent enrollment arguments

$ErrorActionPreference = 'Stop'

$defaultInstallDir = if ($env:BOREALIS_INSTALL_DIR) { $env:BOREALIS_INSTALL_DIR } else { 'C:\Borealis' }
$defaultRepoUrl = if ($env:BOREALIS_BOOTSTRAP_REPO_URL) { $env:BOREALIS_BOOTSTRAP_REPO_URL } else { 'https://github.com/bunny-lab-io/Borealis.git' }
$defaultRepoRef = if ($env:BOREALIS_BOOTSTRAP_REF) { $env:BOREALIS_BOOTSTRAP_REF } else { 'main' }
$defaultGitZipUrl = if ($env:BOREALIS_BOOTSTRAP_GIT_ZIP_URL) { $env:BOREALIS_BOOTSTRAP_GIT_ZIP_URL } else { 'https://github.com/git-for-windows/git/releases/download/v2.47.1.windows.1/MinGit-2.47.1-64-bit.zip' }
$defaultGitZipPath = if ($env:BOREALIS_BOOTSTRAP_GIT_ZIP_PATH) { $env:BOREALIS_BOOTSTRAP_GIT_ZIP_PATH } else { Join-Path $env:TEMP 'BorealisBootstrap_MinGit.zip' }
$defaultGitCacheDir = if ($env:BOREALIS_BOOTSTRAP_GIT_DIR) {
    $env:BOREALIS_BOOTSTRAP_GIT_DIR
} elseif (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
    Join-Path $env:LOCALAPPDATA 'Borealis\Bootstrap\git'
} else {
    Join-Path $env:TEMP 'BorealisBootstrapGit'
}

$installDir = $defaultInstallDir
$repoUrl = $defaultRepoUrl
$repoRef = $defaultRepoRef
$gitZipUrl = $defaultGitZipUrl
$gitZipPath = $defaultGitZipPath
$gitCacheDir = $defaultGitCacheDir
$forwardedServerUrl = $null
$forwardedEnrollmentCode = $null
$passthroughArgs = New-Object System.Collections.Generic.List[string]

function Show-Usage {
    @'
Usage: bootstrap.ps1 [bootstrap options] [Borealis.ps1 options]

Bootstrap options:
  --install-dir <path>   Install location (default: C:\Borealis)
  --repo-url <url>       Git repository URL (default: https://github.com/bunny-lab-io/Borealis.git)
  --ref <name>           Git ref/branch/tag/commit to deploy (default: main)
  --git-zip-url <url>    Portable Git ZIP URL
  --git-zip-path <path>  Portable Git ZIP cache path
  --git-dir <path>       Portable Git extraction directory
  -h, --help             Show this help

Any other arguments are forwarded to Borealis.ps1.
Common forwarded options:
  --agent
  --serverurl <url>
  --enrollmentcode <code>
'@ | Write-Host
}

function Read-OptionValue {
    param(
        [string]$Token,
        [int]$Index,
        [string[]]$SourceArgs,
        [string]$OptionName
    )

    if ($Token.Contains('=')) {
        $parts = $Token -split '=', 2
        return [pscustomobject]@{
            Value = [string]$parts[1]
            NextIndex = $Index
        }
    }

    if (($Index + 1) -ge $SourceArgs.Count) {
        throw "Missing value for $OptionName."
    }

    return [pscustomobject]@{
        Value = [string]$SourceArgs[$Index + 1]
        NextIndex = $Index + 1
    }
}

function Normalize-BorealisArgument {
    param(
        [string]$Token,
        [int]$Index,
        [string[]]$SourceArgs
    )

    $normalized = $Token.ToLowerInvariant()
    switch -Regex ($normalized) {
        '^(--agent|-agent)$' {
            return [pscustomobject]@{ NextIndex = $Index; Handled = $true }
        }
        '^(--serverurl|--server-url|-serverurl|-server-url)(=.*)?$' {
            $result = Read-OptionValue -Token $Token -Index $Index -SourceArgs $SourceArgs -OptionName '--serverurl'
            $script:forwardedServerUrl = $result.Value
            return [pscustomobject]@{ NextIndex = $result.NextIndex; Handled = $true }
        }
        '^(--enrollmentcode|--enrollment-code|-enrollmentcode|-enrollment-code)(=.*)?$' {
            $result = Read-OptionValue -Token $Token -Index $Index -SourceArgs $SourceArgs -OptionName '--enrollmentcode'
            $script:forwardedEnrollmentCode = $result.Value
            return [pscustomobject]@{ NextIndex = $result.NextIndex; Handled = $true }
        }
        default {
            return [pscustomobject]@{ NextIndex = $Index; Handled = $false }
        }
    }
}

function Invoke-WebRequestWithRetry {
    param(
        [string]$Uri,
        [string]$OutFile,
        [int]$MaxAttempts = 3,
        [int]$InitialDelaySeconds = 2
    )

    if ([string]::IsNullOrWhiteSpace($Uri)) {
        throw 'Download URL cannot be empty.'
    }

    $lastError = $null
    for ($attempt = 1; $attempt -le $MaxAttempts; $attempt++) {
        try {
            if (Test-Path $OutFile) {
                Remove-Item -Path $OutFile -Force -ErrorAction SilentlyContinue
            }

            Write-Host ("[i] Download attempt {0}/{1} from {2}" -f $attempt, $MaxAttempts, $Uri)

            $iwrParams = @{
                Uri = $Uri
                OutFile = $OutFile
                ErrorAction = 'Stop'
            }
            $iwrCommand = Get-Command Invoke-WebRequest -ErrorAction Stop
            if ($iwrCommand.Parameters.ContainsKey('UseBasicParsing')) {
                $iwrParams['UseBasicParsing'] = $true
            }

            Invoke-WebRequest @iwrParams

            if (-not (Test-Path $OutFile -PathType Leaf)) {
                throw "Download completed but '$OutFile' was not created."
            }
            $fileInfo = Get-Item -LiteralPath $OutFile -ErrorAction Stop
            if ($fileInfo.Length -le 0) {
                throw "Downloaded file '$OutFile' is empty."
            }

            return
        } catch {
            $lastError = $_
            if ($attempt -lt $MaxAttempts) {
                $delaySeconds = [Math]::Min(30, [int]([Math]::Pow(2, $attempt - 1) * $InitialDelaySeconds))
                Write-Host ("[!] Download failed from {0}: {1}" -f $Uri, $_.Exception.Message) -ForegroundColor Yellow
                Write-Host ("[i] Retrying in {0} second(s)..." -f $delaySeconds) -ForegroundColor Yellow
                Start-Sleep -Seconds $delaySeconds
            }
        }
    }

    if ($lastError) {
        throw $lastError
    }
    throw "Failed to download from '$Uri'."
}

function Expand-ZipArchiveCompat {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ArchivePath,

        [Parameter(Mandatory = $true)]
        [string]$DestinationPath,

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
        New-Item -Path $DestinationPath -ItemType Directory -Force | Out-Null
    }

    try {
        Expand-Archive -LiteralPath $ArchivePath -DestinationPath $DestinationPath -Force -ErrorAction Stop
        return
    } catch {
        try {
            Add-Type -AssemblyName 'System.IO.Compression.FileSystem' -ErrorAction SilentlyContinue
            [System.IO.Compression.ZipFile]::ExtractToDirectory($ArchivePath, $DestinationPath)
            return
        } catch {
            throw "Failed to extract archive '$ArchivePath': $($_.Exception.Message)"
        }
    }
}

function Invoke-GitCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$GitExe,

        [Parameter(Mandatory = $true)]
        [string[]]$Arguments,

        [Parameter()]
        [string]$WorkingDirectory = ''
    )

    if ($WorkingDirectory) {
        & $GitExe -C $WorkingDirectory @Arguments
    } else {
        & $GitExe @Arguments
    }
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "Git command failed with exit code ${exitCode}: git $($Arguments -join ' ')"
    }
}

function Ensure-PortableGit {
    param(
        [string]$GitZipUrl,
        [string]$GitZipPath,
        [string]$GitRoot
    )

    if ([string]::IsNullOrWhiteSpace($GitRoot)) {
        throw 'Portable Git destination cannot be empty.'
    }

    $gitExe = Join-Path $GitRoot 'cmd\git.exe'
    if (Test-Path $gitExe -PathType Leaf) {
        try {
            & $gitExe --version | Out-Null
            if ($LASTEXITCODE -eq 0) {
                return $gitExe
            }
        } catch {}
    }

    $gitZipDir = Split-Path -Path $GitZipPath -Parent
    if (-not [string]::IsNullOrWhiteSpace($gitZipDir) -and -not (Test-Path $gitZipDir)) {
        New-Item -Path $gitZipDir -ItemType Directory -Force | Out-Null
    }

    Write-Host "[i] Downloading portable Git from $GitZipUrl"
    Invoke-WebRequestWithRetry -Uri $GitZipUrl -OutFile $GitZipPath
    Expand-ZipArchiveCompat -ArchivePath $GitZipPath -DestinationPath $GitRoot -ClearDestination

    if (-not (Test-Path $gitExe -PathType Leaf)) {
        throw "Portable Git was extracted but git.exe was not found at '$gitExe'."
    }

    & $gitExe --version | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw 'Portable Git failed validation after extraction.'
    }

    return $gitExe
}

function Sync-BorealisRepository {
    param(
        [string]$GitExe,
        [string]$RepositoryUrl,
        [string]$Ref,
        [string]$DestinationPath,
        [string[]]$PreserveDirectories = @('Agent')
    )

    if (-not (Test-Path $DestinationPath)) {
        New-Item -Path $DestinationPath -ItemType Directory -Force | Out-Null
    }

    $gitMetadataPath = Join-Path $DestinationPath '.git'
    if (-not (Test-Path $gitMetadataPath -PathType Container)) {
        Get-ChildItem -Path $DestinationPath -Force -ErrorAction SilentlyContinue | ForEach-Object {
            if ($PreserveDirectories -contains $_.Name) { return }
            Remove-Item -Path $_.FullName -Recurse -Force -ErrorAction SilentlyContinue
        }

        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $DestinationPath -Arguments @('init')
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $DestinationPath -Arguments @('remote', 'add', 'origin', $RepositoryUrl)
    } else {
        $originUrl = ''
        try {
            $originUrl = (& $GitExe -C $DestinationPath remote get-url origin 2>$null | Select-Object -First 1)
        } catch {
            $originUrl = ''
        }

        if ([string]::IsNullOrWhiteSpace($originUrl)) {
            Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $DestinationPath -Arguments @('remote', 'add', 'origin', $RepositoryUrl)
        } elseif ($originUrl.Trim() -ne $RepositoryUrl) {
            Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $DestinationPath -Arguments @('remote', 'set-url', 'origin', $RepositoryUrl)
        }
    }

    Write-Host "[i] Fetching Borealis ref '$Ref' from $RepositoryUrl"
    Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $DestinationPath -Arguments @('fetch', '--depth', '1', '--force', 'origin', $Ref)
    Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $DestinationPath -Arguments @('checkout', '--force', '-B', 'bootstrap-deploy', 'FETCH_HEAD')
    Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $DestinationPath -Arguments @('reset', '--hard', 'FETCH_HEAD')

    $cleanArgs = New-Object System.Collections.Generic.List[string]
    $cleanArgs.Add('clean')
    $cleanArgs.Add('-fdx')
    foreach ($preserve in $PreserveDirectories) {
        if (-not [string]::IsNullOrWhiteSpace($preserve)) {
            $cleanArgs.Add('-e')
            $cleanArgs.Add($preserve)
        }
    }
    Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $DestinationPath -Arguments @($cleanArgs.ToArray())
}

$rawArgs = @($args)
for ($i = 0; $i -lt $rawArgs.Count; $i++) {
    $token = [string]$rawArgs[$i]
    $normalized = $token.ToLowerInvariant()

    switch -Regex ($normalized) {
        '^(-h|--help|-help|/\?)$' {
            Show-Usage
            exit 0
        }
        '^(--install-dir|-install-dir|-installdir)(=.*)?$' {
            $result = Read-OptionValue -Token $token -Index $i -SourceArgs $rawArgs -OptionName '--install-dir'
            $installDir = $result.Value
            $i = $result.NextIndex
            continue
        }
        '^(--repo-url|-repo-url|-repourl)(=.*)?$' {
            $result = Read-OptionValue -Token $token -Index $i -SourceArgs $rawArgs -OptionName '--repo-url'
            $repoUrl = $result.Value
            $i = $result.NextIndex
            continue
        }
        '^(--ref|--branch|-ref|-branch)(=.*)?$' {
            $result = Read-OptionValue -Token $token -Index $i -SourceArgs $rawArgs -OptionName '--ref'
            $repoRef = $result.Value
            $i = $result.NextIndex
            continue
        }
        '^(--git-zip-url|-git-zip-url|-gitzipurl)(=.*)?$' {
            $result = Read-OptionValue -Token $token -Index $i -SourceArgs $rawArgs -OptionName '--git-zip-url'
            $gitZipUrl = $result.Value
            $i = $result.NextIndex
            continue
        }
        '^(--git-zip-path|-git-zip-path|-gitzippath)(=.*)?$' {
            $result = Read-OptionValue -Token $token -Index $i -SourceArgs $rawArgs -OptionName '--git-zip-path'
            $gitZipPath = $result.Value
            $i = $result.NextIndex
            continue
        }
        '^(--git-dir|-git-dir|-gitdir)(=.*)?$' {
            $result = Read-OptionValue -Token $token -Index $i -SourceArgs $rawArgs -OptionName '--git-dir'
            $gitCacheDir = $result.Value
            $i = $result.NextIndex
            continue
        }
        '^(--zip-url|-zip-url|-zipurl)(=.*)?$' {
            throw 'ZIP-based repository bootstrap is no longer supported. Use --repo-url/--ref.'
        }
        '^(--zip-path|-zip-path|-zippath)(=.*)?$' {
            throw 'ZIP-based repository bootstrap is no longer supported. Use --repo-url/--ref.'
        }
    }

    $normalizedResult = Normalize-BorealisArgument -Token $token -Index $i -SourceArgs $rawArgs
    if ($normalizedResult.Handled) {
        $i = $normalizedResult.NextIndex
        continue
    }

    $passthroughArgs.Add($token)
}

if ([string]::IsNullOrWhiteSpace($forwardedServerUrl) -and -not [string]::IsNullOrWhiteSpace($env:BOREALIS_SERVER_URL)) {
    $forwardedServerUrl = $env:BOREALIS_SERVER_URL
}
if ([string]::IsNullOrWhiteSpace($forwardedEnrollmentCode) -and -not [string]::IsNullOrWhiteSpace($env:BOREALIS_ENROLLMENT_CODE)) {
    $forwardedEnrollmentCode = $env:BOREALIS_ENROLLMENT_CODE
}
if (-not [string]::IsNullOrWhiteSpace($forwardedServerUrl)) {
    $forwardedServerUrl = $forwardedServerUrl.Trim()
}
if (-not [string]::IsNullOrWhiteSpace($forwardedEnrollmentCode)) {
    $forwardedEnrollmentCode = $forwardedEnrollmentCode.Trim()
}

if ([string]::IsNullOrWhiteSpace($installDir)) {
    throw "Refusing to install into an empty path or root path."
}
$resolvedInstallDir = [System.IO.Path]::GetFullPath($installDir)
$installRoot = [System.IO.Path]::GetPathRoot($resolvedInstallDir)
if ([string]::IsNullOrWhiteSpace($installRoot)) {
    throw "Refusing to install into an invalid path: '$installDir'."
}
if ($resolvedInstallDir.TrimEnd('\') -eq $installRoot.TrimEnd('\')) {
    throw "Refusing to install into root path '$resolvedInstallDir'."
}
if ($resolvedInstallDir -ne $installDir) {
    $installDir = $resolvedInstallDir
}
if ([string]::IsNullOrWhiteSpace($repoUrl)) {
    throw 'Repository URL cannot be empty.'
}
if ([string]::IsNullOrWhiteSpace($repoRef)) {
    throw 'Repository ref cannot be empty.'
}

try {
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch {}

    $gitExe = Ensure-PortableGit -GitZipUrl $gitZipUrl -GitZipPath $gitZipPath -GitRoot $gitCacheDir

    Write-Host "[i] Syncing Borealis repository into $installDir"
    Sync-BorealisRepository -GitExe $gitExe -RepositoryUrl $repoUrl -Ref $repoRef -DestinationPath $installDir -PreserveDirectories @('Agent')

    $borealisScript = Join-Path $installDir 'Borealis.ps1'
    if (-not (Test-Path $borealisScript -PathType Leaf)) {
        throw "Borealis.ps1 not found at '$borealisScript' after Git checkout."
    }

    $invokeArgs = New-Object System.Collections.Generic.List[string]
    $invokeArgs.Add('-Agent')
    if (-not [string]::IsNullOrWhiteSpace($forwardedServerUrl)) {
        Write-Host "[i] Forwarding server URL to Borealis.ps1"
        $invokeArgs.Add('-ServerUrl')
        $invokeArgs.Add($forwardedServerUrl)
        $env:BOREALIS_SERVER_URL = $forwardedServerUrl
    }
    if (-not [string]::IsNullOrWhiteSpace($forwardedEnrollmentCode)) {
        Write-Host "[i] Forwarding enrollment code to Borealis.ps1"
        $invokeArgs.Add('-EnrollmentCode')
        $invokeArgs.Add($forwardedEnrollmentCode)
        $env:BOREALIS_ENROLLMENT_CODE = $forwardedEnrollmentCode
    }
    foreach ($arg in $passthroughArgs) {
        if (-not [string]::IsNullOrWhiteSpace($arg)) {
            $invokeArgs.Add($arg)
        }
    }

    $powershellExe = (Get-Command powershell.exe -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -First 1)
    if (-not $powershellExe) {
        $powershellExe = (Get-Process -Id $PID).Path
    }
    if (-not $powershellExe) {
        throw 'Unable to locate a PowerShell host to launch Borealis.ps1.'
    }

    $launcherArgs = New-Object System.Collections.Generic.List[string]
    $launcherArgs.Add('-NoProfile')
    $launcherArgs.Add('-ExecutionPolicy')
    $launcherArgs.Add('Bypass')
    $launcherArgs.Add('-File')
    $launcherArgs.Add($borealisScript)
    foreach ($arg in $invokeArgs) {
        $launcherArgs.Add($arg)
    }

    Write-Host "[i] Launching $borealisScript"
    & $powershellExe @($launcherArgs.ToArray())
    if ($null -ne $LASTEXITCODE) {
        exit $LASTEXITCODE
    }
    exit 0
}
finally {
    if (Test-Path $gitZipPath) {
        Remove-Item -Path $gitZipPath -Force -ErrorAction SilentlyContinue
    }
}
