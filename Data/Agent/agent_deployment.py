import os
import sys
import subprocess
import shlex
import ctypes
from datetime import datetime


def _now():
    return datetime.now().strftime("%Y-%m-%d %H:%M:%S")


def project_paths():
    r"""Derive important paths relative to the venv python (sys.executable).

    Layout assumed at runtime:
      <ProjectRoot>/Agent/Scripts/python.exe  == sys.executable
      <ProjectRoot>/Agent/Borealis/*.py       == deployed agent files
    """
    venv_scripts = os.path.dirname(os.path.abspath(sys.executable))
    venv_root = os.path.abspath(os.path.join(venv_scripts, os.pardir))
    project_root = os.path.abspath(os.path.join(venv_root, os.pardir))
    borealis_dir = os.path.join(venv_root, "Borealis")
    logs_dir = os.path.join(project_root, "Logs")
    temp_dir = os.path.join(project_root, "Temp")
    return {
        "project_root": project_root,
        "venv_root": venv_root,
        "venv_python": sys.executable,
        "venv_pythonw": os.path.join(venv_scripts, "pythonw.exe"),
        "borealis_dir": borealis_dir,
        "logs_dir": logs_dir,
        "temp_dir": temp_dir,
        "service_script": os.path.join(borealis_dir, "windows_script_service.py"),
        # Use tray launcher for the scheduled task
        "agent_script": os.path.join(borealis_dir, "tray_launcher.py"),
    }


def ensure_dirs(paths):
    os.makedirs(paths["logs_dir"], exist_ok=True)
    os.makedirs(paths["temp_dir"], exist_ok=True)


def log_write(paths, name, text):
    try:
        p = os.path.join(paths["logs_dir"], name)
        with open(p, "a", encoding="utf-8") as f:
            f.write(f"{_now()} {text}\n")
    except Exception:
        pass


def is_admin():
    try:
        return ctypes.windll.shell32.IsUserAnAdmin() != 0  # type: ignore[attr-defined]
    except Exception:
        return False


def run(cmd, cwd=None, capture=False):
    if isinstance(cmd, str):
        shell = True
        args = cmd
    else:
        shell = False
        args = cmd
    return subprocess.run(
        args,
        cwd=cwd,
        shell=shell,
        text=True,
        capture_output=capture,
        check=False,
    )


def run_elevated_powershell(paths, ps_content, log_name):
    """Run a short PowerShell script elevated and wait for completion.
    Writes combined output to Logs/log_name.
    """
    ensure_dirs(paths)
    log_path = os.path.join(paths["logs_dir"], log_name)
    stub_path = os.path.join(paths["temp_dir"], f"elevate_{os.getpid()}_{log_name.replace('.', '_')}.ps1")
    with open(stub_path, "w", encoding="utf-8") as f:
        f.write(ps_content)

    # Build powershell command
    ps = "powershell.exe"
    args = f"-NoProfile -ExecutionPolicy Bypass -File \"{stub_path}\""

    # ShellExecute to run as admin
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
    sei.lpFile = ps
    sei.lpParameters = args
    sei.lpDirectory = paths["project_root"]
    sei.nShow = 1
    if not ctypes.windll.shell32.ShellExecuteExW(ctypes.byref(sei)):
        log_write(paths, log_name, "[ERROR] UAC elevation failed (ShellExecuteExW)")
        try:
            os.remove(stub_path)
        except Exception:
            pass
        return 1
    # Wait for elevated process
    ctypes.windll.kernel32.WaitForSingleObject(sei.hProcess, 0xFFFFFFFF)
    # Capture output from stub if it appended
    try:
        with open(log_path, "a", encoding="utf-8") as f:
            f.write("")
    except Exception:
        pass
    try:
        os.remove(stub_path)
    except Exception:
        pass
    return 0


def current_service_path(service_name):
    """Return the configured BinaryPathName for a service (or None)."""
    try:
        r = run(["sc.exe", "qc", service_name], capture=True)
        if r.returncode != 0:
            return None
        for line in (r.stdout or "").splitlines():
            if "BINARY_PATH_NAME" in line:
                # Example: "BINARY_PATH_NAME  : C:\\...\\python.exe C:\\...\\windows_script_service.py"
                parts = line.split(":", 1)
                if len(parts) == 2:
                    return parts[1].strip()
    except Exception:
        pass
    return None


def ensure_script_service(paths):
    service_name = "BorealisAgent"
    log_name = "Borealis_ScriptService_Install.log"
    ensure_dirs(paths)
    log_write(paths, log_name, "[INFO] Ensuring script execution service...")

    # Decide if install/update needed
    need_install = False
    bin_path = current_service_path(service_name)
    expected_root = paths["venv_root"].lower()
    if not bin_path:
        need_install = True
    else:
        if expected_root not in bin_path.lower():
            need_install = True

    if not is_admin():
        # Relaunch elevated to perform service installation/update
        venv = paths["venv_python"].replace("\"", "\"\"")
        srv = paths["service_script"].replace("\"", "\"\"")
        log = os.path.join(paths["logs_dir"], log_name).replace("\"", "\"\"")
        venv_dir = os.path.dirname(paths["venv_python"]).replace("\"", "\"\"")
        postinstall = os.path.join(venv_dir, "pywin32_postinstall.py").replace("\"", "\"\"")
        py_home = paths["venv_root"].replace("\"", "\"\"")
        content = f"""
$ErrorActionPreference = 'Continue'
$venv = "{venv}"
$srv  = "{srv}"
$log  = "{log}"
$post = "{postinstall}"
$pyhome = "{py_home}"
try {{
  try {{ New-Item -ItemType Directory -Force -Path (Split-Path $log -Parent) | Out-Null }} catch {{}}
  # Remove legacy service names if present
  try {{ sc.exe stop BorealisScriptAgent 2>$null | Out-Null }} catch {{}}
  try {{ sc.exe delete BorealisScriptAgent 2>$null | Out-Null }} catch {{}}
  try {{ sc.exe stop BorealisScriptService 2>$null | Out-Null }} catch {{}}
  try {{ sc.exe delete BorealisScriptService 2>$null | Out-Null }} catch {{}}
  if (Test-Path $post) {{ & $venv $post -install *>> "$log" }} else {{ & $venv -m pywin32_postinstall -install *>> "$log" }}
  try {{ & $venv $srv remove *>> "$log" }} catch {{}}
  & $venv $srv --startup auto install *>> "$log"
  # Ensure registry points to correct module and PY path
  reg add "HKLM\SYSTEM\CurrentControlSet\Services\{service_name}\PythonClass" /ve /t REG_SZ /d "windows_script_service.BorealisAgentService" /f | Out-Null
  reg add "HKLM\SYSTEM\CurrentControlSet\Services\{service_name}\PythonPath" /ve /t REG_SZ /d "{paths['borealis_dir']}" /f | Out-Null
  reg add "HKLM\SYSTEM\CurrentControlSet\Services\{service_name}\PythonHome" /ve /t REG_SZ /d "$pyhome" /f | Out-Null
  sc.exe config {service_name} obj= LocalSystem start= auto | Out-File -FilePath "$log" -Append -Encoding UTF8
  sc.exe start {service_name} | Out-File -FilePath "$log" -Append -Encoding UTF8
  "[INFO] Completed service ensure." | Out-File -FilePath "$log" -Append -Encoding UTF8
}} catch {{
  "[ERROR] $_" | Out-File -FilePath "$log" -Append -Encoding UTF8
  exit 1
}}
"""
        rc = run_elevated_powershell(paths, content, log_name)
        return rc == 0

    # Admin path: perform directly
    try:
        # Ensure pywin32 service hooks present in this venv
        post_py = os.path.join(os.path.dirname(paths["venv_python"]), "pywin32_postinstall.py")
        if os.path.isfile(post_py):
            run([paths["venv_python"], post_py, "-install"])  # ignore rc
        else:
            run([paths["venv_python"], "-m", "pywin32_postinstall", "-install"])  # ignore rc
    except Exception:
        pass
    try:
        # Remove legacy service if it exists
        run(["sc.exe", "stop", "BorealisScriptAgent"])  # ignore rc
        run(["sc.exe", "delete", "BorealisScriptAgent"])  # ignore rc
        run(["sc.exe", "stop", "BorealisScriptService"])  # ignore rc
        run(["sc.exe", "delete", "BorealisScriptService"])  # ignore rc
        if need_install:
            run([paths["venv_python"], paths["service_script"], "remove"])  # ignore rc
            r1 = run([paths["venv_python"], paths["service_script"], "--startup", "auto", "install"], capture=True)
            log_write(paths, log_name, f"[INFO] install rc={r1.returncode} out={r1.stdout}\nerr={r1.stderr}")
        # fix registry for module import and runtime resolution
        # PythonHome: base interpreter home (from pyvenv.cfg 'home') so pythonservice can load pythonXY.dll
        # PythonPath: add Borealis dir and venv site-packages including pywin32 dirs
        try:
            cfg = os.path.join(paths["venv_root"], "pyvenv.cfg")
            base_home = None
            if os.path.isfile(cfg):
                with open(cfg, "r", encoding="utf-8", errors="ignore") as f:
                    for line in f:
                        if line.strip().lower().startswith("home ="):
                            base_home = line.split("=",1)[1].strip()
                            break
            if not base_home:
                # fallback to parent of venv Scripts
                base_home = os.path.dirname(os.path.dirname(paths["venv_python"]))
        except Exception:
            base_home = os.path.dirname(os.path.dirname(paths["venv_python"]))

        site = os.path.join(paths["venv_root"], "Lib", "site-packages")
        pypath = ";".join([
            paths["borealis_dir"],
            site,
            os.path.join(site, "win32"),
            os.path.join(site, "win32", "lib"),
            os.path.join(site, "pywin32_system32"),
        ])

        run(["reg", "add", fr"HKLM\\SYSTEM\\CurrentControlSet\\Services\\{service_name}\\PythonClass", "/ve", "/t", "REG_SZ", "/d", "windows_script_service.BorealisAgentService", "/f"])  # noqa
        run(["reg", "add", fr"HKLM\\SYSTEM\\CurrentControlSet\\Services\\{service_name}\\PythonPath", "/ve", "/t", "REG_SZ", "/d", pypath, "/f"])  # noqa
        run(["reg", "add", fr"HKLM\\SYSTEM\\CurrentControlSet\\Services\\{service_name}\\PythonHome", "/ve", "/t", "REG_SZ", "/d", base_home, "/f"])  # noqa
        run(["sc.exe", "config", service_name, "obj=", "LocalSystem"])  # ensure LocalSystem
        run(["sc.exe", "start", service_name])
        # quick validate
        qc = run(["sc.exe", "query", service_name], capture=True)
        ok = (qc.returncode == 0)
        log_write(paths, log_name, f"[INFO] ensure complete (ok={ok})")
        return ok
    except Exception as e:
        log_write(paths, log_name, f"[ERROR] ensure (admin) failed: {e}")
        return False


def ensure_user_logon_task(paths):
    """Ensure the per-user scheduled task that launches the agent in the user's session.

    Name: "Borealis Agent"
    Trigger: On logon (current user)
    Action:  <venv_python> -W ignore::SyntaxWarning <agent_script>
    """
    ensure_dirs(paths)
    log_name = "Borealis_CollectorTask_Install.log"
    task_name = "Borealis Agent"
    # Use pythonw.exe to avoid opening a console window in the user's session
    pyw = paths.get("venv_pythonw") or paths["venv_python"]
    cmd = f"\"{pyw}\" -W ignore::SyntaxWarning \"{paths['agent_script']}\""

    # If task exists, try remove (non-elevated first)
    q = run(["schtasks.exe", "/Query", "/TN", task_name])
    if q.returncode == 0:
        d = run(["schtasks.exe", "/Delete", "/TN", task_name, "/F"], capture=True)
        if d.returncode != 0:
            # Attempt elevated deletion (task might have been created under admin previously)
            ps = f"""
$ErrorActionPreference = 'SilentlyContinue'
try {{ schtasks.exe /Delete /TN "{task_name}" /F | Out-Null }} catch {{}}
"""
            run_elevated_powershell(paths, ps, log_name)

    c = run(["schtasks.exe", "/Create", "/SC", "ONLOGON", "/TN", task_name, "/TR", cmd, "/F", "/RL", "LIMITED"], capture=True)
    log_write(paths, log_name, f"[INFO] create rc={c.returncode} out={c.stdout} err={c.stderr}")
    if c.returncode != 0:
        # Fallback: elevate and register task for the current user SID
        user = os.environ.get("USERNAME", "")
        domain = os.environ.get("USERDOMAIN", os.environ.get("COMPUTERNAME", ""))
        py = (paths.get("venv_pythonw") or paths["venv_python"]).replace("\"", "\"\"")
        ag = paths["agent_script"].replace("\"", "\"\"")
        content = f"""
$ErrorActionPreference = 'Continue'
$task = "{task_name}"
$py   = "{py}"
$arg  = "-W ignore::SyntaxWarning {ag}"
$cmd  = '"' + $py + '" ' + $arg
$user = "{domain}\{user}"
try {{
  # Resolve user SID
  $sid = (New-Object System.Security.Principal.NTAccount($user)).Translate([System.Security.Principal.SecurityIdentifier]).Value
}} catch {{ $sid = $null }}
try {{
  # Delete any existing task (any scope)
  try {{ schtasks.exe /Delete /TN $task /F | Out-Null }} catch {{}}
  $action = New-ScheduledTaskAction -Execute $py -Argument $arg
  $trigger = New-ScheduledTaskTrigger -AtLogOn
  $settings = New-ScheduledTaskSettingsSet -Hidden
  if ($sid) {{
    $principal = New-ScheduledTaskPrincipal -UserId $sid -LogonType Interactive -RunLevel Limited
    Register-ScheduledTask -TaskName $task -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force
  }} else {{
    # Fallback: bind by username (use Interactive to avoid password)
    $principal = New-ScheduledTaskPrincipal -UserId "{domain}\{user}" -LogonType Interactive -RunLevel Limited
    Register-ScheduledTask -TaskName $task -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force
  }}
}} catch {{
  "[ERROR] Task register failed: $_" | Out-File -FilePath "{os.path.join(paths['logs_dir'], log_name)}" -Append -Encoding UTF8
  exit 1
}}
"""
        rc = run_elevated_powershell(paths, content, log_name)
        if rc != 0:
            return False
    else:
        # Created via schtasks; set Hidden=true via elevated PowerShell (schtasks lacks a /HIDDEN switch)
        ps_hide = f"""
$ErrorActionPreference = 'SilentlyContinue'
try {{
  $settings = New-ScheduledTaskSettingsSet -Hidden
  Set-ScheduledTask -TaskName "{task_name}" -Settings $settings | Out-Null
}} catch {{}}
"""
        run_elevated_powershell(paths, ps_hide, log_name)
    # Start immediately (if a session is active)
    run(["schtasks.exe", "/Run", "/TN", task_name])
    return True


def ensure_all():
    paths = project_paths()
    ensure_dirs(paths)
    ok_svc = ensure_script_service(paths)
    # Service now launches per-session helper; scheduled task is not required.
    return 0 if ok_svc else 1


def main(argv):
    # Simple CLI
    if len(argv) <= 1:
        print("Usage: agent_deployment.py [ensure-all|service-install|service-remove|task-ensure|task-remove]")
        return 2
    cmd = argv[1].lower()
    paths = project_paths()
    ensure_dirs(paths)

    if cmd == "ensure-all":
        return ensure_all()
    if cmd == "service-install":
        return 0 if ensure_script_service(paths) else 1
    if cmd == "service-remove":
        name = "BorealisAgent"
        if not is_admin():
            ps = f"try {{ sc.exe stop {name} }} catch {{}}; try {{ sc.exe delete {name} }} catch {{}}"
            return run_elevated_powershell(paths, ps, "Borealis_ScriptService_Remove.log")
        run(["sc.exe", "stop", name])
        r = run(["sc.exe", "delete", name])
        return r.returncode
    if cmd == "task-ensure":
        return 0 if ensure_user_logon_task(paths) else 1
    if cmd == "task-remove":
        tn = "Borealis Agent"
        r = run(["schtasks.exe", "/Delete", "/TN", tn, "/F"])
        return r.returncode

    print(f"Unknown command: {cmd}")
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
