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
    Write-Host "[i] Downloading Borealis ZIP from $zipUrl"
    Invoke-WebRequest -Uri $zipUrl -OutFile $zipPath

    Expand-Archive -Path $zipPath -DestinationPath $extractRoot -Force

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
        Copy-Item -Path $_.FullName -Destination (Join-Path $installDir $_.Name) -Recurse -Force
    }

    $borealisScript = Join-Path $installDir 'Borealis.ps1'
    if (-not (Test-Path $borealisScript -PathType Leaf)) {
        throw "Borealis.ps1 not found at '$borealisScript' after extraction."
    }

    $invokeArgs = New-Object System.Collections.Generic.List[string]
    $invokeArgs.Add('-Agent')
    if (-not [string]::IsNullOrWhiteSpace($forwardedServerUrl)) {
        $invokeArgs.Add('-ServerUrl')
        $invokeArgs.Add($forwardedServerUrl)
    }
    if (-not [string]::IsNullOrWhiteSpace($forwardedEnrollmentCode)) {
        $invokeArgs.Add('-EnrollmentCode')
        $invokeArgs.Add($forwardedEnrollmentCode)
    }
    foreach ($arg in $passthroughArgs) {
        if (-not [string]::IsNullOrWhiteSpace($arg)) {
            $invokeArgs.Add($arg)
        }
    }

    Write-Host "[i] Launching $borealisScript"
    & $borealisScript @($invokeArgs.ToArray())
    exit $LASTEXITCODE
}
finally {
    if (Test-Path $extractRoot) {
        Remove-Item -Path $extractRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
