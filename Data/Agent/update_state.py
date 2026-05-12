from __future__ import annotations

import argparse
import atexit
import json
import os
import re
import threading
import time
import uuid
from pathlib import Path
from typing import Any, Dict, List, Optional


_BUILD_ID_PATTERN = re.compile(r"^[0-9a-fA-F]{7,64}$")
_DEFAULT_BUSY_TTL_SECONDS = 120
_DEFAULT_BUSY_REFRESH_SECONDS = 15


def _ensure_dir(path: Path) -> Path:
    path.mkdir(parents=True, exist_ok=True)
    return path


def _normalize_build_id(value: Any) -> str:
    text = str(value or "").strip()
    if not text:
        return ""
    if _BUILD_ID_PATTERN.match(text):
        return text.lower()
    return ""


def _resolve_project_root() -> Path:
    current = Path(__file__).resolve().parent
    discovered_root: Optional[Path] = None
    for candidate in [current, *current.parents]:
        if (
            (candidate / "Agent.exe").is_file()
            or (candidate / "Agent.sh").is_file()
            or (candidate / "Engine.sh").is_file()
        ):
            discovered_root = candidate
            break

    override = (os.environ.get("BOREALIS_ROOT") or os.environ.get("BOREALIS_PROJECT_ROOT") or "").strip()
    if override:
        candidate = Path(override).expanduser()
        try:
            resolved_override = candidate.resolve()
        except Exception:
            resolved_override = candidate
        try:
            current.relative_to(resolved_override)
            return resolved_override
        except Exception:
            pass
        if discovered_root is None:
            return resolved_override

    return discovered_root or current


def _settings_dir(project_root: Optional[Path] = None) -> Path:
    return _resolve_project_root() / "Agent" / "Borealis" / "Settings" if project_root is None else project_root / "Agent" / "Borealis" / "Settings"


def _updater_root(project_root: Optional[Path] = None) -> Path:
    return _ensure_dir(_settings_dir(project_root) / "Updater")


def _busy_dir(project_root: Optional[Path] = None) -> Path:
    return _ensure_dir(_updater_root(project_root) / "busy")


def installed_build_id_path(project_root: Optional[Path] = None) -> Path:
    return _settings_dir(project_root) / "installed_build_id.txt"


def update_status_path(project_root: Optional[Path] = None) -> Path:
    return _updater_root(project_root) / "update_status.json"


def pending_update_path(project_root: Optional[Path] = None) -> Path:
    return _updater_root(project_root) / "pending_update.json"


def _resolve_git_dir(project_root: Optional[Path] = None) -> Optional[Path]:
    root = _resolve_project_root() if project_root is None else project_root
    git_path = root / ".git"
    if git_path.is_dir():
        return git_path
    if git_path.is_file():
        try:
            content = git_path.read_text(encoding="utf-8", errors="ignore").strip()
        except Exception:
            return None
        if content.lower().startswith("gitdir:"):
            raw_target = content.split(":", 1)[1].strip()
            target = Path(raw_target)
            if not target.is_absolute():
                target = (root / raw_target).resolve()
            return target
    return None


def _read_packed_ref(git_dir: Path, ref_name: str) -> str:
    packed_refs = git_dir / "packed-refs"
    if not packed_refs.is_file():
        return ""
    try:
        for raw_line in packed_refs.read_text(encoding="utf-8", errors="ignore").splitlines():
            line = raw_line.strip()
            if not line or line.startswith("#") or line.startswith("^"):
                continue
            parts = line.split(" ", 1)
            if len(parts) != 2:
                continue
            if parts[1].strip() == ref_name:
                return _normalize_build_id(parts[0])
    except Exception:
        return ""
    return ""


def read_repo_build_id(project_root: Optional[Path] = None) -> str:
    git_dir = _resolve_git_dir(project_root)
    if git_dir is None:
        return ""

    head_path = git_dir / "HEAD"
    if not head_path.is_file():
        return ""
    try:
        head = head_path.read_text(encoding="utf-8", errors="ignore").strip()
    except Exception:
        return ""

    if not head:
        return ""
    if head.startswith("ref:"):
        ref_name = head.split(":", 1)[1].strip()
        if not ref_name:
            return ""
        ref_path = git_dir / Path(ref_name)
        if ref_path.is_file():
            try:
                return _normalize_build_id(ref_path.read_text(encoding="utf-8", errors="ignore"))
            except Exception:
                return ""
        return _read_packed_ref(git_dir, ref_name)
    return _normalize_build_id(head)


def read_installed_build_id(project_root: Optional[Path] = None) -> str:
    path = installed_build_id_path(project_root)
    if not path.is_file():
        return ""
    try:
        return _normalize_build_id(path.read_text(encoding="utf-8", errors="ignore"))
    except Exception:
        return ""


def write_installed_build_id(build_id: Any, project_root: Optional[Path] = None) -> str:
    normalized = _normalize_build_id(build_id)
    if not normalized:
        return ""
    path = installed_build_id_path(project_root)
    _ensure_dir(path.parent)
    try:
        current_value = _normalize_build_id(path.read_text(encoding="utf-8", errors="ignore")) if path.is_file() else ""
    except Exception:
        current_value = ""
    if current_value == normalized:
        return normalized
    path.write_text(normalized, encoding="utf-8")
    return normalized


def sync_installed_build_id(project_root: Optional[Path] = None) -> str:
    repo_build_id = read_repo_build_id(project_root)
    if repo_build_id:
        return write_installed_build_id(repo_build_id, project_root)
    return read_installed_build_id(project_root)


def read_update_status(project_root: Optional[Path] = None) -> Dict[str, Any]:
    path = update_status_path(project_root)
    if not path.is_file():
        return {}
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return {}
    return payload if isinstance(payload, dict) else {}


def write_update_status(payload: Dict[str, Any], project_root: Optional[Path] = None) -> Dict[str, Any]:
    normalized = payload if isinstance(payload, dict) else {}
    _write_json_atomic(update_status_path(project_root), normalized)
    return dict(normalized)


def clear_update_status(project_root: Optional[Path] = None) -> None:
    try:
        update_status_path(project_root).unlink(missing_ok=True)
    except Exception:
        pass


def read_pending_update(project_root: Optional[Path] = None) -> Dict[str, Any]:
    path = pending_update_path(project_root)
    if not path.is_file():
        return {}
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return {}
    return payload if isinstance(payload, dict) else {}


def write_pending_update(payload: Dict[str, Any], project_root: Optional[Path] = None) -> Dict[str, Any]:
    normalized = payload if isinstance(payload, dict) else {}
    _write_json_atomic(pending_update_path(project_root), normalized)
    return dict(normalized)


def clear_pending_update(project_root: Optional[Path] = None) -> None:
    try:
        pending_update_path(project_root).unlink(missing_ok=True)
    except Exception:
        pass


def _write_json_atomic(path: Path, payload: Dict[str, Any]) -> None:
    _ensure_dir(path.parent)
    temp_path = path.with_suffix(path.suffix + ".tmp")
    temp_path.write_text(json.dumps(payload, sort_keys=True), encoding="utf-8")
    temp_path.replace(path)


class BusyLease:
    def __init__(
        self,
        reason: str,
        *,
        project_root: Optional[Path] = None,
        metadata: Optional[Dict[str, Any]] = None,
        ttl_seconds: int = _DEFAULT_BUSY_TTL_SECONDS,
        refresh_interval_seconds: int = _DEFAULT_BUSY_REFRESH_SECONDS,
    ) -> None:
        normalized_reason = re.sub(r"[^A-Za-z0-9_.-]+", "-", str(reason or "busy")).strip(".-")
        self.reason = normalized_reason or "busy"
        self.project_root = _resolve_project_root() if project_root is None else project_root
        self.metadata = metadata or {}
        self.ttl_seconds = max(30, int(ttl_seconds or _DEFAULT_BUSY_TTL_SECONDS))
        self.refresh_interval_seconds = max(5, int(refresh_interval_seconds or _DEFAULT_BUSY_REFRESH_SECONDS))
        self.lease_id = uuid.uuid4().hex
        self.path = _busy_dir(self.project_root) / f"{self.reason}.{self.lease_id}.json"
        self._stop = threading.Event()
        self._thread: Optional[threading.Thread] = None
        self._registered_atexit = False

    def _payload(self) -> Dict[str, Any]:
        now = time.time()
        return {
            "lease_id": self.lease_id,
            "reason": self.reason,
            "pid": os.getpid(),
            "created_at": getattr(self, "_created_at", now),
            "updated_at": now,
            "ttl_seconds": self.ttl_seconds,
            "metadata": self.metadata,
        }

    def _write(self) -> None:
        _write_json_atomic(self.path, self._payload())

    def acquire(self) -> "BusyLease":
        if getattr(self, "_created_at", None) is None:
            self._created_at = time.time()
        self._write()
        if not self._registered_atexit:
            atexit.register(self.close)
            self._registered_atexit = True
        if self._thread is None:
            self._thread = threading.Thread(target=self._refresh_loop, name=f"borealis-busy-{self.reason}", daemon=True)
            self._thread.start()
        return self

    def _refresh_loop(self) -> None:
        while not self._stop.wait(self.refresh_interval_seconds):
            try:
                self._write()
            except Exception:
                pass

    def close(self) -> None:
        self._stop.set()
        if self._thread and self._thread.is_alive() and self._thread is not threading.current_thread():
            self._thread.join(timeout=1)
        try:
            self.path.unlink(missing_ok=True)
        except Exception:
            pass

    def __enter__(self) -> "BusyLease":
        return self.acquire()

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()


def busy_activity(
    reason: str,
    *,
    project_root: Optional[Path] = None,
    metadata: Optional[Dict[str, Any]] = None,
    ttl_seconds: int = _DEFAULT_BUSY_TTL_SECONDS,
    refresh_interval_seconds: int = _DEFAULT_BUSY_REFRESH_SECONDS,
) -> BusyLease:
    return BusyLease(
        reason,
        project_root=project_root,
        metadata=metadata,
        ttl_seconds=ttl_seconds,
        refresh_interval_seconds=refresh_interval_seconds,
    )


def busy_entries(project_root: Optional[Path] = None) -> List[Dict[str, Any]]:
    now = time.time()
    entries: List[Dict[str, Any]] = []
    for path in sorted(_busy_dir(project_root).glob("*.json")):
        try:
            payload = json.loads(path.read_text(encoding="utf-8"))
        except Exception:
            try:
                path.unlink(missing_ok=True)
            except Exception:
                pass
            continue

        ttl_seconds = max(30, int(payload.get("ttl_seconds") or _DEFAULT_BUSY_TTL_SECONDS))
        updated_at = float(payload.get("updated_at") or 0)
        age_seconds = max(0.0, now - updated_at) if updated_at else float("inf")
        if age_seconds > (ttl_seconds + 30):
            try:
                path.unlink(missing_ok=True)
            except Exception:
                pass
            continue

        payload["lease_path"] = str(path)
        payload["age_seconds"] = age_seconds
        entries.append(payload)
    return entries


def get_busy_snapshot(project_root: Optional[Path] = None) -> Dict[str, Any]:
    entries = busy_entries(project_root)
    reasons = [str(entry.get("reason") or "").strip() for entry in entries if str(entry.get("reason") or "").strip()]
    installed_build_id = read_installed_build_id(project_root)
    repo_build_id = read_repo_build_id(project_root)
    pending_update = read_pending_update(project_root)
    update_status = read_update_status(project_root)
    return {
        "busy": bool(entries),
        "reasons": reasons,
        "entries": entries,
        "installed_build_id": installed_build_id,
        "repo_build_id": repo_build_id,
        "pending_update": pending_update,
        "update_status": update_status,
    }


def _cli_status() -> int:
    print(json.dumps(get_busy_snapshot(), sort_keys=True))
    return 0


def _cli_sync_build_id() -> int:
    build_id = sync_installed_build_id()
    if build_id:
        print(build_id)
        return 0
    return 1


def _cli_print_build_id() -> int:
    build_id = read_installed_build_id() or read_repo_build_id()
    if build_id:
        print(build_id)
        return 0
    return 1


def main() -> int:
    parser = argparse.ArgumentParser(description="Borealis agent update state helper")
    subparsers = parser.add_subparsers(dest="command")
    subparsers.required = True
    subparsers.add_parser("status", help="Print updater busy/build status as JSON")
    subparsers.add_parser("sync-build-id", help="Persist the current repo build id into installed_build_id.txt")
    subparsers.add_parser("print-build-id", help="Print the installed build id, falling back to the repo build id")
    args = parser.parse_args()

    if args.command == "status":
        return _cli_status()
    if args.command == "sync-build-id":
        return _cli_sync_build_id()
    if args.command == "print-build-id":
        return _cli_print_build_id()
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
