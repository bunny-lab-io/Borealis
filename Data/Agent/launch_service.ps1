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

    $venvBin = Join-Path $scriptDir '..\Scripts'
    $pyw     = Join-Path $venvBin 'pythonw.exe'
    $py      = Join-Path $venvBin 'python.exe'
    $agentPy = Join-Path $scriptDir 'agent.py'

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
