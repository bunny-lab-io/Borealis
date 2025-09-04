#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Launch-Borealis.ps1

<#
    Borealis.ps1
    ----------------------
    This script deploys the Borealis Workflow Automation Tool with three modules:
      - Server (Web Dashboard)
      - Agent (Client / Data Collector)
      - Desktop App (Electron)

    It begins by presenting a menu to the user. Based on the selection,
    the corresponding module is launched or deployed.

    Usage:
      Set-ExecutionPolicy Unrestricted -Scope Process; .\Borealis.ps1
#>

[CmdletBinding()]
param(
    [switch]$Server,
    [switch]$Agent,
    [ValidateSet('install','repair','remove','launch','')]
    [string]$AgentAction = '',
    [switch]$Vite,
    [switch]$Flask,
    [switch]$Quick
)

# Preselect menu choices from CLI args (optional)
$choice = $null
$modeChoice = $null
$agentSubChoice = $null

if ($Server -and $Agent) {
    Write-Host "Cannot use -Server and -Agent together." -ForegroundColor Red
    exit 1
}

if ($Vite -and $Flask) {
    Write-Host "Cannot combine -Vite and -Flask." -ForegroundColor Red
    exit 1
}

if ($Server) {
    $choice = '1'
} elseif ($Agent) {
    $choice = '2'
    switch ($AgentAction) {
        'install' { $agentSubChoice = '1' }
        'repair'  { $agentSubChoice = '2' }
        'remove'  { $agentSubChoice = '3' }
        'launch'  { $agentSubChoice = '4' }
        default   { }
    }
}

if ($Server) {
    if     ($Vite)             { $modeChoice = '3' }
    elseif ($Flask -and $Quick){ $modeChoice = '2' }
    elseif ($Flask)            { $modeChoice = '1' }
}
$host.UI.RawUI.WindowTitle = "Borealis"
Clear-Host

# ASCII Art Banner
@'

███████████                                        ████   ███         
░░███░░░░░███                                      ░░███  ░░░          
 ░███    ░███  ██████  ████████   ██████   ██████   ░███  ████   █████ 
 ░██████████  ███░░███░░███░░███ ███░░███ ░░░░░███  ░███ ░░███  ███░░  
 ░███░░░░░███░███ ░███ ░███ ░░░ ░███████   ███████  ░███  ░███ ░░█████ 
 ░███    ░███░███ ░███ ░███     ░███░░░   ███░░███  ░███  ░███  ░░░░███
 ███████████ ░░██████  █████    ░░██████ ░░████████ █████ █████ ██████ 
░░░░░░░░░░░   ░░░░░░  ░░░░░      ░░░░░░   ░░░░░░░░ ░░░░░ ░░░░░ ░░░░░░  
'@ | Write-Host -ForegroundColor DarkCyan
Write-Host "Automation Platform" -ForegroundColor DarkGray
Write-Host " "
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
    # Remove scheduled task if it exists
    $taskName = 'Borealis Agent'
    Write-AgentLog -FileName $LogName -Message "Attempting to delete scheduled task: $taskName"
    try { schtasks.exe /Delete /TN "$taskName" /F 2>$null | Out-Null } catch {}
}

# Repair routine: cleans services, ensures venv files, reinstalls and starts BorealisAgent
function Repair-BorealisAgent {
    $logName = 'Repair.log'
    Write-AgentLog -FileName $logName -Message "=== Repair start ==="
    $agentDir = Join-Path $scriptDir 'Agent'
    $venvPython = Join-Path $agentDir 'Scripts\python.exe'
    $deployScript = Join-Path $agentDir 'Borealis\agent_deployment.py'

    # Aggressive cleanup first
    Remove-BorealisServicesAndTasks -LogName $logName
    Start-Sleep -Seconds 1

    # Ensure venv and files exist by reusing the install block
    Write-AgentLog -FileName $logName -Message "Ensuring agent venv and files"
    $venvFolder             = 'Agent'
    $agentSourcePath        = 'Data\Agent\borealis-agent.py'
    $agentRequirements      = 'Data\Agent\agent-requirements.txt'
    $agentDestinationFolder = "$venvFolder\Borealis"
    $venvPythonPath         = Join-Path $scriptDir $venvFolder | Join-Path -ChildPath 'Scripts\python.exe'

    if (-not (Test-Path "$venvFolder\Scripts\Activate")) {
        $pythonForVenv = $pythonExe
        if (-not (Test-Path $pythonForVenv)) {
            $pyCmd = Get-Command py -ErrorAction SilentlyContinue
            $pythonCmd = Get-Command python -ErrorAction SilentlyContinue
            if ($pyCmd) { $pythonForVenv = $pyCmd.Source }
            elseif ($pythonCmd) { $pythonForVenv = $pythonCmd.Source }
            else { throw "Python not found. Please run Server setup (option 1) first." }
        }
        Write-AgentLog -FileName $logName -Message "Creating venv using: $pythonForVenv"
        & $pythonForVenv -m venv $venvFolder | Out-Null
    }

    if (Test-Path $agentSourcePath) {
        Write-AgentLog -FileName $logName -Message "Refreshing Agent/Borealis files"
        Remove-Item $agentDestinationFolder -Recurse -Force -ErrorAction SilentlyContinue
        New-Item -Path $agentDestinationFolder -ItemType Directory -Force | Out-Null
        Copy-Item 'Data\Agent\borealis-agent.py'    $agentDestinationFolder -Recurse
        Copy-Item 'Data\Agent\agent_info.py'        $agentDestinationFolder -Recurse
        Copy-Item 'Data\Agent\agent_roles.py'       $agentDestinationFolder -Recurse
        Copy-Item 'Data\Agent\Python_API_Endpoints' $agentDestinationFolder -Recurse
        Copy-Item 'Data\Agent\windows_script_service.py' $agentDestinationFolder -Force
        Copy-Item 'Data\Agent\agent_deployment.py'  $agentDestinationFolder -Force
        Copy-Item 'Data\Agent\tray_launcher.py'     $agentDestinationFolder -Force
        if (Test-Path 'Data\Agent\Borealis.ico') { Copy-Item 'Data\Agent\Borealis.ico' $agentDestinationFolder -Force }
    }

    . "$venvFolder\Scripts\Activate"
    if (Test-Path $agentRequirements) {
        Write-AgentLog -FileName $logName -Message "Installing agent requirements"
        & $venvPythonPath -m pip install --disable-pip-version-check -q -r $agentRequirements | Out-Null
    }

    # Install service via deployment helper (sets PythonHome/PythonPath)
    Write-AgentLog -FileName $logName -Message "Running agent_deployment.py ensure-all"
    $deployScript = Join-Path $agentDestinationFolder 'agent_deployment.py'
    & $venvPythonPath -W ignore::SyntaxWarning $deployScript ensure-all *>&1 | Tee-Object -FilePath (Join-Path (Ensure-AgentLogDir) $logName) -Append | Out-Null

    # Start the service explicitly
    Write-AgentLog -FileName $logName -Message "Starting service BorealisAgent"
    try { sc.exe start BorealisAgent 2>$null | Out-Null } catch {}
    Start-Sleep -Seconds 2
    Write-AgentLog -FileName $logName -Message "Query service state"
    sc.exe query BorealisAgent *>> (Join-Path (Ensure-AgentLogDir) $logName)
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
        exit 1
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
$node7zUrl      = "https://nodejs.org/dist/v23.11.0/node-v23.11.0-win-x64.7z"
$nodeInstallDir = Join-Path $depsRoot "NodeJS"
$node7zPath     = Join-Path $depsRoot "node-v23.11.0-win-x64.7z"

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
}

#<# ---------------------- Agent Service Helper Functions (Windows) [Deprecated: superseded by Data/Agent/agent_deployment.py] ----------------------
function Ensure-BorealisAgent-Service {
    param(
        [string]$VenvPython,
        [string]$ServiceScript
    )
    if (-not (Test-IsWindows)) {
        Write-Host "Agent service setup skipped (non-Windows)." -ForegroundColor DarkGray
        return
    }

    $serviceName = 'BorealisAgent'
    $svc = Get-CimInstance -ClassName Win32_Service -Filter "Name='$serviceName'" -ErrorAction SilentlyContinue
    $needsInstall = $false
    if (-not $svc) { $needsInstall = $true }
    else {
        if ($svc.StartMode -notin @('Auto','Automatic')) { $needsInstall = $true }
        if ($svc.StartName -ne 'LocalSystem') { $needsInstall = $true }
        # Verify the service points to the current project venv (folder may have moved)
        try {
            $venvRoot = Split-Path $VenvPython -Parent
            $pathName = $svc.PathName
            if ($pathName -and $venvRoot) {
                if ($pathName -notlike ("*" + $venvRoot + "*")) { $needsInstall = $true }
            }
        } catch {}
    }

    if ($needsInstall) {
        if (-not (Test-IsAdmin)) {
            Write-Host "Admin rights required to install/update the agent service. Prompting UAC..." -ForegroundColor Yellow
            $tmp = New-TemporaryFile
            $logDir = Join-Path $scriptDir 'Logs'
            if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Path $logDir -Force | Out-Null }
            $log = Join-Path $logDir 'Borealis_AgentService_Install.log'
            $vEsc = $VenvPython.Replace('"','""')
            $sEsc = $ServiceScript.Replace('"','""')
            $content = @"
$ErrorActionPreference = 'Continue'
$venv = "$vEsc"
$srv  = "$sEsc"
$logPath = "$log"
try {
  try { New-Item -ItemType Directory -Force -Path (Split-Path $logPath -Parent) | Out-Null } catch {}
  # Ensure pywin32 postinstall is applied in this venv (required for services)
  try {
    & $venv -m pywin32_postinstall -install *>&1 | Tee-Object -FilePath $logPath -Append
  } catch { }
  "[INFO] Installing service via: $venv $srv --startup auto install" | Out-File -FilePath $logPath -Encoding UTF8
  & $venv $srv --startup auto install *>&1 | Tee-Object -FilePath $logPath -Append
  try { sc.exe config BorealisAgent obj= LocalSystem | Tee-Object -FilePath $logPath -Append } catch {}
  $code = $LASTEXITCODE
  if ($code -eq $null) { $code = 0 }
  "[INFO] Exit code: $code" | Tee-Object -FilePath $logPath -Append | Out-Host
} catch {
  "[ERROR] $_" | Tee-Object -FilePath $logPath -Append | Out-Host
  $code = 1
} finally {
  if ($code -ne 0 -or $env:BOREALIS_DEBUG_UAC -eq '1') {
    Read-Host 'Press Enter to close this elevated window...'
  }
  exit $code
}
"@
            # pass key vars via env so the elevated scope can see them
            $psi = New-Object System.Diagnostics.ProcessStartInfo
            $psi.FileName = 'powershell.exe'
            $psi.Verb = 'runas'
            $psi.Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$($tmp.FullName)`""
            $psi.UseShellExecute = $true
            $psi.WindowStyle = 'Normal'
            Set-Content -Path $tmp.FullName -Value $content -Force -Encoding UTF8
            $proc = [System.Diagnostics.Process]::Start($psi)
            $proc.WaitForExit()
            Remove-Item $tmp.FullName -Force -ErrorAction SilentlyContinue
            if (Test-Path $log) {
                Write-Host "--- Elevated install log: $log ---" -ForegroundColor DarkCyan
                Get-Content $log | Write-Host
            }
        } else {
            try { & $VenvPython $ServiceScript remove } catch {}
            & $VenvPython $ServiceScript --startup auto install
            try { sc.exe config BorealisAgent obj= LocalSystem } catch {}
        }
        # Refresh service object
        $svc = Get-CimInstance -ClassName Win32_Service -Filter "Name='$serviceName'" -ErrorAction SilentlyContinue
        if (-not $svc) {
            Write-Host "Failed to install Borealis Agent service." -ForegroundColor Red
            return
        }
    }

    if ($svc.State -ne 'Running') {
        if (-not (Test-IsAdmin)) {
            Write-Host "Admin rights required to start the agent service. Prompting UAC..." -ForegroundColor Yellow
            $tmp2 = New-TemporaryFile
            $log2 = Join-Path $scriptDir 'Logs\Borealis_AgentService_Start.log'
            $content2 = @"
$ErrorActionPreference = 'Continue'
try {
  Start-Service -Name '$serviceName'
  "[INFO] Started Borealis Agent service." | Out-File -FilePath "$log2" -Encoding UTF8
  $code = 0
} catch {
  "[ERROR] $_" | Out-File -FilePath "$log2" -Encoding UTF8
  $code = 1
} finally {
  if ($code -ne 0 -or $env:BOREALIS_DEBUG_UAC -eq '1') { Read-Host 'Press Enter to close this elevated window...' }
  exit $code
}
"@
            Set-Content -Path $tmp2.FullName -Value $content2 -Force -Encoding UTF8
            $psi2 = New-Object System.Diagnostics.ProcessStartInfo
            $psi2.FileName = 'powershell.exe'
            $psi2.Verb = 'runas'
            $psi2.Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$($tmp2.FullName)`""
            $psi2.UseShellExecute = $true
            $psi2.WindowStyle = 'Normal'
            $proc2 = [System.Diagnostics.Process]::Start($psi2)
            $proc2.WaitForExit()
            Remove-Item $tmp2.FullName -Force -ErrorAction SilentlyContinue
            if (Test-Path $log2) {
                Write-Host "--- Elevated start log: $log2 ---" -ForegroundColor DarkCyan
                Get-Content $log2 | Write-Host
            }
        } else {
            Start-Service -Name $serviceName
        }
    }

    Write-Host "Borealis Agent service is installed and running as LocalSystem (Automatic)." -ForegroundColor Green
}

# Ensure Script Agent (LocalSystem Windows Service)
function Ensure-BorealisScriptAgent-Service {
    param(
        [string]$VenvPython,
        [string]$ServiceScript
    )
    if (-not (Test-IsWindows)) { return }
    $serviceName = 'BorealisAgent'
    $svc = Get-CimInstance -ClassName Win32_Service -Filter "Name='$serviceName'" -ErrorAction SilentlyContinue
    $needsInstall = $false
    if (-not $svc) { $needsInstall = $true }
    else {
        if ($svc.StartMode -notin @('Auto','Automatic')) { $needsInstall = $true }
        if ($svc.StartName -ne 'LocalSystem') { $needsInstall = $true }
        try {
            $venvRoot = Split-Path $VenvPython -Parent
            if ($svc.PathName -and $venvRoot -and ($svc.PathName -notlike ("*" + $venvRoot + "*"))) { $needsInstall = $true }
        } catch {}
    }

    $logs = Join-Path $scriptDir 'Logs'
    $tempDir = Join-Path $scriptDir 'Temp'; if (-not (Test-Path $tempDir)) { New-Item -ItemType Directory -Path $tempDir -Force | Out-Null }
    if (-not (Test-Path $logs)) { New-Item -ItemType Directory -Path $logs -Force | Out-Null }
    $preflight = Join-Path $logs 'Borealis_ScriptAgent_Preflight.log'
    "$(Get-Date -Format s) Preflight: Ensure-BorealisScriptAgent-Service (needsInstall=$needsInstall)" | Out-File -FilePath $preflight -Append -Encoding UTF8

    if ($needsInstall) {
        if (-not (Test-IsAdmin)) {
            Write-Host "Admin rights required to install/update the script agent service. Prompting UAC..." -ForegroundColor Yellow
            $log = Join-Path $logs 'Borealis_ScriptAgent_Install.log'
            $vEsc = $VenvPython.Replace('"','""')
            $sEsc = $ServiceScript.Replace('"','""')
            $content = @"
$ErrorActionPreference = 'Continue'
$venv = "$vEsc"
$srv  = "$sEsc"
$logPath = "$log"
try {
  try { New-Item -ItemType Directory -Force -Path (Split-Path $logPath -Parent) | Out-Null } catch {}
  & $venv -m pywin32_postinstall -install *>&1 | Tee-Object -FilePath $logPath -Append
  try { & $venv $srv remove *>&1 | Tee-Object -FilePath $logPath -Append } catch {}
  & $venv $srv --startup auto install *>&1 | Tee-Object -FilePath $logPath -Append
  sc.exe config $serviceName obj= LocalSystem | Tee-Object -FilePath $logPath -Append
  $code = $LASTEXITCODE; if ($code -eq $null) { $code = 0 }
} catch {
  "[ERROR] $_" | Tee-Object -FilePath $logPath -Append | Out-Host
  $code = 1
} finally {
  if ($code -ne 0 -or $env:BOREALIS_DEBUG_UAC -eq '1') { Read-Host 'Press Enter to close this elevated window...' }
  exit $code
}
"@
            $elevateScript = Join-Path $tempDir 'Elevated-Install-ScriptAgent.ps1'
            Set-Content -Path $elevateScript -Value $content -Force -Encoding UTF8
            $psi = New-Object System.Diagnostics.ProcessStartInfo
            $psi.FileName = 'powershell.exe'
            $psi.Verb = 'runas'
            $psi.Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$elevateScript`""
            $psi.UseShellExecute = $true
            $psi.WindowStyle = 'Normal'
            $psi.WorkingDirectory = $scriptDir
            $proc = [System.Diagnostics.Process]::Start($psi)
            $proc.WaitForExit()
            if ($proc.ExitCode -eq 0) { Remove-Item $elevateScript -Force -ErrorAction SilentlyContinue } else { Write-Host "Keeping stub for troubleshooting: $elevateScript" -ForegroundColor Yellow }
            if (Test-Path $log) { Write-Host "--- Elevated install log: $log ---" -ForegroundColor DarkCyan; Get-Content $log | Write-Host }
            else { Write-Host "Install log not found at: $log" -ForegroundColor Yellow }
            if ($proc.ExitCode -ne 0) { Write-Host "Elevated install returned code $($proc.ExitCode)" -ForegroundColor Red; return }
        } else {
            try { & $VenvPython $ServiceScript remove } catch {}
            & $VenvPython $ServiceScript --startup auto install
            try { sc.exe config $serviceName obj= LocalSystem } catch {}
        }
    }

    $svc = Get-CimInstance -ClassName Win32_Service -Filter "Name='$serviceName'" -ErrorAction SilentlyContinue
    if (-not $svc) { Write-Host "ScriptAgent service not found after install." -ForegroundColor Red; return $false }
    if ($svc.State -ne 'Running') {
        if (-not (Test-IsAdmin)) {
            Write-Host "Admin rights required to start the script agent service. Prompting UAC..." -ForegroundColor Yellow
            $log2 = Join-Path $logs 'Borealis_ScriptAgent_Start.log'
            $content2 = @"
$ErrorActionPreference = 'Continue'
try { Start-Service -Name '$serviceName'; "[INFO] Started." | Out-File -FilePath "$log2" -Encoding UTF8; $code = 0 } catch { "[ERROR] $_" | Out-File -FilePath "$log2" -Encoding UTF8; $code = 1 } finally { if ($code -ne 0 -or $env:BOREALIS_DEBUG_UAC -eq '1') { Read-Host 'Press Enter to close this elevated window...' }; exit $code }
"@
            $elevateStart = Join-Path $tempDir 'Elevated-Start-ScriptAgent.ps1'
            Set-Content -Path $elevateStart -Value $content2 -Force -Encoding UTF8
            $psi2 = New-Object System.Diagnostics.ProcessStartInfo
            $psi2.FileName = 'powershell.exe'
            $psi2.Verb = 'runas'
            $psi2.Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$elevateStart`""
            $psi2.UseShellExecute = $true
            $psi2.WindowStyle = 'Normal'
            $psi2.WorkingDirectory = $scriptDir
            $proc2 = [System.Diagnostics.Process]::Start($psi2)
            $proc2.WaitForExit()
            if ($proc2.ExitCode -eq 0) { Remove-Item $elevateStart -Force -ErrorAction SilentlyContinue } else { Write-Host "Keeping stub for troubleshooting: $elevateStart" -ForegroundColor Yellow }
            if (Test-Path $log2) { Write-Host "--- Elevated start log: $log2 ---" -ForegroundColor DarkCyan; Get-Content $log2 | Write-Host }
            else { Write-Host "Start log not found at: $log2" -ForegroundColor Yellow }
            if ($proc2.ExitCode -ne 0) { Write-Host "Elevated start returned code $($proc2.ExitCode)" -ForegroundColor Red; return }
        } else {
            Start-Service -Name $serviceName
        }
        Start-Sleep -Seconds 2
        $svc = Get-CimInstance -ClassName Win32_Service -Filter "Name='$serviceName'" -ErrorAction SilentlyContinue
        if ($svc.State -ne 'Running') { Write-Host "ScriptAgent service failed to start." -ForegroundColor Red; return $false }
    }
    return $true
}

# Ensure Collector Agent (per-user scheduled task)
function Ensure-BorealisCollector-Task {
    param(
        [string]$VenvPython,
        [string]$CollectorScript
    )
    $logs = Join-Path $scriptDir 'Logs'
    if (-not (Test-Path $logs)) { New-Item -ItemType Directory -Path $logs -Force | Out-Null }
    "$(Get-Date -Format s) Preflight: Ensure-BorealisCollector-Task" | Out-File -FilePath (Join-Path $logs 'Borealis_Collector_Preflight.log') -Append -Encoding UTF8

    $tempDir = Join-Path $scriptDir 'Temp'
    if (-not (Test-Path $tempDir)) { New-Item -ItemType Directory -Path $tempDir -Force | Out-Null }
    $logC = Join-Path $logs 'Borealis_CollectorTask_Install.log'

    $taskName = 'BorealisCollectorAgent'
    $taskExists = $false
    try {
        $null = schtasks.exe /Query /TN $taskName 2>$null
        if ($LASTEXITCODE -eq 0) { $taskExists = $true }
    } catch {}

    $quoted = '"' + $VenvPython + '" -W ignore::SyntaxWarning "' + $CollectorScript + '"'

    if ($taskExists) {
        # Replace to ensure it points to current project
        # Attempt delete in current user context first
        try {
            schtasks.exe /Delete /TN $taskName /F 2>&1 | Tee-Object -FilePath $logC -Append | Out-Host
        } catch {}
        if ($LASTEXITCODE -ne 0) {
            # If deletion failed (likely created under elevated/Admin previously), delete via elevated stub
            $stubDel = Join-Path $tempDir 'Elevated-Delete-CollectorTask.ps1'
            $tnEsc = $taskName.Replace('"','""')
            $lgEsc = $logC.Replace('"','""')
            $contentDel = @"
$ErrorActionPreference = 'Continue'
try {
  schtasks.exe /Delete /TN "$tnEsc" /F *>&1 | Tee-Object -FilePath "$lgEsc" -Append
  $code = $LASTEXITCODE; if ($code -eq $null) { $code = 0 }
} catch { "[ERROR] $_" | Tee-Object -FilePath "$lgEsc" -Append; $code = 1 } finally {
  if ($code -ne 0 -or $env:BOREALIS_DEBUG_UAC -eq '1') { Read-Host 'Press Enter to close this elevated window...' }
  exit $code
}
"@
            Set-Content -Path $stubDel -Value $contentDel -Force -Encoding UTF8
            $psiD = New-Object System.Diagnostics.ProcessStartInfo
            $psiD.FileName  = 'powershell.exe'
            $psiD.Verb      = 'runas'
            $psiD.Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$stubDel`""
            $psiD.UseShellExecute = $true
            $psiD.WindowStyle = 'Normal'
            try { $psiD.WorkingDirectory = $scriptDir; $pd = [System.Diagnostics.Process]::Start($psiD); $pd.WaitForExit() } catch {}
            Remove-Item $stubDel -Force -ErrorAction SilentlyContinue
        }
        # Re-evaluate existence; if still present, abort
        try { $null = schtasks.exe /Query /TN $taskName 2>$null } catch {}
        if ($LASTEXITCODE -eq 0) {
            Write-Host "Failed to remove existing Collector task (permissions)." -ForegroundColor Red
            if (Test-Path $logC) { Write-Host "--- Collector task log: $logC ---" -ForegroundColor DarkCyan; Get-Content $logC | Write-Host }
            return $false
        }
    }

    # Create per-user task in the current (non-elevated) user context
    # Do NOT elevate creation to avoid creating task under Administrator by mistake
    schtasks.exe /Create /SC ONLOGON /TN $taskName /TR $quoted /F /RL LIMITED 2>&1 | Tee-Object -FilePath $logC -Append | Out-Host
    if ($LASTEXITCODE -ne 0) {
        Write-Host "Failed to create Collector task." -ForegroundColor Red
        if (Test-Path $logC) { Write-Host "--- Collector task log: $logC ---" -ForegroundColor DarkCyan; Get-Content $logC | Write-Host }
        return $false
    }
    # If currently logged in, start now
    try { schtasks.exe /Run /TN $taskName 2>&1 | Tee-Object -FilePath $logC -Append | Out-Host } catch {}
    # Validate task exists
    try { $null = schtasks.exe /Query /TN $taskName 2>$null } catch {}
    if ($LASTEXITCODE -ne 0) { Write-Host "Collector task missing after create." -ForegroundColor Red; return $false }
    return $true
}
#>
# ---------------------- Common Initialization & Visuals ----------------------
Clear-Host

# ASCII Art Banner
@'

███████████                                        ████   ███         
░░███░░░░░███                                      ░░███  ░░░          
 ░███    ░███  ██████  ████████   ██████   ██████   ░███  ████   █████ 
 ░██████████  ███░░███░░███░░███ ███░░███ ░░░░░███  ░███ ░░███  ███░░  
 ░███░░░░░███░███ ░███ ░███ ░░░ ░███████   ███████  ░███  ░███ ░░█████ 
 ░███    ░███░███ ░███ ░███     ░███░░░   ███░░███  ░███  ░███  ░░░░███
 ███████████ ░░██████  █████    ░░██████ ░░████████ █████ █████ ██████ 
░░░░░░░░░░░   ░░░░░░  ░░░░░      ░░░░░░   ░░░░░░░░ ░░░░░ ░░░░░ ░░░░░░  
'@ | Write-Host -ForegroundColor DarkCyan
Write-Host "Automation Platform" -ForegroundColor DarkGray

## Defer PATH updates and tool presence checks until after a selection is made

# ---------------------- Menu Prompt & User Input ----------------------
if (-not $choice) {
    Write-Host " "
    Write-Host "Please choose which function you want to launch:"
    Write-Host " 1) Borealis Server" -ForegroundColor DarkGray
    Write-Host " 2) Borealis Agent" -ForegroundColor DarkGray
    Write-Host " 3) Build Electron App " -NoNewline -ForegroundColor DarkGray
    Write-Host "[Experimental]" -ForegroundColor Red
    Write-Host " 4) Package Self-Contained EXE of Server or Agent " -NoNewline -ForegroundColor DarkGray
    Write-Host "[Experimental]" -ForegroundColor Red
    Write-Host " 5) Update Borealis " -NoNewLine -ForegroundColor DarkGray
    Write-Host "[Requires Re-Build]" -ForegroundColor Red
    Write-Host " 6) Perform AutoHotKey Automation Testing " -NoNewline -ForegroundColor DarkGray
    Write-Host "[Experimental - Dev Testing]" -ForegroundColor Red
    Write-Host "Type a number and press " -NoNewLine
    Write-Host "<ENTER>" -ForegroundColor DarkCyan
    $choice = Read-Host
}
switch ($choice) {

    "1" {
        $host.UI.RawUI.WindowTitle = "Borealis Server"
        Write-Host "Ensuring Server Dependencies Exist..." -ForegroundColor DarkCyan
        
        # ---------------------- Ensure users.json is Present ----------------------
        Run-Step "First-Run: Generating users.json" {
            $usersJsonPath = Join-Path $scriptDir "users.json"
            if (-not (Test-Path $usersJsonPath)) {
        $defaultUsers = @{ users = @(@{ username = "admin"; password = "e6c83b282aeb2e022844595721cc00bbda47cb24537c1779f9bb84f04039e1676e6ba8573e588da1052510e3aa0a32a9e55879ae22b0c2d62136fc0a3e85f8bb" }) } |
            ConvertTo-Json -Depth 4
        $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
        [System.IO.File]::WriteAllText($usersJsonPath, $defaultUsers, $utf8NoBom)
            }
        }
        
        Install_Shared_Dependencies
        Install_Server_Dependencies

        # After ensuring dependencies, verify key tools and update PATH
        foreach ($tool in @($pythonExe, $nodeExe, $npmCmd, $npxCmd)) {
            if (-not (Test-Path $tool)) {
                Write-Host "`r$($symbols.Fail) Bundled executable not found at '$tool'." -ForegroundColor Red
                exit 1
            }
        }
        $env:PATH = '{0};{1};{2}' -f (Split-Path $pythonExe), (Split-Path $nodeExe), $env:PATH

        if (-not $modeChoice) {
            Write-Host " "
            Write-Host "Configure Borealis Server Mode:" -ForegroundColor DarkYellow
            Write-Host " 1) Build & Launch > " -NoNewLine -ForegroundColor DarkGray
            Write-Host "Production Flask Server @ " -NoNewLine
            Write-Host "http://localhost:5000" -ForegroundColor DarkCyan
            Write-Host " 2) [Skip Build] & Immediately Launch > " -NoNewLine -ForegroundColor DarkGray
            Write-Host "Production Flask Server @ " -NoNewLine
            Write-Host "http://localhost:5000" -ForegroundColor DarkCyan
            Write-Host " 3) Launch > " -NoNewLine -ForegroundColor DarkGray
            Write-Host "[Hotload-Ready] " -NoNewLine -ForegroundColor Green
            Write-Host "Vite Dev Server @ " -NoNewLine
            Write-Host "http://localhost:5173" -ForegroundColor DarkCyan
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
                Exit 0
            }
            "3" { $borealis_operation_mode = "developer"  }
            default {
                Write-Host "Invalid mode choice: $modeChoice" -ForegroundColor Red
                Exit 1
            }
        }

        # ───── Now run your deploy logic ─────
        Write-Host "Deploying Borealis Server in '$borealis_operation_mode' mode" -ForegroundColor Blue

        $venvFolder       = "Server"
        $dataSource       = "Data"
        $dataDestination  = "$venvFolder\Borealis"
        $customUIPath     = "$dataSource\Server\WebUI"
        $webUIDestination = "$venvFolder\web-interface"
        $venvPython       = Join-Path $venvFolder 'Scripts\python.exe'

        # Create Virtual Environment & Copy Server Assets
        Run-Step "Create Borealis Virtual Python Environment" {
            # Leverage Bundled Python Dependency to Construct Virtual Python Environment
            if (-not (Test-Path "$venvFolder\Scripts\Activate")) { & $pythonExe -m venv $venvFolder | Out-Null }
            if (Test-Path $dataSource) {
                Remove-Item $dataDestination -Recurse -Force -ErrorAction SilentlyContinue
                New-Item -Path $dataDestination -ItemType Directory -Force | Out-Null
                Copy-Item "$dataSource\Server\Python_API_Endpoints" $dataDestination -Recurse
                Copy-Item "$dataSource\Server\Sounds"                 $dataDestination -Recurse
                Copy-Item "$dataSource\Server\server.py"              $dataDestination
            }
            . "$venvFolder\Scripts\Activate"
        }

        # Install Python Dependencies
        Run-Step "Install Python Dependencies into Virtual Python Environment" {
            if (Test-Path "$dataSource\Server\server-requirements.txt") {
                & $venvPython -m pip install --disable-pip-version-check -q -r "$dataSource\Server\server-requirements.txt" | Out-Null
            }
        }

        # Copy Vite WebUI Assets
        Run-Step "Copy Borealis WebUI Files into: $webUIDestination" {
            if (Test-Path $webUIDestination) {
                Remove-Item "$webUIDestination\public\*" -Recurse -Force -ErrorAction SilentlyContinue
                Remove-Item "$webUIDestination\src\*"    -Recurse -Force -ErrorAction SilentlyContinue
            } else {
                New-Item -Path $webUIDestination -ItemType Directory -Force | Out-Null
            }
            Copy-Item "$customUIPath\*" $webUIDestination -Recurse -Force
        }

        # NPM Install for WebUI
        Run-Step "Vite Web Frontend: Install NPM Packages" {
            Push-Location $webUIDestination
            $env:npm_config_loglevel = "silent"
            & $npmCmd install --silent --no-fund --audit=false | Out-Null
            Pop-Location
        }

        # Vite Operation Mode Control (build vs dev)
        Run-Step "Vite Web Frontend: Start ($borealis_operation_mode)" {
            Push-Location $webUIDestination
            if ($borealis_operation_mode -eq "developer") { $viteSubCommand = "dev" } else { $viteSubCommand = "build" }
            Start-Process -NoNewWindow -FilePath $npmCmd -ArgumentList @("run",$viteSubCommand)
            Pop-Location
        }

        # Launch Flask Server
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
        Write-Host "Agent Menu:" -ForegroundColor Cyan
        Write-Host " 1) Install/Update Agent"
        Write-Host " 2) Repair Borealis Agent"
        Write-Host " 3) Remove Agent"
        Write-Host " 4) Launch UserSession Helper (current session)"
        Write-Host " 5) Back"
        if (-not $agentSubChoice) { $agentSubChoice = Read-Host "Select an option" }

        switch ($agentSubChoice) {
            '2' {
                Repair-BorealisAgent
                break
            }
            '3' {
                Remove-BorealisAgent
                break
            }
            '4' {
                # Manually launch helper for the current session (optional)
                $venvPythonw = Join-Path $scriptDir 'Agent\Scripts\pythonw.exe'
                $helper = Join-Path $scriptDir 'Agent\Borealis\borealis-agent.py'
                if (-not (Test-Path $venvPythonw)) { Write-Host "pythonw.exe not found under Agent\Scripts" -ForegroundColor Yellow }
                if (-not (Test-Path $helper)) { Write-Host "Helper not found under Agent\Borealis" -ForegroundColor Yellow }
                if ((Test-Path $venvPythonw) -and (Test-Path $helper)) {
                    Start-Process -FilePath $venvPythonw -ArgumentList @('-W','ignore::SyntaxWarning', $helper) -WorkingDirectory (Split-Path $helper -Parent)
                    Write-Host "Launched user-session helper." -ForegroundColor Green
                }
                break
            }
            '5' { break }
            Default {
                # 1) Install/Update Agent (original behavior)
                Write-Host "Ensuring Agent Dependencies Exist..." -ForegroundColor DarkCyan
                Install_Shared_Dependencies
                Install_Agent_Dependencies
                if (-not (Test-Path $pythonExe)) {
                    Write-Host "`r$($symbols.Fail) Bundled Python not found at '$pythonExe'." -ForegroundColor Red
                    exit 1
                }
                $env:PATH = '{0};{1}' -f (Split-Path $pythonExe), $env:PATH
                Write-Host "Deploying Borealis Agent..." -ForegroundColor Blue

                $venvFolder             = "Agent"
                $agentSourcePath        = "Data\Agent\borealis-agent.py"
                $agentRequirements      = "Data\Agent\agent-requirements.txt"
                $agentDestinationFolder = "$venvFolder\Borealis"
                $agentDestinationFile   = "$venvFolder\Borealis\borealis-agent.py"
                $venvPython = Join-Path $scriptDir $venvFolder | Join-Path -ChildPath 'Scripts\python.exe'

                Run-Step "Create Virtual Python Environment" {
                    if (-not (Test-Path "$venvFolder\Scripts\Activate")) {
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
                        & $pythonForVenv -m venv $venvFolder
                    }
                    if (Test-Path $agentSourcePath) {
                        Remove-Item $agentDestinationFolder -Recurse -Force -ErrorAction SilentlyContinue
                        New-Item -Path $agentDestinationFolder -ItemType Directory -Force | Out-Null
                        Copy-Item "Data\Agent\borealis-agent.py" $agentDestinationFolder -Recurse
                        Copy-Item "Data\Agent\agent_info.py" $agentDestinationFolder -Recurse
                        Copy-Item "Data\Agent\agent_roles.py" $agentDestinationFolder -Recurse
                        Copy-Item "Data\Agent\Python_API_Endpoints" $agentDestinationFolder -Recurse
                        Copy-Item "Data\Agent\windows_script_service.py" $agentDestinationFolder -Force
                        Copy-Item "Data\Agent\agent_deployment.py" $agentDestinationFolder -Force
                        Copy-Item "Data\Agent\tray_launcher.py" $agentDestinationFolder -Force
                        if (Test-Path "Data\Agent\Borealis.ico") { Copy-Item "Data\Agent\Borealis.ico" $agentDestinationFolder -Force }
                    }
                    . "$venvFolder\Scripts\Activate"
                }

                Run-Step "Install Python Dependencies" {
                    if (Test-Path $agentRequirements) {
                        & $venvPython -m pip install --disable-pip-version-check -q -r $agentRequirements | Out-Null
                    }
                }

                Write-Host "`nConfiguring Borealis Agent (service)..." -ForegroundColor Blue
                Write-Host "===================================================================================="
                $deployScript = Join-Path $agentDestinationFolder 'agent_deployment.py'
                & $venvPython -W ignore::SyntaxWarning $deployScript ensure-all
                if ($LASTEXITCODE -ne 0) {
                    Write-Host "Agent setup FAILED." -ForegroundColor Red
                    Write-Host " - See logs under: $(Join-Path $scriptDir 'Logs')" -ForegroundColor Red
                    exit 1
                } else {
                    Write-Host "Agent setup complete. Service ensured." -ForegroundColor DarkGreen
                }
            }
        }
    }

    "3" {
        $host.UI.RawUI.WindowTitle = "Borealis Electron"
        # Desktop App Deployment (Electron)
        Clear-Host
        Write-Host "Deploying Borealis Desktop App..." -ForegroundColor Cyan
        Write-Host "===================================================================================="
        
        $electronSource      = "Data\Electron"
        $electronDestination = "ElectronApp"
        $scriptDir           = Split-Path $MyInvocation.MyCommand.Path -Parent

        # 1) Prepare ElectronApp folder
        Run-Step "Prepare ElectronApp folder" {
            if (Test-Path $electronDestination) {
                Remove-Item $electronDestination -Recurse -Force
            }
            New-Item -Path $electronDestination -ItemType Directory | Out-Null

            # Copy deployed Flask server
            $deployedServer = Join-Path $scriptDir 'Server\Borealis'
            if (-not (Test-Path $deployedServer)) {
                throw "Server\Borealis not found - please run choice 1 first."
            }
            Copy-Item $deployedServer "$electronDestination\Server" -Recurse

            # Copy Electron scaffold files
            Copy-Item "$electronSource\package.json" "$electronDestination" -Force
            Copy-Item "$electronSource\main.js"        "$electronDestination" -Force

            # Copy built WebUI into renderer
            $staticBuild = Join-Path $scriptDir 'Server\web-interface\build'
            if (-not (Test-Path $staticBuild)) {
                throw "WebUI build not found - run choice 1 to build WebUI first."
            }
            Copy-Item "$staticBuild\*" "$electronDestination\renderer" -Recurse -Force
        }

        # 2) Install Electron & Builder
        Run-Step "ElectronApp: Install Node dependencies" {
            Push-Location $electronDestination
            $env:NODE_ENV = ''
            $env:npm_config_production = ''
            & $npmCmd install --silent --no-fund --audit=false
            Pop-Location
        }

        # 3) Package desktop app
        Run-Step "ElectronApp: Package with electron-builder" {
            Push-Location $electronDestination
            & $npmCmd run dist
            Pop-Location
        }

        # 4) Launch in dev mode
        Run-Step "ElectronApp: Launch in dev mode" {
            Push-Location $electronDestination
            & $npmCmd run dev
            Pop-Location
        }
    }

    "4" {
        $host.UI.RawUI.WindowTitle = "Borealis Packager"
        # Prompt the User for Which System to Package using Pyinstaller
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
        
            default {
                Write-Host "Invalid Choice. Exiting..." -ForegroundColor Red
                exit 1
            }
        }        
    }

    default {
        Write-Host "Invalid selection. Exiting..." -ForegroundColor Red
        exit 1
    }

    "5" {
        $host.UI.RawUI.WindowTitle = "Borealis Updater"
        Write-Host " "
        Write-Host "Updating Borealis..." -ForegroundColor Green

        # Prepare paths
        $updateZip = Join-Path $scriptDir "Update_Staging\main.zip"
        $updateDir = Join-Path $scriptDir "Update_Staging\Borealis-main"
        $preservePath = Join-Path $scriptDir "Data\Server\Python_API_Endpoints\Tesseract-OCR"
        $preserveBackupPath = Join-Path $scriptDir "Update_Staging\Tesseract-OCR"

        Run-Step "Updating: Move Tesseract-OCR Folder Somewhere Safe to Restore Later" {
            if (Test-Path $preservePath) {
                # Ensure staging folder exists
                $stagingPath = Join-Path $scriptDir "Update_Staging"
                if (-not (Test-Path $stagingPath)) {
                    New-Item -ItemType Directory -Force -Path $stagingPath | Out-Null
                }

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
            if (-not (Test-Path $stagingPath)) {
                New-Item -ItemType Directory -Force -Path $stagingPath | Out-Null
            }

            $updateZip = Join-Path $stagingPath "main.zip"
            $updateDir = Join-Path $stagingPath "Borealis-main"
        }

        Run-Step "Updating: Download Update from https://github.com/bunny-lab-io/Borealis/archive/refs/heads/main.zip" {
            Invoke-WebRequest -Uri "https://github.com/bunny-lab-io/Borealis/archive/refs/heads/main.zip" -OutFile $updateZip
        }

        Run-Step "Updating: Extract Update Files" {
            Expand-Archive -Path $updateZip -DestinationPath (Join-Path $scriptDir "Update_Staging") -Force
        }

        Run-Step "Updating: Copy Update Files into Production Borealis Root Folder" {
            Copy-Item "$updateDir\*" $scriptDir -Recurse -Force
        }

        Run-Step "Updating: Restore Tesseract-OCR Folder" {
            $restorePath = Join-Path $scriptDir "Data\Server\Python_API_Endpoints"
            if (Test-Path $preserveBackupPath) {
                # Ensure destination path exists
                if (-not (Test-Path $restorePath)) {
                    New-Item -ItemType Directory -Force -Path $restorePath | Out-Null
                }

                Move-Item -Path $preserveBackupPath -Destination $restorePath -Force
            }
        }

        Run-Step "Updating: Clean Up Update Staging Folder" {
            Remove-Item -Recurse -Force -ErrorAction SilentlyContinue (Join-Path $scriptDir "Update_Staging")
        }

        Write-Host "`nUpdate Complete! Please Re-Launch the Borealis Script." -ForegroundColor Green
        Read-Host "Press any key to re-launch Borealis..."
        & (Join-Path $scriptDir "Borealis.ps1")
        Exit 0
    }

    "6" {
        $host.UI.RawUI.WindowTitle = "AutoHotKey Automation Testing"
        Write-Host " "
        Write-Host "Lauching AutoHotKey Testing Script..." -ForegroundColor Blue
        
        $venvFolder             = "Macro_Testing"
        $scriptSourcePath        = "Data\Experimental\Macros\Macro_Script.py"
        $scriptRequirements      = "Data\Experimental\Macros\macro-requirements.txt"
        $scriptDestinationFolder = "$venvFolder\Borealis"
        $scriptDestinationFile   = "$venvFolder\Borealis\Macro_Script.py"
        $venvPython = Join-Path $scriptDir $venvFolder | Join-Path -ChildPath 'Scripts\python.exe'

        Run-Step "Create Virtual Python Environment" {
            if (-not (Test-Path "$venvFolder\Scripts\Activate")) {
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
                & $pythonForVenv -m venv $venvFolder
            }
            if (Test-Path $scriptSourcePath) {
                Remove-Item $scriptDestinationFolder -Recurse -Force -ErrorAction SilentlyContinue
                New-Item -Path $scriptDestinationFolder -ItemType Directory -Force | Out-Null
                Copy-Item $scriptSourcePath $scriptDestinationFile -Force
                Copy-Item "Dependencies\AutoHotKey" $scriptDestinationFolder -Recurse
            }
            . "$venvFolder\Scripts\Activate"
        }

        Run-Step "Install Python Dependencies" {
            if (Test-Path $scriptRequirements) {
                & $venvPython -m pip install --disable-pip-version-check -q -r $scriptRequirements | Out-Null
            }
        }

        Write-Host "`nLaunching Macro Testing Script..." -ForegroundColor Blue
        Write-Host "===================================================================================="
        & $venvPython -W ignore::SyntaxWarning $scriptDestinationFile
    }
}
