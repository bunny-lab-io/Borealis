import os
import sys
import re
import asyncio
import contextlib
import tempfile
import uuid
import base64
import shutil
import subprocess
from typing import Any, Dict, List, Optional, Tuple

try:
    from qt_compat import QtCore, QtGui, QtWidgets, qt_enum
except Exception:  # pragma: no cover - fallback for runtime path issues
    import sys as _sys
    from pathlib import Path as _Path

    base_dir = _Path(__file__).resolve().parents[1]
    if str(base_dir) not in _sys.path:
        _sys.path.insert(0, str(base_dir))
    from qt_compat import QtCore, QtGui, QtWidgets, qt_enum

try:
    import tray_state
except Exception:  # pragma: no cover - fallback for runtime path issues
    import sys as _sys
    from pathlib import Path as _Path

    base_dir = _Path(__file__).resolve().parents[1]
    if str(base_dir) not in _sys.path:
        _sys.path.insert(0, str(base_dir))
    import tray_state

from signature_utils import decode_script_bytes, verify_and_store_script_signature
try:
    from update_state import busy_activity
except Exception:
    busy_activity = None

ROLE_NAME = 'script_exec_currentuser'
ROLE_CONTEXTS = ['interactive', 'helper']


IS_WINDOWS = os.name == 'nt'
TRAY_POPUP_MARGIN = 18
TRAY_POPUP_WIDTH = 448


def _popup_palette(tone: str) -> Dict[str, str]:
    normalized = str(tone or "healthy").strip().lower() or "healthy"
    palettes = {
        "healthy": {
            "accent": "#38d39f",
            "accent_soft": "rgba(56, 211, 159, 0.16)",
            "accent_border": "rgba(56, 211, 159, 0.42)",
        },
        "neutral": {
            "accent": "#69b7ff",
            "accent_soft": "rgba(105, 183, 255, 0.16)",
            "accent_border": "rgba(105, 183, 255, 0.42)",
        },
        "warning": {
            "accent": "#f0b34c",
            "accent_soft": "rgba(240, 179, 76, 0.16)",
            "accent_border": "rgba(240, 179, 76, 0.42)",
        },
        "error": {
            "accent": "#f06f6f",
            "accent_soft": "rgba(240, 111, 111, 0.16)",
            "accent_border": "rgba(240, 111, 111, 0.42)",
        },
    }
    return palettes.get(normalized, palettes["healthy"])


def _bottom_right_anchor(
    left: int,
    top: int,
    width: int,
    height: int,
    popup_width: int,
    popup_height: int,
    *,
    margin: int = TRAY_POPUP_MARGIN,
) -> Tuple[int, int]:
    x = int(left) + max(int(margin), int(width) - int(popup_width) - int(margin))
    y = int(top) + max(int(margin), int(height) - int(popup_height) - int(margin))
    return x, y


def _warning_lines(view: Dict[str, Any]) -> List[str]:
    return [str(item).strip() for item in (view.get("warnings") or []) if str(item).strip()]


def _supports_qt_ui() -> bool:
    return QtWidgets is not None and QtGui is not None and QtCore is not None


def _background_process_popen_kwargs(cwd: str) -> Dict[str, Any]:
    popen_kwargs: Dict[str, Any] = {
        "cwd": cwd,
        "stdin": subprocess.DEVNULL,
        "stdout": subprocess.DEVNULL,
        "stderr": subprocess.DEVNULL,
    }
    if os.name == "nt":
        creationflags = (
            getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0x00000200)
            | getattr(subprocess, "DETACHED_PROCESS", 0x00000008)
            | getattr(subprocess, "CREATE_NO_WINDOW", 0x08000000)
        )
        if creationflags:
            popen_kwargs["creationflags"] = creationflags
    else:
        popen_kwargs["start_new_session"] = True
    return popen_kwargs


def _canonical_env_key(name: str) -> str:
    cleaned = re.sub(r"[^A-Za-z0-9_]", "_", (name or "").strip())
    return cleaned.upper()


def _sanitize_env_map(raw) -> Dict[str, str]:
    env: Dict[str, str] = {}
    if isinstance(raw, dict):
        for key, value in raw.items():
            if key is None:
                continue
            name = str(key).strip()
            if not name:
                continue
            env_key = _canonical_env_key(name)
            if not env_key:
                continue
            if isinstance(value, bool):
                env_val = "True" if value else "False"
            elif value is None:
                env_val = ""
            else:
                env_val = str(value)
            env[env_key] = env_val
    return env


def _apply_variable_aliases(env_map: Dict[str, str], variables: List[Dict[str, str]]) -> Dict[str, str]:
    if not isinstance(env_map, dict) or not isinstance(variables, list):
        return env_map
    for var in variables:
        if not isinstance(var, dict):
            continue
        name = str(var.get('name') or '').strip()
        if not name:
            continue
        canonical = _canonical_env_key(name)
        if not canonical or canonical not in env_map:
            continue
        value = env_map[canonical]
        alias = re.sub(r"[^A-Za-z0-9_]", "_", name)
        if alias and alias not in env_map:
            env_map[alias] = value
        if alias == name:
            continue
        if re.match(r"^[A-Za-z_][A-Za-z0-9_]*$", name) and name not in env_map:
            env_map[name] = value
    return env_map


def _decode_base64_text(value: str) -> Optional[str]:
    if not isinstance(value, str):
        return None
    stripped = value.strip()
    if not stripped:
        return ""
    try:
        cleaned = re.sub(r"\s+", "", stripped)
    except Exception:
        cleaned = stripped
    try:
        decoded = base64.b64decode(cleaned, validate=True)
    except Exception:
        return None
    try:
        return decoded.decode("utf-8")
    except Exception:
        return decoded.decode("utf-8", errors="replace")


def _decode_script_content(raw_content, encoding_hint) -> str:
    if isinstance(raw_content, str):
        encoding = str(encoding_hint or "").strip().lower()
        if encoding in ("base64", "b64", "base-64"):
            decoded = _decode_base64_text(raw_content)
            if decoded is not None:
                return decoded
        decoded = _decode_base64_text(raw_content)
        if decoded is not None:
            return decoded
        return raw_content
    return ""


def _ps_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _build_wrapped_script(content: str, env_map: Dict[str, str], timeout_seconds: int) -> str:
    def _env_assignment_lines(lines: List[str]) -> None:
        for key, value in (env_map or {}).items():
            if not key:
                continue
            value_literal = _ps_literal(value)
            key_literal = _ps_literal(key)
            env_path_literal = f"[string]::Format('Env:{{0}}', {key_literal})"
            lines.append(
                f"try {{ [System.Environment]::SetEnvironmentVariable({key_literal}, {value_literal}, 'Process') }} catch {{}}"
            )
            lines.append(
                "try { Set-Item -LiteralPath (" + env_path_literal + ") -Value " + value_literal +
                " -ErrorAction Stop } catch { try { New-Item -Path (" + env_path_literal + ") -Value " +
                value_literal + " -Force | Out-Null } catch {} }"
            )

    prelude_lines: List[str] = []
    _env_assignment_lines(prelude_lines)

    prelude = "\n".join(prelude_lines)
    # Keep the assembly body first inside the script block so advanced scripts
    # can legally start with [CmdletBinding()] and param(...).
    inner = content or ""

    pieces: List[str] = []
    if prelude:
        pieces.append(prelude)
    pieces.append("$__BorealisScript = {\n" + inner + "\n}\n")
    script_block = "\n".join(pieces)
    if timeout_seconds and timeout_seconds > 0:
        block = (
            "$job = Start-Job -ScriptBlock $__BorealisScript\n"
            f"if (Wait-Job -Job $job -Timeout {timeout_seconds}) {{\n"
            "  Receive-Job $job\n"
            "} else {\n"
            "  Stop-Job $job -Force\n"
            f"  throw \"Script timed out after {timeout_seconds} seconds\"\n"
            "}\n"
        )
        return script_block + block
    return script_block + "& $__BorealisScript\n"


def _write_temp_script(content: str, suffix: str, env_map: Dict[str, str], timeout_seconds: int):
    temp_dir = os.path.join(tempfile.gettempdir(), "Borealis", "quick_jobs")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="bj_", suffix=suffix, dir=temp_dir, text=True)
    final_content = _build_wrapped_script(content or "", env_map, timeout_seconds)
    with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as fh:
        fh.write(final_content)
    return path


async def _run_powershell_local(path: str):
    if IS_WINDOWS:
        ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
        if not os.path.isfile(ps):
            ps = "powershell.exe"
    else:
        ps = "pwsh"
    try:
        proc = await asyncio.create_subprocess_exec(
            ps,
            "-ExecutionPolicy", "Bypass" if IS_WINDOWS else "Bypass",
            "-NoProfile",
            "-File", path,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            creationflags=(0x08000000 if IS_WINDOWS else 0)
        )
        out_b, err_b = await proc.communicate()
        return proc.returncode, (out_b or b"").decode(errors='replace'), (err_b or b"").decode(errors='replace')
    except Exception as e:
        return -1, "", str(e)


async def _run_subprocess(args: List[str], timeout_seconds: int, *, env: Optional[Dict[str, str]] = None):
    try:
        proc = await asyncio.create_subprocess_exec(
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            creationflags=(0x08000000 if IS_WINDOWS else 0),
            env=env,
        )
        try:
            if timeout_seconds and timeout_seconds > 0:
                out_b, err_b = await asyncio.wait_for(proc.communicate(), timeout=timeout_seconds)
            else:
                out_b, err_b = await proc.communicate()
        except asyncio.TimeoutError:
            with contextlib.suppress(ProcessLookupError):
                proc.kill()
            out_b, err_b = await proc.communicate()
            stdout_text = (out_b or b"").decode(errors="replace")
            stderr_text = (err_b or b"").decode(errors="replace")
            timeout_text = f"Script timed out after {timeout_seconds} seconds"
            if stderr_text:
                stderr_text = f"{stderr_text.rstrip()}\n{timeout_text}"
            else:
                stderr_text = timeout_text
            return -1, stdout_text, stderr_text
        return proc.returncode, (out_b or b"").decode(errors="replace"), (err_b or b"").decode(errors="replace")
    except Exception as exc:
        return -1, "", str(exc)


async def _run_powershell_script_content(content: str, env_map: Dict[str, str], timeout_seconds: int):
    path = _write_temp_script(content or "", ".ps1", env_map, timeout_seconds)
    try:
        return await _run_powershell_local(path)
    finally:
        try:
            if path and os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass


def _resolve_cmd_binary() -> str:
    candidates = []
    if IS_WINDOWS:
        system_root = os.environ.get("SystemRoot") or r"C:\Windows"
        candidates.append(os.path.join(system_root, "System32", "cmd.exe"))
    candidates.extend(["cmd.exe", "cmd"])
    for candidate in candidates:
        if not candidate:
            continue
        if os.path.isabs(candidate):
            if os.path.isfile(candidate):
                return candidate
            continue
        resolved = shutil.which(candidate)
        if resolved:
            return resolved
        return candidate
    return "cmd.exe"


def _resolve_bash_binary() -> str:
    candidates = []
    override = (os.environ.get("BOREALIS_BASH_BIN") or os.environ.get("BOREALIS_REMOTE_BASH_BIN") or "").strip()
    if override:
        candidates.append(override)
    candidates.extend(["bash", "bash.exe", "/bin/bash", "/usr/bin/bash", "sh", "sh.exe", "/bin/sh", "/usr/bin/sh"])
    for candidate in candidates:
        if not candidate:
            continue
        if os.path.isabs(candidate):
            if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
                return candidate
            continue
        resolved = shutil.which(candidate)
        if resolved:
            return resolved
    return ""


async def _run_batch_local(content: str, env_map: Dict[str, str], timeout_seconds: int):
    if not IS_WINDOWS:
        return -1, "", "Batch scripts are only supported on Windows agents."
    temp_dir = os.path.join(tempfile.gettempdir(), "Borealis", "quick_jobs")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="bj_", suffix=".bat", dir=temp_dir, text=True)
    try:
        normalized = (content or "").replace("\r\n", "\n").replace("\r", "\n")
        with os.fdopen(fd, "w", encoding="utf-8", newline="\r\n") as fh:
            fh.write(normalized)
        env = os.environ.copy()
        env.update(env_map or {})
        return await _run_subprocess([_resolve_cmd_binary(), "/D", "/C", path], timeout_seconds, env=env)
    finally:
        try:
            if os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass


async def _run_bash_local(content: str, env_map: Dict[str, str], timeout_seconds: int):
    bash_bin = _resolve_bash_binary()
    if not bash_bin:
        return -1, "", "Bash is not available on this agent."
    temp_dir = os.path.join(tempfile.gettempdir(), "Borealis", "quick_jobs")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="bj_", suffix=".sh", dir=temp_dir, text=True)
    try:
        normalized = (content or "").replace("\r\n", "\n").replace("\r", "\n")
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(normalized)
        if not IS_WINDOWS:
            os.chmod(path, 0o700)
        env = os.environ.copy()
        env.update(env_map or {})
        return await _run_subprocess([bash_bin, path], timeout_seconds, env=env)
    finally:
        try:
            if os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass


async def _run_powershell_via_user_task(content: str, env_map: Dict[str, str], timeout_seconds: int):
    if not IS_WINDOWS:
        return -999, '', 'Windows only'
    ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
    if not os.path.isfile(ps):
        ps = 'powershell.exe'
    path = None
    out_path = None
    import tempfile as _tf
    try:
        temp_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..', 'Temp'))
        os.makedirs(temp_dir, exist_ok=True)
        fd, path = _tf.mkstemp(prefix='usr_task_', suffix='.ps1', dir=temp_dir, text=True)
        final_content = _build_wrapped_script(content or '', env_map, timeout_seconds)
        with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as f:
            f.write(final_content)
        out_path = os.path.join(temp_dir, f'out_{uuid.uuid4().hex}.txt')
        name = f"Borealis Agent - Task - {uuid.uuid4().hex} @ CurrentUser"
        task_ps = f"""
$ErrorActionPreference='Continue'
$task = "{name}"
$ps   = "{ps}"
$scr  = "{path}"
$out  = "{out_path}"
try {{ Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue }} catch {{}}
$action   = New-ScheduledTaskAction -Execute $ps -Argument ('-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $scr + '" *> "' + $out + '"')
$settings = New-ScheduledTaskSettingsSet -DeleteExpiredTaskAfter (New-TimeSpan -Minutes 5) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
$principal= New-ScheduledTaskPrincipal -UserId ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName $task -Action $action -Settings $settings -Principal $principal -Force | Out-Null
Start-ScheduledTask -TaskName $task | Out-Null
Start-Sleep -Seconds 2
Get-ScheduledTask -TaskName $task | Out-Null
"""
        proc = await asyncio.create_subprocess_exec(ps, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', task_ps,
                                                    stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
        out_b, err_b = await proc.communicate()
        if proc.returncode != 0:
            return -999, '', (err_b or out_b or b'').decode(errors='replace')
        # Wait a short time for output file; best-effort
        import time as _t
        deadline = _t.time() + (timeout_seconds if timeout_seconds > 0 else 30)
        out_data = ''
        while _t.time() < deadline:
            try:
                if os.path.isfile(out_path) and os.path.getsize(out_path) > 0:
                    with open(out_path, 'r', encoding='utf-8', errors='replace') as f:
                        out_data = f.read()
                    break
            except Exception:
                pass
            await asyncio.sleep(1)
        # Cleanup best-effort
        try:
            await asyncio.create_subprocess_exec('powershell.exe', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', f"try {{ Unregister-ScheduledTask -TaskName '{name}' -Confirm:$false }} catch {{}}")
        except Exception:
            pass
        try:
            if path and os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass
        try:
            if out_path and os.path.isfile(out_path):
                os.remove(out_path)
        except Exception:
            pass
        return 0, out_data or '', ''
    except Exception as e:
        return -999, '', str(e)


class _TrayStatusPopup(QtWidgets.QFrame if QtWidgets is not None else object):
    def __init__(self, role: "Role") -> None:
        if not _supports_qt_ui():
            return
        super().__init__(None)
        self._role = role
        self._feedback_timer = QtCore.QTimer(self)
        self._feedback_timer.setSingleShot(True)
        self._feedback_timer.timeout.connect(self._clear_feedback)

        popup_flag = qt_enum(QtCore.Qt, "WindowType.Popup", getattr(QtCore.Qt, "Popup", 0))
        frameless_flag = qt_enum(
            QtCore.Qt,
            "WindowType.FramelessWindowHint",
            getattr(QtCore.Qt, "FramelessWindowHint", 0),
        )
        stays_on_top_flag = qt_enum(
            QtCore.Qt,
            "WindowType.WindowStaysOnTopHint",
            getattr(QtCore.Qt, "WindowStaysOnTopHint", 0),
        )
        self.setWindowFlags(popup_flag | frameless_flag | stays_on_top_flag)

        translucent_attr = qt_enum(
            QtCore.Qt,
            "WidgetAttribute.WA_TranslucentBackground",
            getattr(QtCore.Qt, "WA_TranslucentBackground", None),
        )
        styled_attr = qt_enum(
            QtCore.Qt,
            "WidgetAttribute.WA_StyledBackground",
            getattr(QtCore.Qt, "WA_StyledBackground", None),
        )
        delete_close_attr = qt_enum(
            QtCore.Qt,
            "WidgetAttribute.WA_DeleteOnClose",
            getattr(QtCore.Qt, "WA_DeleteOnClose", None),
        )
        if translucent_attr is not None:
            self.setAttribute(translucent_attr, True)
        if styled_attr is not None:
            self.setAttribute(styled_attr, True)
        if delete_close_attr is not None:
            self.setAttribute(delete_close_attr, False)
        self.setObjectName("TrayPopupShell")
        self.setMinimumWidth(TRAY_POPUP_WIDTH)
        self.setMaximumWidth(TRAY_POPUP_WIDTH)

        root = QtWidgets.QVBoxLayout(self)
        root.setContentsMargins(0, 0, 0, 0)

        self._panel = QtWidgets.QFrame(self)
        self._panel.setObjectName("TrayPopupPanel")
        panel_layout = QtWidgets.QVBoxLayout(self._panel)
        panel_layout.setContentsMargins(18, 18, 18, 16)
        panel_layout.setSpacing(14)

        header = QtWidgets.QHBoxLayout()
        header.setSpacing(12)
        self._icon_label = QtWidgets.QLabel(self._panel)
        self._icon_label.setFixedSize(40, 40)
        self._icon_label.setAlignment(
            qt_enum(QtCore.Qt, "AlignmentFlag.AlignCenter", getattr(QtCore.Qt, "AlignCenter", 0))
        )
        header.addWidget(self._icon_label, 0)

        title_layout = QtWidgets.QVBoxLayout()
        title_layout.setSpacing(2)
        self._title_label = QtWidgets.QLabel("Borealis Agent", self._panel)
        self._title_label.setObjectName("TrayPopupTitle")
        self._subtitle_label = QtWidgets.QLabel("", self._panel)
        self._subtitle_label.setObjectName("TrayPopupSubtitle")
        self._subtitle_label.setWordWrap(True)
        title_layout.addWidget(self._title_label)
        title_layout.addWidget(self._subtitle_label)
        header.addLayout(title_layout, 1)

        self._status_badge = QtWidgets.QLabel("", self._panel)
        self._status_badge.setObjectName("TrayPopupBadge")
        self._status_badge.setAlignment(
            qt_enum(QtCore.Qt, "AlignmentFlag.AlignCenter", getattr(QtCore.Qt, "AlignCenter", 0))
        )
        header.addWidget(self._status_badge, 0)
        panel_layout.addLayout(header)

        chip_grid = QtWidgets.QGridLayout()
        chip_grid.setHorizontalSpacing(10)
        chip_grid.setVerticalSpacing(10)
        self._security_card, self._security_value = self._create_chip("Security")
        self._activity_card, self._activity_value = self._create_chip("Activity")
        self._wireguard_card, self._wireguard_value = self._create_chip("WireGuard")
        self._checkin_card, self._checkin_value = self._create_chip("Last Check-In")
        chip_grid.addWidget(self._security_card, 0, 0)
        chip_grid.addWidget(self._activity_card, 0, 1)
        chip_grid.addWidget(self._wireguard_card, 1, 0)
        chip_grid.addWidget(self._checkin_card, 1, 1)
        panel_layout.addLayout(chip_grid)

        self._details_card = QtWidgets.QFrame(self._panel)
        self._details_card.setObjectName("TrayPopupCard")
        details_layout = QtWidgets.QGridLayout(self._details_card)
        details_layout.setContentsMargins(14, 14, 14, 14)
        details_layout.setHorizontalSpacing(10)
        details_layout.setVerticalSpacing(8)
        self._connected_value = self._add_detail_row(details_layout, 0, "Connected To")
        self._build_value = self._add_detail_row(details_layout, 1, "Build")
        self._guid_value = self._add_detail_row(details_layout, 2, "Agent ID")
        panel_layout.addWidget(self._details_card)

        self._warning_card = QtWidgets.QFrame(self._panel)
        self._warning_card.setObjectName("TrayPopupWarningCard")
        warning_layout = QtWidgets.QVBoxLayout(self._warning_card)
        warning_layout.setContentsMargins(14, 12, 14, 12)
        warning_layout.setSpacing(6)
        self._warning_title = QtWidgets.QLabel("Attention Needed", self._warning_card)
        self._warning_title.setObjectName("TrayPopupSectionTitle")
        self._warning_body = QtWidgets.QLabel("", self._warning_card)
        self._warning_body.setObjectName("TrayPopupWarningText")
        self._warning_body.setWordWrap(True)
        warning_layout.addWidget(self._warning_title)
        warning_layout.addWidget(self._warning_body)
        panel_layout.addWidget(self._warning_card)

        primary_button_row = QtWidgets.QHBoxLayout()
        primary_button_row.setSpacing(10)
        self._restart_button = QtWidgets.QPushButton("Restart Agent", self._panel)
        self._restart_button.setObjectName("TrayPopupPrimaryButton")
        self._restart_button.clicked.connect(self._role._restart_agent)
        primary_button_row.addWidget(self._restart_button)
        panel_layout.addLayout(primary_button_row)

        secondary_button_row = QtWidgets.QHBoxLayout()
        secondary_button_row.setSpacing(10)
        copy_button = QtWidgets.QPushButton("Copy Support Details", self._panel)
        copy_button.setObjectName("TrayPopupButton")
        copy_button.clicked.connect(self._copy_support_details)
        open_logs_button = QtWidgets.QPushButton("Open Logs Folder", self._panel)
        open_logs_button.setObjectName("TrayPopupButton")
        open_logs_button.clicked.connect(self._open_logs_folder)
        close_button = QtWidgets.QPushButton("Close", self._panel)
        close_button.setObjectName("TrayPopupButton")
        close_button.clicked.connect(self.hide)
        secondary_button_row.addWidget(copy_button, 1)
        secondary_button_row.addWidget(open_logs_button, 1)
        secondary_button_row.addWidget(close_button, 0)
        panel_layout.addLayout(secondary_button_row)

        self._feedback_label = QtWidgets.QLabel("", self._panel)
        self._feedback_label.setObjectName("TrayPopupFeedback")
        self._feedback_label.setVisible(False)
        panel_layout.addWidget(self._feedback_label)

        self._legal_label = QtWidgets.QLabel(
            "Borealis is distributed under AGPL-3.0. Source and license text ship with the project.",
            self._panel,
        )
        self._legal_label.setObjectName("TrayPopupLegal")
        self._legal_label.setWordWrap(True)
        panel_layout.addWidget(self._legal_label)

        root.addWidget(self._panel)

        try:
            shadow = QtWidgets.QGraphicsDropShadowEffect(self)
            shadow.setBlurRadius(28)
            shadow.setOffset(0, 10)
            shadow.setColor(QtGui.QColor(2, 6, 23, 160))
            self._panel.setGraphicsEffect(shadow)
        except Exception:
            pass

        self.apply_view({})

    def _create_chip(self, label: str) -> Tuple[Any, Any]:
        card = QtWidgets.QFrame(self._panel)
        card.setObjectName("TrayPopupChip")
        layout = QtWidgets.QVBoxLayout(card)
        layout.setContentsMargins(12, 10, 12, 10)
        layout.setSpacing(4)
        title = QtWidgets.QLabel(label, card)
        title.setObjectName("TrayPopupChipLabel")
        value = QtWidgets.QLabel("", card)
        value.setObjectName("TrayPopupChipValue")
        value.setWordWrap(True)
        layout.addWidget(title)
        layout.addWidget(value)
        return card, value

    def _add_detail_row(self, layout: Any, row: int, label: str) -> Any:
        key_label = QtWidgets.QLabel(label, self._details_card)
        key_label.setObjectName("TrayPopupDetailLabel")
        value_label = QtWidgets.QLabel("", self._details_card)
        value_label.setObjectName("TrayPopupDetailValue")
        value_label.setWordWrap(True)
        layout.addWidget(key_label, row, 0)
        layout.addWidget(value_label, row, 1)
        return value_label

    def _copy_support_details(self) -> None:
        view = self._role._last_tray_view or self._role._load_tray_view()
        self._role._copy_support_details(view)
        self.flash_feedback("Support details copied.")

    def _open_logs_folder(self) -> None:
        view = self._role._last_tray_view or self._role._load_tray_view()
        self._role._open_logs_folder(view)
        self.flash_feedback("Logs folder request sent.")

    def flash_feedback(self, message: str) -> None:
        self._feedback_label.setText(str(message or "").strip())
        self._feedback_label.setVisible(bool(self._feedback_label.text()))
        self._feedback_timer.start(3000)

    def _clear_feedback(self) -> None:
        self._feedback_label.clear()
        self._feedback_label.setVisible(False)

    def apply_view(self, view: Dict[str, Any]) -> None:
        current_view = dict(view or {})
        palette = _popup_palette(current_view.get("icon_tone"))
        self._panel.setStyleSheet(
            f"""
            QFrame#TrayPopupPanel {{
                background: qlineargradient(x1:0, y1:0, x2:1, y2:1, stop:0 #07111d, stop:1 #0d1728);
                border: 1px solid rgba(84, 112, 146, 0.32);
                border-radius: 18px;
            }}
            QFrame#TrayPopupCard, QFrame#TrayPopupChip {{
                background-color: rgba(11, 20, 35, 0.88);
                border: 1px solid rgba(84, 112, 146, 0.22);
                border-radius: 14px;
            }}
            QFrame#TrayPopupWarningCard {{
                background-color: rgba(33, 18, 16, 0.92);
                border: 1px solid rgba(240, 111, 111, 0.28);
                border-radius: 14px;
            }}
            QLabel#TrayPopupTitle {{
                color: #f8fafc;
                font-size: 16px;
                font-weight: 700;
            }}
            QLabel#TrayPopupSubtitle {{
                color: #9eb0c8;
                font-size: 11px;
            }}
            QLabel#TrayPopupBadge {{
                background-color: {palette["accent_soft"]};
                border: 1px solid {palette["accent_border"]};
                border-radius: 999px;
                color: {palette["accent"]};
                font-size: 11px;
                font-weight: 700;
                padding: 6px 12px;
            }}
            QLabel#TrayPopupChipLabel, QLabel#TrayPopupDetailLabel, QLabel#TrayPopupSectionTitle {{
                color: #8ea4b8;
                font-size: 10px;
                font-weight: 700;
            }}
            QLabel#TrayPopupChipValue, QLabel#TrayPopupDetailValue {{
                color: #eef4ff;
                font-size: 12px;
                font-weight: 600;
            }}
            QLabel#TrayPopupWarningText {{
                color: #fce8e8;
                font-size: 11px;
            }}
            QLabel#TrayPopupFeedback {{
                color: {palette["accent"]};
                font-size: 11px;
                font-weight: 700;
            }}
            QLabel#TrayPopupLegal {{
                color: #7d8ba1;
                font-size: 10px;
            }}
            QPushButton#TrayPopupPrimaryButton {{
                background-color: {palette["accent"]};
                border: none;
                border-radius: 12px;
                color: #06111d;
                font-size: 12px;
                font-weight: 700;
                min-height: 36px;
                padding: 0 14px;
            }}
            QPushButton#TrayPopupPrimaryButton:disabled {{
                background-color: rgba(84, 112, 146, 0.3);
                color: #7d8ba1;
            }}
            QPushButton#TrayPopupPrimaryButton:hover:!disabled {{
                background-color: #ffffff;
            }}
            QPushButton#TrayPopupButton {{
                background-color: rgba(15, 25, 42, 0.95);
                border: 1px solid rgba(84, 112, 146, 0.24);
                border-radius: 12px;
                color: #dce7f7;
                font-size: 11px;
                font-weight: 600;
                min-height: 34px;
                padding: 0 12px;
            }}
            QPushButton#TrayPopupButton:hover {{
                border-color: {palette["accent_border"]};
                color: #ffffff;
            }}
            """
        )
        self._status_badge.setText(str(current_view.get("overall_status") or "Starting up"))
        self._title_label.setText(str(current_view.get("device_name") or "Borealis Agent"))
        self._subtitle_label.setText(
            f"{current_view.get('connected_host') or 'Not configured'} • {current_view.get('last_check_in') or 'Never'}"
        )
        self._security_value.setText(str(current_view.get("security_status") or "Checking connection"))
        self._activity_value.setText(str(current_view.get("activity_status") or "Idle"))
        self._wireguard_value.setText(str(current_view.get("wireguard_status") or "Unavailable"))
        self._checkin_value.setText(str(current_view.get("last_check_in") or "Never"))
        self._connected_value.setText(str(current_view.get("connected_host") or "Not configured"))
        self._build_value.setText(str(current_view.get("build_id") or "Unknown"))
        self._guid_value.setText(str(current_view.get("agent_guid") or "Unknown"))
        warnings = _warning_lines(current_view)
        self._warning_card.setVisible(bool(warnings))
        self._warning_body.setText("\n".join(f"• {item}" for item in warnings))
        restarting = str(current_view.get("overall_status") or "").strip().lower() == "restarting"
        self._restart_button.setEnabled(not restarting)
        self._restart_button.setText("Restart Requested" if restarting else "Restart Agent")

        tray_icon = None
        try:
            if self._role.tray is not None:
                tray_icon = self._role.tray.icon()
        except Exception:
            tray_icon = None
        if tray_icon is None:
            tray_icon = getattr(self._role, "_base_tray_icon", None)
        if tray_icon is not None:
            try:
                pixmap = tray_icon.pixmap(36, 36)
                if not pixmap.isNull():
                    self._icon_label.setPixmap(pixmap)
                    return
            except Exception:
                pass
        self._icon_label.clear()


class Role:
    def __init__(self, ctx):
        self.ctx = ctx
        self.role_health_label = "Script Execution - CURRENTUSER"
        self._listener_registered = False
        self.tray = None
        self._tray_popup = None
        self._tray_timer = None
        self._tray_icon_cache: Dict[str, Any] = {}
        self._base_tray_icon = None
        self._restart_pending = False
        self._last_tray_view: Dict[str, Any] = {}
        # Setup tray icon in interactive session
        try:
            self._setup_tray()
        except Exception:
            pass

    def health_report(self) -> dict:
        if not self._listener_registered:
            return {
                'status': 'unhealthy',
                'role_label': self.role_health_label,
                'detail': 'Quick job listener was not registered.',
                'details': {
                    'running_status': 'Stopped',
                    'execution_context': 'CURRENTUSER',
                    'listener_state': 'Not Registered',
                },
            }
        return {
            'status': 'healthy',
            'role_label': self.role_health_label,
            'detail': 'Quick job listener ready for CURRENTUSER script execution.',
            'details': {
                'running_status': 'Ready',
                'execution_context': 'CURRENTUSER',
                'listener_state': 'Registered',
            },
        }

    def _log(self, message: str, *, error: bool = False) -> None:
        hooks = getattr(self.ctx, "hooks", {}) or {}
        log_agent_hook = hooks.get("log_agent")
        if callable(log_agent_hook):
            try:
                log_agent_hook(message)
                if error:
                    log_agent_hook(message, fname="agent.error.log")
            except Exception:
                pass

    def _result_payload(
        self,
        *,
        payload: Dict[str, Any],
        job_id: Any,
        status: str,
        stdout: str = "",
        stderr: str = "",
    ) -> Dict[str, Any]:
        result = {
            "job_id": job_id,
            "status": status,
            "stdout": stdout,
            "stderr": stderr,
        }
        context = payload.get("context") if isinstance(payload, dict) else None
        if isinstance(context, dict):
            result["context"] = dict(context)
        return result

    async def _handle_quick_job_run(self, payload: Dict[str, Any]) -> Optional[Dict[str, Any]]:
        try:
            import socket

            hostname = socket.gethostname()
            target = (payload.get("target_hostname") or "").strip().lower()
            if target and target != hostname.lower():
                return None

            job_id = payload.get("job_id")
            script_type = (payload.get("script_type") or "").lower()
            run_mode = (payload.get("run_mode") or "current_user").lower()
            if run_mode == "system":
                return None

            job_label = job_id if job_id is not None else "unknown"
            self._log(f"quick_job_run(currentuser) received payload job_id={job_label}")

            script_bytes = decode_script_bytes(payload.get("script_content"), payload.get("script_encoding"))
            if script_bytes is None:
                self._log(f"quick_job_run(currentuser) invalid script payload job_id={job_label}", error=True)
                return self._result_payload(
                    payload=payload,
                    job_id=job_id,
                    status="Failed",
                    stderr="Invalid script payload (unable to decode)",
                )

            broker_verified = bool(payload.get("broker_verified"))
            if not broker_verified:
                signature_b64 = payload.get("signature")
                sig_alg = (payload.get("sig_alg") or "ed25519").lower()
                signing_key = payload.get("signing_key")
                if sig_alg and sig_alg not in ("ed25519", "eddsa"):
                    self._log(
                        f"quick_job_run(currentuser) unsupported signature algorithm job_id={job_label} alg={sig_alg}",
                        error=True,
                    )
                    return self._result_payload(
                        payload=payload,
                        job_id=job_id,
                        status="Failed",
                        stderr=f"Unsupported script signature algorithm: {sig_alg}",
                    )
                if not isinstance(signature_b64, str) or not signature_b64.strip():
                    self._log(f"quick_job_run(currentuser) missing signature job_id={job_label}", error=True)
                    return self._result_payload(
                        payload=payload,
                        job_id=job_id,
                        status="Failed",
                        stderr="Missing script signature; rejecting payload",
                    )
                http_client_fn = getattr(self.ctx, "hooks", {}).get("http_client") if hasattr(self.ctx, "hooks") else None
                client = http_client_fn() if callable(http_client_fn) else None
                if client is None:
                    self._log(f"quick_job_run(currentuser) missing http_client hook job_id={job_label}", error=True)
                    return self._result_payload(
                        payload=payload,
                        job_id=job_id,
                        status="Failed",
                        stderr="Signature verification unavailable (client missing)",
                    )
                if not verify_and_store_script_signature(client, script_bytes, signature_b64, signing_key):
                    self._log(f"quick_job_run(currentuser) signature verification failed job_id={job_label}", error=True)
                    return self._result_payload(
                        payload=payload,
                        job_id=job_id,
                        status="Failed",
                        stderr="Rejected script payload due to invalid signature",
                    )
                self._log(f"quick_job_run(currentuser) signature verified job_id={job_label}")

            content = script_bytes.decode("utf-8", errors="replace")
            raw_env = payload.get("environment")
            env_map = _sanitize_env_map(raw_env)
            variables = payload.get("variables") if isinstance(payload.get("variables"), list) else []
            for var in variables:
                if not isinstance(var, dict):
                    continue
                name = str(var.get("name") or "").strip()
                if not name:
                    continue
                key = _canonical_env_key(name)
                if key in env_map:
                    continue
                default_val = var.get("default")
                if isinstance(default_val, bool):
                    env_map[key] = "True" if default_val else "False"
                elif default_val is None:
                    env_map[key] = ""
                else:
                    env_map[key] = str(default_val)
            env_map = _apply_variable_aliases(env_map, variables)
            try:
                timeout_seconds = max(0, int(payload.get("timeout_seconds") or 0))
            except Exception:
                timeout_seconds = 0

            busy_ctx = (
                busy_activity(
                    "quick_job_currentuser",
                    metadata={
                        "job_id": str(job_label),
                        "script_type": script_type or "unknown",
                    },
                )
                if callable(busy_activity)
                else contextlib.nullcontext()
            )
            with busy_ctx:
                if script_type == "powershell":
                    rc, out, err = await _run_powershell_script_content(content, env_map, timeout_seconds)
                elif script_type == "batch":
                    rc, out, err = await _run_batch_local(content, env_map, timeout_seconds)
                elif script_type == "bash":
                    rc, out, err = await _run_bash_local(content, env_map, timeout_seconds)
                else:
                    return self._result_payload(
                        payload=payload,
                        job_id=job_id,
                        status="Failed",
                        stderr=f"Unsupported type: {script_type}",
                    )
            return self._result_payload(
                payload=payload,
                job_id=job_id,
                status="Success" if rc == 0 else "Failed",
                stdout=out,
                stderr=err,
            )
        except Exception as exc:
            return self._result_payload(
                payload=payload if isinstance(payload, dict) else {},
                job_id=payload.get("job_id") if isinstance(payload, dict) else None,
                status="Failed",
                stderr=str(exc),
            )

    def register_events(self):
        sio = self.ctx.sio
        self._listener_registered = True
        hooks = getattr(self.ctx, 'hooks', {}) or {}
        helper_register = hooks.get("register_local_helper_handler")
        if callable(helper_register):
            helper_register(self._handle_quick_job_run)
            return

        @sio.on("quick_job_run")
        async def _on_quick_job_run(payload):
            result = await self._handle_quick_job_run(payload if isinstance(payload, dict) else {})
            if not isinstance(result, dict):
                return
            try:
                await sio.emit("quick_job_result", result)
            except Exception:
                pass

    def _setup_tray(self):
        if not _supports_qt_ui():
            return
        app = QtWidgets.QApplication.instance()
        if app is None:
            return
        self._base_tray_icon = self._load_base_tray_icon(app)
        self.tray = QtWidgets.QSystemTrayIcon(self._base_tray_icon)
        self.tray.setToolTip("Borealis Agent")
        self.tray.activated.connect(self._handle_tray_activation)
        self._tray_timer = QtCore.QTimer(self.tray)
        self._tray_timer.setInterval(15000)
        self._tray_timer.timeout.connect(self._refresh_tray_view)
        self._tray_timer.start()
        self._refresh_tray_view()
        self.tray.show()

    def _restart_agent(self):
        if QtWidgets is None:
            return
        message_box = getattr(QtWidgets, "QMessageBox", None)
        if message_box is None:
            return
        confirm = message_box.question(
            getattr(self, "_tray_popup", None),
            "Restart Borealis Agent",
            "Restart the Borealis Agent now?\n\nRemote support activity may pause briefly while the agent reconnects.\n\nPlease wait up to 1 minute for the agent restart request to trigger.",
            message_box.Yes | message_box.No,
            message_box.No,
        )
        if confirm != message_box.Yes:
            return
        try:
            tray_state.request_restart(
                ("currentuser", "system"),
                requested_by="tray",
                requested_by_pid=os.getpid(),
            )
        except Exception:
            return
        self._restart_pending = True
        self._refresh_tray_view()
        if IS_WINDOWS:
            self._exit_app()
            return
        hooks = getattr(self.ctx, "hooks", {}) or {}
        processor = hooks.get("process_restart_request_now")
        if callable(processor):
            try:
                if bool(processor()):
                    return
            except Exception:
                pass
        try:
            tray_state.clear_restart_request("currentuser")
        except Exception:
            pass
        if self._spawn_currentuser_agent():
            self._exit_app()

    def stop_all(self):
        try:
            if self._tray_timer is not None:
                self._tray_timer.stop()
        except Exception:
            pass
        try:
            if self._tray_popup is not None:
                self._tray_popup.hide()
        except Exception:
            pass
        try:
            if self.tray is not None:
                self.tray.hide()
        except Exception:
            pass

    def _load_base_tray_icon(self, app):
        icon = None
        try:
            icon_path = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir, "Borealis.ico"))
            if os.path.isfile(icon_path):
                icon = QtGui.QIcon(icon_path)
        except Exception:
            icon = None
        if icon is None:
            standard_icon = qt_enum(
                QtWidgets.QStyle,
                "StandardPixmap.SP_ComputerIcon",
                getattr(QtWidgets.QStyle, "SP_ComputerIcon", None),
            )
            if standard_icon is not None:
                icon = app.style().standardIcon(standard_icon)
        if icon is None:
            icon = QtGui.QIcon()
        return icon

    def _load_tray_view(self) -> Dict[str, Any]:
        current_snapshot = None
        hooks = getattr(self.ctx, "hooks", {}) or {}
        sync_hook = hooks.get("sync_tray_status")
        if callable(sync_hook):
            try:
                current_snapshot = sync_hook({})
            except Exception:
                current_snapshot = None
        view = tray_state.build_tray_view(
            current_snapshot=current_snapshot,
            current_session_active=True,
        )
        if self._restart_pending and view.get("overall_status") != "Restarting":
            view = dict(view)
            view["overall_status"] = "Restarting"
            view["security_status"] = "Checking connection"
            view["tooltip"] = "Borealis Agent"
            view["menu_entries"] = tray_state.build_menu_entries(view)
            view["support_details"] = tray_state.build_support_details(view)
            view["support_text"] = tray_state.format_support_details(view)
        return view

    def _refresh_tray_view(self):
        if self.tray is None:
            return
        view = self._load_tray_view()
        self._last_tray_view = view
        self._apply_tray_icon(view)
        self.tray.setToolTip(str(view.get("tooltip") or "Borealis Agent"))
        if self._tray_popup is not None and self._tray_popup.isVisible():
            self._tray_popup.apply_view(view)
            self._position_tray_popup()

    def _apply_tray_icon(self, view: Dict[str, Any]) -> None:
        if self.tray is None or self._base_tray_icon is None or QtGui is None:
            return
        tone = str(view.get("icon_tone") or "healthy").strip().lower() or "healthy"
        if tone == "healthy":
            self.tray.setIcon(self._base_tray_icon)
            return
        cached = self._tray_icon_cache.get(tone)
        if cached is not None:
            self.tray.setIcon(cached)
            return
        pixmap = self._base_tray_icon.pixmap(32, 32)
        if pixmap.isNull():
            self.tray.setIcon(self._base_tray_icon)
            return
        color_map = {
            "neutral": "#8ea4b8",
            "warning": "#d8a53f",
            "error": "#d9544f",
        }
        badge_color = color_map.get(tone)
        if not badge_color:
            self.tray.setIcon(self._base_tray_icon)
            return
        canvas = QtGui.QPixmap(pixmap)
        painter = QtGui.QPainter(canvas)
        antialias_hint = qt_enum(
            QtGui.QPainter,
            "RenderHint.Antialiasing",
            getattr(QtGui.QPainter, "Antialiasing", None),
        )
        if antialias_hint is not None:
            painter.setRenderHint(antialias_hint, True)
        painter.setPen(QtGui.QPen(QtGui.QColor("#0f172a"), 2))
        painter.setBrush(QtGui.QBrush(QtGui.QColor(badge_color)))
        painter.drawEllipse(canvas.width() - 14, canvas.height() - 14, 12, 12)
        painter.end()
        icon = QtGui.QIcon(canvas)
        self._tray_icon_cache[tone] = icon
        self.tray.setIcon(icon)

    def _show_status_details(self):
        self._show_tray_popup()

    def _build_status_details_text(self, view: Dict[str, Any]) -> str:
        lines = [str(view.get("support_text") or "").strip()]
        wireguard_detail = str(view.get("wireguard_detail") or "").strip()
        if wireguard_detail:
            lines.append("")
            lines.append(f"WireGuard Detail: {wireguard_detail}")
        logs_dir = str(view.get("logs_dir") or "").strip()
        if logs_dir:
            lines.append(f"Logs Folder: {logs_dir}")
        return "\n".join(line for line in lines if line is not None)

    def _copy_support_details(self, view: Dict[str, Any]) -> None:
        if QtWidgets is None:
            return
        app = QtWidgets.QApplication.instance()
        if app is None:
            return
        clipboard = app.clipboard()
        if clipboard is None:
            return
        clipboard.setText(str(view.get("support_text") or ""))

    def _open_logs_folder(self, view: Dict[str, Any]) -> None:
        logs_dir = str(view.get("logs_dir") or "").strip()
        if not logs_dir:
            return
        try:
            if os.name == "nt" and hasattr(os, "startfile"):
                os.startfile(logs_dir)
                return
            subprocess.Popen(["xdg-open", logs_dir])
        except Exception:
            return

    def _ensure_tray_popup(self):
        if not _supports_qt_ui():
            return None
        if self._tray_popup is None:
            self._tray_popup = _TrayStatusPopup(self)
        return self._tray_popup

    def _handle_tray_activation(self, reason: Any) -> None:
        tray_icon_type = getattr(QtWidgets, "QSystemTrayIcon", None)
        if tray_icon_type is None:
            return
        trigger_reason = qt_enum(
            tray_icon_type,
            "ActivationReason.Trigger",
            getattr(tray_icon_type, "Trigger", None),
        )
        context_reason = qt_enum(
            tray_icon_type,
            "ActivationReason.Context",
            getattr(tray_icon_type, "Context", None),
        )
        double_click_reason = qt_enum(
            tray_icon_type,
            "ActivationReason.DoubleClick",
            getattr(tray_icon_type, "DoubleClick", None),
        )
        if reason not in {trigger_reason, context_reason, double_click_reason}:
            return
        popup = self._ensure_tray_popup()
        if popup is None:
            return
        if popup.isVisible():
            popup.hide()
            return
        self._show_tray_popup()

    def _show_tray_popup(self) -> None:
        popup = self._ensure_tray_popup()
        if popup is None:
            return
        latest_view = self._load_tray_view()
        self._last_tray_view = latest_view
        self._apply_tray_icon(latest_view)
        if self.tray is not None:
            self.tray.setToolTip(str(latest_view.get("tooltip") or "Borealis Agent"))
        popup.apply_view(latest_view)
        popup.adjustSize()
        self._position_tray_popup()
        popup.show()
        raise_window = getattr(popup, "raise_", None) or getattr(popup, "raise", None)
        if callable(raise_window):
            raise_window()
        popup.activateWindow()

    def _position_tray_popup(self) -> None:
        popup = self._tray_popup
        if popup is None or QtGui is None or QtWidgets is None:
            return
        screen = None
        try:
            qgui_app = getattr(QtGui, "QGuiApplication", None)
            if qgui_app is not None and hasattr(QtGui, "QCursor"):
                screen = qgui_app.screenAt(QtGui.QCursor.pos())
        except Exception:
            screen = None
        if screen is None:
            app = QtWidgets.QApplication.instance()
            if app is not None:
                try:
                    screen = app.primaryScreen()
                except Exception:
                    screen = None
        if screen is None:
            return
        geometry = screen.availableGeometry()
        popup.adjustSize()
        size_hint = popup.sizeHint()
        x, y = _bottom_right_anchor(
            geometry.x(),
            geometry.y(),
            geometry.width(),
            geometry.height(),
            size_hint.width(),
            size_hint.height(),
        )
        popup.move(x, y)

    def _spawn_currentuser_agent(self) -> bool:
        try:
            borealis_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir))
            venv_root = os.path.abspath(os.path.join(borealis_dir, os.pardir))
            venv_scripts = os.path.join(venv_root, "Scripts")
            preferred = [
                os.path.join(venv_scripts, "pythonw.exe"),
                os.path.join(venv_scripts, "python.exe"),
                sys.executable,
            ]
            exe = ""
            for candidate in preferred:
                if candidate and os.path.isfile(candidate):
                    exe = candidate
                    break
            if not exe:
                exe = sys.executable
            agent_script = os.path.join(borealis_dir, "agent.py")
            popen_kwargs = _background_process_popen_kwargs(borealis_dir)
            subprocess.Popen(
                [exe, "-W", "ignore::SyntaxWarning", agent_script, "--config", "CURRENTUSER"],
                **popen_kwargs,
            )
            return True
        except Exception:
            return False

    def _exit_app(self) -> None:
        try:
            app = QtWidgets.QApplication.instance() if QtWidgets is not None else None
            if app is not None:
                app.quit()
                return
        except Exception:
            pass
        os._exit(0)
