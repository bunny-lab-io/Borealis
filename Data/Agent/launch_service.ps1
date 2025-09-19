#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/launch_service.ps1
[CmdletBinding()]
param(
    [switch]$Console
)

try {
    $ErrorActionPreference = 'Stop'
    $scriptDir = Split-Path -Path $PSCommandPath -Parent
    Set-Location -Path $scriptDir

    # Ensure a place for wrapper/stdout logs
    $pd = Join-Path $env:ProgramData 'Borealis'
    if (-not (Test-Path $pd)) { New-Item -ItemType Directory -Path $pd -Force | Out-Null }
    $wrapperLog = Join-Path $pd 'svc.wrapper.log'

    $venvBin = Join-Path $scriptDir '..\Scripts'
    $pyw     = Join-Path $venvBin 'pythonw.exe'
    $py      = Join-Path $venvBin 'python.exe'
    $agentPy = Join-Path $scriptDir 'agent.py'

    if (-not (Test-Path $pyw) -and -not (Test-Path $py)) { throw "Python not found under: $venvBin" }
    if (-not (Test-Path $agentPy)) { throw "Agent script not found: $agentPy" }

    $exe = if ($Console) { $py } else { if (Test-Path $pyw) { $pyw } else { $py } }
    $args = @("`"$agentPy`"","--system-service","--config","svc")

    # Launch and keep the task in Running state by waiting on the child
    $p = Start-Process -FilePath $exe -ArgumentList $args -WindowStyle Hidden -PassThru -WorkingDirectory $scriptDir `
         -RedirectStandardOutput (Join-Path $pd 'svc.out.log') -RedirectStandardError (Join-Path $pd 'svc.err.log')
    try { Wait-Process -Id $p.Id } catch {}
} catch {
    try {
        "[$(Get-Date -Format s)] $_" | Out-File -FilePath $wrapperLog -Append -Encoding utf8
    } catch {}
    exit 1
}
