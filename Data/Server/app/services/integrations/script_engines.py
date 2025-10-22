import os
import subprocess
import sys
import platform


def run_powershell_script(script_path: str):
    """
    Execute a PowerShell script with ExecutionPolicy Bypass.

    Returns (returncode, stdout, stderr)
    """
    if not script_path or not os.path.isfile(script_path):
        raise FileNotFoundError(f"Script not found: {script_path}")

    if not script_path.lower().endswith(".ps1"):
        raise ValueError("run_powershell_script only accepts .ps1 files")

    system = platform.system()

    # Choose powershell binary
    ps_bin = None
    if system == "Windows":
        # Prefer Windows PowerShell
        ps_bin = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
        if not os.path.isfile(ps_bin):
            ps_bin = "powershell.exe"
    else:
        # PowerShell Core (pwsh) may exist cross-platform
        ps_bin = "pwsh"

    # Build command
    # -ExecutionPolicy Bypass (Windows only), -NoProfile, -File "script"
    cmd = [ps_bin]
    if system == "Windows":
        cmd += ["-ExecutionPolicy", "Bypass"]
    cmd += ["-NoProfile", "-File", script_path]

    # Hide window on Windows
    creationflags = 0
    startupinfo = None
    if system == "Windows":
        creationflags = 0x08000000  # CREATE_NO_WINDOW
        startupinfo = subprocess.STARTUPINFO()
        startupinfo.dwFlags |= subprocess.STARTF_USESHOWWINDOW

    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        universal_newlines=True,
        creationflags=creationflags,
        startupinfo=startupinfo,
    )
    out, err = proc.communicate()
    return proc.returncode, out or "", err or ""

