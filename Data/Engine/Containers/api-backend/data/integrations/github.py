# ======================================================
# Data\Engine\integrations\github.py
# Description: GitHub REST integration providing cached repository head lookups for Engine services.
#
# API Endpoints (if applicable): None
# ======================================================

"""GitHub integration helpers for the Borealis Engine runtime."""
from __future__ import annotations

import base64
import json
import logging
import os
from Data.Engine.db import dbapi as sqlite3
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import TYPE_CHECKING, Any, Callable, Dict, Optional, Tuple
from urllib.parse import quote

from flask import has_request_context, request

try:  # pragma: no cover - import guard mirrors legacy runtime behaviour
    import requests  # type: ignore
except ImportError:  # pragma: no cover - graceful fallback for minimal environments
    class _RequestsStub:
        class RequestException(RuntimeError):
            """Raised when the ``requests`` library is unavailable."""

        def get(self, *args: Any, **kwargs: Any) -> Any:
            raise self.RequestException("The 'requests' library is required for GitHub integrations.")

    requests = _RequestsStub()  # type: ignore

try:  # pragma: no cover - optional dependency for green thread integration
    from eventlet import tpool as _eventlet_tpool  # type: ignore
except Exception:  # pragma: no cover - optional dependency
    _eventlet_tpool = None  # type: ignore

try:  # pragma: no cover - optional dependency for retrieving original modules
    from eventlet import patcher as _eventlet_patcher  # type: ignore
except Exception:  # pragma: no cover - optional dependency
    _eventlet_patcher = None  # type: ignore

from Data.Engine.services.aegis_cipher import AegisDataCorruptionError, AegisLockedError

__all__ = ["GitHubIntegration"]

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from Data.Engine.services.aegis_cipher import AegisCipherService


class GitHubIntegration:
    """Lightweight cache for GitHub repository head lookups."""

    MIN_TTL_SECONDS = 30
    MAX_TTL_SECONDS = 3600
    DEFAULT_TTL_SECONDS = 60
    DEFAULT_REPO = "bunny-lab-io/Borealis"
    DEFAULT_BRANCH = "main"

    def __init__(
        self,
        *,
        cache_file: Path,
        db_conn_factory: Callable[[], sqlite3.Connection],
        service_log: Callable[[str, str, Optional[str]], None],
        logger: Optional[logging.Logger] = None,
        default_repo: Optional[str] = None,
        default_branch: Optional[str] = None,
        default_ttl_seconds: Optional[int] = None,
        aegis_cipher_service: Optional["AegisCipherService"] = None,
    ) -> None:
        self._cache_file = cache_file
        self._cache_file.parent.mkdir(parents=True, exist_ok=True)
        self._db_conn_factory = db_conn_factory
        self._service_log = service_log
        self._logger = logger or logging.getLogger(__name__)
        self._aegis_cipher_service = aegis_cipher_service

        self._lock = threading.Lock()
        self._token_lock = threading.Lock()
        self._cache: Dict[Tuple[str, str], Tuple[str, float]] = {}
        self._token_cache: Dict[str, Any] = {"value": None, "loaded_at": 0.0, "known": False}

        self._default_repo = self._determine_default_repo(default_repo)
        self._default_branch = self._determine_default_branch(default_branch)
        self._default_ttl = self._determine_default_ttl(default_ttl_seconds)

        self._load_cache()

    @property
    def default_repo(self) -> str:
        return self._default_repo

    @property
    def default_branch(self) -> str:
        return self._default_branch

    def current_repo_hash(
        self,
        repo: Optional[str],
        branch: Optional[str],
        *,
        ttl: Optional[Any] = None,
        force_refresh: bool = False,
    ) -> Tuple[Dict[str, Any], int]:
        owner_repo = (repo or self._default_repo).strip()
        target_branch = (branch or self._default_branch).strip()

        if "/" not in owner_repo:
            return {"error": "repo must be in the form owner/name"}, 400

        ttl_seconds = self._normalise_ttl(ttl)
        return self._resolve(owner_repo, target_branch, ttl_seconds=ttl_seconds, force_refresh=force_refresh)

    def _determine_default_repo(self, override: Optional[str]) -> str:
        candidate = (override or os.environ.get("BOREALIS_REPO") or self.DEFAULT_REPO).strip()
        if "/" not in candidate:
            return self.DEFAULT_REPO
        return candidate

    def _determine_default_branch(self, override: Optional[str]) -> str:
        candidate = (override or os.environ.get("BOREALIS_REPO_BRANCH") or self.DEFAULT_BRANCH).strip()
        return candidate or self.DEFAULT_BRANCH

    def _determine_default_ttl(self, override: Optional[int]) -> int:
        env_value = os.environ.get("BOREALIS_REPO_HASH_REFRESH")
        candidate: Optional[int] = None
        if override is not None:
            candidate = override
        else:
            try:
                candidate = int(env_value) if env_value else None
            except (TypeError, ValueError):
                candidate = None
        if candidate is None:
            candidate = self.DEFAULT_TTL_SECONDS
        return self._normalise_ttl(candidate)

    def _normalise_ttl(self, ttl: Optional[Any]) -> int:
        value: Optional[int] = None
        if isinstance(ttl, str):
            ttl = ttl.strip()
            if not ttl:
                ttl = None
        if ttl is None:
            value = self._default_ttl
        else:
            try:
                value = int(ttl)
            except (TypeError, ValueError):
                value = self._default_ttl
        value = value if value is not None else self._default_ttl
        return max(self.MIN_TTL_SECONDS, min(value, self.MAX_TTL_SECONDS))

    def _load_cache(self) -> None:
        try:
            if not self._cache_file.is_file():
                return
            payload = json.loads(self._cache_file.read_text(encoding="utf-8"))
            entries = payload.get("entries")
            if not isinstance(entries, dict):
                return
            now = time.time()
            with self._lock:
                for key, data in entries.items():
                    if not isinstance(data, dict):
                        continue
                    sha = (data.get("sha") or "").strip()
                    if not sha:
                        continue
                    ts_raw = data.get("ts")
                    try:
                        ts = float(ts_raw)
                    except (TypeError, ValueError):
                        ts = now
                    repo, _, branch = key.partition(":")
                    if repo and branch:
                        self._cache[(repo, branch)] = (sha, ts)
        except Exception:  # pragma: no cover - defensive logging
            self._logger.debug("Failed to hydrate GitHub repo hash cache", exc_info=True)

    def _persist_cache(self) -> None:
        with self._lock:
            snapshot = {
                f"{repo}:{branch}": {"sha": sha, "ts": ts}
                for (repo, branch), (sha, ts) in self._cache.items()
                if sha
            }
        try:
            if not snapshot:
                try:
                    if self._cache_file.exists():
                        self._cache_file.unlink()
                except FileNotFoundError:
                    return
                except Exception:
                    self._logger.debug("Failed to remove GitHub repo hash cache file", exc_info=True)
                return
            payload = {"version": 1, "entries": snapshot}
            tmp_path = self._cache_file.with_suffix(".tmp")
            self._cache_file.parent.mkdir(parents=True, exist_ok=True)
            tmp_path.write_text(json.dumps(payload), encoding="utf-8")
            tmp_path.replace(self._cache_file)
        except Exception:  # pragma: no cover - defensive logging
            self._logger.debug("Failed to persist GitHub repo hash cache", exc_info=True)

    def _resolve(
        self,
        repo: str,
        branch: str,
        *,
        ttl_seconds: int,
        force_refresh: bool,
    ) -> Tuple[Dict[str, Any], int]:
        key = (repo, branch)
        now = time.time()

        with self._lock:
            cached = self._cache.get(key)

        cached_sha: Optional[str] = None
        cached_ts: Optional[float] = None
        cached_age: Optional[float] = None
        if cached:
            cached_sha, cached_ts = cached
            cached_age = max(0.0, now - cached_ts)

        if cached_sha and not force_refresh and cached_age is not None and cached_age < ttl_seconds:
            return self._build_payload(repo, branch, cached_sha, True, cached_age, "cache", None), 200

        sha, error = self._fetch_repo_head(repo, branch, force_refresh=force_refresh)
        if sha:
            with self._lock:
                self._cache[key] = (sha, now)
            self._persist_cache()
            return self._build_payload(repo, branch, sha, False, 0.0, "github", None), 200

        if error:
            self._service_log("server", f"/api/repo/current_hash error: {error}")

        if cached_sha is not None:
            payload = self._build_payload(
                repo,
                branch,
                cached_sha or None,
                True,
                cached_age,
                "cache-stale",
                error or "using cached value",
            )
            return payload, (200 if cached_sha else 503)

        payload = self._build_payload(
            repo,
            branch,
            None,
            False,
            None,
            "github",
            error or "unable to resolve repository head",
        )
        return payload, 503

    def _build_payload(
        self,
        repo: str,
        branch: str,
        sha: Optional[str],
        cached: bool,
        age_seconds: Optional[float],
        source: str,
        error: Optional[str],
    ) -> Dict[str, Any]:
        payload: Dict[str, Any] = {
            "repo": repo,
            "branch": branch,
            "sha": (sha.strip() if isinstance(sha, str) else None) or None,
            "cached": cached,
            "age_seconds": age_seconds,
            "source": source,
        }
        if error:
            payload["error"] = error
        return payload

    def _fetch_repo_head(self, repo: str, branch: str, *, force_refresh: bool) -> Tuple[Optional[str], Optional[str]]:
        headers = {
            "Accept": "application/vnd.github+json",
            "User-Agent": "Borealis-Engine",
        }
        token = self._github_token(force_refresh=force_refresh)
        if token:
            headers["Authorization"] = f"Bearer {token}"
        try:
            encoded_ref = quote(branch, safe="")
            response = self._http_get(
                f"https://api.github.com/repos/{repo}/commits/{encoded_ref}",
                headers=headers,
                timeout=20,
            )
            status = getattr(response, "status_code", None)
            if status == 200:
                try:
                    data = response.json()
                except Exception as exc:
                    return None, f"GitHub REST API repo head decode error: {exc}"
                commit_payload = data.get("commit") if isinstance(data.get("commit"), dict) else {}
                sha = (data.get("sha") or commit_payload.get("sha") or "").strip()
                if sha:
                    return sha, None
                return None, "GitHub REST API repo head missing commit SHA"
            snippet = ""
            try:
                text = getattr(response, "text", "")
                snippet = text[:200] if isinstance(text, str) else ""
            except Exception:
                snippet = ""
            error = f"GitHub REST API repo head lookup failed: HTTP {status}"
            if snippet:
                error = f"{error} {snippet}"
            return None, error
        except requests.RequestException as exc:  # type: ignore[attr-defined]
            return None, f"GitHub REST API repo head lookup raised: {exc}"
        except RecursionError as exc:  # pragma: no cover - defensive guard
            return None, f"GitHub REST API repo head lookup recursion error: {exc}"
        except Exception as exc:  # pragma: no cover - defensive guard
            return None, f"GitHub REST API repo head lookup unexpected error: {exc}"
    def _github_token(self, *, force_refresh: bool) -> Optional[str]:
        if has_request_context():
            header_token = (request.headers.get("X-GitHub-Token") or "").strip()
            if header_token:
                return header_token

        now = time.time()
        with self._token_lock:
            if (
                not force_refresh
                and self._token_cache.get("known")
                and now - (self._token_cache.get("loaded_at") or 0.0) < 15.0
            ):
                cached_token = self._token_cache.get("value")
                return cached_token if cached_token else None

        token = self._load_token_from_db(force_refresh=force_refresh)
        self._set_cached_token(token)
        if token:
            return token

        fallback = os.environ.get("BOREALIS_GITHUB_TOKEN") or os.environ.get("GITHUB_TOKEN")
        fallback = (fallback or "").strip()
        return fallback or None

    def _set_cached_token(self, token: Optional[str]) -> None:
        with self._token_lock:
            self._token_cache["value"] = token if token else None
            self._token_cache["loaded_at"] = time.time()
            self._token_cache["known"] = True

    def load_token(self, *, force_refresh: bool = False) -> Optional[str]:
        token = self._load_token_from_db(force_refresh=force_refresh)
        self._set_cached_token(token)
        return token

    def token_storage_state(self) -> Dict[str, Any]:
        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn_factory()
            cursor = conn.cursor()
            cursor.execute("SELECT token, reset_required, reset_at FROM github_token LIMIT 1")
            row = cursor.fetchone()
            return {
                "has_row": bool(row),
                "has_token": bool(row and str(row[0] or "").strip()),
                "reset_required": bool(row and int(row[1] or 0)),
                "reset_at": int(row[2] or 0) if row and row[2] is not None else 0,
            }
        except sqlite3.OperationalError:
            return {"has_row": False, "has_token": False, "reset_required": False, "reset_at": 0}
        except Exception as exc:
            self._service_log("server", f"github token state lookup failed: {exc}", level="WARNING")
            return {"has_row": False, "has_token": False, "reset_required": False, "reset_at": 0}
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

    def store_token(self, token: Optional[str]) -> None:
        stored_token = token if token else None
        if stored_token is not None and self._aegis_cipher_service is not None:
            stored_token = self._aegis_cipher_service.encrypt_secret_for_text(stored_token)
        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn_factory()
            cur = conn.cursor()
            cur.execute("DELETE FROM github_token")
            if stored_token is not None:
                cur.execute(
                    "INSERT INTO github_token (token, reset_required, reset_at) VALUES (?,?,?)",
                    (stored_token, 0, None),
                )
            conn.commit()
        except Exception as exc:
            if conn is not None:
                try:
                    conn.rollback()
                except Exception:
                    pass
            raise RuntimeError(f"Failed to store token: {exc}") from exc
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass
        self._set_cached_token(token if token else None)

    def verify_token(self, token: Optional[str]) -> Dict[str, Any]:
        if not token:
            return {
                "valid": False,
                "message": "API Token Not Configured",
                "status": "missing",
                "rate_limit": None,
            }

        headers = {
            "Accept": "application/vnd.github+json",
            "User-Agent": "Borealis-Engine",
            "Authorization": f"Bearer {token}",
        }
        try:
            response = self._http_get(
                f"https://api.github.com/repos/{self._default_repo}/branches/{self._default_branch}",
                headers=headers,
                timeout=20,
            )
            limit_header = response.headers.get("X-RateLimit-Limit")
            try:
                limit_value = int(limit_header) if limit_header is not None else None
            except (TypeError, ValueError):
                limit_value = None

            if response.status_code == 200:
                if limit_value is not None and limit_value >= 5000:
                    return {
                        "valid": True,
                        "message": "API Authentication Successful",
                        "status": "ok",
                        "rate_limit": limit_value,
                    }
                return {
                    "valid": False,
                    "message": "API Token Invalid",
                    "status": "insufficient",
                    "rate_limit": limit_value,
                    "error": "Authenticated request did not elevate GitHub rate limits",
                }

            if response.status_code == 401:
                return {
                    "valid": False,
                    "message": "API Token Invalid",
                    "status": "invalid",
                    "rate_limit": limit_value,
                    "error": getattr(response, "text", "")[:200],
                }

            return {
                "valid": False,
                "message": f"GitHub API error (HTTP {response.status_code})",
                "status": "error",
                "rate_limit": limit_value,
                "error": getattr(response, "text", "")[:200],
            }
        except Exception as exc:
            return {
                "valid": False,
                "message": f"API Token validation error: {exc}",
                "status": "error",
                "rate_limit": None,
                "error": str(exc),
            }

    def refresh_default_repo_hash(self, *, force: bool = False) -> Tuple[Dict[str, Any], int]:
        return self._resolve(
            self._default_repo,
            self._default_branch,
            ttl_seconds=self._default_ttl,
            force_refresh=force,
        )

    def _http_get(self, url: str, *, headers: Dict[str, str], timeout: int) -> Any:
        if _eventlet_tpool is not None:
            return self._http_get_subprocess(url, headers=headers, timeout=timeout)
        try:
            return requests.get(url, headers=headers, timeout=timeout)
        except Exception:
            return self._http_get_subprocess(url, headers=headers, timeout=timeout)

    def _http_get_subprocess(self, url: str, *, headers: Dict[str, str], timeout: int) -> Any:
        script = """
import base64
import json
import sys
import urllib.request

url = sys.argv[1]
headers = json.loads(sys.argv[2])
timeout = float(sys.argv[3])
req = urllib.request.Request(url, headers=headers, method="GET")
try:
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        body = resp.read()
        payload = {
            "status": resp.status,
            "headers": dict(resp.getheaders()),
            "body": base64.b64encode(body).decode("ascii"),
            "encoding": "base64",
        }
        sys.stdout.write(json.dumps(payload))
except Exception as exc:
    error_payload = {"error": str(exc)}
    sys.stdout.write(json.dumps(error_payload))
    sys.exit(1)
"""
        try:
            proc = subprocess.run(
                [sys.executable, "-c", script, url, json.dumps(headers), str(float(timeout))],
                capture_output=True,
                text=True,
                timeout=max(float(timeout) + 5.0, 5.0),
            )
        except subprocess.TimeoutExpired as exc:
            raise RuntimeError(f"GitHub subprocess request timed out after {timeout}s") from exc
        output = proc.stdout.strip() or proc.stderr.strip()
        try:
            data = json.loads(output or "{}")
        except json.JSONDecodeError as exc:
            raise RuntimeError(f"GitHub subprocess returned invalid JSON: {output!r}") from exc

        if proc.returncode != 0:
            error_msg = data.get("error") if isinstance(data, dict) else output
            raise RuntimeError(f"GitHub subprocess request failed: {error_msg}")

        status_code = data.get("status")
        raw_headers = data.get("headers") or {}
        body_encoded = data.get("body") or ""
        encoding = data.get("encoding")
        if encoding == "base64":
            body_bytes = base64.b64decode(body_encoded.encode("ascii"))
        else:
            body_bytes = (body_encoded or "").encode("utf-8")

        class _SubprocessResponse:
            def __init__(self, status: int, headers: Dict[str, str], body: bytes):
                self.status_code = status
                self.headers = headers
                self._body = body
                self.text = body.decode("utf-8", errors="replace")

            def json(self) -> Any:
                if not self._body:
                    return {}
                return json.loads(self.text)

        if status_code is None:
            raise RuntimeError(f"GitHub subprocess returned no status code: {data}")

        return _SubprocessResponse(int(status_code), {str(k): str(v) for k, v in raw_headers.items()}, body_bytes)

    def _resolve_original_ssl_module(self):
        if _eventlet_patcher is not None:
            try:
                original = _eventlet_patcher.original("ssl")
                if original is not None:
                    return original
            except Exception:
                pass
        return ssl

    def _resolve_original_socket_module(self):
        if _eventlet_patcher is not None:
            try:
                original = _eventlet_patcher.original("socket")
                if original is not None:
                    return original
            except Exception:
                pass
        import socket as socket_module  # Local import for fallback

        return socket_module

    def _resolve_original_raw_socket_module(self):
        if _eventlet_patcher is not None:
            try:
                original = _eventlet_patcher.original("_socket")
                if original is not None:
                    return original
            except Exception:
                pass
        try:
            import _socket as raw_socket_module  # type: ignore

            return raw_socket_module
        except Exception:
            return self._resolve_original_socket_module()

    def _load_token_from_db(self, *, force_refresh: bool = False) -> Optional[str]:
        if force_refresh:
            with self._token_lock:
                self._token_cache["known"] = False
        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn_factory()
            cursor = conn.cursor()
            cursor.execute("SELECT token FROM github_token LIMIT 1")
            row = cursor.fetchone()
            if row and row[0]:
                candidate = str(row[0]).strip()
                if not candidate:
                    return None
                if self._aegis_cipher_service is not None:
                    try:
                        return self._aegis_cipher_service.decrypt_secret_text(candidate) or None
                    except AegisLockedError:
                        return None
                    except AegisDataCorruptionError as exc:
                        self._service_log("server", f"github token decrypt failed: {exc}", level="WARNING")
                        return None
                    except Exception as exc:
                        self._service_log("server", f"github token decrypt failed: {exc}", level="WARNING")
                        return None
                return candidate or None
            return None
        except sqlite3.OperationalError:
            return None
        except Exception as exc:
            self._service_log("server", f"github token lookup failed: {exc}")
            return None
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass
