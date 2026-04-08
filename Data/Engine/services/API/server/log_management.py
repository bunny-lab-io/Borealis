# ======================================================
# Data\Engine\services\API\server\log_management.py
# Description: REST endpoints for enumerating Engine log files, viewing entries, pruning retention policies, and deleting log data.
#
# API Endpoints (if applicable):
# - GET /api/server/logs (Operator Admin Session) - Lists Engine log domains, retention policies, and metadata.
# - GET /api/server/logs/<log_name>/entries (Operator Admin Session) - Returns the most recent log lines with parsed fields for tabular display.
# - PUT /api/server/logs/retention (Operator Admin Session) - Updates per-domain log retention policies and applies pruning.
# - DELETE /api/server/logs/<log_name> (Operator Admin Session) - Deletes a specific log file or an entire log domain family (active + rotated files).
# ======================================================

"""Engine log management REST endpoints."""

from __future__ import annotations

import json
import logging
import re
from collections import deque
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any, Deque, Dict, Iterable, List, Mapping, MutableMapping, Optional, Tuple

from flask import Blueprint, jsonify, request

from ....config import LOG_ROOT
from ...auth import RequestAuthContext

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


DEFAULT_RETENTION_DAYS = 30
MAX_TAIL_LINES = 2000
SERVICE_LINE_PATTERN = re.compile(
    r"^\[(?P<ts>[^\]]+)\]\s+\[(?P<level>[A-Z0-9_-]+)\](?P<context>(?:\[[^\]]+\])*)\s+(?P<msg>.*)$"
)
CONTEXT_PATTERN = re.compile(r"\[CONTEXT-([^\]]+)\]", re.IGNORECASE)
PY_LOG_PATTERN = re.compile(
    r"^(?P<ts>\d{4}-\d{2}-\d{2}\s+[0-9:,]+)-(?P<logger>.+?)-(?P<level>[A-Z]+):\s*(?P<msg>.*)$"
)


def _canonical_log_name(name: Optional[str]) -> Optional[str]:
    if not name:
        return None
    cleaned = str(name).strip().replace("\\", "/")
    if "/" in cleaned or cleaned.startswith(".") or ".." in cleaned:
        return None
    return cleaned


def _display_label(filename: str) -> str:
    base = filename
    if base.endswith(".log"):
        base = base[:-4]
    base = base.replace("_", " ").replace("-", " ").strip()
    return base.title() or filename


def _stat_metadata(path: Path) -> Dict[str, Any]:
    try:
        stat = path.stat()
    except FileNotFoundError:
        return {"size_bytes": 0, "modified": None}
    modified = datetime.fromtimestamp(stat.st_mtime, tz=timezone.utc).isoformat()
    return {"size_bytes": stat.st_size, "modified": modified}


def _tail_lines(path: Path, limit: int) -> Tuple[List[str], int, bool]:
    """Return the last ``limit`` lines from ``path`` and indicate truncation."""

    count = 0
    lines: Deque[str] = deque(maxlen=limit)
    with path.open("r", encoding="utf-8", errors="replace") as handle:
        for raw_line in handle:
            count += 1
            lines.append(raw_line.rstrip("\r\n"))
    truncated = count > limit
    return list(lines), count, truncated


def _parse_service_line(raw: str, service_name: str) -> Dict[str, Any]:
    match = SERVICE_LINE_PATTERN.match(raw)
    if match:
        context_block = match.group("context") or ""
        context_match = CONTEXT_PATTERN.search(context_block)
        scope = context_match.group(1) if context_match else None
        return {
            "timestamp": match.group("ts"),
            "level": (match.group("level") or "").upper(),
            "scope": scope,
            "service": service_name,
            "message": match.group("msg").strip(),
            "raw": raw,
        }

    py_match = PY_LOG_PATTERN.match(raw)
    if py_match:
        return {
            "timestamp": py_match.group("ts"),
            "level": (py_match.group("level") or "").upper(),
            "scope": None,
            "service": py_match.group("logger") or service_name,
            "message": py_match.group("msg").strip(),
            "raw": raw,
        }

    return {
        "timestamp": None,
        "level": None,
        "scope": None,
        "service": service_name,
        "message": raw.strip(),
        "raw": raw,
    }


class LogRetentionStore:
    """Persists log retention overrides in ``Engine/Logs``."""

    def __init__(self, path: Path, default_days: int = DEFAULT_RETENTION_DAYS) -> None:
        self.path = path
        self.default_days = default_days
        self.path.parent.mkdir(parents=True, exist_ok=True)

    def load(self) -> Dict[str, int]:
        try:
            with self.path.open("r", encoding="utf-8") as handle:
                data = json.load(handle)
        except FileNotFoundError:
            return {}
        except Exception:
            return {}
        overrides = data.get("overrides") if isinstance(data, dict) else None
        if not isinstance(overrides, dict):
            return {}
        result: Dict[str, int] = {}
        for key, value in overrides.items():
            try:
                days = int(value)
            except (TypeError, ValueError):
                continue
            if days <= 0:
                continue
            canonical = _canonical_log_name(key)
            if canonical:
                result[canonical] = days
        return result

    def save(self, mapping: Mapping[str, int]) -> None:
        payload = {"overrides": {key: int(value) for key, value in mapping.items() if value > 0}}
        tmp_path = self.path.with_suffix(".tmp")
        with tmp_path.open("w", encoding="utf-8") as handle:
            json.dump(payload, handle, indent=2)
        tmp_path.replace(self.path)


class EngineLogManager:
    """Provides filesystem-backed log enumeration and mutation helpers."""

    def __init__(
        self,
        *,
        log_root: Path,
        logger: logging.Logger,
        service_log: Optional[Any] = None,
    ) -> None:
        self.log_root = log_root
        self.logger = logger
        self.service_log = service_log
        self.log_root.mkdir(parents=True, exist_ok=True)

    def _resolve(self, filename: str) -> Path:
        canonical = _canonical_log_name(filename)
        if not canonical:
            raise FileNotFoundError("Invalid log name.")
        candidate = (self.log_root / canonical).resolve()
        try:
            candidate.relative_to(self.log_root.resolve())
        except ValueError:
            raise FileNotFoundError("Log path escapes log root.")
        if not candidate.is_file():
            raise FileNotFoundError(f"{canonical} does not exist.")
        return candidate

    def _base_name(self, filename: str) -> Optional[str]:
        if filename.endswith(".log"):
            return filename
        anchor = filename.split(".log.", 1)
        if len(anchor) == 2:
            return f"{anchor[0]}.log"
        return None

    def _family_files(self, base_name: str) -> List[Path]:
        files: List[Path] = []
        prefix = f"{base_name}."
        for entry in self.log_root.iterdir():
            if not entry.is_file():
                continue
            name = entry.name
            if name == base_name or name.startswith(prefix):
                files.append(entry)
        return files

    def _truncate_active_log(self, path: Path) -> bool:
        """Best-effort truncate for logs held open by logging handlers (Windows-safe)."""

        resolved = path.resolve()
        truncated = False
        for logger_name in list(logging.root.manager.loggerDict.keys()) + [self.logger.name]:
            try:
                logger_obj = logging.getLogger(logger_name)
            except Exception:
                continue
            handlers = list(getattr(logger_obj, "handlers", []))
            for handler in handlers:
                base = getattr(handler, "baseFilename", None)
                if not base:
                    continue
                try:
                    if Path(base).resolve() != resolved:
                        continue
                except Exception:
                    continue
                try:
                    handler.acquire()
                    stream = getattr(handler, "stream", None)
                    if stream:
                        stream.seek(0)
                        stream.truncate()
                        stream.flush()
                    else:
                        with resolved.open("w", encoding="utf-8"):
                            pass
                    truncated = True
                except Exception:
                    self.logger.debug("Failed to truncate active log file: %s", path, exc_info=True)
                finally:
                    try:
                        handler.release()
                    except Exception:
                        pass
        return truncated

    def apply_retention(self, retention: Mapping[str, int], default_days: int) -> List[str]:
        deleted: List[str] = []
        now = datetime.now(tz=timezone.utc)
        for entry in self.log_root.iterdir():
            if not entry.is_file():
                continue
            base = self._base_name(entry.name)
            if not base:
                continue
            days = retention.get(base, default_days)
            if days is None or days <= 0:
                continue
            if entry.name == base:
                # Never delete the active log via automated retention.
                continue
            cutoff = now - timedelta(days=days)
            try:
                modified = datetime.fromtimestamp(entry.stat().st_mtime, tz=timezone.utc)
            except FileNotFoundError:
                continue
            if modified < cutoff:
                try:
                    entry.unlink()
                    deleted.append(entry.name)
                except Exception:
                    self.logger.debug("Failed to delete expired log file: %s", entry, exc_info=True)
        return deleted

    def domain_snapshot(self, retention: Mapping[str, int], default_days: int) -> List[Dict[str, Any]]:
        domains: Dict[str, Dict[str, Any]] = {}
        for entry in sorted(self.log_root.iterdir(), key=lambda p: p.name.lower()):
            if not entry.is_file():
                continue
            base = self._base_name(entry.name)
            if not base:
                continue
            domain = domains.setdefault(
                base,
                {
                    "file": base,
                    "display_name": _display_label(base),
                    "rotations": [],
                    "family_size_bytes": 0,
                },
            )
            metadata = _stat_metadata(entry)
            metadata["file"] = entry.name
            domain["family_size_bytes"] += metadata.get("size_bytes", 0) or 0
            if entry.name == base:
                domain.update(metadata)
            else:
                domain["rotations"].append(metadata)

        for domain in domains.values():
            domain["rotations"].sort(
                key=lambda meta: meta.get("modified") or "",
                reverse=True,
            )
            domain["rotation_count"] = len(domain["rotations"])
            domain["retention_days"] = retention.get(domain["file"], default_days)
            versions = [
                {
                    "file": domain["file"],
                    "label": "Active",
                    "modified": domain.get("modified"),
                    "size_bytes": domain.get("size_bytes"),
                }
            ]
            for item in domain["rotations"]:
                versions.append(
                    {
                        "file": item["file"],
                        "label": item["file"],
                        "modified": item.get("modified"),
                        "size_bytes": item.get("size_bytes"),
                    }
                )
            domain["versions"] = versions
        return sorted(domains.values(), key=lambda d: d["display_name"])

    def read_entries(self, filename: str, limit: int) -> Dict[str, Any]:
        path = self._resolve(filename)
        service_name = _display_label(filename)
        lines, count, truncated = _tail_lines(path, limit)
        entries = []
        start_index = max(count - len(lines), 0)
        for offset, line in enumerate(lines, start=start_index):
            parsed = _parse_service_line(line, service_name)
            parsed["id"] = f"{filename}:{offset}"
            entries.append(parsed)
        stat = path.stat()
        return {
            "file": filename,
            "entries": entries,
            "total_lines": count,
            "returned_lines": len(entries),
            "truncated": truncated,
            "size_bytes": stat.st_size,
            "modified": datetime.fromtimestamp(stat.st_mtime, tz=timezone.utc).isoformat(),
        }

    def delete_file(self, filename: str) -> str:
        path = self._resolve(filename)
        try:
            path.unlink()
        except PermissionError:
            if not self._truncate_active_log(path):
                raise
        return filename

    def delete_family(self, filename: str) -> List[str]:
        base = self._base_name(filename) or filename
        canonical = _canonical_log_name(base)
        if not canonical:
            raise FileNotFoundError("Invalid log name.")
        deleted: List[str] = []
        for entry in self._family_files(canonical):
            try:
                entry.unlink()
            except FileNotFoundError:
                continue
            except PermissionError:
                handled = self._truncate_active_log(entry)
                if handled:
                    deleted.append(entry.name)
                else:
                    self.logger.debug("Failed to delete family log file (in use): %s", entry, exc_info=True)
            except Exception:
                self.logger.debug("Failed to delete family log file: %s", entry, exc_info=True)
            else:
                deleted.append(entry.name)
        if not deleted:
            raise FileNotFoundError(f"No files found for {canonical}.")
        return deleted


def _resolve_log_root(config: Optional[Mapping[str, Any]]) -> Path:
    candidates: Iterable[Optional[str]] = ()
    if config:
        candidates = (
            config.get("log_file"),
            config.get("LOG_FILE"),
            (config.get("raw") or {}).get("log_file") if isinstance(config.get("raw"), Mapping) else None,
        )
    for candidate in candidates:
        if candidate:
            return Path(candidate).expanduser().resolve().parent
    return LOG_ROOT


def register_log_management(app, adapters: "EngineServiceAdapters") -> None:
    """Register log management endpoints."""

    blueprint = Blueprint("engine_server_logs", __name__, url_prefix="/api/server/logs")
    auth = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
        db_conn_factory=adapters.db_conn_factory,
        aegis_cipher_service=adapters.aegis_cipher_service,
    )
    log_root = _resolve_log_root(adapters.config)
    manager = EngineLogManager(
        log_root=log_root,
        logger=adapters.context.logger,
        service_log=adapters.service_log,
    )
    retention_store = LogRetentionStore(log_root / "retention_policy.json")

    def _require_admin() -> Optional[Tuple[Dict[str, Any], int]]:
        error = auth.require_admin()
        if error:
            return error
        return None

    def _audit(action: str, detail: str, *, user: Optional[Dict[str, Any]] = None) -> None:
        actor = user or auth.current_user() or {}
        username = actor.get("username") or "unknown"
        message = f"action={action} user={username} detail={detail}"
        try:
            if manager.service_log:
                manager.service_log("server", message, scope="ADMIN")
        except Exception:
            adapters.context.logger.debug("Failed to emit log management audit entry.", exc_info=True)

    @blueprint.route("", methods=["GET"])
    def list_logs():
        error = _require_admin()
        if error:
            return jsonify(error[0]), error[1]
        retention = retention_store.load()
        deleted = manager.apply_retention(retention, retention_store.default_days)
        payload = {
            "log_root": str(manager.log_root),
            "logs": manager.domain_snapshot(retention, retention_store.default_days),
            "default_retention_days": retention_store.default_days,
            "retention_overrides": retention,
            "retention_deleted": deleted,
        }
        return jsonify(payload)

    @blueprint.route("/<path:log_name>/entries", methods=["GET"])
    def log_entries(log_name: str):
        error = _require_admin()
        if error:
            return jsonify(error[0]), error[1]
        limit_raw = request.args.get("limit")
        try:
            limit = min(int(limit_raw), MAX_TAIL_LINES) if limit_raw else 750
        except (TypeError, ValueError):
            limit = 750
        limit = max(50, min(limit, MAX_TAIL_LINES))
        try:
            snapshot = manager.read_entries(log_name, limit)
        except FileNotFoundError:
            return jsonify({"error": "not_found", "message": "Log file not found."}), 404
        return jsonify(snapshot)

    @blueprint.route("/retention", methods=["PUT"])
    def update_retention():
        error = _require_admin()
        if error:
            return jsonify(error[0]), error[1]
        payload = request.get_json(silent=True) or {}
        retention_payload = payload.get("retention")
        parsed_updates: List[Tuple[str, Optional[int]]] = []
        if isinstance(retention_payload, Mapping):
            items = retention_payload.items()
        elif isinstance(retention_payload, list):
            items = []
            for entry in retention_payload:
                if not isinstance(entry, Mapping):
                    continue
                items.append((entry.get("file") or entry.get("name"), entry.get("days")))
        else:
            items = []
        for key, value in items:
            canonical = _canonical_log_name(key)
            if not canonical:
                continue
            if value in ("", False):
                continue
            days_value: Optional[int]
            if value is None:
                days_value = None
            else:
                try:
                    days_candidate = int(value)
                except (TypeError, ValueError):
                    continue
                days_value = days_candidate if days_candidate > 0 else None
            parsed_updates.append((canonical, days_value))
        retention_map = retention_store.load()
        changed = 0
        for key, days in parsed_updates:
            if days is None:
                if key in retention_map:
                    retention_map.pop(key, None)
                    changed += 1
                continue
            if retention_map.get(key) != days:
                retention_map[key] = days
                changed += 1
        retention_store.save(retention_map)
        retention = retention_store.load()
        deleted = manager.apply_retention(retention, retention_store.default_days)
        user = auth.current_user()
        _audit("retention_update", f"entries={changed} deleted={len(deleted)}", user=user)
        return jsonify(
            {
                "status": "ok",
                "logs": manager.domain_snapshot(retention, retention_store.default_days),
                "retention_overrides": retention,
                "retention_deleted": deleted,
            }
        )

    @blueprint.route("/<path:log_name>", methods=["DELETE"])
    def delete_log(log_name: str):
        error = _require_admin()
        if error:
            return jsonify(error[0]), error[1]
        scope = (request.args.get("scope") or "file").lower()
        deleted: List[str] = []
        try:
            if scope == "family":
                deleted = manager.delete_family(log_name)
            else:
                deleted = [manager.delete_file(log_name)]
        except FileNotFoundError:
            return jsonify({"error": "not_found", "message": "Log file not found."}), 404
        user = auth.current_user()
        _audit("log_delete", f"scope={scope} files={','.join(deleted)}", user=user)
        retention = retention_store.load()
        payload = {
            "status": "deleted",
            "deleted": deleted,
            "logs": manager.domain_snapshot(retention, retention_store.default_days),
        }
        return jsonify(payload)

    app.register_blueprint(blueprint)
