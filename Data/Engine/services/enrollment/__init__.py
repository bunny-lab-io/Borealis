"""Enrollment services for the Borealis Engine."""

from __future__ import annotations

from importlib import import_module
from typing import Any

__all__ = [
    "EnrollmentService",
    "EnrollmentRequestResult",
    "EnrollmentStatus",
    "EnrollmentTokenBundle",
    "PollingResult",
    "EnrollmentValidationError",
    "EnrollmentAdminService",
]

_LAZY: dict[str, tuple[str, str]] = {
    "EnrollmentService": ("Data.Engine.services.enrollment.enrollment_service", "EnrollmentService"),
    "EnrollmentRequestResult": (
        "Data.Engine.services.enrollment.enrollment_service",
        "EnrollmentRequestResult",
    ),
    "EnrollmentStatus": ("Data.Engine.services.enrollment.enrollment_service", "EnrollmentStatus"),
    "EnrollmentTokenBundle": (
        "Data.Engine.services.enrollment.enrollment_service",
        "EnrollmentTokenBundle",
    ),
    "PollingResult": ("Data.Engine.services.enrollment.enrollment_service", "PollingResult"),
    "EnrollmentValidationError": (
        "Data.Engine.domain.device_enrollment",
        "EnrollmentValidationError",
    ),
    "EnrollmentAdminService": (
        "Data.Engine.services.enrollment.admin_service",
        "EnrollmentAdminService",
    ),
}


def __getattr__(name: str) -> Any:
    try:
        module_name, attribute = _LAZY[name]
    except KeyError as exc:  # pragma: no cover - defensive
        raise AttributeError(name) from exc

    module = import_module(module_name)
    value = getattr(module, attribute)
    globals()[name] = value
    return value


def __dir__() -> list[str]:  # pragma: no cover - interactive helper
    return sorted(set(__all__))

