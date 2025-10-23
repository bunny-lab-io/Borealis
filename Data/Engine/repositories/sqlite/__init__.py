"""SQLite persistence helpers for the Borealis Engine."""

from __future__ import annotations

from .connection import (
    SQLiteConnectionFactory,
    configure_connection,
    connect,
    connection_factory,
    connection_scope,
)
from .migrations import apply_all, ensure_default_admin

__all__ = [
    "SQLiteConnectionFactory",
    "configure_connection",
    "connect",
    "connection_factory",
    "connection_scope",
    "apply_all",
    "ensure_default_admin",
]

try:  # pragma: no cover - optional dependency shim
    from .device_repository import SQLiteDeviceRepository
    from .enrollment_repository import SQLiteEnrollmentRepository
    from .device_inventory_repository import SQLiteDeviceInventoryRepository
    from .device_view_repository import SQLiteDeviceViewRepository
    from .credential_repository import SQLiteCredentialRepository
    from .github_repository import SQLiteGitHubRepository
    from .job_repository import SQLiteJobRepository
    from .site_repository import SQLiteSiteRepository
    from .token_repository import SQLiteRefreshTokenRepository
    from .user_repository import SQLiteUserRepository
except ModuleNotFoundError as exc:  # pragma: no cover - triggered when auth deps missing
    def _missing_repo(*_args: object, **_kwargs: object) -> None:
        raise ModuleNotFoundError(
            "Engine SQLite repositories require optional authentication dependencies"
        ) from exc

    SQLiteDeviceRepository = _missing_repo  # type: ignore[assignment]
    SQLiteEnrollmentRepository = _missing_repo  # type: ignore[assignment]
    SQLiteDeviceInventoryRepository = _missing_repo  # type: ignore[assignment]
    SQLiteDeviceViewRepository = _missing_repo  # type: ignore[assignment]
    SQLiteCredentialRepository = _missing_repo  # type: ignore[assignment]
    SQLiteGitHubRepository = _missing_repo  # type: ignore[assignment]
    SQLiteJobRepository = _missing_repo  # type: ignore[assignment]
    SQLiteSiteRepository = _missing_repo  # type: ignore[assignment]
    SQLiteRefreshTokenRepository = _missing_repo  # type: ignore[assignment]
else:
    __all__ += [
        "SQLiteDeviceRepository",
        "SQLiteRefreshTokenRepository",
        "SQLiteJobRepository",
        "SQLiteEnrollmentRepository",
        "SQLiteDeviceInventoryRepository",
        "SQLiteDeviceViewRepository",
        "SQLiteCredentialRepository",
        "SQLiteGitHubRepository",
        "SQLiteUserRepository",
        "SQLiteSiteRepository",
    ]
