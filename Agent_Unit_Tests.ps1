# ======================================================
# Agent_Unit_Tests.ps1
# Description: Runs the full Borealis Agent unit test lane on Windows PowerShell.
#
# API Endpoints (if applicable): None
# ======================================================

[CmdletBinding()]
param(
    [int]$TimeoutSeconds = 900,
    [string]$PythonPath = "",
    [string]$ResultsDir = ""
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
New-Item -ItemType Directory -Path $ResultsDir -Force | Out-Null

function Test-PythonHasPytest {
    param([string]$Candidate)
    if (-not $Candidate -or -not (Test-Path $Candidate -PathType Leaf)) { return $false }
    $env:PYTHONDONTWRITEBYTECODE = "1"
    & $Candidate -c "import pytest" *> $null
    return ($LASTEXITCODE -eq 0)
}

function Resolve-TestPython {
    $candidates = @()
    if ($PythonPath) { $candidates += $PythonPath }
    if ($env:BOREALIS_AGENT_TEST_PYTHON) { $candidates += $env:BOREALIS_AGENT_TEST_PYTHON }
    $candidates += @(
        (Join-Path $ProjectRoot "Agent\Scripts\python.exe"),
        (Join-Path $ProjectRoot "Engine\Scripts\python.exe"),
        (Join-Path $ProjectRoot "Dependencies\Python\python.exe")
    )
    $pythonCmd = Get-Command python -ErrorAction SilentlyContinue
    if ($pythonCmd) { $candidates += $pythonCmd.Source }
    $pyCmd = Get-Command py -ErrorAction SilentlyContinue
    if ($pyCmd) { $candidates += $pyCmd.Source }

    foreach ($candidate in $candidates) {
        if (Test-PythonHasPytest -Candidate $candidate) {
            return $candidate
        }
    }
    throw "Python executable with pytest not found. Install pytest or set BOREALIS_AGENT_TEST_PYTHON."
}

$Python = Resolve-TestPython
$LogPath = Join-Path $ResultsDir "agent-pytest.log"
$XmlPath = Join-Path $ResultsDir "agent-pytest.xml"
$SummaryPath = Join-Path $ResultsDir "summary.txt"
$PytestArgs = @("-m", "pytest", "-q", "Data/Agent/Unit_Tests", "--junitxml", $XmlPath)

Write-Host "==> Agent Python unit tests"
$job = Start-Job -ArgumentList $Python, $PytestArgs, $ProjectRoot, $LogPath -ScriptBlock {
    param(
        [string]$PythonExecutable,
        [string[]]$Arguments,
        [string]$Root,
        [string]$LogFile
    )
    $env:PYTHONDONTWRITEBYTECODE = "1"
    $env:BOREALIS_PROJECT_ROOT = $Root
    Set-Location $Root
    & $PythonExecutable @Arguments *> $LogFile
    if ($null -eq $LASTEXITCODE) { return 0 }
    return $LASTEXITCODE
}

$completed = Wait-Job -Job $job -Timeout $TimeoutSeconds
if (-not $completed) {
    Stop-Job -Job $job -Force
    "Agent Python unit tests timed out after $TimeoutSeconds seconds." | Out-File -FilePath $LogPath -Append -Encoding utf8
    $status = 124
} else {
    $status = [int](Receive-Job -Job $job)
}
Remove-Job -Job $job -Force

if ($status -ne 0) {
    Write-Host "Agent Python unit tests failed with status $status. Log: $LogPath" -ForegroundColor Red
} else {
    Write-Host "Agent Python unit tests passed. Log: $LogPath"
}

@(
    "Borealis Agent unit test run",
    "Results: $ResultsDir",
    "Python status: $status",
    "Overall status: $status"
) | Out-File -FilePath $SummaryPath -Encoding utf8

Write-Host "Results written to $ResultsDir"
exit $status
