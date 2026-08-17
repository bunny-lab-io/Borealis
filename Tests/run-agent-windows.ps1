[CmdletBinding()]
param(
    [int]$TimeoutSeconds = 900,
    [string]$ResultsDir = ""
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

$TestsRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = (Resolve-Path (Join-Path $TestsRoot "..")).Path
$ModuleRoot = Join-Path $RepoRoot "Data\Agent"
if (-not $ResultsDir) {
    $stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
    $ResultsDir = if ($env:BOREALIS_UNIT_TEST_RESULTS_DIR) { $env:BOREALIS_UNIT_TEST_RESULTS_DIR } else { Join-Path $RepoRoot "Unit_Test_Results\agent-windows-$stamp" }
}
New-Item -ItemType Directory -Path $ResultsDir -Force | Out-Null

function Invoke-Bounded {
    param(
        [string]$Label,
        [string]$FilePath,
        [string[]]$Arguments,
        [string]$WorkingDirectory,
        [string]$LogPath,
        [hashtable]$Environment = @{}
    )
    Write-Host "==> $Label"
    $saved = @{}
    foreach ($key in $Environment.Keys) {
        $saved[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
        [Environment]::SetEnvironmentVariable($key, [string]$Environment[$key], "Process")
    }
    try {
        $process = Start-Process -FilePath $FilePath -ArgumentList $Arguments -WorkingDirectory $WorkingDirectory -NoNewWindow -PassThru -RedirectStandardOutput $LogPath -RedirectStandardError "$LogPath.stderr"
        if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
            $process.Kill($true)
            throw "$Label timed out after $TimeoutSeconds seconds."
        }
        if ($process.ExitCode -ne 0) {
            Get-Content "$LogPath.stderr" -ErrorAction SilentlyContinue | Write-Error
            throw "$Label failed with status $($process.ExitCode)."
        }
    } finally {
        foreach ($key in $saved.Keys) {
            [Environment]::SetEnvironmentVariable($key, $saved[$key], "Process")
        }
    }
}

$Go = (Get-Command go -ErrorAction Stop).Source
$GoFmt = Join-Path (Split-Path -Parent $Go) "gofmt.exe"
$GoFiles = git -C $RepoRoot ls-files -- "Data/Agent/*.go"
$Unformatted = & $GoFmt -l @GoFiles
if ($Unformatted) { throw "GO FORMAT FAIL: $($Unformatted -join ', ')" }

$TidyRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("borealis-agent-tidy-" + [guid]::NewGuid().ToString("N"))
$BuildRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("borealis-agent-build-" + [guid]::NewGuid().ToString("N"))
try {
    Copy-Item -Path $ModuleRoot -Destination $TidyRoot -Recurse
    Invoke-Bounded -Label "Agent Go module tidy" -FilePath $Go -Arguments @("mod", "tidy") -WorkingDirectory $TidyRoot -LogPath (Join-Path $ResultsDir "agent-go-tidy.log")
    $ModDifference = Compare-Object (Get-Content (Join-Path $ModuleRoot "go.mod")) (Get-Content (Join-Path $TidyRoot "go.mod"))
    $SumDifference = Compare-Object (Get-Content (Join-Path $ModuleRoot "go.sum")) (Get-Content (Join-Path $TidyRoot "go.sum"))
    if ($ModDifference -or $SumDifference) {
        throw "GO MODULE FAIL: go mod tidy changes Agent go.mod or go.sum."
    }

    Invoke-Bounded -Label "Agent Go vet" -FilePath $Go -Arguments @("vet", "./...") -WorkingDirectory $ModuleRoot -LogPath (Join-Path $ResultsDir "agent-go-vet.log")
    Invoke-Bounded -Label "Agent native Windows tests" -FilePath $Go -Arguments @("test", "./...") -WorkingDirectory $ModuleRoot -LogPath (Join-Path $ResultsDir "agent-go-test.log")

    Copy-Item -Path $ModuleRoot -Destination $BuildRoot -Recurse
    $Icon = Join-Path $BuildRoot "Agent.syso"
    if (Test-Path $Icon) { Copy-Item $Icon (Join-Path $BuildRoot "cmd\agent\agent_windows.syso") -Force }
    Invoke-Bounded -Label "Agent Windows build" -FilePath $Go -Arguments @("build", "-trimpath", "-buildvcs=false", "-o", (Join-Path $ResultsDir "Agent-windows-amd64.exe"), "./cmd/agent") -WorkingDirectory $BuildRoot -LogPath (Join-Path $ResultsDir "agent-windows-build.log") -Environment @{ GOOS = "windows"; GOARCH = "amd64"; CGO_ENABLED = "0"; GOWORK = "off" }
    Invoke-Bounded -Label "Agent Linux build" -FilePath $Go -Arguments @("build", "-trimpath", "-buildvcs=false", "-o", (Join-Path $ResultsDir "Agent-linux-amd64"), "./cmd/agent") -WorkingDirectory $BuildRoot -LogPath (Join-Path $ResultsDir "agent-linux-build.log") -Environment @{ GOOS = "linux"; GOARCH = "amd64"; CGO_ENABLED = "0"; GOWORK = "off" }
} finally {
    Remove-Item -Path $TidyRoot -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -Path $BuildRoot -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host "Agent Windows validation passed. Results: $ResultsDir"
