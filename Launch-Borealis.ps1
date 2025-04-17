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

# Define common symbols for displaying progress and status
$symbols = @{
    Success = [char]0x2705
    Running = [char]0x23F3
    Fail    = [char]0x274C
    Info    = [char]0x2139
}

# Function to write progress steps with a given status symbol
function Write-ProgressStep {
    param (
        [string]$Message,
        [string]$Status = $symbols["Info"]
    )
    Write-Host "`r$Status $Message... " -NoNewline
}

# Function to run a step and check for errors
function Run-Step {
    param (
        [string]$Message,
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

# ---------------------- Menu Prompt & User Input ----------------------
Write-Host "Deploying Borealis - Workflow Automation Tool..." -ForegroundColor Blue
Write-Host "===================================================================================="
Write-Host "Please choose which module you want to launch / (re)deploy:"
Write-Host "- Server (Web Dashboard) [1]"
Write-Host "- Agent (Local/Remote Client) [2]"

$choice = Read-Host "Enter 1 or 2"

switch ($choice) {

    # ---------------------- Server Module ----------------------
    "1" {
        Clear-Host
        Write-Host "Deploying Borealis - Workflow Automation Tool..." -ForegroundColor Blue
        Write-Host "===================================================================================="

        # ---------------------- Server: Environment & Dependency Checks ----------------------
        # Check if Node.js is installed
        if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
            Write-Host "`r$($symbols.Fail) Node.js is not installed. Please install Node.js and try again." -ForegroundColor Red
            exit 1
        }
        
        # ---------------------- Server: Path Definitions ----------------------
        $venvFolder       = "Server"
        $dataSource       = "Data"
        $dataDestination  = "$venvFolder\Borealis"
        $customUIPath     = "$dataSource\WebUI"
        $webUIDestination = "$venvFolder\web-interface"

        # ---------------------- Server: Create Python Virtual Environment & Prepare Files ----------------------
        Run-Step "Create Borealis Virtual Python Environment" {
            # Create virtual environment if it does not exist
            if (!(Test-Path "$venvFolder\Scripts\Activate")) {
                python -m venv $venvFolder | Out-Null
            }
            # Copy server data if the Data folder exists
            if (Test-Path $dataSource) {
                if (Test-Path $dataDestination) {
                    Remove-Item -Recurse -Force $dataDestination | Out-Null
                }
                New-Item -Path $dataDestination -ItemType Directory -Force | Out-Null
                Copy-Item -Path "$dataSource\*" -Destination $dataDestination -Recurse
            } else {
                Write-Host "`r$($symbols.Info) Warning: Data folder not found, skipping copy." -ForegroundColor Yellow
            }
            # React UI Deployment: Create default React app if no deployment folder exists
            if (-not (Test-Path $webUIDestination)) {
                npx --yes create-react-app "$webUIDestination" | Out-Null
            }
            # Copy custom UI if it exists
            if (Test-Path $customUIPath) {
                Copy-Item -Path "$customUIPath\*" -Destination $webUIDestination -Recurse -Force
            } else {
                Write-Host "`r$($symbols.Info) No custom UI found, using default React app." -ForegroundColor Yellow
            }
            # Remove any existing React build folders
            if (Test-Path "$webUIDestination\build") {
                Remove-Item -Path "$webUIDestination\build" -Recurse -Force
            }
            # Activate the Python virtual environment
            . "$venvFolder\Scripts\Activate"
        }

        # ---------------------- Server: Install Python Dependencies ----------------------
        Run-Step "Install Python Dependencies into Virtual Python Environment" {
            if (Test-Path "requirements.txt") {
                pip install -q -r requirements.txt 2>&1 | Out-Null
            } else {
                Write-Host "`r$($symbols.Info) No requirements.txt found, skipping Python packages." -ForegroundColor Yellow
            }
        }

        # ---------------------- Server: Build React App ----------------------
        Run-Step "ReactJS Web Frontend: Install Necessary NPM Packages" {
            $packageJsonPath = Join-Path $webUIDestination "package.json"
            if (Test-Path $packageJsonPath) {
                Push-Location $webUIDestination
                $env:npm_config_loglevel = "silent"

                # Configure packages to install using <ProjectRoot>/Data/WebUI/package.json
                npm install --silent --no-fund --audit=false 2>&1 | Out-Null

                Pop-Location
            }
        }

        Run-Step "ReactJS Web Frontend: Build App" {
            Push-Location $webUIDestination
            # Build the React app (output is visible for development)
            npm run build
            Pop-Location
        }

        # ---------------------- Server: Launch Flask Server ----------------------
        Run-Step "Borealis: Launch Flask Server" {
            Push-Location $venvFolder
            Write-Host "`nLaunching Borealis..." -ForegroundColor Green
            Write-Host "===================================================================================="
            Write-Host "$($symbols.Running) Python Flask Server Started..."
            Write-Host "$($symbols.Running) Preloading OCR Engines... Please be patient..."
            python "Borealis\server.py"
            Pop-Location
        }
    }

    # ---------------------- Agent Module ----------------------
    "2" {
        Clear-Host
        Write-Host "Deploying Borealis Agent..." -ForegroundColor Blue
        Write-Host "===================================================================================="

        # ---------------------- Agent: Path Definitions ----------------------
        $venvFolder               = "Agent"
        $agentSourcePath          = "Data\Agent\borealis-agent.py"
        $agentRequirements        = "Data\Agent\requirements.txt"
        $agentDestinationFolder   = "$venvFolder\Agent"
        $agentDestinationFile     = "$agentDestinationFolder\borealis-agent.py"

        # ---------------------- Agent: Create Python Virtual Environment & Copy Agent Script ----------------------
        Run-Step "Create Virtual Python Environment for Agent" {
            # Create virtual environment for the agent
            if (!(Test-Path "$venvFolder\Scripts\Activate")) {
                python -m venv $venvFolder
            }
            # Copy the agent script if it exists
            if (Test-Path $agentSourcePath) {
                if (Test-Path $agentDestinationFolder) {
                    Remove-Item -Recurse -Force $agentDestinationFolder
                }
                New-Item -Path $agentDestinationFolder -ItemType Directory -Force
                Copy-Item -Path $agentSourcePath -Destination $agentDestinationFile -Force
            } else {
                Write-Host "`r$($symbols.Info) Warning: Agent script not found at '$agentSourcePath', skipping copy." -ForegroundColor Yellow
            }
            # Activate the virtual environment
            . "$venvFolder\Scripts\Activate"
        }

        # ---------------------- Agent: Install Python Dependencies ----------------------
        Run-Step "Install Python Dependencies for Agent" {
            if (Test-Path $agentRequirements) {
                pip install -q -r $agentRequirements 2>&1
            } else {
                Write-Host "`r$($symbols.Info) Agent-specific requirements.txt not found at '$agentRequirements', skipping Python packages." -ForegroundColor Yellow
            }
        }

        # ---------------------- Agent: Launch Agent Script ----------------------
        Push-Location $venvFolder
        Write-Host "`nLaunching Borealis Agent..." -ForegroundColor Blue
        Write-Host "===================================================================================="
        python "Agent\borealis-agent.py"
        Pop-Location
    }

    # ---------------------- Default (Invalid Selection) ----------------------
    default {
        Write-Host "Invalid selection. Exiting..." -ForegroundColor Yellow
        exit 1
    }
}
