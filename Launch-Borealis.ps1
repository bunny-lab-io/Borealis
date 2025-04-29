#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Launch-Borealis.ps1

<#
    Deploy-Borealis.ps1
    ----------------------
    This script deploys the Borealis Workflow Automation Tool with two modules:
      - Server (Web Dashboard)
      - Agent (Client / Data Collector)

    It begins by presenting a menu to the user. Based on the selection (1 or 2), the corresponding module is launched.

    Usage:
      Set-ExecutionPolicy Unrestricted -Scope Process; .\Launch-Borealis.ps1
#>

# ---------------------- Common Initialization & Visuals ----------------------
Clear-Host

<#
    Section: Progress Symbols & Helpers
    ------------------------------------
    Define symbols for UI feedback and helper functions to run steps with consistent
    progress/status output and error handling.
#>
$symbols = @{
    Success = [char]0x2705
    Running = [char]0x23F3
    Fail    = [char]0x274C
    Info    = [char]0x2139
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
        [string]    $Message,
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

# ---------------------- Bundle Executables Setup ----------------------
<#
    Section: Locate Bundled Runtimes
    --------------------------------
    Identify the directory of this script, then ensure our bundled Python and NodeJS
    executables are present. Add them to PATH for later use.
#>
$scriptDir  = Split-Path $MyInvocation.MyCommand.Path -Parent
$depsRoot   = Join-Path $scriptDir 'Dependencies'
$pythonExe  = Join-Path $depsRoot 'Python\python.exe'
$nodeExe    = Join-Path $depsRoot 'NodeJS\node.exe'
$npmCmd     = Join-Path (Split-Path $nodeExe) 'npm.cmd'
$npxCmd     = Join-Path (Split-Path $nodeExe) 'npx.cmd'

foreach ($tool in @($pythonExe, $nodeExe, $npmCmd, $npxCmd)) {
    if (-not (Test-Path $tool)) {
        Write-Host "`r$($symbols.Fail) Bundled executable not found at '$tool'." -ForegroundColor Red
        exit 1
    }
}

$env:PATH = '{0};{1};{2}' -f (Split-Path $pythonExe), (Split-Path $nodeExe), $env:PATH

# ---------------------- Menu Prompt & User Input ----------------------
Write-Host "Deploying Borealis - Workflow Automation Tool..." -ForegroundColor Blue
Write-Host "===================================================================================="
Write-Host "Please choose which module you want to launch / (re)deploy:"
Write-Host "- Server (Web Dashboard) [1]"
Write-Host "- Agent (Local/Remote Client) [2]"

$choice = Read-Host "Enter 1 or 2"

switch ($choice) {

    "1" {
        # ---------------------- Server Deployment Setup ----------------------
        Clear-Host
        Write-Host "Deploying Borealis - Workflow Automation Tool..." -ForegroundColor Blue
        Write-Host "===================================================================================="

        $venvFolder       = "Server"
        $dataSource       = "Data"
        $dataDestination  = "$venvFolder\Borealis"
        $customUIPath     = "$dataSource\Server\WebUI"
        $webUIDestination = "$venvFolder\web-interface"
        $venvPython       = Join-Path $venvFolder 'Scripts\python.exe'

        <#
            Step: Create Virtual Environment & Copy Server Assets
        #>
        Run-Step "Create Borealis Virtual Python Environment" {
            if (-not (Test-Path "$venvFolder\Scripts\Activate")) {
                & $pythonExe -m venv $venvFolder | Out-Null
            }
            if (Test-Path $dataSource) {
                Remove-Item $dataDestination -Recurse -Force -ErrorAction SilentlyContinue
                New-Item -Path $dataDestination -ItemType Directory -Force | Out-Null
                Copy-Item "$dataSource\Server\Python_API_Endpoints" $dataDestination -Recurse
                Copy-Item "$dataSource\Server\Sounds"                 $dataDestination -Recurse
                Copy-Item "$dataSource\Server\Workflows"              $dataDestination -Recurse
                Copy-Item "$dataSource\Server\server.py"              $dataDestination
            }
            if (-not (Test-Path $webUIDestination)) {
                & $npxCmd --yes create-react-app $webUIDestination | Out-Null
            }
            if (Test-Path $customUIPath) {
                Copy-Item "$customUIPath\*" $webUIDestination -Recurse -Force
            }
            Remove-Item "$webUIDestination\build" -Recurse -Force -ErrorAction SilentlyContinue
            . "$venvFolder\Scripts\Activate"
        }

        <#
            Step: Install Python Dependencies
        #>
        Run-Step "Install Python Dependencies into Virtual Python Environment" {
            if (Test-Path "$dataSource\Server\server-requirements.txt") {
                & $venvPython -m pip install -q -r "$dataSource\Server\server-requirements.txt" | Out-Null
            }
        }

        <#
            Step: NPM Install
        #>
        Run-Step "ReactJS Web Frontend: Install Necessary NPM Packages" {
            if (Test-Path "$webUIDestination\package.json") {
                Push-Location $webUIDestination
                $env:npm_config_loglevel = "silent"
                & $npmCmd install --silent --no-fund --audit=false | Out-Null
                Pop-Location
            }
        }

        <#
            Step: Build React App
        #>
        Run-Step "ReactJS Web Frontend: " {
            Push-Location $webUIDestination
            & $npmCmd run build
            Pop-Location
        }

        <#
            Step: Launch Flask Server
            --------------------------
            Change into the Server folder so server.py’s relative paths to web-interface/build
            resolve correctly, then invoke Python on the server script.
        #>
        Run-Step "Borealis: Launch Flask Server" {
            Push-Location (Join-Path $scriptDir 'Server')

            $py        = Join-Path $scriptDir 'Server\Scripts\python.exe'
            $server_py = Join-Path $scriptDir 'Server\Borealis\server.py'

            Write-Host "`nLaunching Borealis..." -ForegroundColor Green
            Write-Host "===================================================================================="
            Write-Host "$($symbols.Running) Python Flask Server Started..."
            Write-Host "$($symbols.Running) Preloading OCR Engines... Please be patient..."

            & $py $server_py

            Pop-Location
        }
    }

    "2" {
        # ---------------------- Agent Deployment Setup ----------------------
        Clear-Host
        Write-Host "Deploying Borealis Agent..." -ForegroundColor Blue
        Write-Host "===================================================================================="

        $venvFolder             = "Agent"
        $agentSourcePath        = "Data\Agent\borealis-agent.py"
        $agentRequirements      = "Data\Agent\agent-requirements.txt"
        $agentDestinationFolder = "$venvFolder\Borealis"
        $agentDestinationFile   = "$venvFolder\Borealis\borealis-agent.py"

        # build the absolute path to python.exe inside the venv
        $venvPython = Join-Path $scriptDir $venvFolder
        $venvPython = Join-Path $venvPython 'Scripts\python.exe'

        <#
            Step: Create Virtual Environment & Copy Agent Script
        #>
        Run-Step "Create Virtual Python Environment for Agent" {
            if (-not (Test-Path "$venvFolder\Scripts\Activate")) {
                & $pythonExe -m venv $venvFolder
            }
            if (Test-Path $agentSourcePath) {
                Remove-Item $agentDestinationFolder -Recurse -Force -ErrorAction SilentlyContinue
                New-Item -Path $agentDestinationFolder -ItemType Directory -Force | Out-Null
                Copy-Item $agentSourcePath $agentDestinationFile -Force
            }
            . "$venvFolder\Scripts\Activate"
        }

        <#
            Step: Install Agent Dependencies
        #>
        Run-Step "Install Python Dependencies for Agent" {
            if (Test-Path $agentRequirements) {
                & $venvPython -m pip install -q -r $agentRequirements | Out-Null
            }
        }

        <#
            Step: Launch Agent
        #>
        Run-Step "Launch Borealis Agent" {
            Write-Host "`nLaunching Borealis Agent..." -ForegroundColor Blue
            Write-Host "===================================================================================="
            # call python with the absolute interpreter path and the absolute script path
            $agentScript = Join-Path $scriptDir $agentDestinationFile
            & $venvPython $agentScript
        }
    }

    default {
        Write-Host "Invalid selection. Exiting..." -ForegroundColor Yellow
        exit 1
    }
}
