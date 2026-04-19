from __future__ import annotations

import hashlib
import json
import logging
import os
import re
import threading
import time
import zipfile
from pathlib import Path
from typing import Any, Callable, Dict, Optional

from Data.Engine.auth.guid_utils import normalize_guid
from Data.Engine.db import dbapi as sqlite3


VALID_AGENT_RELEASE_CHANNELS = {"stable", "unstable"}
DEFAULT_AGENT_RELEASE_CHANNEL = "stable"
DEFAULT_REFRESH_INTERVAL_SECONDS = 60
_ARTIFACT_ID_PATTERN = re.compile(r"[^a-z0-9._-]+")
_SETTINGS_PATH_ENV = "BOREALIS_AGENT_RELEASE_CHANNELS_PATH"
_REQUIRED_UPDATE_STATE_MARKERS = (
    "def clear_pending_update(",
    "def read_pending_update(",
    "def read_update_status(",
    "def write_pending_update(",
    "def write_update_status(",
)
_REQUIRED_UPDATE_HELPER_MARKERS = (
    'add_parser("prepare-update"',
    'add_parser("finalize-update"',
)


def _project_root() -> Path:
    return Path(__file__).resolve().parents[3]


def _config_path() -> Path:
    override = _clean_text(os.environ.get(_SETTINGS_PATH_ENV))
    if override:
        return Path(override).expanduser()
    return _project_root() / "Engine" / "Config" / "agent_release_channels.json"


def _cache_root() -> Path:
    return _project_root() / "Engine" / "Cache" / "AgentUpdates"


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


def _write_json_atomic(path: Path, payload: Dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temp_path = path.with_suffix(path.suffix + ".tmp")
    temp_path.write_text(json.dumps(payload, indent=2, sort_keys=True), encoding="utf-8")
    temp_path.replace(path)


def _safe_sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        while True:
            chunk = handle.read(1024 * 1024)
            if not chunk:
                break
            digest.update(chunk)
    return digest.hexdigest().lower()


def _artifact_filename(artifact_id: str) -> str:
    return f"{artifact_id}.zip"


def _artifact_id(channel: str, build_id: str) -> str:
    cleaned_channel = _ARTIFACT_ID_PATTERN.sub("-", _clean_text(channel).lower()).strip("-") or "channel"
    cleaned_build = _ARTIFACT_ID_PATTERN.sub("-", _clean_text(build_id).lower()).strip("-") or "build"
    return f"{cleaned_channel}-{cleaned_build[:20]}"


def _archive_member_text(archive: zipfile.ZipFile, suffix: str) -> str:
    normalized_suffix = suffix.replace("\\", "/").lstrip("/")
    for name in archive.namelist():
        normalized_name = str(name or "").replace("\\", "/").strip("/")
        if not normalized_name:
            continue
        if normalized_name == normalized_suffix or normalized_name.endswith(f"/{normalized_suffix}"):
            with archive.open(name) as handle:
                return handle.read().decode("utf-8", errors="ignore")
    return ""


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
        self._refresh_interval_seconds = max(15, int(refresh_interval_seconds or DEFAULT_REFRESH_INTERVAL_SECONDS))
        self._settings_path = _config_path()
        self._cache_root = _cache_root()
        self._lock = threading.RLock()
        self._refresh_lock = threading.Lock()
        self._thread: Optional[threading.Thread] = None
        self._stop_event = threading.Event()
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
                    self._persist_settings(merged)
                    return merged
        except Exception:
            self._logger.debug("Failed to load agent release channel settings", exc_info=True)
        self._persist_settings(defaults)
        return defaults

    def _persist_settings(self, payload: Dict[str, Any]) -> None:
        payload = _json_clone(payload)
        payload["updated_at"] = _now_ts()
        try:
            _write_json_atomic(self._settings_path, payload)
            self._last_persist_error = ""
        except Exception as exc:
            self._last_persist_error = str(exc)
            self._logger.debug("Failed to persist agent release channel settings", exc_info=True)
            self._log(f"agent release channel settings not persisted path={self._settings_path} err={exc}", level="WARN")

    def _settings_snapshot(self) -> Dict[str, Any]:
        with self._lock:
            return _json_clone(self._settings)

    def _update_settings(self, payload: Dict[str, Any]) -> Dict[str, Any]:
        with self._lock:
            self._settings = _json_clone(payload)
            self._persist_settings(self._settings)
            return _json_clone(self._settings)

    def start(self) -> None:
        with self._lock:
            if self._thread is not None and self._thread.is_alive():
                return
            self._stop_event.clear()
            self._thread = threading.Thread(target=self._refresh_loop, name="borealis-agent-release-poller", daemon=True)
            self._thread.start()
        self._log("agent release channel poller started")

    def stop(self) -> None:
        self._stop_event.set()

    def _refresh_loop(self) -> None:
        while not self._stop_event.is_set():
            try:
                self.refresh_channels()
            except Exception as exc:
                self._log(f"release channel refresh loop failed err={exc}", level="ERROR")
            self._stop_event.wait(self._refresh_interval_seconds)

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

    def set_settings(self, *, default_channel: Optional[str] = None, repo: Optional[str] = None) -> Dict[str, Any]:
        snapshot = self._settings_snapshot()
        if default_channel is not None:
            snapshot["default_channel"] = _normalize_channel(default_channel)
        if repo is not None:
            snapshot.setdefault("github", {})
            snapshot["github"]["repo"] = _normalize_repo(repo, fallback=self._default_settings()["github"]["repo"])
        updated = self._update_settings(snapshot)
        self.refresh_channels(force=True)
        return self.get_settings()

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

    def refresh_channels(self, *, force: bool = False) -> Dict[str, Any]:
        if not self._refresh_lock.acquire(blocking=False):
            return self.get_settings()
        try:
            snapshot = self._settings_snapshot()
            repo = _normalize_repo(snapshot.get("github", {}).get("repo"), fallback=self._default_settings()["github"]["repo"])
            snapshot.setdefault("github", {})
            snapshot["github"]["repo"] = repo
            snapshot["last_refresh_started_at"] = _now_ts()
            snapshot["last_refresh_error"] = ""
            prior_channels = _json_clone(snapshot.get("channels") or {})

            repo_payload = self._repo_metadata(repo)
            default_branch = _clean_text(repo_payload.get("default_branch")) or _clean_text(snapshot.get("github", {}).get("default_branch")) or "main"
            snapshot["github"]["default_branch"] = default_branch

            stable_candidate = self._stable_candidate(repo)
            unstable_candidate = self._unstable_candidate(repo, default_branch)

            refresh_errors = []

            try:
                stable_target = self._ensure_cached_artifact(repo, stable_candidate, force=force)
            except Exception as exc:
                refresh_errors.append(f"stable: {exc}")
                stable_target = self._channel_error_target(
                    repo=repo,
                    candidate=stable_candidate,
                    prior_target=prior_channels.get("stable") if isinstance(prior_channels, dict) else None,
                    error=exc,
                )
                self._log(f"stable agent release target unavailable err={exc}", level="WARN")

            try:
                unstable_target = self._ensure_cached_artifact(repo, unstable_candidate, force=force)
            except Exception as exc:
                refresh_errors.append(f"unstable: {exc}")
                unstable_target = self._channel_error_target(
                    repo=repo,
                    candidate=unstable_candidate,
                    prior_target=prior_channels.get("unstable") if isinstance(prior_channels, dict) else None,
                    error=exc,
                )
                self._log(f"unstable agent release target unavailable err={exc}", level="WARN")

            snapshot["channels"]["stable"] = stable_target
            snapshot["channels"]["unstable"] = unstable_target
            snapshot["last_refresh_completed_at"] = _now_ts()
            snapshot["last_refresh_error"] = "; ".join(refresh_errors)
            self._update_settings(snapshot)

            changed_channels = []
            for channel in VALID_AGENT_RELEASE_CHANNELS:
                prior_build = _clean_text(prior_channels.get(channel, {}).get("build_id"))
                current_build = _clean_text(snapshot["channels"].get(channel, {}).get("build_id"))
                if current_build and current_build != prior_build:
                    changed_channels.append(channel)

            if changed_channels:
                self._log(
                    "promoted agent release targets " + ", ".join(
                        f"{channel}={_clean_text(snapshot['channels'][channel].get('build_id'))[:12]}"
                        for channel in changed_channels
                    )
                )
                self._emit_updates_for_channels(changed_channels)

            return self.get_settings()
        except Exception as exc:
            snapshot = self._settings_snapshot()
            snapshot["last_refresh_error"] = str(exc)
            snapshot["last_refresh_completed_at"] = _now_ts()
            self._update_settings(snapshot)
            self._log(f"release channel refresh failed err={exc}", level="ERROR")
            return self.get_settings()
        finally:
            self._refresh_lock.release()

    def _github_headers(self) -> Dict[str, str]:
        headers = {
            "Accept": "application/vnd.github+json",
            "User-Agent": "Borealis-Engine",
        }
        token = None
        try:
            token = self._github._github_token(force_refresh=False) if self._github is not None else None
        except Exception:
            token = None
        if token:
            headers["Authorization"] = f"Bearer {token}"
        return headers

    def _request_json(self, url: str) -> Dict[str, Any]:
        response = self._github._http_get(url, headers=self._github_headers(), timeout=30)
        status = getattr(response, "status_code", 0)
        if status != 200:
            snippet = _clean_text(getattr(response, "text", ""))[:200]
            raise RuntimeError(f"GitHub request failed status={status} url={url} detail={snippet or '-'}")
        payload = response.json()
        if not isinstance(payload, dict):
            raise RuntimeError(f"GitHub request returned invalid JSON url={url}")
        return payload

    def _repo_metadata(self, repo: str) -> Dict[str, Any]:
        return self._request_json(f"https://api.github.com/repos/{repo}")

    def _commit_metadata(self, repo: str, ref_name: str) -> Dict[str, str]:
        payload = self._request_json(f"https://api.github.com/repos/{repo}/commits/{ref_name}")
        sha = _clean_text(payload.get("sha")).lower()
        if not sha:
            raise RuntimeError(f"GitHub commit lookup missing sha for ref={ref_name}")
        commit_payload = payload.get("commit") if isinstance(payload.get("commit"), dict) else {}
        author_payload = commit_payload.get("author") if isinstance(commit_payload.get("author"), dict) else {}
        committer_payload = commit_payload.get("committer") if isinstance(commit_payload.get("committer"), dict) else {}
        published_at = _clean_text(author_payload.get("date")) or _clean_text(committer_payload.get("date"))
        return {
            "sha": sha,
            "published_at": published_at,
        }

    def _stable_candidate(self, repo: str) -> Dict[str, Any]:
        payload = self._request_json(f"https://api.github.com/repos/{repo}/releases/latest")
        tag_name = _clean_text(payload.get("tag_name"))
        if not tag_name:
            raise RuntimeError("GitHub latest release payload missing tag_name")
        build_id = self._commit_metadata(repo, tag_name).get("sha") or ""
        fallback_url = f"https://github.com/{repo}/archive/refs/tags/{tag_name}.zip"
        return {
            "channel": "stable",
            "build_id": build_id,
            "download_url": _clean_text(payload.get("zipball_url")) or f"https://api.github.com/repos/{repo}/zipball/{tag_name}",
            "fallback_url": fallback_url,
            "version_label": tag_name,
            "release_tag": tag_name,
            "release_name": _clean_text(payload.get("name")) or tag_name,
            "published_at": _clean_text(payload.get("published_at")),
        }

    def _unstable_candidate(self, repo: str, default_branch: str) -> Dict[str, Any]:
        commit_meta = self._commit_metadata(repo, default_branch)
        build_id = _clean_text(commit_meta.get("sha")).lower()
        return {
            "channel": "unstable",
            "build_id": build_id,
            "download_url": f"https://api.github.com/repos/{repo}/zipball/{build_id}",
            "fallback_url": f"https://github.com/{repo}/archive/{build_id}.zip",
            "version_label": default_branch,
            "branch": default_branch,
            "published_at": _clean_text(commit_meta.get("published_at")),
        }

    def _download_bytes(self, url: str) -> bytes:
        response = self._github._http_get(url, headers=self._github_headers(), timeout=120)
        status = getattr(response, "status_code", 0)
        if status != 200:
            snippet = _clean_text(getattr(response, "text", ""))[:200]
            raise RuntimeError(f"artifact download failed status={status} url={url} detail={snippet or '-'}")
        body = getattr(response, "content", None)
        if body is None:
            body = getattr(response, "_body", b"")
        if not isinstance(body, (bytes, bytearray)):
            raise RuntimeError("artifact download returned no content")
        return bytes(body)

    def _validate_cached_artifact(self, artifact_path: Path) -> None:
        try:
            with zipfile.ZipFile(artifact_path) as archive:
                update_state_text = _archive_member_text(archive, "Data/Agent/update_state.py")
                update_helper_text = _archive_member_text(archive, "Data/Agent/update_helper.py")
        except zipfile.BadZipFile as exc:
            raise RuntimeError(f"cached artifact is not a valid zip: {exc}") from exc

        if not update_state_text or not update_helper_text:
            raise RuntimeError("artifact is missing Data/Agent/update_state.py or Data/Agent/update_helper.py")

        missing_state = [marker for marker in _REQUIRED_UPDATE_STATE_MARKERS if marker not in update_state_text]
        if missing_state:
            missing_names = [marker.replace("def ", "").replace("(", "") for marker in missing_state]
            raise RuntimeError(
                "artifact predates the Engine-managed updater interface "
                f"(missing update_state APIs: {', '.join(missing_names)})"
            )

        missing_helper = [marker for marker in _REQUIRED_UPDATE_HELPER_MARKERS if marker not in update_helper_text]
        if missing_helper:
            raise RuntimeError("artifact predates the Engine-managed updater commands required by release channels")

    def _validated_prior_target(self, target: Dict[str, Any]) -> Optional[Dict[str, Any]]:
        if not isinstance(target, dict):
            return None
        artifact_path = Path(_clean_text(target.get("artifact_path"))) if _clean_text(target.get("artifact_path")) else None
        build_id = _clean_text(target.get("build_id")).lower()
        if not artifact_path or not artifact_path.is_file() or not build_id:
            return None
        self._validate_cached_artifact(artifact_path)
        return _json_clone(target)

    def _channel_error_target(
        self,
        *,
        repo: str,
        candidate: Dict[str, Any],
        prior_target: Optional[Dict[str, Any]],
        error: Exception,
    ) -> Dict[str, Any]:
        preserved: Optional[Dict[str, Any]] = None
        if prior_target:
            try:
                preserved = self._validated_prior_target(prior_target)
            except Exception:
                preserved = None

        base = preserved or {
            "channel": _normalize_channel(candidate.get("channel")),
            "repo": repo,
            "build_id": "",
            "artifact_id": "",
            "artifact_path": "",
            "artifact_sha256": "",
            "artifact_size": 0,
            "promoted_at": 0,
        }
        base.update(
            {
                "channel": _normalize_channel(candidate.get("channel")),
                "repo": repo,
                "download_url": _clean_text(candidate.get("download_url")),
                "fallback_url": _clean_text(candidate.get("fallback_url")),
                "version_label": _clean_text(candidate.get("version_label")),
                "release_tag": _clean_text(candidate.get("release_tag")),
                "release_name": _clean_text(candidate.get("release_name")),
                "published_at": _clean_text(candidate.get("published_at")),
                "branch": _clean_text(candidate.get("branch")),
                "refreshed_at": _now_ts(),
                "last_error": str(error),
            }
        )
        if preserved is None:
            base["build_id"] = ""
            base["artifact_id"] = ""
            base["artifact_path"] = ""
            base["artifact_sha256"] = ""
            base["artifact_size"] = 0
            base["promoted_at"] = 0
        return base

    def _ensure_cached_artifact(self, repo: str, candidate: Dict[str, Any], *, force: bool = False) -> Dict[str, Any]:
        channel = _normalize_channel(candidate.get("channel"))
        build_id = _clean_text(candidate.get("build_id")).lower()
        if not build_id:
            raise RuntimeError(f"{channel} target missing build id")
        artifact_id = _artifact_id(channel, build_id)
        artifact_path = self._cache_root / _artifact_filename(artifact_id)
        artifact_sha256 = ""
        artifact_size = 0

        if artifact_path.is_file() and not force:
            artifact_sha256 = _safe_sha_file(artifact_path)
            try:
                artifact_size = int(artifact_path.stat().st_size)
            except Exception:
                artifact_size = 0
        else:
            artifact_path.parent.mkdir(parents=True, exist_ok=True)
            temp_path = artifact_path.with_suffix(".download")
            payload = self._download_bytes(_clean_text(candidate.get("download_url")))
            temp_path.write_bytes(payload)
            self._validate_cached_artifact(temp_path)
            artifact_sha256 = _safe_sha_file(temp_path)
            artifact_size = int(temp_path.stat().st_size)
            temp_path.replace(artifact_path)

        self._validate_cached_artifact(artifact_path)

        manifest = {
            "channel": channel,
            "repo": repo,
            "build_id": build_id,
            "artifact_id": artifact_id,
            "artifact_path": str(artifact_path),
            "artifact_sha256": artifact_sha256,
            "artifact_size": artifact_size,
            "download_url": _clean_text(candidate.get("download_url")),
            "fallback_url": _clean_text(candidate.get("fallback_url")),
            "version_label": _clean_text(candidate.get("version_label")),
            "release_tag": _clean_text(candidate.get("release_tag")),
            "release_name": _clean_text(candidate.get("release_name")),
            "published_at": _clean_text(candidate.get("published_at")),
            "branch": _clean_text(candidate.get("branch")),
            "promoted_at": _now_ts(),
            "refreshed_at": _now_ts(),
            "last_error": "",
        }
        manifest_path = self._cache_root / f"{artifact_id}.json"
        _write_json_atomic(manifest_path, manifest)
        return manifest

    def _load_device_identity(self, *, guid: str = "", hostname: str = "") -> Optional[Dict[str, Any]]:
        normalized_guid = normalize_guid(guid) if _clean_text(guid) else ""
        normalized_host = _clean_text(hostname).lower()
        conn = self._db_conn_factory()
        try:
            cur = conn.cursor()
            if normalized_guid:
                cur.execute(
                    """
                    SELECT guid, hostname, agent_hash, agent_id, agent_release_channel_override
                      FROM devices
                     WHERE UPPER(guid) = ?
                     LIMIT 1
                    """,
                    (normalized_guid,),
                )
            elif normalized_host:
                cur.execute(
                    """
                    SELECT guid, hostname, agent_hash, agent_id, agent_release_channel_override
                      FROM devices
                     WHERE LOWER(hostname) = LOWER(?)
                     ORDER BY last_seen DESC
                     LIMIT 1
                    """,
                    (normalized_host,),
                )
            else:
                return None
            row = cur.fetchone()
            if not row:
                return None
            return {
                "guid": normalize_guid(row[0]) or "",
                "hostname": _clean_text(row[1]),
                "agent_hash": _clean_text(row[2]).lower(),
                "agent_id": _clean_text(row[3]),
                "agent_release_channel_override": _normalize_channel(row[4], default="") if _clean_text(row[4]) else "",
            }
        finally:
            conn.close()

    def manifest_for_device(
        self,
        *,
        guid: str = "",
        hostname: str = "",
        installed_build_id: str = "",
    ) -> Dict[str, Any]:
        record = self._load_device_identity(guid=guid, hostname=hostname)
        if record is None:
            raise RuntimeError("device not found")
        effective_channel = self.resolve_effective_channel(record.get("agent_release_channel_override"))
        target = self.target_for_channel(effective_channel)
        target_build_id = _clean_text(target.get("build_id")).lower()
        if not target_build_id:
            raise RuntimeError("channel target unavailable")
        current_build_id = _clean_text(installed_build_id).lower() or _clean_text(record.get("agent_hash")).lower()
        artifact_id = _clean_text(target.get("artifact_id"))
        return {
            "status": "ok",
            "hostname": record.get("hostname") or hostname,
            "guid": record.get("guid") or guid,
            "effective_channel": effective_channel,
            "target_channel": effective_channel,
            "target_build_id": target_build_id,
            "update_available": bool(not current_build_id or current_build_id != target_build_id),
            "artifact_id": artifact_id,
            "artifact_sha256": _clean_text(target.get("artifact_sha256")),
            "artifact_size": int(target.get("artifact_size") or 0),
            "download_path": f"/api/agent/update/download/{artifact_id}",
            "fallback_url": _clean_text(target.get("fallback_url")),
            "release_tag": _clean_text(target.get("release_tag")),
            "release_name": _clean_text(target.get("release_name")),
            "version_label": _clean_text(target.get("version_label")),
            "published_at": _clean_text(target.get("published_at")),
            "branch": _clean_text(target.get("branch")),
            "repo": _clean_text(self._settings_snapshot().get("github", {}).get("repo")),
            "promoted_at": int(target.get("promoted_at") or 0),
        }

    def heartbeat_hint_for_device(
        self,
        *,
        guid: str = "",
        hostname: str = "",
        installed_build_id: str = "",
    ) -> Dict[str, Any]:
        try:
            manifest = self.manifest_for_device(guid=guid, hostname=hostname, installed_build_id=installed_build_id)
        except Exception:
            return {
                "update_available": False,
                "target_channel": "",
                "target_build_id": "",
            }
        return {
            "update_available": bool(manifest.get("update_available")),
            "target_channel": _clean_text(manifest.get("target_channel")),
            "target_build_id": _clean_text(manifest.get("target_build_id")),
        }

    def artifact_path_for_id(self, artifact_id: str) -> Optional[Path]:
        normalized = _clean_text(artifact_id)
        if not normalized:
            return None
        candidate = self._cache_root / _artifact_filename(normalized)
        if not candidate.is_file():
            return None
        return candidate

    def notify_device(self, *, guid: str = "", hostname: str = "") -> bool:
        record = self._load_device_identity(guid=guid, hostname=hostname)
        if record is None:
            return False
        try:
            manifest = self.manifest_for_device(
                guid=record.get("guid") or guid,
                hostname=record.get("hostname") or hostname,
                installed_build_id=record.get("agent_hash") or "",
            )
        except Exception:
            return False
        if not manifest.get("update_available"):
            return False
        payload = {
            "hostname": record.get("hostname") or hostname,
            "agent_id": record.get("agent_id") or "",
            "requested_at": _now_ts(),
            "target_channel": _clean_text(manifest.get("target_channel")),
            "target_build_id": _clean_text(manifest.get("target_build_id")),
            "artifact_id": _clean_text(manifest.get("artifact_id")),
        }
        emit_host_service_event = getattr(self._context, "emit_host_service_event", None)
        emit_agent_event = getattr(self._context, "emit_agent_event", None)
        emitted = False
        if callable(emit_host_service_event) and _clean_text(record.get("hostname")):
            try:
                emitted = bool(
                    emit_host_service_event(
                        record.get("hostname"),
                        "system",
                        "agent_update_available",
                        payload,
                    )
                )
            except Exception:
                emitted = False
        if not emitted and callable(emit_agent_event) and _clean_text(record.get("agent_id")):
            try:
                emitted = bool(emit_agent_event(record.get("agent_id"), "agent_update_available", payload))
            except Exception:
                emitted = False
        return emitted

    def _emit_updates_for_channels(self, channels: list[str]) -> None:
        normalized_channels = {self.resolve_effective_channel(channel) for channel in channels if channel}
        if not normalized_channels:
            return
        snapshot = self._settings_snapshot()
        default_channel = self.resolve_effective_channel(snapshot.get("default_channel"))
        conn = self._db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT guid, hostname, agent_hash, agent_id, agent_release_channel_override
                  FROM devices
                """
            )
            rows = cur.fetchall()
        finally:
            conn.close()
        for row in rows or []:
            override = _normalize_channel(row[4], default="") if _clean_text(row[4]) else ""
            effective_channel = override if override in VALID_AGENT_RELEASE_CHANNELS else default_channel
            if effective_channel not in normalized_channels:
                continue
            target = snapshot.get("channels", {}).get(effective_channel, {})
            target_build_id = _clean_text(target.get("build_id")).lower()
            installed_build_id = _clean_text(row[2]).lower()
            if target_build_id and installed_build_id == target_build_id:
                continue
            try:
                self.notify_device(guid=row[0], hostname=row[1])
            except Exception:
                self._logger.debug("Failed to notify device about channel target change", exc_info=True)
