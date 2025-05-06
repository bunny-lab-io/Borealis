#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Launch-Borealis.ps1

<#
    Borealis.ps1
    ----------------------
    This script deploys the Borealis Workflow Automation Tool with three modules:
      - Server (Web Dashboard)
      - Agent (Client / Data Collector)
      - Desktop App (Electron)

    It begins by presenting a menu to the user. Based on the selection (1, 2, or 3),
    the corresponding module is launched or deployed.

    Usage:
      Set-ExecutionPolicy Unrestricted -Scope Process; .\Borealis.ps1
#>

# ---------------------- ASCII Art Terminal Required Changes ----------------------
# Set the .NET Console output encoding to UTF8
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

# Change the Windows OEM code page to 65001 (UTF-8)
chcp.com 65001 > $null

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
Write-Host "Drag-&-Drop Automation Orchestration | Macros | Data Collection & Analysis" -ForegroundColor DarkGray

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

# ---------------------- Server Deployment / Operation Mode Variables ----------------------
# Define the default operation mode: production  | developer
[string]$borealis_operation_mode = 'production'

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
Write-Host " "
Write-Host "Please choose which function you want to launch:"
Write-Host " 1) Borealis Server" -ForegroundColor DarkGray
Write-Host " 2) Borealis Agent" -ForegroundColor DarkGray
Write-Host " 3) Build Electron App " -NoNewline -ForegroundColor DarkGray
Write-Host "[Experimental]" -ForegroundColor Red
Write-Host " 4) Package Self-Contained EXE of Server or Agent " -NoNewline -ForegroundColor DarkGray
Write-Host "[Experimental]" -ForegroundColor Red
Write-Host "Type a number and press " -NoNewLine
Write-Host "<ENTER>" -ForegroundColor DarkCyan
$choice = Read-Host
switch ($choice) {

    "1" {
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
        # Agent Deployment (Client / Data Collector)
        Write-Host " "
        Write-Host "Deploying Borealis Agent..." -ForegroundColor Blue
        
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

    "4" {
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
}
