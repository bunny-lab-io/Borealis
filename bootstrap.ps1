# Borealis Windows bootstrapper:
# - Downloads Borealis ZIP from GitHub
# - Extracts to C:\Borealis (or --install-dir)
# - Executes Borealis.ps1, forwarding agent enrollment arguments

$ErrorActionPreference = 'Stop'

$defaultInstallDir = if ($env:BOREALIS_INSTALL_DIR) { $env:BOREALIS_INSTALL_DIR } else { 'C:\Borealis' }
$defaultZipUrl = if ($env:BOREALIS_BOOTSTRAP_ZIP_URL) { $env:BOREALIS_BOOTSTRAP_ZIP_URL } else { 'https://github.com/bunny-lab-io/Borealis/archive/refs/heads/main.zip' }
$defaultZipPath = if ($env:BOREALIS_BOOTSTRAP_ZIP_PATH) { $env:BOREALIS_BOOTSTRAP_ZIP_PATH } else { Join-Path $env:TEMP 'BorealisBootstrap.zip' }

$installDir = $defaultInstallDir
$zipUrl = $defaultZipUrl
$zipPath = $defaultZipPath
$forwardedServerUrl = $null
$forwardedEnrollmentCode = $null
$passthroughArgs = New-Object System.Collections.Generic.List[string]

function Show-Usage {
    @'
Usage: bootstrap.ps1 [bootstrap options] [Borealis.ps1 options]

Bootstrap options:
  --install-dir <path>   Install location (default: C:\Borealis)
  --zip-url <url>        ZIP source URL
  --zip-path <path>      ZIP destination path (default: %TEMP%\BorealisBootstrap.zip)
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

function Get-BootstrapZipUris {
    param(
        [string]$PrimaryUrl
    )

    $uris = New-Object System.Collections.Generic.List[string]
    if (-not [string]::IsNullOrWhiteSpace($PrimaryUrl)) {
        $uris.Add($PrimaryUrl.Trim())
    }

    if (-not [string]::IsNullOrWhiteSpace($env:BOREALIS_BOOTSTRAP_FALLBACK_ZIP_URL)) {
        $uris.Add($env:BOREALIS_BOOTSTRAP_FALLBACK_ZIP_URL.Trim())
    }

    return $uris | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -Unique
}

function Invoke-WebRequestWithRetry {
    param(
        [string[]]$Uris,
        [string]$OutFile,
        [int]$MaxAttemptsPerUri = 3,
        [int]$InitialDelaySeconds = 2
    )

    $lastError = $null
    foreach ($uri in @($Uris)) {
        if ([string]::IsNullOrWhiteSpace($uri)) { continue }

        for ($attempt = 1; $attempt -le $MaxAttemptsPerUri; $attempt++) {
            try {
                if (Test-Path $OutFile) {
                    Remove-Item -Path $OutFile -Force -ErrorAction SilentlyContinue
                }

                Write-Host ("[i] Download attempt {0}/{1} from {2}" -f $attempt, $MaxAttemptsPerUri, $uri)

                $iwrParams = @{
                    Uri = $uri
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

                return $uri
            } catch {
                $lastError = $_
                if ($attempt -lt $MaxAttemptsPerUri) {
                    $delaySeconds = [Math]::Min(30, [int]([Math]::Pow(2, $attempt - 1) * $InitialDelaySeconds))
                    Write-Host ("[!] Download failed from {0}: {1}" -f $uri, $_.Exception.Message) -ForegroundColor Yellow
                    Write-Host ("[i] Retrying in {0} second(s)..." -f $delaySeconds) -ForegroundColor Yellow
                    Start-Sleep -Seconds $delaySeconds
                } else {
                    Write-Host ("[!] Exhausted retries for {0}: {1}" -f $uri, $_.Exception.Message) -ForegroundColor Yellow
                }
            }
        }
    }

    if ($lastError) {
        throw $lastError
    }
    throw 'No usable download URL was provided.'
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
        '^(--zip-url|-zip-url|-zipurl)(=.*)?$' {
            $result = Read-OptionValue -Token $token -Index $i -SourceArgs $rawArgs -OptionName '--zip-url'
            $zipUrl = $result.Value
            $i = $result.NextIndex
            continue
        }
        '^(--zip-path|-zip-path|-zippath)(=.*)?$' {
            $result = Read-OptionValue -Token $token -Index $i -SourceArgs $rawArgs -OptionName '--zip-path'
            $zipPath = $result.Value
            $i = $result.NextIndex
            continue
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
if ([string]::IsNullOrWhiteSpace($zipPath)) {
    throw 'ZIP path cannot be empty.'
}

$zipDirectory = Split-Path -Path $zipPath -Parent
if (-not [string]::IsNullOrWhiteSpace($zipDirectory) -and -not (Test-Path $zipDirectory)) {
    New-Item -Path $zipDirectory -ItemType Directory -Force | Out-Null
}

if (Test-Path $zipPath) {
    Remove-Item -Path $zipPath -Force -ErrorAction SilentlyContinue
}

$extractRoot = Join-Path $env:TEMP ("BorealisBootstrap_{0}" -f ([guid]::NewGuid().ToString('N')))
if (Test-Path $extractRoot) {
    Remove-Item -Path $extractRoot -Recurse -Force -ErrorAction SilentlyContinue
}
New-Item -Path $extractRoot -ItemType Directory -Force | Out-Null

try {
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch {}

    $zipUris = Get-BootstrapZipUris -PrimaryUrl $zipUrl
    if ($zipUris.Count -eq 0) {
        throw "No valid ZIP URL was provided."
    }

    Write-Host "[i] Downloading Borealis ZIP from $($zipUris[0])"
    $resolvedZipUri = Invoke-WebRequestWithRetry -Uris $zipUris -OutFile $zipPath
    if ($resolvedZipUri -ne $zipUrl) {
        Write-Host "[i] Used fallback ZIP source: $resolvedZipUri"
    }

    Expand-ZipArchiveCompat -ArchivePath $zipPath -DestinationPath $extractRoot -ClearDestination

    $extractedRoot = Get-ChildItem -Path $extractRoot -Directory -ErrorAction Stop |
        Where-Object { $_.Name -like 'Borealis-*' } |
        Select-Object -First 1
    if (-not $extractedRoot) {
        throw 'Could not locate extracted Borealis directory.'
    }

    if (-not (Test-Path $installDir)) {
        New-Item -Path $installDir -ItemType Directory -Force | Out-Null
    }

    Write-Host "[i] Installing Borealis into $installDir"
    $preserveDirectories = @('Agent')
    Get-ChildItem -Path $installDir -Force -ErrorAction SilentlyContinue | ForEach-Object {
        if ($preserveDirectories -contains $_.Name) { return }
        Remove-Item -Path $_.FullName -Recurse -Force -ErrorAction SilentlyContinue
    }

    Get-ChildItem -Path $extractedRoot.FullName -Force | ForEach-Object {
        if ($preserveDirectories -contains $_.Name) { return }
        $destinationPath = Join-Path $installDir $_.Name
        if (Test-Path $destinationPath) {
            Remove-Item -Path $destinationPath -Recurse -Force -ErrorAction SilentlyContinue
        }
        Copy-Item -Path $_.FullName -Destination $destinationPath -Recurse -Force
    }

    $borealisScript = Join-Path $installDir 'Borealis.ps1'
    if (-not (Test-Path $borealisScript -PathType Leaf)) {
        throw "Borealis.ps1 not found at '$borealisScript' after extraction."
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
    if (Test-Path $extractRoot) {
        Remove-Item -Path $extractRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
