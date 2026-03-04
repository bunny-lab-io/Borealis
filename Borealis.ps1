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
    [string]$EnrollmentCode = '',
    [string]$ServerUrl = ''
)

# Admin/Elevation helpers for Borealis runtime
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
    Write-Host "Borealis requires Administrator permissions for Engine and Agent tasks." -ForegroundColor Yellow -BackgroundColor Black
    Write-Host "Grant elevated permissions now? (Y/N)" -ForegroundColor Yellow -BackgroundColor Black
    $resp = Read-Host
    if ($resp -notin @('y','Y','yes','YES')) { return $false }

    $argTokens = @('-NoProfile','-ExecutionPolicy','Bypass','-File', $ScriptPath)
    if ($BoundParameters) {
        foreach ($entry in $BoundParameters.GetEnumerator()) {
            $key = $entry.Key
            $value = $entry.Value
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
        Start-Process -FilePath 'powershell.exe' -Verb RunAs -ArgumentList $argLine -WindowStyle Normal | Out-Null
        return $false  # stop current non-elevated instance
    } catch {
        Write-Host "Elevation was denied or failed." -ForegroundColor Red
        return $false
    }
}

# Preselect menu choices from CLI args (optional)
$choice = $null
$modeChoice = $null
$engineModeChoice = $null

$scriptPath = $PSCommandPath
if (-not $scriptPath -or $scriptPath -eq '') { $scriptPath = $MyInvocation.MyCommand.Definition }
if (-not (Request-BorealisElevation -ScriptPath $scriptPath -BoundParameters $PSBoundParameters -ExtraArgs $MyInvocation.UnboundArguments)) {
    exit 0
}

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

function Get-LatestWriteTime {
    param(
        [string]$Path
    )
    try {
        $item = Get-ChildItem -Path $Path -Recurse -Force -ErrorAction Stop |
            Sort-Object -Property LastWriteTime -Descending |
            Select-Object -First 1
        if ($item) { return $item.LastWriteTime }
    } catch {
        return [datetime]::MinValue
    }
    return [datetime]::MinValue
}

function Sync-EngineRuntime {
    param(
        [string]$SourceRoot,
        [string]$DestinationRoot
    )
    if (-not (Test-Path $SourceRoot)) { return $false }

    $needsSync = $false
    if (-not (Test-Path $DestinationRoot)) {
        $needsSync = $true
    } else {
        $sourceTime = Get-LatestWriteTime -Path $SourceRoot
        $destTime = Get-LatestWriteTime -Path $DestinationRoot
        if ($sourceTime -gt $destTime) { $needsSync = $true }
    }

    if (-not $needsSync) { return $false }

    if (Test-Path $DestinationRoot) {
        Remove-Item $DestinationRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
    New-Item -Path $DestinationRoot -ItemType Directory -Force | Out-Null

    Get-ChildItem -Path $SourceRoot -Force | ForEach-Object {
        if ($_.Name -ieq 'Assemblies') {
            return
        }
        Copy-Item -Path $_.FullName -Destination $DestinationRoot -Recurse -Force
    }
    return $true
}

function Sync-EngineAssembliesRuntime {
    param(
        [string]$SourceAssembliesRoot,
        [string]$RuntimeAssembliesRoot
    )

    if (-not (Test-Path $RuntimeAssembliesRoot)) {
        New-Item -Path $RuntimeAssembliesRoot -ItemType Directory -Force | Out-Null
    }

    if (-not (Test-Path $SourceAssembliesRoot)) {
        Write-Host "Engine assemblies source directory '$SourceAssembliesRoot' was not found. Skipping assemblies sync." -ForegroundColor Yellow
        return
    }

    foreach ($dbName in @('official.db', 'community.db')) {
        $sourceDb = Join-Path $SourceAssembliesRoot $dbName
        $runtimeDb = Join-Path $RuntimeAssembliesRoot $dbName

        if (-not (Test-Path $sourceDb)) {
            Write-Host "Engine assemblies source database '$sourceDb' was not found. Skipping '$dbName'." -ForegroundColor Yellow
            continue
        }

        foreach ($suffix in @('', '-wal', '-shm')) {
            $runtimeCandidate = "$runtimeDb$suffix"
            $sourceCandidate = "$sourceDb$suffix"

            if (Test-Path $runtimeCandidate) {
                Remove-Item -Path $runtimeCandidate -Force -ErrorAction SilentlyContinue
            }
            if (Test-Path $sourceCandidate) {
                Copy-Item -Path $sourceCandidate -Destination $runtimeCandidate -Force
            }
        }
    }

    $sourceUserDb = Join-Path $SourceAssembliesRoot 'user_created.db'
    $runtimeUserDb = Join-Path $RuntimeAssembliesRoot 'user_created.db'
    if (-not (Test-Path $runtimeUserDb) -and (Test-Path $sourceUserDb)) {
        foreach ($suffix in @('', '-wal', '-shm')) {
            $runtimeCandidate = "$runtimeUserDb$suffix"
            $sourceCandidate = "$sourceUserDb$suffix"
            if (Test-Path $sourceCandidate) {
                Copy-Item -Path $sourceCandidate -Destination $runtimeCandidate -Force
            }
        }
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

function Get-EngineLaunchStreamPaths {
    $engineLogDir = Ensure-EngineLogDir
    [PSCustomObject]@{
        StdOut = Join-Path $engineLogDir 'engine-launch.stdout.log'
        StdErr = Join-Path $engineLogDir 'engine-launch.stderr.log'
    }
}

function Get-EngineStartLabel {
    param(
        [string]$EngineMode
    )

    $resolvedMode = "production"
    if (-not [string]::IsNullOrWhiteSpace($EngineMode)) {
        $resolvedMode = $EngineMode.Trim().ToLowerInvariant()
    }

    if ($resolvedMode -eq "developer") {
        return "(Dev) Engine Started on https://localhost:5173"
    }

    return "(Production) Engine Started on https://localhost:5000"
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
        Invoke-WebRequest -Uri $Url -OutFile $DestinationPath
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

    # UltraVNC Server (always-on VNC backend for noVNC)
    Run-Step "Dependency: UltraVNC Server" {
        $uvncZipUrl = $env:BOREALIS_ULTRAVNC_ZIP_URL
        if (-not $uvncZipUrl) {
            $uvncZipUrl = "https://uvnc.eu/download/1640/UltraVNC_1640.zip"
        }
        $uvncMsiUrl = $env:BOREALIS_ULTRAVNC_MSI_URL
        if (-not $uvncMsiUrl) {
            $uvncMsiUrl = "https://uvnc.eu/download/1640/UltraVNC_1640_x64_Setup.msi"
        }
        $uvncInstallerUrl = $env:BOREALIS_ULTRAVNC_URL
        if (-not $uvncInstallerUrl) {
            $uvncInstallerUrl = "https://uvnc.eu/download/1640/UltraVNC_1640_x64_Setup.exe"
        }
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
            if (-not (Test-Path $sevenZipExe)) {
                throw "7-Zip CLI not found at: $sevenZipExe"
            }
            try {
                if (-not (Test-Path $uvncZipPath)) {
                    Invoke-WebRequest -Uri $uvncZipUrl -OutFile $uvncZipPath
                }
                if (Test-Path $uvncPayloadRoot) {
                    Remove-Item $uvncPayloadRoot -Recurse -Force -ErrorAction SilentlyContinue
                }
                New-Item -ItemType Directory -Path $uvncPayloadRoot -Force | Out-Null
                & $sevenZipExe x $uvncZipPath "-o$uvncPayloadRoot" -y | Out-Null
            } catch {
                Write-Host "UltraVNC zip download/extract failed. Trying MSI fallback." -ForegroundColor Yellow
            }
            $uvncExe = Get-ChildItem -Path $uvncPayloadRoot -Recurse -Filter "winvnc*.exe" -ErrorAction SilentlyContinue |
                Select-Object -First 1
        }

        if (-not $uvncExe) {
            try {
                if (-not (Test-Path $uvncMsiPath)) {
                    Invoke-WebRequest -Uri $uvncMsiUrl -OutFile $uvncMsiPath
                }
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
            if (-not (Test-Path $uvncInstallerPath)) {
                Invoke-WebRequest -Uri $uvncInstallerUrl -OutFile $uvncInstallerPath
            }
            if (-not (Test-Path $sevenZipExe)) {
                throw "7-Zip CLI not found at: $sevenZipExe"
            }
            try {
                if (-not (Test-Path $uvncPayloadRoot)) {
                    New-Item -ItemType Directory -Path $uvncPayloadRoot -Force | Out-Null
                }
                & $sevenZipExe x $uvncInstallerPath "-o$uvncPayloadRoot" -y | Out-Null
            } catch {
                Write-Host "UltraVNC installer extraction failed." -ForegroundColor Yellow
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
                if (-not (Test-Path $passwordToolZip)) {
                    Invoke-WebRequest -Uri $passwordToolUrl -OutFile $passwordToolZip
                }
                if (Test-Path $sevenZipExe) {
                    $toolDir = Join-Path $uvncRoot "tools"
                    if (Test-Path $toolDir) {
                        Remove-Item $toolDir -Recurse -Force -ErrorAction SilentlyContinue
                    }
                    New-Item -ItemType Directory -Path $toolDir | Out-Null
                    & $sevenZipExe x $passwordToolZip "-o$toolDir" -y | Out-Null
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
    Ensure-SoftwareSASGeneration -LogName 'Install.log'
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
        $providedServerUrl = ''
        if ($ServerUrl -and $ServerUrl.Trim()) {
            $providedServerUrl = $ServerUrl.Trim()
        } elseif ($env:BOREALIS_SERVER_URL -and $env:BOREALIS_SERVER_URL.Trim()) {
            $providedServerUrl = $env:BOREALIS_SERVER_URL.Trim()
        }
        if ($providedServerUrl) {
            $inputUrl = $providedServerUrl
        } else {
            Write-Host ""; Write-Host "Set Borealis Server URL" -ForegroundColor DarkYellow
            $prompt = "Server URL [$currentUrl]"
            $inputUrl = Read-Host $prompt
            if (-not $inputUrl) { $inputUrl = $currentUrl }
            $inputUrl = $inputUrl.Trim()
            if (-not $inputUrl) { $inputUrl = $defaultUrl }
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
$Host.UI.RawUI.BackgroundColor = 'Black'
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
            $engineSourceAbsolute = Join-Path $scriptDir 'Data\Engine'
            $engineDataAbsolute = Join-Path $scriptDir 'Engine\Data\Engine'
            if (Sync-EngineRuntime -SourceRoot $engineSourceAbsolute -DestinationRoot $engineDataAbsolute) {
                Write-Host "Synced Engine runtime code from Data\\Engine." -ForegroundColor DarkCyan
            }
            Run-Step "Sync Engine Assembly Databases" {
                $runtimeAssemblies = Join-Path $scriptDir 'Engine\Assemblies'
                $sourceAssemblies = Join-Path $engineSourceAbsolute 'Assemblies'
                Sync-EngineAssembliesRuntime -SourceAssembliesRoot $sourceAssemblies -RuntimeAssembliesRoot $runtimeAssemblies
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
                $engineStartLabel = Get-EngineStartLabel -EngineMode $engineOperationMode
                Write-Host "$($symbols.Running) $engineStartLabel" -ForegroundColor DarkCyan
                $engineLaunchStreams = Get-EngineLaunchStreamPaths
                & $py -m Data.Engine.bootstrapper 1>> $engineLaunchStreams.StdOut 2>> $engineLaunchStreams.StdErr
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
            Sync-EngineAssembliesRuntime -SourceAssembliesRoot $sourceAssemblies -RuntimeAssembliesRoot $runtimeAssemblies

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
                    $engineLogDir = Ensure-EngineLogDir
                    $viteBuildStdOut = Join-Path $engineLogDir 'vite-build.stdout.log'
                    $viteBuildStdErr = Join-Path $engineLogDir 'vite-build.stderr.log'
                    & $npmCmd run build 1>> $viteBuildStdOut 2>> $viteBuildStdErr
                    if ($LASTEXITCODE -ne 0) {
                        Write-ViteLog ("npm run build failed with code {0}. stderr log: {1}" -f $LASTEXITCODE, $viteBuildStdErr) 'vite-build'
                        throw "Vite production build failed. Review $viteBuildStdErr for details."
                    }
                    Write-ViteLog "npm run build completed successfully." 'vite-build'
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
            $engineStartLabel = Get-EngineStartLabel -EngineMode $engineOperationMode
            Write-Host "$($symbols.Running) $engineStartLabel" -ForegroundColor DarkCyan
            $engineLaunchStreams = Get-EngineLaunchStreamPaths
            & $py -m Data.Engine.bootstrapper 1>> $engineLaunchStreams.StdOut 2>> $engineLaunchStreams.StdErr
            if ($previousEngineMode) { $env:BOREALIS_ENGINE_MODE = $previousEngineMode } else { Remove-Item Env:BOREALIS_ENGINE_MODE -ErrorAction SilentlyContinue }
            if ($previousEnginePort) { $env:BOREALIS_ENGINE_PORT = $previousEnginePort } else { Remove-Item Env:BOREALIS_ENGINE_PORT -ErrorAction SilentlyContinue }
            if ($previousProjectRoot) { $env:BOREALIS_PROJECT_ROOT = $previousProjectRoot } else { Remove-Item Env:BOREALIS_PROJECT_ROOT -ErrorAction SilentlyContinue }
            Pop-Location
        }
    }
    
    "2" {
        $host.UI.RawUI.WindowTitle = "Borealis Agent"
        Write-Host " "
        if (-not (Test-IsAdmin)) {
            Write-Host "Administrator permissions are required to deploy the Borealis Agent." -ForegroundColor Red
            return
        }
        Write-Host "Escalated Permissions Granted > Agent is Eligible for Deployment." -ForegroundColor Green
        Write-Host "Deploying Borealis Agent (fresh install/update path)..." -ForegroundColor Cyan
        InstallOrUpdate-BorealisAgent
        break
    }
    default { Write-Host "Invalid selection. Exiting..." -ForegroundColor Red; exit 1 }
}
