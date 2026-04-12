# ======================================================
# Data\Engine\services\API\__init__.py
# Description: Registers Engine API groups, wiring Engine-native authentication while delegating remaining legacy modules.
#
# API Endpoints (if applicable):
# - GET /health (No Authentication) - Returns an OK status for liveness probing.
# ======================================================

"""API service adapters for the Borealis Engine runtime."""
from __future__ import annotations

import datetime as _dt
import logging
import os
import re
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Iterable, Mapping, Optional, Sequence

from flask import Blueprint, Flask, jsonify

from ...auth import jwt_service as jwt_service_module
from ...auth.device_auth import DeviceAuthManager
from ...auth.dpop import DPoPValidator
from ...auth.rate_limit import SlidingWindowRateLimiter
from ...db import dbapi as sqlite3
from ...db import get_database_manager
from ...database import initialise_engine_database
from ...security import signing
from ...enrollment import NonceCache
from ...integrations import GitHubIntegration
from ..aegis_cipher import AegisCipherService
from ..auth import DevModeManager
from .enrollment import routes as enrollment_routes
from .tokens import routes as token_routes
from .devices.tunnel import register_tunnel
from .devices.vnc import register_vnc
from .devices.shell import register_shell
from .devices.services import register_services

from ...server import EngineContext
from .access_management.login import register_auth
from .assemblies.management import register_assemblies
from .assemblies.execution import register_execution
from .devices import routes as device_routes
from .devices.approval import register_admin_endpoints
from .devices.management import register_management
from .filters import management as filters_management
from .notifications import management as notifications_management
from .scheduled_jobs import management as scheduled_jobs_management
from .watchdogs import management as watchdogs_management
from .workflows import management as workflows_management
from .server import info as server_info, log_management

DEFAULT_API_GROUPS: Sequence[str] = (
    "core",
    "auth",
    "tokens",
    "enrollment",
    "devices",
    "filters",
    "server",
    "assemblies",
    "workflows",
    "scheduled_jobs",
    "notifications",
    "watchdogs",
)

_SERVER_SCOPE_PATTERN = re.compile(r"\\b(?:scope|context|agent_context)=([A-Za-z0-9_-]+)", re.IGNORECASE)
_SERVER_AGENT_ID_PATTERN = re.compile(r"\\bagent_id=([^\\s,]+)", re.IGNORECASE)


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


def _rotate_daily(path: Path) -> None:
    try:
        if not path.is_file():
            return
        stat = path.stat()
        modified = _dt.datetime.fromtimestamp(stat.st_mtime)
        if modified.date() == _dt.datetime.now().date():
            return
        suffix = modified.strftime("%Y-%m-%d")
        rotated = path.with_name(f"{path.name}.{suffix}")
        if rotated.exists():
            return
        path.rename(rotated)
    except Exception:
        pass


_QUIET_SERVICE_LOGS = {"scheduled_jobs", "device_enrollment"}


def _make_service_logger(base: Path, logger: logging.Logger) -> Callable[[str, str, Optional[str]], None]:
    quiet_services = {name.strip().lower().replace("-", "_") for name in _QUIET_SERVICE_LOGS if name}

    def _log(service: str, msg: str, scope: Optional[str] = None, *, level: str = "INFO") -> None:
        level_upper = level.upper()
        service_key = (service or "").strip().lower()
        try:
            base.mkdir(parents=True, exist_ok=True)
            path = base / f"{service}.log"
            try:
                path.parent.mkdir(parents=True, exist_ok=True)
            except Exception:
                pass
            _rotate_daily(path)
            timestamp = time.strftime("%Y-%m-%d %H:%M:%S")
            resolved_scope = _infer_server_scope(msg, scope)
            prefix_parts = [f"[{level_upper}]"]
            if resolved_scope:
                prefix_parts.append(f"[CONTEXT-{resolved_scope}]")
            prefix = "".join(prefix_parts)
            with path.open("a", encoding="utf-8") as handle:
                handle.write(f"[{timestamp}] {prefix} {msg}\n")
        except Exception:
            logger.debug("Failed to write service log entry", exc_info=True)

        numeric_level = getattr(logging, level_upper, logging.INFO)
        suppress_engine_log = service_key in quiet_services or service_key.replace("-", "_") in quiet_services
        if not suppress_engine_log:
            logger.log(numeric_level, "[service:%s] %s", service, msg)

    return _log


def _make_db_conn_factory(
    database_url: str,
    *,
    sslmode: str = "prefer",
    pool_size: int = 10,
    max_overflow: int = 20,
    connect_timeout: int = 15,
    idle_in_transaction_timeout_ms: int = 60000,
    logger: Optional[logging.Logger] = None,
) -> Callable[[], sqlite3.Connection]:
    manager = get_database_manager(
        database_url,
        sslmode=sslmode,
        pool_size=pool_size,
        max_overflow=max_overflow,
        connect_timeout=connect_timeout,
        idle_in_transaction_timeout_ms=idle_in_transaction_timeout_ms,
        logger=logger,
    )

    def _factory() -> sqlite3.Connection:
        if str(manager.engine.url).startswith("sqlite"):
            return sqlite3.connect(database_url, timeout=connect_timeout)
        return sqlite3.Connection(manager.raw_connection(), dialect="postgresql")

    return _factory


@dataclass
class EngineServiceAdapters:
    context: EngineContext
    config: Mapping[str, Any] = field(init=False)
    db_conn_factory: Callable[[], sqlite3.Connection] = field(init=False)
    db_manager: Any = field(init=False)
    jwt_service: Any = field(init=False)
    dpop_validator: DPoPValidator = field(init=False)
    ip_rate_limiter: SlidingWindowRateLimiter = field(init=False)
    fp_rate_limiter: SlidingWindowRateLimiter = field(init=False)
    device_rate_limiter: SlidingWindowRateLimiter = field(init=False)
    nonce_cache: NonceCache = field(init=False)
    script_signer: Any = field(init=False)
    service_log: Callable[[str, str, Optional[str]], None] = field(init=False)
    device_auth_manager: DeviceAuthManager = field(init=False)
    github_integration: GitHubIntegration = field(init=False)
    dev_mode_manager: DevModeManager = field(init=False)
    aegis_cipher_service: AegisCipherService = field(init=False)

    def __post_init__(self) -> None:
        self.db_manager = get_database_manager(
            self.context.database_url,
            sslmode=str(self.context.config.get("db_sslmode") or "prefer"),
            pool_size=int(self.context.config.get("db_pool_size") or 10),
            max_overflow=int(self.context.config.get("db_max_overflow") or 20),
            connect_timeout=int(self.context.config.get("db_connect_timeout") or 15),
            idle_in_transaction_timeout_ms=int(self.context.config.get("db_idle_in_transaction_timeout_ms") or 60000),
            logger=self.context.logger,
        )
        self.db_conn_factory = _make_db_conn_factory(
            self.context.database_url,
            sslmode=str(self.context.config.get("db_sslmode") or "prefer"),
            pool_size=int(self.context.config.get("db_pool_size") or 10),
            max_overflow=int(self.context.config.get("db_max_overflow") or 20),
            connect_timeout=int(self.context.config.get("db_connect_timeout") or 15),
            idle_in_transaction_timeout_ms=int(self.context.config.get("db_idle_in_transaction_timeout_ms") or 60000),
            logger=self.context.logger,
        )
        initialise_engine_database(self.context.database_url, logger=self.context.logger)
        self.config = dict(self.context.config or {})
        self.jwt_service = jwt_service_module.load_service()
        self.dpop_validator = DPoPValidator()
        self.ip_rate_limiter = SlidingWindowRateLimiter()
        self.fp_rate_limiter = SlidingWindowRateLimiter()
        self.nonce_cache = NonceCache()
        try:
            self.script_signer = signing.load_signer()
        except Exception:
            self.script_signer = None

        log_file = str(self.config.get("log_file") or self.config.get("LOG_FILE") or "")
        if log_file:
            base = Path(log_file).resolve().parent
        else:
            base = Path.cwd() / "Engine" / "Logs"
        self.service_log = _make_service_logger(base, self.context.logger)
        self.aegis_cipher_service = AegisCipherService(
            db_conn_factory=self.db_conn_factory,
            logger=self.context.logger,
            service_log=self.service_log,
            hmac_secret=str(self.context.config.get("SECRET_KEY") or ""),
        )
        self.context.aegis_cipher_service = self.aegis_cipher_service

        self.device_rate_limiter = SlidingWindowRateLimiter()
        self.device_auth_manager = DeviceAuthManager(
            db_conn_factory=self.db_conn_factory,
            jwt_service=self.jwt_service,
            dpop_validator=self.dpop_validator,
            log=self.service_log,
            rate_limiter=self.device_rate_limiter,
        )

        config = self.config
        cache_root_value = config.get("cache_dir") or config.get("CACHE_DIR")
        if cache_root_value:
            cache_root = Path(str(cache_root_value))
        else:
            cache_root = base / "cache"
        cache_file = cache_root / "repo_hash_cache.json"

        default_repo = config.get("default_repo") or config.get("DEFAULT_REPO")
        default_branch = config.get("default_branch") or config.get("DEFAULT_BRANCH")
        ttl_raw = config.get("repo_hash_refresh") or config.get("REPO_HASH_REFRESH")
        try:
            default_ttl_seconds = int(ttl_raw) if ttl_raw is not None else None
        except (TypeError, ValueError):
            default_ttl_seconds = None

        self.github_integration = GitHubIntegration(
            cache_file=cache_file,
            db_conn_factory=self.db_conn_factory,
            service_log=self.service_log,
            logger=self.context.logger,
            default_repo=default_repo,
            default_branch=default_branch,
            default_ttl_seconds=default_ttl_seconds,
            aegis_cipher_service=self.aegis_cipher_service,
        )

        env_ttl_raw = os.environ.get("BOREALIS_DEV_MODE_TTL_SECONDS")
        try:
            env_ttl = int(env_ttl_raw) if env_ttl_raw else None
        except (TypeError, ValueError):
            env_ttl = None
        config_ttl_raw = config.get("assemblies_dev_mode_ttl_seconds")
        try:
            config_ttl = int(config_ttl_raw) if config_ttl_raw is not None else None
        except (TypeError, ValueError):
            config_ttl = None

        default_ttl = config_ttl or env_ttl or 900
        if default_ttl < 60:
            default_ttl = 60
        self.dev_mode_manager = DevModeManager(
            logger=self.context.logger,
            default_ttl_seconds=default_ttl,
        )


def _register_tokens(app: Flask, adapters: EngineServiceAdapters) -> None:
    token_routes.register(
        app,
        db_conn_factory=adapters.db_conn_factory,
        jwt_service=adapters.jwt_service,
        dpop_validator=adapters.dpop_validator,
    )


def _register_enrollment(app: Flask, adapters: EngineServiceAdapters) -> None:
    enrollment_routes.register(
        app,
        db_conn_factory=adapters.db_conn_factory,
        log=adapters.service_log,
        jwt_service=adapters.jwt_service,
        ip_rate_limiter=adapters.ip_rate_limiter,
        fp_rate_limiter=adapters.fp_rate_limiter,
        nonce_cache=adapters.nonce_cache,
        script_signer=adapters.script_signer,
    )


def _register_devices(app: Flask, adapters: EngineServiceAdapters) -> None:
    register_management(app, adapters)
    register_admin_endpoints(app, adapters)
    device_routes.register_agents(app, adapters)
    register_tunnel(app, adapters)
    register_vnc(app, adapters)
    register_shell(app, adapters)
    register_services(app, adapters)

def _register_filters(app: Flask, adapters: EngineServiceAdapters) -> None:
    filters_management.register_filters(app, adapters)


def _register_scheduled_jobs(app: Flask, adapters: EngineServiceAdapters) -> None:
    scheduled_jobs_management.register_management(app, adapters)


def _register_workflows(app: Flask, adapters: EngineServiceAdapters) -> None:
    workflows_management.register_management(app, adapters)


def _register_notifications(app: Flask, adapters: EngineServiceAdapters) -> None:
    notifications_management.register_notifications(app, adapters)


def _register_watchdogs(app: Flask, adapters: EngineServiceAdapters) -> None:
    watchdogs_management.register_management(app, adapters)


def _register_assemblies(app: Flask, adapters: EngineServiceAdapters) -> None:
    register_assemblies(app, adapters)
    register_execution(app, adapters)


def _register_server(app: Flask, adapters: EngineServiceAdapters) -> None:
    server_info.register_info(app, adapters)
    log_management.register_log_management(app, adapters)


_GROUP_REGISTRARS: Mapping[str, Callable[[Flask, EngineServiceAdapters], None]] = {
    "auth": register_auth,
    "tokens": _register_tokens,
    "enrollment": _register_enrollment,
    "devices": _register_devices,
    "filters": _register_filters,
    "server": _register_server,
    "assemblies": _register_assemblies,
    "workflows": _register_workflows,
    "scheduled_jobs": _register_scheduled_jobs,
    "notifications": _register_notifications,
    "watchdogs": _register_watchdogs,
}


def _register_core(app: Flask, context: EngineContext) -> None:
    """Register core utility endpoints that do not require legacy adapters."""

    blueprint = Blueprint("engine_core", __name__)

    @blueprint.route("/health", methods=["GET"])
    def health() -> Any:
        return jsonify({"status": "ok"})

    app.register_blueprint(blueprint)
    context.logger.info("Engine registered API group 'core'.")


def register_api(app: Flask, context: EngineContext) -> None:
    """Register Engine API blueprints based on the enabled groups."""

    enabled_groups: Iterable[str] = context.api_groups or DEFAULT_API_GROUPS
    normalized = [group.strip().lower() for group in enabled_groups if group]
    if "filters" not in normalized:
        normalized.append("filters")
    if "notifications" not in normalized:
        normalized.append("notifications")
    if "watchdogs" not in normalized:
        normalized.append("watchdogs")
    adapters: Optional[EngineServiceAdapters] = None

    for group in normalized:
        if group == "core":
            _register_core(app, context)
            continue

        if adapters is None:
            adapters = EngineServiceAdapters(context)
        registrar = _GROUP_REGISTRARS.get(group)
        if registrar is None:
            context.logger.info("Engine API group '%s' is not implemented; skipping.", group)
            continue
        registrar(app, adapters)
        context.logger.info("Engine registered API group '%s'.", group)
