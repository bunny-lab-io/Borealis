
"""Device management endpoints for the Borealis Engine API."""
from __future__ import annotations

import json
import logging
import os
import sqlite3
import threading
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, List, Optional, Tuple

from flask import Blueprint, jsonify, request, session, g
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from Modules.auth.device_auth import require_device_auth
from Modules.guid_utils import normalize_guid

try:
    import requests  # type: ignore
except ImportError:  # pragma: no cover - fallback for minimal test environments
    class _RequestsStub:
        class RequestException(RuntimeError):
            """Stand-in exception when the requests module is unavailable."""

        def get(self, *args: Any, **kwargs: Any) -> Any:
            raise self.RequestException("The 'requests' library is required for repository hash lookups.")

    requests = _RequestsStub()  # type: ignore

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import LegacyServiceAdapters


def _safe_json(raw: Optional[str], default: Any) -> Any:
    if raw is None:
        return json.loads(json.dumps(default)) if isinstance(default, (list, dict)) else default
    try:
        parsed = json.loads(raw)
    except Exception:
        return default
    if isinstance(default, list) and isinstance(parsed, list):
        return parsed
    if isinstance(default, dict) and isinstance(parsed, dict):
        return parsed
    return default


def _ts_to_iso(ts: Optional[int]) -> str:
    if not ts:
        return ""
    try:
        from datetime import datetime, timezone

        return datetime.fromtimestamp(int(ts), timezone.utc).isoformat()
    except Exception:
        return ""


def _status_from_last_seen(last_seen: Optional[int]) -> str:
    if not last_seen:
        return "Offline"
    try:
        if (time.time() - float(last_seen)) <= 300:
            return "Online"
    except Exception:
        pass
    return "Offline"


def _is_internal_request(remote_addr: Optional[str]) -> bool:
    addr = (remote_addr or "").strip()
    if not addr:
        return False
    if addr in {"127.0.0.1", "::1"}:
        return True
    if addr.startswith("127."):
        return True
    if addr.startswith("::ffff:"):
        mapped = addr.split("::ffff:", 1)[-1]
        if mapped in {"127.0.0.1"} or mapped.startswith("127."):
            return True
    return False


def _row_to_site(row: Tuple[Any, ...]) -> Dict[str, Any]:
    return {
        "id": row[0],
        "name": row[1],
        "description": row[2] or "",
        "created_at": row[3] or 0,
        "device_count": row[4] or 0,
    }


DEVICE_TABLE = "devices"
_DEVICE_JSON_LIST_FIELDS: Dict[str, Any] = {
    "memory": [],
    "network": [],
    "software": [],
    "storage": [],
}
_DEVICE_JSON_OBJECT_FIELDS: Dict[str, Any] = {"cpu": {}}


def _is_empty(value: Any) -> bool:
    return value is None or value == "" or value == [] or value == {}


def _deep_merge_preserve(prev: Dict[str, Any], incoming: Dict[str, Any]) -> Dict[str, Any]:
    out: Dict[str, Any] = dict(prev or {})
    for key, value in (incoming or {}).items():
        if isinstance(value, dict):
            out[key] = _deep_merge_preserve(out.get(key) or {}, value)
        elif isinstance(value, list):
            if value:
                out[key] = value
        else:
            if not _is_empty(value):
                out[key] = value
    return out


def _serialize_device_json(value: Any, default: Any) -> str:
    candidate = value
    if candidate is None:
        candidate = default
    if not isinstance(candidate, (list, dict)):
        candidate = default
    try:
        return json.dumps(candidate)
    except Exception:
        try:
            return json.dumps(default)
        except Exception:
            return "{}" if isinstance(default, dict) else "[]"


def _clean_device_str(value: Any) -> Optional[str]:
    if value is None:
        return None
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        text = str(value)
    elif isinstance(value, str):
        text = value
    else:
        try:
            text = str(value)
        except Exception:
            return None
    text = text.strip()
    return text or None


def _coerce_int(value: Any) -> Optional[int]:
    if value is None:
        return None
    try:
        if isinstance(value, str) and value.strip() == "":
            return None
        return int(float(value))
    except (ValueError, TypeError):
        return None


def _extract_device_columns(details: Dict[str, Any]) -> Dict[str, Any]:
    summary = details.get("summary") or {}
    payload: Dict[str, Any] = {}

    for field, default in _DEVICE_JSON_LIST_FIELDS.items():
        payload[field] = _serialize_device_json(details.get(field), default)
    payload["cpu"] = _serialize_device_json(summary.get("cpu") or details.get("cpu"), _DEVICE_JSON_OBJECT_FIELDS["cpu"])

    payload["device_type"] = _clean_device_str(summary.get("device_type") or summary.get("type"))
    payload["domain"] = _clean_device_str(summary.get("domain"))
    payload["external_ip"] = _clean_device_str(summary.get("external_ip") or summary.get("public_ip"))
    payload["internal_ip"] = _clean_device_str(summary.get("internal_ip") or summary.get("private_ip"))
    payload["last_reboot"] = _clean_device_str(summary.get("last_reboot") or summary.get("last_boot"))
    payload["last_seen"] = _coerce_int(summary.get("last_seen"))
    payload["last_user"] = _clean_device_str(
        summary.get("last_user") or summary.get("last_user_name") or summary.get("username")
    )
    payload["operating_system"] = _clean_device_str(
        summary.get("operating_system") or summary.get("agent_operating_system") or summary.get("os")
    )
    uptime_value = summary.get("uptime_sec") or summary.get("uptime_seconds") or summary.get("uptime")
    payload["uptime"] = _coerce_int(uptime_value)
    payload["agent_id"] = _clean_device_str(summary.get("agent_id"))
    payload["ansible_ee_ver"] = _clean_device_str(summary.get("ansible_ee_ver"))
    payload["connection_type"] = _clean_device_str(summary.get("connection_type") or summary.get("remote_type"))
    payload["connection_endpoint"] = _clean_device_str(
        summary.get("connection_endpoint")
        or summary.get("connection_address")
        or summary.get("address")
        or summary.get("external_ip")
        or summary.get("internal_ip")
    )
    return payload


def _device_upsert(
    cur: sqlite3.Cursor,
    hostname: str,
    description: Optional[str],
    merged_details: Dict[str, Any],
    created_at: Optional[int],
    *,
    agent_hash: Optional[str] = None,
    guid: Optional[str] = None,
) -> None:
    if not hostname:
        return
    column_values = _extract_device_columns(merged_details or {})

    normalized_description = description if description is not None else ""
    try:
        normalized_description = str(normalized_description)
    except Exception:
        normalized_description = ""

    normalized_hash = _clean_device_str(agent_hash) or None
    normalized_guid = _clean_device_str(guid) or None
    if normalized_guid:
        try:
            normalized_guid = normalize_guid(normalized_guid)
        except Exception:
            pass

    created_ts = _coerce_int(created_at)
    if not created_ts:
        created_ts = int(time.time())

    sql = f"""
        INSERT INTO {DEVICE_TABLE}(
            hostname,
            description,
            created_at,
            agent_hash,
            guid,
            memory,
            network,
            software,
            storage,
            cpu,
            device_type,
            domain,
            external_ip,
            internal_ip,
            last_reboot,
            last_seen,
            last_user,
            operating_system,
            uptime,
            agent_id,
            ansible_ee_ver,
            connection_type,
            connection_endpoint
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(hostname) DO UPDATE SET
            description=excluded.description,
            created_at=COALESCE({DEVICE_TABLE}.created_at, excluded.created_at),
            agent_hash=COALESCE(NULLIF(excluded.agent_hash, ''), {DEVICE_TABLE}.agent_hash),
            guid=COALESCE(NULLIF(excluded.guid, ''), {DEVICE_TABLE}.guid),
            memory=excluded.memory,
            network=excluded.network,
            software=excluded.software,
            storage=excluded.storage,
            cpu=excluded.cpu,
            device_type=COALESCE(NULLIF(excluded.device_type, ''), {DEVICE_TABLE}.device_type),
            domain=COALESCE(NULLIF(excluded.domain, ''), {DEVICE_TABLE}.domain),
            external_ip=COALESCE(NULLIF(excluded.external_ip, ''), {DEVICE_TABLE}.external_ip),
            internal_ip=COALESCE(NULLIF(excluded.internal_ip, ''), {DEVICE_TABLE}.internal_ip),
            last_reboot=COALESCE(NULLIF(excluded.last_reboot, ''), {DEVICE_TABLE}.last_reboot),
            last_seen=COALESCE(NULLIF(excluded.last_seen, 0), {DEVICE_TABLE}.last_seen),
            last_user=COALESCE(NULLIF(excluded.last_user, ''), {DEVICE_TABLE}.last_user),
            operating_system=COALESCE(NULLIF(excluded.operating_system, ''), {DEVICE_TABLE}.operating_system),
            uptime=COALESCE(NULLIF(excluded.uptime, 0), {DEVICE_TABLE}.uptime),
            agent_id=COALESCE(NULLIF(excluded.agent_id, ''), {DEVICE_TABLE}.agent_id),
            ansible_ee_ver=COALESCE(NULLIF(excluded.ansible_ee_ver, ''), {DEVICE_TABLE}.ansible_ee_ver),
            connection_type=COALESCE(NULLIF(excluded.connection_type, ''), {DEVICE_TABLE}.connection_type),
            connection_endpoint=COALESCE(NULLIF(excluded.connection_endpoint, ''), {DEVICE_TABLE}.connection_endpoint)
    """

    params: List[Any] = [
        hostname,
        normalized_description,
        created_ts,
        normalized_hash,
        normalized_guid,
        column_values.get("memory"),
        column_values.get("network"),
        column_values.get("software"),
        column_values.get("storage"),
        column_values.get("cpu"),
        column_values.get("device_type"),
        column_values.get("domain"),
        column_values.get("external_ip"),
        column_values.get("internal_ip"),
        column_values.get("last_reboot"),
        column_values.get("last_seen"),
        column_values.get("last_user"),
        column_values.get("operating_system"),
        column_values.get("uptime"),
        column_values.get("agent_id"),
        column_values.get("ansible_ee_ver"),
        column_values.get("connection_type"),
        column_values.get("connection_endpoint"),
    ]
    cur.execute(sql, params)


class RepositoryHashCache:
    """Lightweight GitHub head cache with on-disk persistence."""

    def __init__(self, adapters: "LegacyServiceAdapters") -> None:
        self._db_conn_factory = adapters.db_conn_factory
        self._service_log = adapters.service_log
        self._logger = adapters.context.logger
        config = adapters.context.config or {}
        default_root = Path(adapters.context.database_path).resolve().parent / "cache"
        cache_root = Path(config.get("cache_dir") or default_root)
        cache_root.mkdir(parents=True, exist_ok=True)
        self._cache_file = cache_root / "repo_hash_cache.json"
        self._cache: Dict[Tuple[str, str], Tuple[str, float]] = {}
        self._lock = threading.Lock()
        self._load_cache()

    def _load_cache(self) -> None:
        try:
            if not self._cache_file.is_file():
                return
            data = json.loads(self._cache_file.read_text(encoding="utf-8"))
            entries = data.get("entries") or {}
            for key, payload in entries.items():
                sha = payload.get("sha")
                ts = payload.get("ts")
                if not sha or ts is None:
                    continue
                repo, _, branch = key.partition(":")
                if not repo or not branch:
                    continue
                self._cache[(repo, branch)] = (str(sha), float(ts))
        except Exception:
            self._logger.debug("Failed to hydrate repository hash cache", exc_info=True)

    def _persist_cache(self) -> None:
        try:
            snapshot = {
                f"{repo}:{branch}": {"sha": sha, "ts": ts}
                for (repo, branch), (sha, ts) in self._cache.items()
                if sha
            }
            payload = {"version": 1, "entries": snapshot}
            tmp_path = self._cache_file.with_suffix(".tmp")
            tmp_path.write_text(json.dumps(payload), encoding="utf-8")
            tmp_path.replace(self._cache_file)
        except Exception:
            self._logger.debug("Failed to persist repository hash cache", exc_info=True)

    def _github_token(self, *, force_refresh: bool = False) -> Optional[str]:
        env_token = (request.headers.get("X-GitHub-Token") or "").strip()
        if env_token:
            return env_token
        token = None
        if not force_refresh:
            token = request.headers.get("Authorization")
            if token and token.lower().startswith("bearer "):
                return token.split(" ", 1)[1].strip()
        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn_factory()
            cur = conn.cursor()
            cur.execute("SELECT token FROM github_token LIMIT 1")
            row = cur.fetchone()
            if row and row[0]:
                candidate = str(row[0]).strip()
                if candidate:
                    token = candidate
        except sqlite3.Error:
            token = None
        except Exception as exc:
            self._service_log("server", f"github token lookup failed: {exc}")
            token = None
        finally:
            if conn:
                conn.close()
        if token:
            return token
        fallback = os.environ.get("BOREALIS_GITHUB_TOKEN") or os.environ.get("GITHUB_TOKEN")
        return fallback.strip() if fallback else None

    def resolve(
        self,
        repo: str,
        branch: str,
        *,
        ttl: int = 60,
        force_refresh: bool = False,
    ) -> Tuple[Dict[str, Any], int]:
        ttl = max(30, min(int(ttl or 60), 3600))
        key = (repo, branch)
        now = time.time()
        with self._lock:
            cached = self._cache.get(key)
        if cached and not force_refresh:
            sha, ts = cached
            if sha and (now - ts) < ttl:
                return (
                    {
                        "repo": repo,
                        "branch": branch,
                        "sha": sha,
                        "cached": True,
                        "age_seconds": now - ts,
                        "source": "cache",
                    },
                    200,
                )

        headers = {
            "Accept": "application/vnd.github+json",
            "User-Agent": "Borealis-Engine",
        }
        token = self._github_token(force_refresh=force_refresh)
        if token:
            headers["Authorization"] = f"Bearer {token}"

        sha: Optional[str] = None
        error: Optional[str] = None
        try:
            resp = requests.get(
                f"https://api.github.com/repos/{repo}/branches/{branch}",
                headers=headers,
                timeout=20,
            )
            if resp.status_code == 200:
                data = resp.json()
                sha = ((data.get("commit") or {}).get("sha") or "").strip()
            else:
                error = f"GitHub head lookup failed: HTTP {resp.status_code}"
        except requests.RequestException as exc:
            error = f"GitHub head lookup raised: {exc}"

        if sha:
            with self._lock:
                self._cache[key] = (sha, now)
                self._persist_cache()
            return (
                {
                    "repo": repo,
                    "branch": branch,
                    "sha": sha,
                    "cached": False,
                    "age_seconds": 0.0,
                    "source": "github",
                },
                200,
            )

        if error:
            self._service_log("server", f"/api/repo/current_hash error: {error}")

        if cached:
            cached_sha, ts = cached
            return (
                {
                    "repo": repo,
                    "branch": branch,
                    "sha": cached_sha or None,
                    "cached": True,
                    "age_seconds": now - ts,
                    "error": error or "using cached value",
                    "source": "cache-stale",
                },
                200 if cached_sha else 503,
            )

        return (
            {
                "repo": repo,
                "branch": branch,
                "sha": None,
                "cached": False,
                "age_seconds": None,
                "error": error or "unable to resolve repository head",
                "source": "github",
            },
            503,
        )


class DeviceManagementService:
    """Encapsulates database access for device-focused API routes."""

    _DEVICE_COLUMNS: Tuple[str, ...] = (
        "guid",
        "hostname",
        "description",
        "created_at",
        "agent_hash",
        "memory",
        "network",
        "software",
        "storage",
        "cpu",
        "device_type",
        "domain",
        "external_ip",
        "internal_ip",
        "last_reboot",
        "last_seen",
        "last_user",
        "operating_system",
        "uptime",
        "agent_id",
        "ansible_ee_ver",
        "connection_type",
        "connection_endpoint",
    )

    def __init__(self, app, adapters: "LegacyServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.db_conn_factory = adapters.db_conn_factory
        self.service_log = adapters.service_log
        self.logger = adapters.context.logger or logging.getLogger(__name__)
        self.repo_cache = RepositoryHashCache(adapters)

    def _db_conn(self) -> sqlite3.Connection:
        return self.db_conn_factory()

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = self.app.secret_key or "borealis-dev-secret"
        return URLSafeTimedSerializer(secret, salt="borealis-auth")

    def _current_user(self) -> Optional[Dict[str, str]]:
        username = session.get("username")
        role = session.get("role") or "User"
        if username:
            return {"username": username, "role": role}
        token = None
        auth_header = request.headers.get("Authorization") or ""
        if auth_header.lower().startswith("bearer "):
            token = auth_header.split(" ", 1)[1].strip()
        if not token:
            token = request.cookies.get("borealis_auth")
        if not token:
            return None
        try:
            data = self._token_serializer().loads(
                token,
                max_age=int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30)),
            )
            username = data.get("u")
            role = data.get("r") or "User"
            if username:
                return {"username": username, "role": role}
        except (BadSignature, SignatureExpired, Exception):
            return None
        return None

    def _require_login(self) -> Optional[Tuple[Dict[str, Any], int]]:
        if not self._current_user():
            return {"error": "unauthorized"}, 401
        return None

    def _require_admin(self) -> Optional[Tuple[Dict[str, Any], int]]:
        user = self._current_user()
        if not user:
            return {"error": "unauthorized"}, 401
        if (user.get("role") or "").lower() != "admin":
            return {"error": "forbidden"}, 403
        return None

    def _build_device_payload(
        self,
        row: Tuple[Any, ...],
        site_row: Tuple[Optional[int], Optional[str], Optional[str]],
    ) -> Dict[str, Any]:
        mapping = dict(zip(self._DEVICE_COLUMNS, row))
        created_at = mapping.get("created_at") or 0
        last_seen = mapping.get("last_seen") or 0
        summary = {
            "hostname": mapping.get("hostname") or "",
            "description": mapping.get("description") or "",
            "agent_hash": (mapping.get("agent_hash") or "").strip(),
            "agent_guid": normalize_guid(mapping.get("guid")) or "",
            "agent_id": (mapping.get("agent_id") or "").strip(),
            "device_type": mapping.get("device_type") or "",
            "domain": mapping.get("domain") or "",
            "external_ip": mapping.get("external_ip") or "",
            "internal_ip": mapping.get("internal_ip") or "",
            "last_reboot": mapping.get("last_reboot") or "",
            "last_seen": last_seen or 0,
            "last_user": mapping.get("last_user") or "",
            "operating_system": mapping.get("operating_system") or "",
            "uptime": mapping.get("uptime") or 0,
            "created_at": created_at or 0,
            "connection_type": mapping.get("connection_type") or "",
            "connection_endpoint": mapping.get("connection_endpoint") or "",
            "ansible_ee_ver": mapping.get("ansible_ee_ver") or "",
        }
        details = {
            "summary": summary,
            "memory": _safe_json(mapping.get("memory"), []),
            "network": _safe_json(mapping.get("network"), []),
            "software": _safe_json(mapping.get("software"), []),
            "storage": _safe_json(mapping.get("storage"), []),
            "cpu": _safe_json(mapping.get("cpu"), {}),
        }
        site_id, site_name, site_description = site_row
        payload = {
            "hostname": summary["hostname"],
            "description": summary["description"],
            "details": details,
            "summary": summary,
            "created_at": created_at or 0,
            "created_at_iso": _ts_to_iso(created_at),
            "agent_hash": summary["agent_hash"],
            "agent_guid": summary["agent_guid"],
            "guid": summary["agent_guid"],
            "memory": details["memory"],
            "network": details["network"],
            "software": details["software"],
            "storage": details["storage"],
            "cpu": details["cpu"],
            "device_type": summary["device_type"],
            "domain": summary["domain"],
            "external_ip": summary["external_ip"],
            "internal_ip": summary["internal_ip"],
            "last_reboot": summary["last_reboot"],
            "last_seen": last_seen or 0,
            "last_seen_iso": _ts_to_iso(last_seen),
            "last_user": summary["last_user"],
            "operating_system": summary["operating_system"],
            "uptime": summary["uptime"],
            "agent_id": summary["agent_id"],
            "connection_type": summary["connection_type"],
            "connection_endpoint": summary["connection_endpoint"],
            "site_id": site_id,
            "site_name": site_name or "",
            "site_description": site_description or "",
            "status": _status_from_last_seen(last_seen or 0),
        }
        return payload

    def _fetch_devices(
        self,
        *,
        connection_type: Optional[str] = None,
        hostname: Optional[str] = None,
        only_agents: bool = False,
    ) -> List[Dict[str, Any]]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            columns_sql = ", ".join(f"d.{col}" for col in self._DEVICE_COLUMNS)
            sql = f"""
                SELECT {columns_sql}, s.id, s.name, s.description
                  FROM devices AS d
             LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
             LEFT JOIN sites AS s ON s.id = ds.site_id
            """
            clauses: List[str] = []
            params: List[Any] = []
            if connection_type:
                clauses.append("LOWER(d.connection_type) = LOWER(?)")
                params.append(connection_type)
            if hostname:
                clauses.append("LOWER(d.hostname) = LOWER(?)")
                params.append(hostname.lower())
            if only_agents:
                clauses.append("(d.connection_type IS NULL OR TRIM(d.connection_type) = '')")
            if clauses:
                sql += " WHERE " + " AND ".join(clauses)
            cur.execute(sql, params)
            rows = cur.fetchall()
            devices: List[Dict[str, Any]] = []
            for row in rows:
                device_tuple = row[: len(self._DEVICE_COLUMNS)]
                site_tuple = row[len(self._DEVICE_COLUMNS):]
                devices.append(self._build_device_payload(device_tuple, site_tuple))
            return devices
        finally:
            conn.close()
    def list_devices(self) -> Tuple[Dict[str, Any], int]:
        try:
            only_agents = request.args.get("only_agents") in {"1", "true", "yes"}
            devices = self._fetch_devices(
                connection_type=request.args.get("connection_type"),
                hostname=request.args.get("hostname"),
                only_agents=only_agents,
            )
            return {"devices": devices}, 200
        except Exception as exc:
            self.logger.debug("Failed to list devices", exc_info=True)
            return {"error": str(exc)}, 500

    def get_device_by_guid(self, guid: str) -> Tuple[Dict[str, Any], int]:
        normalized_guid = normalize_guid(guid)
        if not normalized_guid:
            return {"error": "invalid guid"}, 400
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            columns_sql = ", ".join(f"d.{col}" for col in self._DEVICE_COLUMNS)
            cur.execute(
                f"""
                SELECT {columns_sql}, s.id, s.name, s.description
                  FROM devices AS d
             LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
             LEFT JOIN sites AS s ON s.id = ds.site_id
                 WHERE LOWER(d.guid) = ?
                """,
                (normalized_guid.lower(),),
            )
            row = cur.fetchone()
            if not row:
                return {"error": "not found"}, 404
            device_tuple = row[: len(self._DEVICE_COLUMNS)]
            site_tuple = row[len(self._DEVICE_COLUMNS):]
            payload = self._build_device_payload(device_tuple, site_tuple)
            return payload, 200
        except Exception as exc:
            self.logger.debug("Failed to load device by guid", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def save_agent_details(self) -> Tuple[Dict[str, Any], int]:
        ctx = getattr(g, "device_auth", None)
        if ctx is None:
            self.service_log("server", "/api/agent/details missing device auth context", level="ERROR")
            return {"error": "auth_context_missing"}, 500

        payload = request.get_json(silent=True) or {}
        details = payload.get("details")
        if not isinstance(details, dict):
            return {"error": "invalid payload"}, 400

        hostname = _clean_device_str(payload.get("hostname"))
        if not hostname:
            summary_host = (details.get("summary") or {}).get("hostname")
            hostname = _clean_device_str(summary_host)
        if not hostname:
            return {"error": "invalid payload"}, 400

        agent_id = _clean_device_str(payload.get("agent_id"))
        agent_hash = _clean_device_str(payload.get("agent_hash"))

        raw_guid = getattr(ctx, "guid", None)
        try:
            auth_guid = normalize_guid(raw_guid) if raw_guid else None
        except Exception:
            auth_guid = None

        fingerprint = _clean_device_str(getattr(ctx, "ssl_key_fingerprint", None))
        fingerprint_lower = fingerprint.lower() if fingerprint else ""
        scope_hint = getattr(ctx, "service_mode", None)

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            columns_sql = ", ".join(f"d.{col}" for col in self._DEVICE_COLUMNS)
            cur.execute(
                f"SELECT {columns_sql}, d.ssl_key_fingerprint FROM {DEVICE_TABLE} AS d WHERE d.hostname = ?",
                (hostname,),
            )
            row = cur.fetchone()

            prev_details: Dict[str, Any] = {}
            description = ""
            created_at = 0
            existing_guid = None
            existing_agent_hash = None
            db_fp = ""

            if row:
                device_tuple = row[: len(self._DEVICE_COLUMNS)]
                previous = self._build_device_payload(device_tuple, (None, None, None))
                try:
                    prev_details = json.loads(json.dumps(previous.get("details", {})))
                except Exception:
                    prev_details = previous.get("details", {}) or {}
                description = previous.get("description") or ""
                created_at = _coerce_int(previous.get("created_at")) or 0
                existing_guid_raw = previous.get("agent_guid") or ""
                try:
                    existing_guid = normalize_guid(existing_guid_raw) if existing_guid_raw else None
                except Exception:
                    existing_guid = None
                existing_agent_hash = _clean_device_str(previous.get("agent_hash")) or None
                db_fp = (row[-1] or "").strip().lower() if row[-1] else ""
            if db_fp and fingerprint_lower and db_fp != fingerprint_lower:
                self.service_log(
                    "server",
                    f"/api/agent/details fingerprint mismatch host={hostname} guid={auth_guid or existing_guid or ''}",
                    scope_hint,
                    level="WARN",
                )
                return {"error": "fingerprint_mismatch"}, 403

            if existing_guid and auth_guid and existing_guid != auth_guid:
                self.service_log(
                    "server",
                    f"/api/agent/details guid mismatch host={hostname} expected={existing_guid} provided={auth_guid}",
                    scope_hint,
                    level="WARN",
                )
                return {"error": "guid_mismatch"}, 403

            incoming_summary = details.setdefault("summary", {})
            if agent_id and not incoming_summary.get("agent_id"):
                incoming_summary["agent_id"] = agent_id
            if hostname and not incoming_summary.get("hostname"):
                incoming_summary["hostname"] = hostname
            if agent_hash:
                incoming_summary["agent_hash"] = agent_hash

            effective_guid = auth_guid or existing_guid
            if effective_guid:
                incoming_summary["agent_guid"] = effective_guid
            if fingerprint:
                incoming_summary.setdefault("ssl_key_fingerprint", fingerprint)

            prev_summary = prev_details.get("summary") if isinstance(prev_details, dict) else {}
            if isinstance(prev_summary, dict):
                if _is_empty(incoming_summary.get("last_seen")) and not _is_empty(prev_summary.get("last_seen")):
                    try:
                        incoming_summary["last_seen"] = int(prev_summary.get("last_seen"))
                    except Exception:
                        pass
                if _is_empty(incoming_summary.get("last_user")) and not _is_empty(prev_summary.get("last_user")):
                    incoming_summary["last_user"] = prev_summary.get("last_user")

            merged = _deep_merge_preserve(prev_details, details)
            merged_summary = merged.setdefault("summary", {})
            if hostname:
                merged_summary.setdefault("hostname", hostname)
            if agent_id:
                merged_summary.setdefault("agent_id", agent_id)
            if agent_hash and _is_empty(merged_summary.get("agent_hash")):
                merged_summary["agent_hash"] = agent_hash
            if effective_guid:
                merged_summary["agent_guid"] = effective_guid
            if fingerprint:
                merged_summary.setdefault("ssl_key_fingerprint", fingerprint)
            if description and _is_empty(merged_summary.get("description")):
                merged_summary["description"] = description
            if existing_agent_hash and _is_empty(merged_summary.get("agent_hash")):
                merged_summary["agent_hash"] = existing_agent_hash

            if created_at <= 0:
                created_at = int(time.time())
            try:
                merged_summary.setdefault(
                    "created",
                    datetime.fromtimestamp(created_at, timezone.utc).strftime("%Y-%m-%d %H:%M:%S"),
                )
            except Exception:
                pass
            merged_summary.setdefault("created_at", created_at)

            _device_upsert(
                cur,
                hostname,
                description,
                merged,
                created_at,
                agent_hash=agent_hash or existing_agent_hash,
                guid=effective_guid,
            )

            if effective_guid and fingerprint:
                now_iso = datetime.now(timezone.utc).isoformat()
                cur.execute(
                    """
                    UPDATE devices
                       SET ssl_key_fingerprint = ?,
                           key_added_at = COALESCE(key_added_at, ?)
                     WHERE guid = ?
                    """,
                    (fingerprint, now_iso, effective_guid),
                )
                cur.execute(
                    """
                    INSERT OR IGNORE INTO device_keys (id, guid, ssl_key_fingerprint, added_at)
                    VALUES (?, ?, ?, ?)
                    """,
                    (str(uuid.uuid4()), effective_guid, fingerprint, now_iso),
                )

            conn.commit()
            return {"status": "ok"}, 200
        except Exception as exc:
            try:
                conn.rollback()
            except Exception:
                pass
            self.logger.debug("Failed to save agent details", exc_info=True)
            self.service_log("server", f"/api/agent/details error: {exc}", scope_hint, level="ERROR")
            return {"error": "internal error"}, 500
        finally:
            conn.close()

    def get_device_details(self, hostname: str) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            columns_sql = ", ".join(f"d.{col}" for col in self._DEVICE_COLUMNS)
            cur.execute(
                f"SELECT {columns_sql} FROM devices AS d WHERE d.hostname = ?",
                (hostname,),
            )
            row = cur.fetchone()
            if not row:
                return {}, 200
            mapping = dict(zip(self._DEVICE_COLUMNS, row))
            created_at = mapping.get("created_at") or 0
            last_seen = mapping.get("last_seen") or 0
            payload = {
                "details": {
                    "summary": {
                        "hostname": mapping.get("hostname") or "",
                        "description": mapping.get("description") or "",
                    },
                    "memory": _safe_json(mapping.get("memory"), []),
                    "network": _safe_json(mapping.get("network"), []),
                    "software": _safe_json(mapping.get("software"), []),
                    "storage": _safe_json(mapping.get("storage"), []),
                    "cpu": _safe_json(mapping.get("cpu"), {}),
                },
                "summary": {
                    "hostname": mapping.get("hostname") or "",
                    "description": mapping.get("description") or "",
                },
                "description": mapping.get("description") or "",
                "created_at": created_at or 0,
                "agent_hash": (mapping.get("agent_hash") or "").strip(),
                "agent_guid": normalize_guid(mapping.get("guid")) or "",
                "memory": _safe_json(mapping.get("memory"), []),
                "network": _safe_json(mapping.get("network"), []),
                "software": _safe_json(mapping.get("software"), []),
                "storage": _safe_json(mapping.get("storage"), []),
                "cpu": _safe_json(mapping.get("cpu"), {}),
                "device_type": mapping.get("device_type") or "",
                "domain": mapping.get("domain") or "",
                "external_ip": mapping.get("external_ip") or "",
                "internal_ip": mapping.get("internal_ip") or "",
                "last_reboot": mapping.get("last_reboot") or "",
                "last_seen": last_seen or 0,
                "last_user": mapping.get("last_user") or "",
                "operating_system": mapping.get("operating_system") or "",
                "uptime": mapping.get("uptime") or 0,
                "agent_id": (mapping.get("agent_id") or "").strip(),
            }
            return payload, 200
        except Exception as exc:
            self.logger.debug("Failed to load device details", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def set_device_description(self, hostname: str, description: str) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "UPDATE devices SET description = ? WHERE hostname = ?",
                (description, hostname),
            )
            if cur.rowcount == 0:
                conn.rollback()
                return {"error": "not found"}, 404
            conn.commit()
            return {"status": "ok"}, 200
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to update device description", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def list_views(self) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, name, columns_json, filters_json, created_at, updated_at
                  FROM device_list_views
              ORDER BY name COLLATE NOCASE ASC
                """
            )
            rows = cur.fetchall()
            views = []
            for row in rows:
                views.append(
                    {
                        "id": row[0],
                        "name": row[1],
                        "columns": json.loads(row[2] or "[]"),
                        "filters": json.loads(row[3] or "{}"),
                        "created_at": row[4],
                        "updated_at": row[5],
                    }
                )
            return {"views": views}, 200
        except Exception as exc:
            self.logger.debug("Failed to list device views", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def get_view(self, view_id: int) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, name, columns_json, filters_json, created_at, updated_at
                  FROM device_list_views
                 WHERE id = ?
                """,
                (view_id,),
            )
            row = cur.fetchone()
            if not row:
                return {"error": "not found"}, 404
            payload = {
                "id": row[0],
                "name": row[1],
                "columns": json.loads(row[2] or "[]"),
                "filters": json.loads(row[3] or "{}"),
                "created_at": row[4],
                "updated_at": row[5],
            }
            return payload, 200
        except Exception as exc:
            self.logger.debug("Failed to load device view", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def create_view(self, name: str, columns: List[str], filters: Dict[str, Any]) -> Tuple[Dict[str, Any], int]:
        now = int(time.time())
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO device_list_views(name, columns_json, filters_json, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?)
                """,
                (name, json.dumps(columns), json.dumps(filters), now, now),
            )
            view_id = cur.lastrowid
            conn.commit()
            cur.execute(
                """
                SELECT id, name, columns_json, filters_json, created_at, updated_at
                  FROM device_list_views
                 WHERE id = ?
                """,
                (view_id,),
            )
            row = cur.fetchone()
            if not row:
                return {"error": "creation_failed"}, 500
            payload = {
                "id": row[0],
                "name": row[1],
                "columns": json.loads(row[2] or "[]"),
                "filters": json.loads(row[3] or "{}"),
                "created_at": row[4],
                "updated_at": row[5],
            }
            return payload, 201
        except sqlite3.IntegrityError:
            conn.rollback()
            return {"error": "name already exists"}, 409
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to create device view", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def update_view(
        self,
        view_id: int,
        *,
        name: Optional[str] = None,
        columns: Optional[List[str]] = None,
        filters: Optional[Dict[str, Any]] = None,
    ) -> Tuple[Dict[str, Any], int]:
        fields: List[str] = []
        params: List[Any] = []
        if name is not None:
            fields.append("name = ?")
            params.append(name)
        if columns is not None:
            fields.append("columns_json = ?")
            params.append(json.dumps(columns))
        if filters is not None:
            fields.append("filters_json = ?")
            params.append(json.dumps(filters))
        fields.append("updated_at = ?")
        params.append(int(time.time()))
        params.append(view_id)
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                f"UPDATE device_list_views SET {', '.join(fields)} WHERE id = ?",
                params,
            )
            if cur.rowcount == 0:
                conn.rollback()
                return {"error": "not found"}, 404
            conn.commit()
            return self.get_view(view_id)
        except sqlite3.IntegrityError:
            conn.rollback()
            return {"error": "name already exists"}, 409
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to update device view", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def delete_view(self, view_id: int) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute("DELETE FROM device_list_views WHERE id = ?", (view_id,))
            if cur.rowcount == 0:
                conn.rollback()
                return {"error": "not found"}, 404
            conn.commit()
            return {"status": "ok"}, 200
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to delete device view", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    # ------------------------------------------------------------------
    # Site management helpers
    # ------------------------------------------------------------------

    def list_sites(self) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT s.id,
                       s.name,
                       s.description,
                       s.created_at,
                       COALESCE(ds.cnt, 0) AS device_count
                  FROM sites AS s
             LEFT JOIN (
                       SELECT site_id, COUNT(*) AS cnt
                         FROM device_sites
                     GROUP BY site_id
                   ) AS ds ON ds.site_id = s.id
              ORDER BY LOWER(s.name) ASC
                """
            )
            rows = cur.fetchall()
            sites = [_row_to_site(row) for row in rows]
            return {"sites": sites}, 200
        except Exception as exc:
            self.logger.debug("Failed to list sites", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def create_site(self, name: str, description: str) -> Tuple[Dict[str, Any], int]:
        if not name:
            return {"error": "name is required"}, 400
        now = int(time.time())
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "INSERT INTO sites(name, description, created_at) VALUES (?, ?, ?)",
                (name, description, now),
            )
            site_id = cur.lastrowid
            conn.commit()
            cur.execute(
                "SELECT id, name, description, created_at, 0 FROM sites WHERE id = ?",
                (site_id,),
            )
            row = cur.fetchone()
            if not row:
                return {"error": "creation_failed"}, 500
            return _row_to_site(row), 201
        except sqlite3.IntegrityError:
            conn.rollback()
            return {"error": "name already exists"}, 409
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to create site", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def delete_sites(self, ids: List[Any]) -> Tuple[Dict[str, Any], int]:
        if not isinstance(ids, list) or not all(isinstance(x, (int, str)) for x in ids):
            return {"error": "ids must be a list"}, 400
        norm_ids: List[int] = []
        for value in ids:
            try:
                norm_ids.append(int(value))
            except Exception:
                continue
        if not norm_ids:
            return {"status": "ok", "deleted": 0}, 200
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            placeholders = ",".join("?" * len(norm_ids))
            cur.execute(
                f"DELETE FROM device_sites WHERE site_id IN ({placeholders})",
                tuple(norm_ids),
            )
            cur.execute(
                f"DELETE FROM sites WHERE id IN ({placeholders})",
                tuple(norm_ids),
            )
            deleted = cur.rowcount
            conn.commit()
            return {"status": "ok", "deleted": deleted}, 200
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to delete sites", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def sites_device_map(self, hostnames: Optional[str]) -> Tuple[Dict[str, Any], int]:
        filter_set: set[str] = set()
        if hostnames:
            for part in hostnames.split(","):
                candidate = part.strip()
                if candidate:
                    filter_set.add(candidate)
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            if filter_set:
                placeholders = ",".join("?" * len(filter_set))
                cur.execute(
                    f"""
                    SELECT ds.device_hostname, s.id, s.name
                      FROM device_sites ds
                      JOIN sites s ON s.id = ds.site_id
                     WHERE ds.device_hostname IN ({placeholders})
                    """,
                    tuple(filter_set),
                )
            else:
                cur.execute(
                    """
                    SELECT ds.device_hostname, s.id, s.name
                      FROM device_sites ds
                      JOIN sites s ON s.id = ds.site_id
                    """
                )
            mapping: Dict[str, Dict[str, Any]] = {}
            for hostname, site_id, site_name in cur.fetchall():
                mapping[str(hostname)] = {"site_id": site_id, "site_name": site_name}
            return {"mapping": mapping}, 200
        except Exception as exc:
            self.logger.debug("Failed to build site device map", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def assign_devices(self, site_id: Any, hostnames: List[str]) -> Tuple[Dict[str, Any], int]:
        try:
            site_id_int = int(site_id)
        except Exception:
            return {"error": "invalid site_id"}, 400
        if not isinstance(hostnames, list) or not all(isinstance(h, str) and h.strip() for h in hostnames):
            return {"error": "hostnames must be a list of strings"}, 400
        now = int(time.time())
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute("SELECT 1 FROM sites WHERE id = ?", (site_id_int,))
            if not cur.fetchone():
                return {"error": "site not found"}, 404
            for hostname in hostnames:
                hn = hostname.strip()
                if not hn:
                    continue
                cur.execute(
                    """
                    INSERT INTO device_sites(device_hostname, site_id, assigned_at)
                    VALUES (?, ?, ?)
                    ON CONFLICT(device_hostname)
                    DO UPDATE SET site_id=excluded.site_id, assigned_at=excluded.assigned_at
                    """,
                    (hn, site_id_int, now),
                )
            conn.commit()
            return {"status": "ok"}, 200
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to assign devices to site", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def rename_site(self, site_id: Any, new_name: str) -> Tuple[Dict[str, Any], int]:
        try:
            site_id_int = int(site_id)
        except Exception:
            return {"error": "invalid id"}, 400
        if not new_name:
            return {"error": "new_name is required"}, 400
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute("UPDATE sites SET name = ? WHERE id = ?", (new_name, site_id_int))
            if cur.rowcount == 0:
                conn.rollback()
                return {"error": "site not found"}, 404
            conn.commit()
            cur.execute(
                """
                SELECT s.id,
                       s.name,
                       s.description,
                       s.created_at,
                       COALESCE(ds.cnt, 0) AS device_count
                  FROM sites AS s
             LEFT JOIN (
                       SELECT site_id, COUNT(*) AS cnt
                         FROM device_sites
                     GROUP BY site_id
                   ) ds ON ds.site_id = s.id
                 WHERE s.id = ?
                """,
                (site_id_int,),
            )
            row = cur.fetchone()
            if not row:
                return {"error": "site not found"}, 404
            return _row_to_site(row), 200
        except sqlite3.IntegrityError:
            conn.rollback()
            return {"error": "name already exists"}, 409
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to rename site", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def repo_current_hash(self) -> Tuple[Dict[str, Any], int]:
        repo = (request.args.get("repo") or "bunny-lab-io/Borealis").strip()
        branch = (request.args.get("branch") or "main").strip()
        refresh_flag = (request.args.get("refresh") or "").strip().lower()
        ttl_raw = request.args.get("ttl")
        if "/" not in repo:
            return {"error": "repo must be in the form owner/name"}, 400
        try:
            ttl = int(ttl_raw) if ttl_raw else 60
        except ValueError:
            ttl = 60
        force_refresh = refresh_flag in {"1", "true", "yes", "force", "refresh"}
        payload, status = self.repo_cache.resolve(repo, branch, ttl=ttl, force_refresh=force_refresh)
        return payload, status

    def agent_hash_list(self) -> Tuple[Dict[str, Any], int]:
        if not _is_internal_request(request.remote_addr):
            remote_addr = (request.remote_addr or "unknown").strip() or "unknown"
            self.service_log(
                "server",
                f"/api/agent/hash_list denied non-local request from {remote_addr}",
                level="WARN",
            )
            return {"error": "forbidden"}, 403
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT guid, hostname, agent_hash, agent_id FROM devices",
            )
            agents = []
            for guid, hostname, agent_hash, agent_id in cur.fetchall():
                agents.append(
                    {
                        "agent_guid": normalize_guid(guid) or None,
                        "hostname": hostname or None,
                        "agent_hash": (agent_hash or "").strip() or None,
                        "agent_id": (agent_id or "").strip() or None,
                        "source": "database",
                    }
                )
            agents.sort(key=lambda rec: (rec.get("hostname") or "", rec.get("agent_id") or ""))
            return {"agents": agents}, 200
        except Exception as exc:
            self.service_log("server", f"/api/agent/hash_list error: {exc}")
            return {"error": "internal error"}, 500
        finally:
            conn.close()

def register_management(app, adapters: "LegacyServiceAdapters") -> None:
    """Register device management endpoints onto the Flask app."""

    service = DeviceManagementService(app, adapters)
    blueprint = Blueprint("devices", __name__)

    @blueprint.route("/api/agent/details", methods=["POST"])
    @require_device_auth(adapters.device_auth_manager)
    def _agent_details():
        payload, status = service.save_agent_details()
        return jsonify(payload), status

    @blueprint.route("/api/devices", methods=["GET"])
    def _list_devices():
        payload, status = service.list_devices()
        return jsonify(payload), status

    @blueprint.route("/api/devices/<guid>", methods=["GET"])
    def _device_by_guid(guid: str):
        payload, status = service.get_device_by_guid(guid)
        return jsonify(payload), status

    @blueprint.route("/api/device/details/<hostname>", methods=["GET"])
    def _device_details(hostname: str):
        payload, status = service.get_device_details(hostname)
        return jsonify(payload), status

    @blueprint.route("/api/device/description/<hostname>", methods=["POST"])
    def _set_description(hostname: str):
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        description = (body.get("description") or "").strip()
        payload, status = service.set_device_description(hostname, description)
        return jsonify(payload), status

    @blueprint.route("/api/device_list_views", methods=["GET"])
    def _list_views():
        payload, status = service.list_views()
        return jsonify(payload), status

    @blueprint.route("/api/device_list_views/<int:view_id>", methods=["GET"])
    def _get_view(view_id: int):
        payload, status = service.get_view(view_id)
        return jsonify(payload), status

    @blueprint.route("/api/device_list_views", methods=["POST"])
    def _create_view():
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        name = (data.get("name") or "").strip()
        columns = data.get("columns") or []
        filters = data.get("filters") or {}
        if not name:
            return jsonify({"error": "name is required"}), 400
        if name.lower() == "default view":
            return jsonify({"error": "reserved name"}), 400
        if not isinstance(columns, list) or not all(isinstance(col, str) for col in columns):
            return jsonify({"error": "columns must be a list of strings"}), 400
        if not isinstance(filters, dict):
            return jsonify({"error": "filters must be an object"}), 400
        payload, status = service.create_view(name, columns, filters)
        return jsonify(payload), status

    @blueprint.route("/api/device_list_views/<int:view_id>", methods=["PUT"])
    def _update_view(view_id: int):
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        name = data.get("name")
        columns = data.get("columns")
        filters = data.get("filters")
        if name is not None:
            name = (name or "").strip()
            if not name:
                return jsonify({"error": "name cannot be empty"}), 400
            if name.lower() == "default view":
                return jsonify({"error": "reserved name"}), 400
        if columns is not None:
            if not isinstance(columns, list) or not all(isinstance(col, str) for col in columns):
                return jsonify({"error": "columns must be a list of strings"}), 400
        if filters is not None and not isinstance(filters, dict):
            return jsonify({"error": "filters must be an object"}), 400
        payload, status = service.update_view(view_id, name=name, columns=columns, filters=filters)
        return jsonify(payload), status

    @blueprint.route("/api/device_list_views/<int:view_id>", methods=["DELETE"])
    def _delete_view(view_id: int):
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.delete_view(view_id)
        return jsonify(payload), status

    @blueprint.route("/api/sites", methods=["GET"])
    def _sites_list():
        payload, status = service.list_sites()
        return jsonify(payload), status

    @blueprint.route("/api/sites", methods=["POST"])
    def _sites_create():
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        name = (data.get("name") or "").strip()
        description = (data.get("description") or "").strip()
        payload, status = service.create_site(name, description)
        return jsonify(payload), status

    @blueprint.route("/api/sites/delete", methods=["POST"])
    def _sites_delete():
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        ids = data.get("ids") or []
        payload, status = service.delete_sites(ids)
        return jsonify(payload), status

    @blueprint.route("/api/sites/device_map", methods=["GET"])
    def _sites_device_map():
        payload, status = service.sites_device_map(request.args.get("hostnames"))
        return jsonify(payload), status

    @blueprint.route("/api/sites/assign", methods=["POST"])
    def _sites_assign():
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        payload, status = service.assign_devices(data.get("site_id"), data.get("hostnames") or [])
        return jsonify(payload), status

    @blueprint.route("/api/sites/rename", methods=["POST"])
    def _sites_rename():
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        payload, status = service.rename_site(data.get("id"), (data.get("new_name") or "").strip())
        return jsonify(payload), status

    @blueprint.route("/api/repo/current_hash", methods=["GET"])
    def _repo_current_hash():
        payload, status = service.repo_current_hash()
        return jsonify(payload), status

    @blueprint.route("/api/agent/hash_list", methods=["GET"])
    def _agent_hash_list():
        payload, status = service.agent_hash_list()
        return jsonify(payload), status

    app.register_blueprint(blueprint)
    adapters.context.logger.info("Engine registered API group 'devices'.")
