<#
    Python-NodeJS-Installer.ps1
    -----------------------------------
    Silently installs Python 3.13.3 and Node.js 23.11.0,
    replaces PATH to avoid Windows Store shim issue,
    and ensures persistence across reboots.

    REQUIREMENT:
    Must be run from an Administrator PowerShell session.

    Usage:
    Set-ExecutionPolicy Unrestricted -Scope Process; .\Python-NodeJS-Installer.ps1
#>

$ErrorActionPreference = "Stop"

# ----------- ADMIN WARNING AND PAUSE -----------
Write-Host "=================================================================" -ForegroundColor Yellow
Write-Host "  WARNING: This script must be run as Administrator to function." -ForegroundColor Yellow
Write-Host "  It will modify the system PATH (User and Machine scopes) and install tools globally." -ForegroundColor Yellow
Write-Host "=================================================================" -ForegroundColor Yellow
Pause
Write-Host ""

# ----------- Install URLs and Paths -----------
$pythonURL = "https://www.python.org/ftp/python/3.13.3/python-3.13.3-amd64.exe"
$nodeURL   = "https://nodejs.org/dist/latest/node-v23.11.0-x64.msi"

$tempDir = $env:TEMP
$pythonInstaller = Join-Path $tempDir "python-installer.exe"
$nodeInstaller   = Join-Path $tempDir "node-setup.msi"

$realPythonPath   = "C:\Program Files\Python313"
$realPythonExe    = "$realPythonPath\python.exe"
$realScriptsPath  = "$realPythonPath\Scripts"

# ----------- PATH Cleanup Function -----------
function Clean-PythonFromPath {
    Write-Host "[INFO] Cleaning existing Python paths from system and user PATH..."

    $scopes = @([EnvironmentVariableTarget]::User, [EnvironmentVariableTarget]::Machine)

    foreach ($scope in $scopes) {
        $originalPath = [Environment]::GetEnvironmentVariable("Path", $scope)
        if (-not $originalPath) { continue }

        $cleaned = $originalPath -split ";" | Where-Object {
            ($_ -notmatch "WindowsApps") -and ($_ -notmatch "Python") -and ($_ -notmatch "Python3")
        }

        $newPath = ($cleaned -join ";").TrimEnd(";")

        [Environment]::SetEnvironmentVariable("Path", $newPath, $scope)
        Write-Host "[OK] Cleaned Python-related entries from $scope PATH"
    }
}

# ----------- Global PATH Modification -----------
function Set-PersistentPath {
    param (
        [string]$TargetPath
    )

    $scope = [EnvironmentVariableTarget]::Machine
    $currentPath = [Environment]::GetEnvironmentVariable("Path", $scope)

    $filtered = $currentPath -split ";" | Where-Object {
        ($_ -notmatch "WindowsApps") -and ($_ -notmatch "Python")
    }

    $newPath = ($filtered + $TargetPath + "$TargetPath\Scripts") -join ";"
    [Environment]::SetEnvironmentVariable("Path", $newPath, $scope)

    Write-Host "[OK] System PATH updated with: $TargetPath"
}

# ----------- Install Python -----------
function Install-Python {
    if (Test-Path $realPythonExe) {
        Write-Host "[OK] Python already installed at $realPythonPath"
        Set-PersistentPath -TargetPath $realPythonPath
        return
    }

    Write-Host "[INFO] Downloading Python installer..."
    Invoke-WebRequest -Uri $pythonURL -OutFile $pythonInstaller

    Write-Host "[INFO] Installing Python silently..."
    Start-Process -FilePath $pythonInstaller -ArgumentList '/quiet InstallAllUsers=1 PrependPath=1 Include_test=0' -Wait

    Remove-Item $pythonInstaller -Force

    if (Test-Path $realPythonExe) {
        Write-Host "[OK] Python installed at $realPythonPath"
        Set-PersistentPath -TargetPath $realPythonPath
    } else {
        Write-Host "[ERROR] Python install failed or path not found." -ForegroundColor Red
        exit 1
    }
}

# ----------- Install NodeJS -----------
function Install-NodeJS {
    if (Get-Command node -ErrorAction SilentlyContinue) {
        Write-Host "[OK] Node.js is already installed."
        return
    }

    Write-Host "[INFO] Downloading Node.js installer..."
    Invoke-WebRequest -Uri $nodeURL -OutFile $nodeInstaller

    Write-Host "[INFO] Installing Node.js silently..."
    Start-Process -FilePath "msiexec.exe" -ArgumentList "/i `"$nodeInstaller`" /qn /norestart" -Wait

    Remove-Item $nodeInstaller -Force
    Write-Host "[OK] Node.js installed."
}

# ----------- Main Script -----------
Write-Host "=============================================="
Write-Host "  Python + NodeJS Installer for Borealis"
Write-Host "=============================================="
Write-Host ""

Clean-PythonFromPath
Install-Python
Install-NodeJS

Write-Host ""
Write-Host "[OK] All dependencies installed and system PATH updated."
Write-Host "You can now run Launch-Borealis.ps1 in a new PowerShell window."
