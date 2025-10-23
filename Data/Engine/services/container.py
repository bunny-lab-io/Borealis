"""Service container assembly for the Borealis Engine."""

from __future__ import annotations

import logging
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Optional

from Data.Engine.config import EngineSettings
from Data.Engine.integrations.github import GitHubArtifactProvider
from Data.Engine.repositories.sqlite import (
    SQLiteConnectionFactory,
    SQLiteDeviceRepository,
    SQLiteEnrollmentRepository,
    SQLiteGitHubRepository,
    SQLiteJobRepository,
    SQLiteRefreshTokenRepository,
    SQLiteUserRepository,
)
from Data.Engine.services.auth import (
    DeviceAuthService,
    DPoPValidator,
    OperatorAccountService,
    OperatorAuthService,
    JWTService,
    TokenService,
    load_jwt_service,
)
from Data.Engine.services.crypto.signing import ScriptSigner, load_signer
from Data.Engine.services.enrollment import EnrollmentService
from Data.Engine.services.enrollment.admin_service import EnrollmentAdminService
from Data.Engine.services.enrollment.nonce_cache import NonceCache
from Data.Engine.services.github import GitHubService
from Data.Engine.services.jobs import SchedulerService
from Data.Engine.services.rate_limit import SlidingWindowRateLimiter
from Data.Engine.services.realtime import AgentRealtimeService

__all__ = ["EngineServiceContainer", "build_service_container"]


@dataclass(frozen=True, slots=True)
class EngineServiceContainer:
    device_auth: DeviceAuthService
    token_service: TokenService
    enrollment_service: EnrollmentService
    enrollment_admin_service: EnrollmentAdminService
    jwt_service: JWTService
    dpop_validator: DPoPValidator
    agent_realtime: AgentRealtimeService
    scheduler_service: SchedulerService
    github_service: GitHubService
    operator_auth_service: OperatorAuthService
    operator_account_service: OperatorAccountService


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
    job_repo = SQLiteJobRepository(db_factory, logger=log.getChild("jobs"))
    github_repo = SQLiteGitHubRepository(db_factory, logger=log.getChild("github_repo"))
    user_repo = SQLiteUserRepository(db_factory, logger=log.getChild("users"))

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

    enrollment_admin_service = EnrollmentAdminService(
        repository=enrollment_repo,
        user_repository=user_repo,
        logger=log.getChild("enrollment_admin"),
    )

    device_auth = DeviceAuthService(
        device_repository=device_repo,
        jwt_service=jwt_service,
        logger=log.getChild("device_auth"),
        rate_limiter=rate_limiter,
        dpop_validator=dpop_validator,
    )

    agent_realtime = AgentRealtimeService(
        device_repository=device_repo,
        logger=log.getChild("agent_realtime"),
    )

    scheduler_service = SchedulerService(
        job_repository=job_repo,
        assemblies_root=settings.project_root / "Assemblies",
        logger=log.getChild("scheduler"),
    )

    operator_auth_service = OperatorAuthService(
        repository=user_repo,
        logger=log.getChild("operator_auth"),
    )
    operator_account_service = OperatorAccountService(
        repository=user_repo,
        logger=log.getChild("operator_accounts"),
    )

    github_provider = GitHubArtifactProvider(
        cache_file=settings.github.cache_file,
        default_repo=settings.github.default_repo,
        default_branch=settings.github.default_branch,
        refresh_interval=settings.github.refresh_interval_seconds,
        logger=log.getChild("github.provider"),
    )
    github_service = GitHubService(
        repository=github_repo,
        provider=github_provider,
        logger=log.getChild("github"),
    )
    github_service.start_background_refresh()

    return EngineServiceContainer(
        device_auth=device_auth,
        token_service=token_service,
        enrollment_service=enrollment_service,
        enrollment_admin_service=enrollment_admin_service,
        jwt_service=jwt_service,
        dpop_validator=dpop_validator,
        agent_realtime=agent_realtime,
        scheduler_service=scheduler_service,
        github_service=github_service,
        operator_auth_service=operator_auth_service,
        operator_account_service=operator_account_service,
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
