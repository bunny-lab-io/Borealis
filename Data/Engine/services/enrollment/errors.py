"""Error types shared across enrollment components."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

__all__ = ["EnrollmentValidationError"]


@dataclass(frozen=True, slots=True)
class EnrollmentValidationError(Exception):
    """Raised when enrollment input fails validation."""

    code: str
    http_status: int = 400
    retry_after: Optional[float] = None

    def to_response(self) -> dict[str, object]:
        payload: dict[str, object] = {"error": self.code}
        if self.retry_after is not None:
            payload["retry_after"] = self.retry_after
        return payload

    def __str__(self) -> str:  # pragma: no cover - debug helper
        return f"{self.code} (status={self.http_status})"
