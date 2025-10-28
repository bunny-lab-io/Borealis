"""API service adapters for the Borealis Engine runtime."""
from __future__ import annotations

import datetime as _dt
import logging
import re
import sqlite3
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Iterable, Mapping, Optional, Sequence

from flask import Blueprint, Flask, jsonify

from Modules.auth import jwt_service as jwt_service_module
from Modules.auth.dpop import DPoPValidator
from Modules.auth.rate_limit import SlidingWindowRateLimiter
from Modules.crypto import signing
from Modules.enrollment import routes as enrollment_routes
from Modules.enrollment.nonce_store import NonceCache
from Modules.tokens import routes as token_routes

from ...server import EngineContext
from .access_management.login import register_auth
from .devices.management import register_management

DEFAULT_API_GROUPS: Sequence[str] = ("auth", "tokens", "enrollment", "devices")

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


def _make_service_logger(base: Path, logger: logging.Logger) -> Callable[[str, str, Optional[str]], None]:
    def _log(service: str, msg: str, scope: Optional[str] = None, *, level: str = "INFO") -> None:
        level_upper = level.upper()
        try:
            base.mkdir(parents=True, exist_ok=True)
            path = base / f"{service}.log"
            _rotate_daily(path)
            timestamp = time.strftime("%Y-%m-%d %H:%M:%S")
            resolved_scope = _infer_server_scope(msg, scope)
            prefix_parts = [f"[{level_upper}]"]
            if resolved_scope:
                prefix_parts.append(f"[CONTEXT-{resolved_scope}]")
            prefix = "".join(prefix_parts)
            with path.open("a", encoding="utf-8") as handle:
                handle.write(f"[{timestamp}] {prefix} {msg}\\n")
        except Exception:
            logger.debug("Failed to write service log entry", exc_info=True)

        numeric_level = getattr(logging, level_upper, logging.INFO)
        logger.log(numeric_level, "[service:%s] %s", service, msg)

    return _log


def _make_db_conn_factory(database_path: str) -> Callable[[], sqlite3.Connection]:
    def _factory() -> sqlite3.Connection:
        conn = sqlite3.connect(database_path, timeout=15)
        try:
            cur = conn.cursor()
            cur.execute("PRAGMA journal_mode=WAL")
            cur.execute("PRAGMA busy_timeout=5000")
            cur.execute("PRAGMA synchronous=NORMAL")
            conn.commit()
        except Exception:
            pass
        return conn

    return _factory


@dataclass
class LegacyServiceAdapters:
    context: EngineContext
    db_conn_factory: Callable[[], sqlite3.Connection] = field(init=False)
    jwt_service: Any = field(init=False)
    dpop_validator: DPoPValidator = field(init=False)
    ip_rate_limiter: SlidingWindowRateLimiter = field(init=False)
    fp_rate_limiter: SlidingWindowRateLimiter = field(init=False)
    nonce_cache: NonceCache = field(init=False)
    script_signer: Any = field(init=False)
    service_log: Callable[[str, str, Optional[str]], None] = field(init=False)

    def __post_init__(self) -> None:
        self.db_conn_factory = _make_db_conn_factory(self.context.database_path)
        self.jwt_service = jwt_service_module.load_service()
        self.dpop_validator = DPoPValidator()
        self.ip_rate_limiter = SlidingWindowRateLimiter()
        self.fp_rate_limiter = SlidingWindowRateLimiter()
        self.nonce_cache = NonceCache()
        try:
            self.script_signer = signing.load_signer()
        except Exception:
            self.script_signer = None

        log_file = str(self.context.config.get("log_file") or self.context.config.get("LOG_FILE") or "")
        if log_file:
            base = Path(log_file).resolve().parent
        else:
            base = Path(self.context.database_path).resolve().parent
        self.service_log = _make_service_logger(base, self.context.logger)


def _register_tokens(app: Flask, adapters: LegacyServiceAdapters) -> None:
    token_routes.register(
        app,
        db_conn_factory=adapters.db_conn_factory,
        jwt_service=adapters.jwt_service,
        dpop_validator=adapters.dpop_validator,
    )


def _register_enrollment(app: Flask, adapters: LegacyServiceAdapters) -> None:
    tls_bundle = adapters.context.tls_bundle_path or ""
    enrollment_routes.register(
        app,
        db_conn_factory=adapters.db_conn_factory,
        log=adapters.service_log,
        jwt_service=adapters.jwt_service,
        tls_bundle_path=tls_bundle,
        ip_rate_limiter=adapters.ip_rate_limiter,
        fp_rate_limiter=adapters.fp_rate_limiter,
        nonce_cache=adapters.nonce_cache,
        script_signer=adapters.script_signer,
    )


_GROUP_REGISTRARS: Mapping[str, Callable[[Flask, LegacyServiceAdapters], None]] = {
    "auth": register_auth,
    "tokens": _register_tokens,
    "enrollment": _register_enrollment,
    "devices": register_management,
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
    adapters: Optional[LegacyServiceAdapters] = None

    for group in normalized:
        if group == "core":
            _register_core(app, context)
            continue

        if adapters is None:
            adapters = LegacyServiceAdapters(context)
        registrar = _GROUP_REGISTRARS.get(group)
        if registrar is None:
            context.logger.info("Engine API group '%s' is not implemented; skipping.", group)
            continue
        registrar(app, adapters)
        context.logger.info("Engine registered API group '%s'.", group)
