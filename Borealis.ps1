#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Borealis.ps1

[CmdletBinding()]
param(
    [switch]$Server,
    [switch]$Agent,
    [switch]$Vite,
    [switch]$Flask,
    [switch]$Quick,
    [switch]$EngineTests,
    [switch]$EngineProduction,
    [switch]$EngineDev,
    [string]$EnrollmentCode = ''
)

# Preselect menu choices from CLI args (optional)
$choice = $null
$modeChoice = $null
$engineModeChoice = $null

$scriptDir = Split-Path $MyInvocation.MyCommand.Path -Parent

if ($EngineTests) {
    Set-Location -Path $scriptDir
    $env:BOREALIS_PROJECT_ROOT = $scriptDir

    $python = Get-Command python3 -ErrorAction SilentlyContinue
    if (-not $python) {
        $python = Get-Command python -ErrorAction SilentlyContinue
    }

    if (-not $python) {
        Write-Host "Python interpreter not found. Install Python 3 to run Engine tests." -ForegroundColor Red
        exit 1
    }

    & $python.Source -m pytest 'Data/Engine/Unit_Tests'
    exit $LASTEXITCODE
}

if ($Server -and $Agent) {
    Write-Host "Cannot use -Server and -Agent together." -ForegroundColor Red
    exit 1
}

if ($Vite -and $Flask) {
    Write-Host "Cannot combine -Vite and -Flask." -ForegroundColor Red
    exit 1
}

if ($EngineProduction -and $EngineDev) {
    Write-Host "Cannot combine -EngineProduction and -EngineDev." -ForegroundColor Red
    exit 1
}

if (($EngineProduction -or $EngineDev) -and ($Server -or $Agent)) {
    Write-Host "Engine automation switches cannot be combined with -Server or -Agent." -ForegroundColor Red
    exit 1
}

if ($Server) {
    # Auto-select main menu option for Server when -Server flag is provided
    $choice = '1'
} elseif ($Agent) {
    $choice = '2'
} elseif ($EngineProduction -or $EngineDev) {
    $choice = '1'
    if ($EngineProduction) { $engineModeChoice = '1' }
    if ($EngineDev) { $engineModeChoice = '3' }
}

if ($Server) {
    if     ($Vite)             { $modeChoice = '3' }
    elseif ($Flask -and $Quick){ $modeChoice = '2' }
    elseif ($Flask)            { $modeChoice = '1' }
}
$host.UI.RawUI.WindowTitle = "Borealis"
Clear-Host

## Note: Heavy dependency downloads are deferred until selecting Server (option 1)
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

# Admin/Elevation helpers for Agent deployment
function Test-IsAdmin {
    try {
        $id = [Security.Principal.WindowsIdentity]::GetCurrent()
        $p  = New-Object Security.Principal.WindowsPrincipal($id)
        return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    } catch { return $false }
}

function Request-AgentElevation {
    param(
        [string]$ScriptPath,
        [switch]$Auto
    )
    if (Test-IsAdmin) { return $true }

    if (-not $Auto) {
        Write-Host ""  # spacer
        Write-Host "Agent requires Administrator permissions to register scheduled tasks and run reliably." -ForegroundColor Yellow -BackgroundColor Black
        Write-Host "Grant elevated permissions now? (Y/N)" -ForegroundColor Yellow -BackgroundColor Black
        $resp = Read-Host
        if ($resp -notin @('y','Y','yes','YES')) { return $false }
    }

    $args = @('-NoProfile','-ExecutionPolicy','Bypass','-File', '"' + $ScriptPath + '"', '-Agent')
    try {
        Start-Process -FilePath 'powershell.exe' -Verb RunAs -ArgumentList $args -WindowStyle Normal | Out-Null
        return $false  # stop current non-elevated instance
    } catch {
        Write-Host "Elevation was denied or failed." -ForegroundColor Red
        return $false
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

function Ensure-EngineLogDir {
    $engineRoot = Join-Path $scriptDir 'Engine'
    if (-not (Test-Path $engineRoot)) {
        New-Item -ItemType Directory -Path $engineRoot -Force | Out-Null
    }
    $engineLogDir = Join-Path $engineRoot 'Logs'
    if (-not (Test-Path $engineLogDir)) {
        New-Item -ItemType Directory -Path $engineLogDir -Force | Out-Null
    }
    return $engineLogDir
}

function Write-ViteLog {
    param(
        [string]$Message,
        [string]$ServiceName = 'vite-dev'
    )
    $engineLogDir = Ensure-EngineLogDir
    $logPath = Join-Path $engineLogDir 'vite.log'
    $timestamp = (Get-Date).ToString('s')
    "$timestamp-$ServiceName-$Message" | Out-File -FilePath $logPath -Append -Encoding UTF8
}

function Ensure-EngineTlsMaterial {
    param(
        [string]$PythonPath,
        [string]$CertificateRoot
    )

    $effectiveRoot = $null

    if (Test-Path $PythonPath) {
        $code = @'
from Data.Engine.security import certificates
certificates.ensure_certificate()
print(certificates.engine_certificates_root())
'@
        try {
            $output = & $PythonPath -c $code
            if ($output) {
                $raw = $output | Select-Object -Last 1
                if ($raw) {
                    $effectiveRoot = ([string]$raw).Trim()
                }
            }
        } catch {
            Write-Host "Failed to pre-generate Engine TLS certificates: $($_.Exception.Message)" -ForegroundColor Yellow
        }
    }

    if (-not $effectiveRoot -and $CertificateRoot) {
        $certCandidate = Join-Path $CertificateRoot 'borealis-server-cert.pem'
        $keyCandidate  = Join-Path $CertificateRoot 'borealis-server-key.pem'
        if ((Test-Path $certCandidate) -and (Test-Path $keyCandidate)) {
            $effectiveRoot = $CertificateRoot
        } else {
            $fallbackMessage = "Provided certificate root '$CertificateRoot' is missing expected TLS material; using Engine runtime certificates instead."
            Write-Host $fallbackMessage -ForegroundColor Yellow
            try { Write-ViteLog $fallbackMessage } catch {}
        }
    }

    if (-not $effectiveRoot) {
        $effectiveRoot = Join-Path $scriptDir 'Engine\Certificates'
    }

    if (-not (Test-Path $effectiveRoot)) {
        New-Item -Path $effectiveRoot -ItemType Directory -Force | Out-Null
    }

    $env:BOREALIS_CERT_DIR   = $effectiveRoot
    $env:BOREALIS_TLS_CERT   = Join-Path $effectiveRoot 'borealis-server-cert.pem'
    $env:BOREALIS_TLS_KEY    = Join-Path $effectiveRoot 'borealis-server-key.pem'
    $env:BOREALIS_TLS_BUNDLE = Join-Path $effectiveRoot 'borealis-server-bundle.pem'
}

function Ensure-EngineWebInterface {
    param(
        [string]$ProjectRoot
    )

    $engineDestination = Join-Path $ProjectRoot 'Engine\web-interface'
    $engineStageSource = Join-Path $ProjectRoot 'Data\Engine\web-interface'

    if (-not (Test-Path $engineStageSource)) {
        throw "Engine web interface source missing at '$engineStageSource'."
    }

    if (Test-Path $engineDestination) {
        Remove-Item $engineDestination -Recurse -Force -ErrorAction SilentlyContinue
    }

    New-Item -Path $engineDestination -ItemType Directory -Force | Out-Null

    Copy-Item (Join-Path $engineStageSource '*') $engineDestination -Recurse -Force

    if (-not (Test-Path (Join-Path $engineDestination 'package.json'))) {
        throw "Failed to stage Engine web interface into '$engineDestination' from '$engineStageSource'."
    }
}

function Get-WebUiLatestWriteTime {
    param([string]$Root)

    if (-not (Test-Path $Root)) { return $null }

    $exclusions = @('\node_modules\', '\build\', '\dist\')
    $files = Get-ChildItem -Path $Root -Recurse -File -ErrorAction SilentlyContinue | Where-Object {
        $full = $_.FullName
        -not ($exclusions | Where-Object { $full -like "*$_*" })
    }
    if (-not $files) { return $null }
    return ($files | Sort-Object -Property LastWriteTime -Descending | Select-Object -First 1).LastWriteTime
}

function Test-WebUiBuildFresh {
    param(
        [string]$SourceRoot,
        [string]$BuildRoot
    )

    $sourceLatest = Get-WebUiLatestWriteTime -Root $SourceRoot
    if (-not $sourceLatest) { return $false }

    $buildIndex = Join-Path $BuildRoot 'index.html'
    if (-not (Test-Path $buildIndex -PathType Leaf)) { return $false }

    try {
        $buildTime = (Get-Item $buildIndex -ErrorAction Stop).LastWriteTime
    } catch {
        return $false
    }

    return ($buildTime -ge $sourceLatest)
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

# ---------------------- Server Deployment / Operation Mode Variables ----------------------
# Define the default operation mode: production  | developer
[string]$borealis_operation_mode = 'production'

# ---------------------- Bundle Executables Setup ----------------------
$scriptDir  = Split-Path $MyInvocation.MyCommand.Path -Parent
$depsRoot   = Join-Path $scriptDir 'Dependencies'
$pythonExe  = Join-Path $depsRoot 'Python\python.exe'
$nodeExe    = Join-Path $depsRoot 'NodeJS\node.exe'
$sevenZipExe    = Join-Path $depsRoot "7zip\7z.exe"
$npmCmd     = Join-Path (Split-Path $nodeExe) 'npm.cmd'
$npxCmd     = Join-Path (Split-Path $nodeExe) 'npx.cmd'
$ansibleEeRequirementsPath = Join-Path $scriptDir 'Data\Agent\ansible-ee-requirements.txt'
$ansibleEeVersionFile      = Join-Path $scriptDir 'Data\Agent\ansible-ee-version.txt'
$script:AnsibleExecutionEnvironmentVersion = '1.0.0'
if (Test-Path $ansibleEeVersionFile -PathType Leaf) {
    try {
        $rawVersion = (Get-Content -Path $ansibleEeVersionFile -Raw -ErrorAction Stop)
        if ($rawVersion) {
            $script:AnsibleExecutionEnvironmentVersion = ($rawVersion.Split("`n")[0]).Trim()
        }
    } catch {
        # Leave default version value
    }
}
$node7zUrl      = "https://nodejs.org/dist/v23.11.0/node-v23.11.0-win-x64.7z"
$nodeInstallDir = Join-Path $depsRoot "NodeJS"
$node7zPath     = Join-Path $depsRoot "node-v23.11.0-win-x64.7z"
$gitVersionTag  = 'v2.47.1.windows.1'
$gitPackageName = 'MinGit-2.47.1-64-bit.zip'
$gitZipUrl      = "https://github.com/git-for-windows/git/releases/download/$gitVersionTag/$gitPackageName"
$gitZipPath     = Join-Path $depsRoot $gitPackageName
$gitInstallDir  = Join-Path $depsRoot 'git'
$gitExePath     = Join-Path $gitInstallDir 'cmd\git.exe'

# ---------------------- Dependency Installation Functions ----------------------
function Install_Shared_Dependencies {
    # Python (shared by Server and Agent)
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
                    Invoke-WebRequest -Uri $url -OutFile $localPath
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

function Install_Server_Dependencies {
    # Tesseract OCR Engine
    Run-Step "Dependency: Tesseract-OCR" {
        $tessExeUrl     = "https://github.com/tesseract-ocr/tesseract/releases/download/5.5.0/tesseract-ocr-w64-setup-5.5.0.20241111.exe"
        $tessExePath    = Join-Path $depsRoot "tesseract-installer.exe"
        $tessInstallDir = Join-Path $scriptDir "Data\Engine\Python_API_Endpoints\Tesseract-OCR"

        if (-not (Test-Path (Join-Path $tessInstallDir "tesseract.exe"))) {
            # Download the installer if it doesn't exist
            if (-not (Test-Path $tessExePath)) {
                Invoke-WebRequest -Uri $tessExeUrl -OutFile $tessExePath
            }

            # Extract using 7-Zip
            if (-not (Test-Path $sevenZipExe)) {
                throw "7-Zip CLI not found at: $sevenZipExe"
            }

            if (Test-Path $tessInstallDir) {
                Remove-Item $tessInstallDir -Recurse -Force -ErrorAction SilentlyContinue
            }
            New-Item -ItemType Directory -Path $tessInstallDir | Out-Null

            & $sevenZipExe x $tessExePath "-o$tessInstallDir" -y | Out-Null

            # Optional cleanup
            Remove-Item $tessExePath -Force -ErrorAction SilentlyContinue
        }
    }

    # Tesseract Language Data
    Run-Step "Dependency: Tesseract-OCR - Pre-Trained Model Data" {
        $langDataDir = Join-Path $scriptDir "Data\Engine\Python_API_Endpoints\Tesseract-OCR\tessdata"
        $engPath     = Join-Path $langDataDir "eng.traineddata"
        $osdPath     = Join-Path $langDataDir "osd.traineddata"

        if (-not (Test-Path $engPath)) {
            Invoke-WebRequest -Uri "https://github.com/tesseract-ocr/tessdata/raw/main/eng.traineddata" -OutFile $engPath
        }

        if (-not (Test-Path $osdPath)) {
            Invoke-WebRequest -Uri "https://github.com/tesseract-ocr/tessdata/raw/main/osd.traineddata" -OutFile $osdPath
        }
    }

    # NodeJS (required for Vite / Web UI)
    Run-Step "Dependency: NodeJS" {
        if (-not (Test-Path $nodeExe)) {
            # Download archive if not present
            if (-not (Test-Path $node7zPath)) {
                Invoke-WebRequest -Uri $node7zUrl -OutFile $node7zPath
            }

            # Extract using bundled 7z
            if (-not (Test-Path $sevenZipExe)) {
                throw "7-Zip CLI not found at: $sevenZipExe"
            }

            & $sevenZipExe x $node7zPath "-o$nodeInstallDir" -y | Out-Null

            # The extracted contents might live under a subfolder; flatten if needed
            $extracted = Get-ChildItem $nodeInstallDir | Where-Object { $_.PSIsContainer } | Select-Object -First 1
            if ($extracted) {
                Get-ChildItem $extracted.FullName | Move-Item -Destination $nodeInstallDir -Force
                Remove-Item $extracted.FullName -Recurse -Force
            }

            # Clean Up 7z File After Extraction
            Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $node7zPath
        }
    }
}

function Install_Agent_Dependencies {
    # AutoHotKey portable
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
                Invoke-WebRequest -Uri $ahkZipUrl -OutFile $ahkZipPath
            }

            if (-not (Test-Path $sevenZipExe)) {
                throw "7-Zip CLI not found at: $sevenZipExe"
            }

            if (Test-Path $ahkInstallDir) {
                Remove-Item $ahkInstallDir -Recurse -Force -ErrorAction SilentlyContinue
            }
            New-Item -ItemType Directory -Path $ahkInstallDir | Out-Null
            & $sevenZipExe x $ahkZipPath "-o$ahkInstallDir" -y | Out-Null

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
                Invoke-WebRequest -Uri $gitZipUrl -OutFile $gitZipPath
            }

            if (-not (Test-Path $sevenZipExe)) {
                throw "7-Zip CLI not found at: $sevenZipExe"
            }

            if (Test-Path $gitInstallDir) {
                Remove-Item $gitInstallDir -Recurse -Force -ErrorAction SilentlyContinue
            }

            New-Item -ItemType Directory -Path $gitInstallDir | Out-Null
            & $sevenZipExe x $gitZipPath "-o$gitInstallDir" -y | Out-Null

            Remove-Item $gitZipPath -Force -ErrorAction SilentlyContinue

            if (-not (Test-Path $gitExePath)) {
                throw "Git executable not found after extraction."
            }
        }
    }
}

function Ensure-AnsibleExecutionEnvironment {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ProjectRoot,

        [string]$PythonBootstrapExe,

        [string]$RequirementsPath,
        [string]$ExpectedVersion = '1.0.0',
        [string]$LogName = 'Install.log'
    )

    $pythonBootstrap = $PythonBootstrapExe
    $bundleCandidate = Join-Path $ProjectRoot 'Dependencies\Python\python.exe'
    if ([string]::IsNullOrWhiteSpace($pythonBootstrap)) {
        $pythonBootstrap = $bundleCandidate
    }

    if (-not (Test-Path $pythonBootstrap -PathType Leaf)) {
        if ((-not [string]::IsNullOrWhiteSpace($PythonBootstrapExe)) -and ($PythonBootstrapExe -ne $pythonBootstrap)) {
            Write-AgentLog -FileName $LogName -Message "[AnsibleEE] Provided Python bootstrap path '$PythonBootstrapExe' was not found."
        }

        if (Test-Path $bundleCandidate -PathType Leaf) {
            $pythonBootstrap = $bundleCandidate
        } else {
            Write-AgentLog -FileName $LogName -Message "[AnsibleEE] Unable to locate bundled Python bootstrap executable at $bundleCandidate."
            throw "Bundled Python executable not found for Ansible execution environment provisioning."
        }
    }

    Write-AgentLog -FileName $LogName -Message "[AnsibleEE] Using Python bootstrap at $pythonBootstrap"

    $eeRoot = Join-Path $ProjectRoot 'Agent\Ansible_EE'
    $metadataPath = Join-Path $eeRoot 'metadata.json'
    $versionTxtPath = Join-Path $eeRoot 'version.txt'

    $requirementsHash = ''
    if ($RequirementsPath -and (Test-Path $RequirementsPath -PathType Leaf)) {
        try {
            $requirementsHash = (Get-FileHash -Path $RequirementsPath -Algorithm SHA256).Hash
        } catch {
            $requirementsHash = ''
        }
    }

    $currentVersion = ''
    $currentHash = ''
    if (Test-Path $metadataPath -PathType Leaf) {
        try {
            $metaRaw = Get-Content -Path $metadataPath -Raw -ErrorAction Stop
            if ($metaRaw) {
                $meta = $metaRaw | ConvertFrom-Json -ErrorAction Stop
                if ($meta.version) {
                    $currentVersion = ($meta.version).ToString().Trim()
                }
                if ($meta.requirements_hash) {
                    $currentHash = ($meta.requirements_hash).ToString().Trim()
                } elseif ($meta.requirements_sha256) {
                    $currentHash = ($meta.requirements_sha256).ToString().Trim()
                }
            }
        } catch {
            $currentVersion = ''
            $currentHash = ''
        }
    }

    $pythonCandidates = @(
        (Join-Path $eeRoot 'Scripts\python.exe')
        (Join-Path $eeRoot 'Scripts\python3.exe')
        (Join-Path $eeRoot 'bin\python3')
        (Join-Path $eeRoot 'bin\python')
    )

    $existingPython = $pythonCandidates | Where-Object { Test-Path $_ -PathType Leaf } | Select-Object -First 1

    $expectedVersionNorm = $ExpectedVersion
    if ([string]::IsNullOrWhiteSpace($expectedVersionNorm)) {
        $expectedVersionNorm = '1.0.0'
    }
    $expectedVersionNorm = $expectedVersionNorm.Trim()
    $isUpToDate = $false
    if ($existingPython -and $currentVersion -and ($currentVersion -eq $expectedVersionNorm)) {
        if (-not $requirementsHash -or ($currentHash -and $currentHash -eq $requirementsHash)) {
            $isUpToDate = $true
        }
    }

    if ($isUpToDate) {
        Write-AgentLog -FileName $LogName -Message "[AnsibleEE] Existing execution environment is up-to-date (version $currentVersion)."
        return
    }

    Write-AgentLog -FileName $LogName -Message "[AnsibleEE] Provisioning execution environment version $expectedVersionNorm."

    if (Test-Path $eeRoot) {
        try { Remove-Item -Path $eeRoot -Recurse -Force -ErrorAction Stop } catch {}
    }
    New-Item -ItemType Directory -Force -Path $eeRoot | Out-Null

    & $pythonBootstrap -m venv $eeRoot | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-AgentLog -FileName $LogName -Message "[AnsibleEE] python -m venv failed with exit code $LASTEXITCODE"
        throw "Failed to create Ansible execution environment virtual environment."
    }

    $pythonExe = $pythonCandidates | Where-Object { Test-Path $_ -PathType Leaf } | Select-Object -First 1
    if (-not $pythonExe) {
        Write-AgentLog -FileName $LogName -Message "[AnsibleEE] Unable to locate python executable inside execution environment."
        throw "Ansible execution environment python executable missing after provisioning."
    }

    & $pythonExe -m pip install --upgrade pip setuptools wheel --disable-pip-version-check | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-AgentLog -FileName $LogName -Message "[AnsibleEE] pip bootstrap failed with exit code $LASTEXITCODE"
        throw "Failed to bootstrap pip inside the Ansible execution environment."
    }

    if ($RequirementsPath -and (Test-Path $RequirementsPath -PathType Leaf)) {
        & $pythonExe -m pip install --disable-pip-version-check -r $RequirementsPath | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-AgentLog -FileName $LogName -Message "[AnsibleEE] pip install -r requirements failed with exit code $LASTEXITCODE"
            throw "Failed to install Ansible execution environment requirements."
        }
    } else {
        Write-AgentLog -FileName $LogName -Message "[AnsibleEE] Requirements file not found; skipping dependency installation."
    }

    $metadata = [ordered]@{
        version = $expectedVersionNorm
        created_utc = (Get-Date).ToUniversalTime().ToString('o')
        python = $pythonExe
        bootstrap_python = $pythonBootstrap
    }
    if ($requirementsHash) {
        $metadata['requirements_hash'] = $requirementsHash
    }

    $supportDir = Join-Path $eeRoot 'support'
    try {
        New-Item -ItemType Directory -Force -Path $supportDir | Out-Null
    } catch {}

    $fcntlStubPath = Join-Path $supportDir 'fcntl.py'
    $fcntlStub = @'
"""Compat shim for POSIX-only fcntl module.

Generated by Borealis to allow Ansible tooling to run on Windows hosts
where the standard library fcntl module is unavailable. The stub provides
symbol constants and no-op function implementations so imports succeed.
"""

LOCK_SH = 1
LOCK_EX = 2
LOCK_UN = 8
LOCK_NB = 4

F_DUPFD = 0
F_GETFD = 1
F_SETFD = 2
F_GETFL = 3
F_SETFL = 4

FD_CLOEXEC = 1

def ioctl(*_args, **_kwargs):
    return 0


def fcntl(*_args, **_kwargs):
    return 0


def flock(*_args, **_kwargs):
    return 0


def lockf(*_args, **_kwargs):
    return 0
'@

    try {
        if (-not (Test-Path (Join-Path $supportDir '__init__.py') -PathType Leaf)) {
            Set-FileUtf8Content -Path (Join-Path $supportDir '__init__.py') -Value ''
        }
        Set-FileUtf8Content -Path $fcntlStubPath -Value $fcntlStub
    } catch {
        Write-AgentLog -FileName $LogName -Message "[AnsibleEE] Failed to seed Windows fcntl compatibility shim: $($_.Exception.Message)"
    }

    try {
        $metadataJson = $metadata | ConvertTo-Json -Depth 5
        Set-FileUtf8Content -Path $metadataPath -Value $metadataJson
    } catch {
        Write-AgentLog -FileName $LogName -Message "[AnsibleEE] Failed to persist metadata.json: $($_.Exception.Message)"
        throw "Unable to persist Ansible execution environment metadata."
    }

    try {
        Set-FileUtf8Content -Path $versionTxtPath -Value $expectedVersionNorm
    } catch {}

    Write-AgentLog -FileName $LogName -Message "[AnsibleEE] Execution environment ready at $eeRoot"
}

function Ensure-AgentTasks {
    param([string]$ScriptRoot)
    $pyw         = Join-Path $ScriptRoot 'Agent\Scripts\pythonw.exe'
    $agentPy     = Join-Path $ScriptRoot 'Agent\Borealis\agent.py'
    $svcWrapper  = Join-Path $ScriptRoot 'Agent\Borealis\launch_service.ps1'
    if (-not (Test-Path $pyw))      { Write-Host "pythonw.exe not found under Agent\Scripts" -ForegroundColor Yellow; return }
    if (-not (Test-Path $agentPy))  { Write-Host "Agent script not found under Agent\Borealis" -ForegroundColor Yellow; return }
    if (-not (Test-Path $svcWrapper)) { Write-Host "launch_service.ps1 not found under Agent\Borealis" -ForegroundColor Yellow; return }

    # Clean old tasks first
    try { Unregister-ScheduledTask -TaskName 'Borealis Agent' -Confirm:$false -ErrorAction SilentlyContinue } catch {}
    try { Unregister-ScheduledTask -TaskName 'Borealis Agent (UserHelper)' -Confirm:$false -ErrorAction SilentlyContinue } catch {}

    # SYSTEM startup task
    # Use a wrapper PowerShell to enforce WorkingDirectory and capture stdout/stderr
    $sysArg     = ('-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "{0}"' -f $svcWrapper)
    $sysAction  = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $sysArg -WorkingDirectory (Split-Path $svcWrapper -Parent)
    $sysTrigger = New-ScheduledTaskTrigger -AtStartup
    $sysSet     = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -Hidden -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
    $sysPrin    = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
    Register-ScheduledTask -TaskName 'Borealis Agent' -Action $sysAction -Trigger $sysTrigger -Settings $sysSet -Principal $sysPrin -Force | Out-Null
    try { Start-ScheduledTask -TaskName 'Borealis Agent' | Out-Null } catch {}

    # Optional user-session helper for interactive roles (tray, overlays)
    $helperName = 'Borealis Agent (UserHelper)'
    $usrArg     = ('"{0}" --config CURRENTUSER' -f $agentPy)
    $usrAction  = New-ScheduledTaskAction -Execute $pyw -Argument $usrArg -WorkingDirectory (Split-Path $agentPy -Parent)
    $usrTrig    = New-ScheduledTaskTrigger -AtLogOn
    $usrSet     = New-ScheduledTaskSettingsSet -Hidden -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
    $currentUser= [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
    $usrPrin    = New-ScheduledTaskPrincipal -UserId $currentUser -LogonType Interactive -RunLevel Limited
    Register-ScheduledTask -TaskName $helperName -Action $usrAction -Trigger $usrTrig -Settings $usrSet -Principal $usrPrin -Force | Out-Null
    try { Start-ScheduledTask -TaskName $helperName | Out-Null } catch {}
}
function InstallOrUpdate-BorealisAgent {
    Write-Host "Ensuring Agent Dependencies Exist..." -ForegroundColor DarkCyan
    Install_Shared_Dependencies
    Install_Agent_Dependencies
    if (-not (Test-Path $pythonExe)) {
        Write-Host "`r$($symbols.Fail) Bundled Python not found at '$pythonExe'." -ForegroundColor Red
        exit 1
    }
    $env:PATH = '{0};{1}' -f (Split-Path $pythonExe), $env:PATH
    Write-Host "Cleaning previous agent tasks/processes..." -ForegroundColor Yellow
    Remove-BorealisServicesAndTasks -LogName 'Install.log'
    Ensure-SystemUtf8CodePage -LogName 'Install.log'
    Write-Host "Deploying Borealis Agent..." -ForegroundColor Blue

    # Resolve all paths relative to the script directory to avoid CWD issues
    $venvFolderPath         = Join-Path $scriptDir 'Agent'
    $agentSourceRoot        = Join-Path $scriptDir 'Data\Agent'
    $agentSourcePath        = Join-Path $agentSourceRoot 'agent.py'
    $agentRequirements      = Join-Path $agentSourceRoot 'agent-requirements.txt'
    $agentDestinationFolder = Join-Path $venvFolderPath 'Borealis'
    $agentDestinationFile   = Join-Path $agentDestinationFolder 'agent.py'
    $venvPython             = Join-Path $venvFolderPath 'Scripts\python.exe'
    $existingServerUrl      = $null

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
                Write-Host "Python not found. Install Python or run Server setup (option 1)." -ForegroundColor Red
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
            # Cleanup Previous Agent Folder & Create New Folder
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
            Remove-Item $agentDestinationFolder -Recurse -Force -ErrorAction SilentlyContinue
            New-Item -Path $agentDestinationFolder -ItemType Directory -Force | Out-Null

            # Copy Agent Files to Virtual Python Environment
            $coreAgentFiles = @(
                (Join-Path $agentSourceRoot 'Python_API_Endpoints'),
                (Join-Path $agentSourceRoot 'Roles'),
                (Join-Path $agentSourceRoot 'Scripts'),
                (Join-Path $agentSourceRoot 'agent_deployment.py'),
                (Join-Path $agentSourceRoot 'agent.py'),
                (Join-Path $agentSourceRoot 'ansible-ee-version.txt'),
                (Join-Path $agentSourceRoot 'Borealis.ico'),
                (Join-Path $agentSourceRoot 'fcntl_stub.py'),
                (Join-Path $agentSourceRoot 'launch_service.ps1'),
                (Join-Path $agentSourceRoot 'role_manager.py'),
                (Join-Path $agentSourceRoot 'security.py'),
                (Join-Path $agentSourceRoot 'signature_utils.py'),
                (Join-Path $agentSourceRoot 'sitecustomize.py'),
                (Join-Path $agentSourceRoot 'termios_stub.py')
            )

            Copy-Item $coreAgentFiles -Destination $agentDestinationFolder -Recurse -Force
            
            # Ensure ReverseTunnel role is refreshed explicitly (covers incremental changes)
            $rtSource = Join-Path $agentSourceRoot 'Roles\ReverseTunnel'
            $rtDest = Join-Path $agentDestinationFolder 'Roles'
            if (Test-Path $rtSource) {
                Copy-Item $rtSource -Destination $rtDest -Recurse -Force
            }
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

    Run-Step "Provision Ansible Execution Environment" {
        Ensure-AnsibleExecutionEnvironment `
            -ProjectRoot $scriptDir `
            -PythonBootstrapExe $pythonExe `
            -RequirementsPath $ansibleEeRequirementsPath `
            -ExpectedVersion $script:AnsibleExecutionEnvironmentVersion `
            -LogName 'Install.log'
    }

    Run-Step "Configure Agent Settings" {
        $settingsDir = Join-Path $scriptDir 'Agent\Borealis\Settings'
        $oldSettingsDir = Join-Path $scriptDir 'Agent\Settings'
        if (-not (Test-Path $settingsDir)) { New-Item -Path $settingsDir -ItemType Directory -Force | Out-Null }
        $serverUrlPath = Join-Path $settingsDir 'server_url.txt'
        $configPath = Join-Path $settingsDir 'agent_settings.json'
        # Migrate any prior interim location file if present
        $oldServerUrlPath = Join-Path $oldSettingsDir 'server_url.txt'
        if (-not (Test-Path $serverUrlPath) -and (Test-Path $oldServerUrlPath)) {
            try { Move-Item -Path $oldServerUrlPath -Destination $serverUrlPath -Force } catch { try { Copy-Item $oldServerUrlPath $serverUrlPath -Force } catch {} }
        }
        $defaultUrl = 'https://localhost:5000'
        $currentUrl = $defaultUrl
        if ($existingServerUrl -and $existingServerUrl.Trim()) {
            $currentUrl = $existingServerUrl.Trim()
        } elseif (Test-Path $serverUrlPath) {
            try { $txt = (Get-Content -Path $serverUrlPath -ErrorAction SilentlyContinue | Select-Object -First 1) } catch { $txt = '' }
            if ($txt -and $txt.Trim()) { $currentUrl = $txt.Trim() }
        }
        Write-Host ""; Write-Host "Set Borealis Server URL" -ForegroundColor DarkYellow
        $prompt = "Server URL [$currentUrl]"
        $inputUrl = Read-Host $prompt
        if (-not $inputUrl) { $inputUrl = $currentUrl }
        $inputUrl = $inputUrl.Trim()
        if (-not $inputUrl) { $inputUrl = $defaultUrl }
        
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

        $config['enrollment_code'] = $providedEnrollmentCode
        # Retain legacy key to avoid breaking existing agent readers
        $config['installer_code'] = $providedEnrollmentCode

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
    }

    Write-Host "`nConfiguring Borealis Agent (tasks)..." -ForegroundColor Blue
    Write-Host "===================================================================================="
    Ensure-AgentTasks -ScriptRoot $scriptDir
    if ($script:Utf8CodePageChanged) {
        $msg = 'System code pages set to UTF-8. A reboot is required before Ansible can run.'
        Write-AgentLog -FileName 'Install.log' -Message ("[UTF8] {0}" -f $msg)
        Write-Host "`n$msg" -ForegroundColor Yellow
    }
}

# ---------------------- Main -----------------------
Clear-Host
@'
:::::::::   ::::::::  :::::::::  ::::::::::     :::     :::        ::::::::::: :::::::: 
:+:    :+: :+:    :+: :+:    :+: :+:          :+: :+:   :+:            :+:    :+:    :+:
+:+    +:+ +:+    +:+ +:+    +:+ +:+         +:+   +:+  +:+            +:+    +:+       
+#++:++#+  +#+    +:+ +#++:++#:  +#++:++#   +#++:++#++: +#+            +#+    +#++:++#++
+#+    +#+ +#+    +#+ +#+    +#+ +#+        +#+     +#+ +#+            +#+           +#+
#+#    #+# #+#    #+# #+#    #+# #+#        #+#     #+# #+#            #+#    #+#    #+#
#########   ########  ###    ### ########## ###     ### ########## ########### ######## 
'@ | Write-Host -ForegroundColor DarkCyan
@'
____ _  _ ___ ____ _  _ ____ ___ _ ____ _  _    ___  _    ____ ___ ____ ____ ____ _  _
|__| |  |  |  |  | |\/| |__|  |  | |  | |\ |    |__] |    |__|  |  |___ |  | |__/ |\/|
|  | |__|  |  |__| |  | |  |  |  | |__| | \|    |    |___ |  |  |  |    |__| |  \ |  |
'@ | Write-Host -ForegroundColor DarkGray

if (-not $choice) {
    Write-Host " "
    Write-Host "Please choose which function you want to launch:"
    Write-Host " 1) Borealis Engine" -ForegroundColor DarkGray
    Write-Host " 2) Borealis Agent" -ForegroundColor DarkGray
    Write-Host "Type a number and press " -NoNewLine
    Write-Host "<ENTER>" -ForegroundColor DarkCyan
    $choice = Read-Host
}

switch ($choice) {
    "1" {
        $host.UI.RawUI.WindowTitle = "Borealis Engine"
        Write-Host "Ensuring Engine Dependencies Exist..." -ForegroundColor DarkCyan

        Install_Shared_Dependencies
        Install_Server_Dependencies

        foreach ($tool in @($pythonExe, $nodeExe, $npmCmd, $npxCmd)) {
            if (-not (Test-Path $tool)) {
                Write-Host "`r$($symbols.Fail) Bundled executable not found at '$tool'." -ForegroundColor Red
                exit 1
            }
        }
        $nodeDir = Split-Path $nodeExe
        $env:BOREALIS_NODE_DIR = $nodeDir
        $env:BOREALIS_NPM_CMD  = $npmCmd
        $env:BOREALIS_NPX_CMD  = $npxCmd
        $env:PATH = '{0};{1};{2}' -f (Split-Path $pythonExe), $nodeDir, $env:PATH

        if (-not $engineModeChoice) {
            Write-Host " "
            Write-Host "Configure Borealis Engine Mode:" -ForegroundColor DarkYellow
            Write-Host " 1) Build & Launch > Production Flask Server @ https://localhost:5000" -ForegroundColor DarkCyan
            Write-Host " 2) [Skip Build] & Immediately Launch > Production Flask Server @ https://localhost:5000" -ForegroundColor DarkCyan
            Write-Host " 3) Launch > [Hotload-Ready] Vite Dev Server @ http://localhost:5173" -ForegroundColor DarkCyan
            $engineModeChoice = Read-Host "Enter choice [1/2/3]"
        } else {
            Write-Host "Auto-selecting Borealis Engine mode option $engineModeChoice." -ForegroundColor DarkYellow
        }

        $engineOperationMode = "production"
        $engineImmediateLaunch = $false
        switch ($engineModeChoice) {
            "1" { $engineOperationMode = "production" }
            "2" { $engineImmediateLaunch = $true }
            "3" { $engineOperationMode = "developer" }
            default {
                Write-Host "Invalid mode choice: $engineModeChoice" -ForegroundColor Red
                break
            }
        }

        if ($engineImmediateLaunch) {
            $webUiSourceRoot = Join-Path $scriptDir 'Data\Engine\web-interface'
            $webUiBuildRoot  = Join-Path $scriptDir 'Engine\web-interface\build'
            $webUiFresh = Test-WebUiBuildFresh -SourceRoot $webUiSourceRoot -BuildRoot $webUiBuildRoot
            if (-not $webUiFresh) {
                Write-Host "Detected WebUI changes newer than the last production build. Running full build instead of Quick/Skip." -ForegroundColor Yellow
                $engineImmediateLaunch = $false
            }
        }

        if ($engineModeChoice -notin @('1','2','3')) {
            break
        }

        if ($engineImmediateLaunch) {
            Run-Step "Borealis Engine: Launch Flask Server" {
                Push-Location (Join-Path $scriptDir "Engine")
                $py = Join-Path $scriptDir "Engine\Scripts\python.exe"
                $previousEngineMode = $env:BOREALIS_ENGINE_MODE
                $previousEnginePort = $env:BOREALIS_ENGINE_PORT
                $previousProjectRoot = $env:BOREALIS_PROJECT_ROOT
                $env:BOREALIS_ENGINE_MODE = $engineOperationMode
                $env:BOREALIS_ENGINE_PORT = "5000"
                $env:BOREALIS_PROJECT_ROOT = $scriptDir
                Write-Host "`nLaunching Borealis Engine..." -ForegroundColor Green
                Write-Host "===================================================================================="
                Write-Host "$($symbols.Running) Engine Socket Server Started..."
                & $py -m Data.Engine.bootstrapper
                if ($previousEngineMode) { $env:BOREALIS_ENGINE_MODE = $previousEngineMode } else { Remove-Item Env:BOREALIS_ENGINE_MODE -ErrorAction SilentlyContinue }
                if ($previousEnginePort) { $env:BOREALIS_ENGINE_PORT = $previousEnginePort } else { Remove-Item Env:BOREALIS_ENGINE_PORT -ErrorAction SilentlyContinue }
                if ($previousProjectRoot) { $env:BOREALIS_PROJECT_ROOT = $previousProjectRoot } else { Remove-Item Env:BOREALIS_PROJECT_ROOT -ErrorAction SilentlyContinue }
                Pop-Location
            }
            break
        }

        Write-Host "Deploying Borealis Engine in '$engineOperationMode' mode" -ForegroundColor Blue

        $venvFolder            = "Engine"
        $dataSource            = "Data"
        $engineSource          = "$dataSource\Engine"
        $engineDataDestination = "$venvFolder\Data\Engine"
        $webUIDestination      = "$venvFolder\web-interface"
        $venvPython            = Join-Path $venvFolder 'Scripts\python.exe'
        $engineSourceAbsolute  = Join-Path $scriptDir $engineSource

        Run-Step "Create Borealis Engine Virtual Python Environment" {
            $venvActivate = Join-Path $venvFolder 'Scripts\Activate'
            $pyvenvCfg = Join-Path $venvFolder 'pyvenv.cfg'
            $expectedPython = $pythonExe
            $expectedPythonNorm = $null
            $expectedHomeNorm = $null
            try {
                if (Test-Path $pythonExe -PathType Leaf) {
                    $expectedPython = (Resolve-Path $pythonExe -ErrorAction Stop).ProviderPath
                }
            } catch {
                $expectedPython = $pythonExe
            }
            if ($expectedPython) {
                $expectedPythonNorm = $expectedPython.ToLowerInvariant()
                try {
                    $expectedHome = Split-Path -Path $expectedPython -Parent
                } catch {
                    $expectedHome = $null
                }
                if ($expectedHome) {
                    $expectedHomeNorm = $expectedHome.ToLowerInvariant()
                }
            }

            $venvNeedsUpgrade = $false
            if (Test-Path $pyvenvCfg -PathType Leaf) {
                try {
                    $cfgLines = Get-Content -Path $pyvenvCfg -ErrorAction Stop
                    $cfgMap = @{}
                    foreach ($line in $cfgLines) {
                        $trimmed = $line.Trim()
                        if (-not $trimmed -or $trimmed.StartsWith('#')) {
                            continue
                        }
                        $parts = $trimmed -split '=', 2
                        if ($parts.Count -ne 2) {
                            continue
                        }
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
                            try {
                                $resolvedExe = (Resolve-Path $cfgExecutable -ErrorAction Stop).ProviderPath
                            } catch {
                                $resolvedExe = $cfgExecutable
                            }
                            if ($resolvedExe) {
                                $resolvedExeNorm = $resolvedExe.ToLowerInvariant()
                            } else {
                                $resolvedExeNorm = $null
                            }
                            if ($resolvedExeNorm -and $resolvedExeNorm -ne $expectedPythonNorm) {
                                $venvNeedsUpgrade = $true
                            }
                        }
                        if (-not $venvNeedsUpgrade -and $cfgHome -and $expectedHomeNorm) {
                            try {
                                $resolvedHome = (Resolve-Path $cfgHome -ErrorAction Stop).ProviderPath
                            } catch {
                                $resolvedHome = $cfgHome
                            }
                            if ($resolvedHome) {
                                $resolvedHomeNorm = $resolvedHome.ToLowerInvariant()
                            } else {
                                $resolvedHomeNorm = $null
                            }
                            if ($resolvedHomeNorm -and $resolvedHomeNorm -ne $expectedHomeNorm) {
                                $venvNeedsUpgrade = $true
                            }
                        }
                    }
                } catch {
                    $venvNeedsUpgrade = $true
                }
            }

            if (-not (Test-Path $venvActivate)) {
                & $pythonExe -m venv $venvFolder | Out-Null
            } elseif ($venvNeedsUpgrade) {
                Write-Host "Detected relocated Engine virtual environment. Rebuilding interpreter bindings..." -ForegroundColor Yellow
                & $pythonExe -m venv --upgrade $venvFolder | Out-Null
            }

            $engineDataRoot = Join-Path $venvFolder 'Data'
            if (-not (Test-Path $engineDataRoot)) {
                New-Item -Path $engineDataRoot -ItemType Directory -Force | Out-Null
            }

            $engineDataAbsolute = Join-Path $scriptDir $engineDataDestination

            $runtimeAssemblies = Join-Path $scriptDir 'Engine\Assemblies'
            $sourceAssemblies  = Join-Path $engineSourceAbsolute 'Assemblies'

            $runtimeDatabase   = Join-Path $scriptDir 'Engine\database.db'

            $runtimeAuthTokens = Join-Path $scriptDir 'Engine\Auth_Tokens'

            if (Test-Path $engineDataAbsolute) {
                Remove-Item $engineDataAbsolute -Recurse -Force -ErrorAction SilentlyContinue
            }
            New-Item -Path $engineDataAbsolute -ItemType Directory -Force | Out-Null

            if (-not (Test-Path $engineSourceAbsolute)) {
                throw "Engine source directory '$engineSourceAbsolute' not found."
            }
            Get-ChildItem -Path $engineSourceAbsolute -Force | ForEach-Object {
                if ($_.Name -ieq 'Assemblies') {
                    return
                }
                Copy-Item -Path $_.FullName -Destination $engineDataAbsolute -Recurse -Force
            }

            if (-not (Test-Path $runtimeAssemblies) -and (Test-Path $sourceAssemblies)) {
                Copy-Item -Path $sourceAssemblies -Destination $runtimeAssemblies -Recurse -Force
            } elseif (-not (Test-Path $runtimeAssemblies)) {
                New-Item -Path $runtimeAssemblies -ItemType Directory -Force | Out-Null
            }

            if (-not (Test-Path $runtimeAuthTokens)) {
                New-Item -Path $runtimeAuthTokens -ItemType Directory -Force | Out-Null
            }

            if (-not (Test-Path $runtimeDatabase)) {
                $runtimeDatabaseDir = Split-Path -Path $runtimeDatabase -Parent
                if (-not (Test-Path $runtimeDatabaseDir)) {
                    New-Item -Path $runtimeDatabaseDir -ItemType Directory -Force | Out-Null
                }
            }

            . (Join-Path $venvFolder 'Scripts\Activate')
        }

        Run-Step "Install Engine Python Dependencies into Virtual Python Environment" {
            $engineRequirements = @(
                (Join-Path $engineSourceAbsolute 'engine-requirements.txt'),
                (Join-Path $engineSourceAbsolute 'requirements.txt')
            )
            $requirementsPath = $engineRequirements | Where-Object { Test-Path $_ } | Select-Object -First 1
            if ($requirementsPath) {
                & $venvPython -m pip install --disable-pip-version-check -q -r $requirementsPath | Out-Null
            }
        }

        Run-Step "Copy Borealis Engine WebUI Files into: $webUIDestination" {
            Ensure-EngineWebInterface -ProjectRoot $scriptDir
            $webUIDestinationAbsolute = Join-Path $scriptDir $webUIDestination
            if (-not (Test-Path (Join-Path $webUIDestinationAbsolute 'package.json'))) {
                throw "Failed to stage Engine web interface into '$webUIDestinationAbsolute'."
            }
        }

        Run-Step "Vite Web Frontend: Install NPM Packages" {
            $webUIDestinationAbsolute = Join-Path $scriptDir $webUIDestination
            if (Test-Path $webUIDestinationAbsolute) {
                Push-Location $webUIDestinationAbsolute
                try {
                    $env:npm_config_loglevel = "silent"
                    & $npmCmd install --silent --no-fund --audit=false *> $null
                    if ($LASTEXITCODE -ne 0) {
                        throw "npm install exited with code $LASTEXITCODE"
                    }
                } finally {
                    Pop-Location
                }
            } else {
                Write-Host "Web interface destination '$webUIDestinationAbsolute' not found." -ForegroundColor Yellow
                throw "Web interface destination missing; cannot install npm packages."
            }
        }

        Run-Step "Vite Web Frontend: Start ($engineOperationMode)" {
            $webUIDestinationAbsolute = Join-Path $scriptDir $webUIDestination
            if (-not (Test-Path $webUIDestinationAbsolute)) {
                Write-ViteLog "WebUI destination missing at '$webUIDestinationAbsolute'; skipping Vite start."
                return
            }

            Push-Location $webUIDestinationAbsolute
            try {
                Ensure-EngineTlsMaterial -PythonPath $venvPython
                $requiredTlsFiles = @($env:BOREALIS_TLS_CERT, $env:BOREALIS_TLS_KEY, $env:BOREALIS_TLS_BUNDLE)
                foreach ($tlsFile in $requiredTlsFiles) {
                    if ([string]::IsNullOrWhiteSpace($tlsFile) -or -not (Test-Path $tlsFile)) {
                        Write-ViteLog "TLS artifact missing or unreadable: '$tlsFile'"
                        throw "Unable to locate Borealis TLS material needed for Vite."
                    }
                }
                $tlsSummary = "cert=$env:BOREALIS_TLS_CERT bundle=$env:BOREALIS_TLS_BUNDLE"

                if ($engineOperationMode -eq "developer") {
                    $engineLogDir = Ensure-EngineLogDir
                    $viteStdOut = Join-Path $engineLogDir 'vite-dev.stdout.log'
                    $viteStdErr = Join-Path $engineLogDir 'vite-dev.stderr.log'
                    foreach ($logPath in @($viteStdOut, $viteStdErr)) {
                        if (Test-Path $logPath) {
                            $archivePath = '{0}.{1}' -f $logPath, (Get-Date).ToString('yyyyMMddHHmmss')
                            Move-Item -Path $logPath -Destination $archivePath -Force
                            Write-ViteLog ("Archived previous {0} -> {1}" -f (Split-Path $logPath -Leaf), (Split-Path $archivePath -Leaf))
                        }
                    }

                    $nodeDirForVite = Split-Path $nodeExe -ErrorAction SilentlyContinue
                    $localBin = Join-Path $webUIDestinationAbsolute 'node_modules\.bin'
                    foreach ($candidate in @($nodeDirForVite, $localBin)) {
                        if ([string]::IsNullOrWhiteSpace($candidate)) {
                            continue
                        }
                        if (-not (Test-Path $candidate)) {
                            continue
                        }
                        $pathParts = $env:PATH -split [System.IO.Path]::PathSeparator
                        if ($pathParts -notcontains $candidate) {
                            $env:PATH = "$candidate$([System.IO.Path]::PathSeparator)$env:PATH"
                            Write-ViteLog "Appended '$candidate' to PATH for Vite session."
                        }
                    }

                    Write-ViteLog "Starting Vite dev server from '$webUIDestinationAbsolute' using TLS ($tlsSummary)."
                    Write-ViteLog "npm CLI: $npmCmd"
                    $startInfoArgs = @('run', 'dev')
                    try {
                        $viteProcess = Start-Process -FilePath $npmCmd `
                                                     -ArgumentList $startInfoArgs `
                                                     -WorkingDirectory $webUIDestinationAbsolute `
                                                     -RedirectStandardOutput $viteStdOut `
                                                     -RedirectStandardError $viteStdErr `
                                                     -NoNewWindow -PassThru
                        Write-ViteLog ("Spawned npm run dev (PID {0}); streaming to {1} / {2}" -f $viteProcess.Id, (Split-Path $viteStdOut -Leaf), (Split-Path $viteStdErr -Leaf))
                        Start-Sleep -Seconds 2
                        if ($viteProcess.HasExited) {
                            $stderrTail = ''
                            if (Test-Path $viteStdErr) {
                                $stderrTail = (Get-Content $viteStdErr -Tail 20) -join ' | '
                            }
                            Write-ViteLog ("npm run dev exited with code {0}. stderr tail: {1}" -f $viteProcess.ExitCode, $stderrTail)
                            throw "Vite dev server failed to start. Review $viteStdErr for details."
                        } else {
                            Write-ViteLog "Vite dev server is listening on https://localhost:5173 (PID $($viteProcess.Id))."
                        }
                    } catch {
                        Write-ViteLog ("Failed to launch npm run dev: {0}" -f $_.Exception.Message)
                        throw
                    }
                } else {
                    Write-ViteLog "Executing npm run build for production WebUI assets."
                    & $npmCmd run build
                }
            } finally {
                Pop-Location
            }
        }

        Run-Step "Borealis Engine: Launch Flask Server" {
            Push-Location (Join-Path $scriptDir "Engine")
            $py = Join-Path $scriptDir "Engine\Scripts\python.exe"
            $previousEngineMode = $env:BOREALIS_ENGINE_MODE
            $previousEnginePort = $env:BOREALIS_ENGINE_PORT
            $previousProjectRoot = $env:BOREALIS_PROJECT_ROOT
            $env:BOREALIS_ENGINE_MODE = $engineOperationMode
            $env:BOREALIS_ENGINE_PORT = "5000"
            $env:BOREALIS_PROJECT_ROOT = $scriptDir
            Write-Host "`nLaunching Borealis Engine..." -ForegroundColor Green
            Write-Host "===================================================================================="
            Write-Host "$($symbols.Running) Engine Socket Server Started..."
            & $py -m Data.Engine.bootstrapper
            if ($previousEngineMode) { $env:BOREALIS_ENGINE_MODE = $previousEngineMode } else { Remove-Item Env:BOREALIS_ENGINE_MODE -ErrorAction SilentlyContinue }
            if ($previousEnginePort) { $env:BOREALIS_ENGINE_PORT = $previousEnginePort } else { Remove-Item Env:BOREALIS_ENGINE_PORT -ErrorAction SilentlyContinue }
            if ($previousProjectRoot) { $env:BOREALIS_PROJECT_ROOT = $previousProjectRoot } else { Remove-Item Env:BOREALIS_PROJECT_ROOT -ErrorAction SilentlyContinue }
            Pop-Location
        }
    }
    
    "2" {
        $host.UI.RawUI.WindowTitle = "Borealis Agent"
        Write-Host " "
        # Ensure elevation before performing Agent deployment
        $scriptPath = $PSCommandPath
        if (-not $scriptPath -or $scriptPath -eq '') { $scriptPath = $MyInvocation.MyCommand.Definition }
        # If already elevated, skip prompt; otherwise prompt, then relaunch directly to the Agent deploy flow via -Agent
        $cont = Request-AgentElevation -ScriptPath $scriptPath
        if (-not $cont -and -not (Test-IsAdmin)) { return }
        if (Test-IsAdmin) {
            Write-Host "Escalated Permissions Granted > Agent is Eligible for Deployment." -ForegroundColor Green
        }
        Write-Host "Deploying Borealis Agent (fresh install/update path)..." -ForegroundColor Cyan
        InstallOrUpdate-BorealisAgent
        break
    }
    default { Write-Host "Invalid selection. Exiting..." -ForegroundColor Red; exit 1 }
}
