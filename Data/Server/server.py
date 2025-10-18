#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/server.py
"""
Borealis Server
----------------
Flask + Socket.IO application that fronts the Borealis platform. The module
coordinates agent lifecycle, workflow storage, job scheduling, and supportive
utilities exposed through HTTP and WebSocket interfaces.

Section Guide:
  * Logging & Repository Hash Tracking
  * Local Python Integrations
  * Runtime Stack Configuration
  * Authentication & Identity
  * Storage, Assemblies, and Legacy Script APIs
  * Agent Lifecycle and Scheduler Integration
  * Sites, Search, and Device Views
  * Quick Jobs, Ansible Reporting, and Service Accounts
  * WebSocket Event Handling and Entrypoint
"""

import os
import sys
from pathlib import Path
import ssl

# Ensure the modular server package is importable when the runtime is launched
# from a packaged directory (e.g., Server/Borealis). We look for the canonical
# Data/Server location as well as a sibling Modules folder.
_SERVER_DIR = os.path.abspath(os.path.dirname(__file__))
_SEARCH_ROOTS = [
    _SERVER_DIR,
    os.path.abspath(os.path.join(_SERVER_DIR, "..", "..", "Data", "Server")),
]
for root in _SEARCH_ROOTS:
    modules_dir = os.path.join(root, "Modules")
    if os.path.isdir(modules_dir) and root not in sys.path:
        sys.path.insert(0, root)

import eventlet
# Monkey-patch stdlib for cooperative sockets (keep real threads for tpool usage)
eventlet.monkey_patch(thread=False)

from eventlet import tpool

try:
    from eventlet.wsgi import HttpProtocol  # type: ignore
except Exception:
    HttpProtocol = None  # type: ignore[assignment]
else:
    _original_handle_one_request = HttpProtocol.handle_one_request

    def _quiet_tls_http_mismatch(self):  # type: ignore[override]
        try:
            return _original_handle_one_request(self)
        except ssl.SSLError as exc:  # type: ignore[arg-type]
            reason = getattr(exc, "reason", "")
            reason_text = str(reason).lower() if reason else ""
            message = " ".join(str(arg) for arg in exc.args if arg).lower()
            if "http_request" in message or reason_text == "http request":
                try:
                    self.close_connection = True  # type: ignore[attr-defined]
                except Exception:
                    pass
                try:
                    conn = getattr(self, "socket", None) or getattr(self, "connection", None)
                    if conn:
                        conn.close()
                except Exception:
                    pass
                return None
            raise

    HttpProtocol.handle_one_request = _quiet_tls_http_mismatch  # type: ignore[assignment]

import requests
import re
import base64
from flask import Flask, request, jsonify, Response, send_from_directory, make_response, session, g
from flask_socketio import SocketIO, emit, join_room
from flask_cors import CORS
from werkzeug.middleware.proxy_fix import ProxyFix
from itsdangerous import URLSafeTimedSerializer, BadSignature, SignatureExpired

import time
import json  # For reading workflow JSON files
import shutil  # For moving workflow files and folders
from typing import List, Dict, Tuple, Optional, Any, Set, Sequence
import sqlite3
import io
import uuid
import subprocess
import stat
import traceback
from threading import Lock

from datetime import datetime, timezone

from Modules import db_migrations
from Modules.auth import jwt_service as jwt_service_module
from Modules.auth.dpop import DPoPValidator
from Modules.auth.device_auth import DeviceAuthManager, require_device_auth
from Modules.auth.rate_limit import SlidingWindowRateLimiter
from Modules.agents import routes as agent_routes
from Modules.crypto import certificates, signing
from Modules.enrollment import routes as enrollment_routes
from Modules.enrollment.nonce_store import NonceCache
from Modules.tokens import routes as token_routes
from Modules.admin import routes as admin_routes
from Modules.jobs.prune import start_prune_job

try:
    from cryptography.fernet import Fernet  # type: ignore
except Exception:
    Fernet = None  # optional; we will fall back to reversible base64 if missing

try:
    import pyotp  # type: ignore
except Exception:
    pyotp = None  # type: ignore

try:
    import qrcode  # type: ignore
except Exception:
    qrcode = None  # type: ignore

# =============================================================================
# Section: Logging & Observability
# =============================================================================
# Centralized writers for server/ansible logs with daily rotation under Logs/Server.
def _server_logs_root() -> str:
    try:
        return os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..', 'Logs', 'Server'))
    except Exception:
        return os.path.abspath(os.path.join(os.path.dirname(__file__), 'Logs', 'Server'))


def _rotate_daily(path: str):
    try:
        import datetime as _dt
        if os.path.isfile(path):
            mtime = os.path.getmtime(path)
            dt = _dt.datetime.fromtimestamp(mtime)
            today = _dt.datetime.now().date()
            if dt.date() != today:
                suffix = dt.strftime('%Y-%m-%d')
                newp = f"{path}.{suffix}"
                try:
                    os.replace(path, newp)
                except Exception:
                    pass
    except Exception:
        pass


_SERVER_SCOPE_PATTERN = re.compile(r"\\b(?:scope|context|agent_context)=([A-Za-z0-9_-]+)", re.IGNORECASE)
_SERVER_AGENT_ID_PATTERN = re.compile(r"\\bagent_id=([^\s,]+)", re.IGNORECASE)
_AGENT_CONTEXT_HEADER = "X-Borealis-Agent-Context"


def _canonical_server_scope(raw: Optional[str]) -> Optional[str]:
    if not raw:
        return None
    value = "".join(ch for ch in str(raw) if ch.isalnum() or ch in ("_", "-"))
    if not value:
        return None
    return value.upper()


def _scope_from_agent_id(agent_id: Optional[str]) -> Optional[str]:
    candidate = _canonical_server_scope(agent_id)
    if not candidate:
        return None
    if candidate.endswith("_SYSTEM"):
        return "SYSTEM"
    if candidate.endswith("_CURRENTUSER"):
        return "CURRENTUSER"
    return candidate


def _infer_server_scope(message: str, explicit: Optional[str]) -> Optional[str]:
    scope = _canonical_server_scope(explicit)
    if scope:
        return scope
    match = _SERVER_SCOPE_PATTERN.search(message or "")
    if match:
        scope = _canonical_server_scope(match.group(1))
        if scope:
            return scope
    agent_match = _SERVER_AGENT_ID_PATTERN.search(message or "")
    if agent_match:
        scope = _scope_from_agent_id(agent_match.group(1))
        if scope:
            return scope
    return None


def _write_service_log(service: str, msg: str, scope: Optional[str] = None, *, level: str = "INFO"):
    try:
        base = _server_logs_root()
        os.makedirs(base, exist_ok=True)
        path = os.path.join(base, f"{service}.log")
        _rotate_daily(path)
        ts = time.strftime('%Y-%m-%d %H:%M:%S')
        resolved_scope = _infer_server_scope(msg, scope)
        prefix_parts = [f"[{level.upper()}]"]
        if resolved_scope:
            prefix_parts.append(f"[CONTEXT-{resolved_scope}]")
        prefix = "".join(prefix_parts)
        with open(path, 'a', encoding='utf-8') as fh:
            fh.write(f'[{ts}] {prefix} {msg}\n')
    except Exception:
        pass


def _mask_server_value(value: str, *, prefix: int = 4, suffix: int = 4) -> str:
    try:
        if not value:
            return ''
        stripped = value.strip()
        if len(stripped) <= prefix + suffix:
            return '*' * len(stripped)
        return f"{stripped[:prefix]}***{stripped[-suffix:]}"
    except Exception:
        return '***'


def _summarize_socket_headers(headers) -> str:
    try:
        rendered = []
        for key, value in headers.items():
            lowered = key.lower()
            display = value
            if lowered == 'authorization':
                if isinstance(value, str) and value.lower().startswith('bearer '):
                    token = value.split(' ', 1)[1]
                    display = f"Bearer {_mask_server_value(token)}"
                else:
                    display = _mask_server_value(str(value))
            elif lowered == 'cookie':
                display = '<redacted>'
            rendered.append(f"{key}={display}")
        return ", ".join(rendered)
    except Exception:
        return '<header-summary-unavailable>'


# =============================================================================
# Section: Repository Hash Tracking
# =============================================================================
# Cache GitHub repository heads so agents can poll without rate limit pressure.
_REPO_HEAD_CACHE: Dict[str, Tuple[str, float]] = {}
_REPO_HEAD_LOCK = Lock()

_CACHE_ROOT = os.environ.get('BOREALIS_CACHE_DIR')
if _CACHE_ROOT:
    _CACHE_ROOT = os.path.abspath(_CACHE_ROOT)
else:
    _CACHE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), 'cache'))
_REPO_HASH_CACHE_FILE = os.path.join(_CACHE_ROOT, 'repo_hash_cache.json')

_DEFAULT_REPO = os.environ.get('BOREALIS_REPO', 'bunny-lab-io/Borealis')
_DEFAULT_BRANCH = os.environ.get('BOREALIS_REPO_BRANCH', 'main')
try:
    _REPO_HASH_INTERVAL = int(os.environ.get('BOREALIS_REPO_HASH_REFRESH', '60'))
except ValueError:
    _REPO_HASH_INTERVAL = 60
_REPO_HASH_INTERVAL = max(30, min(_REPO_HASH_INTERVAL, 3600))
_REPO_HASH_WORKER_STARTED = False
_REPO_HASH_WORKER_LOCK = Lock()

DB_PATH = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", "database.db"))
os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)

_GITHUB_TOKEN_CACHE: Dict[str, Any] = {"token": None, "loaded_at": 0.0, "known": False}
_GITHUB_TOKEN_LOCK = Lock()

TLS_CERT_PATH, TLS_KEY_PATH, TLS_BUNDLE_PATH = certificates.certificate_paths()
os.environ.setdefault("BOREALIS_TLS_CERT", TLS_CERT_PATH)
os.environ.setdefault("BOREALIS_TLS_KEY", TLS_KEY_PATH)
os.environ.setdefault("BOREALIS_TLS_BUNDLE", TLS_BUNDLE_PATH)
try:
    os.environ.setdefault("BOREALIS_CERT_DIR", str(Path(TLS_CERT_PATH).resolve().parent))
except Exception:
    pass

JWT_SERVICE = jwt_service_module.load_service()
SCRIPT_SIGNER = signing.load_signer()
IP_RATE_LIMITER = SlidingWindowRateLimiter()
FP_RATE_LIMITER = SlidingWindowRateLimiter()
AUTH_RATE_LIMITER = SlidingWindowRateLimiter()
ENROLLMENT_NONCE_CACHE = NonceCache()
DPOP_VALIDATOR = DPoPValidator()
DEVICE_AUTH_MANAGER: Optional[DeviceAuthManager] = None


def _set_cached_github_token(token: Optional[str]) -> None:
    with _GITHUB_TOKEN_LOCK:
        _GITHUB_TOKEN_CACHE["token"] = token if token else None
        _GITHUB_TOKEN_CACHE["loaded_at"] = time.time()
        _GITHUB_TOKEN_CACHE["known"] = True


def _load_github_token_from_db(*, force_refresh: bool = False) -> Optional[str]:
    now = time.time()
    with _GITHUB_TOKEN_LOCK:
        if (
            not force_refresh
            and _GITHUB_TOKEN_CACHE.get("known")
            and now - (_GITHUB_TOKEN_CACHE.get("loaded_at") or 0.0) < 15.0
        ):
            return _GITHUB_TOKEN_CACHE.get("token")  # type: ignore[return-value]

    conn: Optional[sqlite3.Connection] = None
    token: Optional[str] = None
    try:
        conn = sqlite3.connect(DB_PATH, timeout=5)
        cur = conn.cursor()
        cur.execute("SELECT token FROM github_token LIMIT 1")
        row = cur.fetchone()
        if row and row[0]:
            candidate = str(row[0]).strip()
            token = candidate or None
    except sqlite3.OperationalError:
        token = None
    except Exception as exc:
        _write_service_log("server", f"github token lookup failed: {exc}")
        token = None
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass

    _set_cached_github_token(token)
    return token


def _github_api_token(*, force_refresh: bool = False) -> Optional[str]:
    token = _load_github_token_from_db(force_refresh=force_refresh)
    if token:
        return token
    env_token = os.environ.get("BOREALIS_GITHUB_TOKEN") or os.environ.get("GITHUB_TOKEN")
    if env_token:
        env_token = env_token.strip()
        return env_token or None
    return None


def _verify_github_token(token: Optional[str]) -> Dict[str, Any]:
    if not token:
        return {
            "valid": False,
            "message": "API Token Not Configured",
            "status": "missing",
            "rate_limit": None,
        }

    headers = {
        "Accept": "application/vnd.github+json",
        "User-Agent": "Borealis-Server",
        "Authorization": f"Bearer {token}",
    }
    try:
        resp = requests.get(
            f"https://api.github.com/repos/{_DEFAULT_REPO}/branches/{_DEFAULT_BRANCH}",
            headers=headers,
            timeout=20,
        )
        limit_header = resp.headers.get("X-RateLimit-Limit")
        try:
            limit_value = int(limit_header) if limit_header is not None else None
        except (TypeError, ValueError):
            limit_value = None

        if resp.status_code == 200:
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

        if resp.status_code == 401:
            return {
                "valid": False,
                "message": "API Token Invalid",
                "status": "invalid",
                "rate_limit": limit_value,
                "error": resp.text[:200],
            }

        return {
            "valid": False,
            "message": f"GitHub API error (HTTP {resp.status_code})",
            "status": "error",
            "rate_limit": limit_value,
            "error": resp.text[:200],
        }
    except Exception as exc:
        return {
            "valid": False,
            "message": f"API Token validation error: {exc}",
            "status": "error",
            "rate_limit": None,
            "error": str(exc),
        }


def _hydrate_repo_hash_cache_from_disk() -> None:
    try:
        if not os.path.isfile(_REPO_HASH_CACHE_FILE):
            return
        with open(_REPO_HASH_CACHE_FILE, 'r', encoding='utf-8') as fh:
            payload = json.load(fh)
        entries = payload.get('entries') if isinstance(payload, dict) else None
        if not isinstance(entries, dict):
            return
        now = time.time()
        with _REPO_HEAD_LOCK:
            for key, entry in entries.items():
                if not isinstance(entry, dict):
                    continue
                sha = (entry.get('sha') or '').strip()
                if not sha:
                    continue
                ts_raw = entry.get('ts')
                try:
                    ts = float(ts_raw)
                except (TypeError, ValueError):
                    ts = now
                _REPO_HEAD_CACHE[key] = (sha, ts)
    except Exception as exc:
        _write_service_log('server', f'failed to hydrate repo hash cache: {exc}')


def _persist_repo_hash_cache() -> None:
    snapshot: Dict[str, Tuple[str, float]]
    with _REPO_HEAD_LOCK:
        snapshot = {
            key: (sha, ts)
            for key, (sha, ts) in _REPO_HEAD_CACHE.items()
            if sha
        }
    try:
        if not snapshot:
            try:
                os.remove(_REPO_HASH_CACHE_FILE)
            except FileNotFoundError:
                pass
            except Exception as exc:
                _write_service_log('server', f'failed to remove repo hash cache file: {exc}')
            return
        os.makedirs(_CACHE_ROOT, exist_ok=True)
        tmp_path = _REPO_HASH_CACHE_FILE + '.tmp'
        payload = {
            'version': 1,
            'entries': {
                key: {'sha': sha, 'ts': ts}
                for key, (sha, ts) in snapshot.items()
            },
        }
        with open(tmp_path, 'w', encoding='utf-8') as fh:
            json.dump(payload, fh)
        os.replace(tmp_path, _REPO_HASH_CACHE_FILE)
    except Exception as exc:
        _write_service_log('server', f'failed to persist repo hash cache: {exc}')


def _fetch_repo_head(owner_repo: str, branch: str = 'main', *, ttl_seconds: int = 60, force_refresh: bool = False) -> Dict[str, Any]:
    """Resolve the latest commit hash for ``owner_repo``/``branch`` via GitHub's REST API.

    The server caches the response so that a fleet of agents can reuse the
    result without exhausting rate limits. ``ttl_seconds`` bounds how long a
    cached value is considered fresh. When ``force_refresh`` is True the cache
    is bypassed and a new request is attempted immediately.
    """

    key = f"{owner_repo}:{branch}"
    now = time.time()

    with _REPO_HEAD_LOCK:
        cached = _REPO_HEAD_CACHE.get(key)

    cached_sha: Optional[str] = None
    cached_ts: Optional[float] = None
    cached_age: Optional[float] = None
    if cached:
        cached_sha, cached_ts = cached
        cached_age = max(0.0, now - cached_ts)

    if cached_sha and not force_refresh and cached_age is not None and cached_age < max(30, ttl_seconds):
        return {
            'sha': cached_sha,
            'cached': True,
            'age_seconds': cached_age,
            'error': None,
            'source': 'cache',
        }

    headers = {
        'Accept': 'application/vnd.github+json',
        'User-Agent': 'Borealis-Server'
    }
    token = _github_api_token(force_refresh=force_refresh)
    if token:
        headers['Authorization'] = f'Bearer {token}'

    error_msg: Optional[str] = None
    sha: Optional[str] = None
    try:
        resp = requests.get(
            f'https://api.github.com/repos/{owner_repo}/branches/{branch}',
            headers=headers,
            timeout=20,
        )
        if resp.status_code == 200:
            data = resp.json()
            sha = (data.get('commit') or {}).get('sha')
        else:
            error_msg = f'GitHub REST API repo head lookup failed: HTTP {resp.status_code} {resp.text[:200]}'
    except Exception as exc:  # pragma: no cover - defensive logging
        error_msg = f'GitHub REST API repo head lookup raised: {exc}'

    if sha:
        sha = sha.strip()
        with _REPO_HEAD_LOCK:
            _REPO_HEAD_CACHE[key] = (sha, now)
        _persist_repo_hash_cache()
        return {
            'sha': sha,
            'cached': False,
            'age_seconds': 0.0,
            'error': None,
            'source': 'github',
        }

    if error_msg:
        _write_service_log('server', error_msg)

    if cached_sha is not None:
        return {
            'sha': cached_sha,
            'cached': True,
            'age_seconds': cached_age,
            'error': error_msg or 'using cached value',
            'source': 'cache-stale',
        }

    return {
        'sha': None,
        'cached': False,
        'age_seconds': None,
        'error': error_msg or 'unable to resolve repository head',
        'source': 'github',
    }


def _refresh_default_repo_hash(force: bool = False) -> Dict[str, Any]:
    ttl = max(30, _REPO_HASH_INTERVAL)
    try:
        return _fetch_repo_head(_DEFAULT_REPO, _DEFAULT_BRANCH, ttl_seconds=ttl, force_refresh=force)
    except Exception as exc:  # pragma: no cover - defensive logging
        _write_service_log('server', f'default repo hash refresh failed: {exc}')
        raise


def _repo_hash_background_worker():
    interval = max(30, _REPO_HASH_INTERVAL)
    # Fetch immediately, then sleep between refreshes
    while True:
        try:
            _refresh_default_repo_hash(force=True)
        except Exception:
            # _refresh_default_repo_hash already logs details
            pass
        eventlet.sleep(interval)


def _ensure_repo_hash_worker():
    global _REPO_HASH_WORKER_STARTED
    with _REPO_HASH_WORKER_LOCK:
        if _REPO_HASH_WORKER_STARTED:
            return
        _REPO_HASH_WORKER_STARTED = True
        try:
            eventlet.spawn_n(_repo_hash_background_worker)
        except Exception as exc:
            _REPO_HASH_WORKER_STARTED = False
            _write_service_log('server', f'failed to start repo hash worker: {exc}')


def _ansible_log_server(msg: str):
    _write_service_log('ansible', msg)

# =============================================================================
# Section: Local Python Integrations
# =============================================================================
# Shared constants and helper imports that bridge into bundled Python services.
DEFAULT_SERVICE_ACCOUNT = '.\\svcBorealis'
LEGACY_SERVICE_ACCOUNTS = {'.\\svcBorealisAnsibleRunner', 'svcBorealisAnsibleRunner'}

from Python_API_Endpoints.ocr_engines import run_ocr_on_base64
from Python_API_Endpoints.script_engines import run_powershell_script
from job_scheduler import register as register_job_scheduler
from job_scheduler import set_online_lookup as scheduler_set_online_lookup
from job_scheduler import set_server_ansible_runner as scheduler_set_server_runner
from job_scheduler import set_credential_fetcher as scheduler_set_credential_fetcher

# =============================================================================
# Section: Runtime Stack Configuration
# =============================================================================
# Configure Flask, reverse-proxy awareness, CORS, and Socket.IO transport.

_STATIC_CANDIDATES = [
    os.path.join(os.path.dirname(__file__), '../web-interface/build'),
    os.path.join(os.path.dirname(__file__), 'WebUI/build'),
    os.path.join(os.path.dirname(__file__), '../WebUI/build'),
]

_resolved_static_folder = None
for candidate in _STATIC_CANDIDATES:
    absolute_candidate = os.path.abspath(candidate)
    if os.path.isdir(absolute_candidate):
        _resolved_static_folder = absolute_candidate
        break

if not _resolved_static_folder:
    # Fall back to the first candidate so Flask still initialises; individual
    # requests will 404 until a build exists, matching the previous behaviour.
    _resolved_static_folder = os.path.abspath(_STATIC_CANDIDATES[0])

app = Flask(
    __name__,
    static_folder=_resolved_static_folder,
    static_url_path=''
)

# Respect reverse proxy headers for scheme/host so cookies and redirects behave
app.wsgi_app = ProxyFix(app.wsgi_app, x_proto=1, x_host=1)

# Enable CORS on All Routes (allow credentials). Optionally lock down via env.
_cors_origins = os.environ.get('BOREALIS_CORS_ORIGINS')  # e.g. "https://ui.example.com,https://admin.example.com"
if _cors_origins:
    origins = [o.strip() for o in _cors_origins.split(',') if o.strip()]
    CORS(app, supports_credentials=True, origins=origins)
else:
    CORS(app, supports_credentials=True)

# Basic secret key for session cookies (can be overridden via env)
app.secret_key = os.environ.get('BOREALIS_SECRET', 'borealis-dev-secret')

# Session cookie policy (tunable for dev/prod/reverse proxy)
# Defaults keep dev working; override via env in production/proxy scenarios.
app.config.update(
    SESSION_COOKIE_HTTPONLY=True,
    SESSION_COOKIE_SAMESITE=os.environ.get('BOREALIS_COOKIE_SAMESITE', 'Lax'),  # set to 'None' when UI/API are on different sites
    SESSION_COOKIE_SECURE=(os.environ.get('BOREALIS_COOKIE_SECURE', '0').lower() in ('1', 'true', 'yes')),
)
app.config.setdefault("PREFERRED_URL_SCHEME", "https")

# Optionally pin cookie domain if served under a fixed hostname (leave unset for host-only/IP dev)
_cookie_domain = os.environ.get('BOREALIS_COOKIE_DOMAIN')  # e.g. ".example.com" or "borealis.bunny-lab.io"
if _cookie_domain:
    app.config['SESSION_COOKIE_DOMAIN'] = _cookie_domain

socketio = SocketIO(
    app,
    cors_allowed_origins="*",
    async_mode="eventlet",
    engineio_options={
        'max_http_buffer_size':       100_000_000,
        'max_websocket_message_size': 100_000_000
    }
)

_hydrate_repo_hash_cache_from_disk()
_ensure_repo_hash_worker()

# Endpoint: / and /<path> — serve the built Vite SPA assets.

@app.route('/', defaults={'path': ''})
@app.route('/<path:path>')
def serve_dist(path):
    full_path = os.path.join(app.static_folder, path)
    if path and os.path.isfile(full_path):
        return send_from_directory(app.static_folder, path)
    else:
        # SPA entry point
        return send_from_directory(app.static_folder, 'index.html')


@app.errorhandler(404)
def spa_fallback(error):
    """Serve the SPA entry point for unknown front-end routes.

    When the browser refreshes on a client-side route (e.g. ``/devices``),
    Flask would normally raise a 404 because no matching server route exists.
    We intercept that here and return ``index.html`` so the React router can
    take over. API and asset paths continue to surface their original 404s.
    """

    # Preserve 404s for API endpoints, websocket upgrades, and obvious asset
    # requests (anything containing an extension).
    request_path = (request.path or '').strip()
    if request_path.startswith('/api') or request_path.startswith('/socket.io'):
        return error
    if '.' in os.path.basename(request_path):
        return error
    if request.method not in {'GET', 'HEAD'}:
        return error

    try:
        return send_from_directory(app.static_folder, 'index.html')
    except Exception:
        # If the build is missing we fall back to the original 404 response so
        # the caller still receives an accurate status code.
        return error


# Endpoint: /health — liveness probe for orchestrators.

@app.route("/health")
def health():
    return jsonify({"status": "ok"})


# Endpoint: /api/repo/current_hash — cached GitHub head lookup for agents.
@app.route("/api/repo/current_hash", methods=["GET"])
def api_repo_current_hash():
    try:
        repo = (request.args.get('repo') or _DEFAULT_REPO).strip()
        branch = (request.args.get('branch') or _DEFAULT_BRANCH).strip()
        refresh_flag = (request.args.get('refresh') or '').strip().lower()
        ttl_raw = request.args.get('ttl')
        if '/' not in repo:
            return jsonify({"error": "repo must be in the form owner/name"}), 400
        try:
            ttl = int(ttl_raw) if ttl_raw else _REPO_HASH_INTERVAL
        except ValueError:
            ttl = _REPO_HASH_INTERVAL
        ttl = max(30, min(ttl, 3600))
        force_refresh = refresh_flag in {'1', 'true', 'yes', 'force', 'refresh'}
        if repo == _DEFAULT_REPO and branch == _DEFAULT_BRANCH:
            result = _refresh_default_repo_hash(force=force_refresh)
        else:
            result = _fetch_repo_head(repo, branch, ttl_seconds=ttl, force_refresh=force_refresh)
        sha = (result.get('sha') or '').strip()
        payload = {
            'repo': repo,
            'branch': branch,
            'sha': sha if sha else None,
            'cached': bool(result.get('cached')),
            'age_seconds': result.get('age_seconds'),
            'source': result.get('source'),
        }
        if result.get('error'):
            payload['error'] = result['error']
        if sha:
            return jsonify(payload)
        return jsonify(payload), 503
    except Exception as exc:
        _write_service_log('server', f'/api/repo/current_hash error: {exc}')
        return jsonify({"error": "internal error"}), 500


def _lookup_agent_hash_record(agent_id: str) -> Optional[Dict[str, Any]]:
    agent_id = (agent_id or '').strip()
    if not agent_id:
        return None

    info = registered_agents.get(agent_id) or {}
    candidate = (info.get('agent_hash') or '').strip()
    candidate_guid = _normalize_guid(info.get('agent_guid')) if info.get('agent_guid') else ''
    hostname = (info.get('hostname') or '').strip()
    if candidate:
        payload: Dict[str, Any] = {
            'agent_id': agent_id,
            'agent_hash': candidate,
            'source': 'memory',
        }
        if hostname:
            payload['hostname'] = hostname
        if candidate_guid:
            payload['agent_guid'] = candidate_guid
        return payload

    conn = None
    try:
        conn = _db_conn()
        cur = conn.cursor()
        rows = _device_rows_for_agent(cur, agent_id)
        effective_hostname = hostname or None
        if rows:
            if not effective_hostname:
                effective_hostname = rows[0].get('hostname') or effective_hostname
            for row in rows:
                if row.get('matched'):
                    normalized_hash = (row.get('agent_hash') or '').strip()
                    if not normalized_hash:
                        details = row.get('details') or {}
                        try:
                            normalized_hash = ((details.get('summary') or {}).get('agent_hash') or '').strip()
                        except Exception:
                            normalized_hash = ''
                    if normalized_hash:
                        payload = {
                            'agent_id': agent_id,
                            'agent_hash': normalized_hash,
                            'hostname': row.get('hostname') or effective_hostname,
                            'source': 'database',
                        }
                        row_guid = _normalize_guid(row.get('guid')) if row.get('guid') else ''
                        if row_guid:
                            payload['agent_guid'] = row_guid
                        return payload
            first = rows[0]
            fallback_hash = (first.get('agent_hash') or '').strip()
            if not fallback_hash:
                details = first.get('details') or {}
                try:
                    fallback_hash = ((details.get('summary') or {}).get('agent_hash') or '').strip()
                except Exception:
                    fallback_hash = ''
            if fallback_hash:
                payload = {
                    'agent_id': agent_id,
                    'agent_hash': fallback_hash,
                    'hostname': first.get('hostname') or effective_hostname,
                    'source': 'database',
                }
                row_guid = _normalize_guid(first.get('guid')) if first.get('guid') else ''
                if row_guid:
                    payload['agent_guid'] = row_guid
                return payload
        cur.execute(f'SELECT {_device_column_sql()} FROM {DEVICE_TABLE}')
        for row in cur.fetchall():
            record = _row_to_device_dict(row, _DEVICE_TABLE_COLUMNS)
            snapshot = _assemble_device_snapshot(record)
            summary = snapshot.get('summary', {})
            summary_agent_id = (summary.get('agent_id') or '').strip()
            if summary_agent_id != agent_id:
                continue
            normalized_hash = (snapshot.get('agent_hash') or '').strip()
            if not normalized_hash:
                normalized_hash = (summary.get('agent_hash') or '').strip()
            if normalized_hash:
                payload = {
                    'agent_id': agent_id,
                    'agent_hash': normalized_hash,
                    'hostname': snapshot.get('hostname'),
                    'source': 'database',
                }
                normalized_guid = snapshot.get('agent_guid') or ''
                if not normalized_guid:
                    normalized_guid = _normalize_guid(summary.get('agent_guid')) if summary.get('agent_guid') else ''
                if normalized_guid:
                    payload['agent_guid'] = normalized_guid
                return payload
    finally:
        if conn:
            try:
                conn.close()
            except Exception:
                pass
    return None


def _lookup_agent_hash_by_guid(agent_guid: str) -> Optional[Dict[str, Any]]:
    normalized_guid = _normalize_guid(agent_guid)
    if not normalized_guid:
        return None

    # Prefer in-memory record when available
    for aid, rec in registered_agents.items():
        try:
            if _normalize_guid(rec.get('agent_guid')) == normalized_guid:
                candidate_hash = (rec.get('agent_hash') or '').strip()
                if candidate_hash:
                    payload: Dict[str, Any] = {
                        'agent_guid': normalized_guid,
                        'agent_hash': candidate_hash,
                        'source': 'memory',
                    }
                    hostname = (rec.get('hostname') or '').strip()
                    if hostname:
                        payload['hostname'] = hostname
                    if aid:
                        payload['agent_id'] = aid
                    return payload
                break
        except Exception:
            continue

    conn = None
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            f'SELECT {_device_column_sql()} FROM {DEVICE_TABLE} WHERE LOWER(guid) = ?',
            (normalized_guid.lower(),),
        )
        row = cur.fetchone()
        if not row:
            return None
        record = _row_to_device_dict(row, _DEVICE_TABLE_COLUMNS)
        snapshot = _assemble_device_snapshot(record)
        summary = snapshot.get('summary', {})
        agent_id = (summary.get('agent_id') or '').strip()
        normalized_hash = (snapshot.get('agent_hash') or summary.get('agent_hash') or '').strip()
        payload = {
            'agent_guid': normalized_guid,
            'agent_hash': normalized_hash,
            'hostname': snapshot.get('hostname'),
            'source': 'database',
        }
        if agent_id:
            payload['agent_id'] = agent_id
        return payload
    finally:
        if conn:
            try:
                conn.close()
            except Exception:
                pass


def _collect_agent_hash_records() -> List[Dict[str, Any]]:
    """Aggregate known agent hash records from memory and the database."""

    records: List[Dict[str, Any]] = []
    key_to_index: Dict[str, int] = {}

    def _register(
        agent_id: Optional[str],
        agent_guid: Optional[str],
        hostname: Optional[str],
        agent_hash: Optional[str],
        source: Optional[str],
    ) -> None:
        normalized_id = (agent_id or '').strip()
        normalized_guid = _normalize_guid(agent_guid)
        normalized_hostname = (hostname or '').strip()
        normalized_hash = (agent_hash or '').strip()
        keys: List[str] = []
        if normalized_id:
            keys.append(f'id:{normalized_id.lower()}')
        if normalized_guid:
            keys.append(f'guid:{normalized_guid.lower()}')
        if normalized_hostname:
            keys.append(f'host:{normalized_hostname.lower()}')

        if not keys:
            records.append(
                {
                    'agent_id': normalized_id or None,
                    'agent_guid': normalized_guid or None,
                    'hostname': normalized_hostname or None,
                    'agent_hash': normalized_hash or None,
                    'source': source or None,
                }
            )
            return

        existing_idx: Optional[int] = None
        for key in keys:
            if key in key_to_index:
                existing_idx = key_to_index[key]
                break

        if existing_idx is None:
            idx = len(records)
            records.append(
                {
                    'agent_id': normalized_id or None,
                    'agent_guid': normalized_guid or None,
                    'hostname': normalized_hostname or None,
                    'agent_hash': normalized_hash or None,
                    'source': source or None,
                }
            )
            for key in keys:
                key_to_index[key] = idx
            return

        existing = records[existing_idx]
        prev_hash = (existing.get('agent_hash') or '').strip()
        if normalized_hash and (not prev_hash or source == 'memory'):
            existing['agent_hash'] = normalized_hash
            if source:
                existing['source'] = source
        if normalized_id and not (existing.get('agent_id') or '').strip():
            existing['agent_id'] = normalized_id
        if normalized_guid and not (existing.get('agent_guid') or '').strip():
            existing['agent_guid'] = normalized_guid
        if normalized_hostname and not (existing.get('hostname') or '').strip():
            existing['hostname'] = normalized_hostname
        if source == 'memory':
            existing['source'] = 'memory'
        for key in keys:
            key_to_index[key] = existing_idx

    conn = None
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(f'SELECT {_device_column_sql()} FROM {DEVICE_TABLE}')
        for row in cur.fetchall():
            record = _row_to_device_dict(row, _DEVICE_TABLE_COLUMNS)
            snapshot = _assemble_device_snapshot(record)
            summary = snapshot.get('summary', {})
            summary_hash = (summary.get('agent_hash') or '').strip()
            summary_guid = summary.get('agent_guid') or snapshot.get('agent_guid') or ''
            summary_agent_id = (summary.get('agent_id') or '').strip()
            normalized_hash = (snapshot.get('agent_hash') or '').strip() or summary_hash
            _register(
                summary_agent_id or None,
                summary_guid or snapshot.get('agent_guid'),
                snapshot.get('hostname'),
                normalized_hash,
                'database',
            )
    except Exception as exc:
        _write_service_log('server', f'collect_agent_hash_records database error: {exc}')
    finally:
        if conn:
            try:
                conn.close()
            except Exception:
                pass

    try:
        for agent_id, info in (registered_agents or {}).items():
            mode = _normalize_service_mode(info.get('service_mode'), agent_id)
            if mode != 'currentuser':
                if agent_id and isinstance(agent_id, str) and agent_id.lower().endswith('-script'):
                    continue
                if info.get('is_script_agent'):
                    continue
            _register(
                agent_id,
                info.get('agent_guid'),
                info.get('hostname'),
                info.get('agent_hash'),
                'memory',
            )
    except Exception as exc:
        _write_service_log('server', f'collect_agent_hash_records memory error: {exc}')

    records.sort(
        key=lambda r: (
            (r.get('hostname') or '').lower(),
            (r.get('agent_id') or '').lower(),
        )
    )
    sanitized: List[Dict[str, Any]] = []
    for rec in records:
        sanitized.append(
            {
                'agent_id': rec.get('agent_id') or None,
                'agent_guid': rec.get('agent_guid') or None,
                'hostname': rec.get('hostname') or None,
                'agent_hash': (rec.get('agent_hash') or '').strip() or None,
                'source': rec.get('source') or None,
            }
        )
    return sanitized


def _apply_agent_hash_update(agent_id: str, agent_hash: str, agent_guid: Optional[str] = None) -> Tuple[Dict[str, Any], int]:
    agent_id = (agent_id or '').strip()
    agent_hash = (agent_hash or '').strip()
    normalized_guid = _normalize_guid(agent_guid)
    if not agent_hash or (not agent_id and not normalized_guid):
        return {'error': 'agent_hash and agent_guid or agent_id required'}, 400

    conn = None
    hostname = None
    resolved_agent_id = agent_id
    response_payload: Optional[Dict[str, Any]] = None
    status_code = 200
    try:
        conn = _db_conn()
        cur = conn.cursor()
        updated_via_guid = False
        if normalized_guid:
            cur.execute(
                f'SELECT {_device_column_sql()} FROM {DEVICE_TABLE} WHERE LOWER(guid) = ?',
                (normalized_guid.lower(),),
            )
            row = cur.fetchone()
            if row:
                updated_via_guid = True
                record = _row_to_device_dict(row, _DEVICE_TABLE_COLUMNS)
                snapshot = _assemble_device_snapshot(record)
                hostname = snapshot.get('hostname')
                description = snapshot.get('description')
                details = snapshot.get('details', {})
                summary = details.setdefault('summary', {})
                if not resolved_agent_id:
                    resolved_agent_id = (summary.get('agent_id') or '').strip()
                if resolved_agent_id:
                    summary['agent_id'] = resolved_agent_id
                summary['agent_hash'] = agent_hash
                summary['agent_guid'] = normalized_guid
                existing_created_at = snapshot.get('created_at')
                existing_hash = snapshot.get('agent_hash')
                _device_upsert(
                    cur,
                    hostname,
                    description,
                    details,
                    existing_created_at,
                    agent_hash=agent_hash or existing_hash,
                    guid=normalized_guid,
                )
                conn.commit()
            elif not agent_id:
                response_payload = {
                    'status': 'ignored',
                    'agent_guid': normalized_guid,
                    'agent_hash': agent_hash,
                }

        if response_payload is None and not updated_via_guid:
            target = None
            rows = _device_rows_for_agent(cur, resolved_agent_id)
            for row in rows:
                if row.get('matched'):
                    target = row
                    break
            if not target and rows:
                target = rows[0]
            if not target:
                response_payload = {
                    'status': 'ignored',
                    'agent_id': resolved_agent_id,
                    'agent_hash': agent_hash,
                }
            else:
                hostname = target.get('hostname')
                details = target.get('details') or {}
                summary = details.setdefault('summary', {})
                summary['agent_hash'] = agent_hash
                if normalized_guid:
                    summary['agent_guid'] = normalized_guid
                if resolved_agent_id:
                    summary['agent_id'] = resolved_agent_id
                snapshot_existing = _load_device_snapshot(cur, hostname=hostname)
                description = snapshot_existing.get('description') if snapshot_existing else None
                existing_created_at = snapshot_existing.get('created_at') if snapshot_existing else None
                existing_hash = snapshot_existing.get('agent_hash') if snapshot_existing else None
                effective_guid = normalized_guid or target.get('guid')
                _device_upsert(
                    cur,
                    hostname,
                    description,
                    details,
                    existing_created_at,
                    agent_hash=agent_hash or existing_hash,
                    guid=effective_guid,
                )
                conn.commit()
                if not normalized_guid:
                    normalized_guid = _normalize_guid(target.get('guid')) if target.get('guid') else ''
                if not normalized_guid:
                    normalized_guid = _normalize_guid((summary.get('agent_guid') or '')) if summary else ''
        elif response_payload is None and normalized_guid and not hostname:
            # GUID provided and update attempted but hostname not resolved
            response_payload = {
                'status': 'ignored',
                'agent_guid': normalized_guid,
                'agent_hash': agent_hash,
            }
    except Exception as exc:
        if conn:
            try:
                conn.rollback()
            except Exception:
                pass
        _write_service_log('server', f'/api/agent/hash error: {exc}')
        return {'error': 'internal error'}, 500
    finally:
        if conn:
            try:
                conn.close()
            except Exception:
                pass

    if response_payload is not None:
        return response_payload, status_code

    normalized_hash = agent_hash
    if normalized_guid:
        try:
            for aid, rec in registered_agents.items():
                guid_candidate = _normalize_guid(rec.get('agent_guid'))
                if guid_candidate == normalized_guid or (resolved_agent_id and aid == resolved_agent_id):
                    rec['agent_hash'] = normalized_hash
                    rec['agent_guid'] = normalized_guid
                    if not resolved_agent_id:
                        resolved_agent_id = aid
        except Exception:
            pass
    if resolved_agent_id and resolved_agent_id in registered_agents:
        registered_agents[resolved_agent_id]['agent_hash'] = normalized_hash
        if normalized_guid:
            registered_agents[resolved_agent_id]['agent_guid'] = normalized_guid
    if hostname:
        try:
            for aid, rec in registered_agents.items():
                if rec.get('hostname') and rec['hostname'] == hostname:
                    rec['agent_hash'] = normalized_hash
                    if normalized_guid:
                        rec['agent_guid'] = normalized_guid
        except Exception:
            pass

    payload: Dict[str, Any] = {
        'status': 'ok',
        'agent_hash': agent_hash,
    }
    if resolved_agent_id:
        payload['agent_id'] = resolved_agent_id
    if hostname:
        payload['hostname'] = hostname
    if normalized_guid:
        payload['agent_guid'] = normalized_guid
    return payload, 200


# Endpoint: /api/agent/hash — methods GET, POST.

@app.route("/api/agent/hash", methods=["GET", "POST"])
def api_agent_hash():
    if request.method == 'GET':
        agent_guid = _normalize_guid(request.args.get('agent_guid'))
        agent_id = (request.args.get('agent_id') or request.args.get('id') or '').strip()
        if not agent_guid and not agent_id:
            data = request.get_json(silent=True) or {}
            agent_guid = _normalize_guid(data.get('agent_guid')) if data else agent_guid
            agent_id = (data.get('agent_id') or '').strip() if data else agent_id
        try:
            record = None
            if agent_guid:
                record = _lookup_agent_hash_by_guid(agent_guid)
            if not record and agent_id:
                record = _lookup_agent_hash_record(agent_id)
        except Exception as exc:
            _write_service_log('server', f'/api/agent/hash lookup error: {exc}')
            return jsonify({'error': 'internal error'}), 500
        if record:
            return jsonify(record)
        return jsonify({'error': 'agent hash not found'}), 404

    data = request.get_json(silent=True) or {}
    agent_id = (data.get('agent_id') or '').strip()
    agent_hash = (data.get('agent_hash') or '').strip()
    agent_guid = _normalize_guid(data.get('agent_guid')) if data else None
    payload, status = _apply_agent_hash_update(agent_id, agent_hash, agent_guid)
    return jsonify(payload), status


# Endpoint: /api/agent/hash_list — methods GET.

@app.route("/api/agent/hash_list", methods=["GET"])
def api_agent_hash_list():
    try:
        records = _collect_agent_hash_records()
        return jsonify({'agents': records})
    except Exception as exc:
        _write_service_log('server', f'/api/agent/hash_list error: {exc}')
        return jsonify({'error': 'internal error'}), 500


# Endpoint: /api/server/time — return current UTC timestamp metadata.

@app.route("/api/server/time", methods=["GET"])
def api_server_time():
    try:
        from datetime import datetime, timezone
        now_local = datetime.now().astimezone()
        now_utc = datetime.now(timezone.utc)
        tzinfo = now_local.tzinfo
        offset = tzinfo.utcoffset(now_local) if tzinfo else None
        # Friendly display string, e.g., "September 23rd 2025 @ 12:49AM"
        def _ordinal(n: int) -> str:
            if 11 <= (n % 100) <= 13:
                suf = 'th'
            else:
                suf = {1: 'st', 2: 'nd', 3: 'rd'}.get(n % 10, 'th')
            return f"{n}{suf}"
        month = now_local.strftime("%B")
        day_disp = _ordinal(now_local.day)
        year = now_local.strftime("%Y")
        hour24 = now_local.hour
        hour12 = hour24 % 12 or 12
        minute = now_local.minute
        ampm = "AM" if hour24 < 12 else "PM"
        display = f"{month} {day_disp} {year} @ {hour12}:{minute:02d}{ampm}"
        return jsonify({
            "epoch": int(now_local.timestamp()),
            "iso": now_local.isoformat(),
            "utc_iso": now_utc.isoformat().replace("+00:00", "Z"),
            "timezone": str(tzinfo) if tzinfo else "",
            "offset_seconds": int(offset.total_seconds()) if offset else 0,
            "display": display,
        })
    except Exception as e:
        return jsonify({"error": str(e)}), 500

# =============================================================================
# Section: Authentication & User Accounts
# =============================================================================
# Credential storage, MFA helpers, and user management endpoints.

def _now_ts() -> int:
    return int(time.time())


def _sha512_hex(s: str) -> str:
    import hashlib
    return hashlib.sha512((s or '').encode('utf-8')).hexdigest()


def _generate_totp_secret() -> str:
    if not pyotp:
        raise RuntimeError("pyotp is not installed; MFA unavailable")
    return pyotp.random_base32()


def _totp_for_secret(secret: str):
    if not pyotp:
        raise RuntimeError("pyotp is not installed; MFA unavailable")
    normalized = (secret or "").replace(" ", "").strip().upper()
    if not normalized:
        raise ValueError("empty MFA secret")
    return pyotp.TOTP(normalized, digits=6, interval=30)


def _totp_provisioning_uri(secret: str, username: str) -> Optional[str]:
    try:
        totp = _totp_for_secret(secret)
    except Exception:
        return None
    issuer = os.environ.get('BOREALIS_MFA_ISSUER', 'Borealis')
    try:
        return totp.provisioning_uri(name=username, issuer_name=issuer)
    except Exception:
        return None


def _totp_qr_data_uri(payload: str) -> Optional[str]:
    if not payload:
        return None
    if qrcode is None:
        return None
    try:
        img = qrcode.make(payload, box_size=6, border=4)
        buf = io.BytesIO()
        img.save(buf, format="PNG")
        encoded = base64.b64encode(buf.getvalue()).decode("ascii")
        return f"data:image/png;base64,{encoded}"
    except Exception:
        return None


def _db_conn():
    conn = sqlite3.connect(DB_PATH, timeout=15)
    try:
        cur = conn.cursor()
        # Enable better read/write concurrency
        cur.execute("PRAGMA journal_mode=WAL")
        cur.execute("PRAGMA busy_timeout=5000")
        cur.execute("PRAGMA synchronous=NORMAL")
        conn.commit()
    except Exception:
        pass
    return conn


if DEVICE_AUTH_MANAGER is None:
    DEVICE_AUTH_MANAGER = DeviceAuthManager(
        db_conn_factory=_db_conn,
        jwt_service=JWT_SERVICE,
        dpop_validator=DPOP_VALIDATOR,
        log=_write_service_log,
        rate_limiter=AUTH_RATE_LIMITER,
    )

def _update_last_login(username: str) -> None:
    if not username:
        return
    try:
        conn = _db_conn()
        cur = conn.cursor()
        now = _now_ts()
        cur.execute(
            "UPDATE users SET last_login=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
            (now, now, username)
        )
        conn.commit()
        conn.close()
    except Exception:
        pass


def _finalize_login(username: str, role: str):
    session.pop("mfa_pending", None)
    session["username"] = username
    session["role"] = role
    _update_last_login(username)
    token = _make_token(username, role or "User")
    resp = jsonify({"status": "ok", "username": username, "role": role, "token": token})
    samesite = app.config.get("SESSION_COOKIE_SAMESITE", "Lax")
    secure = bool(app.config.get("SESSION_COOKIE_SECURE", False))
    domain = app.config.get("SESSION_COOKIE_DOMAIN", None)
    resp.set_cookie(
        "borealis_auth",
        token,
        httponly=False,
        samesite=samesite,
        secure=secure,
        domain=domain,
        path="/",
    )
    return resp


def _user_row_to_dict(row):
    # id, username, display_name, role, last_login, created_at, updated_at, mfa_enabled?, mfa_secret?
    mfa_enabled = 0
    if len(row) > 7:
        try:
            mfa_enabled = 1 if (row[7] or 0) else 0
        except Exception:
            mfa_enabled = 0
    return {
        "id": row[0],
        "username": row[1],
        "display_name": row[2] or row[1],
        "role": row[3] or "User",
        "last_login": row[4] or 0,
        "created_at": row[5] or 0,
        "updated_at": row[6] or 0,
        "mfa_enabled": mfa_enabled,
    }


def _current_user():
    # Prefer server-side session if present
    u = session.get('username')
    role = session.get('role')
    if u:
        return {"username": u, "role": role or "User"}

    # Otherwise allow token-based auth (Authorization: Bearer <token> or borealis_auth cookie)
    token = None
    auth = request.headers.get('Authorization') or ''
    if auth.lower().startswith('bearer '):
        token = auth.split(' ', 1)[1].strip()
    if not token:
        token = request.cookies.get('borealis_auth')
    if token:
        user = _verify_token(token)
        if user:
            return user
    return None


def _require_login():
    user = _current_user()
    if not user:
        return jsonify({"error": "unauthorized"}), 401
    return None


def _require_admin():
    user = _current_user()
    if not user:
        return jsonify({"error": "unauthorized"}), 401
    if (user.get('role') or '').lower() != 'admin':
        return jsonify({"error": "forbidden"}), 403
    return None


# =============================================================================
# Section: Token & Session Utilities
# =============================================================================
# URL-safe serializers that back login cookies and recovery flows.

def _token_serializer():
    secret = app.secret_key or 'borealis-dev-secret'
    return URLSafeTimedSerializer(secret, salt='borealis-auth')


def _make_token(username: str, role: str) -> str:
    s = _token_serializer()
    payload = {"u": username, "r": role or 'User', "ts": _now_ts()}
    return s.dumps(payload)


def _verify_token(token: str):
    try:
        s = _token_serializer()
        max_age = int(os.environ.get('BOREALIS_TOKEN_TTL_SECONDS', 60*60*24*30))  # 30 days
        data = s.loads(token, max_age=max_age)
        return {"username": data.get('u'), "role": data.get('r') or 'User'}
    except (BadSignature, SignatureExpired, Exception):
        return None


# Endpoint: /api/auth/login — methods POST.

@app.route("/api/auth/login", methods=["POST"])
def api_login():
    payload = request.get_json(silent=True) or {}
    username = (payload.get('username') or '').strip()
    password = payload.get('password')  # plain (optional)
    password_sha512 = (payload.get('password_sha512') or '').strip().lower()
    if not username or (not password and not password_sha512):
        return jsonify({"error": "missing credentials"}), 400

    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            """
            SELECT
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                COALESCE(mfa_enabled, 0) AS mfa_enabled,
                COALESCE(mfa_secret, '') AS mfa_secret
            FROM users WHERE LOWER(username)=LOWER(?)
            """,
            (username,)
        )
        row = cur.fetchone()
        if not row:
            conn.close()
            return jsonify({"error": "invalid username or password"}), 401
        stored_hash = (row[3] or '').lower()
        check_hash = password_sha512 or _sha512_hex(password or '')
        if stored_hash != (check_hash or '').lower():
            conn.close()
            return jsonify({"error": "invalid username or password"}), 401
        role = row[4] or 'User'
        conn.close()
        mfa_enabled = bool(row[8] or 0)
        existing_secret = (row[9] or '').strip()

        session.pop('username', None)
        session.pop('role', None)

        if not mfa_enabled:
            session.pop('mfa_pending', None)
            return _finalize_login(row[1], role)

        # MFA required path
        stage = 'verify' if existing_secret else 'setup'
        pending_token = uuid.uuid4().hex
        pending = {
            "username": row[1],
            "role": role,
            "token": pending_token,
            "stage": stage,
            "expires": _now_ts() + 300  # 5 minutes window
        }
        secret = None
        otpauth_url = None
        qr_image = None
        if stage == 'setup':
            try:
                secret = _generate_totp_secret()
            except Exception as exc:
                return jsonify({"error": f"MFA setup unavailable: {exc}"}), 500
            pending['secret'] = secret
            otpauth_url = _totp_provisioning_uri(secret, row[1])
            if otpauth_url:
                qr_image = _totp_qr_data_uri(otpauth_url)
        else:
            # For verification we rely on stored secret in DB
            pending['secret'] = None
        session['mfa_pending'] = pending
        session.modified = True
        resp_payload = {
            "status": "mfa_required",
            "stage": stage,
            "pending_token": pending_token,
            "username": row[1],
            "role": role,
        }
        if stage == 'setup':
            resp_payload.update({
                "secret": secret,
                "otpauth_url": otpauth_url,
                "qr_image": qr_image,
            })
        return jsonify(resp_payload)
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/auth/logout — methods POST.

@app.route("/api/auth/logout", methods=["POST"])  # simple logout
def api_logout():
    session.clear()
    resp = jsonify({"status": "ok"})
    # Clear token cookie as well
    resp.set_cookie('borealis_auth', '', expires=0, path='/')
    return resp


# Endpoint: /api/auth/mfa/verify — methods POST.

@app.route("/api/auth/mfa/verify", methods=["POST"])
def api_mfa_verify():
    pending = session.get("mfa_pending") or {}
    if not pending or not isinstance(pending, dict):
        return jsonify({"error": "mfa_pending"}), 401
    payload = request.get_json(silent=True) or {}
    token = (payload.get("pending_token") or "").strip()
    code_raw = str(payload.get("code") or "").strip()
    code = "".join(ch for ch in code_raw if ch.isdigit())
    if not token or token != pending.get("token"):
        return jsonify({"error": "invalid_session"}), 401
    if pending.get("expires", 0) < _now_ts():
        session.pop("mfa_pending", None)
        return jsonify({"error": "expired"}), 401
    if len(code) < 6:
        return jsonify({"error": "invalid_code"}), 400
    username = pending.get("username") or ""
    role = pending.get("role") or "User"
    stage = pending.get("stage") or "verify"
    try:
        if stage == "setup":
            secret = pending.get("secret") or ""
            totp = _totp_for_secret(secret)
            if not totp.verify(code, valid_window=1):
                return jsonify({"error": "invalid_code"}), 401
            # Persist the secret only after successful verification
            now = _now_ts()
            conn = _db_conn()
            cur = conn.cursor()
            cur.execute(
                "UPDATE users SET mfa_secret=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
                (secret, now, username)
            )
            conn.commit()
            conn.close()
        else:
            conn = _db_conn()
            cur = conn.cursor()
            cur.execute(
                "SELECT COALESCE(mfa_secret,'') FROM users WHERE LOWER(username)=LOWER(?)",
                (username,)
            )
            row = cur.fetchone()
            conn.close()
            secret = (row[0] or "").strip() if row else ""
            if not secret:
                return jsonify({"error": "mfa_not_configured"}), 403
            totp = _totp_for_secret(secret)
            if not totp.verify(code, valid_window=1):
                return jsonify({"error": "invalid_code"}), 401
        return _finalize_login(username, role)
    except Exception as exc:
        return jsonify({"error": str(exc)}), 500


# Endpoint: /api/auth/me — methods GET.

@app.route("/api/auth/me", methods=["GET"])  # whoami
def api_me():
    user = _current_user()
    if not user:
        return jsonify({"error": "unauthorized"}), 401
    # Enrich with display_name if possible
    username = (user.get('username') or '').strip()
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, username, display_name, role, last_login, created_at, updated_at FROM users WHERE LOWER(username)=LOWER(?)",
            (username,)
        )
        row = cur.fetchone()
        conn.close()
        if row:
            info = _user_row_to_dict(row)
            # Return minimal fields but include display_name
            return jsonify({
                "username": info['username'],
                "display_name": info['display_name'],
                "role": info['role']
            })
    except Exception:
        pass
    # Fallback to original shape
    return jsonify({
        "username": username,
        "display_name": username,
        "role": user.get('role') or 'User'
    })


# Endpoint: /api/users — methods GET.

@app.route("/api/users", methods=["GET"])
def api_users_list():
    chk = _require_admin()
    if chk:
        return chk
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, username, display_name, role, last_login, created_at, updated_at, COALESCE(mfa_enabled,0) FROM users ORDER BY LOWER(username) ASC"
        )
        rows = cur.fetchall()
        conn.close()
        users = [
            {
                "id": r[0],
                "username": r[1],
                "display_name": r[2] or r[1],
                "role": r[3] or 'User',
                "last_login": r[4] or 0,
                "created_at": r[5] or 0,
                "updated_at": r[6] or 0,
                "mfa_enabled": 1 if (r[7] or 0) else 0,
            }
            for r in rows
        ]
        return jsonify({"users": users})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/users — methods POST.

@app.route("/api/users", methods=["POST"])  # create user
def api_users_create():
    chk = _require_admin()
    if chk:
        return chk
    data = request.get_json(silent=True) or {}
    username = (data.get('username') or '').strip()
    display_name = (data.get('display_name') or username).strip()
    role = (data.get('role') or 'User').strip().title()
    password_sha512 = (data.get('password_sha512') or '').strip().lower()
    if not username or not password_sha512:
        return jsonify({"error": "username and password_sha512 are required"}), 400
    if role not in ('User', 'Admin'):
        return jsonify({"error": "invalid role"}), 400
    now = _now_ts()
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "INSERT INTO users(username, display_name, password_sha512, role, created_at, updated_at) VALUES(?,?,?,?,?,?)",
            (username, display_name or username, password_sha512, role, now, now)
        )
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except sqlite3.IntegrityError:
        return jsonify({"error": "username already exists"}), 409
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/users/<username> — methods DELETE.

@app.route("/api/users/<username>", methods=["DELETE"])  # delete user
def api_users_delete(username):
    chk = _require_admin()
    if chk:
        return chk
    username = (username or '').strip()
    if not username:
        return jsonify({"error": "invalid username"}), 400
    try:
        conn = _db_conn()
        cur = conn.cursor()
        # prevent deleting current user
        me = _current_user()
        if me and (me.get('username','').lower() == username.lower()):
            conn.close()
            return jsonify({"error": "You cannot delete the user you are currently logged in as."}), 400
        # ensure at least one other user remains
        cur.execute("SELECT COUNT(*) FROM users")
        total = cur.fetchone()[0] or 0
        if total <= 1:
            conn.close()
            return jsonify({"error": "There is only one user currently configured, you cannot delete this user until you have created another."}), 400
        cur.execute("DELETE FROM users WHERE LOWER(username)=LOWER(?)", (username,))
        deleted = cur.rowcount
        conn.commit()
        conn.close()
        if deleted == 0:
            return jsonify({"error": "user not found"}), 404
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/users/<username>/reset_password — methods POST.

@app.route("/api/users/<username>/reset_password", methods=["POST"])  # reset password
def api_users_reset_password(username):
    chk = _require_admin()
    if chk:
        return chk
    data = request.get_json(silent=True) or {}
    password_sha512 = (data.get('password_sha512') or '').strip().lower()
    if not password_sha512 or len(password_sha512) != 128:
        return jsonify({"error": "invalid password hash"}), 400
    try:
        conn = _db_conn()
        cur = conn.cursor()
        now = _now_ts()
        cur.execute(
            "UPDATE users SET password_sha512=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
            (password_sha512, now, username)
        )
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "user not found"}), 404
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/users/<username>/role — methods POST.

@app.route("/api/users/<username>/role", methods=["POST"])  # change role
def api_users_change_role(username):
    chk = _require_admin()
    if chk:
        return chk
    data = request.get_json(silent=True) or {}
    role = (data.get('role') or '').strip().title()
    if role not in ('User', 'Admin'):
        return jsonify({"error": "invalid role"}), 400
    try:
        conn = _db_conn()
        cur = conn.cursor()
        # Prevent removing last admin
        if role == 'User':
            cur.execute("SELECT COUNT(*) FROM users WHERE LOWER(role)='admin'")
            admin_cnt = cur.fetchone()[0] or 0
            cur.execute("SELECT LOWER(role) FROM users WHERE LOWER(username)=LOWER(?)", (username,))
            row = cur.fetchone()
            if row and (row[0] or '').lower() == 'admin' and admin_cnt <= 1:
                conn.close()
                return jsonify({"error": "cannot demote the last admin"}), 400
        now = _now_ts()
        cur.execute(
            "UPDATE users SET role=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
            (role, now, username)
        )
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "user not found"}), 404
        conn.commit()
        conn.close()
        # If current user changed their own role, refresh session role
        me = _current_user()
        if me and me.get('username','').lower() == username.lower():
            session['role'] = role
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/users/<username>/mfa — methods POST.

@app.route("/api/users/<username>/mfa", methods=["POST"])
def api_users_toggle_mfa(username):
    chk = _require_admin()
    if chk:
        return chk
    username = (username or "").strip()
    if not username:
        return jsonify({"error": "invalid username"}), 400
    data = request.get_json(silent=True) or {}
    enabled = bool(data.get("enabled"))
    reset_secret = bool(data.get("reset_secret", False))
    try:
        conn = _db_conn()
        cur = conn.cursor()
        now = _now_ts()
        if enabled:
            if reset_secret:
                cur.execute(
                    "UPDATE users SET mfa_enabled=1, mfa_secret=NULL, updated_at=? WHERE LOWER(username)=LOWER(?)",
                    (now, username)
                )
            else:
                cur.execute(
                    "UPDATE users SET mfa_enabled=1, updated_at=? WHERE LOWER(username)=LOWER(?)",
                    (now, username)
                )
        else:
            if reset_secret:
                cur.execute(
                    "UPDATE users SET mfa_enabled=0, mfa_secret=NULL, updated_at=? WHERE LOWER(username)=LOWER(?)",
                    (now, username)
                )
            else:
                cur.execute(
                    "UPDATE users SET mfa_enabled=0, updated_at=? WHERE LOWER(username)=LOWER(?)",
                    (now, username)
                )
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "user not found"}), 404
        conn.commit()
        conn.close()
        # If the current user disabled MFA for themselves, clear pending session state
        me = _current_user()
        if me and me.get("username", "").lower() == username.lower() and not enabled:
            session.pop("mfa_pending", None)
        return jsonify({"status": "ok"})
    except Exception as exc:
        return jsonify({"error": str(exc)}), 500

# =============================================================================
# Section: Access Management - Credentials
# =============================================================================


@app.route("/api/credentials", methods=["GET", "POST"])
def api_credentials_collection():
    chk = _require_admin()
    if chk:
        return chk
    if request.method == "GET":
        site_filter = _coerce_int(request.args.get("site_id"))
        connection_filter = request.args.get("connection_type")
        where_parts: List[str] = []
        params: List[Any] = []
        if site_filter is not None:
            where_parts.append("c.site_id = ?")
            params.append(site_filter)
        if connection_filter:
            where_parts.append("LOWER(c.connection_type) = ?")
            params.append(_normalize_connection_type(connection_filter))
        where_clause = " AND ".join(where_parts)
        creds = _query_credentials(where_clause, tuple(params))
        return jsonify({"credentials": creds})

    data = request.get_json(silent=True) or {}
    name = (data.get("name") or "").strip()
    if not name:
        return jsonify({"error": "name is required"}), 400
    credential_type = _normalize_credential_type(data.get("credential_type"))
    connection_type = _normalize_connection_type(data.get("connection_type"))
    username = (data.get("username") or "").strip()
    description = (data.get("description") or "").strip()
    site_id = _coerce_int(data.get("site_id"))
    metadata = data.get("metadata") if isinstance(data.get("metadata"), dict) else None
    metadata_json = json.dumps(metadata) if metadata else None

    password_blob = _secret_from_payload(data.get("password"))
    private_key_val = data.get("private_key")
    if isinstance(private_key_val, str) and private_key_val and not private_key_val.endswith("\n"):
        private_key_val = private_key_val + "\n"
    private_key_blob = _secret_from_payload(private_key_val)
    private_key_passphrase_blob = _secret_from_payload(data.get("private_key_passphrase"))

    become_method = _normalize_become_method(data.get("become_method"))
    become_username = (data.get("become_username") or "").strip()
    become_password_blob = _secret_from_payload(data.get("become_password"))

    now = _now_ts()
    conn = None
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO credentials (
                name,
                description,
                site_id,
                credential_type,
                connection_type,
                username,
                password_encrypted,
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_method,
                become_username,
                become_password_encrypted,
                metadata_json,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                name,
                description,
                site_id,
                credential_type,
                connection_type,
                username,
                password_blob,
                private_key_blob,
                private_key_passphrase_blob,
                become_method,
                become_username,
                become_password_blob,
                metadata_json,
                now,
                now,
            ),
        )
        conn.commit()
        cred_id = int(cur.lastrowid or 0)
        conn.close()
    except sqlite3.IntegrityError:
        if conn:
            conn.close()
        return jsonify({"error": "credential name already exists"}), 409
    except Exception as exc:
        if conn:
            conn.close()
        return jsonify({"error": str(exc)}), 500

    record = _fetch_credential_record(cred_id)
    return jsonify({"credential": record}), 201


@app.route("/api/credentials/<int:credential_id>", methods=["GET", "PUT", "DELETE"])
def api_credentials_detail(credential_id: int):
    chk = _require_admin()
    if chk:
        return chk

    if request.method == "GET":
        conn = None
        try:
            conn = _db_conn()
            conn.row_factory = sqlite3.Row  # type: ignore[attr-defined]
            cur = conn.cursor()
            cur.execute(
                """
                SELECT c.*, s.name AS site_name
                FROM credentials c
                LEFT JOIN sites s ON s.id = c.site_id
                WHERE c.id = ?
                """,
                (credential_id,),
            )
            row = cur.fetchone()
        except Exception as exc:
            if conn:
                conn.close()
            return jsonify({"error": str(exc)}), 500
        if conn:
            conn.close()
        if not row:
            return jsonify({"error": "credential not found"}), 404
        row_map = dict(row)
        row_map["has_password"] = 1 if row_map.get("password_encrypted") else 0
        row_map["has_private_key"] = 1 if row_map.get("private_key_encrypted") else 0
        row_map["has_become_password"] = 1 if row_map.get("become_password_encrypted") else 0
        detail = _credential_row_to_dict(row_map)
        detail["has_private_key_passphrase"] = bool(row_map.get("private_key_passphrase_encrypted"))
        detail["password_fingerprint"] = _secret_fingerprint(row_map.get("password_encrypted"))
        detail["private_key_fingerprint"] = _secret_fingerprint(row_map.get("private_key_encrypted"))
        detail["become_password_fingerprint"] = _secret_fingerprint(row_map.get("become_password_encrypted"))
        return jsonify({"credential": detail})

    if request.method == "DELETE":
        conn = None
        try:
            conn = _db_conn()
            cur = conn.cursor()
            cur.execute("UPDATE scheduled_jobs SET credential_id=NULL WHERE credential_id=?", (credential_id,))
            cur.execute("DELETE FROM credentials WHERE id=?", (credential_id,))
            if cur.rowcount == 0:
                conn.close()
                return jsonify({"error": "credential not found"}), 404
            conn.commit()
            conn.close()
            return jsonify({"status": "ok"})
        except Exception as exc:
            if conn:
                conn.close()
            return jsonify({"error": str(exc)}), 500

    data = request.get_json(silent=True) or {}
    updates: Dict[str, Any] = {}
    if "name" in data:
        name = (data.get("name") or "").strip()
        if not name:
            return jsonify({"error": "name cannot be empty"}), 400
        updates["name"] = name
    if "description" in data:
        updates["description"] = (data.get("description") or "").strip()
    if "site_id" in data:
        updates["site_id"] = _coerce_int(data.get("site_id"))
    if "credential_type" in data:
        updates["credential_type"] = _normalize_credential_type(data.get("credential_type"))
    if "connection_type" in data:
        updates["connection_type"] = _normalize_connection_type(data.get("connection_type"))
    if "username" in data:
        updates["username"] = (data.get("username") or "").strip()
    if "become_method" in data:
        updates["become_method"] = _normalize_become_method(data.get("become_method"))
    if "become_username" in data:
        updates["become_username"] = (data.get("become_username") or "").strip()
    if "metadata" in data:
        metadata = data.get("metadata")
        if metadata is None:
            updates["metadata_json"] = None
        elif isinstance(metadata, dict):
            updates["metadata_json"] = json.dumps(metadata)
    if data.get("clear_password"):
        updates["password_encrypted"] = None
    elif "password" in data:
        updates["password_encrypted"] = _secret_from_payload(data.get("password"))
    if data.get("clear_private_key"):
        updates["private_key_encrypted"] = None
    elif "private_key" in data:
        pk_val = data.get("private_key")
        if isinstance(pk_val, str) and pk_val and not pk_val.endswith("\n"):
            pk_val = pk_val + "\n"
        updates["private_key_encrypted"] = _secret_from_payload(pk_val)
    if data.get("clear_private_key_passphrase"):
        updates["private_key_passphrase_encrypted"] = None
    elif "private_key_passphrase" in data:
        updates["private_key_passphrase_encrypted"] = _secret_from_payload(data.get("private_key_passphrase"))
    if data.get("clear_become_password"):
        updates["become_password_encrypted"] = None
    elif "become_password" in data:
        updates["become_password_encrypted"] = _secret_from_payload(data.get("become_password"))

    if not updates:
        return jsonify({"error": "no fields to update"}), 400
    updates["updated_at"] = _now_ts()

    conn = None
    try:
        conn = _db_conn()
        cur = conn.cursor()
        set_clause = ", ".join([f"{col}=?" for col in updates.keys()])
        params = list(updates.values()) + [credential_id]
        cur.execute(f"UPDATE credentials SET {set_clause} WHERE id=?", params)
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "credential not found"}), 404
        conn.commit()
        conn.close()
    except sqlite3.IntegrityError:
        if conn:
            conn.close()
        return jsonify({"error": "credential name already exists"}), 409
    except Exception as exc:
        if conn:
            conn.close()
        return jsonify({"error": str(exc)}), 500

    record = _fetch_credential_record(credential_id)
    return jsonify({"credential": record})


# =============================================================================
# Section: Access Management - GitHub Token
# =============================================================================


@app.route("/api/github/token", methods=["GET", "POST"])
def api_github_token():
    chk = _require_admin()
    if chk:
        return chk

    if request.method == "GET":
        token = _load_github_token_from_db(force_refresh=True)
        verify = _verify_github_token(token)
        message = verify.get("message") or ("API Token Invalid" if token else "API Token Not Configured")
        return jsonify({
            "token": token or "",
            "has_token": bool(token),
            "valid": bool(verify.get("valid")),
            "message": message,
            "status": verify.get("status") or ("missing" if not token else "unknown"),
            "rate_limit": verify.get("rate_limit"),
            "error": verify.get("error"),
            "checked_at": _now_ts(),
        })

    data = request.get_json(silent=True) or {}
    token = str(data.get("token") or "").strip()

    conn = None
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute("DELETE FROM github_token")
        if token:
            cur.execute("INSERT INTO github_token (token) VALUES (?)", (token,))
        conn.commit()
        conn.close()
    except Exception as exc:
        if conn:
            try:
                conn.close()
            except Exception:
                pass
        return jsonify({"error": f"Failed to store token: {exc}"}), 500

    _set_cached_github_token(token or None)

    verify = _verify_github_token(token or None)
    message = verify.get("message") or ("API Token Invalid" if token else "API Token Not Configured")

    try:
        eventlet.spawn_n(_refresh_default_repo_hash, True)
    except Exception:
        pass

    return jsonify({
        "token": token,
        "has_token": bool(token),
        "valid": bool(verify.get("valid")),
        "message": message,
        "status": verify.get("status") or ("missing" if not token else "unknown"),
        "rate_limit": verify.get("rate_limit"),
        "error": verify.get("error"),
        "checked_at": _now_ts(),
    })


# =============================================================================
# Section: Server-Side Ansible Execution
# =============================================================================

_ANSIBLE_WORKSPACE_DIR = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "..", "..", "Logs", "Server", "AnsibleRuns")
)


def _ensure_ansible_workspace() -> str:
    try:
        os.makedirs(_ANSIBLE_WORKSPACE_DIR, exist_ok=True)
    except Exception:
        pass
    return _ANSIBLE_WORKSPACE_DIR


_WINRM_USERNAME_VAR = "__borealis_winrm_username"
_WINRM_PASSWORD_VAR = "__borealis_winrm_password"
_WINRM_TRANSPORT_VAR = "__borealis_winrm_transport"


def _fetch_credential_with_secrets(credential_id: int) -> Optional[Dict[str, Any]]:
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            """
            SELECT
                id,
                name,
                credential_type,
                connection_type,
                username,
                password_encrypted,
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_method,
                become_username,
                become_password_encrypted,
                metadata_json
            FROM credentials
            WHERE id=?
            """,
            (credential_id,),
        )
        row = cur.fetchone()
        conn.close()
    except Exception:
        return None
    if not row:
        return None
    return {
        "id": row[0],
        "name": row[1],
        "credential_type": (row[2] or "machine").lower(),
        "connection_type": (row[3] or "ssh").lower(),
        "username": row[4] or "",
        "password": _decrypt_secret(row[5]) if row[5] else "",
        "private_key": _decrypt_secret(row[6]) if row[6] else "",
        "private_key_passphrase": _decrypt_secret(row[7]) if row[7] else "",
        "become_method": _normalize_become_method(row[8]),
        "become_username": row[9] or "",
        "become_password": _decrypt_secret(row[10]) if row[10] else "",
        "metadata": {},
    }

    try:
        meta_json = row[11] if len(row) > 11 else None
        if meta_json:
            meta = json.loads(meta_json)
            if isinstance(meta, dict):
                cred["metadata"] = meta
    except Exception:
        pass

    return cred


def _inject_winrm_credential(
    base_values: Optional[Dict[str, Any]],
    credential: Optional[Dict[str, Any]],
) -> Dict[str, Any]:
    values: Dict[str, Any] = dict(base_values or {})
    if not credential:
        return values

    username = str(credential.get("username") or "")
    password = str(credential.get("password") or "")
    metadata = credential.get("metadata") if isinstance(credential.get("metadata"), dict) else {}
    transport = metadata.get("winrm_transport") if isinstance(metadata, dict) else None
    transport_str = str(transport or "ntlm").strip().lower() or "ntlm"

    values[_WINRM_USERNAME_VAR] = username
    values[_WINRM_PASSWORD_VAR] = password
    values[_WINRM_TRANSPORT_VAR] = transport_str
    return values


def _emit_ansible_recap_from_row(row):
    if not row:
        return
    try:
        payload = {
            "id": row[0],
            "run_id": row[1],
            "hostname": row[2] or "",
            "agent_id": row[3] or "",
            "playbook_path": row[4] or "",
            "playbook_name": row[5] or "",
            "scheduled_job_id": row[6],
            "scheduled_run_id": row[7],
            "activity_job_id": row[8],
            "status": row[9] or "",
            "recap_text": row[10] or "",
            "recap_json": json.loads(row[11]) if (row[11] or "").strip() else None,
            "started_ts": row[12],
            "finished_ts": row[13],
            "created_at": row[14],
            "updated_at": row[15],
        }
        socketio.emit("ansible_recap_update", payload)
        if payload.get("activity_job_id"):
            socketio.emit(
                "device_activity_changed",
                {
                    "hostname": payload.get("hostname") or "",
                    "activity_id": payload.get("activity_job_id"),
                    "status": payload.get("status") or "",
                    "change": "updated",
                    "source": "ansible",
                },
            )
    except Exception:
        pass


def _record_ansible_recap_start(
    run_id: str,
    hostname: str,
    playbook_rel_path: str,
    playbook_name: str,
    activity_id: Optional[int],
    scheduled_job_id: Optional[int],
    scheduled_run_id: Optional[int],
):
    try:
        now = _now_ts()
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO ansible_play_recaps (
                run_id,
                hostname,
                agent_id,
                playbook_path,
                playbook_name,
                scheduled_job_id,
                scheduled_run_id,
                activity_job_id,
                status,
                recap_text,
                recap_json,
                started_ts,
                created_at,
                updated_at
            )
            VALUES (?, ?, 'server', ?, ?, ?, ?, ?, 'Running', '', '', ?, ?, ?)
            ON CONFLICT(run_id) DO UPDATE SET
                hostname=excluded.hostname,
                playbook_path=excluded.playbook_path,
                playbook_name=excluded.playbook_name,
                scheduled_job_id=excluded.scheduled_job_id,
                scheduled_run_id=excluded.scheduled_run_id,
                activity_job_id=excluded.activity_job_id,
                status='Running',
                started_ts=COALESCE(ansible_play_recaps.started_ts, excluded.started_ts),
                updated_at=excluded.updated_at
            """,
            (
                run_id,
                hostname,
                playbook_rel_path,
                playbook_name,
                scheduled_job_id,
                scheduled_run_id,
                activity_id,
                now,
                now,
                now,
            ),
        )
        conn.commit()
        conn.close()
    except Exception as exc:
        _ansible_log_server(f"[server_run] failed to record recap start run_id={run_id}: {exc}")


def _queue_server_ansible_run(
    *,
    hostname: str,
    playbook_abs_path: str,
    playbook_rel_path: str,
    playbook_name: str,
    credential_id: int,
    variable_values: Dict[str, Any],
    source: str,
    activity_id: Optional[int] = None,
    scheduled_job_id: Optional[int] = None,
    scheduled_run_id: Optional[int] = None,
    scheduled_job_run_row_id: Optional[int] = None,
) -> str:
    try:
        run_id = uuid.uuid4().hex
    except Exception:
        run_id = str(int(time.time() * 1000))

    _record_ansible_recap_start(
        run_id,
        hostname,
        playbook_rel_path,
        playbook_name,
        activity_id,
        scheduled_job_id,
        scheduled_run_id,
    )

    ctx = {
        "run_id": run_id,
        "hostname": hostname,
        "playbook_abs_path": playbook_abs_path,
        "playbook_rel_path": playbook_rel_path,
        "playbook_name": playbook_name,
        "credential_id": credential_id,
        "variable_values": variable_values or {},
        "activity_id": activity_id,
        "scheduled_job_id": scheduled_job_id,
        "scheduled_run_id": scheduled_run_id,
        "scheduled_job_run_row_id": scheduled_job_run_row_id,
        "source": source,
        "started_ts": _now_ts(),
    }
    try:
        socketio.start_background_task(_execute_server_ansible_run, ctx, None)
    except Exception as exc:
        _ansible_log_server(f"[server_run] failed to queue background task run_id={run_id}: {exc}")
        _execute_server_ansible_run(ctx, immediate_error=str(exc))
    return run_id


def _execute_server_ansible_run(ctx: Dict[str, Any], immediate_error: Optional[str] = None):
    run_id = ctx.get("run_id") or uuid.uuid4().hex
    hostname = ctx.get("hostname") or ""
    playbook_abs_path = ctx.get("playbook_abs_path") or ""
    playbook_rel_path = ctx.get("playbook_rel_path") or os.path.basename(playbook_abs_path)
    playbook_name = ctx.get("playbook_name") or os.path.basename(playbook_abs_path)
    credential_id = ctx.get("credential_id")
    variable_values = ctx.get("variable_values") or {}
    activity_id = ctx.get("activity_id")
    scheduled_job_id = ctx.get("scheduled_job_id")
    scheduled_run_id = ctx.get("scheduled_run_id")
    scheduled_job_run_row_id = ctx.get("scheduled_job_run_row_id")
    started_ts = ctx.get("started_ts") or _now_ts()
    source = ctx.get("source") or "ansible"

    status = "Failed"
    stdout = ""
    stderr = ""
    error_message = immediate_error or ""
    finished_ts = started_ts
    workspace = None

    try:
        credential = _fetch_credential_with_secrets(int(credential_id))
        if not credential:
            raise RuntimeError("Credential not found")
        connection_type = credential.get("connection_type", "ssh")
        if connection_type not in ("ssh",):
            raise RuntimeError(f"Unsupported credential connection type '{connection_type}' for server execution")

        workspace_root = _ensure_ansible_workspace()
        workspace = os.path.join(workspace_root, run_id)
        os.makedirs(workspace, exist_ok=True)

        inventory_path = os.path.join(workspace, "inventory.json")
        extra_vars_path = None
        key_path = None

        host_entry: Dict[str, Any] = {
            "ansible_host": hostname,
            "ansible_user": credential.get("username") or "",
            "ansible_connection": "ssh",
            "ansible_ssh_common_args": "-o StrictHostKeyChecking=no",
        }
        if credential.get("password"):
            host_entry["ansible_password"] = credential["password"]
            host_entry["ansible_ssh_pass"] = credential["password"]
        if credential.get("private_key"):
            key_path = os.path.join(workspace, "ssh_key")
            with open(key_path, "w", encoding="utf-8") as fh:
                fh.write(credential["private_key"])
            try:
                os.chmod(key_path, stat.S_IRUSR | stat.S_IWUSR)
            except Exception:
                pass
            host_entry["ansible_ssh_private_key_file"] = key_path
        become_method = credential.get("become_method") or ""
        become_username = credential.get("become_username") or ""
        become_password = credential.get("become_password") or ""
        if become_method or become_username or become_password:
            host_entry["ansible_become"] = True
            if become_method:
                host_entry["ansible_become_method"] = become_method
            if become_username:
                host_entry["ansible_become_user"] = become_username
            if become_password:
                host_entry["ansible_become_password"] = become_password

        with open(inventory_path, "w", encoding="utf-8") as fh:
            json.dump({"all": {"hosts": {hostname: host_entry}}}, fh)

        if variable_values:
            extra_vars_path = os.path.join(workspace, "extra_vars.json")
            with open(extra_vars_path, "w", encoding="utf-8") as fh:
                json.dump(variable_values, fh)

        env = os.environ.copy()
        env.setdefault("ANSIBLE_STDOUT_CALLBACK", "yaml")
        env["ANSIBLE_HOST_KEY_CHECKING"] = "False"
        env.setdefault("ANSIBLE_RETRY_FILES_ENABLED", "False")

        cmd = ["ansible-playbook", playbook_abs_path, "-i", inventory_path]
        if extra_vars_path:
            cmd.extend(["--extra-vars", f"@{extra_vars_path}"])
        if become_method or become_username or become_password:
            cmd.append("--become")
            if become_method:
                cmd.extend(["--become-method", become_method])
            if become_username:
                cmd.extend(["--become-user", become_username])

        _ansible_log_server(
            f"[server_run] start run_id={run_id} host='{hostname}' playbook='{playbook_rel_path}' credential={credential_id}"
        )

        proc = tpool.execute(
            subprocess.run,
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            env=env,
            cwd=os.path.dirname(playbook_abs_path) or None,
        )

        stdout = proc.stdout or ""
        stderr = proc.stderr or ""
        status = "Success" if proc.returncode == 0 else "Failed"
        finished_ts = _now_ts()
        if proc.returncode != 0 and not error_message:
            error_message = stderr or stdout or f"ansible-playbook exited with {proc.returncode}"
    except Exception as exc:
        finished_ts = _now_ts()
        status = "Failed"
        if not error_message:
            error_message = str(exc)
        if not stderr:
            stderr = f"{exc}"
        _ansible_log_server(
            f"[server_run] error run_id={run_id} host='{hostname}' err={exc}\n{traceback.format_exc()}"
        )
    finally:
        if workspace:
            try:
                shutil.rmtree(workspace, ignore_errors=True)
            except Exception:
                pass

    recap_json = "{}"

    try:
        conn = _db_conn()
        cur = conn.cursor()
        if activity_id:
            try:
                cur.execute(
                    "UPDATE activity_history SET status=?, stdout=?, stderr=?, ran_at=? WHERE id=?",
                    (status, stdout, stderr, finished_ts, int(activity_id)),
                )
            except Exception:
                pass
        if scheduled_job_run_row_id:
            try:
                cur.execute(
                    "UPDATE scheduled_job_runs SET status=?, finished_ts=?, updated_at=?, error=? WHERE id=?",
                    (
                        status,
                        finished_ts,
                        finished_ts,
                        (error_message or "")[:1024],
                        int(scheduled_job_run_row_id),
                    ),
                )
            except Exception:
                pass
        try:
            cur.execute(
                """
                UPDATE ansible_play_recaps
                SET status=?,
                    recap_text=?,
                    recap_json=?,
                    finished_ts=?,
                    updated_at=?,
                    hostname=?,
                    playbook_path=?,
                    playbook_name=?,
                    scheduled_job_id=?,
                    scheduled_run_id=?,
                    activity_job_id=?
                WHERE run_id=?
                """,
                (
                    status,
                    stdout,
                    recap_json,
                    finished_ts,
                    finished_ts,
                    hostname,
                    playbook_rel_path,
                    playbook_name,
                    scheduled_job_id,
                    scheduled_run_id,
                    activity_id,
                    run_id,
                ),
            )
        except Exception as exc:
            _ansible_log_server(f"[server_run] failed to update recap run_id={run_id}: {exc}")
        try:
            cur.execute(
                "SELECT id, run_id, hostname, agent_id, playbook_path, playbook_name, scheduled_job_id, scheduled_run_id, activity_job_id, status, recap_text, recap_json, started_ts, finished_ts, created_at, updated_at FROM ansible_play_recaps WHERE run_id=?",
                (run_id,),
            )
            recap_row = cur.fetchone()
        except Exception:
            recap_row = None
        conn.commit()
        conn.close()
    except Exception as exc:
        _ansible_log_server(f"[server_run] database update failed run_id={run_id}: {exc}")

    if recap_row:
        _emit_ansible_recap_from_row(recap_row)

    if status == "Success":
        _ansible_log_server(
            f"[server_run] completed run_id={run_id} host='{hostname}' status=Success duration={finished_ts - started_ts}s"
        )
    else:
        _ansible_log_server(
            f"[server_run] completed run_id={run_id} host='{hostname}' status=Failed duration={finished_ts - started_ts}s"
        )


# =============================================================================
# Section: Python Sidecar Services
# =============================================================================
# Bridge into Python helpers such as OCR and PowerShell execution.

# Endpoint: /api/ocr — methods POST.

@app.route("/api/ocr", methods=["POST"])
def ocr_endpoint():
    payload = request.get_json()
    image_b64 = payload.get("image_base64")
    engine = payload.get("engine", "tesseract").lower().strip()
    backend = payload.get("backend", "cpu").lower().strip()

    if engine in ["tesseractocr", "tesseract"]:
        engine = "tesseract"
    elif engine == "easyocr":
        engine = "easyocr"
    else:
        return jsonify({"error": f"OCR engine '{engine}' not recognized."}), 400

    try:
        lines = run_ocr_on_base64(image_b64, engine=engine, backend=backend)
        return jsonify({"lines": lines})
    except Exception as e:
        return jsonify({"error": str(e)}), 500

# unified assembly endpoints supersede prior storage workflow endpoints

# =============================================================================
# Section: Storage Legacy Helpers
# =============================================================================
# Compatibility helpers for direct script/storage file access.

def _safe_read_json(path: str) -> Dict:
    """
    Try to read JSON safely. Returns {} on failure.
    """
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except Exception:
        return {}

def _extract_tab_name(obj: Dict) -> str:
    """
    Best-effort extraction of a workflow tab name from a JSON object.
    Falls back to empty string when unknown.
    """
    if not isinstance(obj, dict):
        return ""
    for key in ["tabName", "tab_name", "name", "title"]:
        val = obj.get(key)
        if isinstance(val, str) and val.strip():
            return val.strip()
    return ""

# unified assembly endpoints provide listing instead


# superseded by /api/assembly/load


# superseded by /api/assembly/create and /api/assembly/edit


# superseded by /api/assembly/rename


# =============================================================================
# Section: Assemblies CRUD
# =============================================================================
# Unified workflow/script/playbook storage with path normalization.


def _assemblies_root() -> str:
    return os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies")
    )


_ISLAND_DIR_MAP = {
    # normalized -> directory name
    "workflows": "Workflows",
    "workflow": "Workflows",
    "scripts": "Scripts",
    "script": "Scripts",
    "ansible": "Ansible_Playbooks",
    "ansible_playbooks": "Ansible_Playbooks",
    "ansible-playbooks": "Ansible_Playbooks",
    "playbooks": "Ansible_Playbooks",
}


def _normalize_relpath(p: str) -> str:
    return (p or "").replace("\\", "/").strip("/")


def _resolve_island_root(island: str) -> Optional[str]:
    key = (island or "").strip().lower()
    sub = _ISLAND_DIR_MAP.get(key)
    if not sub:
        return None
    root = os.path.join(_assemblies_root(), sub)
    return os.path.abspath(root)


def _resolve_assembly_path(island: str, rel_path: str) -> Tuple[str, str, str]:
    root = _resolve_island_root(island)
    if not root:
        raise ValueError("invalid island")
    rel_norm = _normalize_relpath(rel_path)
    abs_path = os.path.abspath(os.path.join(root, rel_norm))
    if not abs_path.startswith(root):
        raise ValueError("invalid path")
    return root, abs_path, rel_norm


def _default_ext_for_island(island: str, item_type: str = "") -> str:
    isl = (island or "").lower().strip()
    if isl in ("workflows", "workflow"):
        return ".json"
    if isl in ("ansible", "ansible_playbooks", "ansible-playbooks", "playbooks"):
        return ".json"
    if isl in ("scripts", "script"):
        return ".json"
    t = (item_type or "").lower().strip()
    if t == "bash":
        return ".json"
    if t == "batch":
        return ".json"
    if t == "powershell":
        return ".json"
    return ".json"


def _default_type_for_island(island: str, item_type: str = "") -> str:
    isl = (island or "").lower().strip()
    if isl in ("ansible", "ansible_playbooks", "ansible-playbooks", "playbooks"):
        return "ansible"
    t = (item_type or "").lower().strip()
    if t in ("powershell", "batch", "bash", "ansible"):
        return t
    return "powershell"


def _empty_assembly_document(default_type: str = "powershell") -> Dict[str, Any]:
    return {
        "version": 1,
        "name": "",
        "description": "",
        "category": "application" if (default_type or "").lower() == "ansible" else "script",
        "type": default_type or "powershell",
        "script": "",
        "timeout_seconds": 3600,
        "sites": {"mode": "all", "values": []},
        "variables": [],
        "files": []
    }


def _decode_base64_text(value: Any) -> Optional[str]:
    if not isinstance(value, str):
        return None
    stripped = value.strip()
    if not stripped:
        return ""
    try:
        cleaned = re.sub(r"\s+", "", stripped)
    except Exception:
        cleaned = stripped
    try:
        decoded = base64.b64decode(cleaned, validate=True)
    except Exception:
        return None
    try:
        return decoded.decode("utf-8")
    except Exception:
        return decoded.decode("utf-8", errors="replace")


def _decode_script_content(value: Any, encoding_hint: str = "") -> str:
    encoding = (encoding_hint or "").strip().lower()
    if isinstance(value, str):
        if encoding in ("base64", "b64", "base-64"):
            decoded = _decode_base64_text(value)
            if decoded is not None:
                return decoded.replace("\r\n", "\n")
        decoded = _decode_base64_text(value)
        if decoded is not None:
            return decoded.replace("\r\n", "\n")
        return value.replace("\r\n", "\n")
    return ""


def _encode_script_content(script_text: Any) -> str:
    if not isinstance(script_text, str):
        if script_text is None:
            script_text = ""
        else:
            script_text = str(script_text)
    normalized = script_text.replace("\r\n", "\n")
    if not normalized:
        return ""
    encoded = base64.b64encode(normalized.encode("utf-8"))
    return encoded.decode("ascii")


def _prepare_assembly_storage(doc: Dict[str, Any]) -> Dict[str, Any]:
    stored: Dict[str, Any] = {}
    for key, value in (doc or {}).items():
        if key == "script":
            stored[key] = _encode_script_content(value)
        else:
            stored[key] = value
    stored["script_encoding"] = "base64"
    return stored


def _normalize_assembly_document(obj: Any, default_type: str, base_name: str) -> Dict[str, Any]:
    doc = _empty_assembly_document(default_type)
    if not isinstance(obj, dict):
        obj = {}
    base = (base_name or "assembly").strip()
    doc["name"] = str(obj.get("name") or obj.get("display_name") or base)
    doc["description"] = str(obj.get("description") or "")
    category = str(obj.get("category") or doc["category"]).strip().lower()
    if category in ("script", "application"):
        doc["category"] = category
    typ = str(obj.get("type") or obj.get("script_type") or default_type or "powershell").strip().lower()
    if typ in ("powershell", "batch", "bash", "ansible"):
        doc["type"] = typ
    script_val = obj.get("script")
    content_val = obj.get("content")
    script_lines = obj.get("script_lines")
    if isinstance(script_lines, list):
        try:
            doc["script"] = "\n".join(str(line) for line in script_lines)
        except Exception:
            doc["script"] = ""
    elif isinstance(script_val, str):
        doc["script"] = script_val
    else:
        if isinstance(content_val, str):
            doc["script"] = content_val
    encoding_hint = str(obj.get("script_encoding") or obj.get("scriptEncoding") or "").strip().lower()
    doc["script"] = _decode_script_content(doc.get("script"), encoding_hint)
    if encoding_hint in ("base64", "b64", "base-64"):
        doc["script_encoding"] = "base64"
    else:
        probe_source = ""
        if isinstance(script_val, str) and script_val:
            probe_source = script_val
        elif isinstance(content_val, str) and content_val:
            probe_source = content_val
        decoded_probe = _decode_base64_text(probe_source) if probe_source else None
        if decoded_probe is not None:
            doc["script_encoding"] = "base64"
            doc["script"] = decoded_probe.replace("\r\n", "\n")
        else:
            doc["script_encoding"] = "plain"
    timeout_val = obj.get("timeout_seconds", obj.get("timeout"))
    if timeout_val is not None:
        try:
            doc["timeout_seconds"] = max(0, int(timeout_val))
        except Exception:
            pass
    sites = obj.get("sites") if isinstance(obj.get("sites"), dict) else {}
    values = sites.get("values") if isinstance(sites.get("values"), list) else []
    mode = str(sites.get("mode") or ("specific" if values else "all")).strip().lower()
    if mode not in ("all", "specific"):
        mode = "all"
    doc["sites"] = {
        "mode": mode,
        "values": [str(v).strip() for v in values if isinstance(v, (str, int, float)) and str(v).strip()]
    }
    vars_in = obj.get("variables") if isinstance(obj.get("variables"), list) else []
    doc_vars: List[Dict[str, Any]] = []
    for v in vars_in:
        if not isinstance(v, dict):
            continue
        name = str(v.get("name") or v.get("key") or "").strip()
        if not name:
            continue
        vtype = str(v.get("type") or "string").strip().lower()
        if vtype not in ("string", "number", "boolean", "credential"):
            vtype = "string"
        default_val = v.get("default", v.get("default_value"))
        doc_vars.append({
            "name": name,
            "label": str(v.get("label") or ""),
            "type": vtype,
            "default": default_val,
            "required": bool(v.get("required")),
            "description": str(v.get("description") or "")
        })
    doc["variables"] = doc_vars
    files_in = obj.get("files") if isinstance(obj.get("files"), list) else []
    doc_files: List[Dict[str, Any]] = []
    for f in files_in:
        if not isinstance(f, dict):
            continue
        fname = f.get("file_name") or f.get("name")
        data = f.get("data")
        if not fname or not isinstance(data, str):
            continue
        size_val = f.get("size")
        try:
            size_int = int(size_val)
        except Exception:
            size_int = 0
        doc_files.append({
            "file_name": str(fname),
            "size": size_int,
            "mime_type": str(f.get("mime_type") or f.get("mimeType") or ""),
            "data": data
        })
    doc["files"] = doc_files
    try:
        doc["version"] = int(obj.get("version") or doc["version"])
    except Exception:
        pass
    return doc


def _load_assembly_document(abs_path: str, island: str, type_hint: str = "") -> Dict[str, Any]:
    base_name = os.path.splitext(os.path.basename(abs_path))[0]
    default_type = _default_type_for_island(island, type_hint)
    if abs_path.lower().endswith(".json"):
        data = _safe_read_json(abs_path)
        return _normalize_assembly_document(data, default_type, base_name)
    try:
        with open(abs_path, "r", encoding="utf-8", errors="replace") as fh:
            content = fh.read()
    except Exception:
        content = ""
    doc = _empty_assembly_document(default_type)
    doc["name"] = base_name
    normalized_script = (content or "").replace("\r\n", "\n")
    doc["script"] = normalized_script
    if default_type == "ansible":
        doc["category"] = "application"
    return doc


# Endpoint: /api/assembly/create — methods POST.

@app.route("/api/assembly/create", methods=["POST"])
def assembly_create():
    data = request.get_json(silent=True) or {}
    island = (data.get("island") or "").strip()
    kind = (data.get("kind") or "").strip().lower()  # file | folder
    path = (data.get("path") or "").strip()
    content = data.get("content")
    item_type = (data.get("type") or "").strip().lower()  # optional hint for scripts
    try:
        root, abs_path, rel_norm = _resolve_assembly_path(island, path)
        if not rel_norm:
            return jsonify({"error": "path required"}), 400
        if kind == "folder":
            os.makedirs(abs_path, exist_ok=True)
            return jsonify({"status": "ok"})
        elif kind == "file":
            base, ext = os.path.splitext(abs_path)
            if not ext:
                abs_path = base + _default_ext_for_island(island, item_type)
            os.makedirs(os.path.dirname(abs_path), exist_ok=True)
            # Workflows expect JSON; scripts/ansible use assembly documents
            if (island or "").lower() in ("workflows", "workflow"):
                obj = content
                if isinstance(obj, str):
                    try:
                        obj = json.loads(obj)
                    except Exception:
                        obj = {}
                if not isinstance(obj, dict):
                    obj = {}
                # seed tab_name based on filename when empty
                base_name = os.path.splitext(os.path.basename(abs_path))[0]
                if "tab_name" not in obj:
                    obj["tab_name"] = base_name
                with open(abs_path, "w", encoding="utf-8") as fh:
                    json.dump(obj, fh, indent=2)
            else:
                obj = content
                if isinstance(obj, str):
                    try:
                        obj = json.loads(obj)
                    except Exception:
                        obj = {}
                if not isinstance(obj, dict):
                    obj = {}
                base_name = os.path.splitext(os.path.basename(abs_path))[0]
                normalized = _normalize_assembly_document(
                    obj,
                    _default_type_for_island(island, item_type),
                    base_name,
                )
                with open(abs_path, "w", encoding="utf-8") as fh:
                    json.dump(_prepare_assembly_storage(normalized), fh, indent=2)
            rel_new = os.path.relpath(abs_path, root).replace(os.sep, "/")
            return jsonify({"status": "ok", "rel_path": rel_new})
        else:
            return jsonify({"error": "invalid kind"}), 400
    except ValueError as ve:
        return jsonify({"error": str(ve)}), 400
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/assembly/edit — methods POST.

@app.route("/api/assembly/edit", methods=["POST"])
def assembly_edit():
    data = request.get_json(silent=True) or {}
    island = (data.get("island") or "").strip()
    path = (data.get("path") or "").strip()
    content = data.get("content")
    try:
        root, abs_path, _ = _resolve_assembly_path(island, path)
        if not os.path.isfile(abs_path):
            return jsonify({"error": "file not found"}), 404
        target_abs = abs_path
        if not abs_path.lower().endswith(".json"):
            base, _ = os.path.splitext(abs_path)
            target_abs = base + _default_ext_for_island(island, data.get("type"))
        if (island or "").lower() in ("workflows", "workflow"):
            obj = content
            if isinstance(obj, str):
                obj = json.loads(obj)
            if not isinstance(obj, dict):
                return jsonify({"error": "invalid content for workflow"}), 400
            with open(target_abs, "w", encoding="utf-8") as fh:
                json.dump(obj, fh, indent=2)
        else:
            obj = content
            if isinstance(obj, str):
                try:
                    obj = json.loads(obj)
                except Exception:
                    obj = {}
            if not isinstance(obj, dict):
                obj = {}
            base_name = os.path.splitext(os.path.basename(target_abs))[0]
            normalized = _normalize_assembly_document(
                obj,
                _default_type_for_island(island, obj.get("type") if isinstance(obj, dict) else ""),
                base_name,
            )
            with open(target_abs, "w", encoding="utf-8") as fh:
                json.dump(_prepare_assembly_storage(normalized), fh, indent=2)
        if target_abs != abs_path:
            try:
                os.remove(abs_path)
            except Exception:
                pass
        rel_new = os.path.relpath(target_abs, root).replace(os.sep, "/")
        return jsonify({"status": "ok", "rel_path": rel_new})
    except ValueError as ve:
        return jsonify({"error": str(ve)}), 400
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/assembly/rename — methods POST.

@app.route("/api/assembly/rename", methods=["POST"])
def assembly_rename():
    data = request.get_json(silent=True) or {}
    island = (data.get("island") or "").strip()
    kind = (data.get("kind") or "").strip().lower()
    path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    item_type = (data.get("type") or "").strip().lower()
    if not new_name:
        return jsonify({"error": "new_name required"}), 400
    try:
        root, old_abs, _ = _resolve_assembly_path(island, path)
        if kind == "folder":
            if not os.path.isdir(old_abs):
                return jsonify({"error": "folder not found"}), 404
            new_abs = os.path.join(os.path.dirname(old_abs), new_name)
        elif kind == "file":
            if not os.path.isfile(old_abs):
                return jsonify({"error": "file not found"}), 404
            base, ext = os.path.splitext(new_name)
            if not ext:
                new_name = base + _default_ext_for_island(island, item_type)
            new_abs = os.path.join(os.path.dirname(old_abs), os.path.basename(new_name))
        else:
            return jsonify({"error": "invalid kind"}), 400

        if not os.path.abspath(new_abs).startswith(root):
            return jsonify({"error": "invalid destination"}), 400

        os.rename(old_abs, new_abs)

        # If a workflow file is renamed, update internal name fields
        if kind == "file" and (island or "").lower() in ("workflows", "workflow"):
            try:
                obj = _safe_read_json(new_abs)
                base_name = os.path.splitext(os.path.basename(new_abs))[0]
                for k in ["tabName", "tab_name", "name", "title"]:
                    if k in obj:
                        obj[k] = base_name
                if "tab_name" not in obj:
                    obj["tab_name"] = base_name
                with open(new_abs, "w", encoding="utf-8") as fh:
                    json.dump(obj, fh, indent=2)
            except Exception:
                pass

        rel_new = os.path.relpath(new_abs, root).replace(os.sep, "/")
        return jsonify({"status": "ok", "rel_path": rel_new})
    except ValueError as ve:
        return jsonify({"error": str(ve)}), 400
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/assembly/move — methods POST.

@app.route("/api/assembly/move", methods=["POST"])
def assembly_move():
    data = request.get_json(silent=True) or {}
    island = (data.get("island") or "").strip()
    path = (data.get("path") or "").strip()
    new_path = (data.get("new_path") or "").strip()
    kind = (data.get("kind") or "").strip().lower()  # optional; used for existence checks
    try:
        root, old_abs, _ = _resolve_assembly_path(island, path)
        _, new_abs, _ = _resolve_assembly_path(island, new_path)
        if kind == "folder":
            if not os.path.isdir(old_abs):
                return jsonify({"error": "folder not found"}), 404
        else:
            if not os.path.isfile(old_abs):
                return jsonify({"error": "file not found"}), 404
        os.makedirs(os.path.dirname(new_abs), exist_ok=True)
        shutil.move(old_abs, new_abs)
        return jsonify({"status": "ok"})
    except ValueError as ve:
        return jsonify({"error": str(ve)}), 400
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/assembly/delete — methods POST.

@app.route("/api/assembly/delete", methods=["POST"])
def assembly_delete():
    data = request.get_json(silent=True) or {}
    island = (data.get("island") or "").strip()
    kind = (data.get("kind") or "").strip().lower()
    path = (data.get("path") or "").strip()
    try:
        root, abs_path, rel_norm = _resolve_assembly_path(island, path)
        if not rel_norm:
            return jsonify({"error": "cannot delete root"}), 400
        if kind == "folder":
            if not os.path.isdir(abs_path):
                return jsonify({"error": "folder not found"}), 404
            shutil.rmtree(abs_path)
        elif kind == "file":
            if not os.path.isfile(abs_path):
                return jsonify({"error": "file not found"}), 404
            os.remove(abs_path)
        else:
            return jsonify({"error": "invalid kind"}), 400
        return jsonify({"status": "ok"})
    except ValueError as ve:
        return jsonify({"error": str(ve)}), 400
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/assembly/list — methods GET.

@app.route("/api/assembly/list", methods=["GET"])
def assembly_list():
    """List files and folders for a given island (workflows|scripts|ansible)."""
    island = (request.args.get("island") or "").strip()
    try:
        root = _resolve_island_root(island)
        if not root:
            return jsonify({"error": "invalid island"}), 400
        os.makedirs(root, exist_ok=True)

        items: List[Dict] = []
        folders: List[str] = []

        isl = (island or "").lower()
        if isl in ("workflows", "workflow"):
            exts = (".json",)
            for r, dirs, files in os.walk(root):
                rel_root = os.path.relpath(r, root)
                if rel_root != ".":
                    folders.append(rel_root.replace(os.sep, "/"))
                for fname in files:
                    if not fname.lower().endswith(exts):
                        continue
                    fp = os.path.join(r, fname)
                    rel_path = os.path.relpath(fp, root).replace(os.sep, "/")
                    try:
                        mtime = os.path.getmtime(fp)
                    except Exception:
                        mtime = 0.0
                    obj = _safe_read_json(fp)
                    tab = _extract_tab_name(obj)
                    items.append({
                        "file_name": fname,
                        "rel_path": rel_path,
                        "type": "workflow",
                        "tab_name": tab,
                        "last_edited": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime)),
                        "last_edited_epoch": mtime
                    })
        elif isl in ("scripts", "script"):
            exts = (".json", ".ps1", ".bat", ".sh")
            for r, dirs, files in os.walk(root):
                rel_root = os.path.relpath(r, root)
                if rel_root != ".":
                    folders.append(rel_root.replace(os.sep, "/"))
                for fname in files:
                    if not fname.lower().endswith(exts):
                        continue
                    fp = os.path.join(r, fname)
                    rel_path = os.path.relpath(fp, root).replace(os.sep, "/")
                    try:
                        mtime = os.path.getmtime(fp)
                    except Exception:
                        mtime = 0.0
                    stype = _detect_script_type(fp)
                    doc = _load_assembly_document(fp, "scripts", stype)
                    items.append({
                        "file_name": fname,
                        "rel_path": rel_path,
                        "type": doc.get("type", stype),
                        "name": doc.get("name"),
                        "category": doc.get("category"),
                        "description": doc.get("description"),
                        "last_edited": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime)),
                        "last_edited_epoch": mtime
                    })
        else:  # ansible
            exts = (".json", ".yml")
            for r, dirs, files in os.walk(root):
                rel_root = os.path.relpath(r, root)
                if rel_root != ".":
                    folders.append(rel_root.replace(os.sep, "/"))
                for fname in files:
                    if not fname.lower().endswith(exts):
                        continue
                    fp = os.path.join(r, fname)
                    rel_path = os.path.relpath(fp, root).replace(os.sep, "/")
                    try:
                        mtime = os.path.getmtime(fp)
                    except Exception:
                        mtime = 0.0
                    stype = _detect_script_type(fp)
                    doc = _load_assembly_document(fp, "ansible", stype)
                    items.append({
                        "file_name": fname,
                        "rel_path": rel_path,
                        "type": doc.get("type", "ansible"),
                        "name": doc.get("name"),
                        "category": doc.get("category"),
                        "description": doc.get("description"),
                        "last_edited": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime)),
                        "last_edited_epoch": mtime
                    })

        items.sort(key=lambda x: x.get("last_edited_epoch", 0.0), reverse=True)
        return jsonify({"root": root, "items": items, "folders": folders})
    except ValueError as ve:
        return jsonify({"error": str(ve)}), 400
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/assembly/load — methods GET.

@app.route("/api/assembly/load", methods=["GET"])
def assembly_load():
    """Load a file for a given island. Returns workflow JSON for workflows, and text content for others."""
    island = (request.args.get("island") or "").strip()
    rel_path = (request.args.get("path") or "").strip()
    try:
        root, abs_path, _ = _resolve_assembly_path(island, rel_path)
        if not os.path.isfile(abs_path):
            return jsonify({"error": "file not found"}), 404
        isl = (island or "").lower()
        if isl in ("workflows", "workflow"):
            obj = _safe_read_json(abs_path)
            return jsonify(obj)
        else:
            doc = _load_assembly_document(abs_path, island)
            rel = os.path.relpath(abs_path, root).replace(os.sep, "/")
            result = {
                "file_name": os.path.basename(abs_path),
                "rel_path": rel,
                "type": doc.get("type"),
                "assembly": doc,
                "content": doc.get("script")
            }
            return jsonify(result)
    except ValueError as ve:
        return jsonify({"error": str(ve)}), 400
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# =============================================================================
# Section: Scripts File API (Deprecated)
# =============================================================================
# Older per-file script endpoints retained for backward compatibility.

def _scripts_root() -> str:
    # Scripts live under Assemblies. We unify listing under Assemblies and
    # only allow access within top-level folders: "Scripts" and "Ansible Playbooks".
    return os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies")
    )

def _scripts_allowed_top_levels() -> List[str]:
    # Scripts API is scoped strictly to the Scripts top-level.
    return ["Scripts"]

def _is_valid_scripts_relpath(rel_path: str) -> bool:
    try:
        p = (rel_path or "").replace("\\", "/").lstrip("/")
        if not p:
            return False
        top = p.split("/", 1)[0]
        return top in _scripts_allowed_top_levels()
    except Exception:
        return False


def _detect_script_type(filename: str) -> str:
    fn_lower = (filename or "").lower()
    if fn_lower.endswith(".json") and os.path.isfile(filename):
        try:
            obj = _safe_read_json(filename)
            if isinstance(obj, dict):
                typ = str(obj.get("type") or obj.get("script_type") or "").strip().lower()
                if typ in ("powershell", "batch", "bash", "ansible"):
                    return typ
        except Exception:
            pass
        return "powershell"
    if fn_lower.endswith(".yml"):
        return "ansible"
    if fn_lower.endswith(".ps1"):
        return "powershell"
    if fn_lower.endswith(".bat"):
        return "batch"
    if fn_lower.endswith(".sh"):
        return "bash"
    return "unknown"


def _ext_for_type(script_type: str) -> str:
    t = (script_type or "").lower()
    if t in ("ansible", "powershell", "batch", "bash"):
        return ".json"
    return ".json"


"""
Legacy scripts endpoints removed in favor of unified assembly APIs.
"""
# Endpoint: /api/scripts/list — methods GET.

@app.route("/api/scripts/list", methods=["GET"])
def list_scripts():
    """Scan <ProjectRoot>/Assemblies/Scripts for script files and return list + folders."""
    scripts_root = _scripts_root()
    results: List[Dict] = []
    folders: List[str] = []

    if not os.path.isdir(scripts_root):
        return jsonify({
            "root": scripts_root,
            "scripts": [],
            "folders": []
        }), 200

    exts = (".yml", ".ps1", ".bat", ".sh")
    for top in _scripts_allowed_top_levels():
        base_dir = os.path.join(scripts_root, top)
        if not os.path.isdir(base_dir):
            continue
        for root, dirs, files in os.walk(base_dir):
            rel_root = os.path.relpath(root, scripts_root)
            if rel_root != ".":
                folders.append(rel_root.replace(os.sep, "/"))
            for fname in files:
                if not fname.lower().endswith(exts):
                    continue

                full_path = os.path.join(root, fname)
                rel_path = os.path.relpath(full_path, scripts_root)
                parts = rel_path.split(os.sep)
                folder_parts = parts[:-1]
                breadcrumb_prefix = " > ".join(folder_parts) if folder_parts else ""
                display_name = f"{breadcrumb_prefix} > {fname}" if breadcrumb_prefix else fname

                try:
                    mtime = os.path.getmtime(full_path)
                except Exception:
                    mtime = 0.0

                results.append({
                    "name": display_name,
                    "breadcrumb_prefix": breadcrumb_prefix,
                    "file_name": fname,
                    "rel_path": rel_path.replace(os.sep, "/"),
                    "type": _detect_script_type(fname),
                    "last_edited": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime)),
                    "last_edited_epoch": mtime
                })

    results.sort(key=lambda x: x.get("last_edited_epoch", 0.0), reverse=True)

    return jsonify({"error": "deprecated; use /api/assembly/list?island=scripts"}), 410


# Endpoint: /api/scripts/load — methods GET.

@app.route("/api/scripts/load", methods=["GET"])
def load_script():
    rel_path = request.args.get("path", "")
    scripts_root = _scripts_root()
    abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
    if (not abs_path.startswith(scripts_root)) or (not _is_valid_scripts_relpath(rel_path)) or (not os.path.isfile(abs_path)):
        return jsonify({"error": "Script not found"}), 404
    try:
        with open(abs_path, "r", encoding="utf-8", errors="replace") as fh:
            content = fh.read()
        return jsonify({"error": "deprecated; use /api/assembly/load?island=scripts&path=..."}), 410
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/scripts/save — methods POST.

@app.route("/api/scripts/save", methods=["POST"])
def save_script():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    name = (data.get("name") or "").strip()
    content = data.get("content")
    script_type = (data.get("type") or "").strip().lower()

    if content is None:
        return jsonify({"error": "Missing content"}), 400

    scripts_root = _scripts_root()
    os.makedirs(scripts_root, exist_ok=True)

    # Determine target path
    if rel_path:
        # Append extension only if none provided
        base, ext = os.path.splitext(rel_path)
        if not ext:
            desired_ext = _ext_for_type(script_type)
            if desired_ext:
                rel_path = base + desired_ext
        abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
        if not _is_valid_scripts_relpath(rel_path):
            return jsonify({"error": "Invalid path (must be under 'Scripts')"}), 400
    else:
        if not name:
            return jsonify({"error": "Missing name"}), 400
        # Append extension only if none provided
        ext = os.path.splitext(name)[1]
        if not ext:
            desired_ext = _ext_for_type(script_type) or ".txt"
            name = os.path.splitext(name)[0] + desired_ext
        # Default top-level folder is Scripts only (Playbooks handled separately)
        if (script_type or "").lower() == "ansible":
            return jsonify({"error": "Ansible playbooks are managed separately from scripts."}), 400
        abs_path = os.path.abspath(os.path.join(scripts_root, "Scripts", os.path.basename(name)))

    if not abs_path.startswith(scripts_root):
        return jsonify({"error": "Invalid path"}), 400

    os.makedirs(os.path.dirname(abs_path), exist_ok=True)
    return jsonify({"error": "deprecated; use /api/assembly/create or /api/assembly/edit"}), 410


# Endpoint: /api/scripts/rename_file — methods POST.

@app.route("/api/scripts/rename_file", methods=["POST"])
def rename_script_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    script_type = (data.get("type") or "").strip().lower()
    scripts_root = _scripts_root()
    old_abs = os.path.abspath(os.path.join(scripts_root, rel_path))
    if not old_abs.startswith(scripts_root) or not os.path.isfile(old_abs):
        return jsonify({"error": "File not found"}), 404
    if not new_name:
        return jsonify({"error": "Invalid new_name"}), 400
    # Append extension only if none provided
    if not os.path.splitext(new_name)[1]:
        desired_ext = _ext_for_type(script_type)
        if desired_ext:
            new_name = os.path.splitext(new_name)[0] + desired_ext
    new_abs = os.path.join(os.path.dirname(old_abs), os.path.basename(new_name))
    return jsonify({"error": "deprecated; use /api/assembly/rename"}), 410


# Endpoint: /api/scripts/move_file — methods POST.

@app.route("/api/scripts/move_file", methods=["POST"])
def move_script_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_rel = (data.get("new_path") or "").strip()
    scripts_root = _scripts_root()
    old_abs = os.path.abspath(os.path.join(scripts_root, rel_path))
    new_abs = os.path.abspath(os.path.join(scripts_root, new_rel))
    if not old_abs.startswith(scripts_root) or not os.path.isfile(old_abs):
        return jsonify({"error": "File not found"}), 404
    if (not new_abs.startswith(scripts_root)) or (not _is_valid_scripts_relpath(new_rel)):
        return jsonify({"error": "Invalid destination"}), 400
    os.makedirs(os.path.dirname(new_abs), exist_ok=True)
    return jsonify({"error": "deprecated; use /api/assembly/move"}), 410


# Endpoint: /api/scripts/delete_file — methods POST.

@app.route("/api/scripts/delete_file", methods=["POST"])
def delete_script_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    scripts_root = _scripts_root()
    abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
    if (not abs_path.startswith(scripts_root)) or (not _is_valid_scripts_relpath(rel_path)) or (not os.path.isfile(abs_path)):
        return jsonify({"error": "File not found"}), 404
    return jsonify({"error": "deprecated; use /api/assembly/delete"}), 410

# =============================================================================
# Section: Ansible File API (Deprecated)
# =============================================================================
# Legacy Ansible playbook file operations pending assembly migration.

def _ansible_root() -> str:
    return os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Ansible_Playbooks")
    )


def _is_valid_ansible_relpath(rel_path: str) -> bool:
    try:
        p = (rel_path or "").replace("\\", "/").lstrip("/")
        # allow any subpath; prevent empty
        return bool(p)
    except Exception:
        return False


# Endpoint: /api/ansible/list — methods GET.

@app.route("/api/ansible/list", methods=["GET"])
def list_ansible():
    """Scan <ProjectRoot>/Assemblies/Ansible_Playbooks for .yml playbooks and return list + folders."""
    root = _ansible_root()
    results: List[Dict] = []
    folders: List[str] = []
    if not os.path.isdir(root):
        os.makedirs(root, exist_ok=True)
        return jsonify({ "root": root, "items": [], "folders": [] }), 200
    for r, dirs, files in os.walk(root):
        rel_root = os.path.relpath(r, root)
        if rel_root != ".":
            folders.append(rel_root.replace(os.sep, "/"))
        for fname in files:
            if not fname.lower().endswith(".yml"):
                continue
            full_path = os.path.join(r, fname)
            rel_path = os.path.relpath(full_path, root).replace(os.sep, "/")
            try:
                mtime = os.path.getmtime(full_path)
            except Exception:
                mtime = 0.0
            results.append({
                "file_name": fname,
                "rel_path": rel_path,
                "type": "ansible",
                "last_edited": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime)),
                "last_edited_epoch": mtime
            })
    return jsonify({"error": "deprecated; use /api/assembly/list?island=ansible"}), 410


# Endpoint: /api/ansible/load — methods GET.

@app.route("/api/ansible/load", methods=["GET"])
def load_ansible():
    rel_path = request.args.get("path", "")
    root = _ansible_root()
    abs_path = os.path.abspath(os.path.join(root, rel_path))
    if (not abs_path.startswith(root)) or (not _is_valid_ansible_relpath(rel_path)) or (not os.path.isfile(abs_path)):
        return jsonify({"error": "Playbook not found"}), 404
    try:
        with open(abs_path, "r", encoding="utf-8", errors="replace") as fh:
            content = fh.read()
        return jsonify({"error": "deprecated; use /api/assembly/load?island=ansible&path=..."}), 410
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/ansible/save — methods POST.

@app.route("/api/ansible/save", methods=["POST"])
def save_ansible():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    name = (data.get("name") or "").strip()
    content = data.get("content")
    if content is None:
        return jsonify({"error": "Missing content"}), 400
    root = _ansible_root()
    os.makedirs(root, exist_ok=True)
    if rel_path:
        base, ext = os.path.splitext(rel_path)
        if not ext:
            rel_path = base + ".yml"
        abs_path = os.path.abspath(os.path.join(root, rel_path))
    else:
        if not name:
            return jsonify({"error": "Missing name"}), 400
        ext = os.path.splitext(name)[1]
        if not ext:
            name = os.path.splitext(name)[0] + ".yml"
        abs_path = os.path.abspath(os.path.join(root, os.path.basename(name)))
    if not abs_path.startswith(root):
        return jsonify({"error": "Invalid path"}), 400
    os.makedirs(os.path.dirname(abs_path), exist_ok=True)
    return jsonify({"error": "deprecated; use /api/assembly/create or /api/assembly/edit"}), 410


# Endpoint: /api/ansible/rename_file — methods POST.

@app.route("/api/ansible/rename_file", methods=["POST"])
def rename_ansible_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    root = _ansible_root()
    old_abs = os.path.abspath(os.path.join(root, rel_path))
    if not old_abs.startswith(root) or not os.path.isfile(old_abs):
        return jsonify({"error": "File not found"}), 404
    if not new_name:
        return jsonify({"error": "Invalid new_name"}), 400
    if not os.path.splitext(new_name)[1]:
        new_name = os.path.splitext(new_name)[0] + ".yml"
    new_abs = os.path.join(os.path.dirname(old_abs), os.path.basename(new_name))
    return jsonify({"error": "deprecated; use /api/assembly/rename"}), 410


# Endpoint: /api/ansible/move_file — methods POST.

@app.route("/api/ansible/move_file", methods=["POST"])
def move_ansible_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_rel = (data.get("new_path") or "").strip()
    root = _ansible_root()
    old_abs = os.path.abspath(os.path.join(root, rel_path))
    new_abs = os.path.abspath(os.path.join(root, new_rel))
    if not old_abs.startswith(root) or not os.path.isfile(old_abs):
        return jsonify({"error": "File not found"}), 404
    if (not new_abs.startswith(root)) or (not _is_valid_ansible_relpath(new_rel)):
        return jsonify({"error": "Invalid destination"}), 400
    os.makedirs(os.path.dirname(new_abs), exist_ok=True)
    return jsonify({"error": "deprecated; use /api/assembly/move"}), 410


# Endpoint: /api/ansible/delete_file — methods POST.

@app.route("/api/ansible/delete_file", methods=["POST"])
def delete_ansible_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    root = _ansible_root()
    abs_path = os.path.abspath(os.path.join(root, rel_path))
    if (not abs_path.startswith(root)) or (not _is_valid_ansible_relpath(rel_path)) or (not os.path.isfile(abs_path)):
        return jsonify({"error": "File not found"}), 404
    return jsonify({"error": "deprecated; use /api/assembly/delete"}), 410


# Endpoint: /api/ansible/create_folder — methods POST.

@app.route("/api/ansible/create_folder", methods=["POST"])
def ansible_create_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    root = _ansible_root()
    rel_path = (rel_path or "").replace("\\", "/").strip("/")
    abs_path = os.path.abspath(os.path.join(root, rel_path))
    if not abs_path.startswith(root):
        return jsonify({"error": "Invalid path"}), 400
    return jsonify({"error": "deprecated; use /api/assembly/create"}), 410


# Endpoint: /api/ansible/delete_folder — methods POST.

@app.route("/api/ansible/delete_folder", methods=["POST"])
def ansible_delete_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    root = _ansible_root()
    abs_path = os.path.abspath(os.path.join(root, rel_path))
    if (not abs_path.startswith(root)) or (not _is_valid_ansible_relpath(rel_path)) or (not os.path.isdir(abs_path)):
        return jsonify({"error": "Folder not found"}), 404
    rel_norm = (rel_path or "").replace("\\", "/").strip("/")
    if rel_norm in ("",):
        return jsonify({"error": "Cannot delete top-level folder"}), 400
    return jsonify({"error": "deprecated; use /api/assembly/delete"}), 410


# Endpoint: /api/ansible/rename_folder — methods POST.

@app.route("/api/ansible/rename_folder", methods=["POST"])
def ansible_rename_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    root = _ansible_root()
    old_abs = os.path.abspath(os.path.join(root, rel_path))
    if not old_abs.startswith(root) or not os.path.isdir(old_abs):
        return jsonify({"error": "Folder not found"}), 404
    if not new_name:
        return jsonify({"error": "Invalid new_name"}), 400
    rel_norm = (rel_path or "").replace("\\", "/").strip("/")
    if rel_norm in ("",):
        return jsonify({"error": "Cannot rename top-level folder"}), 400
    new_abs = os.path.join(os.path.dirname(old_abs), new_name)
    return jsonify({"error": "deprecated; use /api/assembly/rename"}), 410


# Endpoint: /api/scripts/create_folder — methods POST.

@app.route("/api/scripts/create_folder", methods=["POST"])
def scripts_create_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    scripts_root = _scripts_root()
    # If caller provided a path that does not include a valid top-level,
    # default to creating under the "Scripts" top-level for convenience.
    if not _is_valid_scripts_relpath(rel_path):
        rel_path = os.path.join("Scripts", rel_path) if rel_path else "Scripts"
    abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
    if not abs_path.startswith(scripts_root):
        return jsonify({"error": "Invalid path"}), 400
    return jsonify({"error": "deprecated; use /api/assembly/create"}), 410


# Endpoint: /api/scripts/delete_folder — methods POST.

@app.route("/api/scripts/delete_folder", methods=["POST"])
def scripts_delete_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    scripts_root = _scripts_root()
    abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
    if (not abs_path.startswith(scripts_root)) or (not _is_valid_scripts_relpath(rel_path)) or (not os.path.isdir(abs_path)):
        return jsonify({"error": "Folder not found"}), 404
    rel_norm = (rel_path or "").replace("\\", "/").strip("/")
    if rel_norm in ("Scripts", "Ansible Playbooks"):
        return jsonify({"error": "Cannot delete top-level folder"}), 400
    return jsonify({"error": "deprecated; use /api/assembly/delete"}), 410


# Endpoint: /api/scripts/rename_folder — methods POST.

@app.route("/api/scripts/rename_folder", methods=["POST"])
def scripts_rename_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    scripts_root = _scripts_root()
    old_abs = os.path.abspath(os.path.join(scripts_root, rel_path))
    if not old_abs.startswith(scripts_root) or not os.path.isdir(old_abs):
        return jsonify({"error": "Folder not found"}), 404
    if not new_name:
        return jsonify({"error": "Invalid new_name"}), 400
    rel_norm = (rel_path or "").replace("\\", "/").strip("/")
    if rel_norm in ("Scripts", "Ansible Playbooks"):
        return jsonify({"error": "Cannot rename top-level folder"}), 400
    new_abs = os.path.join(os.path.dirname(old_abs), new_name)
    return jsonify({"error": "deprecated; use /api/assembly/rename"}), 410

# =============================================================================
# Section: Agent Lifecycle API
# =============================================================================
# Agent registration, configuration, device persistence, and screenshot streaming metadata.

registered_agents: Dict[str, Dict] = {}
agent_configurations: Dict[str, Dict] = {}
latest_images: Dict[str, Dict] = {}

DEVICE_TABLE = "devices"
_DEVICE_JSON_LIST_FIELDS = {
    "memory": [],
    "network": [],
    "software": [],
    "storage": [],
}
_DEVICE_JSON_OBJECT_FIELDS = {
    "cpu": {},
}


_DEVICE_TABLE_COLUMNS = [
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
    "ssl_key_fingerprint",
    "token_version",
    "status",
    "key_added_at",
]


def _device_column_sql(alias: Optional[str] = None) -> str:
    if alias:
        return ", ".join(f"{alias}.{col}" for col in _DEVICE_TABLE_COLUMNS)
    return ", ".join(_DEVICE_TABLE_COLUMNS)


def _parse_device_json(raw: Optional[str], default: Any) -> Any:
    candidate = default
    if raw is None:
        return json.loads(json.dumps(default)) if isinstance(default, (list, dict)) else default
    try:
        data = json.loads(raw)
    except Exception:
        data = None
    if isinstance(default, list):
        if isinstance(data, list):
            return data
        return []
    if isinstance(default, dict):
        if isinstance(data, dict):
            return data
        return {}
    return default


def _ts_to_iso(ts: Optional[int]) -> str:
    if not ts:
        return ""
    try:
        from datetime import datetime, timezone

        return datetime.fromtimestamp(int(ts), timezone.utc).isoformat()
    except Exception:
        return ""


def _ts_to_human(ts: Optional[int]) -> str:
    if not ts:
        return ""
    try:
        from datetime import datetime

        return datetime.utcfromtimestamp(int(ts)).strftime("%Y-%m-%d %H:%M:%S")
    except Exception:
        return ""


def _row_to_device_dict(row: Sequence[Any], columns: Sequence[str]) -> Dict[str, Any]:
    return {columns[idx]: row[idx] for idx in range(min(len(row), len(columns)))}


def _assemble_device_snapshot(record: Dict[str, Any]) -> Dict[str, Any]:
    parsed_lists: Dict[str, List[Any]] = {}
    for field, default in _DEVICE_JSON_LIST_FIELDS.items():
        parsed_lists[field] = _parse_device_json(record.get(field), default)
    cpu_obj = _parse_device_json(record.get("cpu"), _DEVICE_JSON_OBJECT_FIELDS["cpu"])

    created_ts = _coerce_int(record.get("created_at")) or 0
    last_seen_ts = _coerce_int(record.get("last_seen")) or 0
    uptime_val = _coerce_int(record.get("uptime")) or 0

    agent_hash = _clean_device_str(record.get("agent_hash")) or ""
    normalized_guid = _normalize_guid(record.get("guid")) if record.get("guid") else ""

    summary: Dict[str, Any] = {
        "hostname": record.get("hostname") or "",
        "description": record.get("description") or "",
        "agent_hash": agent_hash,
        "agent_guid": normalized_guid,
        "agent_id": _clean_device_str(record.get("agent_id")) or "",
        "device_type": _clean_device_str(record.get("device_type")) or "",
        "domain": _clean_device_str(record.get("domain")) or "",
        "external_ip": _clean_device_str(record.get("external_ip")) or "",
        "internal_ip": _clean_device_str(record.get("internal_ip")) or "",
        "last_reboot": _clean_device_str(record.get("last_reboot")) or "",
        "last_seen": last_seen_ts,
        "last_user": _clean_device_str(record.get("last_user")) or "",
        "operating_system": _clean_device_str(record.get("operating_system")) or "",
        "uptime": uptime_val,
        "uptime_sec": uptime_val,
        "created_at": created_ts,
        "created": _ts_to_human(created_ts),
        "connection_type": _clean_device_str(record.get("connection_type")) or "",
        "connection_endpoint": _clean_device_str(record.get("connection_endpoint")) or "",
        "ansible_ee_ver": _clean_device_str(record.get("ansible_ee_ver")) or "",
    }

    details = {
        "summary": summary,
        "memory": parsed_lists["memory"],
        "network": parsed_lists["network"],
        "software": parsed_lists["software"],
        "storage": parsed_lists["storage"],
        "cpu": cpu_obj,
    }

    payload: Dict[str, Any] = {
        "hostname": summary["hostname"],
        "description": summary.get("description", ""),
        "created_at": created_ts,
        "created_at_iso": _ts_to_iso(created_ts),
        "agent_hash": agent_hash,
        "agent_guid": normalized_guid,
        "guid": normalized_guid,
        "memory": parsed_lists["memory"],
        "network": parsed_lists["network"],
        "software": parsed_lists["software"],
        "storage": parsed_lists["storage"],
        "cpu": cpu_obj,
        "device_type": summary.get("device_type", ""),
        "domain": summary.get("domain", ""),
        "external_ip": summary.get("external_ip", ""),
        "internal_ip": summary.get("internal_ip", ""),
        "last_reboot": summary.get("last_reboot", ""),
        "last_seen": last_seen_ts,
        "last_seen_iso": _ts_to_iso(last_seen_ts),
        "last_user": summary.get("last_user", ""),
        "operating_system": summary.get("operating_system", ""),
        "uptime": uptime_val,
        "agent_id": summary.get("agent_id", ""),
        "connection_type": summary.get("connection_type", ""),
        "connection_endpoint": summary.get("connection_endpoint", ""),
        "details": details,
        "summary": summary,
    }
    return payload


def _load_device_snapshot(
    cur: sqlite3.Cursor,
    *,
    hostname: Optional[str] = None,
    guid: Optional[str] = None,
) -> Optional[Dict[str, Any]]:
    if hostname:
        cur.execute(
            f"SELECT {_device_column_sql()} FROM {DEVICE_TABLE} WHERE hostname = ?",
            (hostname,),
        )
    elif guid:
        normalized_guid = _normalize_guid(guid)
        if not normalized_guid:
            return None
        cur.execute(
            f"SELECT {_device_column_sql()} FROM {DEVICE_TABLE} WHERE LOWER(guid) = ?",
            (normalized_guid.lower(),),
        )
    else:
        return None
    row = cur.fetchone()
    if not row:
        return None
    record = _row_to_device_dict(row, _DEVICE_TABLE_COLUMNS)
    return _assemble_device_snapshot(record)


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
    payload = {}

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
        summary.get("last_user")
        or summary.get("last_user_name")
        or summary.get("username")
    )
    payload["operating_system"] = _clean_device_str(
        summary.get("operating_system")
        or summary.get("agent_operating_system")
        or summary.get("os")
    )
    uptime_value = (
        summary.get("uptime_sec")
        or summary.get("uptime_seconds")
        or summary.get("uptime")
    )
    payload["uptime"] = _coerce_int(uptime_value)
    payload["agent_id"] = _clean_device_str(summary.get("agent_id"))
    payload["ansible_ee_ver"] = _clean_device_str(summary.get("ansible_ee_ver"))
    payload["connection_type"] = _clean_device_str(
        summary.get("connection_type")
        or summary.get("remote_type")
    )
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
            normalized_guid = _normalize_guid(normalized_guid)
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


# --- Simple at-rest secret handling for service account passwords ---
_SERVER_SECRET_PATH = os.path.abspath(os.path.join(os.path.dirname(__file__), 'server_secret.key'))


def _load_or_create_secret_key() -> Optional[bytes]:
    try:
        # Prefer explicit env var (base64-encoded)
        key_env = os.environ.get('BOREALIS_SECRET_KEY')
        if key_env:
            try:
                return base64.urlsafe_b64decode(key_env.encode('utf-8'))
            except Exception:
                # If env holds raw Fernet key already
                try:
                    b = key_env.encode('utf-8')
                    # Basic format check for Fernet keys (urlsafe base64 32 bytes -> 44 chars)
                    if len(b) in (32, 44):
                        return b
                except Exception:
                    pass
        # Else manage a local key file alongside server.py
        if os.path.isfile(_SERVER_SECRET_PATH):
            with open(_SERVER_SECRET_PATH, 'rb') as fh:
                return fh.read().strip()
        # Create when cryptography is available
        if Fernet is not None:
            k = Fernet.generate_key()
            try:
                with open(_SERVER_SECRET_PATH, 'wb') as fh:
                    fh.write(k)
            except Exception:
                pass
            return k
    except Exception:
        pass
    return None


_SECRET_KEY_BYTES = _load_or_create_secret_key()


def _encrypt_secret(plaintext: str) -> bytes:
    try:
        if Fernet is not None and _SECRET_KEY_BYTES:
            f = Fernet(_SECRET_KEY_BYTES)
            return f.encrypt((plaintext or '').encode('utf-8'))
    except Exception:
        pass
    # Fallback: reversible base64 (not secure). Kept to avoid blocking dev if crypto missing.
    try:
        return base64.b64encode((plaintext or '').encode('utf-8'))
    except Exception:
        return (plaintext or '').encode('utf-8')


def _decrypt_secret(blob: Optional[bytes]) -> str:
    if blob is None:
        return ''
    try:
        data = bytes(blob)
    except Exception:
        try:
            data = (blob or b'')  # type: ignore
        except Exception:
            data = b''
    # Try Fernet first
    try:
        if Fernet is not None and _SECRET_KEY_BYTES:
            f = Fernet(_SECRET_KEY_BYTES)
            return f.decrypt(data).decode('utf-8', errors='replace')
    except Exception:
        pass
    # Fall back to base64 decode
    try:
        return base64.b64decode(data).decode('utf-8', errors='replace')
    except Exception:
        try:
            return data.decode('utf-8', errors='replace')
        except Exception:
            return ''


def _normalize_credential_type(value: Optional[str]) -> str:
    val = (value or '').strip().lower()
    if val in {"machine", "ssh", "domain", "token", "api"}:
        return val if val != "ssh" else "machine"
    return "machine"


def _normalize_connection_type(value: Optional[str]) -> str:
    val = (value or '').strip().lower()
    if val in {"ssh", "linux", "unix"}:
        return "ssh"
    if val in {"winrm", "windows"}:
        return "winrm"
    if val in {"api", "http"}:
        return "api"
    return "ssh"


def _normalize_become_method(value: Optional[str]) -> str:
    val = (value or '').strip().lower()
    if val in {"", "none", "no", "false"}:
        return ""
    if val in {"sudo", "su", "runas", "enable"}:
        return val
    return ""


def _secret_from_payload(value) -> Optional[bytes]:
    if value is None:
        return None
    if isinstance(value, str):
        if value.strip() == "":
            return None
        return _encrypt_secret(value)
    text = str(value)
    if not text.strip():
        return None
    return _encrypt_secret(text)


def _coerce_int(value) -> Optional[int]:
    try:
        if value is None:
            return None
        if isinstance(value, str) and value.strip() == "":
            return None
        return int(value)
    except Exception:
        return None


def _credential_row_to_dict(row) -> Dict[str, Any]:
    if not row:
        return {}
    # Support both sqlite3.Row and tuple
    try:
        getter = row.__getitem__
        keys = row.keys() if hasattr(row, "keys") else None
    except Exception:
        getter = None
        keys = None

    def _get(field, index=None):
        if getter and keys:
            try:
                return getter(field)
            except Exception:
                pass
        if index is not None:
            try:
                return row[index]
            except Exception:
                return None
        return None

    metadata_json = _get("metadata_json")
    metadata = {}
    if metadata_json:
        try:
            metadata = json.loads(metadata_json)
            if not isinstance(metadata, dict):
                metadata = {}
        except Exception:
            metadata = {}
    created_at = _get("created_at")
    updated_at = _get("updated_at")
    out = {
        "id": _get("id"),
        "name": _get("name"),
        "description": _get("description") or "",
        "credential_type": _get("credential_type") or "machine",
        "connection_type": _get("connection_type") or "ssh",
        "site_id": _get("site_id"),
        "site_name": _get("site_name"),
        "username": _get("username") or "",
        "become_method": _get("become_method") or "",
        "become_username": _get("become_username") or "",
        "has_password": bool(_get("has_password")),
        "has_private_key": bool(_get("has_private_key")),
        "has_private_key_passphrase": bool(_get("has_private_key_passphrase")),
        "has_become_password": bool(_get("has_become_password")),
        "metadata": metadata,
        "created_at": int(created_at or 0),
        "updated_at": int(updated_at or 0),
    }
    return out


def _query_credentials(where_clause: str = "", params: Sequence[Any] = ()) -> List[Dict[str, Any]]:
    try:
        conn = _db_conn()
        conn.row_factory = sqlite3.Row  # type: ignore[attr-defined]
        cur = conn.cursor()
        sql = """
            SELECT
                c.id,
                c.name,
                c.description,
                c.credential_type,
                c.connection_type,
                c.username,
                c.site_id,
                s.name AS site_name,
                c.become_method,
                c.become_username,
                c.metadata_json,
                c.created_at,
                c.updated_at,
                CASE WHEN c.password_encrypted IS NOT NULL AND LENGTH(c.password_encrypted) > 0 THEN 1 ELSE 0 END AS has_password,
                CASE WHEN c.private_key_encrypted IS NOT NULL AND LENGTH(c.private_key_encrypted) > 0 THEN 1 ELSE 0 END AS has_private_key,
                CASE WHEN c.private_key_passphrase_encrypted IS NOT NULL AND LENGTH(c.private_key_passphrase_encrypted) > 0 THEN 1 ELSE 0 END AS has_private_key_passphrase,
                CASE WHEN c.become_password_encrypted IS NOT NULL AND LENGTH(c.become_password_encrypted) > 0 THEN 1 ELSE 0 END AS has_become_password
            FROM credentials c
            LEFT JOIN sites s ON s.id = c.site_id
        """
        if where_clause:
            sql += f" WHERE {where_clause} "
        sql += " ORDER BY LOWER(c.name)"
        cur.execute(sql, params)
        rows = cur.fetchall()
        conn.close()
        return [_credential_row_to_dict(r) for r in rows]
    except Exception as exc:
        _write_service_log("server", f"credential query failure: {exc}")
        return []


def _fetch_credential_record(credential_id: int) -> Optional[Dict[str, Any]]:
    rows = _query_credentials("c.id = ?", (credential_id,))
    if rows:
        return rows[0]
    return None


def _secret_fingerprint(secret_blob: Optional[bytes]) -> str:
    if not secret_blob:
        return ""
    try:
        import hashlib
        plaintext = _decrypt_secret(secret_blob)
        if not plaintext:
            return ""
        digest = hashlib.sha256(plaintext.encode("utf-8")).hexdigest()
        return digest[:16]
    except Exception:
        return ""


def init_db():
    """Initialize all required tables in the unified database."""
    conn = _db_conn()
    db_migrations.apply_all(conn)
    cur = conn.cursor()

    # Device table (renamed from historical device_details)
    cur.execute(
        "SELECT name FROM sqlite_master WHERE type='table' AND name=?",
        (DEVICE_TABLE,),
    )
    has_devices = cur.fetchone()
    if not has_devices:
        cur.execute(
            "SELECT name FROM sqlite_master WHERE type='table' AND name='device_details'"
        )
        legacy = cur.fetchone()
        if legacy:
            cur.execute("ALTER TABLE device_details RENAME TO devices")
        else:
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS devices (
                    hostname TEXT PRIMARY KEY,
                    description TEXT,
                    created_at INTEGER,
                    agent_hash TEXT,
                    guid TEXT,
                    memory TEXT,
                    network TEXT,
                    software TEXT,
                    storage TEXT,
                    cpu TEXT,
                    device_type TEXT,
                    domain TEXT,
                    external_ip TEXT,
                    internal_ip TEXT,
                    last_reboot TEXT,
                    last_seen INTEGER,
                    last_user TEXT,
                    operating_system TEXT,
                    uptime INTEGER,
                    agent_id TEXT,
                    ansible_ee_ver TEXT,
                    connection_type TEXT,
                    connection_endpoint TEXT
                )
                """
            )

    # Ensure required columns exist on upgraded installs
    cur.execute(f"PRAGMA table_info({DEVICE_TABLE})")
    existing_cols = [r[1] for r in cur.fetchall()]

    def _ensure_column(name: str, decl: str) -> None:
        if name not in existing_cols:
            cur.execute(f"ALTER TABLE {DEVICE_TABLE} ADD COLUMN {name} {decl}")
            existing_cols.append(name)

    _ensure_column("description", "TEXT")
    _ensure_column("created_at", "INTEGER")
    _ensure_column("agent_hash", "TEXT")
    _ensure_column("guid", "TEXT")
    _ensure_column("memory", "TEXT")
    _ensure_column("network", "TEXT")
    _ensure_column("software", "TEXT")
    _ensure_column("storage", "TEXT")
    _ensure_column("cpu", "TEXT")
    _ensure_column("device_type", "TEXT")
    _ensure_column("domain", "TEXT")
    _ensure_column("external_ip", "TEXT")
    _ensure_column("internal_ip", "TEXT")
    _ensure_column("last_reboot", "TEXT")
    _ensure_column("last_seen", "INTEGER")
    _ensure_column("last_user", "TEXT")
    _ensure_column("operating_system", "TEXT")
    _ensure_column("uptime", "INTEGER")
    _ensure_column("agent_id", "TEXT")
    _ensure_column("ansible_ee_ver", "TEXT")
    _ensure_column("connection_type", "TEXT")
    _ensure_column("connection_endpoint", "TEXT")

    details_rows: List[Tuple[Any, ...]] = []
    if "details" in existing_cols:
        try:
            cur.execute(f"SELECT hostname, details FROM {DEVICE_TABLE}")
            details_rows = cur.fetchall()
        except Exception:
            details_rows = []
        for hostname, details_json in details_rows:
            try:
                details = json.loads(details_json or "{}")
            except Exception:
                details = {}
            column_values = _extract_device_columns(details)
            params = [column_values.get(col) for col in (
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
                "connection_type",
                "connection_endpoint",
            )]
            cur.execute(
                f"""
                UPDATE {DEVICE_TABLE}
                SET memory=?, network=?, software=?, storage=?, cpu=?,
                    device_type=?, domain=?, external_ip=?, internal_ip=?,
                    last_reboot=?, last_seen=?, last_user=?, operating_system=?,
                    uptime=?, agent_id=?, connection_type=?, connection_endpoint=?
                WHERE hostname=?
                """,
                params + [hostname],
            )

        if "details" in existing_cols:
            try:
                cur.execute(f"ALTER TABLE {DEVICE_TABLE} DROP COLUMN details")
                existing_cols.remove("details")
            except sqlite3.OperationalError:
                try:
                    cur.execute("BEGIN")
                except Exception:
                    pass
                temp_table = f"{DEVICE_TABLE}_old"
                cur.execute(f"ALTER TABLE {DEVICE_TABLE} RENAME TO {temp_table}")
                cur.execute(
                    """
                    CREATE TABLE IF NOT EXISTS devices (
                        hostname TEXT PRIMARY KEY,
                        description TEXT,
                        created_at INTEGER,
                        agent_hash TEXT,
                        guid TEXT,
                        memory TEXT,
                        network TEXT,
                        software TEXT,
                        storage TEXT,
                        cpu TEXT,
                        device_type TEXT,
                        domain TEXT,
                        external_ip TEXT,
                        internal_ip TEXT,
                        last_reboot TEXT,
                        last_seen INTEGER,
                        last_user TEXT,
                        operating_system TEXT,
                        uptime INTEGER,
                        agent_id TEXT,
                        ansible_ee_ver TEXT,
                        connection_type TEXT,
                        connection_endpoint TEXT
                    )
                    """
                )
                copy_cols = [col for col in _DEVICE_TABLE_COLUMNS if col != "details"]
                col_sql = ", ".join(copy_cols)
                cur.execute(
                    f"INSERT INTO {DEVICE_TABLE} ({col_sql}) SELECT {col_sql} FROM {temp_table}"
                )
                cur.execute(f"DROP TABLE {temp_table}")
                cur.execute("COMMIT")
                cur.execute(f"PRAGMA table_info({DEVICE_TABLE})")
                existing_cols = [r[1] for r in cur.fetchall()]

    # Activity history table for script/job runs
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS activity_history (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            hostname TEXT,
            script_path TEXT,
            script_name TEXT,
            script_type TEXT,
            ran_at INTEGER,
            status TEXT,
            stdout TEXT,
            stderr TEXT
        )
        """
    )

    # Saved device list views
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS device_list_views (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL,
            columns_json TEXT NOT NULL,
            filters_json TEXT,
            created_at INTEGER,
            updated_at INTEGER
        )
        """
    )

    # Sites master table
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS sites (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL,
            description TEXT,
            created_at INTEGER
        )
        """
    )

    # Device assignments. A device (hostname) can be assigned to at most one site.
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS device_sites (
            device_hostname TEXT UNIQUE NOT NULL,
            site_id INTEGER NOT NULL,
            assigned_at INTEGER,
            FOREIGN KEY(site_id) REFERENCES sites(id) ON DELETE CASCADE
        )
        """
    )

    # Users table
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS users (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            username TEXT UNIQUE NOT NULL,
            display_name TEXT,
            password_sha512 TEXT NOT NULL,
            role TEXT NOT NULL DEFAULT 'Admin',
            last_login INTEGER,
            created_at INTEGER,
            updated_at INTEGER,
            mfa_enabled INTEGER NOT NULL DEFAULT 0,
            mfa_secret TEXT
        )
        """
    )

    try:
        cur.execute("PRAGMA table_info(users)")
        user_cols = [r[1] for r in cur.fetchall()]
        if "mfa_enabled" not in user_cols:
            cur.execute("ALTER TABLE users ADD COLUMN mfa_enabled INTEGER NOT NULL DEFAULT 0")
            user_cols.append("mfa_enabled")
        if "mfa_secret" not in user_cols:
            cur.execute("ALTER TABLE users ADD COLUMN mfa_secret TEXT")
    except Exception:
        pass

    # Ansible play recap storage (one row per playbook run/session)
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS ansible_play_recaps (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            run_id TEXT UNIQUE NOT NULL,
            hostname TEXT,
            agent_id TEXT,
            playbook_path TEXT,
            playbook_name TEXT,
            scheduled_job_id INTEGER,
            scheduled_run_id INTEGER,
            activity_job_id INTEGER,
            status TEXT,
            recap_text TEXT,
            recap_json TEXT,
            started_ts INTEGER,
            finished_ts INTEGER,
            created_at INTEGER,
            updated_at INTEGER
        )
        """
    )
    try:
        # Helpful lookups for device views and run correlation
        cur.execute("CREATE INDEX IF NOT EXISTS idx_ansible_recaps_host_created ON ansible_play_recaps(hostname, created_at)")
        cur.execute("CREATE INDEX IF NOT EXISTS idx_ansible_recaps_status ON ansible_play_recaps(status)")
    except Exception:
        pass

    # Per-agent local service account credentials for Ansible WinRM loopback
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS agent_service_account (
          agent_id            TEXT PRIMARY KEY,
          username            TEXT NOT NULL,
          password_hash       BLOB,
          password_encrypted  BLOB NOT NULL,
          last_rotated_utc    TEXT NOT NULL,
          version             INTEGER NOT NULL DEFAULT 1
        )
        """
    )
    conn.commit()

    # Central credential vault for remote execution
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS credentials (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL UNIQUE,
            description TEXT,
            site_id INTEGER,
            credential_type TEXT NOT NULL DEFAULT 'machine',
            connection_type TEXT NOT NULL DEFAULT 'ssh',
            username TEXT,
            password_encrypted BLOB,
            private_key_encrypted BLOB,
            private_key_passphrase_encrypted BLOB,
            become_method TEXT,
            become_username TEXT,
            become_password_encrypted BLOB,
            metadata_json TEXT,
            created_at INTEGER NOT NULL,
            updated_at INTEGER NOT NULL,
            FOREIGN KEY(site_id) REFERENCES sites(id) ON DELETE SET NULL
        )
        """
    )
    try:
        cur.execute("PRAGMA table_info(credentials)")
        cred_cols = [row[1] for row in cur.fetchall()]
        if "connection_type" not in cred_cols:
            cur.execute("ALTER TABLE credentials ADD COLUMN connection_type TEXT NOT NULL DEFAULT 'ssh'")
        if "credential_type" not in cred_cols:
            cur.execute("ALTER TABLE credentials ADD COLUMN credential_type TEXT NOT NULL DEFAULT 'machine'")
        if "metadata_json" not in cred_cols:
            cur.execute("ALTER TABLE credentials ADD COLUMN metadata_json TEXT")
        if "private_key_passphrase_encrypted" not in cred_cols:
            cur.execute("ALTER TABLE credentials ADD COLUMN private_key_passphrase_encrypted BLOB")
        if "become_method" not in cred_cols:
            cur.execute("ALTER TABLE credentials ADD COLUMN become_method TEXT")
        if "become_username" not in cred_cols:
            cur.execute("ALTER TABLE credentials ADD COLUMN become_username TEXT")
        if "become_password_encrypted" not in cred_cols:
            cur.execute("ALTER TABLE credentials ADD COLUMN become_password_encrypted BLOB")
        if "site_id" not in cred_cols:
            cur.execute("ALTER TABLE credentials ADD COLUMN site_id INTEGER")
        if "description" not in cred_cols:
            cur.execute("ALTER TABLE credentials ADD COLUMN description TEXT")
    except Exception:
        pass
    conn.commit()

    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS github_token (
            token TEXT
        )
        """
    )
    conn.commit()

    # Scheduled jobs table
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS scheduled_jobs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            components_json TEXT NOT NULL,
            targets_json TEXT NOT NULL,
            schedule_type TEXT NOT NULL,
            start_ts INTEGER,
            duration_stop_enabled INTEGER DEFAULT 0,
            expiration TEXT,
            execution_context TEXT NOT NULL,
            credential_id INTEGER,
            use_service_account INTEGER NOT NULL DEFAULT 1,
            enabled INTEGER DEFAULT 1,
            created_at INTEGER,
            updated_at INTEGER
        )
        """
    )
    try:
        cur.execute("PRAGMA table_info(scheduled_jobs)")
        sj_cols = [row[1] for row in cur.fetchall()]
        if "credential_id" not in sj_cols:
            cur.execute("ALTER TABLE scheduled_jobs ADD COLUMN credential_id INTEGER")
        if "use_service_account" not in sj_cols:
            cur.execute("ALTER TABLE scheduled_jobs ADD COLUMN use_service_account INTEGER NOT NULL DEFAULT 1")
    except Exception:
        pass
    conn.commit()
    conn.close()


init_db()

enrollment_routes.register(
    app,
    db_conn_factory=_db_conn,
    log=_write_service_log,
    jwt_service=JWT_SERVICE,
    tls_bundle_path=TLS_BUNDLE_PATH,
    ip_rate_limiter=IP_RATE_LIMITER,
    fp_rate_limiter=FP_RATE_LIMITER,
    nonce_cache=ENROLLMENT_NONCE_CACHE,
    script_signer=SCRIPT_SIGNER,
)

token_routes.register(
    app,
    db_conn_factory=_db_conn,
    jwt_service=JWT_SERVICE,
    dpop_validator=DPOP_VALIDATOR,
)

agent_routes.register(
    app,
    db_conn_factory=_db_conn,
    auth_manager=DEVICE_AUTH_MANAGER,
    log=_write_service_log,
    script_signer=SCRIPT_SIGNER,
)

admin_routes.register(
    app,
    db_conn_factory=_db_conn,
    require_admin=_require_admin,
    current_user=_current_user,
    log=_write_service_log,
)

start_prune_job(
    socketio,
    db_conn_factory=_db_conn,
    log=_write_service_log,
)


def ensure_default_admin():
    """Ensure at least one admin user exists.

    If no user with role 'Admin' exists, create the default
    admin account (username 'admin', password 'Password').
    If an admin already exists, leave the user table untouched.
    """
    try:
        conn = _db_conn()
        cur = conn.cursor()

        # Check if any admin role exists (case-insensitive)
        cur.execute("SELECT COUNT(*) FROM users WHERE LOWER(role)='admin'")
        has_admin = (cur.fetchone()[0] or 0) > 0

        if not has_admin:
            now = _now_ts()
            default_hash = "e6c83b282aeb2e022844595721cc00bbda47cb24537c1779f9bb84f04039e1676e6ba8573e588da1052510e3aa0a32a9e55879ae22b0c2d62136fc0a3e85f8bb"

            # Prefer to (re)create the built-in 'admin' user if missing.
            # If a non-admin 'admin' user exists, promote it rather than failing insert.
            cur.execute("SELECT COUNT(*) FROM users WHERE LOWER(username)='admin'")
            admin_user_exists = (cur.fetchone()[0] or 0) > 0

            if not admin_user_exists:
                cur.execute(
                    "INSERT INTO users(username, display_name, password_sha512, role, created_at, updated_at) VALUES(?,?,?,?,?,?)",
                    ("admin", "Administrator", default_hash, "Admin", now, now)
                )
            else:
                # Promote existing 'admin' user to Admin if needed (preserve password)
                cur.execute(
                    "UPDATE users SET role='Admin', updated_at=? WHERE LOWER(username)='admin' AND LOWER(role)!='admin'",
                    (now,)
                )
            conn.commit()

        conn.close()
    except Exception:
        # Non-fatal if this fails; /health etc still work
        pass


ensure_default_admin()

# =============================================================================
# Section: Scheduler Integration
# =============================================================================
# Connect the Flask app to the background job scheduler and helpers.

job_scheduler = register_job_scheduler(app, socketio, DB_PATH)
scheduler_set_server_runner(job_scheduler, _queue_server_ansible_run)
scheduler_set_credential_fetcher(job_scheduler, _fetch_credential_with_secrets)
job_scheduler.start()

# Provide scheduler with online device lookup based on registered agents
def _online_hostnames_snapshot():
    # Consider agent online if we saw collector activity within last 5 minutes
    try:
        now = time.time()
        out = []
        for rec in (registered_agents or {}).values():
            host = rec.get('hostname')
            last = float(rec.get('collector_active_ts') or 0)
            if host and (now - last) <= 300:
                out.append(str(host))
        return out
    except Exception:
        return []

scheduler_set_online_lookup(job_scheduler, _online_hostnames_snapshot)

# =============================================================================
# Section: Site Management API
# =============================================================================
# CRUD for site records and device membership.


def _row_to_site(row):
    # id, name, description, created_at, device_count
    return {
        "id": row[0],
        "name": row[1],
        "description": row[2] or "",
        "created_at": row[3] or 0,
        "device_count": row[4] or 0,
    }


# Endpoint: /api/sites — methods GET.

@app.route("/api/sites", methods=["GET"])
def list_sites():
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            """
            SELECT s.id, s.name, s.description, s.created_at,
                   COALESCE(ds.cnt, 0) AS device_count
            FROM sites s
            LEFT JOIN (
                SELECT site_id, COUNT(*) AS cnt
                FROM device_sites
                GROUP BY site_id
            ) ds ON ds.site_id = s.id
            ORDER BY LOWER(s.name) ASC
            """
        )
        rows = cur.fetchall()
        conn.close()
        return jsonify({"sites": [_row_to_site(r) for r in rows]})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/sites — methods POST.

@app.route("/api/sites", methods=["POST"])
def create_site():
    payload = request.get_json(silent=True) or {}
    name = (payload.get("name") or "").strip()
    description = (payload.get("description") or "").strip()
    if not name:
        return jsonify({"error": "name is required"}), 400
    now = int(time.time())
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "INSERT INTO sites(name, description, created_at) VALUES (?, ?, ?)",
            (name, description, now),
        )
        site_id = cur.lastrowid
        conn.commit()
        # Return created row with device_count = 0
        cur.execute(
            "SELECT id, name, description, created_at, 0 FROM sites WHERE id = ?",
            (site_id,),
        )
        row = cur.fetchone()
        conn.close()
        return jsonify(_row_to_site(row))
    except sqlite3.IntegrityError:
        return jsonify({"error": "name already exists"}), 409
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/sites/delete — methods POST.

@app.route("/api/sites/delete", methods=["POST"])
def delete_sites():
    payload = request.get_json(silent=True) or {}
    ids = payload.get("ids") or []
    if not isinstance(ids, list) or not all(isinstance(x, (int, str)) for x in ids):
        return jsonify({"error": "ids must be a list"}), 400
    # Normalize to ints where possible
    norm_ids = []
    for x in ids:
        try:
            norm_ids.append(int(x))
        except Exception:
            pass
    if not norm_ids:
        return jsonify({"status": "ok", "deleted": 0})
    try:
        conn = _db_conn()
        cur = conn.cursor()
        # Clean assignments first (in case FK ON DELETE CASCADE not enforced)
        cur.execute(
            f"DELETE FROM device_sites WHERE site_id IN ({','.join('?'*len(norm_ids))})",
            tuple(norm_ids),
        )
        cur.execute(
            f"DELETE FROM sites WHERE id IN ({','.join('?'*len(norm_ids))})",
            tuple(norm_ids),
        )
        deleted = cur.rowcount
        conn.commit()
        conn.close()
        return jsonify({"status": "ok", "deleted": deleted})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/sites/device_map — methods GET.

@app.route("/api/sites/device_map", methods=["GET"])
def sites_device_map():
    """
    Map hostnames to assigned site.
    Optional query param: hostnames=comma,separated,list to filter.
    Returns: { mapping: { hostname: { site_id, site_name } } }
    """
    try:
        host_param = (request.args.get("hostnames") or "").strip()
        filter_set = set()
        if host_param:
            for part in host_param.split(','):
                p = part.strip()
                if p:
                    filter_set.add(p)
        conn = _db_conn()
        cur = conn.cursor()
        if filter_set:
            placeholders = ','.join('?' * len(filter_set))
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
        mapping = {}
        for hostname, site_id, site_name in cur.fetchall():
            mapping[str(hostname)] = {"site_id": site_id, "site_name": site_name}
        conn.close()
        return jsonify({"mapping": mapping})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/sites/assign — methods POST.

@app.route("/api/sites/assign", methods=["POST"])
def assign_devices_to_site():
    payload = request.get_json(silent=True) or {}
    site_id = payload.get("site_id")
    hostnames = payload.get("hostnames") or []
    try:
        site_id = int(site_id)
    except Exception:
        return jsonify({"error": "invalid site_id"}), 400
    if not isinstance(hostnames, list) or not all(isinstance(x, str) and x.strip() for x in hostnames):
        return jsonify({"error": "hostnames must be a list of strings"}), 400
    now = int(time.time())
    try:
        conn = _db_conn()
        cur = conn.cursor()
        # Ensure site exists
        cur.execute("SELECT 1 FROM sites WHERE id = ?", (site_id,))
        if not cur.fetchone():
            conn.close()
            return jsonify({"error": "site not found"}), 404
        # Assign each hostname (replace existing assignment if present)
        for hn in hostnames:
            hn = hn.strip()
            if not hn:
                continue
            cur.execute(
                "INSERT INTO device_sites(device_hostname, site_id, assigned_at) VALUES (?, ?, ?)\n"
                "ON CONFLICT(device_hostname) DO UPDATE SET site_id=excluded.site_id, assigned_at=excluded.assigned_at",
                (hn, site_id, now),
            )
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Rename a site (update name only)
# Endpoint: /api/sites/rename — methods POST.

@app.route("/api/sites/rename", methods=["POST"])
def rename_site():
    payload = request.get_json(silent=True) or {}
    site_id = payload.get("id")
    new_name = (payload.get("new_name") or "").strip()
    try:
        site_id = int(site_id)
    except Exception:
        return jsonify({"error": "invalid id"}), 400
    if not new_name:
        return jsonify({"error": "new_name is required"}), 400
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute("UPDATE sites SET name = ? WHERE id = ?", (new_name, site_id))
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "site not found"}), 404
        conn.commit()
        cur.execute(
            """
            SELECT s.id, s.name, s.description, s.created_at,
                   COALESCE(ds.cnt, 0) AS device_count
            FROM sites s
            LEFT JOIN (
                SELECT site_id, COUNT(*) AS cnt
                FROM device_sites
                GROUP BY site_id
            ) ds ON ds.site_id = s.id
            WHERE s.id = ?
            """,
            (site_id,)
        )
        row = cur.fetchone()
        conn.close()
        return jsonify(_row_to_site(row))
    except sqlite3.IntegrityError:
        return jsonify({"error": "name already exists"}), 409
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# =============================================================================
# Section: Global Search API
# =============================================================================
# Lightweight search surface for auto-complete over device data.


def _load_device_records(limit: int = 0):
    """
    Load device records from SQLite and flatten commonly-searched fields
    from the devices table. Returns a list of dicts with keys:
      hostname, description, last_user, internal_ip, external_ip, site_id, site_name
    """
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(f"SELECT {_device_column_sql()} FROM {DEVICE_TABLE}")
        rows = cur.fetchall()

        # Build device -> site mapping
        cur.execute(
            """
            SELECT ds.device_hostname, s.id, s.name
            FROM device_sites ds
            JOIN sites s ON s.id = ds.site_id
            """
        )
        site_map = {r[0]: {"site_id": r[1], "site_name": r[2]} for r in cur.fetchall()}

        conn.close()
    except Exception:
        rows = []
        site_map = {}

    out = []
    for row in rows:
        record = _row_to_device_dict(row, _DEVICE_TABLE_COLUMNS)
        snapshot = _assemble_device_snapshot(record)
        summary = snapshot.get("summary", {})
        rec = {
            "hostname": snapshot.get("hostname") or summary.get("hostname") or "",
            "description": snapshot.get("description") or summary.get("description") or "",
            "last_user": snapshot.get("last_user") or summary.get("last_user") or summary.get("last_user_name") or "",
            "internal_ip": snapshot.get("internal_ip") or summary.get("internal_ip") or "",
            "external_ip": snapshot.get("external_ip") or summary.get("external_ip") or "",
        }
        site_info = site_map.get(rec["hostname"]) or {}
        rec.update({
            "site_id": site_info.get("site_id"),
            "site_name": site_info.get("site_name") or "",
        })
        out.append(rec)
        if limit and len(out) >= limit:
            break
    return out


# Endpoint: /api/search/suggest — methods GET.

@app.route("/api/search/suggest", methods=["GET"])
def search_suggest():
    """
    Suggest results for the top-bar search with category selector.
    Query parameters:
      field: one of hostname|description|last_user|internal_ip|external_ip|serial_number|site_name|site_description
      q: text fragment (case-insensitive contains)
      limit: max results per group (default 5)
    Returns: { devices: [...], sites: [...], field: "...", q: "..." }
    """
    field = (request.args.get("field") or "hostname").strip().lower()
    q = (request.args.get("q") or "").strip()
    try:
        limit = int(request.args.get("limit") or 5)
    except Exception:
        limit = 5

    q_lc = q.lower()
    # Do not suggest on very short queries to avoid dumping all rows
    if len(q_lc) < 3:
        return jsonify({"field": field, "q": q, "devices": [], "sites": []})

    device_fields = {
        "hostname": "hostname",
        "description": "description",
        "last_user": "last_user",
        "internal_ip": "internal_ip",
        "external_ip": "external_ip",
        "serial_number": "serial_number",  # placeholder, currently not stored
    }
    site_fields = {
        "site_name": "name",
        "site_description": "description",
    }

    devices = []
    sites = []

    # Device suggestions
    if field in device_fields:
        key = device_fields[field]
        for rec in _load_device_records():
            # serial_number is not currently tracked; produce no suggestions
            if key == "serial_number":
                break
            val = str(rec.get(key) or "")
            if not q or q_lc in val.lower():
                devices.append({
                    "hostname": rec.get("hostname") or "",
                    "value": val,
                    "site_id": rec.get("site_id"),
                    "site_name": rec.get("site_name") or "",
                    "description": rec.get("description") or "",
                    "last_user": rec.get("last_user") or "",
                    "internal_ip": rec.get("internal_ip") or "",
                    "external_ip": rec.get("external_ip") or "",
                })
            if len(devices) >= limit:
                break

    # Site suggestions
    if field in site_fields:
        column = site_fields[field]
        try:
            conn = _db_conn()
            cur = conn.cursor()
            cur.execute("SELECT id, name, description FROM sites")
            for sid, name, desc in cur.fetchall():
                val = name if column == "name" else (desc or "")
                if not q or q_lc in str(val).lower():
                    sites.append({
                        "site_id": sid,
                        "site_name": name,
                        "site_description": desc or "",
                        "value": val or "",
                    })
                if len(sites) >= limit:
                    break
            conn.close()
        except Exception:
            pass

    return jsonify({
        "field": field,
        "q": q,
        "devices": devices,
        "sites": sites,
    })


# Endpoint: /api/devices — methods GET.

def _fetch_devices(
    *,
    connection_type: Optional[str] = None,
    hostname: Optional[str] = None,
    only_agents: bool = False,
) -> List[Dict[str, Any]]:
    try:
        conn = _db_conn()
        cur = conn.cursor()
        sql = f"""
            SELECT {_device_column_sql('d')},
                   s.id, s.name, s.description
            FROM {DEVICE_TABLE} d
            LEFT JOIN device_sites ds ON ds.device_hostname = d.hostname
            LEFT JOIN sites s ON s.id = ds.site_id
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
        conn.close()
    except Exception as exc:
        raise RuntimeError(str(exc)) from exc

    now = time.time()
    devices: List[Dict[str, Any]] = []
    for row in rows:
        core = row[: len(_DEVICE_TABLE_COLUMNS)]
        site_id, site_name, site_description = row[len(_DEVICE_TABLE_COLUMNS) :]
        record = _row_to_device_dict(core, _DEVICE_TABLE_COLUMNS)
        snapshot = _assemble_device_snapshot(record)
        summary = snapshot.get("summary", {})
        last_seen = snapshot.get("last_seen") or 0
        status = "Offline"
        try:
            if last_seen and (now - float(last_seen)) <= 300:
                status = "Online"
        except Exception:
            pass
        devices.append(
            {
                "hostname": snapshot.get("hostname") or summary.get("hostname") or "",
                "description": snapshot.get("description") or summary.get("description") or "",
                "details": snapshot.get("details", {}),
                "summary": summary,
                "created_at": snapshot.get("created_at") or 0,
                "created_at_iso": snapshot.get("created_at_iso") or _ts_to_iso(snapshot.get("created_at")),
                "agent_hash": snapshot.get("agent_hash") or summary.get("agent_hash") or "",
                "agent_guid": snapshot.get("agent_guid") or summary.get("agent_guid") or "",
                "guid": snapshot.get("agent_guid") or summary.get("agent_guid") or "",
                "memory": snapshot.get("memory", []),
                "network": snapshot.get("network", []),
                "software": snapshot.get("software", []),
                "storage": snapshot.get("storage", []),
                "cpu": snapshot.get("cpu", {}),
                "device_type": snapshot.get("device_type") or summary.get("device_type") or "",
                "domain": snapshot.get("domain") or "",
                "external_ip": snapshot.get("external_ip") or summary.get("external_ip") or "",
                "internal_ip": snapshot.get("internal_ip") or summary.get("internal_ip") or "",
                "connection_type": snapshot.get("connection_type") or summary.get("connection_type") or "",
                "connection_endpoint": snapshot.get("connection_endpoint") or summary.get("connection_endpoint") or "",
                "last_reboot": snapshot.get("last_reboot") or summary.get("last_reboot") or "",
                "last_seen": last_seen,
                "last_seen_iso": snapshot.get("last_seen_iso") or _ts_to_iso(last_seen),
                "last_user": snapshot.get("last_user") or summary.get("last_user") or "",
                "operating_system": snapshot.get("operating_system")
                or summary.get("operating_system")
                or summary.get("agent_operating_system")
                or "",
                "uptime": snapshot.get("uptime") or 0,
                "agent_id": snapshot.get("agent_id") or summary.get("agent_id") or "",
                "site_id": site_id,
                "site_name": site_name or "",
                "site_description": site_description or "",
                "status": status,
            }
        )
    return devices


@app.route("/api/devices", methods=["GET"])
def list_devices():
    """Return all devices with expanded columns for the WebUI."""
    try:
        devices = _fetch_devices()
    except RuntimeError as exc:
        return jsonify({"error": str(exc)}), 500
    return jsonify({"devices": devices})


def _upsert_remote_device(
    connection_type: str,
    hostname: str,
    address: Optional[str],
    description: Optional[str],
    os_hint: Optional[str],
    *,
    ensure_existing_type: Optional[str],
) -> Dict[str, Any]:
    conn = _db_conn()
    cur = conn.cursor()
    existing = _load_device_snapshot(cur, hostname=hostname)
    existing_type = (existing or {}).get("summary", {}).get("connection_type") or ""
    existing_type = existing_type.strip().lower()

    if ensure_existing_type and existing_type != ensure_existing_type.lower():
        conn.close()
        raise ValueError("device not found")
    if ensure_existing_type is None and existing_type and existing_type != connection_type.lower():
        conn.close()
        raise ValueError("device already exists with different connection type")

    created_ts = existing.get("summary", {}).get("created_at") if existing else None
    if not created_ts:
        created_ts = _now_ts()

    endpoint = address or (existing.get("summary", {}).get("connection_endpoint") if existing else "")
    if not endpoint:
        conn.close()
        raise ValueError("address is required")

    description_val = description
    if description_val is None:
        description_val = existing.get("summary", {}).get("description") if existing else ""

    os_value = os_hint or (existing.get("summary", {}).get("operating_system") if existing else "")
    os_value = os_value or ""

    device_type_label = "SSH Remote" if connection_type.lower() == "ssh" else "WinRM Remote"

    summary_payload = {
        "connection_type": connection_type.lower(),
        "connection_endpoint": endpoint,
        "internal_ip": endpoint,
        "external_ip": endpoint,
        "device_type": device_type_label,
        "operating_system": os_value,
        "last_seen": 0,
    }

    _device_upsert(
        cur,
        hostname,
        description_val,
        {"summary": summary_payload},
        created_ts,
    )
    conn.commit()
    conn.close()

    devices = _fetch_devices(hostname=hostname)
    if not devices:
        raise RuntimeError("failed to load device after upsert")
    return devices[0]


def _delete_remote_device(connection_type: str, hostname: str) -> None:
    conn = _db_conn()
    cur = conn.cursor()
    existing = _load_device_snapshot(cur, hostname=hostname)
    existing_type = (existing or {}).get("summary", {}).get("connection_type") or ""
    if (existing_type or "").strip().lower() != connection_type.lower():
        conn.close()
        raise ValueError("device not found")
    cur.execute("DELETE FROM device_sites WHERE device_hostname = ?", (hostname,))
    cur.execute(f"DELETE FROM {DEVICE_TABLE} WHERE hostname = ?", (hostname,))
    conn.commit()
    conn.close()


def _remote_devices_collection(connection_type: str):
    chk = _require_admin()
    if chk:
        return chk
    if request.method == "GET":
        try:
            devices = _fetch_devices(connection_type=connection_type)
            return jsonify({"devices": devices})
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 500

    data = request.get_json(silent=True) or {}
    hostname = _clean_device_str(data.get("hostname")) or ""
    address = _clean_device_str(
        data.get("address")
        or data.get("connection_endpoint")
        or data.get("endpoint")
        or data.get("host")
    ) or ""
    description = _clean_device_str(data.get("description")) or ""
    os_hint = _clean_device_str(data.get("operating_system") or data.get("os")) or ""
    if not hostname:
        return jsonify({"error": "hostname is required"}), 400
    if not address:
        return jsonify({"error": "address is required"}), 400

    try:
        device = _upsert_remote_device(
            connection_type,
            hostname,
            address,
            description,
            os_hint,
            ensure_existing_type=None,
        )
    except ValueError as exc:
        return jsonify({"error": str(exc)}), 409
    except Exception as exc:
        return jsonify({"error": str(exc)}), 500
    return jsonify({"device": device}), 201


def _remote_device_detail(connection_type: str, hostname: str):
    chk = _require_admin()
    if chk:
        return chk
    normalized_host = _clean_device_str(hostname) or ""
    if not normalized_host:
        return jsonify({"error": "invalid hostname"}), 400

    if request.method == "DELETE":
        try:
            _delete_remote_device(connection_type, normalized_host)
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 404
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500
        return jsonify({"status": "ok"})

    data = request.get_json(silent=True) or {}
    address = _clean_device_str(
        data.get("address")
        or data.get("connection_endpoint")
        or data.get("endpoint")
    )
    description = data.get("description")
    os_hint = _clean_device_str(data.get("operating_system") or data.get("os"))
    if address is None and description is None and os_hint is None:
        return jsonify({"error": "no fields to update"}), 400
    try:
        device = _upsert_remote_device(
            connection_type,
            normalized_host,
            address if address is not None else "",
            description,
            os_hint,
            ensure_existing_type=connection_type,
        )
    except ValueError as exc:
        return jsonify({"error": str(exc)}), 404
    except Exception as exc:
        return jsonify({"error": str(exc)}), 500
    return jsonify({"device": device})


@app.route("/api/ssh_devices", methods=["GET", "POST"])
def api_ssh_devices():
    return _remote_devices_collection("ssh")


@app.route("/api/ssh_devices/<hostname>", methods=["PUT", "DELETE"])
def api_ssh_device_detail(hostname: str):
    return _remote_device_detail("ssh", hostname)


@app.route("/api/winrm_devices", methods=["GET", "POST"])
def api_winrm_devices():
    return _remote_devices_collection("winrm")


@app.route("/api/winrm_devices/<hostname>", methods=["PUT", "DELETE"])
def api_winrm_device_detail(hostname: str):
    return _remote_device_detail("winrm", hostname)


@app.route("/api/agent_devices", methods=["GET"])
def api_agent_devices():
    chk = _require_admin()
    if chk:
        return chk
    try:
        devices = _fetch_devices(only_agents=True)
        return jsonify({"devices": devices})
    except RuntimeError as exc:
        return jsonify({"error": str(exc)}), 500


# Endpoint: /api/devices/<guid> — methods GET.

@app.route("/api/devices/<guid>", methods=["GET"])
def get_device_by_guid(guid: str):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        snapshot = _load_device_snapshot(cur, guid=guid)
        site_info = {"site_id": None, "site_name": "", "site_description": ""}
        if snapshot:
            try:
                cur.execute(
                    """
                    SELECT s.id, s.name, s.description
                    FROM device_sites ds
                    JOIN sites s ON s.id = ds.site_id
                    WHERE ds.device_hostname = ?
                    """,
                    (snapshot.get("hostname"),),
                )
                site_row = cur.fetchone()
                if site_row:
                    site_info = {
                        "site_id": site_row[0],
                        "site_name": site_row[1] or "",
                        "site_description": site_row[2] or "",
                    }
            except Exception:
                pass
        conn.close()
    except Exception as exc:
        return jsonify({"error": str(exc)}), 500

    if not snapshot:
        return jsonify({"error": "not found"}), 404

    last_seen = snapshot.get("last_seen") or 0
    status = "Offline"
    try:
        if last_seen and (time.time() - float(last_seen)) <= 300:
            status = "Online"
    except Exception:
        pass

    payload = dict(snapshot)
    payload.update(site_info)
    payload["status"] = status
    return jsonify(payload)


# =============================================================================
# Section: Device List Views
# =============================================================================
# Persisted filters/layouts for the device list interface.

def _row_to_view(row):
    return {
        "id": row[0],
        "name": row[1],
        "columns": json.loads(row[2] or "[]"),
        "filters": json.loads(row[3] or "{}"),
        "created_at": row[4],
        "updated_at": row[5],
    }


# Endpoint: /api/device_list_views — methods GET.

@app.route("/api/device_list_views", methods=["GET"])
def list_device_list_views():
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, name, columns_json, filters_json, created_at, updated_at FROM device_list_views ORDER BY name COLLATE NOCASE ASC"
        )
        rows = cur.fetchall()
        conn.close()
        return jsonify({"views": [_row_to_view(r) for r in rows]})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/device_list_views/<int:view_id> — methods GET.

@app.route("/api/device_list_views/<int:view_id>", methods=["GET"])
def get_device_list_view(view_id: int):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, name, columns_json, filters_json, created_at, updated_at FROM device_list_views WHERE id = ?",
            (view_id,),
        )
        row = cur.fetchone()
        conn.close()
        if not row:
            return jsonify({"error": "not found"}), 404
        return jsonify(_row_to_view(row))
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/device_list_views — methods POST.

@app.route("/api/device_list_views", methods=["POST"])
def create_device_list_view():
    payload = request.get_json(silent=True) or {}
    name = (payload.get("name") or "").strip()
    columns = payload.get("columns") or []
    filters = payload.get("filters") or {}

    if not name:
        return jsonify({"error": "name is required"}), 400
    if name.lower() == "default view":
        return jsonify({"error": "reserved name"}), 400
    if not isinstance(columns, list) or not all(isinstance(x, str) for x in columns):
        return jsonify({"error": "columns must be a list of strings"}), 400
    if not isinstance(filters, dict):
        return jsonify({"error": "filters must be an object"}), 400

    now = int(time.time())
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "INSERT INTO device_list_views(name, columns_json, filters_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
            (name, json.dumps(columns), json.dumps(filters), now, now),
        )
        vid = cur.lastrowid
        conn.commit()
        cur.execute(
            "SELECT id, name, columns_json, filters_json, created_at, updated_at FROM device_list_views WHERE id = ?",
            (vid,),
        )
        row = cur.fetchone()
        conn.close()
        return jsonify(_row_to_view(row)), 201
    except sqlite3.IntegrityError:
        return jsonify({"error": "name already exists"}), 409
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/device_list_views/<int:view_id> — methods PUT.

@app.route("/api/device_list_views/<int:view_id>", methods=["PUT"])
def update_device_list_view(view_id: int):
    payload = request.get_json(silent=True) or {}
    name = payload.get("name")
    columns = payload.get("columns")
    filters = payload.get("filters")
    if name is not None:
        name = (name or "").strip()
        if not name:
            return jsonify({"error": "name cannot be empty"}), 400
        if name.lower() == "default view":
            return jsonify({"error": "reserved name"}), 400
    if columns is not None:
        if not isinstance(columns, list) or not all(isinstance(x, str) for x in columns):
            return jsonify({"error": "columns must be a list of strings"}), 400
    if filters is not None and not isinstance(filters, dict):
        return jsonify({"error": "filters must be an object"}), 400

    fields = []
    params = []
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

    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(f"UPDATE device_list_views SET {', '.join(fields)} WHERE id = ?", params)
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "not found"}), 404
        conn.commit()
        cur.execute(
            "SELECT id, name, columns_json, filters_json, created_at, updated_at FROM device_list_views WHERE id = ?",
            (view_id,),
        )
        row = cur.fetchone()
        conn.close()
        return jsonify(_row_to_view(row))
    except sqlite3.IntegrityError:
        return jsonify({"error": "name already exists"}), 409
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/device_list_views/<int:view_id> — methods DELETE.

@app.route("/api/device_list_views/<int:view_id>", methods=["DELETE"])
def delete_device_list_view(view_id: int):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute("DELETE FROM device_list_views WHERE id = ?", (view_id,))
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "not found"}), 404
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


def _persist_last_seen(hostname: str, last_seen: int, agent_id: str = None):
    """Persist last_seen (and agent_id if provided) into the stored device record.

    Ensures that after a server restart, we can restore last_seen from DB even
    if the agent is offline, and helps merge entries by keeping track of the
    last known agent_id for a hostname.
    """
    if not hostname or str(hostname).strip().lower() == "unknown":
        return
    try:
        conn = _db_conn()
        cur = conn.cursor()
        snapshot = _load_device_snapshot(cur, hostname=hostname) or {}
        details = snapshot.get("details", {})
        description = snapshot.get("description") or ""
        created_at = int(snapshot.get("created_at") or 0)
        existing_hash = snapshot.get("agent_hash")

        summary = details.get("summary") or {}
        summary["hostname"] = summary.get("hostname") or hostname
        try:
            summary["last_seen"] = int(last_seen or 0)
        except Exception:
            summary["last_seen"] = int(time.time())
        if agent_id:
            try:
                summary["agent_id"] = str(agent_id)
            except Exception:
                pass
        try:
            existing_guid = (snapshot.get("agent_guid") or "").strip()
        except Exception:
            existing_guid = ""
        if existing_guid and not summary.get("agent_guid"):
            summary["agent_guid"] = _normalize_guid(existing_guid)
        details["summary"] = summary

        now = int(time.time())
        # Ensure 'created' string aligns with created_at we will store
        target_created_at = created_at or now
        try:
            from datetime import datetime, timezone
            human = datetime.fromtimestamp(target_created_at, timezone.utc).strftime('%Y-%m-%d %H:%M:%S')
            details.setdefault('summary', {})['created'] = details.get('summary', {}).get('created') or human
        except Exception:
            pass

        # Single upsert to avoid unique-constraint races
        effective_hash = summary.get("agent_hash") or existing_hash
        effective_guid = summary.get("agent_guid") or existing_guid
        _device_upsert(
            cur,
            hostname,
            description,
            details,
            target_created_at,
            agent_hash=effective_hash,
            guid=effective_guid,
        )
        conn.commit()
        conn.close()
    except Exception as e:
        print(f"[WARN] Failed to persist last_seen for {hostname}: {e}")


def _normalize_guid(value: Optional[str]) -> str:
    candidate = (value or "").strip()
    if not candidate:
        return ""
    candidate = candidate.replace("{", "").replace("}", "")
    try:
        upper = candidate.upper()
        if upper.count("-") == 4 and len(upper) == 36:
            return upper
        if len(candidate) == 32 and all(c in "0123456789abcdefABCDEF" for c in candidate):
            grouped = "-".join(
                [candidate[0:8], candidate[8:12], candidate[12:16], candidate[16:20], candidate[20:32]]
            )
            return grouped.upper()
    except Exception:
        pass
    return candidate.upper()


def load_agents_from_db():
    """Populate registered_agents with any devices stored in the database."""
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(f"SELECT {_device_column_sql()} FROM {DEVICE_TABLE}")
        rows = cur.fetchall()
        for row in rows:
            record = _row_to_device_dict(row, _DEVICE_TABLE_COLUMNS)
            snapshot = _assemble_device_snapshot(record)
            hostname = snapshot.get("hostname")
            description = snapshot.get("description")
            details = snapshot.get("details", {})
            summary = details.get("summary", {})
            agent_id = summary.get("agent_id") or hostname
            stored_hash = (snapshot.get("agent_hash") or summary.get("agent_hash") or "").strip()
            agent_guid = (summary.get("agent_guid") or snapshot.get("agent_guid") or "").strip()
            if snapshot.get("agent_guid") and not summary.get("agent_guid"):
                normalized_guid = _normalize_guid(snapshot.get("agent_guid"))
                summary["agent_guid"] = normalized_guid
                try:
                    _device_upsert(
                        cur,
                        hostname,
                        description,
                        details,
                        snapshot.get("created_at"),
                        agent_hash=snapshot.get("agent_hash") or stored_hash,
                        guid=normalized_guid,
                    )
                except Exception:
                    pass
            registered_agents[agent_id] = {
                "agent_id": agent_id,
                "hostname": summary.get("hostname") or hostname,
                "agent_operating_system": summary.get("operating_system")
                or summary.get("agent_operating_system")
                or "-",
                "device_type": summary.get("device_type") or "",
                "last_seen": summary.get("last_seen") or 0,
                "status": "Offline",
            }
            if stored_hash:
                registered_agents[agent_id]["agent_hash"] = stored_hash
            if agent_guid:
                registered_agents[agent_id]["agent_guid"] = _normalize_guid(agent_guid)
        conn.close()
    except Exception as e:
        print(f"[WARN] Failed to load agents from DB: {e}")


load_agents_from_db()


def _extract_hostname_from_agent(agent_id: str) -> Optional[str]:
    try:
        agent_id = (agent_id or "").strip()
        if not agent_id:
            return None
        lower = agent_id.lower()
        marker = "-agent"
        idx = lower.find(marker)
        if idx <= 0:
            return None
        return agent_id[:idx]
    except Exception:
        return None


def _device_rows_for_agent(cur, agent_id: str) -> List[Dict[str, Any]]:
    results: List[Dict[str, Any]] = []
    normalized_id = (agent_id or "").strip()
    if not normalized_id:
        return results
    base_host = _extract_hostname_from_agent(normalized_id)
    if not base_host:
        return results
    try:
        cur.execute(
            f"SELECT {_device_column_sql()} FROM {DEVICE_TABLE} WHERE LOWER(hostname) = ?",
            (base_host.lower(),),
        )
        rows = cur.fetchall()
    except Exception:
        return results
    for row in rows or []:
        record = _row_to_device_dict(row, _DEVICE_TABLE_COLUMNS)
        snapshot = _assemble_device_snapshot(record)
        summary = snapshot.get("summary", {})
        summary_agent = (summary.get("agent_id") or "").strip()
        matched = summary_agent.lower() == normalized_id.lower() if summary_agent else False
        results.append(
            {
                "hostname": snapshot.get("hostname"),
                "agent_hash": (snapshot.get("agent_hash") or "").strip(),
                "details": snapshot.get("details", {}),
                "summary_agent_id": summary_agent,
                "matched": matched,
                "guid": snapshot.get("agent_guid") or "",
            }
        )
    return results


def _ensure_agent_guid_for_hostname(cur, hostname: str, agent_id: Optional[str] = None) -> Optional[str]:
    normalized_host = (hostname or "").strip()
    if not normalized_host:
        return None
    snapshot = _load_device_snapshot(cur, hostname=normalized_host)
    if snapshot:
        actual_host = snapshot.get("hostname") or normalized_host
        existing_guid = snapshot.get("agent_guid") or ""
        details = snapshot.get("details", {})
        description = snapshot.get("description") or ""
        created_at = snapshot.get("created_at")
        agent_hash = snapshot.get("agent_hash")
    else:
        actual_host = normalized_host
        existing_guid = ""
        details = {}
        description = ""
        created_at = None
        agent_hash = None
    summary = details.setdefault("summary", {})
    if agent_id and not summary.get("agent_id"):
        try:
            summary["agent_id"] = str(agent_id)
        except Exception:
            summary["agent_id"] = agent_id
    if actual_host and not summary.get("hostname"):
        summary["hostname"] = actual_host

    if existing_guid:
        normalized = _normalize_guid(existing_guid)
        summary.setdefault("agent_guid", normalized)
        _device_upsert(
            cur,
            actual_host,
            description,
            details,
            created_at,
            agent_hash=agent_hash,
            guid=normalized,
        )
        return summary.get("agent_guid") or normalized

    new_guid = str(uuid.uuid4())
    summary["agent_guid"] = new_guid
    now = int(time.time())
    _device_upsert(
        cur,
        actual_host,
        description,
        details,
        created_at or now,
        agent_hash=agent_hash,
        guid=new_guid,
    )
    return new_guid


def _ensure_agent_guid(agent_id: str, hostname: Optional[str] = None) -> Optional[str]:
    agent_id = (agent_id or "").strip()
    normalized_host = (hostname or "").strip()
    conn = None
    try:
        conn = _db_conn()
        cur = conn.cursor()
        rows = _device_rows_for_agent(cur, agent_id) if agent_id else []
        for row in rows:
            candidate = _normalize_guid(row.get("guid"))
            if candidate:
                summary = row.get("details", {}).setdefault("summary", {})
                summary.setdefault("agent_guid", candidate)
                host = row.get("hostname")
                snapshot_existing = _load_device_snapshot(cur, hostname=host)
                description = snapshot_existing.get("description") if snapshot_existing else None
                created_at = snapshot_existing.get("created_at") if snapshot_existing else None
                agent_hash = snapshot_existing.get("agent_hash") if snapshot_existing else None
                _device_upsert(
                    cur,
                    host,
                    description,
                    row.get("details", {}),
                    created_at,
                    agent_hash=agent_hash,
                    guid=candidate,
                )
                conn.commit()
                return candidate

        target_host = normalized_host
        if not target_host:
            for row in rows:
                if row.get("matched"):
                    target_host = row.get("hostname")
                    break
            if not target_host and rows:
                target_host = rows[0].get("hostname")
        if not target_host and agent_id and agent_id in registered_agents:
            target_host = registered_agents[agent_id].get("hostname")
        if not target_host:
            return None

        guid = _ensure_agent_guid_for_hostname(cur, target_host, agent_id)
        conn.commit()
        return guid
    except Exception as exc:
        _write_service_log('server', f'ensure_agent_guid failure for {agent_id or hostname}: {exc}')
        return None
    finally:
        if conn:
            try:
                conn.close()
            except Exception:
                pass

# Endpoint: /api/agents — methods GET.

@app.route("/api/agents")
def get_agents():
    """Return agents with collector activity indicator."""
    now = time.time()
    # Collapse duplicates by hostname; prefer newer last_seen and non-script entries
    seen_by_hostname = {}
    for aid, info in (registered_agents or {}).items():
        d = dict(info)
        mode = _normalize_service_mode(d.get('service_mode'), aid)
        # Hide non-interactive script helper entries from the public list
        if mode != 'currentuser':
            if aid and isinstance(aid, str) and aid.lower().endswith('-script'):
                continue
            if info.get('is_script_agent'):
                continue
        d['service_mode'] = mode
        ts = d.get('collector_active_ts') or 0
        d['collector_active'] = bool(ts and (now - float(ts) < 130))
        host = (d.get('hostname') or '').strip() or 'unknown'
        bucket = seen_by_hostname.setdefault(host, {})
        cur = bucket.get(mode)
        if not cur or int(d.get('last_seen') or 0) >= int(cur[1].get('last_seen') or 0):
            bucket[mode] = (aid, d)
    out = {}
    for host, bucket in seen_by_hostname.items():
        for mode, (aid, d) in bucket.items():
            d = dict(d)
            d['hostname'] = (d.get('hostname') or '').strip() or host
            d['service_mode'] = mode
            out[aid] = d
    return jsonify(out)


"""Scheduled Jobs API moved to Data/Server/job_scheduler.py"""


## dayjs_to_ts removed; scheduling parsing now lives in job_scheduler


def _normalize_service_mode(value, agent_id=None):
    try:
        if isinstance(value, str):
            text = value.strip().lower()
        else:
            text = ''
    except Exception:
        text = ''
    if not text and agent_id:
        try:
            aid = agent_id.lower()
            if '-svc-' in aid or aid.endswith('-svc'):
                return 'system'
        except Exception:
            pass
    if text in {'system', 'svc', 'service', 'system_service'}:
        return 'system'
    if text in {'interactive', 'currentuser', 'user', 'current_user'}:
        return 'currentuser'
    return 'currentuser'


def _is_empty(v):
    return v is None or v == '' or v == [] or v == {}


def _deep_merge_preserve(prev: dict, incoming: dict) -> dict:
    out = dict(prev or {})
    for k, v in (incoming or {}).items():
        if isinstance(v, dict):
            out[k] = _deep_merge_preserve(out.get(k) or {}, v)
        elif isinstance(v, list):
            # Only replace list if incoming has content; else keep prev
            if v:
                out[k] = v
        else:
            # Keep previous non-empty values when incoming is empty
            if _is_empty(v):
                # do not overwrite
                continue
            out[k] = v
    return out


# Endpoint: /api/agent/details — methods POST.

@app.route("/api/agent/details", methods=["POST"])
@require_device_auth(DEVICE_AUTH_MANAGER)
def save_agent_details():
    data = request.get_json(silent=True) or {}
    hostname = data.get("hostname")
    details = data.get("details")
    agent_id = data.get("agent_id")
    agent_hash = data.get("agent_hash")
    if isinstance(agent_hash, str):
        agent_hash = agent_hash.strip() or None
    else:
        agent_hash = None
    ctx = getattr(g, "device_auth")
    auth_guid = _normalize_guid(ctx.guid)
    fingerprint = (ctx.ssl_key_fingerprint or "").strip()
    if not hostname and isinstance(details, dict):
        hostname = (details.get("summary") or {}).get("hostname")
    if not hostname or not isinstance(details, dict):
        return jsonify({"error": "invalid payload"}), 400
    try:
        conn = _db_conn()
        cur = conn.cursor()
        snapshot = _load_device_snapshot(cur, hostname=hostname) or {}
        try:
            prev_details = json.loads(json.dumps(snapshot.get("details", {})))
        except Exception:
            prev_details = snapshot.get("details", {}) or {}
        description = snapshot.get("description") or ""
        created_at = int(snapshot.get("created_at") or 0)
        existing_guid = (snapshot.get("agent_guid") or "").strip() or None
        existing_agent_hash = (snapshot.get("agent_hash") or "").strip() or None
        db_fp = (snapshot.get("ssl_key_fingerprint") or "").strip().lower()
        if db_fp and fingerprint and db_fp != fingerprint.lower():
            return jsonify({"error": "fingerprint_mismatch"}), 403

        normalized_existing_guid = _normalize_guid(existing_guid) if existing_guid else None
        if normalized_existing_guid and auth_guid and normalized_existing_guid != auth_guid:
            return jsonify({"error": "guid_mismatch"}), 403

        # Ensure summary exists and attach hostname/agent_id if missing
        incoming_summary = details.setdefault("summary", {})
        if agent_id and not incoming_summary.get("agent_id"):
            try:
                incoming_summary["agent_id"] = str(agent_id)
            except Exception:
                pass
        if hostname and not incoming_summary.get("hostname"):
            incoming_summary["hostname"] = hostname
        if agent_hash:
            try:
                incoming_summary["agent_hash"] = agent_hash
            except Exception:
                pass
        effective_guid = auth_guid or existing_guid
        normalized_effective_guid = auth_guid or normalized_existing_guid
        if normalized_effective_guid:
            incoming_summary["agent_guid"] = normalized_effective_guid
        if fingerprint:
            incoming_summary.setdefault("ssl_key_fingerprint", fingerprint)

        # Preserve last_seen if incoming omitted it
        if not incoming_summary.get("last_seen"):
            last_seen = None
            if agent_id and agent_id in registered_agents:
                last_seen = registered_agents[agent_id].get("last_seen")
            if last_seen is None:
                last_seen = (prev_details.get("summary") or {}).get("last_seen")
            if last_seen is not None:
                try:
                    incoming_summary["last_seen"] = int(last_seen)
                except Exception:
                    pass

        # Deep-merge incoming over previous, but do not overwrite with empties
        merged = _deep_merge_preserve(prev_details, details)

        # Preserve last_user if incoming omitted/empty
        try:
            prev_last_user = (prev_details.get('summary') or {}).get('last_user')
            cur_last_user = (merged.get('summary') or {}).get('last_user')
            if _is_empty(cur_last_user) and prev_last_user:
                merged.setdefault('summary', {})['last_user'] = prev_last_user
        except Exception:
            pass

        # Refresh server-side in-memory registry for OS and device type
        try:
            if agent_id and agent_id in registered_agents:
                rec = registered_agents[agent_id]
                os_name = (merged.get("summary") or {}).get("operating_system") or (merged.get("summary") or {}).get("agent_operating_system")
                if os_name:
                    rec["agent_operating_system"] = os_name
                dt = ((merged.get("summary") or {}).get("device_type") or "").strip()
                if dt:
                    rec["device_type"] = dt
        except Exception:
            pass

        now = int(time.time())
        # Ensure created_at is set on first insert and mirror into merged.summary.created as human string
        if created_at <= 0:
            created_at = now
        try:
            from datetime import datetime, timezone
            human = datetime.fromtimestamp(created_at, timezone.utc).strftime('%Y-%m-%d %H:%M:%S')
            merged.setdefault('summary', {})
            if not merged['summary'].get('created'):
                merged['summary']['created'] = human
        except Exception:
            pass

        # Upsert row without destroying created_at; keep previous created_at if exists
        _device_upsert(
            cur,
            hostname,
            description,
            merged,
            created_at,
            agent_hash=agent_hash or existing_agent_hash,
            guid=normalized_effective_guid,
        )
        if normalized_effective_guid and fingerprint:
            now_iso = datetime.now(timezone.utc).isoformat()
            cur.execute(
                """
                UPDATE devices
                   SET ssl_key_fingerprint = ?,
                       key_added_at = COALESCE(key_added_at, ?)
                 WHERE guid = ?
                """,
                (fingerprint, now_iso, normalized_effective_guid),
            )
            cur.execute(
                """
                INSERT OR IGNORE INTO device_keys (id, guid, ssl_key_fingerprint, added_at)
                VALUES (?, ?, ?, ?)
                """,
                (str(uuid.uuid4()), normalized_effective_guid, fingerprint, now_iso),
            )
        conn.commit()
        conn.close()

        normalized_hash = None
        try:
            normalized_hash = (agent_hash or (merged.get("summary") or {}).get("agent_hash") or "").strip()
        except Exception:
            normalized_hash = agent_hash
        if agent_id and agent_id in registered_agents:
            if normalized_hash:
                registered_agents[agent_id]["agent_hash"] = normalized_hash
            if normalized_effective_guid:
                registered_agents[agent_id]["agent_guid"] = normalized_effective_guid
        # Also update any entries keyed by hostname (duplicate agents)
        try:
            for aid, rec in registered_agents.items():
                if rec.get("hostname") == hostname and normalized_hash:
                    rec["agent_hash"] = normalized_hash
                if rec.get("hostname") == hostname and normalized_effective_guid:
                    rec["agent_guid"] = normalized_effective_guid
        except Exception:
            pass
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/device/details/<hostname> — methods GET.

@app.route("/api/device/details/<hostname>", methods=["GET"])
def get_device_details(hostname: str):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        snapshot = _load_device_snapshot(cur, hostname=hostname)
        conn.close()
        if snapshot:
            summary = snapshot.get("summary", {})
            payload = {
                "details": snapshot.get("details", {}),
                "summary": summary,
                "description": snapshot.get("description") or summary.get("description") or "",
                "created_at": snapshot.get("created_at") or 0,
                "agent_hash": snapshot.get("agent_hash") or summary.get("agent_hash") or "",
                "agent_guid": snapshot.get("agent_guid") or summary.get("agent_guid") or "",
                "memory": snapshot.get("memory", []),
                "network": snapshot.get("network", []),
                "software": snapshot.get("software", []),
                "storage": snapshot.get("storage", []),
                "cpu": snapshot.get("cpu", {}),
                "device_type": snapshot.get("device_type") or summary.get("device_type") or "",
                "domain": snapshot.get("domain") or summary.get("domain") or "",
                "external_ip": snapshot.get("external_ip") or summary.get("external_ip") or "",
                "internal_ip": snapshot.get("internal_ip") or summary.get("internal_ip") or "",
                "last_reboot": snapshot.get("last_reboot") or summary.get("last_reboot") or "",
                "last_seen": snapshot.get("last_seen") or summary.get("last_seen") or 0,
                "last_user": snapshot.get("last_user") or summary.get("last_user") or "",
                "operating_system": snapshot.get("operating_system")
                or summary.get("operating_system")
                or summary.get("agent_operating_system")
                or "",
                "uptime": snapshot.get("uptime") or summary.get("uptime") or 0,
                "agent_id": snapshot.get("agent_id") or summary.get("agent_id") or "",
            }
            return jsonify(payload)
    except Exception:
        pass
    return jsonify({})


# Endpoint: /api/device/description/<hostname> — methods POST.

@app.route("/api/device/description/<hostname>", methods=["POST"])
def set_device_description(hostname: str):
    data = request.get_json(silent=True) or {}
    description = (data.get("description") or "").strip()
    try:
        conn = _db_conn()
        cur = conn.cursor()
        snapshot = _load_device_snapshot(cur, hostname=hostname) or {}
        try:
            details = json.loads(json.dumps(snapshot.get("details", {})))
        except Exception:
            details = snapshot.get("details", {}) or {}
        created_at = snapshot.get("created_at") or int(time.time())
        agent_hash = snapshot.get("agent_hash")
        guid = snapshot.get("agent_guid")
        existing_description = snapshot.get("description") or ""
        summary = details.setdefault("summary", {})
        summary["description"] = description
        _device_upsert(
            cur,
            hostname,
            description or existing_description or "",
            details,
            created_at,
            agent_hash=agent_hash,
            guid=guid,
        )
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


    except Exception as e:
        return jsonify({"error": str(e)}), 500


# =============================================================================
# Section: Quick Jobs & Activity
# =============================================================================
# Submit ad-hoc runs and expose execution history for devices.

def _detect_script_type(fn: str) -> str:
    fn_lower = (fn or "").lower()
    if fn_lower.endswith(".json") and os.path.isfile(fn):
        try:
            obj = _safe_read_json(fn)
            if isinstance(obj, dict):
                typ = str(obj.get("type") or obj.get("script_type") or "").strip().lower()
                if typ in ("powershell", "batch", "bash", "ansible"):
                    return typ
        except Exception:
            pass
        return "powershell"
    if fn_lower.endswith(".yml"):
        return "ansible"
    if fn_lower.endswith(".ps1"):
        return "powershell"
    if fn_lower.endswith(".bat"):
        return "batch"
    if fn_lower.endswith(".sh"):
        return "bash"
    return "unknown"


def _safe_filename(rel_path: str) -> str:
    try:
        return os.path.basename(rel_path or "")
    except Exception:
        return rel_path or ""


def _env_string(value: Any) -> str:
    if isinstance(value, bool):
        return "True" if value else "False"
    if value is None:
        return ""
    return str(value)


def _canonical_env_key(name: Any) -> str:
    try:
        return re.sub(r"[^A-Za-z0-9_]", "_", str(name or "").strip()).upper()
    except Exception:
        return ""


def _expand_env_aliases(env_map: Dict[str, str], variables: List[Dict[str, Any]]) -> Dict[str, str]:
    expanded: Dict[str, str] = dict(env_map or {})
    if not isinstance(variables, list):
        return expanded
    for var in variables:
        if not isinstance(var, dict):
            continue
        name = str(var.get("name") or "").strip()
        if not name:
            continue
        canonical = _canonical_env_key(name)
        if not canonical or canonical not in expanded:
            continue
        value = expanded[canonical]
        alias = re.sub(r"[^A-Za-z0-9_]", "_", name)
        if alias and alias not in expanded:
            expanded[alias] = value
        if alias != name and re.match(r"^[A-Za-z_][A-Za-z0-9_]*$", name) and name not in expanded:
            expanded[name] = value
    return expanded


def _powershell_literal(value: Any, var_type: str) -> str:
    """Convert a variable value to a PowerShell literal for substitution."""
    typ = str(var_type or "string").lower()
    if typ == "boolean":
        if isinstance(value, bool):
            truthy = value
        elif value is None:
            truthy = False
        elif isinstance(value, (int, float)):
            truthy = value != 0
        else:
            s = str(value).strip().lower()
            if s in {"true", "1", "yes", "y", "on"}:
                truthy = True
            elif s in {"false", "0", "no", "n", "off", ""}:
                truthy = False
            else:
                truthy = bool(s)
        return "$true" if truthy else "$false"
    if typ == "number":
        if value is None or value == "":
            return "0"
        return str(value)
    # Treat credentials and any other type as strings
    s = "" if value is None else str(value)
    return "'" + s.replace("'", "''") + "'"


def _extract_variable_default(var: Dict[str, Any]) -> Any:
    for key in ("value", "default", "defaultValue", "default_value"):
        if key in var:
            val = var.get(key)
            return "" if val is None else val
    return ""


def _prepare_variable_context(doc_variables: List[Dict[str, Any]], overrides: Dict[str, Any]):
    env_map: Dict[str, str] = {}
    variables: List[Dict[str, Any]] = []
    literal_lookup: Dict[str, str] = {}
    doc_names: Dict[str, bool] = {}

    overrides = overrides or {}

    if not isinstance(doc_variables, list):
        doc_variables = []

    for var in doc_variables:
        if not isinstance(var, dict):
            continue
        name = str(var.get("name") or "").strip()
        if not name:
            continue
        doc_names[name] = True
        canonical = _canonical_env_key(name)
        var_type = str(var.get("type") or "string").lower()
        default_val = _extract_variable_default(var)
        final_val = overrides[name] if name in overrides else default_val
        if canonical:
            env_map[canonical] = _env_string(final_val)
            literal_lookup[canonical] = _powershell_literal(final_val, var_type)
        if name in overrides:
            new_var = dict(var)
            new_var["value"] = overrides[name]
            variables.append(new_var)
        else:
            variables.append(var)

    for name, val in overrides.items():
        if name in doc_names:
            continue
        canonical = _canonical_env_key(name)
        if canonical:
            env_map[canonical] = _env_string(val)
            literal_lookup[canonical] = _powershell_literal(val, "string")
        variables.append({"name": name, "value": val, "type": "string"})

    env_map = _expand_env_aliases(env_map, variables)
    return env_map, variables, literal_lookup


_ENV_VAR_PATTERN = re.compile(r"(?i)\$env:(\{)?([A-Za-z0-9_\-]+)(?(1)\})")


def _rewrite_powershell_script(content: str, literal_lookup: Dict[str, str]) -> str:
    if not content or not literal_lookup:
        return content

    def _replace(match: Any) -> str:
        name = match.group(2)
        canonical = _canonical_env_key(name)
        if not canonical:
            return match.group(0)
        literal = literal_lookup.get(canonical)
        if literal is None:
            return match.group(0)
        return literal

    return _ENV_VAR_PATTERN.sub(_replace, content)


# Endpoint: /api/scripts/quick_run — methods POST.

@app.route("/api/scripts/quick_run", methods=["POST"])
def scripts_quick_run():
    """Queue a Quick Job to agents via WebSocket and record Running status.

    Payload: { script_path: str, hostnames: [str], run_mode?: 'current_user'|'admin'|'system', admin_user?, admin_pass? }
    """
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("script_path") or "").strip()
    hostnames = data.get("hostnames") or []
    run_mode = (data.get("run_mode") or "system").strip().lower()
    admin_user = ""
    admin_pass = ""

    if not rel_path or not isinstance(hostnames, list) or not hostnames:
        return jsonify({"error": "Missing script_path or hostnames[]"}), 400

    scripts_root = _scripts_root()
    abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
    if (not abs_path.startswith(scripts_root)) or (not _is_valid_scripts_relpath(rel_path)) or (not os.path.isfile(abs_path)):
        return jsonify({"error": "Script not found"}), 404

    doc = _load_assembly_document(abs_path, "scripts")
    script_type = (doc.get("type") or "powershell").lower()
    if script_type != "powershell":
        return jsonify({"error": f"Unsupported script type '{script_type}'. Only powershell is supported for Quick Job currently."}), 400

    content = doc.get("script") or ""
    doc_variables = doc.get("variables") if isinstance(doc.get("variables"), list) else []

    overrides_raw = data.get("variable_values")
    overrides: Dict[str, Any] = {}
    if isinstance(overrides_raw, dict):
        for key, val in overrides_raw.items():
            name = str(key or "").strip()
            if not name:
                continue
            overrides[name] = val

    env_map, variables, literal_lookup = _prepare_variable_context(doc_variables, overrides)
    content = _rewrite_powershell_script(content, literal_lookup)
    encoded_content = _encode_script_content(content)
    timeout_seconds = 0
    try:
        timeout_seconds = max(0, int(doc.get("timeout_seconds") or 0))
    except Exception:
        timeout_seconds = 0
    friendly_name = (doc.get("name") or "").strip() or _safe_filename(rel_path)

    now = int(time.time())
    results = []
    for host in hostnames:
        job_id = None
        try:
            conn = _db_conn()
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                VALUES(?,?,?,?,?,?,?,?)
                """,
                (
                    host,
                    rel_path.replace(os.sep, "/"),
                    friendly_name,
                    script_type,
                    now,
                    "Running",
                    "",
                    "",
                ),
            )
            job_id = cur.lastrowid
            conn.commit()
            conn.close()
        except Exception as db_err:
            return jsonify({"error": f"DB insert failed: {db_err}"}), 500

        payload = {
            "job_id": job_id,
            "target_hostname": host,
            "script_type": script_type,
            "script_name": friendly_name,
            "script_path": rel_path.replace(os.sep, "/"),
            "script_content": encoded_content,
            "script_encoding": "base64",
            "environment": env_map,
            "variables": variables,
            "timeout_seconds": timeout_seconds,
            "files": doc.get("files") if isinstance(doc.get("files"), list) else [],
            "run_mode": run_mode,
            "admin_user": admin_user,
            "admin_pass": admin_pass,
        }
        # Broadcast to all connected clients; no broadcast kw in python-socketio v5
        socketio.emit("quick_job_run", payload)
        try:
            socketio.emit("device_activity_changed", {
                "hostname": str(host),
                "activity_id": job_id,
                "change": "created",
                "source": "quick_job",
            })
        except Exception:
            pass
        results.append({"hostname": host, "job_id": job_id, "status": "Running"})

    return jsonify({"results": results})


# Endpoint: /api/ansible/quick_run — methods POST.

@app.route("/api/ansible/quick_run", methods=["POST"])
def ansible_quick_run():
    """Queue an Ansible Playbook Quick Job via WebSocket to targeted agents.

    Payload: { playbook_path: str, hostnames: [str] }
    The playbook_path is relative to the Ansible island (e.g., "folder/play.yml").
    """
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("playbook_path") or "").strip()
    hostnames = data.get("hostnames") or []
    credential_id = data.get("credential_id")
    use_service_account_raw = data.get("use_service_account")
    if not rel_path or not isinstance(hostnames, list) or not hostnames:
        _ansible_log_server(f"[quick_run] invalid payload rel_path='{rel_path}' hostnames={hostnames}")
        return jsonify({"error": "Missing playbook_path or hostnames[]"}), 400
    server_mode = False
    cred_id_int = None
    credential_detail: Optional[Dict[str, Any]] = None
    overrides_raw = data.get("variable_values")
    variable_values: Dict[str, Any] = {}
    if isinstance(overrides_raw, dict):
        for key, val in overrides_raw.items():
            name = str(key or "").strip()
            if not name:
                continue
            variable_values[name] = val
    if credential_id not in (None, "", "null"):
        try:
            cred_id_int = int(credential_id)
            if cred_id_int <= 0:
                cred_id_int = None
        except Exception:
            return jsonify({"error": "Invalid credential_id"}), 400
    if use_service_account_raw is None:
        use_service_account = cred_id_int is None
    else:
        use_service_account = bool(use_service_account_raw)
    if use_service_account:
        cred_id_int = None
        credential_detail = None
    if cred_id_int:
        credential_detail = _fetch_credential_with_secrets(cred_id_int)
        if not credential_detail:
            return jsonify({"error": "Credential not found"}), 404
        conn_type = (credential_detail.get("connection_type") or "ssh").lower()
        if conn_type in ("ssh", "linux", "unix"):
            server_mode = True
        elif conn_type in ("winrm", "psrp"):
            variable_values = _inject_winrm_credential(variable_values, credential_detail)
        else:
            return jsonify({"error": f"Credential connection '{conn_type}' not supported"}), 400
    try:
        root, abs_path, _ = _resolve_assembly_path('ansible', rel_path)
        if not os.path.isfile(abs_path):
            _ansible_log_server(f"[quick_run] playbook not found path={abs_path}")
            return jsonify({"error": "Playbook not found"}), 404
        doc = _load_assembly_document(abs_path, 'ansible')
        content = doc.get('script') or ''
        encoded_content = _encode_script_content(content)
        variables = doc.get('variables') if isinstance(doc.get('variables'), list) else []
        files = doc.get('files') if isinstance(doc.get('files'), list) else []
        friendly_name = (doc.get("name") or "").strip() or os.path.basename(abs_path)
        if server_mode and not cred_id_int:
            return jsonify({"error": "credential_id is required for server-side execution"}), 400

        results = []
        for host in hostnames:
            # Create activity_history row so UI shows running state and can receive recap mirror
            job_id = None
            try:
                conn2 = _db_conn()
                cur2 = conn2.cursor()
                now_ts = int(time.time())
                cur2.execute(
                    """
                    INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                    VALUES(?,?,?,?,?,?,?,?)
                    """,
                    (
                        str(host),
                        rel_path.replace(os.sep, "/"),
                        friendly_name,
                        "ansible",
                        now_ts,
                        "Running",
                        "",
                        "",
                    ),
                )
                job_id = cur2.lastrowid
                conn2.commit()
                conn2.close()
            except Exception:
                job_id = None

            try:
                import uuid as _uuid
                run_id = _uuid.uuid4().hex
            except Exception:
                run_id = str(int(time.time() * 1000))
            payload = {
                "run_id": run_id,
                "target_hostname": str(host),
                "playbook_name": friendly_name,
                "playbook_content": encoded_content,
                "playbook_encoding": "base64",
                "connection": "winrm",
                "variables": variables,
                "files": files,
                "activity_job_id": job_id,
                "variable_values": variable_values,
            }
            try:
                if server_mode and cred_id_int:
                    run_id = _queue_server_ansible_run(
                        hostname=str(host),
                        playbook_abs_path=abs_path,
                        playbook_rel_path=rel_path.replace(os.sep, "/"),
                        playbook_name=friendly_name,
                        credential_id=cred_id_int,
                        variable_values=variable_values,
                        source="quick_job",
                        activity_id=job_id,
                    )
                    if job_id:
                        socketio.emit("device_activity_changed", {
                            "hostname": str(host),
                            "activity_id": job_id,
                            "change": "created",
                            "source": "ansible",
                        })
                    results.append({"hostname": host, "run_id": run_id, "status": "Queued", "activity_job_id": job_id, "execution": "server"})
                else:
                    _ansible_log_server(f"[quick_run] emit ansible_playbook_run host='{host}' run_id={run_id} job_id={job_id} path={rel_path}")
                    socketio.emit("ansible_playbook_run", payload)
                    if job_id:
                        socketio.emit("device_activity_changed", {
                            "hostname": str(host),
                            "activity_id": job_id,
                            "change": "created",
                            "source": "ansible",
                        })
                    results.append({"hostname": host, "run_id": run_id, "status": "Queued", "activity_job_id": job_id, "execution": "agent"})
            except Exception as ex:
                _ansible_log_server(f"[quick_run] emit failed host='{host}' run_id={run_id} err={ex}")
                if job_id:
                    try:
                        conn_fail = _db_conn()
                        cur_fail = conn_fail.cursor()
                        cur_fail.execute(
                            "UPDATE activity_history SET status='Failed', stderr=?, ran_at=? WHERE id=?",
                            (str(ex), int(time.time()), job_id),
                        )
                        conn_fail.commit()
                        conn_fail.close()
                    except Exception:
                        pass
                results.append({"hostname": host, "run_id": run_id, "status": "Failed", "activity_job_id": job_id, "error": str(ex)})
        if credential_detail is not None:
            # Remove decrypted secrets from scope as soon as possible
            credential_detail.clear()
        return jsonify({"results": results})
    except ValueError as ve:
        return jsonify({"error": str(ve)}), 400
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/device/activity/<hostname> — methods GET, DELETE.

@app.route("/api/device/activity/<hostname>", methods=["GET", "DELETE"])
def device_activity(hostname: str):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        if request.method == "DELETE":
            cur.execute("DELETE FROM activity_history WHERE hostname = ?", (hostname,))
            conn.commit()
            conn.close()
            return jsonify({"status": "ok"})

        cur.execute(
            "SELECT id, script_name, script_path, script_type, ran_at, status, LENGTH(stdout), LENGTH(stderr) FROM activity_history WHERE hostname = ? ORDER BY ran_at DESC, id DESC",
            (hostname,),
        )
        rows = cur.fetchall()
        conn.close()
        out = []
        for (jid, name, path, stype, ran_at, status, so_len, se_len) in rows:
            out.append({
                "id": jid,
                "script_name": name,
                "script_path": path,
                "script_type": stype,
                "ran_at": ran_at,
                "status": status,
                "has_stdout": bool(so_len or 0),
                "has_stderr": bool(se_len or 0),
            })
        return jsonify({"history": out})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/device/activity/job/<int:job_id> — methods GET.

@app.route("/api/device/activity/job/<int:job_id>", methods=["GET"])
def device_activity_job(job_id: int):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, hostname, script_name, script_path, script_type, ran_at, status, stdout, stderr FROM activity_history WHERE id = ?",
            (job_id,),
        )
        row = cur.fetchone()
        conn.close()
        if not row:
            return jsonify({"error": "Not found"}), 404
        (jid, hostname, name, path, stype, ran_at, status, stdout, stderr) = row
        return jsonify({
            "id": jid,
            "hostname": hostname,
            "script_name": name,
            "script_path": path,
            "script_type": stype,
            "ran_at": ran_at,
            "status": status,
            "stdout": stdout or "",
            "stderr": stderr or "",
        })
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@socketio.on("quick_job_result")
def handle_quick_job_result(data):
    """Agent reports back stdout/stderr/status for a job."""
    try:
        job_id = int(data.get("job_id"))
    except Exception:
        return
    status = (data.get("status") or "").strip() or "Failed"
    stdout = data.get("stdout") or ""
    stderr = data.get("stderr") or ""
    broadcast_payload = None
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "UPDATE activity_history SET status=?, stdout=?, stderr=? WHERE id=?",
            (status, stdout, stderr, job_id),
        )
        conn.commit()
        try:
            cur.execute(
                "SELECT run_id FROM scheduled_job_run_activity WHERE activity_id=?",
                (job_id,),
            )
            link = cur.fetchone()
            if link:
                run_id = int(link[0])
                ts_now = _now_ts()
                if status.lower() == "running":
                    cur.execute(
                        "UPDATE scheduled_job_runs SET status='Running', updated_at=? WHERE id=?",
                        (ts_now, run_id),
                    )
                else:
                    cur.execute(
                        "UPDATE scheduled_job_runs SET status=?, finished_ts=COALESCE(finished_ts, ?), updated_at=? WHERE id=?",
                        (status, ts_now, ts_now, run_id),
                    )
                conn.commit()
        except Exception:
            pass
        try:
            cur.execute(
                "SELECT id, hostname, status FROM activity_history WHERE id=?",
                (job_id,),
            )
            row = cur.fetchone()
            if row and (row[1] or "").strip():
                broadcast_payload = {
                    "activity_id": row[0],
                    "hostname": row[1],
                    "status": row[2] or status,
                    "change": "updated",
                    "source": "quick_job",
                }
        except Exception:
            pass
        conn.close()
    except Exception as e:
        print(f"[ERROR] quick_job_result DB update failed for job {job_id}: {e}")
    if broadcast_payload:
        try:
            socketio.emit("device_activity_changed", broadcast_payload)
        except Exception:
            pass


# =============================================================================
# Section: Ansible Runtime Reporting
# =============================================================================
# Collect and return Ansible recap payloads emitted by agents.

def _json_dump_safe(obj) -> str:
    try:
        if isinstance(obj, str):
            # Accept pre-serialized JSON strings as-is
            json.loads(obj)
            return obj
        return json.dumps(obj or {})
    except Exception:
        return json.dumps({})


# =============================================================================
# Section: Service Account Rotation
# =============================================================================
# Manage local service account secrets for SYSTEM PowerShell runs.

def _now_iso_utc() -> str:
    try:
        return datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')
    except Exception:
        return datetime.utcnow().isoformat() + 'Z'


def _gen_strong_password(length: int = 24) -> str:
    import secrets, string as _s
    length = max(12, int(length or 24))
    # ensure at least one from each class
    classes = [
        _s.ascii_lowercase,
        _s.ascii_uppercase,
        _s.digits,
        '!@#$%^&*()-_=+[]{}<>.?',
    ]
    chars = ''.join(classes)
    pw = [secrets.choice(c) for c in classes]
    pw += [secrets.choice(chars) for _ in range(length - len(pw))]
    secrets.SystemRandom().shuffle(pw)
    return ''.join(pw)


def _service_acct_get(conn, agent_id: str):
    cur = conn.cursor()
    cur.execute(
        "SELECT agent_id, username, password_encrypted, last_rotated_utc, version FROM agent_service_account WHERE agent_id=?",
        (agent_id,)
    )
    return cur.fetchone()


def _service_acct_set(conn, agent_id: str, username: str, plaintext_password: str):
    username = (username or '').strip()
    if not username or username in LEGACY_SERVICE_ACCOUNTS:
        username = DEFAULT_SERVICE_ACCOUNT
    enc = _encrypt_secret(plaintext_password)
    now_utc = _now_iso_utc()
    cur = conn.cursor()
    cur.execute(
        """
        INSERT INTO agent_service_account(agent_id, username, password_hash, password_encrypted, last_rotated_utc, version)
        VALUES(?,?,?,?,?,1)
        ON CONFLICT(agent_id) DO UPDATE SET
            username=excluded.username,
            password_hash=excluded.password_hash,
            password_encrypted=excluded.password_encrypted,
            last_rotated_utc=excluded.last_rotated_utc
        """,
        (agent_id, username, None, enc, now_utc)
    )
    conn.commit()
    return {
        'username': username,
        'password': plaintext_password,
        'last_rotated_utc': now_utc,
    }



# Endpoint: /api/agent/checkin — methods POST.

@app.route('/api/agent/checkin', methods=['POST'])
@require_device_auth(DEVICE_AUTH_MANAGER)
def api_agent_checkin():
    payload = request.get_json(silent=True) or {}
    agent_id = (payload.get('agent_id') or '').strip()
    if not agent_id:
        return jsonify({'error': 'agent_id required'}), 400

    ctx = getattr(g, "device_auth")
    auth_guid = _normalize_guid(ctx.guid)
    fingerprint = (ctx.ssl_key_fingerprint or "").strip()
    raw_username = (payload.get('username') or '').strip()
    username = raw_username or DEFAULT_SERVICE_ACCOUNT
    if username in LEGACY_SERVICE_ACCOUNTS:
        username = DEFAULT_SERVICE_ACCOUNT
    hostname = (payload.get('hostname') or '').strip()

    reg = registered_agents.get(agent_id) or {}
    reg_guid = _normalize_guid(reg.get("agent_guid") or "")
    if reg_guid and auth_guid and reg_guid != auth_guid:
        return jsonify({'error': 'guid_mismatch'}), 403

    conn = None
    try:
        conn = _db_conn()
        row = _service_acct_get(conn, agent_id)
        if not row:
            pw = _gen_strong_password()
            out = _service_acct_set(conn, agent_id, username, pw)
            _ansible_log_server(f"[checkin] created creds agent_id={agent_id} user={out['username']} rotated={out['last_rotated_utc']}")
        else:
            stored_username = (row[1] or '').strip()
            try:
                plain = _decrypt_secret(row[2])
            except Exception:
                plain = ''
            if stored_username in LEGACY_SERVICE_ACCOUNTS:
                if not plain:
                    plain = _gen_strong_password()
                out = _service_acct_set(conn, agent_id, DEFAULT_SERVICE_ACCOUNT, plain)
                _ansible_log_server(f"[checkin] upgraded legacy service user for agent_id={agent_id} -> {out['username']}")
            elif not plain:
                plain = _gen_strong_password()
                out = _service_acct_set(conn, agent_id, stored_username or username, plain)
            else:
                eff_user = stored_username or username
                if eff_user in LEGACY_SERVICE_ACCOUNTS:
                    eff_user = DEFAULT_SERVICE_ACCOUNT
                out = {
                    'username': eff_user,
                    'password': plain,
                    'last_rotated_utc': row[3] or _now_iso_utc(),
                }

        now_ts = int(time.time())
        try:
            if hostname:
                _persist_last_seen(hostname, now_ts, agent_id)
        except Exception:
            pass

        try:
            cur = conn.cursor()
            if auth_guid:
                cur.execute(
                    """
                    UPDATE devices
                       SET agent_id = COALESCE(?, agent_id),
                           ssl_key_fingerprint = COALESCE(?, ssl_key_fingerprint),
                           last_seen = ?
                     WHERE guid = ?
                    """,
                    (agent_id or None, fingerprint or None, now_ts, auth_guid),
                )
                if cur.rowcount == 0 and hostname:
                    cur.execute(
                        """
                        UPDATE devices
                           SET guid = ?,
                               agent_id = COALESCE(?, agent_id),
                               ssl_key_fingerprint = COALESCE(?, ssl_key_fingerprint),
                               last_seen = ?
                         WHERE hostname = ?
                        """,
                        (auth_guid, agent_id or None, fingerprint or None, now_ts, hostname),
                    )
                if fingerprint:
                    cur.execute(
                        """
                        INSERT OR IGNORE INTO device_keys (id, guid, ssl_key_fingerprint, added_at)
                        VALUES (?, ?, ?, ?)
                        """,
                        (str(uuid.uuid4()), auth_guid, fingerprint, datetime.now(timezone.utc).isoformat()),
                    )
            conn.commit()
        except Exception as exc:
            _write_service_log("server", f"device update during checkin failed: {exc}")

        registered = registered_agents.setdefault(agent_id, {})
        if auth_guid:
            registered["agent_guid"] = auth_guid

        _ansible_log_server(f"[checkin] return creds agent_id={agent_id} user={out['username']}")
        return jsonify(
            {
                'username': out['username'],
                'password': out['password'],
                'policy': {'force_rotation_minutes': 43200},
                'agent_guid': auth_guid or None,
            }
        )
    except Exception as e:
        _ansible_log_server(f"[checkin] error agent_id={agent_id} err={e}")
        return jsonify({'error': str(e)}), 500
    finally:
        if conn:
            try:
                conn.close()
            except Exception:
                pass


# Endpoint: /api/agent/service-account/rotate — methods POST.

@app.route('/api/agent/service-account/rotate', methods=['POST'])
@require_device_auth(DEVICE_AUTH_MANAGER)
def api_agent_service_account_rotate():
    payload = request.get_json(silent=True) or {}
    agent_id = (payload.get('agent_id') or '').strip()
    if not agent_id:
        return jsonify({'error': 'agent_id required'}), 400

    ctx = getattr(g, "device_auth")
    auth_guid = _normalize_guid(ctx.guid)
    reg = registered_agents.get(agent_id) or {}
    reg_guid = _normalize_guid(reg.get("agent_guid") or "")
    if reg_guid and auth_guid and reg_guid != auth_guid:
        return jsonify({'error': 'guid_mismatch'}), 403

    requested_username = (payload.get('username') or '').strip()
    try:
        conn = _db_conn()
        row = _service_acct_get(conn, agent_id)
        stored_username = ''
        if row:
            stored_username = (row[1] or '').strip()
        user_eff = requested_username or stored_username or DEFAULT_SERVICE_ACCOUNT
        if user_eff in LEGACY_SERVICE_ACCOUNTS:
            user_eff = DEFAULT_SERVICE_ACCOUNT
            _ansible_log_server(f"[rotate] upgrading legacy service user for agent_id={agent_id}")
        pw_new = _gen_strong_password()
        out = _service_acct_set(conn, agent_id, user_eff, pw_new)
        try:
            registered = registered_agents.setdefault(agent_id, {})
            if auth_guid:
                registered["agent_guid"] = auth_guid
        finally:
            conn.close()
        _ansible_log_server(f"[rotate] rotated agent_id={agent_id} user={out['username']} at={out['last_rotated_utc']}")
        return jsonify({
            'username': out['username'],
            'password': out['password'],
            'policy': { 'force_rotation_minutes': 43200 }
        })
    except Exception as e:
        _ansible_log_server(f"[rotate] error agent_id={agent_id} err={e}")
        return jsonify({'error': str(e)}), 500

# Endpoint: /api/ansible/recap/report — methods POST.

@app.route("/api/ansible/recap/report", methods=["POST"])
def api_ansible_recap_report():
    """Create or update an Ansible recap row for a running/finished playbook.

    Expects JSON body with fields:
      run_id: str (required) – unique id for this playbook run (uuid recommended)
      hostname: str (optional)
      agent_id: str (optional)
      playbook_path: str (optional)
      playbook_name: str (optional)
      scheduled_job_id: int (optional)
      scheduled_run_id: int (optional)
      activity_job_id: int (optional)
      status: str (Running|Success|Failed|Cancelled) (optional)
      recap_text: str (optional)
      recap_json: object or str (optional)
      started_ts: int (optional)
      finished_ts: int (optional)
    """
    data = request.get_json(silent=True) or {}
    run_id = (data.get("run_id") or "").strip()
    if not run_id:
        return jsonify({"error": "run_id is required"}), 400

    now = _now_ts()
    hostname = (data.get("hostname") or "").strip()
    agent_id = (data.get("agent_id") or "").strip()
    playbook_path = (data.get("playbook_path") or "").strip()
    playbook_name = (data.get("playbook_name") or "").strip() or (os.path.basename(playbook_path) if playbook_path else "")
    status = (data.get("status") or "").strip()
    recap_text = data.get("recap_text")
    recap_json = data.get("recap_json")

    # IDs to correlate with other subsystems (optional)
    try:
        scheduled_job_id = int(data.get("scheduled_job_id")) if data.get("scheduled_job_id") is not None else None
    except Exception:
        scheduled_job_id = None
    try:
        scheduled_run_id = int(data.get("scheduled_run_id")) if data.get("scheduled_run_id") is not None else None
    except Exception:
        scheduled_run_id = None
    try:
        activity_job_id = int(data.get("activity_job_id")) if data.get("activity_job_id") is not None else None
    except Exception:
        activity_job_id = None

    try:
        started_ts = int(data.get("started_ts")) if data.get("started_ts") is not None else None
    except Exception:
        started_ts = None
    try:
        finished_ts = int(data.get("finished_ts")) if data.get("finished_ts") is not None else None
    except Exception:
        finished_ts = None

    recap_json_str = _json_dump_safe(recap_json) if recap_json is not None else None

    try:
        conn = _db_conn()
        cur = conn.cursor()

        # Attempt update by run_id first
        cur.execute(
            "SELECT id FROM ansible_play_recaps WHERE run_id = ?",
            (run_id,)
        )
        row = cur.fetchone()
        if row:
            recap_id = int(row[0])
            cur.execute(
                """
                UPDATE ansible_play_recaps
                SET hostname = COALESCE(?, hostname),
                    agent_id = COALESCE(?, agent_id),
                    playbook_path = COALESCE(?, playbook_path),
                    playbook_name = COALESCE(?, playbook_name),
                    scheduled_job_id = COALESCE(?, scheduled_job_id),
                    scheduled_run_id = COALESCE(?, scheduled_run_id),
                    activity_job_id = COALESCE(?, activity_job_id),
                    status = COALESCE(?, status),
                    recap_text = CASE WHEN ? IS NOT NULL THEN ? ELSE recap_text END,
                    recap_json = CASE WHEN ? IS NOT NULL THEN ? ELSE recap_json END,
                    started_ts = COALESCE(?, started_ts),
                    finished_ts = COALESCE(?, finished_ts),
                    updated_at = ?
                WHERE run_id = ?
                """,
                (
                    hostname or None,
                    agent_id or None,
                    playbook_path or None,
                    playbook_name or None,
                    scheduled_job_id,
                    scheduled_run_id,
                    activity_job_id,
                    status or None,
                    recap_text, recap_text,
                    recap_json_str, recap_json_str,
                    started_ts,
                    finished_ts,
                    now,
                    run_id,
                )
            )
            conn.commit()
        else:
            cur.execute(
                """
                INSERT INTO ansible_play_recaps (
                    run_id, hostname, agent_id, playbook_path, playbook_name,
                    scheduled_job_id, scheduled_run_id, activity_job_id,
                    status, recap_text, recap_json, started_ts, finished_ts,
                    created_at, updated_at
                ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
                """,
                (
                    run_id,
                    hostname or None,
                    agent_id or None,
                    playbook_path or None,
                    playbook_name or None,
                    scheduled_job_id,
                    scheduled_run_id,
                    activity_job_id,
                    status or None,
                    recap_text if recap_text is not None else None,
                    recap_json_str,
                    started_ts,
                    finished_ts,
                    now,
                    now,
                )
            )
            recap_id = cur.lastrowid
            conn.commit()

        # If linked to an activity_history row, mirror status/stdout for Activity tab UX
        try:
            if activity_job_id:
                cur.execute(
                    "UPDATE activity_history SET status = COALESCE(?, status), stdout = CASE WHEN ? IS NOT NULL THEN ? ELSE stdout END WHERE id = ?",
                    (status or None, recap_text, recap_text, activity_job_id)
                )
                conn.commit()
        except Exception:
            pass

        # Reflect into scheduled_job_runs if linked
        try:
            if scheduled_job_id and scheduled_run_id:
                st = (status or '').strip()
                ts_now = now
                # If Running, update status/started_ts if needed; otherwise mark finished + status
                if st.lower() == 'running':
                    cur.execute(
                        "UPDATE scheduled_job_runs SET status='Running', updated_at=?, started_ts=COALESCE(started_ts, ?) WHERE id=? AND job_id=?",
                        (ts_now, started_ts or ts_now, int(scheduled_run_id), int(scheduled_job_id))
                    )
                else:
                    cur.execute(
                        "UPDATE scheduled_job_runs SET status=?, finished_ts=COALESCE(?, finished_ts, ?), updated_at=? WHERE id=? AND job_id=?",
                        (st or 'Success', finished_ts, ts_now, ts_now, int(scheduled_run_id), int(scheduled_job_id))
                    )
                conn.commit()
        except Exception:
            pass

        # Return the latest row
        cur.execute(
            "SELECT id, run_id, hostname, agent_id, playbook_path, playbook_name, scheduled_job_id, scheduled_run_id, activity_job_id, status, recap_text, recap_json, started_ts, finished_ts, created_at, updated_at FROM ansible_play_recaps WHERE id=?",
            (recap_id,)
        )
        row = cur.fetchone()
        conn.close()

        # Broadcast to connected clients for live updates
        try:
            payload = {
                "id": row[0],
                "run_id": row[1],
                "hostname": row[2] or "",
                "agent_id": row[3] or "",
                "playbook_path": row[4] or "",
                "playbook_name": row[5] or "",
                "scheduled_job_id": row[6],
                "scheduled_run_id": row[7],
                "activity_job_id": row[8],
                "status": row[9] or "",
                "recap_text": row[10] or "",
                "recap_json": json.loads(row[11]) if (row[11] or "").strip() else None,
                "started_ts": row[12],
                "finished_ts": row[13],
                "created_at": row[14],
                "updated_at": row[15],
            }
            socketio.emit("ansible_recap_update", payload)
            if payload.get("activity_job_id"):
                socketio.emit("device_activity_changed", {
                    "hostname": payload.get("hostname") or "",
                    "activity_id": payload.get("activity_job_id"),
                    "status": payload.get("status") or "",
                    "change": "updated",
                    "source": "ansible",
                })
        except Exception:
            pass

        return jsonify({
            "id": row[0],
            "run_id": row[1],
            "hostname": row[2] or "",
            "agent_id": row[3] or "",
            "playbook_path": row[4] or "",
            "playbook_name": row[5] or "",
            "scheduled_job_id": row[6],
            "scheduled_run_id": row[7],
            "activity_job_id": row[8],
            "status": row[9] or "",
            "recap_text": row[10] or "",
            "recap_json": json.loads(row[11]) if (row[11] or "").strip() else None,
            "started_ts": row[12],
            "finished_ts": row[13],
            "created_at": row[14],
            "updated_at": row[15],
        })
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/ansible/recaps — methods GET.

@app.route("/api/ansible/recaps", methods=["GET"])
def api_ansible_recaps_list():
    """List Ansible play recaps. Optional query params: hostname, limit (default 50)"""
    hostname = (request.args.get("hostname") or "").strip()
    try:
        limit = int(request.args.get("limit") or 50)
    except Exception:
        limit = 50
    try:
        conn = _db_conn()
        cur = conn.cursor()
        if hostname:
            cur.execute(
                """
                SELECT id, run_id, hostname, playbook_name, status, created_at, updated_at, started_ts, finished_ts
                FROM ansible_play_recaps
                WHERE hostname = ?
                ORDER BY COALESCE(updated_at, created_at) DESC, id DESC
                LIMIT ?
                """,
                (hostname, limit)
            )
        else:
            cur.execute(
                """
                SELECT id, run_id, hostname, playbook_name, status, created_at, updated_at, started_ts, finished_ts
                FROM ansible_play_recaps
                ORDER BY COALESCE(updated_at, created_at) DESC, id DESC
                LIMIT ?
                """,
                (limit,)
            )
        rows = cur.fetchall()
        conn.close()
        out = []
        for r in rows:
            out.append({
                "id": r[0],
                "run_id": r[1],
                "hostname": r[2] or "",
                "playbook_name": r[3] or "",
                "status": r[4] or "",
                "created_at": r[5],
                "updated_at": r[6],
                "started_ts": r[7],
                "finished_ts": r[8],
            })
        return jsonify({"recaps": out})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/ansible/recap/<int:recap_id> — methods GET.

@app.route("/api/ansible/recap/<int:recap_id>", methods=["GET"])
def api_ansible_recap_get(recap_id: int):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, run_id, hostname, agent_id, playbook_path, playbook_name, scheduled_job_id, scheduled_run_id, activity_job_id, status, recap_text, recap_json, started_ts, finished_ts, created_at, updated_at FROM ansible_play_recaps WHERE id=?",
            (recap_id,)
        )
        row = cur.fetchone()
        conn.close()
        if not row:
            return jsonify({"error": "Not found"}), 404
        return jsonify({
            "id": row[0],
            "run_id": row[1],
            "hostname": row[2] or "",
            "agent_id": row[3] or "",
            "playbook_path": row[4] or "",
            "playbook_name": row[5] or "",
            "scheduled_job_id": row[6],
            "scheduled_run_id": row[7],
            "activity_job_id": row[8],
            "status": row[9] or "",
            "recap_text": row[10] or "",
            "recap_json": json.loads(row[11]) if (row[11] or "").strip() else None,
            "started_ts": row[12],
            "finished_ts": row[13],
            "created_at": row[14],
            "updated_at": row[15],
        })
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Endpoint: /api/ansible/run_for_activity/<int:activity_id> — methods GET.

@app.route("/api/ansible/run_for_activity/<int:activity_id>", methods=["GET"])
def api_ansible_run_for_activity(activity_id: int):
    """Return the latest run_id/status for a recap row linked to an activity_history id."""
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            """
            SELECT run_id, status
            FROM ansible_play_recaps
            WHERE activity_job_id = ?
            ORDER BY COALESCE(updated_at, created_at) DESC, id DESC
            LIMIT 1
            """,
            (activity_id,)
        )
        row = cur.fetchone()
        conn.close()
        if not row:
            return jsonify({"error": "Not found"}), 404
        return jsonify({"run_id": row[0], "status": row[1] or ""})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@socketio.on("collector_status")
def handle_collector_status(data):
    """Collector agent reports activity and optional last_user.

    To avoid flapping of summary.last_user between the SYSTEM service and the
    interactive user helper, we only accept last_user updates that look like a
    real interactive user and, by preference, only from the interactive agent
    (agent_id ending with "-script"). Machine accounts (..$) and built-in
    service principals (SYSTEM/LOCAL SERVICE/NETWORK SERVICE) are ignored.
    """
    agent_id = (data or {}).get('agent_id')
    hostname = (data or {}).get('hostname')
    active = bool((data or {}).get('active'))
    last_user = (data or {}).get('last_user')
    if not agent_id:
        return

    mode = _normalize_service_mode((data or {}).get('service_mode'), agent_id)
    rec = registered_agents.setdefault(agent_id, {})
    rec['agent_id'] = agent_id
    if hostname:
        rec['hostname'] = hostname
    if active:
        rec['collector_active_ts'] = time.time()
    if mode:
        rec['service_mode'] = mode

    # Helper: decide if a reported user string is a real interactive user
    def _is_valid_interactive_user(s: str) -> bool:
        try:
            if not s:
                return False
            t = str(s).strip()
            if not t:
                return False
            # Reject machine accounts and well-known service identities
            upper = t.upper()
            if t.endswith('$'):
                return False
            if any(x in upper for x in ('NT AUTHORITY\\', 'NT SERVICE\\')):
                return False
            if upper.endswith('\\SYSTEM') or upper.endswith('\\LOCAL SERVICE') or upper.endswith('\\NETWORK SERVICE') or upper == 'ANONYMOUS LOGON':
                return False
            # Looks acceptable (DOMAIN\\user or user)
            return True
        except Exception:
            return False

    # Prefer interactive/script agent as the source of truth for last_user
    is_script_agent = False
    try:
        is_script_agent = bool((isinstance(agent_id, str) and agent_id.lower().endswith('-script')) or rec.get('is_script_agent'))
    except Exception:
        is_script_agent = False

    # If we have a usable last_user and a hostname, persist it
    if last_user and _is_valid_interactive_user(last_user) and (hostname or rec.get('hostname')):
        # If this event is coming from the SYSTEM service agent, ignore it to
        # prevent clobbering the interactive user's value.
        try:
            if isinstance(agent_id, str) and ('-svc-' in agent_id.lower() or agent_id.lower().endswith('-svc')) and not is_script_agent:
                return
        except Exception:
            pass
        try:
            host = hostname or rec.get('hostname')
            conn = _db_conn()
            cur = conn.cursor()
            snapshot = _load_device_snapshot(cur, hostname=host) or {}
            try:
                details = json.loads(json.dumps(snapshot.get("details", {})))
            except Exception:
                details = snapshot.get("details", {}) or {}
            description = snapshot.get("description") or ""
            created_at = int(snapshot.get("created_at") or 0)
            existing_hash = (snapshot.get("agent_hash") or "").strip() or None
            existing_guid = (snapshot.get("agent_guid") or "").strip() or None
            summary = details.get('summary') or {}
            # Only update last_user if provided; do not clear other fields
            summary['last_user'] = last_user
            details['summary'] = summary
            now = int(time.time())
            _device_upsert(
                cur,
                host,
                description,
                details,
                created_at or now,
                agent_hash=existing_hash,
                guid=existing_guid,
            )
            conn.commit()
            conn.close()
        except Exception:
            pass


# Endpoint: /api/agent/<agent_id> — methods DELETE.

@app.route("/api/agent/<agent_id>", methods=["DELETE"])
def delete_agent(agent_id: str):
    """Remove an agent from the registry and database."""
    info = registered_agents.pop(agent_id, None)
    agent_configurations.pop(agent_id, None)
    # IMPORTANT: Do NOT delete devices here. Multiple in-memory agent
    # records can refer to the same hostname; removing one should not wipe the
    # persisted device inventory for the hostname. A dedicated endpoint can be
    # added later to purge devices by hostname if needed.
    if info:
        return jsonify({"status": "removed"})
    return jsonify({"error": "agent not found"}), 404

# Endpoint: /api/agent/provision — methods POST.

@app.route("/api/agent/provision", methods=["POST"])
def provision_agent():
    data = request.json
    agent_id = data.get("agent_id")
    roles = data.get("roles", [])

    if not agent_id or not isinstance(roles, list):
        return jsonify({"error": "Missing agent_id or roles[] in provision payload."}), 400

    config = {"roles": roles}
    agent_configurations[agent_id] = config

    if agent_id in registered_agents:
        registered_agents[agent_id]["status"] = "provisioned"

    # Target only the intended agent by emitting to its room
    try:
        socketio.emit("agent_config", {**config, "agent_id": agent_id}, room=agent_id)
    except TypeError:
        # Compatibility with older flask-socketio versions that use 'to'
        socketio.emit("agent_config", {**config, "agent_id": agent_id}, to=agent_id)
    return jsonify({"status": "provisioned", "roles": roles})

# Endpoint: /api/proxy — fan-out HTTP proxy used by agents.

@app.route("/api/proxy", methods=["GET", "POST", "OPTIONS"])
def proxy():
    target = request.args.get("url")
    if not target:
        return {"error": "Missing ?url="}, 400

    upstream = requests.request(
        method  = request.method,
        url     = target,
        headers = {k:v for k,v in request.headers if k.lower() != "host"},
        data    = request.get_data(),
        timeout = 10
    )

    excluded = ["content-encoding","content-length","transfer-encoding","connection"]
    headers  = [(k,v) for k,v in upstream.raw.headers.items() if k.lower() not in excluded]

    resp = make_response(upstream.content, upstream.status_code)
    for k,v in headers:
        resp.headers[k] = v
    return resp

# Endpoint: /api/agent/<agent_id>/node/<node_id>/screenshot/live — lightweight diagnostic viewer.

@app.route("/api/agent/<agent_id>/node/<node_id>/screenshot/live")
def screenshot_node_viewer(agent_id, node_id):
    return f"""
    <!DOCTYPE html>
    <html>
    <head>
        <title>Borealis Live View - {agent_id}:{node_id}</title>
        <style>
            body {{
                margin: 0;
                background-color: #000;
                display: flex;
                align-items: center;
                justify-content: center;
                height: 100vh;
            }}
            canvas {{
                border: 1px solid #444;
                max-width: 90vw;
                max-height: 90vh;
                background-color: #111;
            }}
        </style>
    </head>
    <body>
        <canvas id="viewerCanvas"></canvas>
        <script src="https://cdn.socket.io/4.8.1/socket.io.min.js"></script>
        <script>
            const agentId = "{agent_id}";
            const nodeId = "{node_id}";
            const canvas = document.getElementById("viewerCanvas");
            const ctx = canvas.getContext("2d");
            const socket = io(window.location.origin, {{ transports: ["websocket"] }});

            socket.on("agent_screenshot_task", (data) => {{
                if (data.agent_id !== agentId || data.node_id !== nodeId) return;
                const base64 = data.image_base64;
                if (!base64 || base64.length < 100) return;

                const img = new Image();
                img.onload = () => {{
                    if (canvas.width !== img.width || canvas.height !== img.height) {{
                        canvas.width = img.width;
                        canvas.height = img.height;
                    }}
                    ctx.clearRect(0, 0, canvas.width, canvas.height);
                    ctx.drawImage(img, 0, 0);
                }};
                img.src = "data:image/png;base64," + base64;
            }});
        </script>
    </body>
    </html>
    """

# =============================================================================
# Section: WebSocket Event Handlers
# =============================================================================
# Realtime channels for screenshots, macros, windows, and Ansible control.

@socketio.on('connect')
def socket_connect():
    try:
        sid = getattr(request, 'sid', '<unknown>')
    except Exception:
        sid = '<unknown>'
    try:
        remote_addr = request.remote_addr
    except Exception:
        remote_addr = None
    try:
        scope = _canonical_server_scope(request.headers.get(_AGENT_CONTEXT_HEADER))
    except Exception:
        scope = None
    try:
        query_pairs = [f"{k}={v}" for k, v in request.args.items()]  # type: ignore[attr-defined]
        query_summary = "&".join(query_pairs) if query_pairs else "<none>"
    except Exception:
        query_summary = "<unavailable>"
    header_summary = _summarize_socket_headers(getattr(request, 'headers', {}))
    transport = request.args.get('transport') if hasattr(request, 'args') else None  # type: ignore[attr-defined]
    _write_service_log(
        'server',
        f"socket.io connect sid={sid} ip={remote_addr} transport={transport!r} query={query_summary} headers={header_summary}",
        scope=scope,
    )


@socketio.on('disconnect')
def socket_disconnect():
    try:
        sid = getattr(request, 'sid', '<unknown>')
    except Exception:
        sid = '<unknown>'
    try:
        remote_addr = request.remote_addr
    except Exception:
        remote_addr = None
    try:
        scope = _canonical_server_scope(request.headers.get(_AGENT_CONTEXT_HEADER))
    except Exception:
        scope = None
    _write_service_log(
        'server',
        f"socket.io disconnect sid={sid} ip={remote_addr}",
        scope=scope,
    )


@socketio.on("agent_screenshot_task")
def receive_screenshot_task(data):
    agent_id = data.get("agent_id")
    node_id = data.get("node_id")
    image = data.get("image_base64", "")
    
    if not agent_id or not node_id:
        print("[WS] Screenshot task missing agent_id or node_id.")
        return

    if image:
        latest_images[f"{agent_id}:{node_id}"] = {
            "image_base64": image,
            "timestamp": time.time()
        }

    # Relay to all connected clients; use server-level emit
    socketio.emit("agent_screenshot_task", data)

@socketio.on("connect_agent")
def connect_agent(data):
    """
    Initial agent connect. Agent may only send agent_id here.
    Hostname/OS are filled in by subsequent heartbeats.
    """
    agent_id = (data or {}).get("agent_id")
    if not agent_id:
        return
    print(f"Agent connected: {agent_id}")
    try:
        scope = _normalize_service_mode((data or {}).get("service_mode"), agent_id)
    except Exception:
        scope = None
    try:
        sid = getattr(request, 'sid', '<unknown>')
    except Exception:
        sid = '<unknown>'
    _write_service_log(
        'server',
        f"socket.io connect_agent agent_id={agent_id} sid={sid} service_mode={scope}",
        scope=scope,
    )

    # Join per-agent room so we can address this connection specifically
    try:
        join_room(agent_id)
    except Exception:
        pass

    service_mode = scope if scope else _normalize_service_mode((data or {}).get("service_mode"), agent_id)
    rec = registered_agents.setdefault(agent_id, {})
    rec["agent_id"] = agent_id
    rec["hostname"] = rec.get("hostname", "unknown")
    rec["agent_operating_system"] = rec.get("agent_operating_system", "-")
    rec["last_seen"] = int(time.time())
    rec["status"] = "provisioned" if agent_id in agent_configurations else "orphaned"
    rec["service_mode"] = service_mode
    # Flag non-interactive script agents so they can be filtered out elsewhere if desired
    try:
        if isinstance(agent_id, str) and agent_id.lower().endswith('-script'):
            rec['is_script_agent'] = service_mode != 'currentuser'
        elif 'is_script_agent' in rec:
            rec.pop('is_script_agent', None)
    except Exception:
        pass
    # If we already know the hostname for this agent, persist last_seen so it
    # can be restored after server restarts.
    try:
        _persist_last_seen(rec.get("hostname"), rec["last_seen"], rec.get("agent_id"))
    except Exception:
        pass

@socketio.on("agent_heartbeat")
def on_agent_heartbeat(data):
    """
    Heartbeat payload from agent:
      { agent_id, hostname, agent_operating_system, last_seen }
    Updates registry so Devices view can display OS/hostname and recency.
    """
    if not data:
        return
    agent_id = data.get("agent_id")
    if not agent_id:
        return
    hostname = data.get("hostname")

    incoming_mode = _normalize_service_mode(data.get("service_mode"), agent_id)

    if hostname:
        # Avoid duplicate entries per-hostname by collapsing to the newest agent_id.
        # Prefer non-script agents; we do not surface script agents in /api/agents.
        try:
            is_current_script = isinstance(agent_id, str) and agent_id.lower().endswith('-script')
        except Exception:
            is_current_script = False
        # Transfer any existing configuration from displaced entries to this agent if needed
        transferred_cfg = False
        for aid, info in list(registered_agents.items()):
            if aid == agent_id:
                continue
            if info.get("hostname") == hostname:
                existing_mode = _normalize_service_mode(info.get("service_mode"), aid)
                if existing_mode != incoming_mode:
                    continue
                # If the incoming is a script helper and there is a non-script entry, keep non-script
                if is_current_script and not info.get('is_script_agent'):
                    # Do not register duplicate script entry; just update last_seen persistence below
                    # and return after persistence to avoid creating a second record.
                    try:
                        _persist_last_seen(hostname, int(data.get("last_seen") or time.time()), info.get("agent_id") or aid)
                    except Exception:
                        pass
                    return
                # Otherwise, evict the older/placeholder/script entry and transfer config if present
                if not transferred_cfg and aid in agent_configurations and agent_id not in agent_configurations:
                    agent_configurations[agent_id] = agent_configurations.get(aid)
                    transferred_cfg = True
                registered_agents.pop(aid, None)
                agent_configurations.pop(aid, None)

    rec = registered_agents.setdefault(agent_id, {})
    rec["agent_id"] = agent_id
    if hostname:
        rec["hostname"] = hostname
    if data.get("agent_operating_system"):
        rec["agent_operating_system"] = data.get("agent_operating_system")
    rec["last_seen"] = int(data.get("last_seen") or time.time())
    rec["status"] = "provisioned" if agent_id in agent_configurations else rec.get("status", "orphaned")
    rec["service_mode"] = incoming_mode
    try:
        if isinstance(agent_id, str) and agent_id.lower().endswith('-script'):
            rec['is_script_agent'] = incoming_mode != 'currentuser'
        elif 'is_script_agent' in rec:
            rec.pop('is_script_agent', None)
    except Exception:
        pass
    # Persist last_seen (and agent_id) into DB keyed by hostname so it survives restarts.
    try:
        _persist_last_seen(rec.get("hostname") or hostname, rec["last_seen"], rec.get("agent_id"))
    except Exception:
        pass

@socketio.on("request_config")
def send_agent_config(data):
    agent_id = (data or {}).get("agent_id")
    config = agent_configurations.get(agent_id)
    if config:
        emit("agent_config", config)

@socketio.on("screenshot")
def receive_screenshot(data):
    agent_id = data.get("agent_id")
    image = data.get("image_base64")

    if agent_id and image:
        latest_images[agent_id] = {
            "image_base64": image,
            "timestamp": time.time()
        }
        # Broadcast to all clients; use server-level emit
        socketio.emit("new_screenshot", {"agent_id": agent_id, "image_base64": image})

@socketio.on("disconnect")
def on_disconnect():
    print("[WebSocket] Connection Disconnected")

# Macro Websocket Handlers
@socketio.on("macro_status")
def receive_macro_status(data):
    """
    Receives macro status/errors from agent and relays to all clients
    Expected payload: {
        "agent_id": ...,
        "node_id": ...,
        "success": True/False,
        "message": "...",
        "timestamp": ...
    }
    """
    print(f"[Macro Status] Agent {data.get('agent_id')} Node {data.get('node_id')} Success: {data.get('success')} Msg: {data.get('message')}")
    # Broadcast to all; use server-level emit for v5 API
    socketio.emit("macro_status", data)

@socketio.on("list_agent_windows")
def handle_list_agent_windows(data):
    """
    Forwards list_agent_windows event to all agents (or filter for a specific agent_id).
    """
    # Forward to all agents/clients
    socketio.emit("list_agent_windows", data)

@socketio.on("agent_window_list")
def handle_agent_window_list(data):
    """
    Relay the list of windows from the agent back to all connected clients.
    """
    # Relay the list to all interested clients
    socketio.emit("agent_window_list", data)

# Relay Ansible control messages from UI to agents
@socketio.on("ansible_playbook_cancel")
def relay_ansible_cancel(data):
    try:
        socketio.emit("ansible_playbook_cancel", data)
    except Exception:
        pass

@socketio.on("ansible_playbook_run")
def relay_ansible_run(data):
    try:
        socketio.emit("ansible_playbook_run", data)
    except Exception:
        pass

# =============================================================================
# Section: Module Entrypoint
# =============================================================================
# Run the Socket.IO-enabled Flask server when executed as __main__.

if __name__ == "__main__":
    # Use SocketIO runner so WebSocket transport works with eventlet.
    # Eventlet's WSGI server expects raw cert/key paths rather than an ssl.SSLContext.
    socketio.run(
        app,
        host="0.0.0.0",
        port=5000,
        certfile=TLS_CERT_PATH,
        keyfile=TLS_KEY_PATH,
    )
