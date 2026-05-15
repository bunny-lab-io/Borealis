# ======================================================
# Agent_Unit_Tests.ps1
# Description: Runs Borealis Agent unit tests on Windows PowerShell.
#
# API Endpoints (if applicable): None
# ======================================================

[CmdletBinding()]
param(
    [int]$TimeoutSeconds = 900,
    [string]$ResultsDir = "",
    [string]$Domain = "",
    [switch]$ListDomains
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

$ScriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = if ($env:BOREALIS_PROJECT_ROOT) { $env:BOREALIS_PROJECT_ROOT } else { $ScriptRoot }
$Timestamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
if (-not $ResultsDir) {
    if ($env:BOREALIS_UNIT_TEST_RESULTS_DIR) {
        $ResultsDir = $env:BOREALIS_UNIT_TEST_RESULTS_DIR
    } else {
        $ResultsDir = Join-Path $ProjectRoot "Unit_Test_Results\agent-$Timestamp"
    }
}
$RequestedDomain = if ($Domain) { $Domain } elseif ($env:BOREALIS_AGENT_UNIT_TEST_DOMAIN) { $env:BOREALIS_AGENT_UNIT_TEST_DOMAIN } else { "all" }

function Get-AgentDomains {
    return @(
        "all",
        "go-agent"
    )
}

if ($ListDomains) {
    Get-AgentDomains | ForEach-Object { Write-Output $_ }
    exit 0
}

if (-not ((Get-AgentDomains) -contains $RequestedDomain)) {
    Write-Host "Unknown Agent test domain: $RequestedDomain" -ForegroundColor Red
    Write-Host "Available domains:"
    Get-AgentDomains | ForEach-Object { Write-Host $_ }
    exit 2
}

New-Item -ItemType Directory -Path $ResultsDir -Force | Out-Null

$LogPath = Join-Path $ResultsDir "agent-go.log"
$SummaryPath = Join-Path $ResultsDir "summary.txt"

function Resolve-Go {
    $goCmd = Get-Command go -ErrorAction SilentlyContinue
    if ($goCmd) { return $goCmd.Source }
    $goRoot = Join-Path $ProjectRoot "Dependencies\Go"
    if (Test-Path $goRoot) {
        $candidate = Get-ChildItem -Path $goRoot -Filter go.exe -Recurse -ErrorAction SilentlyContinue |
            Where-Object { $_.FullName -like "*\bin\go.exe" } |
            Select-Object -First 1
        if ($candidate) { return $candidate.FullName }
    }
    throw "Go executable not found. Install Go 1.22+ or run Data\Agent\build-agent.sh from Linux first."
}

function Invoke-Go {
    param(
        [string]$GoPath,
        [string[]]$Arguments,
        [hashtable]$EnvVars = @{}
    )
    foreach ($entry in $EnvVars.GetEnumerator()) {
        [Environment]::SetEnvironmentVariable($entry.Key, [string]$entry.Value, "Process")
    }
    & $GoPath @Arguments *> $LogPath
    return $LASTEXITCODE
}

$StagedWindowsIcon = ""
function Stage-AgentWindowsIcon {
    $source = Join-Path $ProjectRoot "Data\Agent\Agent.syso"
    $target = Join-Path $ProjectRoot "Data\Agent\cmd\agent\agent_windows.syso"
    if (Test-Path $source) {
        Copy-Item -Path $source -Destination $target -Force
        $script:StagedWindowsIcon = $target
    }
}

function Clear-AgentWindowsIcon {
    if ($script:StagedWindowsIcon -and (Test-Path $script:StagedWindowsIcon)) {
        Remove-Item -Path $script:StagedWindowsIcon -Force -ErrorAction SilentlyContinue
    }
    $script:StagedWindowsIcon = ""
}

Write-Host "==> Go Agent unit/build checks ($RequestedDomain)"
try {
    $Go = Resolve-Go
    $AgentRoot = Join-Path $ProjectRoot "Data\Agent"
    Set-Location $AgentRoot
    $status = Invoke-Go -GoPath $Go -Arguments @("mod", "tidy")
    if ($status -eq 0) {
        $status = Invoke-Go -GoPath $Go -Arguments @("test", "./...")
    }
    if ($status -eq 0) {
        Stage-AgentWindowsIcon
        $status = Invoke-Go -GoPath $Go -Arguments @("build", "-trimpath", "-buildvcs=false", "-o", (Join-Path $ResultsDir "Agent-windows-amd64.exe"), "./cmd/agent") -EnvVars @{ GOOS = "windows"; GOARCH = "amd64"; CGO_ENABLED = "0" }
        Clear-AgentWindowsIcon
    }
    if ($status -eq 0) {
        $status = Invoke-Go -GoPath $Go -Arguments @("build", "-trimpath", "-buildvcs=false", "-o", (Join-Path $ResultsDir "Agent-linux-amd64"), "./cmd/agent") -EnvVars @{ GOOS = "linux"; GOARCH = "amd64"; CGO_ENABLED = "0" }
    }
} finally {
    Clear-AgentWindowsIcon
}

if ($status -ne 0) {
    Write-Host "Go Agent checks failed with status $status. Log: $LogPath" -ForegroundColor Red
} else {
    Write-Host "Go Agent checks passed. Log: $LogPath"
}

@(
    "Borealis Agent unit test run",
    "Domain: $RequestedDomain",
    "Results: $ResultsDir",
    "Python status: skipped (legacy source moved to Data/Agent_Old)",
    "Go Agent status: $status",
    "Overall status: $status"
) | Out-File -FilePath $SummaryPath -Encoding utf8

Write-Host "Results written to $ResultsDir"
exit $status
