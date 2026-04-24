from __future__ import annotations

import asyncio
import codecs
import contextlib
import locale
import mimetypes
import os
import platform
import shutil
import socket
import stat as stat_module
import string
import subprocess
import tempfile
import time
import zipfile
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional

try:
    from Roles.system_task_orchestration import LaneCoordinator
except ModuleNotFoundError as exc:  # pragma: no cover - package import fallback
    if not str(getattr(exc, "name", "") or "").startswith("Roles"):
        raise
    from Data.Agent.Roles.system_task_orchestration import LaneCoordinator


ROLE_NAME = "file_management"
ROLE_CONTEXTS = ["system"]

IS_WINDOWS = os.name == "nt"
IS_LINUX = platform.system().lower() == "linux"
STATUS_PROGRESS_INTERVAL_SECONDS = 2.0
TEXT_EDITOR_MAX_BYTES = int(os.environ.get("BOREALIS_FILE_EDITOR_MAX_BYTES", 1024 * 1024))
TRANSFER_CONTROL_POLL_SECONDS = 1.0


class FileManagementError(RuntimeError):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = str(code or "file_management_error").strip() or "file_management_error"
        self.message = str(message or self.code).strip() or self.code


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _hostname() -> str:
    try:
        return _clean_text(socket.gethostname())
    except Exception:
        return ""


def _normalize_upload_name(value: Any) -> str:
    raw = _clean_text(value).replace("\\", "/")
    if not raw:
        return ""
    return Path(raw).name.strip().replace("\x00", "")


def _normalize_archive_name(value: Any, suffix: str) -> str:
    normalized = _normalize_upload_name(value)
    desired_suffix = _clean_text(suffix) or ".zip"
    if not desired_suffix.startswith("."):
        desired_suffix = f".{desired_suffix}"
    if not normalized:
        return f"download{desired_suffix}"
    source = Path(normalized)
    stem = source.stem if source.suffix else normalized
    stem = _clean_text(stem) or "download"
    return f"{stem}{desired_suffix}"


def _normalize_requested_path(value: Any) -> str:
    raw = _clean_text(value)
    if not raw:
        raise FileManagementError("invalid_path", "A non-empty absolute path is required.")
    if IS_WINDOWS:
        candidate = raw.replace("/", "\\")
        if candidate.startswith("\\\\"):
            normalized = os.path.normpath(candidate)
            return normalized
        if len(candidate) >= 3 and candidate[1:3] == ":\\" and candidate[0].isalpha():
            normalized = os.path.normpath(candidate)
            if len(normalized) == 2 and normalized[1] == ":":
                normalized += "\\"
            return normalized
        raise FileManagementError("invalid_path", f"Expected an absolute Windows path, received '{raw}'.")
    if not raw.startswith("/"):
        raise FileManagementError("invalid_path", f"Expected an absolute POSIX path, received '{raw}'.")
    normalized = os.path.normpath(raw)
    return normalized or "/"


def _parent_path(path_value: str) -> str:
    normalized = _normalize_requested_path(path_value)
    if IS_WINDOWS:
        drive, tail = os.path.splitdrive(normalized)
        stripped = normalized.rstrip("\\/")
        if not tail or stripped.lower() == drive.rstrip("\\/").lower():
            return ""
        parent = os.path.dirname(stripped)
        if parent.lower() == drive.lower():
            parent += "\\"
        return parent
    if normalized == "/":
        return "/"
    parent = os.path.dirname(normalized) or "/"
    return parent


def _path_exists(path_value: str) -> bool:
    try:
        return os.path.lexists(path_value)
    except Exception:
        return False


def _ensure_directory(path_value: str) -> str:
    normalized = _normalize_requested_path(path_value)
    if not _path_exists(normalized):
        raise FileManagementError("path_not_found", f"'{normalized}' does not exist.")
    if not os.path.isdir(normalized):
        raise FileManagementError("not_a_directory", f"'{normalized}' is not a directory.")
    return normalized


def _safe_lstat(path_value: str):
    try:
        return os.lstat(path_value)
    except FileNotFoundError as exc:
        raise FileManagementError("path_not_found", f"'{path_value}' does not exist.") from exc
    except PermissionError as exc:
        raise FileManagementError("permission_denied", f"Permission denied for '{path_value}'.") from exc
    except OSError as exc:
        raise FileManagementError("invalid_path", str(exc)) from exc


def _is_hidden(name: str, path_value: str, stat_result) -> bool:
    if _clean_text(name).startswith("."):
        return True
    if IS_WINDOWS:
        file_attrs = getattr(stat_result, "st_file_attributes", 0)
        return bool(file_attrs & getattr(stat_module, "FILE_ATTRIBUTE_HIDDEN", 0))
    return False


def _attributes_for_path(name: str, path_value: str, stat_result, *, is_dir: bool, is_symlink: bool) -> list[str]:
    attributes: list[str] = []
    if _is_hidden(name, path_value, stat_result):
        attributes.append("hidden")
    if not os.access(path_value, os.W_OK):
        attributes.append("read_only")
    if is_symlink:
        attributes.append("symlink")
    if IS_WINDOWS:
        file_attrs = getattr(stat_result, "st_file_attributes", 0)
        if file_attrs & getattr(stat_module, "FILE_ATTRIBUTE_SYSTEM", 0):
            attributes.append("system")
        if file_attrs & getattr(stat_module, "FILE_ATTRIBUTE_REPARSE_POINT", 0):
            attributes.append("reparse_point")
    if is_dir:
        attributes.append("directory")
    return attributes


def _entry_from_path(path_value: str, *, parent_path: str = "", force_name: str = "") -> Dict[str, Any]:
    normalized = _normalize_requested_path(path_value)
    stat_result = _safe_lstat(normalized)
    is_symlink = stat_module.S_ISLNK(stat_result.st_mode)
    is_dir = os.path.isdir(normalized)
    name = force_name or (
        normalized
        if (IS_WINDOWS and len(normalized) <= 3 and normalized[1:3] == ":\\")
        else (os.path.basename(normalized.rstrip("\\/")) if normalized not in {"/"} else "/")
    )
    kind = "directory" if is_dir and not is_symlink else "symlink" if is_symlink else "file"
    attributes = _attributes_for_path(name, normalized, stat_result, is_dir=is_dir, is_symlink=is_symlink)
    return {
        "path": normalized,
        "parent_path": parent_path,
        "name": name,
        "kind": kind,
        "size_bytes": 0 if is_dir else int(getattr(stat_result, "st_size", 0) or 0),
        "modified_at": int(getattr(stat_result, "st_mtime", 0) or 0),
        "attributes": attributes,
        "has_children": bool(is_dir and not is_symlink),
        "is_hidden": "hidden" in attributes,
    }


def _sort_entries(entries: Iterable[Dict[str, Any]]) -> list[Dict[str, Any]]:
    def _key(row: Dict[str, Any]):
        kind = _clean_text(row.get("kind")).lower()
        name = _clean_text(row.get("name")).lower()
        return (0 if kind == "directory" else 1, name)

    return sorted((dict(row) for row in entries), key=_key)


def _list_children(path_value: str) -> list[Dict[str, Any]]:
    parent_path = _ensure_directory(path_value)
    entries: list[Dict[str, Any]] = []
    try:
        with os.scandir(parent_path) as scan:
            for item in scan:
                try:
                    entries.append(_entry_from_path(item.path, parent_path=parent_path))
                except FileManagementError:
                    continue
    except PermissionError as exc:
        raise FileManagementError("permission_denied", f"Permission denied for '{parent_path}'.") from exc
    except OSError as exc:
        raise FileManagementError("invalid_path", str(exc)) from exc
    return _sort_entries(entries)


def _roots_payload() -> Dict[str, Any]:
    if IS_WINDOWS:
        roots = []
        for letter in string.ascii_uppercase:
            drive = f"{letter}:\\"
            if not _path_exists(drive):
                continue
            try:
                roots.append(_entry_from_path(drive, parent_path="", force_name=drive))
            except FileManagementError:
                continue
        return {
            "ok": True,
            "platform": "windows",
            "context_label": "SYSTEM",
            "current_path": "",
            "entries": _sort_entries(roots),
        }
    return {
        "ok": True,
        "platform": "linux" if IS_LINUX else platform.system().lower() or "unknown",
        "context_label": "root",
        "current_path": "/",
        "entries": _list_children("/"),
    }


def _validate_child_name(value: Any) -> str:
    name = _normalize_upload_name(value)
    if not name:
        raise FileManagementError("invalid_name", "A file or folder name is required.")
    if name in {".", ".."}:
        raise FileManagementError("invalid_name", "Relative path markers are not allowed.")
    return name


def _normalize_relative_upload_path(value: Any, *, fallback_name: Any = "") -> str:
    raw = _clean_text(value).replace("\\", "/")
    if not raw:
        raw = _validate_child_name(fallback_name)
    if raw.startswith("/") or raw.startswith("\\") or (len(raw) >= 2 and raw[1] == ":"):
        raise FileManagementError("invalid_name", f"Expected a relative upload path, received '{value}'.")
    segments = []
    for segment in raw.split("/"):
        cleaned = _clean_text(segment)
        if not cleaned:
            continue
        if cleaned in {".", ".."}:
            raise FileManagementError("invalid_name", "Relative path markers are not allowed in upload manifests.")
        segments.append(_validate_child_name(cleaned))
    if not segments:
        raise FileManagementError("invalid_name", "A relative upload path is required.")
    return "/".join(segments)


def _path_equal(left: str, right: str) -> bool:
    return os.path.normcase(_normalize_requested_path(left)) == os.path.normcase(_normalize_requested_path(right))


def _destination_inside_source(source_path: str, destination_path: str) -> bool:
    normalized_source = _normalize_requested_path(source_path)
    normalized_destination = _normalize_requested_path(destination_path)
    if not os.path.isdir(normalized_source) or os.path.islink(normalized_source):
        return False
    try:
        common = os.path.commonpath([normalized_source, normalized_destination])
    except Exception:
        return False
    return os.path.normcase(common) == os.path.normcase(normalized_source)


def _split_copy_name(path_value: str) -> tuple[str, str]:
    name = os.path.basename(path_value.rstrip("\\/"))
    if os.path.isdir(path_value) and not os.path.islink(path_value):
        return name, ""
    stem, suffix = os.path.splitext(name)
    return stem or name, suffix


def _next_copy_destination(path_value: str) -> str:
    normalized = _normalize_requested_path(path_value)
    if not _path_exists(normalized):
        return normalized
    parent_dir = os.path.dirname(normalized.rstrip("\\/")) or _parent_path(normalized)
    stem, suffix = _split_copy_name(normalized)
    candidate = os.path.join(parent_dir, f"{stem} - Copy{suffix}")
    counter = 2
    while _path_exists(candidate):
        candidate = os.path.join(parent_dir, f"{stem} - Copy ({counter}){suffix}")
        counter += 1
    return candidate


def _copy_item(source_path: str, destination_path: str) -> None:
    if os.path.islink(source_path):
        try:
            target = os.readlink(source_path)
            os.symlink(target, destination_path, target_is_directory=os.path.isdir(source_path))
            return
        except (AttributeError, NotImplementedError, OSError):
            pass
    if os.path.isdir(source_path) and not os.path.islink(source_path):
        shutil.copytree(source_path, destination_path, symlinks=True)
        return
    shutil.copy2(source_path, destination_path, follow_symlinks=False)


def _ensure_no_conflict(path_value: str) -> None:
    if _path_exists(path_value):
        raise FileManagementError("conflict", f"'{path_value}' already exists.")


def _normalize_selection_rows(value: Any) -> list[Dict[str, Any]]:
    rows: list[Dict[str, Any]] = []
    candidates = value if isinstance(value, list) else [value]
    for item in candidates:
        if isinstance(item, dict):
            path_value = _clean_text(item.get("path"))
            if not path_value:
                continue
            rows.append(
                {
                    "path": _normalize_requested_path(path_value),
                    "name": _clean_text(item.get("name")),
                    "kind": _clean_text(item.get("kind")).lower(),
                }
            )
            continue
        path_value = _clean_text(item)
        if not path_value:
            continue
        normalized = _normalize_requested_path(path_value)
        rows.append({"path": normalized, "name": os.path.basename(normalized.rstrip("\\/")), "kind": ""})
    deduped: dict[str, Dict[str, Any]] = {}
    for row in rows:
        deduped[row["path"]] = row
    return list(deduped.values())


def _normalize_upload_candidate_rows(value: Any) -> list[Dict[str, Any]]:
    rows: list[Dict[str, Any]] = []
    candidates = value if isinstance(value, list) else [value]
    for item in candidates:
        if not isinstance(item, dict):
            continue
        name = _validate_child_name(item.get("name") or item.get("filename"))
        relative_path = _normalize_relative_upload_path(item.get("relative_path"), fallback_name=name)
        client_key = _clean_text(item.get("client_key")) or relative_path
        rows.append(
            {
                "client_key": client_key,
                "name": name,
                "relative_path": relative_path,
                "size_bytes": int(item.get("size_bytes") or 0),
                "modified_at": int(item.get("modified_at") or 0),
            }
        )
    deduped: dict[str, Dict[str, Any]] = {}
    for row in rows:
        deduped[row["client_key"]] = row
    return list(deduped.values())


def _inspect_upload_conflicts(path_value: Any, items: Any) -> Dict[str, Any]:
    target_path = _ensure_directory(path_value)
    candidates = _normalize_upload_candidate_rows(items)
    conflicts: list[Dict[str, Any]] = []
    for row in candidates:
        destination_path = os.path.join(target_path, *row["relative_path"].split("/"))
        if not _path_exists(destination_path):
            continue
        destination_parent = _parent_path(destination_path)
        destination_entry = _entry_from_path(destination_path, parent_path=destination_parent)
        conflicts.append(
            {
                "client_key": row["client_key"],
                "name": row["name"],
                "relative_path": row["relative_path"],
                "display_name": row["relative_path"] if row["relative_path"] != row["name"] else row["name"],
                "destination": destination_entry,
                "upload_size_bytes": int(row.get("size_bytes") or 0),
                "upload_modified_at": int(row.get("modified_at") or 0),
                "replace_supported": _clean_text(destination_entry.get("kind")).lower() != "directory",
            }
        )
    return {"ok": True, "target_path": target_path, "conflicts": conflicts}


def _normalize_text_editor_line_ending(value: Any) -> str:
    normalized = _clean_text(value).lower()
    if normalized in {"", "lf", "\\n"}:
        return "lf"
    if normalized in {"crlf", "\\r\\n"}:
        return "crlf"
    if normalized in {"cr", "\\r"}:
        return "cr"
    raise FileManagementError("invalid_request", f"Unsupported line ending '{value}'.")


def _line_ending_sequence(value: Any) -> str:
    normalized = _normalize_text_editor_line_ending(value)
    if normalized == "crlf":
        return "\r\n"
    if normalized == "cr":
        return "\r"
    return "\n"


def _detect_line_ending(text: str) -> str:
    if "\r\n" in text:
        return "crlf"
    if "\r" in text:
        return "cr"
    return "lf"


def _looks_like_binary_bytes(payload: bytes, *, allow_nulls: bool = False) -> bool:
    if not payload:
        return False
    sample = payload[:4096]
    if not allow_nulls and b"\x00" in sample:
        return True
    control_bytes = 0
    for value in sample:
        if value in {9, 10, 13}:
            continue
        if value < 32 or value == 127:
            control_bytes += 1
    return control_bytes > max(1, int(len(sample) * 0.2))


def _looks_like_binary_text(text: str) -> bool:
    if not text:
        return False
    sample = text[:4096]
    control_chars = 0
    for character in sample:
        if character in {"\t", "\n", "\r"}:
            continue
        if ord(character) < 32 or ord(character) == 127:
            control_chars += 1
    return control_chars > max(1, int(len(sample) * 0.2))


def _decode_text_payload(payload: bytes) -> tuple[str, str]:
    if not payload:
        return "", "utf-8"
    preferred_encoding = _clean_text(locale.getpreferredencoding(False)).lower()
    candidates: list[str] = []
    if payload.startswith(codecs.BOM_UTF8):
        candidates.append("utf-8-sig")
    elif payload.startswith((codecs.BOM_UTF16_LE, codecs.BOM_UTF16_BE)):
        candidates.append("utf-16")
    candidates.append("utf-8")
    if preferred_encoding and preferred_encoding not in {candidate.lower() for candidate in candidates}:
        candidates.append(preferred_encoding)
    seen: set[str] = set()
    for encoding in candidates:
        normalized = _clean_text(encoding).lower()
        if not normalized or normalized in seen:
            continue
        seen.add(normalized)
        try:
            return payload.decode(encoding), encoding
        except UnicodeDecodeError:
            continue
        except LookupError as exc:
            raise FileManagementError("text_encoding_not_supported", str(exc)) from exc
    raise FileManagementError("text_encoding_not_supported", "This text file uses an unsupported encoding.")


def _read_text_file(path_value: Any) -> Dict[str, Any]:
    source_path = _normalize_requested_path(path_value)
    if not _path_exists(source_path):
        raise FileManagementError("path_not_found", f"'{source_path}' does not exist.")
    if os.path.isdir(source_path):
        raise FileManagementError("not_a_file", f"'{source_path}' is not a file.")
    try:
        with open(source_path, "rb") as handle:
            payload = handle.read(TEXT_EDITOR_MAX_BYTES + 1)
    except PermissionError as exc:
        raise FileManagementError("permission_denied", f"Permission denied for '{source_path}'.") from exc
    except OSError as exc:
        raise FileManagementError("invalid_path", str(exc)) from exc
    if len(payload) > TEXT_EDITOR_MAX_BYTES:
        raise FileManagementError(
            "file_too_large",
            f"'{source_path}' exceeds the lightweight editor limit of {TEXT_EDITOR_MAX_BYTES} bytes.",
        )
    allow_nulls = payload.startswith((codecs.BOM_UTF16_LE, codecs.BOM_UTF16_BE))
    if _looks_like_binary_bytes(payload, allow_nulls=allow_nulls):
        raise FileManagementError("binary_not_supported", "Binary files cannot be opened in the lightweight text editor.")
    content, encoding = _decode_text_payload(payload)
    if _looks_like_binary_text(content):
        raise FileManagementError("binary_not_supported", "Binary files cannot be opened in the lightweight text editor.")
    entry = _entry_from_path(source_path, parent_path=_parent_path(source_path))
    return {
        "path": source_path,
        "content": content,
        "encoding": encoding,
        "line_ending": _detect_line_ending(content),
        "size_bytes": int(len(payload)),
        "modified_at": int(entry.get("modified_at") or 0),
        "entry": entry,
    }


def _write_text_file(path_value: Any, content: Any, *, encoding: Any, line_ending: Any) -> Dict[str, Any]:
    destination_path = _normalize_requested_path(path_value)
    if not _path_exists(destination_path):
        raise FileManagementError("path_not_found", f"'{destination_path}' does not exist.")
    if os.path.isdir(destination_path):
        raise FileManagementError("not_a_file", f"'{destination_path}' is not a file.")
    requested_encoding = _clean_text(encoding) or "utf-8"
    try:
        codecs.lookup(requested_encoding)
    except LookupError as exc:
        raise FileManagementError("text_encoding_not_supported", str(exc)) from exc
    raw_content = "" if content is None else str(content)
    normalized_content = raw_content.replace("\r\n", "\n").replace("\r", "\n")
    encoded_content = normalized_content.replace("\n", _line_ending_sequence(line_ending)).encode(requested_encoding)
    if len(encoded_content) > TEXT_EDITOR_MAX_BYTES:
        raise FileManagementError(
            "file_too_large",
            f"'{destination_path}' exceeds the lightweight editor limit of {TEXT_EDITOR_MAX_BYTES} bytes.",
        )
    try:
        with open(destination_path, "r+b") as handle:
            handle.seek(0)
            handle.write(encoded_content)
            handle.truncate()
    except PermissionError as exc:
        raise FileManagementError("permission_denied", f"Permission denied for '{destination_path}'.") from exc
    except OSError as exc:
        raise FileManagementError("invalid_path", str(exc)) from exc
    entry = _entry_from_path(destination_path, parent_path=_parent_path(destination_path))
    return {
        "path": destination_path,
        "encoding": requested_encoding,
        "line_ending": _normalize_text_editor_line_ending(line_ending),
        "size_bytes": int(len(encoded_content)),
        "modified_at": int(entry.get("modified_at") or 0),
        "entry": entry,
    }


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.role_health_label = "File Management"
        hooks = getattr(ctx, "hooks", {}) or {}
        self._log_hook = hooks.get("log_agent")
        self._http_client_factory = hooks.get("http_client")
        self._listener_registered = False
        self._lane_coordinator = LaneCoordinator(log=lambda message: self._log(message))
        self._background_tasks: set[asyncio.Task[Any]] = set()
        self._last_error = ""
        self._last_transfer_at = 0
        self._active_transfers = 0
        self._temp_root = Path(tempfile.gettempdir()) / "Borealis" / "file_management"
        self._temp_root.mkdir(parents=True, exist_ok=True)

    def _find_7zip_executable(self) -> str:
        candidates: list[str] = []
        env_candidate = _clean_text(os.environ.get("BOREALIS_7ZIP_EXE"))
        if env_candidate:
            candidates.append(env_candidate)
        if IS_WINDOWS:
            repo_root = Path(__file__).resolve().parents[3]
            for base_dir in (repo_root, Path.cwd()):
                candidates.append(str(base_dir / "Dependencies" / "7zip" / "7z.exe"))
            for env_name in ("ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"):
                install_root = _clean_text(os.environ.get(env_name))
                if install_root:
                    candidates.append(str(Path(install_root) / "7-Zip" / "7z.exe"))
        for executable_name in ("7zz", "7z", "7za"):
            resolved = shutil.which(executable_name)
            if resolved:
                candidates.append(resolved)
        seen: set[str] = set()
        for candidate in candidates:
            normalized = _clean_text(candidate)
            if not normalized or normalized.lower() in seen:
                continue
            seen.add(normalized.lower())
            if Path(normalized).is_file():
                return normalized
        return ""

    def _can_use_7zip_archive(self, selections: list[Dict[str, Any]]) -> bool:
        if not selections:
            return False
        common_parent = ""
        for selection in selections:
            source_path = _normalize_requested_path(selection.get("path"))
            label = _normalize_upload_name(selection.get("name")) or os.path.basename(source_path.rstrip("\\/")) or source_path
            source_name = os.path.basename(source_path.rstrip("\\/"))
            if not source_name or label != source_name:
                return False
            parent_path = _parent_path(source_path)
            if not parent_path:
                return False
            if not common_parent:
                common_parent = parent_path
                continue
            if os.path.normcase(common_parent) != os.path.normcase(parent_path):
                return False
        return bool(common_parent)

    def _build_7zip_selection(
        self,
        selections: list[Dict[str, Any]],
        archive_path: str,
        seven_zip_exe: str,
        *,
        transfer_id: str = "",
    ) -> int:
        if not self._can_use_7zip_archive(selections):
            raise FileManagementError("archive_fallback_required", "7-Zip archiving requires selections from the same parent directory.")
        first_path = _normalize_requested_path(selections[0].get("path"))
        working_directory = _parent_path(first_path)
        input_names = [os.path.basename(_normalize_requested_path(selection.get("path")).rstrip("\\/")) for selection in selections]
        command = [seven_zip_exe, "a", "-t7z", "-mx=5", "-y", archive_path, *input_names]
        try:
            process = subprocess.Popen(
                command,
                cwd=working_directory,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            last_control_check = 0.0
            while process.poll() is None:
                time.sleep(0.2)
                now_ts = time.time()
                if transfer_id and now_ts - last_control_check >= TRANSFER_CONTROL_POLL_SECONDS:
                    self._ensure_transfer_not_canceled(transfer_id)
                    last_control_check = now_ts
            stdout, stderr = process.communicate()
            completed = type("Completed", (), {"returncode": process.returncode, "stdout": stdout, "stderr": stderr})()
        except OSError as exc:
            raise FileManagementError("archive_failed", str(exc)) from exc
        if completed.returncode != 0:
            details = _clean_text(completed.stderr) or _clean_text(completed.stdout) or "7-Zip failed to create the archive."
            raise FileManagementError("archive_failed", details)
        try:
            return int(os.path.getsize(archive_path))
        except Exception:
            return 0

    def _log(self, message: str, *, error: bool = False) -> None:
        if callable(self._log_hook):
            try:
                self._log_hook(message, fname="file_management.log")
                if error:
                    self._log_hook(message, fname="agent.error.log")
            except Exception:
                pass

    def _http_client(self) -> Any:
        if not callable(self._http_client_factory):
            raise FileManagementError("client_unavailable", "The Borealis HTTP client is unavailable.")
        try:
            client = self._http_client_factory()
        except Exception as exc:
            raise FileManagementError("client_unavailable", str(exc)) from exc
        if client is None:
            raise FileManagementError("client_unavailable", "The Borealis HTTP client is unavailable.")
        return client

    def _matches_target(self, payload: Dict[str, Any]) -> bool:
        target_hostname = _clean_text(payload.get("hostname") or payload.get("target_hostname")).lower()
        if target_hostname and target_hostname != _hostname().lower():
            return False
        target_agent = _clean_text(payload.get("agent_id"))
        if target_agent and target_agent != _clean_text(self.ctx.agent_id):
            return False
        return True

    def _transfer_control_snapshot(self, transfer_id: str) -> Dict[str, Any]:
        client = self._http_client()
        client.ensure_authenticated()
        client.refresh_base_url()
        headers = client.auth_headers()
        url = f"{client.base_url}/api/agent/files/transfers/{transfer_id}/status"
        response = client.session.get(url, headers=headers, timeout=30)
        response.raise_for_status()
        payload = response.json() if callable(getattr(response, "json", None)) else {}
        return payload if isinstance(payload, dict) else {}

    def _ensure_transfer_not_canceled(self, transfer_id: str) -> None:
        try:
            snapshot = self._transfer_control_snapshot(transfer_id)
        except FileManagementError as exc:
            if _clean_text(getattr(exc, "code", "")).lower() == "client_unavailable":
                return
            raise
        if bool(snapshot.get("cancel_requested")) or _clean_text(snapshot.get("status")).lower() in {"canceling", "canceled"}:
            raise FileManagementError("transfer_canceled", "Transfer canceled by operator.")

    def _report_progress(
        self,
        transfer_id: str,
        *,
        status: str = "",
        bytes_complete: Optional[int] = None,
        bytes_total: Optional[int] = None,
        error: str = "",
        archive_name: str = "",
    ) -> Dict[str, Any]:
        client = self._http_client()
        payload: Dict[str, Any] = {}
        if status:
            payload["status"] = status
        if bytes_complete is not None:
            payload["bytes_complete"] = int(bytes_complete)
        if bytes_total is not None:
            payload["bytes_total"] = int(bytes_total)
        if error:
            payload["error"] = error
        if archive_name:
            payload["archive_name"] = archive_name
        response = client.post_json(f"/api/agent/files/transfers/{transfer_id}/progress", payload, require_auth=True)
        return response if isinstance(response, dict) else {}

    def _paste_items(self, *, operation: str, selections: list[Dict[str, Any]], destination_path: str) -> list[Dict[str, Any]]:
        normalized_operation = _clean_text(operation).lower()
        if normalized_operation not in {"copy", "cut"}:
            raise FileManagementError("invalid_request", f"Unsupported paste operation '{operation}'.")
        if not selections:
            raise FileManagementError("invalid_request", "At least one source path is required.")
        destination_dir = _ensure_directory(destination_path)
        plan: list[tuple[str, str]] = []
        for row in selections:
            source_path = _normalize_requested_path(row.get("path"))
            if not _path_exists(source_path):
                raise FileManagementError("path_not_found", f"'{source_path}' does not exist.")
            final_path = os.path.join(destination_dir, os.path.basename(source_path.rstrip("\\/")))
            if _destination_inside_source(source_path, destination_dir):
                raise FileManagementError("invalid_path", "A folder cannot be pasted into itself.")
            if normalized_operation == "cut":
                if _path_equal(source_path, final_path):
                    continue
                _ensure_no_conflict(final_path)
            else:
                if _path_equal(source_path, final_path) or _path_exists(final_path):
                    final_path = _next_copy_destination(final_path)
            plan.append((source_path, final_path))
        pasted: list[Dict[str, Any]] = []
        for source_path, final_path in plan:
            if normalized_operation == "cut":
                shutil.move(source_path, final_path)
            else:
                _copy_item(source_path, final_path)
            pasted.append(_entry_from_path(final_path, parent_path=destination_dir))
        return pasted

    def _stream_upload_item(
        self,
        *,
        transfer_id: str,
        item_id: str,
        destination_path: str,
        progress_state: Dict[str, Any],
        overwrite_existing: bool = False,
    ) -> None:
        client = self._http_client()
        client.ensure_authenticated()
        client.refresh_base_url()
        headers = client.auth_headers()
        item_url = f"{client.base_url}/api/agent/files/transfers/{transfer_id}/upload-item/{item_id}"
        parent_dir = os.path.dirname(destination_path)
        if parent_dir:
            os.makedirs(parent_dir, exist_ok=True)
        temp_file = None
        last_control_check = 0.0
        try:
            if _path_exists(destination_path) and not overwrite_existing:
                raise FileManagementError("conflict", f"'{destination_path}' already exists.")
            if overwrite_existing and _path_exists(destination_path) and os.path.isdir(destination_path) and not os.path.islink(destination_path):
                raise FileManagementError("conflict", f"'{destination_path}' already exists as a directory.")
            with client.session.get(item_url, headers=headers, timeout=300, stream=True) as response:
                if response.status_code == 409:
                    raise FileManagementError("transfer_canceled", "Transfer canceled by operator.")
                response.raise_for_status()
                fd, temp_file = tempfile.mkstemp(prefix=".borealis-upload-", suffix=".tmp", dir=parent_dir or None)
                with os.fdopen(fd, "wb") as handle:
                    for chunk in response.iter_content(chunk_size=64 * 1024):
                        if not chunk:
                            continue
                        handle.write(chunk)
                        progress_state["bytes_complete"] = int(progress_state.get("bytes_complete") or 0) + len(chunk)
                        now_ts = time.time()
                        if now_ts - float(progress_state.get("last_report_at") or 0.0) >= STATUS_PROGRESS_INTERVAL_SECONDS:
                            snapshot = self._report_progress(
                                transfer_id,
                                status="running",
                                bytes_complete=progress_state["bytes_complete"],
                                bytes_total=progress_state.get("bytes_total"),
                            ) or {}
                            progress_state["last_report_at"] = now_ts
                            if bool(snapshot.get("cancel_requested")):
                                raise FileManagementError("transfer_canceled", "Transfer canceled by operator.")
                        elif now_ts - last_control_check >= TRANSFER_CONTROL_POLL_SECONDS:
                            self._ensure_transfer_not_canceled(transfer_id)
                            last_control_check = now_ts
                os.replace(temp_file, destination_path)
                temp_file = None
        except PermissionError as exc:
            raise FileManagementError("permission_denied", f"Permission denied for '{destination_path}'.") from exc
        except FileManagementError:
            raise
        except Exception as exc:
            raise FileManagementError("transfer_failed", str(exc)) from exc
        finally:
            if temp_file:
                with contextlib.suppress(Exception):
                    os.remove(temp_file)

    def _upload_transfer_worker(self, payload: Dict[str, Any]) -> None:
        transfer_id = _clean_text(payload.get("transfer_id"))
        target_path = _ensure_directory(payload.get("target_path"))
        items = payload.get("items") if isinstance(payload.get("items"), list) else []
        if not transfer_id or not items:
            raise FileManagementError("invalid_request", "Upload manifest is missing transfer metadata.")
        bytes_total = sum(int((row or {}).get("size_bytes") or 0) for row in items if isinstance(row, dict))
        progress_state = {
            "bytes_complete": 0,
            "bytes_total": bytes_total,
            "last_report_at": 0.0,
        }
        snapshot = self._report_progress(transfer_id, status="running", bytes_complete=0, bytes_total=bytes_total) or {}
        if bool(snapshot.get("cancel_requested")):
            raise FileManagementError("transfer_canceled", "Transfer canceled by operator.")
        for row in items:
            if not isinstance(row, dict):
                continue
            self._ensure_transfer_not_canceled(transfer_id)
            item_id = _clean_text(row.get("item_id"))
            relative_path = _normalize_relative_upload_path(row.get("relative_path"), fallback_name=row.get("name"))
            destination_path = os.path.join(target_path, *relative_path.split("/"))
            self._stream_upload_item(
                transfer_id=transfer_id,
                item_id=item_id,
                destination_path=destination_path,
                progress_state=progress_state,
                overwrite_existing=bool(row.get("overwrite_existing")),
            )
        self._report_progress(
            transfer_id,
            status="completed",
            bytes_complete=progress_state["bytes_complete"],
            bytes_total=progress_state["bytes_total"],
        )
        self._last_error = ""

    def _zip_selection(self, selections: list[Dict[str, Any]], archive_path: str, *, transfer_id: str = "") -> int:
        total_bytes = 0
        with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for selection in selections:
                if transfer_id:
                    self._ensure_transfer_not_canceled(transfer_id)
                source_path = _normalize_requested_path(selection.get("path"))
                label = _normalize_upload_name(selection.get("name")) or os.path.basename(source_path.rstrip("\\/")) or source_path
                if os.path.isdir(source_path) and not os.path.islink(source_path):
                    emitted = False
                    for root, dirs, files in os.walk(source_path, topdown=True, followlinks=False):
                        if transfer_id:
                            self._ensure_transfer_not_canceled(transfer_id)
                        dirs[:] = [name for name in dirs if not os.path.islink(os.path.join(root, name))]
                        relative_root = os.path.relpath(root, source_path)
                        if relative_root == ".":
                            relative_root = ""
                        arc_root = "/".join(part for part in [label, relative_root.replace("\\", "/")] if part)
                        if not dirs and not files:
                            archive.writestr(f"{arc_root}/", b"")
                            emitted = True
                        for file_name in files:
                            file_path = os.path.join(root, file_name)
                            if os.path.isdir(file_path):
                                continue
                            relative_name = os.path.relpath(file_path, source_path).replace("\\", "/")
                            archive_name = "/".join(part for part in [label, relative_name] if part)
                            archive.write(file_path, archive_name)
                            emitted = True
                    if not emitted:
                        archive.writestr(f"{label}/", b"")
                    continue
                archive_name = label
                archive.write(source_path, archive_name)
        try:
            total_bytes = int(os.path.getsize(archive_path))
        except Exception:
            total_bytes = 0
        return total_bytes

    def _post_download_artifact(self, *, transfer_id: str, artifact_path: str, artifact_name: str, mime_type: str) -> None:
        client = self._http_client()
        client.ensure_authenticated()
        client.refresh_base_url()
        url = f"{client.base_url}/api/agent/files/transfers/{transfer_id}/content"
        headers = client.auth_headers()
        with open(artifact_path, "rb") as handle:
            response = client.session.post(
                url,
                headers=headers,
                files={"artifact": (artifact_name, handle, mime_type or "application/octet-stream")},
                data={"archive_name": artifact_name, "mime_type": mime_type or "application/octet-stream"},
                timeout=900,
            )
        if response.status_code == 409:
            raise FileManagementError("transfer_canceled", "Transfer canceled by operator.")
        response.raise_for_status()

    def _download_transfer_worker(self, payload: Dict[str, Any]) -> None:
        transfer_id = _clean_text(payload.get("transfer_id"))
        selections = _normalize_selection_rows(payload.get("items"))
        if not transfer_id or not selections:
            raise FileManagementError("invalid_request", "Download manifest is missing transfer metadata.")
        archive_required = bool(payload.get("archive_required"))
        requested_archive_name = _normalize_upload_name(payload.get("archive_name")) or "download.7z"
        self._ensure_transfer_not_canceled(transfer_id)

        if not archive_required and len(selections) == 1 and os.path.isfile(selections[0]["path"]):
            source_path = selections[0]["path"]
            artifact_name = _normalize_upload_name(selections[0].get("name")) or os.path.basename(source_path)
            mime_type = mimetypes.guess_type(artifact_name)[0] or "application/octet-stream"
            total_bytes = int(os.path.getsize(source_path) or 0)
            snapshot = self._report_progress(transfer_id, status="running", bytes_complete=0, bytes_total=total_bytes) or {}
            if bool(snapshot.get("cancel_requested")):
                raise FileManagementError("transfer_canceled", "Transfer canceled by operator.")
            self._ensure_transfer_not_canceled(transfer_id)
            self._post_download_artifact(
                transfer_id=transfer_id,
                artifact_path=source_path,
                artifact_name=artifact_name,
                mime_type=mime_type,
            )
            self._last_error = ""
            return

        self._temp_root.mkdir(parents=True, exist_ok=True)
        seven_zip_exe = self._find_7zip_executable()
        use_7zip = bool(seven_zip_exe and self._can_use_7zip_archive(selections))
        archive_suffix = ".7z" if use_7zip else ".zip"
        archive_name = _normalize_archive_name(requested_archive_name, archive_suffix)
        mime_type = "application/x-7z-compressed" if use_7zip else "application/zip"
        fd, archive_path = tempfile.mkstemp(prefix="download-", suffix=archive_suffix, dir=self._temp_root)
        os.close(fd)
        try:
            try:
                if use_7zip:
                    total_bytes = self._build_7zip_selection(selections, archive_path, seven_zip_exe, transfer_id=transfer_id)
                else:
                    total_bytes = self._zip_selection(selections, archive_path, transfer_id=transfer_id)
            except FileManagementError as exc:
                if _clean_text(getattr(exc, "code", "")).lower() == "transfer_canceled":
                    raise
                if not use_7zip:
                    raise
                self._log(f"7-Zip archive creation failed; falling back to ZIP for {transfer_id}: {exc}")
                with contextlib.suppress(Exception):
                    os.remove(archive_path)
                archive_name = _normalize_archive_name(requested_archive_name, ".zip")
                mime_type = "application/zip"
                fd, archive_path = tempfile.mkstemp(prefix="download-", suffix=".zip", dir=self._temp_root)
                os.close(fd)
                total_bytes = self._zip_selection(selections, archive_path, transfer_id=transfer_id)
            snapshot = self._report_progress(
                transfer_id,
                status="running",
                bytes_complete=0,
                bytes_total=total_bytes,
                archive_name=archive_name,
            ) or {}
            if bool(snapshot.get("cancel_requested")):
                raise FileManagementError("transfer_canceled", "Transfer canceled by operator.")
            self._ensure_transfer_not_canceled(transfer_id)
            self._post_download_artifact(
                transfer_id=transfer_id,
                artifact_path=archive_path,
                artifact_name=archive_name,
                mime_type=mime_type,
            )
            self._last_error = ""
        finally:
            with contextlib.suppress(Exception):
                os.remove(archive_path)

    def _track_task(self, task: asyncio.Task[Any]) -> None:
        self._background_tasks.add(task)

        def _cleanup(done: asyncio.Task[Any]) -> None:
            self._background_tasks.discard(done)
            if done.cancelled():
                return
            try:
                done.result()
            except Exception as exc:
                self._last_error = str(exc)
                self._log(f"file_management background task failed: {exc}", error=True)

        task.add_done_callback(_cleanup)

    async def _run_transfer_background(self, payload: Dict[str, Any]) -> None:
        transfer_id = _clean_text(payload.get("transfer_id")) or "unknown"
        action = _clean_text(payload.get("action")).lower()
        self._active_transfers += 1
        self._last_transfer_at = int(time.time())
        try:
            if action == "upload_start":
                await self._lane_coordinator.run(
                    lane="file_management",
                    job_id=f"upload:{transfer_id}",
                    work=lambda: asyncio.to_thread(self._upload_transfer_worker, dict(payload)),
                )
            elif action == "download_start":
                await self._lane_coordinator.run(
                    lane="file_management",
                    job_id=f"download:{transfer_id}",
                    work=lambda: asyncio.to_thread(self._download_transfer_worker, dict(payload)),
                )
            else:
                raise FileManagementError("invalid_request", f"Unsupported transfer action '{action}'.")
        except FileManagementError as exc:
            if _clean_text(getattr(exc, "code", "")).lower() == "transfer_canceled":
                self._last_error = str(exc)
                try:
                    self._report_progress(transfer_id, status="canceled", error=str(exc))
                except Exception:
                    self._log(f"file_management failed to report transfer cancellation transfer_id={transfer_id}", error=True)
            else:
                self._last_error = str(exc)
                self._log(f"file_management transfer failed transfer_id={transfer_id} error={exc}", error=True)
                try:
                    self._report_progress(transfer_id, status="failed", error=str(exc))
                except Exception:
                    self._log(f"file_management failed to report transfer failure transfer_id={transfer_id}", error=True)
        except Exception as exc:
            self._last_error = str(exc)
            self._log(f"file_management transfer failed transfer_id={transfer_id} error={exc}", error=True)
            try:
                self._report_progress(transfer_id, status="failed", error=str(exc))
            except Exception:
                self._log(f"file_management failed to report transfer failure transfer_id={transfer_id}", error=True)
        finally:
            self._active_transfers = max(0, int(self._active_transfers) - 1)
            self._last_transfer_at = int(time.time())

    def health_report(self) -> Dict[str, Any]:
        lane_details = self._lane_coordinator.snapshot() if self._lane_coordinator is not None else {}
        if not self._listener_registered:
            return {
                "status": "unhealthy",
                "role_label": self.role_health_label,
                "detail": "File-management listener is not registered.",
                "details": {
                    "running_status": "Stopped",
                    "listener_state": "Not Registered",
                },
            }
        details = {
            "running_status": "Ready",
            "listener_state": "Registered",
            "active_transfers": str(max(0, int(self._active_transfers))),
            "last_transfer_at": str(int(self._last_transfer_at or 0)),
            **lane_details,
        }
        if self._last_error:
            details["last_error"] = self._last_error
            return {
                "status": "recovering",
                "role_label": self.role_health_label,
                "detail": self._last_error,
                "details": details,
            }
        return {
            "status": "healthy",
            "role_label": self.role_health_label,
            "detail": "Remote file-management listeners are ready.",
            "details": details,
        }

    def register_events(self) -> None:
        sio = self.ctx.sio
        self._listener_registered = True

        @sio.on("file_management_request")
        async def _on_file_management_request(payload):
            if not isinstance(payload, dict):
                return {"ok": False, "error": "invalid_request", "message": "Expected an object payload."}
            if not self._matches_target(payload):
                return {"ok": False, "error": "not_found", "message": "The file-management request targeted another device."}
            action = _clean_text(payload.get("action")).lower()
            try:
                if action == "roots":
                    self._last_error = ""
                    return _roots_payload()
                if action == "children":
                    current_path = _normalize_requested_path(payload.get("path"))
                    self._last_error = ""
                    return {"ok": True, "current_path": current_path, "entries": _list_children(current_path)}
                if action == "upload_conflicts":
                    target_path = _ensure_directory(payload.get("target_path"))
                    conflict_payload = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id=f"upload_conflicts:{target_path}",
                        work=lambda: asyncio.to_thread(_inspect_upload_conflicts, target_path, payload.get("items")),
                    )
                    self._last_error = ""
                    return conflict_payload
                if action == "read_text":
                    source_path = _normalize_requested_path(payload.get("path"))

                    text_payload = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id=f"read_text:{source_path}",
                        work=lambda: asyncio.to_thread(_read_text_file, source_path),
                    )
                    self._last_error = ""
                    return {"ok": True, **text_payload}
                if action == "write_text":
                    source_path = _normalize_requested_path(payload.get("path"))

                    text_payload = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id=f"write_text:{source_path}",
                        work=lambda: asyncio.to_thread(
                            _write_text_file,
                            source_path,
                            payload.get("content"),
                            encoding=payload.get("encoding"),
                            line_ending=payload.get("line_ending"),
                        ),
                    )
                    self._last_error = ""
                    return {"ok": True, **text_payload}
                if action == "mkdir":
                    parent_path = _ensure_directory(payload.get("path"))
                    name = _validate_child_name(payload.get("name"))

                    def _mkdir():
                        destination = os.path.join(parent_path, name)
                        _ensure_no_conflict(destination)
                        os.mkdir(destination)
                        return _entry_from_path(destination, parent_path=parent_path)

                    entry = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id=f"mkdir:{parent_path}:{name}",
                        work=lambda: asyncio.to_thread(_mkdir),
                    )
                    self._last_error = ""
                    return {"ok": True, "entry": entry}
                if action == "rename":
                    source_path = _normalize_requested_path(payload.get("path"))
                    if not _path_exists(source_path):
                        raise FileManagementError("path_not_found", f"'{source_path}' does not exist.")
                    new_name = _validate_child_name(payload.get("new_name"))

                    def _rename():
                        parent_path = _parent_path(source_path)
                        if not parent_path or _clean_text(parent_path).lower() == _clean_text(source_path).lower():
                            raise FileManagementError("invalid_path", "Root paths cannot be renamed.")
                        destination = os.path.join(parent_path, new_name)
                        _ensure_no_conflict(destination)
                        os.rename(source_path, destination)
                        return _entry_from_path(destination, parent_path=parent_path)

                    entry = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id=f"rename:{source_path}",
                        work=lambda: asyncio.to_thread(_rename),
                    )
                    self._last_error = ""
                    return {"ok": True, "entry": entry}
                if action == "move":
                    selections = _normalize_selection_rows(payload.get("paths"))
                    destination_path = _ensure_directory(payload.get("destination_path"))
                    if not selections:
                        raise FileManagementError("invalid_request", "At least one source path is required.")

                    def _move():
                        moved = []
                        for row in selections:
                            source_path = row["path"]
                            if not _path_exists(source_path):
                                raise FileManagementError("path_not_found", f"'{source_path}' does not exist.")
                            final_path = os.path.join(destination_path, os.path.basename(source_path.rstrip("\\/")))
                            if source_path == final_path:
                                continue
                            if os.path.isdir(source_path) and not os.path.islink(source_path):
                                try:
                                    common = os.path.commonpath([source_path, destination_path])
                                except Exception:
                                    common = ""
                                if common and _clean_text(common).lower() == _clean_text(source_path).lower():
                                    raise FileManagementError("invalid_path", "A folder cannot be moved into itself.")
                            _ensure_no_conflict(final_path)
                        for row in selections:
                            source_path = row["path"]
                            final_path = os.path.join(destination_path, os.path.basename(source_path.rstrip("\\/")))
                            if source_path == final_path:
                                continue
                            shutil.move(source_path, final_path)
                            moved.append(_entry_from_path(final_path, parent_path=destination_path))
                        return moved

                    moved = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id=f"move:{destination_path}",
                        work=lambda: asyncio.to_thread(_move),
                    )
                    self._last_error = ""
                    return {"ok": True, "moved": moved}
                if action == "paste":
                    selections = _normalize_selection_rows(payload.get("paths"))
                    destination_path = _ensure_directory(payload.get("destination_path"))
                    operation = _clean_text(payload.get("operation")).lower()
                    pasted = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id=f"paste:{destination_path}:{operation or 'copy'}",
                        work=lambda: asyncio.to_thread(
                            self._paste_items,
                            operation=operation,
                            selections=selections,
                            destination_path=destination_path,
                        ),
                    )
                    self._last_error = ""
                    return {"ok": True, "pasted": pasted}
                if action == "delete":
                    selections = _normalize_selection_rows(payload.get("paths"))
                    if not selections:
                        raise FileManagementError("invalid_request", "At least one path is required.")

                    def _delete():
                        deleted = []
                        for row in selections:
                            source_path = row["path"]
                            if not _path_exists(source_path):
                                raise FileManagementError("path_not_found", f"'{source_path}' does not exist.")
                        for row in selections:
                            source_path = row["path"]
                            deleted.append(source_path)
                            if os.path.isdir(source_path) and not os.path.islink(source_path):
                                shutil.rmtree(source_path)
                            else:
                                os.remove(source_path)
                        return deleted

                    deleted = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id="delete",
                        work=lambda: asyncio.to_thread(_delete),
                    )
                    self._last_error = ""
                    return {"ok": True, "deleted": deleted}
                if action in {"upload_start", "download_start"}:
                    transfer_id = _clean_text(payload.get("transfer_id"))
                    if not transfer_id:
                        raise FileManagementError("invalid_request", "Transfer metadata is missing transfer_id.")
                    self._track_task(asyncio.create_task(self._run_transfer_background(dict(payload))))
                    self._last_error = ""
                    return {"ok": True, "status": "accepted", "transfer_id": transfer_id}
                raise FileManagementError("invalid_request", f"Unsupported file-management action '{action}'.")
            except FileManagementError as exc:
                self._last_error = exc.message
                self._log(f"file_management request failed action={action} error={exc.message}", error=True)
                return {"ok": False, "error": exc.code, "message": exc.message}
            except Exception as exc:
                self._last_error = str(exc)
                self._log(f"file_management request crashed action={action} error={exc}", error=True)
                return {"ok": False, "error": "internal_error", "message": str(exc)}

    def stop_all(self) -> None:
        for task in list(self._background_tasks):
            task.cancel()
