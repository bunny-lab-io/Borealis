"""Builders for operator authentication payloads."""

from __future__ import annotations

import hashlib
from dataclasses import dataclass
from typing import Mapping


@dataclass(frozen=True, slots=True)
class OperatorLoginRequest:
    """Normalized operator login credentials."""

    username: str
    password_sha512: str


@dataclass(frozen=True, slots=True)
class OperatorMFAVerificationRequest:
    """Normalized MFA verification payload."""

    pending_token: str
    code: str


def _sha512_hex(raw: str) -> str:
    digest = hashlib.sha512()
    digest.update(raw.encode("utf-8"))
    return digest.hexdigest()


def build_login_request(payload: Mapping[str, object]) -> OperatorLoginRequest:
    """Validate and normalize the login *payload*."""

    username = str(payload.get("username") or "").strip()
    password_sha512 = str(payload.get("password_sha512") or "").strip().lower()
    password = payload.get("password")

    if not username:
        raise ValueError("username is required")

    if password_sha512:
        normalized_hash = password_sha512
    else:
        if not isinstance(password, str) or not password:
            raise ValueError("password is required")
        normalized_hash = _sha512_hex(password)

    return OperatorLoginRequest(username=username, password_sha512=normalized_hash)


def build_mfa_request(payload: Mapping[str, object]) -> OperatorMFAVerificationRequest:
    """Validate and normalize the MFA verification *payload*."""

    pending_token = str(payload.get("pending_token") or "").strip()
    raw_code = str(payload.get("code") or "").strip()
    digits = "".join(ch for ch in raw_code if ch.isdigit())

    if not pending_token:
        raise ValueError("pending_token is required")
    if len(digits) < 6:
        raise ValueError("code must contain 6 digits")

    return OperatorMFAVerificationRequest(pending_token=pending_token, code=digits)


__all__ = [
    "OperatorLoginRequest",
    "OperatorMFAVerificationRequest",
    "build_login_request",
    "build_mfa_request",
]
