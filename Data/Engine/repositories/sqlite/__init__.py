"""SQLite persistence helpers for the Borealis Engine."""

from __future__ import annotations

from .connection import (
    SQLiteConnectionFactory,
    configure_connection,
    connect,
    connection_factory,
    connection_scope,
)
from .device_repository import SQLiteDeviceRepository
from .enrollment_repository import SQLiteEnrollmentRepository
from .github_repository import SQLiteGitHubRepository
from .job_repository import SQLiteJobRepository
from .migrations import apply_all
from .token_repository import SQLiteRefreshTokenRepository

__all__ = [
    "SQLiteConnectionFactory",
    "configure_connection",
    "connect",
    "connection_factory",
    "connection_scope",
    "SQLiteDeviceRepository",
    "SQLiteRefreshTokenRepository",
    "SQLiteJobRepository",
    "SQLiteEnrollmentRepository",
    "SQLiteGitHubRepository",
    "apply_all",
]
