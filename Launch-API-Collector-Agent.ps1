# Launch-API-Collector-Agent.ps1
# Run this script with:
#   Set-ExecutionPolicy Unrestricted -Scope Process; .\Launch-API-Collector-Agent.ps1

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
        [string]$Status = $symbols["Info"]
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
            Write-Host "`r$($symbols.Success) $Message                        "
        } else {
            throw "Non-zero exit code"
        }
    } catch {
        Write-Host "`r$($symbols.Fail) $Message - Failed: $_                        " -ForegroundColor Red
        exit 1
    }
}

Clear-Host
Write-Host "Deploying Borealis API Collector Agent..." -ForegroundColor Green
Write-Host "===================================================================================="

# ---------------------- Path Definitions ----------------------
$venvFolder               = "Borealis-API-Collector-Agent"
$agentSourcePath          = "Data\Agent\api-collector-agent.py"
$agentRequirements        = "Data\Agent\requirements.txt"
$agentDestinationFolder   = "$venvFolder\Agent"
$agentDestinationFile     = "$agentDestinationFolder\api-collector-agent.py"

# ---------------------- Create Python Virtual Environment & Copy Agent ----------------------
Run-Step "Create Virtual Python Environment for Collector Agent" {
    if (!(Test-Path "$venvFolder\Scripts\Activate")) {
        python -m venv $venvFolder | Out-Null
    }

    # Copy Agent Script
    if (Test-Path $agentSourcePath) {
        if (Test-Path $agentDestinationFolder) {
            Remove-Item -Recurse -Force $agentDestinationFolder | Out-Null
        }
        New-Item -Path $agentDestinationFolder -ItemType Directory -Force | Out-Null
        Copy-Item -Path $agentSourcePath -Destination $agentDestinationFile -Force
    } else {
        Write-Host "`r$($symbols.Info) Warning: Agent script not found at '$agentSourcePath', skipping copy." -ForegroundColor Yellow
    }

    . "$venvFolder\Scripts\Activate"
}

# ---------------------- Install Python Dependencies ----------------------
Run-Step "Install Python Dependencies for Collector Agent" {
    if (Test-Path $agentRequirements) {
        pip install -q -r $agentRequirements 2>&1 | Out-Null
    } else {
        Write-Host "`r$($symbols.Info) Agent-specific requirements.txt not found at '$agentRequirements', skipping Python packages." -ForegroundColor Yellow
    }
}

# ---------------------- Launch Agent ----------------------
Push-Location $venvFolder
Write-Host "`nLaunching Borealis API Collector Agent..." -ForegroundColor Green
Write-Host "===================================================================================="
Write-Host "$($symbols.Running) Starting Agent..." -NoNewline
python "Agent\api-collector-agent.py"
Pop-Location
