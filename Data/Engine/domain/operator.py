"""Domain models for operator authentication."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal, Optional


@dataclass(frozen=True, slots=True)
class OperatorAccount:
    """Snapshot of an operator account stored in SQLite."""

    username: str
    display_name: str
    password_sha512: str
    role: str
    last_login: int
    created_at: int
    updated_at: int
    mfa_enabled: bool
    mfa_secret: Optional[str]


@dataclass(frozen=True, slots=True)
class OperatorLoginSuccess:
    """Successful login payload for the caller."""

    username: str
    role: str
    token: str


@dataclass(frozen=True, slots=True)
class OperatorMFAChallenge:
    """Details describing an in-progress MFA challenge."""

    username: str
    role: str
    stage: Literal["setup", "verify"]
    pending_token: str
    expires_at: int
    secret: Optional[str] = None
    otpauth_url: Optional[str] = None
    qr_image: Optional[str] = None


__all__ = [
    "OperatorAccount",
    "OperatorLoginSuccess",
    "OperatorMFAChallenge",
]
