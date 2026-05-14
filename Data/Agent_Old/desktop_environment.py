"""Desktop-environment detection helpers for Agent role health."""
from __future__ import annotations

import glob
import os
import subprocess
import sys
from typing import Iterable


_DISPLAY_MANAGER_UNITS = (
    "display-manager.service",
    "gdm.service",
    "gdm3.service",
    "sddm.service",
    "lightdm.service",
    "xdm.service",
)

_DESKTOP_PROCESS_HINTS = (
    "Xorg",
    "Xwayland",
    "wayland",
    "gnome-shell",
    "kwin_wayland",
    "kwin_x11",
    "xfce4-session",
    "startplasma-wayland",
    "startplasma-x11",
    "cinnamon-session",
    "mate-session",
    "lxqt-session",
)


def _truthy_text(value: object) -> bool:
    return bool(str(value or "").strip())


def _run_command(command: Iterable[str], *, timeout: float = 2.0) -> subprocess.CompletedProcess:
    return subprocess.run(
        list(command),
        capture_output=True,
        text=True,
        timeout=timeout,
        check=False,
    )


def _systemd_display_manager_active() -> bool:
    if not os.path.isdir("/run/systemd/system"):
        return False
    try:
        result = _run_command(["systemctl", "is-active", *list(_DISPLAY_MANAGER_UNITS)])
    except Exception:
        return False
    states = {line.strip().lower() for line in str(result.stdout or "").splitlines() if line.strip()}
    return "active" in states or "activating" in states


def _display_socket_present() -> bool:
    candidates = []
    candidates.extend(glob.glob("/tmp/.X11-unix/X*"))
    candidates.extend(glob.glob("/run/user/*/wayland-*"))
    return any(os.path.exists(path) for path in candidates)


def _desktop_process_present() -> bool:
    try:
        result = _run_command(["ps", "-eo", "comm,args"], timeout=2.0)
    except Exception:
        return False
    if result.returncode != 0:
        return False
    haystack = str(result.stdout or "")
    return any(hint in haystack for hint in _DESKTOP_PROCESS_HINTS)


def desktop_environment_active() -> bool:
    """Return True when an interactive graphical desktop appears active."""

    if os.name == "nt":
        return True
    if sys.platform == "darwin":
        return True
    if not sys.platform.startswith("linux"):
        return False
    if _truthy_text(os.environ.get("DISPLAY")) or _truthy_text(os.environ.get("WAYLAND_DISPLAY")):
        return True
    if _truthy_text(os.environ.get("XDG_CURRENT_DESKTOP")) or _truthy_text(os.environ.get("DESKTOP_SESSION")):
        return True
    if _systemd_display_manager_active():
        return True
    if _display_socket_present():
        return True
    return _desktop_process_present()


def no_desktop_environment_detail() -> str:
    return "No Desktop Environment Active."
