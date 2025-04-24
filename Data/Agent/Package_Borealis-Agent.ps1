#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/Package_Borealis-Agent.ps1

# Configuration
$packagingDir     = "Packaging_Data"
$venvDir          = "$packagingDir\Pyinstaller_Virtual_Environment"
$distDir          = "$packagingDir\dist"
$buildDir         = "$packagingDir\build"
$specPath         = "$packagingDir"
$agentScript      = "borealis-agent.py"
$outputName       = "Borealis-Agent"
$finalExeName     = "$outputName.exe"
$requirementsPath = "requirements.txt"
$iconPath         = "..\Borealis.ico"

# figure out where everything lives
$scriptDir      = Split-Path $MyInvocation.MyCommand.Definition -Parent
$projectRoot    = Resolve-Path (Join-Path $scriptDir "..\..")
$embeddedPython = Join-Path $projectRoot 'Dependencies\Python\python.exe'

# Ensure Packaging_Data directory exists
if (-Not (Test-Path $packagingDir)) {
    New-Item -ItemType Directory -Path $packagingDir | Out-Null
}

# 1) Create or recreate virtual environment using the embedded Python
if (-Not (Test-Path "$venvDir\Scripts\python.exe")) {
    Write-Host "[SETUP] Creating virtual environment at $venvDir using embedded Python"
    & $embeddedPython -m venv --upgrade-deps $venvDir
}

# helper for calling into that venv
$venvPy = Join-Path $venvDir 'Scripts\python.exe'

# 2) Bootstrap pip (in case ensurepip wasn’t automatic) and upgrade
Write-Host "[INFO] Ensuring pip is available in the venv"
& $venvPy -m ensurepip --upgrade

Write-Host "[INFO] Upgrading pip"
& $venvPy -m pip install --upgrade pip

# 3) Install your agent’s dependencies
Write-Host "[INFO] Installing agent dependencies from $requirementsPath"
& $venvPy -m pip install -r $requirementsPath

# 4) Install PyInstaller into that same venv
Write-Host "[INFO] Installing PyInstaller"
& $venvPy -m pip install pyinstaller

# 5) Clean previous build artifacts
Write-Host "[INFO] Cleaning previous build artifacts"
Remove-Item -Recurse -Force $distDir, $buildDir, "$specPath\$outputName.spec" -ErrorAction SilentlyContinue

# 6) Run PyInstaller
Write-Host "[INFO] Running PyInstaller with icon $iconPath"
& $venvPy -m PyInstaller `
    --onefile `
    --icon "$iconPath" `
    --noconfirm `
    --name $outputName `
    --distpath $distDir `
    --workpath $buildDir `
    --specpath $specPath `
    $agentScript

# 7) Copy the final exe back into your Agent folder
if (Test-Path "$distDir\$finalExeName") {
    Copy-Item "$distDir\$finalExeName" ".\$finalExeName" -Force
    Write-Host "[SUCCESS] Agent packaged at .\$finalExeName"
} else {
    Write-Host "[FAILURE] Packaging failed." -ForegroundColor Red
}
