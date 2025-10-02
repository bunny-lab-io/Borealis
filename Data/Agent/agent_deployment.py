import os
import sys
import subprocess
import ctypes
from datetime import datetime


def _now():
    return datetime.now().strftime("%Y-%m-%d %H:%M:%S")


def project_paths():
    venv_scripts = os.path.dirname(os.path.abspath(sys.executable))
    venv_root = os.path.abspath(os.path.join(venv_scripts, os.pardir))
    project_root = os.path.abspath(os.path.join(venv_root, os.pardir))
    borealis_dir = os.path.join(venv_root, "Borealis")
    logs_dir = os.path.join(project_root, "Logs", "Agent")
    temp_dir = os.path.join(project_root, "Temp")
    return {
        "project_root": project_root,
        "venv_root": venv_root,
        "venv_python": sys.executable,
        "venv_pythonw": os.path.join(venv_scripts, "pythonw.exe"),
        "borealis_dir": borealis_dir,
        "logs_dir": logs_dir,
        "temp_dir": temp_dir,
        "agent_script": os.path.join(borealis_dir, "agent.py"),
    }


def ensure_dirs(paths):
    os.makedirs(paths["logs_dir"], exist_ok=True)
    os.makedirs(paths["temp_dir"], exist_ok=True)


def log_write(paths, name, text):
    try:
        # Centralize into Agent logs; default to install.log when unspecified
        fn = name or "install.log"
        p = os.path.join(paths["logs_dir"], fn)
        with open(p, "a", encoding="utf-8") as f:
            f.write(f"{_now()} {text}\n")
    except Exception:
        pass


def is_admin():
    try:
        return ctypes.windll.shell32.IsUserAnAdmin() != 0
    except Exception:
        return False


def run(cmd, capture=False):
    return subprocess.run(cmd, text=True, capture_output=capture, check=False)


def run_elevated_powershell(paths, ps_content, log_name):
    ensure_dirs(paths)
    log_path = os.path.join(paths["logs_dir"], log_name)
    stub_path = os.path.join(paths["temp_dir"], f"elevate_{os.getpid()}_{log_name.replace('.', '_')}.ps1")
    with open(stub_path, "w", encoding="utf-8") as f:
        f.write(ps_content)
    SEE_MASK_NOCLOSEPROCESS = 0x00000040
    class SHELLEXECUTEINFO(ctypes.Structure):
        _fields_ = [
            ("cbSize", ctypes.c_ulong),
            ("fMask", ctypes.c_ulong),
            ("hwnd", ctypes.c_void_p),
            ("lpVerb", ctypes.c_wchar_p),
            ("lpFile", ctypes.c_wchar_p),
            ("lpParameters", ctypes.c_wchar_p),
            ("lpDirectory", ctypes.c_wchar_p),
            ("nShow", ctypes.c_int),
            ("hInstApp", ctypes.c_void_p),
            ("lpIDList", ctypes.c_void_p),
            ("lpClass", ctypes.c_wchar_p),
            ("hkeyClass", ctypes.c_void_p),
            ("dwHotKey", ctypes.c_ulong),
            ("hIcon", ctypes.c_void_p),
            ("hProcess", ctypes.c_void_p),
        ]
    sei = SHELLEXECUTEINFO()
    sei.cbSize = ctypes.sizeof(SHELLEXECUTEINFO)
    sei.fMask = SEE_MASK_NOCLOSEPROCESS
    sei.hwnd = None
    sei.lpVerb = "runas"
    sei.lpFile = "powershell.exe"
    sei.lpParameters = f"-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File \"{stub_path}\""
    sei.lpDirectory = paths["project_root"]
    # Hide the elevated PowerShell window entirely (UAC prompt may still appear)
    sei.nShow = 0
    if not ctypes.windll.shell32.ShellExecuteExW(ctypes.byref(sei)):
        log_write(paths, log_name, "[ERROR] UAC elevation failed (ShellExecuteExW)")
        return 1
    hproc = sei.hProcess
    if hproc:
        ctypes.windll.kernel32.WaitForSingleObject(hproc, 0xFFFFFFFF)
    try:
        os.remove(stub_path)
    except Exception:
        pass
    return 0


def ensure_user_logon_task(paths):
    """Ensure per-user scheduled task that launches the helper at logon.
    Task name: "Borealis Agent"
    """
    task_name = "Borealis Agent"
    pyw = paths.get("venv_pythonw") or paths["venv_python"]
    cmd = f'"{pyw}" -W ignore::SyntaxWarning "{paths["agent_script"]}"'
    # Try create non-elevated
    q = run(["schtasks.exe", "/Query", "/TN", task_name], capture=True)
    if q.returncode == 0:
        d = run(["schtasks.exe", "/Delete", "/TN", task_name, "/F"], capture=True)
        if d.returncode != 0:
            pass
    c = run(["schtasks.exe", "/Create", "/SC", "ONLOGON", "/TN", task_name, "/TR", cmd, "/F", "/RL", "LIMITED"], capture=True)
    if c.returncode == 0:
        run(["schtasks.exe", "/Run", "/TN", task_name], capture=True)
        return True
    # Elevated fallback using ScheduledTasks cmdlets for better reliability
    ps = f"""
$ErrorActionPreference='Continue'
$task = "{task_name}"
$py   = "{pyw}"
$arg  = "-W ignore::SyntaxWarning {paths['agent_script']}"
try {{ Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue }} catch {{}}
$action = New-ScheduledTaskAction -Execute $py -Argument $arg
$trigger= New-ScheduledTaskTrigger -AtLogOn
$settings = New-ScheduledTaskSettingsSet -Hidden -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
Register-ScheduledTask -TaskName $task -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null
Start-ScheduledTask -TaskName $task | Out-Null
"""
    rc = run_elevated_powershell(paths, ps, "Borealis_CollectorTask_Install.log")
    return rc == 0


def ensure_all():
    paths = project_paths()
    ensure_dirs(paths)
    ok = ensure_user_logon_task(paths)
    return 0 if ok else 1


def main(argv):
    if len(argv) <= 1:
        print("Usage: agent_deployment.py [ensure-all|task-ensure|task-remove]")
        return 2
    cmd = argv[1].lower()
    paths = project_paths()
    ensure_dirs(paths)
    if cmd == "ensure-all":
        return ensure_all()
    if cmd == "task-ensure":
        return 0 if ensure_user_logon_task(paths) else 1
    if cmd == "task-remove":
        return run(["schtasks.exe", "/Delete", "/TN", "Borealis Agent", "/F"]).returncode
    print(f"Unknown command: {cmd}")
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
