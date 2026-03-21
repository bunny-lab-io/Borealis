from __future__ import annotations

import argparse
import json
import os
import time
from pathlib import Path
from typing import Any, Dict, Optional
from urllib.parse import urlencode, urlparse, urlunparse

import requests

from security import AgentKeyStore
from update_state import get_busy_snapshot, installed_build_id_path, read_installed_build_id, read_repo_build_id, sync_installed_build_id


def _settings_dir() -> Path:
    return installed_build_id_path().parent


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
        return text.rstrip("/")

    scheme = parsed.scheme or "https"
    hostname = parsed.hostname or ""
    port = parsed.port
    allow_plaintext = (os.environ.get("BOREALIS_ALLOW_PLAINTEXT") or "").strip().lower() in {"1", "true", "yes", "on"}
    if scheme == "http" and not allow_plaintext and hostname.lower() in {"localhost", "127.0.0.1", "::1"}:
        scheme = "https"
        if port in {None, 80}:
            port = 5000

    netloc = hostname
    if parsed.username:
        netloc = parsed.username
        if parsed.password:
            netloc += f":{parsed.password}"
        netloc += f"@{hostname}"
    if port:
        netloc += f":{port}"
    normalized = parsed._replace(scheme=scheme, netloc=netloc)
    return urlunparse(normalized).rstrip("/")


def _keystore() -> AgentKeyStore:
    scope = (os.environ.get("BOREALIS_AGENT_SCOPE") or "SYSTEM").strip() or "SYSTEM"
    return AgentKeyStore(str(_settings_dir()), scope=scope)


def _verify_bundle_path(store: AgentKeyStore) -> Any:
    try:
        candidate = Path(store.server_certificate_path())
        if candidate.is_file():
            return str(candidate)
    except Exception:
        pass
    return True


def _refresh_access_token(store: AgentKeyStore, server_url: str, guid: str, refresh_token: str, verify: Any) -> Dict[str, Any]:
    response = requests.post(
        f"{server_url.rstrip('/')}/api/agent/token/refresh",
        json={"guid": guid, "refresh_token": refresh_token},
        headers={"User-Agent": "borealis-agent-updater"},
        timeout=60,
        verify=verify,
    )
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


def _get_access_token(store: AgentKeyStore, server_url: str, verify: Any) -> str:
    access_token = (store.load_access_token() or "").strip()
    expiry = store.get_access_expiry() or 0
    now = int(time.time())
    if access_token and (expiry <= 0 or expiry > (now + 30)):
        return access_token

    guid = (store.load_guid() or "").strip()
    refresh_token = (store.load_refresh_token() or "").strip()
    if not guid or not refresh_token:
        raise RuntimeError("agent guid or refresh token missing")
    refreshed = _refresh_access_token(store, server_url, guid, refresh_token, verify)
    return refreshed["access_token"]


def _fetch_repo_hash(force_refresh: bool = False) -> Dict[str, Any]:
    store = _keystore()
    server_url = _read_server_url()
    if not server_url:
        raise RuntimeError("server_url.txt missing or empty")

    verify = _verify_bundle_path(store)
    params = {
        "repo": os.environ.get("BOREALIS_UPDATE_REPO") or "bunny-lab-io/Borealis",
        "branch": os.environ.get("BOREALIS_UPDATE_BRANCH") or "main",
    }
    if force_refresh:
        params["refresh"] = "1"
    url = f"{server_url.rstrip('/')}/api/repo/current_hash?{urlencode(params)}"

    access_token = _get_access_token(store, server_url, verify)
    headers = {
        "Authorization": f"Bearer {access_token}",
        "User-Agent": "borealis-agent-updater",
    }
    response = requests.get(url, headers=headers, timeout=45, verify=verify)
    if response.status_code == 401:
        guid = (store.load_guid() or "").strip()
        refresh_token = (store.load_refresh_token() or "").strip()
        if not guid or not refresh_token:
            response.raise_for_status()
        refreshed = _refresh_access_token(store, server_url, guid, refresh_token, verify)
        headers["Authorization"] = f"Bearer {refreshed['access_token']}"
        response = requests.get(url, headers=headers, timeout=45, verify=verify)
    response.raise_for_status()
    payload = response.json()
    sha = str(payload.get("sha") or "").strip()
    if not sha:
        raise RuntimeError("repo hash response missing sha")
    return payload


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
