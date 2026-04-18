from __future__ import annotations

import argparse
import ipaddress
import json
import os
import time
from pathlib import Path
from typing import Any, Dict, Optional
from urllib.parse import urlencode, urlparse, urlunparse

import requests

try:
    from security import AgentKeyStore
    from update_state import get_busy_snapshot, installed_build_id_path, read_installed_build_id, read_repo_build_id, sync_installed_build_id
except ImportError:  # pragma: no cover - package import path for tests
    from .security import AgentKeyStore
    from .update_state import (
        get_busy_snapshot,
        installed_build_id_path,
        read_installed_build_id,
        read_repo_build_id,
        sync_installed_build_id,
    )


_TRANSIENT_REFRESH_STATUS_CODES = frozenset({500, 502, 503, 504})
_REFRESH_RETRY_DELAYS_SECONDS = (1.0, 2.0, 5.0)


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


def main() -> int:
    parser = argparse.ArgumentParser(description="Borealis updater helper")
    subparsers = parser.add_subparsers(dest="command")
    subparsers.required = True

    subparsers.add_parser("status", help="Print busy/build status JSON")
    repo_hash_parser = subparsers.add_parser("repo-hash", help="Fetch /api/repo/current_hash with agent auth")
    repo_hash_parser.add_argument("--refresh", action="store_true", help="Force a cache refresh on the Engine")
    subparsers.add_parser("sync-build-id", help="Persist the current repo build id to installed_build_id.txt")

    args = parser.parse_args()
    if args.command == "status":
        return _cmd_status()
    if args.command == "repo-hash":
        return _cmd_repo_hash(bool(args.refresh))
    if args.command == "sync-build-id":
        return _cmd_sync_build_id()
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
