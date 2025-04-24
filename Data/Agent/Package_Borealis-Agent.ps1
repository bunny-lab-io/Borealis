#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Agent/Package_Borealis-Agent.ps1

# Configuration
$packagingDir = "Packaging_Data"
$venvDir = "$packagingDir\Pyinstaller_Virtual_Environment"
$distDir = "$packagingDir\dist"
$buildDir = "$packagingDir\build"
$specPath = "$packagingDir"
$agentScript = "borealis-agent.py"
$outputName = "Borealis-Agent"
$finalExeName = "$outputName.exe"
$requirementsPath = "requirements.txt"
$iconPath = "..\Borealis.ico"

# Ensure Packaging_Data directory exists
if (-Not (Test-Path $packagingDir)) {
    New-Item -ItemType Directory -Path $packagingDir | Out-Null
}

# Set up virtual environment
if (-Not (Test-Path "$venvDir\Scripts\Activate.ps1")) {
    Write-Host "[SETUP] Creating virtual environment at $venvDir"
    python -m venv $venvDir
}

# Activate virtual environment
Write-Host "[INFO] Activating virtual environment"
. "$venvDir\Scripts\Activate.ps1"

# Install agent dependencies from requirements file
Write-Host "[INFO] Installing agent dependencies from $requirementsPath"
pip install --upgrade pip > $null
pip install -r $requirementsPath > $null

# Install PyInstaller
Write-Host "[INFO] Installing PyInstaller"
pip install pyinstaller > $null

# Clean previous build artifacts
Write-Host "[INFO] Cleaning previous build artifacts"
Remove-Item -Recurse -Force $distDir, $buildDir, "$specPath\$outputName.spec" -ErrorAction SilentlyContinue

# Run PyInstaller to create single-file executable with custom icon
Write-Host "[INFO] Running PyInstaller with icon $iconPath"
pyinstaller --onefile --icon "$iconPath" --noconfirm --name $outputName --distpath $distDir --workpath $buildDir --specpath $specPath $agentScript

# Copy resulting executable to Agent folder
if (Test-Path "$distDir\$finalExeName") {
    Copy-Item "$distDir\$finalExeName" ".\$finalExeName" -Force
    Write-Host "[SUCCESS] Agent packaged at .\$finalExeName"
} else {
    Write-Host "[FAILURE] Packaging failed." -ForegroundColor Red
}
