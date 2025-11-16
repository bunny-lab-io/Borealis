#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/launch_service.ps1
[CmdletBinding()]
param(
    [switch]$Console
)

try {
    $ErrorActionPreference = 'Stop'
    $scriptDir = Split-Path -Path $PSCommandPath -Parent
    Set-Location -Path $scriptDir

    # Centralized logs under <ProjectRoot>\Agent\Logs
    $projRoot  = Resolve-Path (Join-Path $scriptDir '..\..')
    $agentRoot = Join-Path $projRoot 'Agent'
    if (-not (Test-Path $agentRoot)) { New-Item -ItemType Directory -Path $agentRoot -Force | Out-Null }
    $logsAgent = Join-Path $agentRoot 'Logs'
    if (-not (Test-Path $logsAgent)) { New-Item -ItemType Directory -Path $logsAgent -Force | Out-Null }
    $wrapperLog = Join-Path $logsAgent 'service_wrapper.log'

    function Write-WrapperLog {
        param([string]$Message)
        if (-not $Message) { return }
        try {
            "[{0}] {1}" -f (Get-Date -Format s), $Message | Out-File -FilePath $wrapperLog -Append -Encoding utf8
        } catch {}
    }

    $venvBin = Join-Path $scriptDir '..\Scripts'
    $pyw     = Join-Path $venvBin 'pythonw.exe'
    $py      = Join-Path $venvBin 'python.exe'
    $agentPy = Join-Path $scriptDir 'agent.py'

    $pyvenvCfg    = Join-Path $agentRoot 'pyvenv.cfg'
    $expectedPy   = Join-Path $projRoot 'Dependencies\Python\python.exe'
    if (-not (Test-Path $expectedPy -PathType Leaf) -and (Test-Path $py -PathType Leaf)) {
        $expectedPy = $py
    }

    $expectedPyNorm = $null
    $expectedHomeNorm = $null
    try {
        if (Test-Path $expectedPy -PathType Leaf) {
            $expectedPy = (Resolve-Path $expectedPy -ErrorAction Stop).ProviderPath
        }
    } catch {}
    if ($expectedPy) {
        $expectedPyNorm = $expectedPy.ToLowerInvariant()
        try { $expectedHome = Split-Path -Path $expectedPy -Parent } catch { $expectedHome = $null }
        if ($expectedHome) { $expectedHomeNorm = $expectedHome.ToLowerInvariant() }
    }

    $venvNeedsRepair = $false
    if (Test-Path $pyvenvCfg -PathType Leaf) {
        try {
            $cfgLines = Get-Content -Path $pyvenvCfg -ErrorAction Stop
            $cfgMap = @{}
            foreach ($line in $cfgLines) {
                $trimmed = $line.Trim()
                if (-not $trimmed -or $trimmed.StartsWith('#')) { continue }
                $parts = $trimmed -split '=', 2
                if ($parts.Count -ne 2) { continue }
                $cfgMap[$parts[0].Trim().ToLowerInvariant()] = $parts[1].Trim()
            }

            $cfgExecutable = $cfgMap['executable']
            $cfgHome = $cfgMap['home']

            if ($cfgExecutable -and -not (Test-Path $cfgExecutable -PathType Leaf)) {
                $venvNeedsRepair = $true
            } elseif ($cfgHome -and -not (Test-Path $cfgHome -PathType Container)) {
                $venvNeedsRepair = $true
            } else {
                if ($cfgExecutable -and $expectedPyNorm) {
                    try { $resolvedExe = (Resolve-Path $cfgExecutable -ErrorAction Stop).ProviderPath } catch { $resolvedExe = $cfgExecutable }
                    $resolvedExeNorm = if ($resolvedExe) { $resolvedExe.ToLowerInvariant() } else { $null }
                    if ($resolvedExeNorm -and $resolvedExeNorm -ne $expectedPyNorm) { $venvNeedsRepair = $true }
                }
                if (-not $venvNeedsRepair -and $cfgHome -and $expectedHomeNorm) {
                    try { $resolvedHome = (Resolve-Path $cfgHome -ErrorAction Stop).ProviderPath } catch { $resolvedHome = $cfgHome }
                    $resolvedHomeNorm = if ($resolvedHome) { $resolvedHome.ToLowerInvariant() } else { $null }
                    if ($resolvedHomeNorm -and $resolvedHomeNorm -ne $expectedHomeNorm) { $venvNeedsRepair = $true }
                }
            }
        } catch { $venvNeedsRepair = $true }
    }

    if ($venvNeedsRepair -and (Test-Path $expectedPy -PathType Leaf)) {
        Write-WrapperLog ("Detected relocated Agent virtual environment. Rebuilding interpreter bindings with {0}" -f $expectedPy)
        try {
            & $expectedPy -m venv --upgrade $agentRoot | Out-Null
            Write-WrapperLog "Agent virtual environment successfully rebound to current project path."
        } catch {
            Write-WrapperLog ("Agent virtual environment repair failed: {0}" -f $_.Exception.Message)
        }
    }

    if (-not (Test-Path $pyw) -and -not (Test-Path $py)) { throw "Python not found under: $venvBin" }
    if (-not (Test-Path $agentPy)) { throw "Agent script not found: $agentPy" }

    $exe = if ($Console) { $py } else { if (Test-Path $pyw) { $pyw } else { $py } }
    $args = @("`"$agentPy`"","--system-service","--config","SYSTEM")

    # Launch and keep the task in Running state by waiting on the child
    $p = Start-Process -FilePath $exe -ArgumentList $args -WindowStyle Hidden -PassThru -WorkingDirectory $scriptDir `
         -RedirectStandardOutput (Join-Path $logsAgent 'service.out.log') -RedirectStandardError (Join-Path $logsAgent 'service.err.log')
    try { Wait-Process -Id $p.Id } catch {}
} catch {
    try {
        "[$(Get-Date -Format s)] $_" | Out-File -FilePath $wrapperLog -Append -Encoding utf8
    } catch {}
    exit 1
}
