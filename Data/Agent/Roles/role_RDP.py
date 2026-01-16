# ======================================================
# Data\Agent\Roles\role_RDP.py
# Description: Optional RDP readiness helper for Borealis (Windows-only).
#
# API Endpoints (if applicable): None
# ======================================================

"""RDP readiness helper role (no-op unless enabled via environment flags)."""

from __future__ import annotations

import os
import subprocess
import time
from pathlib import Path

ROLE_NAME = "RDP"
ROLE_CONTEXTS = ["system"]


def _log_path() -> Path:
    root = Path(__file__).resolve().parents[2] / "Logs"
    root.mkdir(parents=True, exist_ok=True)
    return root / "rdp.log"


def _write_log(message: str) -> None:
    ts = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime())
    try:
        _log_path().open("a", encoding="utf-8").write(f"[{ts}] [rdp-role] {message}\n")
    except Exception:
        pass


def _bool_env(name: str) -> bool:
    value = os.environ.get(name)
    if value is None:
        return False
    return str(value).strip().lower() in {"1", "true", "yes", "on"}


def _enable_rdp_windows() -> None:
    command = (
        "Set-ItemProperty -Path 'HKLM:\\System\\CurrentControlSet\\Control\\Terminal Server' "
        "-Name fDenyTSConnections -Value 0; "
        "Set-Service -Name TermService -StartupType Automatic; "
        "Start-Service -Name TermService; "
        "Enable-NetFirewallRule -DisplayGroup 'Remote Desktop'"
    )
    try:
        result = subprocess.run(
            ["powershell.exe", "-NoProfile", "-Command", command],
            capture_output=True,
            text=True,
            check=False,
        )
        if result.returncode != 0:
            _write_log(f"RDP enable failed: {result.stderr.strip()}")
        else:
            _write_log("RDP enable applied (registry/service/firewall).")
    except Exception as exc:
        _write_log(f"RDP enable failed: {exc}")


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        auto_enable = _bool_env("BOREALIS_RDP_AUTO_ENABLE")
        _write_log(f"RDP role loaded auto_enable={auto_enable}")
        if auto_enable and os.name == "nt":
            _enable_rdp_windows()

    def register_events(self) -> None:
        return

    def stop_all(self) -> None:
        return
