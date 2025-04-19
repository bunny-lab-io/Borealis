#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Package-Borealis-Server.ps1

# ------------------- CONFIGURATION -------------------
$venvDir = "Pyinstaller_Virtual_Environment"
$serverScript = "Server\Borealis\server.py"
$outputName = "Borealis-Server"
$distExePath = "dist\$outputName.exe"
$buildDir = "Pyinstaller_Server_Build"
$specFile = "$outputName.spec"

$reactBuild = "Server\web-interface\build"
$tesseractDir = "Server\Borealis\Python_API_Endpoints\Tesseract-OCR"

# ------------------- ENV SETUP -------------------
Write-Host "`n[INFO] Preparing virtual environment and dependencies..." -ForegroundColor Cyan

$activateScript = "$venvDir\Scripts\Activate.ps1"

if (-Not (Test-Path $activateScript)) {
    Write-Host "[SETUP] Creating virtual environment at $venvDir"
    python -m venv $venvDir
}

# ------------------- ACTIVATE -------------------
Write-Host "[INFO] Activating virtual environment..."
. $activateScript

# ------------------- INSTALL DEPENDENCIES -------------------
Write-Host "[INFO] Installing project dependencies into virtual environment..."
pip install --upgrade pip > $null
pip install -r requirements.txt

# ------------------- INSTALL PYINSTALLER -------------------
Write-Host "[INFO] Installing PyInstaller..."
pip install pyinstaller > $null

# Resolve PyInstaller path
$pyInstallerPath = "$venvDir\Scripts\pyinstaller.exe"

if (-Not (Test-Path $pyInstallerPath)) {
    Write-Host "[ERROR] PyInstaller not found even after install. Aborting." -ForegroundColor Red
    exit 1
}

# ------------------- CLEANUP OLD BUILD -------------------
Write-Host "[INFO] Cleaning previous build artifacts..." -ForegroundColor Gray
Remove-Item -Recurse -Force "dist", "build", $specFile -ErrorAction SilentlyContinue

# ------------------- BUILD PYINSTALLER CMD -------------------
$reactStaticAssets = "$reactBuild;web-interface/build"
$tesseractAssets = "$tesseractDir;Borealis/Python_API_Endpoints/Tesseract-OCR"

$cmdArgs = @(
    "--onefile",
    "--noconfirm",
    "--name", "$outputName",
    "--add-data", "`"$reactStaticAssets`"",
    "--add-data", "`"$tesseractAssets`"",
    "--hidden-import=eventlet",
    "--clean",
    "`"$serverScript`""
)

$arguments = $cmdArgs -join " "

Write-Host "`n[INFO] Running PyInstaller with Start-Process..." -ForegroundColor Yellow
Start-Process -FilePath $pyInstallerPath -ArgumentList $arguments -Wait -NoNewWindow

# ------------------- DONE -------------------
if (Test-Path $distExePath) {
    Write-Host "`n[SUCCESS] Packaging complete!"
    Write-Host "         Output: $distExePath" -ForegroundColor Green
} else {
    Write-Host "`n[FAILURE] Packaging failed." -ForegroundColor Red
}
