#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Launch-Borealis.ps1

<#
    Deploy-Borealis.ps1
    ----------------------
    This script deploys the Borealis Workflow Automation Tool with three modules:
      - Server (Web Dashboard)
      - Agent (Client / Data Collector)
      - Desktop App (Electron)

    It begins by presenting a menu to the user. Based on the selection (1, 2, or 3),
    the corresponding module is launched or deployed.

    Usage:
      Set-ExecutionPolicy Unrestricted -Scope Process; .\Launch-Borealis.ps1
#>

# ---------------------- Common Initialization & Visuals ----------------------
Clear-Host

# ASCII Art Banner
@'
____                       _ _     
| __ )  ___  _ __ ___  __ _| (_)___ 
|  _ \ / _ \| '__/ _ \/ _` | | / __|
| |_) | (_) | | |  __/ (_| | | \__ \
|____/ \___/|_|  \___|\__,_|_|_|___/

'@ | Write-Host


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

# ---------------------- Bundle Executables Setup ----------------------
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
Write-Host "Workflow Automation Tool" -ForegroundColor Blue
Write-Host "===================================================================================="
Write-Host " "
Write-Host "Please choose which function you want to launch / (re)deploy:"
Write-Host "- Server (Web Dashboard) [1]"
Write-Host "- Agent (Local/Remote Client) [2]"
#Write-Host "- Desktop App (Electron) ((Run Step 1 Beforehand)) [3]"

$choice = Read-Host "Enter 1 or 2"

switch ($choice) {

    "1" {
        # Server Deployment (Web Dashboard)
        Clear-Host
        Write-Host "Deploying Borealis - Web Dashboard..." -ForegroundColor Blue
        Write-Host "===================================================================================="

        $venvFolder       = "Server"
        $dataSource       = "Data"
        $dataDestination  = "$venvFolder\Borealis"
        $customUIPath     = "$dataSource\Server\WebUI"
        $webUIDestination = "$venvFolder\web-interface"
        $venvPython       = Join-Path $venvFolder 'Scripts\python.exe'

        # Create Virtual Environment & Copy Server Assets
        Run-Step "Create Borealis Virtual Python Environment" {
            if (-not (Test-Path "$venvFolder\Scripts\Activate")) {
                & $pythonExe -m venv $venvFolder | Out-Null
            }
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

        # Copy Vite WebUI assets (no CRA)
        Run-Step "Setup Vite WebUI assets" {
            if (Test-Path $webUIDestination) {
                Remove-Item $webUIDestination -Recurse -Force -ErrorAction SilentlyContinue
            }
            New-Item -Path $webUIDestination -ItemType Directory -Force | Out-Null
            Copy-Item "$customUIPath\*" $webUIDestination -Recurse -Force
        }

        # NPM Install for WebUI
        Run-Step "Vite Web Frontend: Install NPM Packages" {
            Push-Location $webUIDestination
            $env:npm_config_loglevel = "silent"
            & $npmCmd install --silent --no-fund --audit=false | Out-Null
            Pop-Location
        }

        # ---------------------- Dev-mode Vite (HMR) ----------------------
        Run-Step "Vite Web Frontend: Start Dev Server" {
            Push-Location $webUIDestination
            # Launch Vite in watch/HMR mode in a new process
            Start-Process -NoNewWindow -FilePath $npmCmd -ArgumentList @("run", "dev")
            Pop-Location
        }

        # Launch Flask Server
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
        # Agent Deployment (Client / Data Collector)
        Clear-Host
        Write-Host "Deploying Borealis Agent..." -ForegroundColor Blue
        Write-Host "===================================================================================="
        
        $venvFolder             = "Agent"
        $agentSourcePath        = "Data\Agent\borealis-agent.py"
        $agentRequirements      = "Data\Agent\agent-requirements.txt"
        $agentDestinationFolder = "$venvFolder\Borealis"
        $agentDestinationFile   = "$venvFolder\Borealis\borealis-agent.py"
        $venvPython = Join-Path $scriptDir $venvFolder | Join-Path -ChildPath 'Scripts\python.exe'

        Run-Step "Create Virtual Python Environment" {
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

        Run-Step "Install Python Dependencies" {
            if (Test-Path $agentRequirements) {
                & $venvPython -m pip install --disable-pip-version-check -q -r $agentRequirements | Out-Null
            }
        }

        Write-Host "`nLaunching Borealis Agent..." -ForegroundColor Blue
        Write-Host "===================================================================================="
        & $venvPython -W ignore::SyntaxWarning $agentDestinationFile
    }

    "3" {
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

    default {
        Write-Host "Invalid selection. Exiting..." -ForegroundColor Yellow
        exit 1
    }
}
