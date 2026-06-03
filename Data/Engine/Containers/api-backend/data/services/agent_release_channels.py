from __future__ import annotations

import json
import logging
import os
import threading
import time
from pathlib import Path
from typing import Any, Callable, Dict, Optional

from Data.Engine.db import dbapi as sqlite3


VALID_AGENT_RELEASE_CHANNELS = {"stable", "unstable"}
DEFAULT_AGENT_RELEASE_CHANNEL = "stable"
DEFAULT_REFRESH_INTERVAL_SECONDS = 60
_SETTINGS_PATH_ENV = "BOREALIS_AGENT_RELEASE_CHANNELS_PATH"


def _project_root() -> Path:
    root_env = _clean_text(os.environ.get("BOREALIS_PROJECT_ROOT"))
    if root_env:
        return Path(root_env).expanduser().resolve()
    current = Path(__file__).resolve()
    for candidate in (current, *current.parents):
        if (candidate / "Engine.sh").is_file():
            return candidate
    return Path.cwd().resolve()


def _config_path() -> Path:
    override = _clean_text(os.environ.get(_SETTINGS_PATH_ENV))
    if override:
        return Path(override).expanduser()
    return (
        _project_root()
        / "Engine"
        / "Services"
        / "api-backend"
        / "config"
        / "agent_release_channels.json"
    )


def _now_ts() -> int:
    return int(time.time())


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _normalize_channel(value: Any, *, default: str = DEFAULT_AGENT_RELEASE_CHANNEL) -> str:
    text = _clean_text(value).lower()
    if text in VALID_AGENT_RELEASE_CHANNELS:
        return text
    return default


def _normalize_repo(value: Any, *, fallback: str) -> str:
    candidate = _clean_text(value) or fallback
    if "/" not in candidate:
        return fallback
    return candidate


def _json_clone(value: Any) -> Any:
    try:
        return json.loads(json.dumps(value))
    except Exception:
        if isinstance(value, dict):
            return dict(value)
        if isinstance(value, list):
            return list(value)
        return value


class AgentReleaseChannelManager:
    def __init__(
        self,
        *,
        context: Any,
        db_conn_factory: Callable[[], sqlite3.Connection],
        github_integration: Any,
        service_log: Optional[Callable[[str, str, Optional[str]], None]] = None,
        logger: Optional[logging.Logger] = None,
        refresh_interval_seconds: int = DEFAULT_REFRESH_INTERVAL_SECONDS,
    ) -> None:
        self._context = context
        self._db_conn_factory = db_conn_factory
        self._github = github_integration
        self._service_log = service_log
        self._logger = logger or logging.getLogger(__name__)
        self._settings_path = _config_path()
        self._lock = threading.RLock()
        self._last_persist_error = ""
        self._settings = self._load_or_create_settings()

    def _log(self, message: str, *, level: str = "INFO") -> None:
        if callable(self._service_log):
            try:
                self._service_log("agent_release_channels", message, level=level)
            except Exception:
                self._logger.debug("agent release channel service log write failed", exc_info=True)
        numeric_level = getattr(logging, level.upper(), logging.INFO)
        self._logger.log(numeric_level, "%s", message)

    def _default_settings(self) -> Dict[str, Any]:
        default_repo = _normalize_repo(
            getattr(self._github, "default_repo", "") or os.environ.get("BOREALIS_UPDATE_REPO"),
            fallback="bunny-lab-io/Borealis",
        )
        default_branch = _clean_text(getattr(self._github, "default_branch", "") or os.environ.get("BOREALIS_UPDATE_BRANCH") or "main") or "main"
        now_ts = _now_ts()
        return {
            "version": 1,
            "default_channel": DEFAULT_AGENT_RELEASE_CHANNEL,
            "github": {
                "repo": default_repo,
                "default_branch": default_branch,
            },
            "channels": {
                "stable": {
                    "channel": "stable",
                    "build_id": "",
                    "artifact_id": "",
                    "artifact_sha256": "",
                    "artifact_size": 0,
                    "artifact_path": "",
                    "download_url": "",
                    "fallback_url": "",
                    "version_label": "",
                    "release_tag": "",
                    "release_name": "",
                    "published_at": "",
                    "promoted_at": 0,
                    "refreshed_at": 0,
                    "last_error": "",
                },
                "unstable": {
                    "channel": "unstable",
                    "build_id": "",
                    "artifact_id": "",
                    "artifact_sha256": "",
                    "artifact_size": 0,
                    "artifact_path": "",
                    "download_url": "",
                    "fallback_url": "",
                    "version_label": default_branch,
                    "branch": default_branch,
                    "promoted_at": 0,
                    "refreshed_at": 0,
                    "last_error": "",
                },
            },
            "last_refresh_started_at": 0,
            "last_refresh_completed_at": 0,
            "last_refresh_error": "",
            "created_at": now_ts,
            "updated_at": now_ts,
        }

    def _load_or_create_settings(self) -> Dict[str, Any]:
        defaults = self._default_settings()
        try:
            if self._settings_path.is_file():
                loaded = json.loads(self._settings_path.read_text(encoding="utf-8"))
                if isinstance(loaded, dict):
                    merged = _json_clone(defaults)
                    merged.update({k: v for k, v in loaded.items() if k != "channels"})
                    loaded_channels = loaded.get("channels") if isinstance(loaded.get("channels"), dict) else {}
                    for channel in VALID_AGENT_RELEASE_CHANNELS:
                        existing = loaded_channels.get(channel) if isinstance(loaded_channels, dict) else {}
                        if isinstance(existing, dict):
                            merged["channels"][channel].update(existing)
                    merged["default_channel"] = _normalize_channel(merged.get("default_channel"))
                    merged["github"]["repo"] = _normalize_repo(merged.get("github", {}).get("repo"), fallback=defaults["github"]["repo"])
                    merged["github"]["default_branch"] = _clean_text(merged.get("github", {}).get("default_branch")) or defaults["github"]["default_branch"]
                    return merged
        except Exception:
            self._logger.debug("Failed to load agent release channel settings", exc_info=True)
        return defaults

    def _settings_snapshot(self) -> Dict[str, Any]:
        with self._lock:
            try:
                if self._settings_path.is_file():
                    loaded = json.loads(self._settings_path.read_text(encoding="utf-8"))
                    if isinstance(loaded, dict):
                        defaults = self._default_settings()
                        merged = _json_clone(defaults)
                        merged.update({k: v for k, v in loaded.items() if k != "channels"})
                        loaded_channels = loaded.get("channels") if isinstance(loaded.get("channels"), dict) else {}
                        for channel in VALID_AGENT_RELEASE_CHANNELS:
                            existing = loaded_channels.get(channel) if isinstance(loaded_channels, dict) else {}
                            if isinstance(existing, dict):
                                merged["channels"][channel].update(existing)
                        merged["default_channel"] = _normalize_channel(merged.get("default_channel"))
                        merged["github"]["repo"] = _normalize_repo(
                            merged.get("github", {}).get("repo"),
                            fallback=defaults["github"]["repo"],
                        )
                        merged["github"]["default_branch"] = (
                            _clean_text(merged.get("github", {}).get("default_branch"))
                            or defaults["github"]["default_branch"]
                        )
                        self._settings = merged
            except Exception:
                self._logger.debug("Failed to reload agent release channel settings", exc_info=True)
            return _json_clone(self._settings)

    def start(self) -> None:
        self._log("agent release channel poller disabled; Go api-backend owns refresh")

    def stop(self) -> None:
        return None

    def get_settings(self) -> Dict[str, Any]:
        snapshot = self._settings_snapshot()
        snapshot["github_token"] = self._github_token_payload()
        snapshot["settings_path"] = str(self._settings_path)
        snapshot["last_persist_error"] = self._last_persist_error
        return snapshot

    def _github_token_payload(self) -> Dict[str, Any]:
        token_state = {}
        try:
            token_state = self._github.token_storage_state() if self._github is not None else {}
        except Exception:
            token_state = {}
        return {
            "has_token": bool(token_state.get("has_token")),
            "reset_required": bool(token_state.get("reset_required")),
            "reset_at": int(token_state.get("reset_at") or 0) if token_state else 0,
        }

    def resolve_effective_channel(self, override: Any) -> str:
        snapshot = self._settings_snapshot()
        override_channel = _normalize_channel(override, default="")
        if override_channel in VALID_AGENT_RELEASE_CHANNELS:
            return override_channel
        return _normalize_channel(snapshot.get("default_channel"))

    def target_for_channel(self, channel: Any) -> Dict[str, Any]:
        snapshot = self._settings_snapshot()
        normalized = _normalize_channel(channel)
        channels = snapshot.get("channels") if isinstance(snapshot.get("channels"), dict) else {}
        target = channels.get(normalized) if isinstance(channels, dict) else {}
        if not isinstance(target, dict):
            target = {}
        payload = _json_clone(target)
        payload["channel"] = normalized
        return payload

    def target_for_override(self, override: Any) -> Dict[str, Any]:
        return self.target_for_channel(self.resolve_effective_channel(override))
