[CmdletBinding()]
param(
    [int]$TimeoutSeconds = 900,
    [string]$ResultsDir = "",
    [string]$Domain = "",
    [switch]$ListDomains
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

if ($ListDomains) {
    Write-Output "all"
    Write-Output "go-agent"
    exit 0
}

$RequestedDomain = if ($Domain) { $Domain } elseif ($env:BOREALIS_AGENT_UNIT_TEST_DOMAIN) { $env:BOREALIS_AGENT_UNIT_TEST_DOMAIN } else { "all" }
if (@("all", "go-agent") -notcontains $RequestedDomain) {
    Write-Error "Unknown Agent test domain: $RequestedDomain"
    exit 2
}

$ScriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = (Resolve-Path (Join-Path $ScriptRoot "..\..\..")).Path
$Runner = Join-Path $RepoRoot "Tests\run-agent-windows.ps1"
& $Runner -TimeoutSeconds $TimeoutSeconds -ResultsDir $ResultsDir
exit $LASTEXITCODE
