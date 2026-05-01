# ======================================================
# Agent_Unit_Tests.ps1
# Description: Runs Borealis Agent unit tests on Windows PowerShell.
#
# API Endpoints (if applicable): None
# ======================================================

[CmdletBinding()]
param(
    [int]$TimeoutSeconds = 900,
    [string]$PythonPath = "",
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
        "device-audit",
        "file-management",
        "heartbeat",
        "remote-shell",
        "roles",
        "runtime",
        "scripts",
        "software",
        "tokens",
        "tray",
        "updates",
        "vnc",
        "wireguard"
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

function Add-ExistingPath {
    param(
        [System.Collections.Generic.List[string]]$Targets,
        [string]$Path
    )
    $fullPath = Join-Path $ProjectRoot $Path
    if (Test-Path $fullPath) {
        [void]$Targets.Add($Path)
    }
}

function Get-AgentPytestTargets {
    param([string]$DomainName)
    $testRoot = "Data/Agent/Unit_Tests"
    $targets = New-Object System.Collections.Generic.List[string]

    switch ($DomainName) {
        "all" {
            Add-ExistingPath -Targets $targets -Path $testRoot
        }
        "device-audit" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_device_audit.py"
        }
        "file-management" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_system_file_management.py"
        }
        "heartbeat" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_system_heartbeat.py"
        }
        "remote-shell" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_remote_shell.py"
        }
        "roles" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_device_audit.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_remote_shell.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_script_exec_currentuser.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_script_exec_system.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_system_file_management.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_system_heartbeat.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_system_software_management.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_vnc.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_wireguard_tunnel.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_system_script_execution.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_system_software_management.py"
        }
        "runtime" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_agent_runtime_copy.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_agent_socket_supervisor.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_runtime_paths.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_session_runtime.py"
        }
        "scripts" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_script_exec_currentuser.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_script_exec_system.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_system_script_execution.py"
        }
        "software" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_system_software_management.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_system_software_management.py"
        }
        "tokens" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_refresh_token_storage.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_token_refresh.py"
        }
        "tray" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_agent_tray_restart.py"
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_tray_state.py"
        }
        "updates" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_update_helper.py"
        }
        "vnc" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_vnc.py"
        }
        "wireguard" {
            Add-ExistingPath -Targets $targets -Path "$testRoot/test_role_wireguard_tunnel.py"
        }
    }

    return $targets.ToArray()
}

$Python = Resolve-TestPython
$LogPath = Join-Path $ResultsDir "agent-pytest.log"
$XmlPath = Join-Path $ResultsDir "agent-pytest.xml"
$SummaryPath = Join-Path $ResultsDir "summary.txt"
$PytestTargets = Get-AgentPytestTargets -DomainName $RequestedDomain
if ($PytestTargets.Count -eq 0) {
    Write-Host "No Agent Python unit tests found for domain $RequestedDomain." -ForegroundColor Red
    exit 2
}
$PytestArgs = @("-m", "pytest", "-q") + $PytestTargets + @("--junitxml", $XmlPath)

Write-Host "==> Agent Python unit tests ($RequestedDomain)"
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
    "Domain: $RequestedDomain",
    "Results: $ResultsDir",
    "Python status: $status",
    "Overall status: $status"
) | Out-File -FilePath $SummaryPath -Encoding utf8

Write-Host "Results written to $ResultsDir"
exit $status
