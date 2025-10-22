"""Service container assembly for the Borealis Engine."""

from __future__ import annotations

import logging
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Optional

from Data.Engine.config import EngineSettings
from Data.Engine.repositories.sqlite import (
    SQLiteConnectionFactory,
    SQLiteDeviceRepository,
    SQLiteEnrollmentRepository,
    SQLiteRefreshTokenRepository,
)
from Data.Engine.services.auth import (
    DeviceAuthService,
    DPoPValidator,
    JWTService,
    TokenService,
    load_jwt_service,
)
from Data.Engine.services.crypto.signing import ScriptSigner, load_signer
from Data.Engine.services.enrollment import EnrollmentService
from Data.Engine.services.enrollment.nonce_cache import NonceCache
from Data.Engine.services.rate_limit import SlidingWindowRateLimiter

__all__ = ["EngineServiceContainer", "build_service_container"]


@dataclass(frozen=True, slots=True)
class EngineServiceContainer:
    device_auth: DeviceAuthService
    token_service: TokenService
    enrollment_service: EnrollmentService
    jwt_service: JWTService
    dpop_validator: DPoPValidator


def build_service_container(
    settings: EngineSettings,
    *,
    db_factory: SQLiteConnectionFactory,
    logger: Optional[logging.Logger] = None,
) -> EngineServiceContainer:
    log = logger or logging.getLogger("borealis.engine.services")

    device_repo = SQLiteDeviceRepository(db_factory, logger=log.getChild("devices"))
    token_repo = SQLiteRefreshTokenRepository(db_factory, logger=log.getChild("tokens"))
    enrollment_repo = SQLiteEnrollmentRepository(db_factory, logger=log.getChild("enrollment"))

    jwt_service = load_jwt_service()
    dpop_validator = DPoPValidator()
    rate_limiter = SlidingWindowRateLimiter()

    token_service = TokenService(
        refresh_token_repository=token_repo,
        device_repository=device_repo,
        jwt_service=jwt_service,
        dpop_validator=dpop_validator,
        logger=log.getChild("token_service"),
    )

    enrollment_service = EnrollmentService(
        device_repository=device_repo,
        enrollment_repository=enrollment_repo,
        token_repository=token_repo,
        jwt_service=jwt_service,
        tls_bundle_loader=_tls_bundle_loader(settings),
        ip_rate_limiter=SlidingWindowRateLimiter(),
        fingerprint_rate_limiter=SlidingWindowRateLimiter(),
        nonce_cache=NonceCache(),
        script_signer=_load_script_signer(log),
        logger=log.getChild("enrollment"),
    )

    device_auth = DeviceAuthService(
        device_repository=device_repo,
        jwt_service=jwt_service,
        logger=log.getChild("device_auth"),
        rate_limiter=rate_limiter,
        dpop_validator=dpop_validator,
    )

    return EngineServiceContainer(
        device_auth=device_auth,
        token_service=token_service,
        enrollment_service=enrollment_service,
        jwt_service=jwt_service,
        dpop_validator=dpop_validator,
    )


def _tls_bundle_loader(settings: EngineSettings) -> Callable[[], str]:
    candidates = [
        Path(os.getenv("BOREALIS_TLS_BUNDLE", "")),
        settings.project_root / "Certificates" / "Server" / "borealis-server-bundle.pem",
    ]

    def loader() -> str:
        for candidate in candidates:
            if candidate and candidate.is_file():
                try:
                    return candidate.read_text(encoding="utf-8")
                except Exception:
                    continue
        return ""

    return loader


def _load_script_signer(logger: logging.Logger) -> Optional[ScriptSigner]:
    try:
        return load_signer()
    except Exception as exc:
        logger.warning("script signer unavailable: %s", exc)
        return None
