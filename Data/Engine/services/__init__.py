"""Application services for the Borealis Engine."""

from __future__ import annotations

from importlib import import_module
from typing import Any, Dict, Tuple

__all__ = [
    "DeviceAuthService",
    "DeviceRecord",
    "RefreshTokenRecord",
    "TokenRefreshError",
    "TokenRefreshErrorCode",
    "TokenService",
    "EnrollmentService",
    "EnrollmentRequestResult",
    "EnrollmentStatus",
    "EnrollmentTokenBundle",
    "EnrollmentValidationError",
    "PollingResult",
    "AgentRealtimeService",
    "AgentRecord",
    "SchedulerService",
    "GitHubService",
    "GitHubTokenPayload",
    "EnrollmentAdminService",
    "SiteService",
    "DeviceInventoryService",
    "DeviceViewService",
    "CredentialService",
]

_LAZY_TARGETS: Dict[str, Tuple[str, str]] = {
    "DeviceAuthService": ("Data.Engine.services.auth.device_auth_service", "DeviceAuthService"),
    "DeviceRecord": ("Data.Engine.services.auth.device_auth_service", "DeviceRecord"),
    "RefreshTokenRecord": ("Data.Engine.services.auth.device_auth_service", "RefreshTokenRecord"),
    "TokenService": ("Data.Engine.services.auth.token_service", "TokenService"),
    "TokenRefreshError": ("Data.Engine.services.auth.token_service", "TokenRefreshError"),
    "TokenRefreshErrorCode": ("Data.Engine.services.auth.token_service", "TokenRefreshErrorCode"),
    "EnrollmentService": ("Data.Engine.services.enrollment.enrollment_service", "EnrollmentService"),
    "EnrollmentRequestResult": ("Data.Engine.services.enrollment.enrollment_service", "EnrollmentRequestResult"),
    "EnrollmentStatus": ("Data.Engine.services.enrollment.enrollment_service", "EnrollmentStatus"),
    "EnrollmentTokenBundle": ("Data.Engine.services.enrollment.enrollment_service", "EnrollmentTokenBundle"),
    "PollingResult": ("Data.Engine.services.enrollment.enrollment_service", "PollingResult"),
    "EnrollmentValidationError": ("Data.Engine.domain.device_enrollment", "EnrollmentValidationError"),
    "AgentRealtimeService": ("Data.Engine.services.realtime.agent_registry", "AgentRealtimeService"),
    "AgentRecord": ("Data.Engine.services.realtime.agent_registry", "AgentRecord"),
    "SchedulerService": ("Data.Engine.services.jobs.scheduler_service", "SchedulerService"),
    "GitHubService": ("Data.Engine.services.github.github_service", "GitHubService"),
    "GitHubTokenPayload": ("Data.Engine.services.github.github_service", "GitHubTokenPayload"),
    "EnrollmentAdminService": (
        "Data.Engine.services.enrollment.admin_service",
        "EnrollmentAdminService",
    ),
    "SiteService": ("Data.Engine.services.sites.site_service", "SiteService"),
    "DeviceInventoryService": (
        "Data.Engine.services.devices.device_inventory_service",
        "DeviceInventoryService",
    ),
    "DeviceViewService": (
        "Data.Engine.services.devices.device_view_service",
        "DeviceViewService",
    ),
    "CredentialService": (
        "Data.Engine.services.credentials.credential_service",
        "CredentialService",
    ),
}


def __getattr__(name: str) -> Any:
    try:
        module_name, attribute = _LAZY_TARGETS[name]
    except KeyError as exc:
        raise AttributeError(name) from exc

    module = import_module(module_name)
    value = getattr(module, attribute)
    globals()[name] = value
    return value


def __dir__() -> Any:  # pragma: no cover - interactive helper
    return sorted(set(__all__))
