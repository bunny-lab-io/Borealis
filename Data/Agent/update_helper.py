from __future__ import annotations

import argparse
import hashlib
import importlib.util
import ipaddress
import json
import os
import shutil
import tempfile
import time
from pathlib import Path
from typing import Any, Dict, Optional
from urllib.parse import urlencode, urlparse, urlunparse
import zipfile

import requests

try:
    from security import AgentKeyStore
except ImportError:  # pragma: no cover - package import path for tests
    from .security import AgentKeyStore


_TRANSIENT_REFRESH_STATUS_CODES = frozenset({500, 502, 503, 504})
_REFRESH_RETRY_DELAYS_SECONDS = (1.0, 2.0, 5.0)
_REQUIRED_UPDATE_STATE_ATTRS = (
    "installed_build_id_path",
    "read_installed_build_id",
    "read_repo_build_id",
    "sync_installed_build_id",
    "write_installed_build_id",
    "get_busy_snapshot",
    "read_update_status",
    "write_update_status",
    "read_pending_update",
    "write_pending_update",
    "clear_pending_update",
)
# Preserve live runtime/dependency trees; the platform-specific refresh step restages them safely afterward.
_SYNC_EXCLUDED_TOP_LEVEL = frozenset({"Agent", "Engine", "Dependencies", ".git", "__pycache__", ".pytest_cache"})
_SYNC_EXCLUDED_RELATIVE = frozenset({"Update.ps1", "Update.sh"})


def _settings_dir() -> Path:
    raw_override = (os.environ.get("BOREALIS_AGENT_SETTINGS_DIR") or "").strip()
    if raw_override:
        canonical = Path(raw_override).expanduser()
    else:
        canonical = installed_build_id_path().parent
    _sync_legacy_settings_material(canonical)
    return canonical


def _legacy_settings_roots() -> list[Path]:
    roots: list[Path] = []
    project_root = _resolve_project_root()
    raw_override = (os.environ.get("BOREALIS_AGENT_SETTINGS_DIR") or "").strip()
    if raw_override:
        roots.append(Path(raw_override).expanduser())

    roots.extend(
        [
            project_root / "Agent" / "Settings",
            project_root / "Agent" / "Borealis",
            Path(__file__).resolve().parent / "Settings",
        ]
    )
    unique: list[Path] = []
    seen: set[str] = set()
    for candidate in roots:
        try:
            resolved = candidate.resolve()
        except Exception:
            resolved = candidate
        key = str(resolved)
        if key in seen:
            continue
        seen.add(key)
        unique.append(resolved)
    return unique


def _resolve_project_root() -> Path:
    current = Path(__file__).resolve().parent
    for candidate in [current, *current.parents]:
        if (candidate / "Borealis.ps1").is_file() or (candidate / "Borealis.sh").is_file():
            return candidate

    override = (os.environ.get("BOREALIS_ROOT") or os.environ.get("BOREALIS_PROJECT_ROOT") or "").strip()
    if override:
        return Path(override).expanduser()
    return current


def _load_update_state_module_from_path(module_path: Path):
    module_path = module_path.resolve()
    spec = importlib.util.spec_from_file_location(
        f"_borealis_update_state_runtime_{abs(hash(str(module_path)))}",
        module_path,
    )
    if spec is None or spec.loader is None:
        raise ImportError(f"Unable to load update_state module from {module_path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    missing = [name for name in _REQUIRED_UPDATE_STATE_ATTRS if not hasattr(module, name)]
    if missing:
        raise ImportError(
            "update_state.py is missing required updater APIs: "
            + ", ".join(sorted(missing))
        )
    return module


def _load_update_state_module():
    return _load_update_state_module_from_path(Path(__file__).resolve().with_name("update_state.py"))


_UPDATE_STATE = _load_update_state_module()
installed_build_id_path = _UPDATE_STATE.installed_build_id_path
read_installed_build_id = _UPDATE_STATE.read_installed_build_id
read_repo_build_id = _UPDATE_STATE.read_repo_build_id
sync_installed_build_id = _UPDATE_STATE.sync_installed_build_id
write_installed_build_id = _UPDATE_STATE.write_installed_build_id
get_busy_snapshot = _UPDATE_STATE.get_busy_snapshot
read_update_status = _UPDATE_STATE.read_update_status
write_update_status = _UPDATE_STATE.write_update_status
read_pending_update = _UPDATE_STATE.read_pending_update
write_pending_update = _UPDATE_STATE.write_pending_update
clear_pending_update = _UPDATE_STATE.clear_pending_update


def _copy_if_missing(destination: Path, source: Path, *, text_mode: bool = False) -> bool:
    if destination.exists() or not source.is_file():
        return False
    destination.parent.mkdir(parents=True, exist_ok=True)
    if text_mode:
        destination.write_text(source.read_text(encoding="utf-8", errors="ignore"), encoding="utf-8")
    else:
        destination.write_bytes(source.read_bytes())
    return True


def _sync_legacy_settings_material(destination: Path) -> None:
    try:
        destination.mkdir(parents=True, exist_ok=True)
    except Exception:
        return

    sources = [path for path in _legacy_settings_roots() if path != destination]
    file_specs = (
        ("server_url.txt", ("server_url.txt",), True),
        ("Agent_GUID.txt", ("Agent_GUID.txt", "agent_GUID"), True),
        ("refresh.token", ("refresh.token",), False),
        ("access.jwt", ("access.jwt",), True),
        ("access.meta.json", ("access.meta.json",), True),
    )
    for dest_name, source_names, text_mode in file_specs:
        dest_path = destination / dest_name
        if dest_path.exists():
            continue
        copied = False
        for root in sources:
            for source_name in source_names:
                try:
                    if _copy_if_missing(dest_path, root / source_name, text_mode=text_mode):
                        copied = True
                        break
                except Exception:
                    continue
            if copied:
                break


def _read_server_url() -> str:
    env_value = (os.environ.get("BOREALIS_SERVER_URL") or "").strip()
    if env_value:
        return _normalize_server_url(env_value)

    server_url_path = _settings_dir() / "server_url.txt"
    if server_url_path.is_file():
        try:
            return _normalize_server_url(server_url_path.read_text(encoding="utf-8", errors="ignore"))
        except Exception:
            return ""
    return ""


def _normalize_server_url(raw_value: Any) -> str:
    text = str(raw_value or "").strip()
    if not text:
        return ""
    if "://" not in text:
        text = f"https://{text}"
    try:
        parsed = urlparse(text)
    except Exception:
        return ""

    scheme = (parsed.scheme or "https").lower()
    hostname = (parsed.hostname or "").strip().lower()
    port = parsed.port
    if scheme != "https" or not hostname:
        return ""
    if hostname == "localhost" or _is_literal_ip(hostname):
        return ""

    netloc = hostname
    if parsed.username:
        netloc = parsed.username
        if parsed.password:
            netloc += f":{parsed.password}"
        netloc += f"@{hostname}"
    if port:
        netloc += f":{port}"
    normalized = parsed._replace(scheme="https", netloc=netloc, params="", fragment="")
    return urlunparse(normalized).rstrip("/")


def _keystore() -> AgentKeyStore:
    scope = (os.environ.get("BOREALIS_AGENT_SCOPE") or "SYSTEM").strip() or "SYSTEM"
    return AgentKeyStore(str(_settings_dir()), scope=scope)


def _is_literal_ip(hostname: str) -> bool:
    host = str(hostname or "").strip()
    if not host:
        return False
    try:
        ipaddress.ip_address(host)
    except ValueError:
        return False
    return True


def _build_session(server_url: str) -> requests.Session:
    _ = server_url
    return requests.Session()


def _perform_request(
    session: requests.Session,
    method: str,
    url: str,
    *,
    verify: Any,
    **kwargs,
) -> requests.Response:
    request_kwargs = dict(kwargs)
    request_kwargs["verify"] = verify
    return session.request(method, url, **request_kwargs)


def _refresh_access_token(
    store: AgentKeyStore,
    server_url: str,
    guid: str,
    refresh_token: str,
    verify: Any,
    *,
    session: Optional[requests.Session] = None,
) -> Dict[str, Any]:
    own_session = session is None
    request_session = session or _build_session(server_url)
    try:
        total_attempts = len(_REFRESH_RETRY_DELAYS_SECONDS) + 1
        for attempt in range(1, total_attempts + 1):
            response = _perform_request(
                request_session,
                "post",
                f"{server_url.rstrip('/')}/api/agent/token/refresh",
                verify=verify,
                json={"guid": guid, "refresh_token": refresh_token},
                headers={"User-Agent": "borealis-agent-updater"},
                timeout=60,
            )
            if response.status_code in _TRANSIENT_REFRESH_STATUS_CODES and attempt < total_attempts:
                time.sleep(_REFRESH_RETRY_DELAYS_SECONDS[attempt - 1])
                continue
            response.raise_for_status()
            payload = response.json()
            access_token = str(payload.get("access_token") or "").strip()
            if not access_token:
                raise RuntimeError("refresh response missing access_token")
            expires_in = 900
            try:
                expires_in = int(payload.get("expires_in") or 900)
            except Exception:
                expires_in = 900
            expires_at = int(time.time()) + max(0, expires_in - 5)
            store.save_access_token(access_token, expires_at=expires_at)
            return {"access_token": access_token, "expires_at": expires_at}
        raise RuntimeError("refresh response unavailable after retry")
    finally:
        if own_session:
            request_session.close()


def _get_access_token(
    store: AgentKeyStore,
    server_url: str,
    verify: Any,
    *,
    session: Optional[requests.Session] = None,
) -> str:
    access_token = (store.load_access_token() or "").strip()
    expiry = store.get_access_expiry() or 0
    now = int(time.time())
    if access_token and (expiry <= 0 or expiry > (now + 30)):
        return access_token

    guid = (store.load_guid() or "").strip()
    refresh_token = (store.load_refresh_token() or "").strip()
    if not guid or not refresh_token:
        raise RuntimeError("agent guid or refresh token missing")
    refreshed = _refresh_access_token(
        store,
        server_url,
        guid,
        refresh_token,
        verify,
        session=session,
    )
    return refreshed["access_token"]


def _fetch_repo_hash(force_refresh: bool = False) -> Dict[str, Any]:
    store = _keystore()
    server_url = _read_server_url()
    if not server_url:
        raise RuntimeError("server_url.txt missing or invalid; configure a public HTTPS FQDN")

    verify = True
    params = {
        "repo": os.environ.get("BOREALIS_UPDATE_REPO") or "bunny-lab-io/Borealis",
        "branch": os.environ.get("BOREALIS_UPDATE_BRANCH") or "main",
    }
    if force_refresh:
        params["refresh"] = "1"
    url = f"{server_url.rstrip('/')}/api/repo/current_hash?{urlencode(params)}"

    session = _build_session(server_url)
    try:
        access_token = _get_access_token(store, server_url, verify, session=session)
        headers = {
            "Authorization": f"Bearer {access_token}",
            "User-Agent": "borealis-agent-updater",
        }
        response = _perform_request(
            session,
            "get",
            url,
            verify=verify,
            headers=headers,
            timeout=45,
        )
        if response.status_code == 401:
            guid = (store.load_guid() or "").strip()
            refresh_token = (store.load_refresh_token() or "").strip()
            if not guid or not refresh_token:
                response.raise_for_status()
            refreshed = _refresh_access_token(
                store,
                server_url,
                guid,
                refresh_token,
                verify,
                session=session,
            )
            headers["Authorization"] = f"Bearer {refreshed['access_token']}"
            response = _perform_request(
                session,
                "get",
                url,
                verify=True,
                headers=headers,
                timeout=45,
            )
        response.raise_for_status()
        payload = response.json()
        sha = str(payload.get("sha") or "").strip()
        if not sha:
            raise RuntimeError("repo hash response missing sha")
        return payload
    finally:
        session.close()


def _perform_authed_request(
    method: str,
    path_or_url: str,
    *,
    json_body: Optional[Dict[str, Any]] = None,
    timeout: int = 60,
    stream: bool = False,
) -> requests.Response:
    store = _keystore()
    server_url = _read_server_url()
    if not server_url:
        raise RuntimeError("server_url.txt missing or invalid; configure a public HTTPS FQDN")
    verify = True
    request_url = path_or_url if "://" in str(path_or_url) else f"{server_url.rstrip('/')}/{str(path_or_url).lstrip('/')}"
    session = _build_session(server_url)
    try:
        access_token = _get_access_token(store, server_url, verify, session=session)
        headers = {
            "Authorization": f"Bearer {access_token}",
            "User-Agent": "borealis-agent-updater",
        }
        response = _perform_request(
            session,
            method,
            request_url,
            verify=verify,
            headers=headers,
            json=json_body,
            timeout=timeout,
            stream=stream,
        )
        if response.status_code == 401:
            guid = (store.load_guid() or "").strip()
            refresh_token = (store.load_refresh_token() or "").strip()
            if not guid or not refresh_token:
                response.raise_for_status()
            refreshed = _refresh_access_token(
                store,
                server_url,
                guid,
                refresh_token,
                verify,
                session=session,
            )
            headers["Authorization"] = f"Bearer {refreshed['access_token']}"
            response = _perform_request(
                session,
                method,
                request_url,
                verify=verify,
                headers=headers,
                json=json_body,
                timeout=timeout,
                stream=stream,
            )
        response.raise_for_status()
        return response
    finally:
        session.close()


def _fetch_update_manifest(*, installed_build_id: str = "", hostname: str = "") -> Dict[str, Any]:
    params = {}
    if installed_build_id:
        params["installed_build_id"] = installed_build_id
    if hostname:
        params["hostname"] = hostname
    suffix = ""
    if params:
        suffix = "?" + urlencode(params)
    response = _perform_authed_request("get", f"/api/agent/update/manifest{suffix}", timeout=60)
    payload = response.json()
    if not isinstance(payload, dict):
        raise RuntimeError("update manifest response was not JSON")
    return payload


def _update_status_patch(**fields: Any) -> Dict[str, Any]:
    payload = read_update_status()
    payload.update({key: value for key, value in fields.items()})
    write_update_status(payload)
    return payload


def _normalize_build_id(value: Any) -> str:
    return str(value or "").strip().lower()


def _current_installed_build_id() -> str:
    return _normalize_build_id(read_installed_build_id())


def _pending_payload_from_manifest(manifest: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "artifact_id": str(manifest.get("artifact_id") or "").strip(),
        "effective_channel": str(manifest.get("effective_channel") or manifest.get("target_channel") or "").strip().lower(),
        "target_channel": str(manifest.get("target_channel") or manifest.get("effective_channel") or "").strip().lower(),
        "target_build_id": _normalize_build_id(manifest.get("target_build_id")),
        "artifact_sha256": str(manifest.get("artifact_sha256") or "").strip().lower(),
        "fallback_url": str(manifest.get("fallback_url") or "").strip(),
        "download_path": str(manifest.get("download_path") or "").strip(),
        "repo": str(manifest.get("repo") or "").strip(),
        "requested_at": int(time.time()),
    }


def _download_manifest_artifact(manifest: Dict[str, Any]) -> Dict[str, Any]:
    server_url = _read_server_url()
    if not server_url:
        raise RuntimeError("server_url.txt missing or invalid; configure a public HTTPS FQDN")
    temp_dir = Path(tempfile.mkdtemp(prefix="borealis-update-", dir=str(_settings_dir() / "Updater")))
    archive_path = temp_dir / "agent-update.zip"
    last_error = ""

    download_path = str(manifest.get("download_path") or "").strip()
    if download_path:
        try:
            response = _perform_authed_request("get", download_path, timeout=180)
            archive_path.write_bytes(response.content)
            return {"archive_path": archive_path, "source": "engine"}
        except Exception as exc:
            last_error = str(exc)

    fallback_url = str(manifest.get("fallback_url") or "").strip()
    if fallback_url:
        try:
            response = requests.get(fallback_url, timeout=180)
            response.raise_for_status()
            archive_path.write_bytes(response.content)
            return {"archive_path": archive_path, "source": "github"}
        except Exception as exc:
            if last_error:
                raise RuntimeError(f"engine download failed: {last_error}; github fallback failed: {exc}") from exc
            raise RuntimeError(f"github fallback failed: {exc}") from exc

    if last_error:
        raise RuntimeError(f"engine download failed: {last_error}")
    raise RuntimeError("manifest did not provide any artifact download path")


def _verify_archive_sha256(archive_path: Path, expected_sha256: str) -> None:
    expected = str(expected_sha256 or "").strip().lower()
    if not expected:
        return
    digest = hashlib.sha256()
    with archive_path.open("rb") as handle:
        while True:
            chunk = handle.read(1024 * 1024)
            if not chunk:
                break
            digest.update(chunk)
    actual = digest.hexdigest().lower()
    if actual != expected:
        raise RuntimeError(f"artifact checksum mismatch expected={expected} actual={actual}")


def _resolve_extracted_source_root(extract_root: Path) -> Path:
    children = [path for path in extract_root.iterdir() if path.name != "__MACOSX"]
    if len(children) == 1 and children[0].is_dir():
        return children[0]
    return extract_root


def _validate_engine_managed_source_root(source_root: Path) -> None:
    helper_path = source_root / "Data" / "Agent" / "update_helper.py"
    update_state_path = source_root / "Data" / "Agent" / "update_state.py"
    if not helper_path.is_file() or not update_state_path.is_file():
        raise RuntimeError(
            "target artifact is missing Engine-managed updater files under Data/Agent; "
            "publish a release that includes the new updater or switch this device to unstable"
        )
    try:
        _load_update_state_module_from_path(update_state_path)
    except Exception as exc:
        raise RuntimeError(
            "target artifact predates the Engine-managed updater interface; "
            "publish a new stable release with the updater changes or switch this device to unstable "
            f"(detail: {exc})"
        ) from exc


def _should_exclude_relative(relative_path: str) -> bool:
    normalized = str(relative_path or "").replace("\\", "/").strip("/")
    if not normalized:
        return False
    if normalized in _SYNC_EXCLUDED_RELATIVE:
        return True
    top_level = normalized.split("/", 1)[0]
    return top_level in _SYNC_EXCLUDED_TOP_LEVEL


def _remove_path(path: Path) -> None:
    if not path.exists():
        return
    if path.is_dir() and not path.is_symlink():
        shutil.rmtree(path)
    else:
        path.unlink()


def _copy_tree(src: Path, dst: Path, *, relative_root: Optional[Path] = None) -> None:
    relative_root = relative_root or src
    dst.mkdir(parents=True, exist_ok=True)
    src_entries = {entry.name: entry for entry in src.iterdir()}
    dst_entries = {entry.name: entry for entry in dst.iterdir()}

    for name, existing in dst_entries.items():
        relative_name = str((existing.relative_to(_resolve_project_root())).as_posix()) if existing.exists() else name
        if _should_exclude_relative(relative_name):
            continue
        if name not in src_entries:
            _remove_path(existing)

    for name, source_entry in src_entries.items():
        relative_name = source_entry.relative_to(relative_root).as_posix()
        if _should_exclude_relative(relative_name):
            continue
        destination_entry = dst / name
        if source_entry.is_dir():
            if destination_entry.exists() and not destination_entry.is_dir():
                _remove_path(destination_entry)
            _copy_tree(source_entry, destination_entry, relative_root=relative_root)
            continue
        destination_entry.parent.mkdir(parents=True, exist_ok=True)
        if destination_entry.exists() and destination_entry.is_dir():
            _remove_path(destination_entry)
        shutil.copy2(source_entry, destination_entry)


def _apply_source_archive(archive_path: Path) -> None:
    project_root = _resolve_project_root()
    with tempfile.TemporaryDirectory(prefix="borealis-stage-", dir=str(_settings_dir() / "Updater")) as temp_dir:
        extract_root = Path(temp_dir) / "extract"
        extract_root.mkdir(parents=True, exist_ok=True)
        with zipfile.ZipFile(archive_path) as archive:
            archive.extractall(extract_root)
        source_root = _resolve_extracted_source_root(extract_root)
        _validate_engine_managed_source_root(source_root)
        _copy_tree(source_root, project_root, relative_root=source_root)


def prepare_update(*, force: bool = False) -> Dict[str, Any]:
    installed_build_id = _current_installed_build_id()
    _update_status_patch(
        state="checking",
        last_checked_at=int(time.time()),
        last_error="",
        target_build_id="",
        target_channel="",
        effective_channel="",
        last_source="",
    )
    manifest = _fetch_update_manifest(installed_build_id=installed_build_id)
    target_build_id = _normalize_build_id(manifest.get("target_build_id"))
    target_channel = str(manifest.get("target_channel") or "").strip().lower()
    effective_channel = str(manifest.get("effective_channel") or target_channel).strip().lower()
    if not target_build_id:
        raise RuntimeError("update manifest missing target_build_id")

    if not force and installed_build_id and installed_build_id == target_build_id:
        clear_pending_update()
        _update_status_patch(
            state="up_to_date",
            last_checked_at=int(time.time()),
            target_build_id=target_build_id,
            target_channel=target_channel,
            effective_channel=effective_channel,
            last_error="",
            update_available=False,
        )
        return {
            "status": "up_to_date",
            "target_build_id": target_build_id,
            "target_channel": target_channel,
            "effective_channel": effective_channel,
        }

    pending_payload = _pending_payload_from_manifest(manifest)
    write_pending_update(pending_payload)
    snapshot = get_busy_snapshot()
    if snapshot.get("busy"):
        reasons = snapshot.get("reasons") if isinstance(snapshot.get("reasons"), list) else []
        _update_status_patch(
            state="deferred",
            last_checked_at=int(time.time()),
            target_build_id=target_build_id,
            target_channel=target_channel,
            effective_channel=effective_channel,
            last_error="",
            update_available=True,
        )
        return {
            "status": "deferred",
            "target_build_id": target_build_id,
            "target_channel": target_channel,
            "effective_channel": effective_channel,
            "reasons": [str(item).strip() for item in reasons if str(item).strip()],
        }

    _update_status_patch(
        state="downloading",
        last_checked_at=int(time.time()),
        target_build_id=target_build_id,
        target_channel=target_channel,
        effective_channel=effective_channel,
        last_error="",
        update_available=True,
    )
    download = _download_manifest_artifact(manifest)
    archive_path = download["archive_path"]
    last_source = str(download.get("source") or "").strip()
    try:
        _verify_archive_sha256(archive_path, str(manifest.get("artifact_sha256") or ""))
        _update_status_patch(state="staging", last_source=last_source)
        _apply_source_archive(archive_path)
    except Exception as exc:
        _update_status_patch(
            state="failed",
            last_checked_at=int(time.time()),
            target_build_id=target_build_id,
            target_channel=target_channel,
            effective_channel=effective_channel,
            last_source=last_source,
            last_error=str(exc),
            update_available=True,
        )
        raise
    finally:
        try:
            shutil.rmtree(archive_path.parent)
        except Exception:
            pass

    _update_status_patch(
        state="staged",
        last_checked_at=int(time.time()),
        target_build_id=target_build_id,
        target_channel=target_channel,
        effective_channel=effective_channel,
        last_source=last_source,
        last_error="",
        update_available=True,
    )
    return {
        "status": "staged",
        "target_build_id": target_build_id,
        "target_channel": target_channel,
        "effective_channel": effective_channel,
        "last_source": last_source,
        "artifact_id": str(manifest.get("artifact_id") or "").strip(),
    }


def finalize_update(*, build_id: str, channel: str = "", source: str = "") -> Dict[str, Any]:
    normalized_build_id = _normalize_build_id(build_id)
    if not normalized_build_id:
        raise RuntimeError("build_id is required")
    write_installed_build_id(normalized_build_id)
    clear_pending_update()
    status = _update_status_patch(
        state="applied",
        last_checked_at=int(time.time()),
        target_build_id=normalized_build_id,
        target_channel=str(channel or "").strip().lower(),
        effective_channel=str(channel or "").strip().lower(),
        last_source=str(source or "").strip(),
        last_error="",
        update_available=False,
    )
    return {"status": "applied", "installed_build_id": normalized_build_id, "update_status": status}


def _cmd_status() -> int:
    snapshot = get_busy_snapshot()
    snapshot["server_url"] = _read_server_url()
    snapshot["installed_build_id"] = read_installed_build_id()
    snapshot["repo_build_id"] = read_repo_build_id()
    print(json.dumps(snapshot, sort_keys=True))
    return 0


def _cmd_repo_hash(force_refresh: bool) -> int:
    payload = _fetch_repo_hash(force_refresh=force_refresh)
    print(json.dumps(payload, sort_keys=True))
    return 0


def _cmd_sync_build_id() -> int:
    build_id = sync_installed_build_id()
    if not build_id:
        return 1
    print(build_id)
    return 0


def _cmd_manifest() -> int:
    payload = _fetch_update_manifest(installed_build_id=_current_installed_build_id())
    print(json.dumps(payload, sort_keys=True))
    return 0


def _cmd_prepare_update(force: bool) -> int:
    payload = prepare_update(force=force)
    print(json.dumps(payload, sort_keys=True))
    return 0


def _cmd_finalize_update(build_id: str, channel: str, source: str) -> int:
    payload = finalize_update(build_id=build_id, channel=channel, source=source)
    print(json.dumps(payload, sort_keys=True))
    return 0


def _cmd_clear_pending() -> int:
    clear_pending_update()
    print(json.dumps({"status": "cleared"}, sort_keys=True))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="Borealis updater helper")
    subparsers = parser.add_subparsers(dest="command")
    subparsers.required = True

    subparsers.add_parser("status", help="Print busy/build status JSON")
    repo_hash_parser = subparsers.add_parser("repo-hash", help="Fetch /api/repo/current_hash with agent auth")
    repo_hash_parser.add_argument("--refresh", action="store_true", help="Force a cache refresh on the Engine")
    subparsers.add_parser("sync-build-id", help="Persist the current repo build id to installed_build_id.txt")
    subparsers.add_parser("manifest", help="Fetch the current Engine-managed update manifest")
    prepare_parser = subparsers.add_parser("prepare-update", help="Download and stage the current Engine-managed update")
    prepare_parser.add_argument("--force", action="store_true", help="Stage the target even if installed_build_id already matches")
    finalize_parser = subparsers.add_parser("finalize-update", help="Persist the installed build id after a successful runtime refresh")
    finalize_parser.add_argument("--build-id", required=True, help="Applied build id")
    finalize_parser.add_argument("--channel", default="", help="Applied release channel")
    finalize_parser.add_argument("--source", default="", help="Artifact download source")
    subparsers.add_parser("clear-pending", help="Clear any persisted pending target")

    args = parser.parse_args()
    if args.command == "status":
        return _cmd_status()
    if args.command == "repo-hash":
        return _cmd_repo_hash(bool(args.refresh))
    if args.command == "sync-build-id":
        return _cmd_sync_build_id()
    if args.command == "manifest":
        return _cmd_manifest()
    if args.command == "prepare-update":
        return _cmd_prepare_update(bool(args.force))
    if args.command == "finalize-update":
        return _cmd_finalize_update(str(args.build_id or ""), str(args.channel or ""), str(args.source or ""))
    if args.command == "clear-pending":
        return _cmd_clear_pending()
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
