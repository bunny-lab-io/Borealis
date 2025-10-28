
"""Device management endpoints for the Borealis Engine API."""
from __future__ import annotations

import json
import logging
import os
import sqlite3
import threading
import time
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, List, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from Modules.guid_utils import normalize_guid

try:
    import requests  # type: ignore
except ImportError:  # pragma: no cover - fallback for minimal test environments
    class _RequestsStub:
        class RequestException(RuntimeError):
            """Stand-in exception when the requests module is unavailable."""

        def get(self, *args: Any, **kwargs: Any) -> Any:
            raise RuntimeError("The 'requests' library is required for repository hash lookups.")

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
