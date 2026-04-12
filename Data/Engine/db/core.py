# ======================================================
# Data\Engine\db\core.py
# Description: Shared PostgreSQL/SQLAlchemy database engine helpers for the Borealis Engine runtime.
#
# API Endpoints (if applicable): None
# ======================================================

"""Shared SQLAlchemy-backed database runtime for Borealis."""

from __future__ import annotations

import logging
import threading
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, Optional

from sqlalchemy import create_engine, event
from sqlalchemy.engine import Engine


DEFAULT_ENGINE_SCHEMA = "engine"
DEFAULT_ASSEMBLIES_SCHEMA = "assemblies"


@dataclass(frozen=True)
class DatabaseSettings:
    """Resolved database settings for the Engine runtime."""

    url: str
    sslmode: str = "prefer"
    pool_size: int = 10
    max_overflow: int = 20
    connect_timeout: int = 15
    idle_in_transaction_timeout_ms: int = 60000
    engine_schema: str = DEFAULT_ENGINE_SCHEMA
    assemblies_schema: str = DEFAULT_ASSEMBLIES_SCHEMA


def normalize_database_url(raw: str) -> str:
    """Normalize common PostgreSQL DSN variants for SQLAlchemy."""

    text = str(raw or "").strip()
    if not text:
        raise ValueError("BOREALIS_DATABASE_URL is required.")
    if text.startswith("sqlite://"):
        return text
    if "://" not in text:
        return f"sqlite:///{Path(text).expanduser().resolve().as_posix()}"
    if text.startswith("postgres://"):
        return "postgresql+psycopg://" + text[len("postgres://") :]
    if text.startswith("postgresql://"):
        return "postgresql+psycopg://" + text[len("postgresql://") :]
    return text


class DatabaseManager:
    """Owns the shared SQLAlchemy engine and schema/bootstrap helpers."""

    def __init__(self, settings: DatabaseSettings, *, logger: Optional[logging.Logger] = None) -> None:
        self.settings = settings
        self.logger = logger or logging.getLogger(__name__)
        self._pool_metrics_lock = threading.Lock()
        self._checked_out_connections = 0
        self._pool_high_water = 0
        self._last_pool_warning = 0
        normalized_url = normalize_database_url(settings.url)
        engine_kwargs = {
            "future": True,
            "pool_pre_ping": True,
            "pool_size": max(1, int(settings.pool_size or 1)),
            "max_overflow": max(0, int(settings.max_overflow or 0)),
        }
        if normalized_url.startswith("postgresql+psycopg://"):
            engine_kwargs["connect_args"] = {
                "connect_timeout": max(1, int(settings.connect_timeout or 1)),
                "sslmode": str(settings.sslmode or "prefer"),
            }
        self._engine = create_engine(
            normalized_url,
            **engine_kwargs,
        )
        if normalized_url.startswith("postgresql+psycopg://"):
            self._configure_pool_observers()

    def _configure_pool_observers(self) -> None:
        capacity = max(1, int(self.settings.pool_size or 1) + max(0, int(self.settings.max_overflow or 0)))
        warning_threshold = max(1, int(capacity * 0.8))

        @event.listens_for(self._engine, "checkout")
        def _on_checkout(*_args) -> None:
            should_log = False
            checked_out = 0
            high_water = 0
            with self._pool_metrics_lock:
                self._checked_out_connections += 1
                checked_out = self._checked_out_connections
                if checked_out > self._pool_high_water:
                    self._pool_high_water = checked_out
                    high_water = checked_out
                    if checked_out >= warning_threshold:
                        should_log = True
                if checked_out >= warning_threshold and checked_out > self._last_pool_warning:
                    self._last_pool_warning = checked_out
                    should_log = True
                high_water = max(high_water, self._pool_high_water)
            if should_log:
                self.logger.warning(
                    "Database pool pressure rising checked_out=%s high_water=%s capacity=%s",
                    checked_out,
                    high_water,
                    capacity,
                )

        @event.listens_for(self._engine, "checkin")
        def _on_checkin(*_args) -> None:
            with self._pool_metrics_lock:
                self._checked_out_connections = max(0, self._checked_out_connections - 1)
                if self._checked_out_connections < warning_threshold:
                    self._last_pool_warning = 0

    @property
    def engine(self) -> Engine:
        return self._engine

    def raw_connection(self):
        conn = self._engine.raw_connection()
        if str(self._engine.url).startswith("postgresql+psycopg://"):
            try:
                cur = conn.cursor()
                cur.execute(
                    f"SET search_path TO {self.settings.engine_schema}, {self.settings.assemblies_schema}, public"
                )
                cur.execute("SET TIME ZONE 'UTC'")
                idle_timeout_ms = max(0, int(self.settings.idle_in_transaction_timeout_ms or 0))
                if idle_timeout_ms > 0:
                    cur.execute(f"SET idle_in_transaction_session_timeout = {idle_timeout_ms}")
                conn.commit()
            except Exception:
                try:
                    conn.rollback()
                except Exception:
                    pass
                raise
        return conn

    def ensure_schemas(self) -> None:
        if not str(self._engine.url).startswith("postgresql+psycopg://"):
            return
        with self._engine.begin() as conn:
            conn.exec_driver_sql(f"CREATE SCHEMA IF NOT EXISTS {self.settings.engine_schema}")
            conn.exec_driver_sql(f"CREATE SCHEMA IF NOT EXISTS {self.settings.assemblies_schema}")

    def healthcheck(self) -> None:
        with self._engine.connect() as conn:
            conn.exec_driver_sql("SELECT 1")


_MANAGER_LOCK = threading.Lock()
_MANAGERS: Dict[str, DatabaseManager] = {}


def get_database_manager(
    database_url: str,
    *,
    sslmode: str = "prefer",
    pool_size: int = 10,
    max_overflow: int = 20,
    connect_timeout: int = 15,
    idle_in_transaction_timeout_ms: int = 60000,
    logger: Optional[logging.Logger] = None,
) -> DatabaseManager:
    """Return a cached database manager for the provided DSN."""

    normalized = normalize_database_url(database_url)
    cache_key = "|".join(
        [
            normalized,
            str(sslmode or "prefer"),
            str(pool_size or 10),
            str(max_overflow or 20),
            str(connect_timeout or 15),
            str(idle_in_transaction_timeout_ms or 60000),
        ]
    )
    with _MANAGER_LOCK:
        manager = _MANAGERS.get(cache_key)
        if manager is None:
            manager = DatabaseManager(
                DatabaseSettings(
                    url=normalized,
                    sslmode=str(sslmode or "prefer"),
                    pool_size=max(1, int(pool_size or 1)),
                    max_overflow=max(0, int(max_overflow or 0)),
                    connect_timeout=max(1, int(connect_timeout or 1)),
                    idle_in_transaction_timeout_ms=max(0, int(idle_in_transaction_timeout_ms or 0)),
                ),
                logger=logger,
            )
            _MANAGERS[cache_key] = manager
        return manager
