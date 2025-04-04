# Start_Windows - WebServer.ps1
# Run this script with:
#   Set-ExecutionPolicy Unrestricted -Scope Process; .\Start_Windows -WebServer.ps1

# ---------------------- Initialization & Visuals ----------------------
$symbols = @{
    Success = [char]0x2705
    Running = [char]0x23F3
    Fail    = [char]0x274C
    Info    = [char]0x2139
}

function Write-ProgressStep {
    param (
        [string]$Message,
        [string]$Status = $symbols["Info"]  # Ensure proper lookup
    )
    Write-Host "`r$Status $Message... " -NoNewline
}

function Run-Step {
    param (
        [string]$Message,
        [scriptblock]$Script
    )
    Write-ProgressStep -Message $Message -Status "$($symbols.Running)"
    try {
        & $Script
        if ($LASTEXITCODE -eq 0 -or $?) {
            Write-Host "`r$($symbols.Success) $Message                        "  # Fix symbol lookup
        } else {
            throw "Non-zero exit code"
        }
    } catch {
        Write-Host "`r$($symbols.Fail) $Message - Failed: $_                        " -ForegroundColor Red
        exit 1
    }
}

Clear-Host
Write-Host "Deploying Borealis - Workflow Automation Tool..." -ForegroundColor Green
Write-Host "===================================================================================="

# ---------------------- Node.js Check ----------------------
if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
    Write-Host "`r$($symbols.Fail) Node.js is not installed. Please install Node.js and try again." -ForegroundColor Red
    exit 1
}

# ---------------------- Path Definitions ----------------------
$venvFolder       = "Borealis-Workflow-Automation-Tool"
$dataSource       = "Data"
$dataDestination  = "$venvFolder\Borealis"
$customUIPath     = "$dataSource\WebUI"
$webUIDestination = "$venvFolder\web-interface"

# ---------------------- Create Python Virtual Environment & Prepare Borealis Files ----------------------
Run-Step "Create Borealis Virtual Python Environment" {
    if (!(Test-Path "$venvFolder\Scripts\Activate")) {
        python -m venv $venvFolder | Out-Null
    }
    # ---------------------- Copy Server Data ----------------------
    if (Test-Path $dataSource) {
        if (Test-Path $dataDestination) {
            Remove-Item -Recurse -Force $dataDestination | Out-Null
        }
        New-Item -Path $dataDestination -ItemType Directory -Force | Out-Null
        Copy-Item -Path "$dataSource\*" -Destination $dataDestination -Recurse
    } else {
        Write-Host "`r$($symbols.Info) Warning: Data folder not found, skipping copy." -ForegroundColor Yellow
    }

    # ---------------------- React UI Deployment ----------------------
    if (-not (Test-Path $webUIDestination)) {
        npx create-react-app $webUIDestination | Out-Null
    }

    if (Test-Path $customUIPath) {
        Copy-Item -Path "$customUIPath\*" -Destination $webUIDestination -Recurse -Force
    } else {
        Write-Host "`r$($symbols.Info) No custom UI found, using default React app." -ForegroundColor Yellow
    }

    # Remove Pre-Existing ReactJS Build Folder (If one Exists)
    if (Test-Path "$webUIDestination\build") {
        Remove-Item -Path "$webUIDestination\build" -Recurse -Force
    }

    # ---------------------- Activate Python Virtual Environment ----------------------
    . "$venvFolder\Scripts\Activate"
}

# ---------------------- Install Python Dependencies ----------------------
Run-Step "Install Python Dependencies into Virtual Python Environment" {
    if (Test-Path "requirements.txt") {
        pip install -q -r requirements.txt 2>&1 | Out-Null
    } else {
        Write-Host "`r$($symbols.Info) No requirements.txt found, skipping Python packages." -ForegroundColor Yellow
    }
}

# ---------------------- Build React App ----------------------
Run-Step "ReactJS Web Frontend: Install Necessary NPM Packages" {
    $packageJsonPath = Join-Path $webUIDestination "package.json"
    if (Test-Path $packageJsonPath) {
        Push-Location $webUIDestination
        $env:npm_config_loglevel = "silent"

        # Install NPM
        npm install --silent --no-fund --audit=false 2>&1 | Out-Null

        # Install React Resizable
        npm install --silent react-resizable --no-fund --audit=false | Out-Null

        # Install React Flow
        npm install --silent reactflow --no-fund --audit=false | Out-Null

        # Install Material UI Libraries
        npm install --silent @mui/material @mui/icons-material @emotion/react @emotion/styled --no-fund --audit=false 2>&1 | Out-Null

        Pop-Location
    }
}

Run-Step "ReactJS Web Frontend: Build App" {
    Push-Location $webUIDestination
    #npm run build | Out-Null # Suppress Compilation Output
    npm run build # Enabled during Development
    Pop-Location
}

# ---------------------- Launch Flask Server ----------------------
Push-Location $venvFolder
Write-Host "`nLaunching Borealis..." -ForegroundColor Green
Write-Host "===================================================================================="
Write-Host "$($symbols.Running) Starting Python Flask Server..." -NoNewline
python "Borealis\server.py"
Pop-Location
