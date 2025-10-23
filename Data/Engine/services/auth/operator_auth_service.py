"""Operator authentication service."""

from __future__ import annotations

import base64
import io
import logging
import os
import time
import uuid
from typing import Optional

try:  # pragma: no cover - optional dependencies mirror legacy server behaviour
    import pyotp  # type: ignore
except Exception:  # pragma: no cover - gracefully degrade when unavailable
    pyotp = None  # type: ignore

try:  # pragma: no cover - optional dependency
    import qrcode  # type: ignore
except Exception:  # pragma: no cover - gracefully degrade when unavailable
    qrcode = None  # type: ignore

from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from Data.Engine.builders.operator_auth import (
    OperatorLoginRequest,
    OperatorMFAVerificationRequest,
)
from Data.Engine.domain import (
    OperatorAccount,
    OperatorLoginSuccess,
    OperatorMFAChallenge,
)
from Data.Engine.repositories.sqlite.user_repository import SQLiteUserRepository


class OperatorAuthError(Exception):
    """Base class for operator authentication errors."""


class InvalidCredentialsError(OperatorAuthError):
    """Raised when username/password verification fails."""


class MFAUnavailableError(OperatorAuthError):
    """Raised when MFA functionality is requested but dependencies are missing."""


class InvalidMFACodeError(OperatorAuthError):
    """Raised when the submitted MFA code is invalid."""


class MFASessionError(OperatorAuthError):
    """Raised when the MFA session state cannot be validated."""


class OperatorAuthService:
    """Authenticate operator accounts and manage MFA challenges."""

    def __init__(
        self,
        repository: SQLiteUserRepository,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._repository = repository
        self._log = logger or logging.getLogger("borealis.engine.services.operator_auth")

    def authenticate(
        self, request: OperatorLoginRequest
    ) -> OperatorLoginSuccess | OperatorMFAChallenge:
        account = self._repository.fetch_by_username(request.username)
        if not account:
            raise InvalidCredentialsError("invalid username or password")

        if not self._password_matches(account, request.password_sha512):
            raise InvalidCredentialsError("invalid username or password")

        if not account.mfa_enabled:
            return self._finalize_login(account)

        stage = "verify" if account.mfa_secret else "setup"
        return self._build_mfa_challenge(account, stage)

    def verify_mfa(
        self,
        challenge: OperatorMFAChallenge,
        request: OperatorMFAVerificationRequest,
    ) -> OperatorLoginSuccess:
        now = int(time.time())
        if challenge.pending_token != request.pending_token:
            raise MFASessionError("invalid_session")
        if challenge.expires_at < now:
            raise MFASessionError("expired")

        if challenge.stage == "setup":
            secret = (challenge.secret or "").strip()
            if not secret:
                raise MFASessionError("mfa_not_configured")
            totp = self._totp_for_secret(secret)
            if not totp.verify(request.code, valid_window=1):
                raise InvalidMFACodeError("invalid_code")
            self._repository.store_mfa_secret(challenge.username, secret, timestamp=now)
        else:
            account = self._repository.fetch_by_username(challenge.username)
            if not account or not account.mfa_secret:
                raise MFASessionError("mfa_not_configured")
            totp = self._totp_for_secret(account.mfa_secret)
            if not totp.verify(request.code, valid_window=1):
                raise InvalidMFACodeError("invalid_code")

        account = self._repository.fetch_by_username(challenge.username)
        if not account:
            raise InvalidCredentialsError("invalid username or password")
        return self._finalize_login(account)

    def issue_token(self, username: str, role: str) -> str:
        serializer = self._token_serializer()
        payload = {"u": username, "r": role or "User", "ts": int(time.time())}
        return serializer.dumps(payload)

    def resolve_token(self, token: str, *, max_age: int = 30 * 24 * 3600) -> Optional[OperatorAccount]:
        """Return the account associated with *token* if it is valid."""

        token = (token or "").strip()
        if not token:
            return None

        serializer = self._token_serializer()
        try:
            payload = serializer.loads(token, max_age=max_age)
        except (BadSignature, SignatureExpired):
            return None

        username = str(payload.get("u") or "").strip()
        if not username:
            return None

        return self._repository.fetch_by_username(username)

    def fetch_account(self, username: str) -> Optional[OperatorAccount]:
        """Return the operator account for *username* if it exists."""

        username = (username or "").strip()
        if not username:
            return None
        return self._repository.fetch_by_username(username)

    def _finalize_login(self, account: OperatorAccount) -> OperatorLoginSuccess:
        now = int(time.time())
        self._repository.update_last_login(account.username, now)
        token = self.issue_token(account.username, account.role)
        return OperatorLoginSuccess(username=account.username, role=account.role, token=token)

    def _password_matches(self, account: OperatorAccount, provided_hash: str) -> bool:
        expected = (account.password_sha512 or "").strip().lower()
        candidate = (provided_hash or "").strip().lower()
        return bool(expected and candidate and expected == candidate)

    def _build_mfa_challenge(
        self,
        account: OperatorAccount,
        stage: str,
    ) -> OperatorMFAChallenge:
        now = int(time.time())
        pending_token = uuid.uuid4().hex
        secret = None
        otpauth_url = None
        qr_image = None

        if stage == "setup":
            secret = self._generate_totp_secret()
            otpauth_url = self._totp_provisioning_uri(secret, account.username)
            qr_image = self._totp_qr_data_uri(otpauth_url) if otpauth_url else None

        return OperatorMFAChallenge(
            username=account.username,
            role=account.role,
            stage="verify" if stage == "verify" else "setup",
            pending_token=pending_token,
            expires_at=now + 300,
            secret=secret,
            otpauth_url=otpauth_url,
            qr_image=qr_image,
        )

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = os.getenv("BOREALIS_FLASK_SECRET_KEY") or "change-me"
        return URLSafeTimedSerializer(secret, salt="borealis-auth")

    def _generate_totp_secret(self) -> str:
        if not pyotp:
            raise MFAUnavailableError("pyotp is not installed; MFA unavailable")
        return pyotp.random_base32()  # type: ignore[no-any-return]

    def _totp_for_secret(self, secret: str):
        if not pyotp:
            raise MFAUnavailableError("pyotp is not installed; MFA unavailable")
        normalized = secret.replace(" ", "").strip().upper()
        if not normalized:
            raise MFASessionError("mfa_not_configured")
        return pyotp.TOTP(normalized, digits=6, interval=30)

    def _totp_provisioning_uri(self, secret: str, username: str) -> Optional[str]:
        try:
            totp = self._totp_for_secret(secret)
        except OperatorAuthError:
            return None
        issuer = os.getenv("BOREALIS_MFA_ISSUER", "Borealis")
        try:
            return totp.provisioning_uri(name=username, issuer_name=issuer)
        except Exception:  # pragma: no cover - defensive
            return None

    def _totp_qr_data_uri(self, payload: str) -> Optional[str]:
        if not payload or qrcode is None:
            return None
        try:
            img = qrcode.make(payload, box_size=6, border=4)
            buf = io.BytesIO()
            img.save(buf, format="PNG")
            encoded = base64.b64encode(buf.getvalue()).decode("ascii")
            return f"data:image/png;base64,{encoded}"
        except Exception:  # pragma: no cover - defensive
            self._log.warning("failed to generate MFA QR code", exc_info=True)
            return None


__all__ = [
    "OperatorAuthService",
    "OperatorAuthError",
    "InvalidCredentialsError",
    "MFAUnavailableError",
    "InvalidMFACodeError",
    "MFASessionError",
]
