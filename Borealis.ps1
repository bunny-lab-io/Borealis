#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Borealis.ps1

[CmdletBinding()]
param(
    [switch]$Server,
    [switch]$Agent,
    [ValidateSet('install','repair','remove','launch','')]
    [string]$AgentAction = '',
    [switch]$Vite,
    [switch]$Flask,
    [switch]$Quick,
    [switch]$EngineTests,
    [string]$InstallerCode = ''
)

# Preselect menu choices from CLI args (optional)
$choice = $null
$modeChoice = $null
$agentSubChoice = $null

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

if ($Server) {
    # Auto-select main menu option for Server when -Server flag is provided
    $choice = '1'
} elseif ($Agent) {
    $choice = '2'
    switch ($AgentAction) {
        'install' { $agentSubChoice = '1' }
        'repair'  { $agentSubChoice = '2' }
        'remove'  { $agentSubChoice = '3' }
        'launch'  { $agentSubChoice = '4' }
        default   { $agentSubChoice = '1' }
    }
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
        [string]$AgentActionParam,
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
    if ($AgentActionParam -and $AgentActionParam.Trim()) {
        $args += @('-AgentAction', $AgentActionParam)
    }
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
    $logRoot = Join-Path $scriptDir 'Logs'
    $agentLogDir = Join-Path $logRoot 'Agent'
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

function Ensure-EngineTlsMaterial {
    param(
        [string]$PythonPath,
        [string]$CertificateRoot
    )

    if (-not (Test-Path $CertificateRoot)) {
        New-Item -Path $CertificateRoot -ItemType Directory -Force | Out-Null
    }

    if (Test-Path $PythonPath) {
        $code = @'
from Data.Engine.services.crypto import certificates
certificates.ensure_certificate()
'@
        try {
            & $PythonPath -c $code | Out-Null
        } catch {
            Write-Host "Failed to pre-generate Engine TLS certificates: $($_.Exception.Message)" -ForegroundColor Yellow
        }
    }

    $env:BOREALIS_CERT_DIR   = $CertificateRoot
    $env:BOREALIS_TLS_CERT   = Join-Path $CertificateRoot 'borealis-server-cert.pem'
    $env:BOREALIS_TLS_KEY    = Join-Path $CertificateRoot 'borealis-server-key.pem'
    $env:BOREALIS_TLS_BUNDLE = Join-Path $CertificateRoot 'borealis-server-bundle.pem'
}

function Ensure-EngineWebInterface {
    param(
        [string]$ProjectRoot
    )

    $engineSource = Join-Path $ProjectRoot 'Engine\web-interface'
    $legacySource = Join-Path $ProjectRoot 'Data\Server\WebUI'

    if (-not (Test-Path $legacySource)) {
        return
    }

    $enginePackage = Join-Path $engineSource 'package.json'
    $engineSrcDir  = Join-Path $engineSource 'src'
    if ((Test-Path $enginePackage) -and (Test-Path $engineSrcDir)) {
        return
    }

    if (-not (Test-Path $engineSource)) {
        New-Item -Path $engineSource -ItemType Directory -Force | Out-Null
    }

    $preserve = @('.gitignore','README.md')
    Get-ChildItem -Path $engineSource -Force | Where-Object { $preserve -notcontains $_.Name } |
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue

    Copy-Item (Join-Path $legacySource '*') $engineSource -Recurse -Force

    if (-not (Test-Path $enginePackage)) {
        throw "Failed to stage Engine web interface into '$engineSource'."
    }
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

# Repair routine: cleans services, ensures venv files, reinstalls and starts BorealisAgent
function Repair-BorealisAgent {
    $logName = 'Repair.log'
    Write-AgentLog -FileName $logName -Message "=== Repair start ==="
    Remove-BorealisServicesAndTasks -LogName $logName
    InstallOrUpdate-BorealisAgent
    Write-AgentLog -FileName $logName -Message "=== Repair end ==="
}

function Remove-BorealisAgent {
    $logName = 'Removal.log'
    Write-AgentLog -FileName $logName -Message "=== Removal start ==="
    Remove-BorealisServicesAndTasks -LogName $logName
    # Kill stray helpers
    Write-AgentLog -FileName $logName -Message "Terminating stray helper processes"
    Get-Process python,pythonw -ErrorAction SilentlyContinue | Where-Object { $_.Path -like (Join-Path $scriptDir 'Agent\*') } | ForEach-Object {
        try { $_ | Stop-Process -Force } catch {}
    }
    # Remove venv folder
    $venvFolder = Join-Path $scriptDir 'Agent'
    Write-AgentLog -FileName $logName -Message "Removing folder: $venvFolder"
    try { Remove-Item $venvFolder -Recurse -Force -ErrorAction SilentlyContinue } catch {}
    Write-AgentLog -FileName $logName -Message "=== Removal end ==="
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
        $tessInstallDir = Join-Path $scriptDir "Data\Server\Python_API_Endpoints\Tesseract-OCR"

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
        $langDataDir = Join-Path $scriptDir "Data\Server\Python_API_Endpoints\Tesseract-OCR\tessdata"
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
        if (-not (Test-Path (Join-Path $venvFolderPath 'Scripts\Activate'))) {
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
            & $pythonForVenv -m venv $venvFolderPath
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

        $existingInstallerCode = ''
        if ('installer_code' -in $config.Keys -and $null -ne $config['installer_code']) {
            $existingInstallerCode = [string]$config['installer_code']
        }

        $providedInstallerCode = ''
        if ($InstallerCode -and $InstallerCode.Trim()) {
            $providedInstallerCode = $InstallerCode.Trim()
        } elseif ($env:BOREALIS_INSTALLER_CODE -and $env:BOREALIS_INSTALLER_CODE.Trim()) {
            $providedInstallerCode = $env:BOREALIS_INSTALLER_CODE.Trim()
        }

        if (-not $providedInstallerCode) {
            $defaultDisplay = if ($existingInstallerCode) { $existingInstallerCode } else { '' }
            Write-Host ""; Write-Host "Optional: set an installer code for agent enrollment." -ForegroundColor DarkYellow
            $inputCode = Read-Host ("Installer Code [{0}]" -f $defaultDisplay)
            if ($inputCode -and $inputCode.Trim()) {
                $providedInstallerCode = $inputCode.Trim()
            } elseif ($defaultDisplay) {
                $providedInstallerCode = $defaultDisplay
            } else {
                $providedInstallerCode = ''
            }
        }

        $config['installer_code'] = $providedInstallerCode

        try {
            $configJson = $config | ConvertTo-Json -Depth 10
            [System.IO.File]::WriteAllText($configPath, $configJson, $utf8NoBom)
            if ($providedInstallerCode) {
                Write-Host "Installer code saved to agent_settings.json." -ForegroundColor Green
            } else {
                Write-Host "Installer code cleared in agent_settings.json." -ForegroundColor Yellow
            }
        } catch {
            Write-AgentLog -FileName 'Install.log' -Message ("[CONFIG] Failed to persist agent_settings.json: {0}" -f $_.Exception.Message)
            Write-Host "Failed to update agent_settings.json. Check Logs/Agent/install.log for details." -ForegroundColor Red
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
    Write-Host " 1) Borealis Server" -ForegroundColor DarkGray
    Write-Host " 2) Borealis Agent" -ForegroundColor DarkGray
    Write-Host " 3) Build Electron App " -NoNewline -ForegroundColor DarkGray
    Write-Host "[Experimental]" -ForegroundColor Red
    Write-Host " 4) Package Self-Contained EXE of Server or Agent " -NoNewline -ForegroundColor DarkGray
    Write-Host "[Experimental]" -ForegroundColor Red
    Write-Host " 5) Borealis Engine " -NoNewline -ForegroundColor DarkGray
    Write-Host "[In Development]" -ForegroundColor Cyan
    # (Removed) AutoHotKey experimental testing
    Write-Host "Type a number and press " -NoNewLine
    Write-Host "<ENTER>" -ForegroundColor DarkCyan
    $choice = Read-Host
}

switch ($choice) {
    "1" {
        $host.UI.RawUI.WindowTitle = "Borealis Server"
        Write-Host "Ensuring Server Dependencies Exist..." -ForegroundColor DarkCyan

        Install_Shared_Dependencies
        Install_Server_Dependencies

        foreach ($tool in @($pythonExe, $nodeExe, $npmCmd, $npxCmd)) {
            if (-not (Test-Path $tool)) { Write-Host "`r$($symbols.Fail) Bundled executable not found at '$tool'." -ForegroundColor Red; exit 1 }
        }
        $env:PATH = '{0};{1};{2}' -f (Split-Path $pythonExe), (Split-Path $nodeExe), $env:PATH

        if (-not $modeChoice) {
            Write-Host " "
            Write-Host "Configure Borealis Server Mode:" -ForegroundColor DarkYellow
            Write-Host " 1) Build & Launch > Production Flask Server @ http://localhost:5000" -ForegroundColor DarkCyan
            Write-Host " 2) [Skip Build] & Immediately Launch > Production Flask Server @ http://localhost:5000" -ForegroundColor DarkCyan
            Write-Host " 3) Launch > [Hotload-Ready] Vite Dev Server @ http://localhost:5173" -ForegroundColor DarkCyan
            $modeChoice = Read-Host "Enter choice [1/2/3]"
        }

        switch ($modeChoice) {
            "1" { $borealis_operation_mode = "production" }
            "2" {
                Run-Step "Borealis: Launch Flask Server" {
                    Push-Location (Join-Path $scriptDir "Server")
                    & (Join-Path $scriptDir "Server\Scripts\python.exe") (Join-Path $scriptDir "Server\Borealis\server.py")
                    Pop-Location
                }
                break
            }
            "3" { $borealis_operation_mode = "developer" }
            default { Write-Host "Invalid mode choice: $modeChoice" -ForegroundColor Red; break }
        }

        Write-Host "Deploying Borealis Server in '$borealis_operation_mode' mode" -ForegroundColor Blue

        $venvFolder       = "Server"
        $dataSource       = "Data"
        $dataDestination  = "$venvFolder\Borealis"
        $customUIPath     = (Join-Path $scriptDir 'Engine\web-interface')
        $webUIDestination = "$venvFolder\web-interface"
        $venvPython       = Join-Path $venvFolder 'Scripts\python.exe'

        Run-Step "Create Borealis Virtual Python Environment" {
            if (-not (Test-Path "$venvFolder\Scripts\Activate")) { & $pythonExe -m venv $venvFolder | Out-Null }
            if (Test-Path $dataSource) {
                $preserveItems = @('auth_keys','server_secret.key','cache')
                $preserveRoot = Join-Path $venvFolder '.__borealis_preserve'
                if (Test-Path $dataDestination) {
                    Remove-Item $preserveRoot -Recurse -Force -ErrorAction SilentlyContinue
                    New-Item -Path $preserveRoot -ItemType Directory -Force | Out-Null
                    foreach ($item in $preserveItems) {
                        $sourcePath = Join-Path $dataDestination $item
                        if (Test-Path $sourcePath) {
                            $targetPath = Join-Path $preserveRoot $item
                            $targetParent = Split-Path $targetPath -Parent
                            if (-not (Test-Path $targetParent)) {
                                New-Item -Path $targetParent -ItemType Directory -Force | Out-Null
                            }
                            Move-Item -Path $sourcePath -Destination $targetPath -Force
                        }
                    }
                    Remove-Item $dataDestination -Recurse -Force -ErrorAction SilentlyContinue
                }
                New-Item -Path $dataDestination -ItemType Directory -Force | Out-Null
                Copy-Item "$dataSource\Server\Python_API_Endpoints" $dataDestination -Recurse
                Copy-Item "$dataSource\Server\Sounds"               $dataDestination -Recurse
                Copy-Item "$dataSource\Server\Modules"               $dataDestination -Recurse
                Copy-Item "$dataSource\Server\server.py"            $dataDestination
                Copy-Item "$dataSource\Server\job_scheduler.py"     $dataDestination 
                if (Test-Path $preserveRoot) {
                    Get-ChildItem -Path $preserveRoot -Force | ForEach-Object {
                        $target = Join-Path $dataDestination $_.Name
                        if (Test-Path $target) {
                            Remove-Item $target -Recurse -Force -ErrorAction SilentlyContinue
                        }
                        Move-Item -Path $_.FullName -Destination $target -Force
                    }
                    Remove-Item $preserveRoot -Recurse -Force -ErrorAction SilentlyContinue
                }
            }
            . "$venvFolder\Scripts\Activate"
        }

        Run-Step "Install Python Dependencies into Virtual Python Environment" {
            if (Test-Path "$dataSource\Server\server-requirements.txt") { & $venvPython -m pip install --disable-pip-version-check -q -r "$dataSource\Server\server-requirements.txt" | Out-Null }
        }

        Run-Step "Copy Borealis WebUI Files into: $webUIDestination" {
            Ensure-EngineWebInterface -ProjectRoot $scriptDir
            if (Test-Path $webUIDestination) {
                Remove-Item "$webUIDestination\public\*" -Recurse -Force -ErrorAction SilentlyContinue
                Remove-Item "$webUIDestination\src\*"    -Recurse -Force -ErrorAction SilentlyContinue
            } else {
                New-Item -Path $webUIDestination -ItemType Directory -Force | Out-Null
            }
            if (-not (Test-Path $customUIPath)) { throw "Web interface source '$customUIPath' not found." }
            Copy-Item "$customUIPath\*" $webUIDestination -Recurse -Force
        }

        Run-Step "Vite Web Frontend: Install NPM Packages" {
            Push-Location $webUIDestination
            $env:npm_config_loglevel = "silent"
            & $npmCmd install --silent --no-fund --audit=false | Out-Null
            Pop-Location
        }

        Run-Step "Vite Web Frontend: Start ($borealis_operation_mode)" {
            Push-Location $webUIDestination
            if ($borealis_operation_mode -eq "developer") { $viteSubCommand = "dev" } else { $viteSubCommand = "build" }
            $certRoot = Join-Path $scriptDir 'Certificates\Server'
            Ensure-EngineTlsMaterial -PythonPath $venvPython -CertificateRoot $certRoot
            Start-Process -NoNewWindow -FilePath $npmCmd -ArgumentList @("run",$viteSubCommand)
            Pop-Location
        }

        Run-Step "Borealis: Launch Flask Server" {
            Push-Location (Join-Path $scriptDir "Server")
            $py        = Join-Path $scriptDir "Server\Scripts\python.exe"
            $server_py = Join-Path $scriptDir "Server\Borealis\server.py"
            Write-Host "`nLaunching Borealis..." -ForegroundColor Green
            Write-Host "===================================================================================="
            Write-Host "$($symbols.Running) Python Flask API Server Started..."
            & $py $server_py
            Pop-Location
        }
        break
    }

    "2" {
        $host.UI.RawUI.WindowTitle = "Borealis Agent"
        Write-Host " "
        # Ensure elevation before showing Agent menu
        $scriptPath = $PSCommandPath
        if (-not $scriptPath -or $scriptPath -eq '') { $scriptPath = $MyInvocation.MyCommand.Definition }
        # If already elevated, skip prompt; otherwise prompt, then relaunch directly to the Agent menu via -Agent
        $cont = Request-AgentElevation -ScriptPath $scriptPath -AgentActionParam $AgentAction
        if (-not $cont -and -not (Test-IsAdmin)) { return }
        if (Test-IsAdmin) {
            Write-Host "Escalated Permissions Granted > Agent is Eligible for Deployment." -ForegroundColor Green
        }
        Write-Host "Agent Menu:" -ForegroundColor Cyan
        Write-Host " 1) Install/Update Agent"
        Write-Host " 2) Repair Borealis Agent"
        Write-Host " 3) Remove Agent"
        Write-Host " 4) Launch UserSession Helper (current session)"
        Write-Host " 5) Back"
        if (-not $agentSubChoice) { $agentSubChoice = Read-Host "Select an option" }

        switch ($agentSubChoice) {
            '1' { InstallOrUpdate-BorealisAgent; break }
            '2' { Repair-BorealisAgent; break }
            '3' { Remove-BorealisAgent; break }
            '4' {
                $venvPythonw = Join-Path $scriptDir 'Agent\Scripts\pythonw.exe'
                $helper = Join-Path $scriptDir 'Agent\Borealis\agent.py'
                if (-not (Test-Path $venvPythonw)) { Write-Host "pythonw.exe not found under Agent\Scripts" -ForegroundColor Yellow }
                if (-not (Test-Path $helper)) { Write-Host "Helper not found under Agent\Borealis" -ForegroundColor Yellow }
                if ((Test-Path $venvPythonw) -and (Test-Path $helper)) {
                    Start-Process -FilePath $venvPythonw -ArgumentList @('-W','ignore::SyntaxWarning', $helper) -WorkingDirectory (Split-Path $helper -Parent)
                    Write-Host "Launched user-session helper." -ForegroundColor Green
                }
                break
            }
            default { break }
        }
    }

    "3" {
        $host.UI.RawUI.WindowTitle = "Borealis Electron"
        Clear-Host
        Write-Host "Deploying Borealis Desktop App..." -ForegroundColor Cyan
        Write-Host "===================================================================================="

        $electronSource      = "Data\Electron"
        $electronDestination = "ElectronApp"
        $scriptDir           = Split-Path $MyInvocation.MyCommand.Path -Parent

        Run-Step "Prepare ElectronApp folder" {
            if (Test-Path $electronDestination) { Remove-Item $electronDestination -Recurse -Force }
            New-Item -Path $electronDestination -ItemType Directory | Out-Null
            $deployedServer = Join-Path $scriptDir 'Server\Borealis'
            if (-not (Test-Path $deployedServer)) { throw "Server\Borealis not found - please run choice 1 first." }
            Copy-Item $deployedServer "$electronDestination\Server" -Recurse
            Copy-Item "$electronSource\package.json" "$electronDestination" -Force
            Copy-Item "$electronSource\main.js"        "$electronDestination" -Force
            $staticBuild = Join-Path $scriptDir 'Server\web-interface\build'
            if (-not (Test-Path $staticBuild)) { throw "WebUI build not found - run choice 1 to build WebUI first." }
            Copy-Item "$staticBuild\*" "$electronDestination\renderer" -Recurse -Force
        }

        Run-Step "ElectronApp: Install Node dependencies" {
            Push-Location $electronDestination
            $env:NODE_ENV = ''
            $env:npm_config_production = ''
            & $npmCmd install --silent --no-fund --audit=false
            Pop-Location
        }

        Run-Step "ElectronApp: Package with electron-builder" {
            Push-Location $electronDestination
            & $npmCmd run dist
            Pop-Location
        }

        Run-Step "ElectronApp: Launch in dev mode" {
            Push-Location $electronDestination
            & $npmCmd run dev
            Pop-Location
        }
    }

    "4" {
        $host.UI.RawUI.WindowTitle = "Borealis Packager"
        Write-Host "Choose which module to package into a self-contained EXE file:" -ForegroundColor DarkYellow
        Write-Host " 1) Server" -ForegroundColor DarkGray
        Write-Host " 2) Agent" -ForegroundColor DarkGray
        $exePackageChoice = Read-Host "Enter choice [1/2]"
        switch ($exePackageChoice) {
            "1" {
                $serverScriptDir = Join-Path -Path $PSScriptRoot -ChildPath "Data\Server"
                Set-Location -Path $serverScriptDir
                & (Join-Path -Path $serverScriptDir -ChildPath "Package-Borealis-Server.ps1")
            }
            "2" {
                $agentScriptDir = Join-Path -Path $PSScriptRoot -ChildPath "Data\Agent"
                Set-Location -Path $agentScriptDir
                & (Join-Path -Path $agentScriptDir -ChildPath "Package_Borealis-Agent.ps1")
            }
            default { Write-Host "Invalid Choice. Exiting..." -ForegroundColor Red; exit 1 }
        }
    }

    "5" {
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
        $env:PATH = '{0};{1};{2}' -f (Split-Path $pythonExe), (Split-Path $nodeExe), $env:PATH

        Write-Host " "
        Write-Host "Configure Borealis Engine Mode:" -ForegroundColor DarkYellow
        Write-Host " 1) Build & Launch > Production Flask Server @ http://localhost:5001" -ForegroundColor DarkCyan
        Write-Host " 2) [Skip Build] & Immediately Launch > Production Flask Server @ http://localhost:5001" -ForegroundColor DarkCyan
        Write-Host " 3) Launch > [Hotload-Ready] Vite Dev Server @ http://localhost:5173" -ForegroundColor DarkCyan
        $engineModeChoice = Read-Host "Enter choice [1/2/3]"

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

        if ($engineModeChoice -notin @('1','2','3')) {
            break
        }

        if ($engineImmediateLaunch) {
            Run-Step "Borealis Engine: Launch Flask Server" {
                Push-Location (Join-Path $scriptDir "Engine")
                $py = Join-Path $scriptDir "Engine\Scripts\python.exe"
                $previousEngineMode = $env:BOREALIS_ENGINE_MODE
                $previousEnginePort = $env:BOREALIS_ENGINE_PORT
                $env:BOREALIS_ENGINE_MODE = $engineOperationMode
                $env:BOREALIS_ENGINE_PORT = "5001"
                Write-Host "`nLaunching Borealis Engine..." -ForegroundColor Green
                Write-Host "===================================================================================="
                Write-Host "$($symbols.Running) Engine Socket Server Started..."
                & $py -m Data.Engine.bootstrapper
                if ($previousEngineMode) { $env:BOREALIS_ENGINE_MODE = $previousEngineMode } else { Remove-Item Env:BOREALIS_ENGINE_MODE -ErrorAction SilentlyContinue }
                if ($previousEnginePort) { $env:BOREALIS_ENGINE_PORT = $previousEnginePort } else { Remove-Item Env:BOREALIS_ENGINE_PORT -ErrorAction SilentlyContinue }
                Pop-Location
            }
            break
        }

        Write-Host "Deploying Borealis Engine in '$engineOperationMode' mode" -ForegroundColor Blue

        $venvFolder            = "Engine"
        $dataSource            = "Data"
        $engineSource          = "$dataSource\Engine"
        $engineDataDestination = "$venvFolder\Data\Engine"
        $webUIFallbackSource   = "$dataSource\Server\web-interface"
        $webUIDestination      = "$venvFolder\web-interface"
        $venvPython            = Join-Path $venvFolder 'Scripts\python.exe'
        $engineSourceAbsolute  = Join-Path $scriptDir $engineSource
        $webUIFallbackAbsolute = Join-Path $scriptDir $webUIFallbackSource

        Run-Step "Create Borealis Engine Virtual Python Environment" {
            $venvActivate = Join-Path $venvFolder 'Scripts\Activate'
            if (-not (Test-Path $venvActivate)) {
                & $pythonExe -m venv $venvFolder | Out-Null
            }

            $engineDataRoot = Join-Path $venvFolder 'Data'
            if (-not (Test-Path $engineDataRoot)) {
                New-Item -Path $engineDataRoot -ItemType Directory -Force | Out-Null
            }

            if (Test-Path (Join-Path $scriptDir $engineDataDestination)) {
                Remove-Item (Join-Path $scriptDir $engineDataDestination) -Recurse -Force -ErrorAction SilentlyContinue
            }
            New-Item -Path (Join-Path $scriptDir $engineDataDestination) -ItemType Directory -Force | Out-Null

            if (-not (Test-Path $engineSourceAbsolute)) {
                throw "Engine source directory '$engineSourceAbsolute' not found."
            }
            Copy-Item (Join-Path $engineSourceAbsolute '*') (Join-Path $scriptDir $engineDataDestination) -Recurse -Force

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
            $engineWebUISource = Join-Path $scriptDir 'Engine\web-interface'
            if (Test-Path $engineWebUISource) {
                $webUIDestinationAbsolute = Join-Path $scriptDir $webUIDestination
                if (Test-Path $webUIDestinationAbsolute) {
                    Remove-Item (Join-Path $webUIDestinationAbsolute '*') -Recurse -Force -ErrorAction SilentlyContinue
                } else {
                    New-Item -Path $webUIDestinationAbsolute -ItemType Directory -Force | Out-Null
                }
                Copy-Item (Join-Path $engineWebUISource '*') $webUIDestinationAbsolute -Recurse -Force
            } elseif (-not (Test-Path (Join-Path $scriptDir $webUIDestination)) -or -not (Get-ChildItem -Path (Join-Path $scriptDir $webUIDestination) -ErrorAction SilentlyContinue | Select-Object -First 1)) {
                if (Test-Path $webUIFallbackAbsolute) {
                    $webUIDestinationAbsolute = Join-Path $scriptDir $webUIDestination
                    if (-not (Test-Path $webUIDestinationAbsolute)) {
                        New-Item -Path $webUIDestinationAbsolute -ItemType Directory -Force | Out-Null
                    }
                    Copy-Item (Join-Path $webUIFallbackAbsolute '*') $webUIDestinationAbsolute -Recurse -Force
                } else {
                    Write-Host "Fallback WebUI source not found at '$webUIFallbackAbsolute'." -ForegroundColor Yellow
                }
            } else {
                Write-Host "Existing Engine web interface detected; skipping fallback copy." -ForegroundColor DarkYellow
            }
        }

        Run-Step "Vite Web Frontend: Install NPM Packages" {
            $webUIDestinationAbsolute = Join-Path $scriptDir $webUIDestination
            if (Test-Path $webUIDestinationAbsolute) {
                Push-Location $webUIDestinationAbsolute
                $env:npm_config_loglevel = "silent"
                & $npmCmd install --silent --no-fund --audit=false | Out-Null
                Pop-Location
            } else {
                Write-Host "Web interface destination '$webUIDestinationAbsolute' not found." -ForegroundColor Yellow
            }
        }

        Run-Step "Vite Web Frontend: Start ($engineOperationMode)" {
            $webUIDestinationAbsolute = Join-Path $scriptDir $webUIDestination
            if (Test-Path $webUIDestinationAbsolute) {
                Push-Location $webUIDestinationAbsolute
                if ($engineOperationMode -eq "developer") { $viteSubCommand = "dev" } else { $viteSubCommand = "build" }
                $certRoot = Join-Path $scriptDir 'Certificates\Server'
                Ensure-EngineTlsMaterial -PythonPath $venvPython -CertificateRoot $certRoot
                Start-Process -NoNewWindow -FilePath $npmCmd -ArgumentList @("run", $viteSubCommand)
                Pop-Location
            }
        }

        Run-Step "Borealis Engine: Launch Flask Server" {
            Push-Location (Join-Path $scriptDir "Engine")
            $py = Join-Path $scriptDir "Engine\Scripts\python.exe"
            $previousEngineMode = $env:BOREALIS_ENGINE_MODE
            $previousEnginePort = $env:BOREALIS_ENGINE_PORT
            $env:BOREALIS_ENGINE_MODE = $engineOperationMode
            $env:BOREALIS_ENGINE_PORT = "5001"
            Write-Host "`nLaunching Borealis Engine..." -ForegroundColor Green
            Write-Host "===================================================================================="
            Write-Host "$($symbols.Running) Engine Socket Server Started..."
            & $py -m Data.Engine.bootstrapper
            if ($previousEngineMode) { $env:BOREALIS_ENGINE_MODE = $previousEngineMode } else { Remove-Item Env:BOREALIS_ENGINE_MODE -ErrorAction SilentlyContinue }
            if ($previousEnginePort) { $env:BOREALIS_ENGINE_PORT = $previousEnginePort } else { Remove-Item Env:BOREALIS_ENGINE_PORT -ErrorAction SilentlyContinue }
            Pop-Location
        }
    }

    # (Removed) case "6" experimental AHK test

    default { Write-Host "Invalid selection. Exiting..." -ForegroundColor Red; exit 1 }
}
