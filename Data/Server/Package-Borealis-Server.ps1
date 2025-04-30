#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/Package-Borealis-Server.ps1

# ------------- Configuration -------------
# (all paths are made absolute via Join-Path and $scriptDir)
$scriptDir        = Split-Path $MyInvocation.MyCommand.Definition -Parent
$projectRoot      = Resolve-Path (Join-Path $scriptDir "..\..")        # go up two levels to <ProjectRoot>\Borealis
$packagingDir     = Join-Path $scriptDir "Packaging_Server"
$venvDir          = Join-Path $packagingDir "Pyinstaller_Virtual_Environment"
$distDir          = Join-Path $packagingDir "dist"
$buildDir         = Join-Path $packagingDir "build"
$specPath         = $packagingDir

$serverScript     = Join-Path $scriptDir "server.py"
$outputName       = "Borealis-Server"
$finalExeName     = "$outputName.exe"
$requirementsPath = Join-Path $scriptDir "server-requirements.txt"
$iconPath         = Join-Path $scriptDir "Borealis.ico"

# Static assets to bundle:
#   - the compiled React build under Server/web-interface/build
$staticBuildSrc   = Join-Path $projectRoot "Server\web-interface\build"
$staticBuildDst   = "web-interface/build"
#   - Tesseract-OCR folder must be nested under 'Borealis/Python_API_Endpoints/Tesseract-OCR'
$ocrSrc           = Join-Path $scriptDir "Python_API_Endpoints\Tesseract-OCR"
$ocrDst           = "Borealis/Python_API_Endpoints/Tesseract-OCR"
$soundsSrc        = Join-Path $scriptDir "Sounds"
$soundsDst        = "Sounds"

# Embedded Python shipped under Dependencies\Python\python.exe
$embeddedPython   = Join-Path $projectRoot "Dependencies\Python\python.exe"

# ------------- Prepare packaging folder -------------
if (-Not (Test-Path $packagingDir)) {
    New-Item -ItemType Directory -Path $packagingDir | Out-Null
}

# 1) Create or upgrade virtual environment
if (-Not (Test-Path (Join-Path $venvDir "Scripts\python.exe"))) {
    Write-Host "[SETUP] Creating virtual environment at $venvDir"
    & $embeddedPython -m venv --upgrade-deps $venvDir
}

# helper to invoke venv's python
$venvPy = Join-Path $venvDir "Scripts\python.exe"

# 2) Bootstrap & upgrade pip
Write-Host "[INFO] Bootstrapping pip"
& $venvPy -m ensurepip --upgrade
& $venvPy -m pip install --upgrade pip

# 3) Install server dependencies
Write-Host "[INFO] Installing server dependencies"
& $venvPy -m pip install -r $requirementsPath
#    Ensure dnspython is available for Eventlet's greendns support
& $venvPy -m pip install dnspython

# 4) Install PyInstaller
Write-Host "[INFO] Installing PyInstaller"
& $venvPy -m pip install pyinstaller

# 5) Clean previous artifacts
Write-Host "[INFO] Cleaning previous artifacts"
Remove-Item -Recurse -Force $distDir, $buildDir, "$specPath\$outputName.spec" -ErrorAction SilentlyContinue

# 6) Run PyInstaller, bundling server code and assets
#     Collect all Eventlet and DNS submodules to avoid missing dynamic imports
Write-Host "[INFO] Running PyInstaller"
& $venvPy -m PyInstaller `
    --onefile `
    --name $outputName `
    --icon $iconPath `
    --collect-submodules eventlet `
    --collect-submodules dns `
    --distpath $distDir `
    --workpath $buildDir `
    --specpath $specPath `
    --add-data "$staticBuildSrc;$staticBuildDst" `
    --add-data "$ocrSrc;$ocrDst" `
    --add-data "$soundsSrc;$soundsDst" `
    $serverScript

# 7) Copy the final EXE back to Data/Server
if (Test-Path (Join-Path $distDir $finalExeName)) {
    Copy-Item (Join-Path $distDir $finalExeName) (Join-Path $scriptDir $finalExeName) -Force
    Write-Host "[SUCCESS] Server packaged at $finalExeName"
} else {
    Write-Host "[FAILURE] Packaging failed." -ForegroundColor Red
}
