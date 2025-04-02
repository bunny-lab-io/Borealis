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

# ---------------------- Create Python Virtual Environment ----------------------
Run-Step "Create Virtual Python Environment" {
    if (!(Test-Path "$venvFolder\Scripts\Activate")) {
        python -m venv $venvFolder | Out-Null
    }
}

# ---------------------- Copy Server Data ----------------------
Run-Step "Copy Borealis Server Data into Virtual Python Environment" {
    if (Test-Path $dataSource) {
        if (Test-Path $dataDestination) {
            Remove-Item -Recurse -Force $dataDestination | Out-Null
        }
        New-Item -Path $dataDestination -ItemType Directory -Force | Out-Null
        Copy-Item -Path "$dataSource\*" -Destination $dataDestination -Recurse
    } else {
        Write-Host "`r$($symbols.Info) Warning: Data folder not found, skipping copy." -ForegroundColor Yellow
    }
}

# ---------------------- React UI Deployment ----------------------
Run-Step "Create a new ReactJS App in $webUIDestination" {
    if (-not (Test-Path $webUIDestination)) {
        npx create-react-app $webUIDestination | Out-Null
    }
}

Run-Step "Overwrite ReactJS App Files with Borealis ReactJS Files" {
    if (Test-Path $customUIPath) {
        Copy-Item -Path "$customUIPath\*" -Destination $webUIDestination -Recurse -Force
    } else {
        Write-Host "`r$($symbols.Info) No custom UI found, using default React app." -ForegroundColor Yellow
    }
}

Run-Step "Remove Existing ReactJS Build Folder (If Exists)" {
    if (Test-Path "$webUIDestination\build") {
        Remove-Item -Path "$webUIDestination\build" -Recurse -Force
    }
}

# ---------------------- Activate Python Virtual Environment ----------------------
Run-Step "Activate Virtual Python Environment" {
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
Run-Step "ReactJS App: Install NPM" {
    $packageJsonPath = Join-Path $webUIDestination "package.json"
    if (Test-Path $packageJsonPath) {
        Push-Location $webUIDestination
        $env:npm_config_loglevel = "silent"
        npm install --silent --no-fund --audit=false 2>&1 | Out-Null
        Pop-Location
    }
}

Run-Step "ReactJS App: Install React Resizable" {
    Push-Location $webUIDestination
    npm install react-resizable --no-fund --audit=false | Out-Null
    Pop-Location
}

Run-Step "ReactJS App: Install React Flow" {
    Push-Location $webUIDestination
    npm install reactflow --no-fund --audit=false | Out-Null
    Pop-Location
}

Run-Step "ReactJS App: Install Material UI Libraries" {
    Push-Location $webUIDestination
    $env:npm_config_loglevel = "silent"  # Force NPM to be completely silent
    npm install --silent @mui/material @mui/icons-material @emotion/react @emotion/styled --no-fund --audit=false 2>&1 | Out-Null
    Pop-Location
}

Run-Step "ReactJS App: Building App" {
    Push-Location $webUIDestination
    #npm run build | Out-Null
    npm run build
    Pop-Location
}

# ---------------------- Launch Flask Server ----------------------
Push-Location $venvFolder
Write-Host "`nLaunching Borealis..." -ForegroundColor Green
Write-Host "===================================================================================="
Write-Host "$($symbols.Running) Starting the Python Flask server..." -NoNewline
python "Borealis\server.py"
Write-Host "`r$($symbols.Success) Borealis Launched Successfully!"
Pop-Location
