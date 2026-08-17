# ======================================================
# Data\Engine\auth\jwt_service.py
# Description: Engine-native JWT access-token helpers with signing key storage under api-backend service secrets.
#
# API Endpoints (if applicable): None
# ======================================================

"""JWT access-token helpers backed by an Engine-managed Ed25519 key."""

from __future__ import annotations

import hashlib
import os
import time
from pathlib import Path
from typing import Any, Dict, Optional

import jwt
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

_TOKEN_ENV_ROOT = "BOREALIS_ENGINE_AUTH_TOKEN_ROOT"
_KEY_FILENAME = "borealis-jwt-ed25519.key"


def _env_path(name: str) -> Optional[Path]:
    value = os.environ.get(name)
    if not value:
        return None
    try:
        return Path(value).expanduser().resolve()
    except Exception:
        try:
            return Path(value).expanduser()
        except Exception:
            return Path(value)


def _engine_runtime_root() -> Path:
    env = _env_path("BOREALIS_ENGINE_ROOT") or _env_path("BOREALIS_ENGINE_RUNTIME")
    if env:
        env.mkdir(parents=True, exist_ok=True)
        return env
    root = Path(os.environ.get("BOREALIS_PROJECT_ROOT") or "/opt/Borealis").expanduser() / "Engine"
    root.mkdir(parents=True, exist_ok=True)
    return root


def _token_root() -> Path:
    env = _env_path(_TOKEN_ENV_ROOT)
    if env:
        env.mkdir(parents=True, exist_ok=True)
        return env
    root = _engine_runtime_root() / "Services" / "api-backend" / "secrets" / "Auth_Tokens"
    root.mkdir(parents=True, exist_ok=True)
    return root


def _tighten_permissions(path: Path) -> None:
    try:
        if os.name != "nt":
            path.chmod(0o600)
    except Exception:
        pass


def _key_file() -> Path:
    return _token_root() / _KEY_FILENAME


class JWTService:
    def __init__(self, private_key: ed25519.Ed25519PrivateKey, key_id: str):
        self._private_key = private_key
        self._public_key = private_key.public_key()
        self._key_id = key_id

    @property
    def key_id(self) -> str:
        return self._key_id

    def issue_access_token(
        self,
        guid: str,
        ssl_key_fingerprint: str,
        token_version: int,
        expires_in: int = 900,
        extra_claims: Optional[Dict[str, Any]] = None,
    ) -> str:
        now = int(time.time())
        payload: Dict[str, Any] = {
            "sub": f"device:{guid}",
            "guid": guid,
            "ssl_key_fingerprint": ssl_key_fingerprint,
            "token_version": int(token_version),
            "iat": now,
            "nbf": now,
            "exp": now + int(expires_in),
        }
        if extra_claims:
            payload.update(extra_claims)

        token = jwt.encode(
            payload,
            self._private_key.private_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PrivateFormat.PKCS8,
                encryption_algorithm=serialization.NoEncryption(),
            ),
            algorithm="EdDSA",
            headers={"kid": self._key_id},
        )
        return token

    def issue_claims(self, claims: Dict[str, Any]) -> str:
        payload = dict(claims or {})
        token = jwt.encode(
            payload,
            self._private_key.private_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PrivateFormat.PKCS8,
                encryption_algorithm=serialization.NoEncryption(),
            ),
            algorithm="EdDSA",
            headers={"kid": self._key_id},
        )
        return token

    def decode(self, token: str, *, audience: Optional[str] = None) -> Dict[str, Any]:
        options = {"require": ["exp", "iat", "sub"]}
        public_pem = self._public_key.public_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
        return jwt.decode(
            token,
            public_pem,
            algorithms=["EdDSA"],
            audience=audience,
            options=options,
        )

    def public_jwk(self) -> Dict[str, Any]:
        public_bytes = self._public_key.public_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PublicFormat.Raw,
        )
        jwk_x = jwt.utils.base64url_encode(public_bytes).decode("ascii")
        return {"kty": "OKP", "crv": "Ed25519", "kid": self._key_id, "alg": "EdDSA", "use": "sig", "x": jwk_x}


def load_service() -> JWTService:
    private_key = _load_or_create_private_key()
    public_bytes = private_key.public_key().public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    key_id = hashlib.sha256(public_bytes).hexdigest()[:16]
    return JWTService(private_key, key_id)


def _load_or_create_private_key() -> ed25519.Ed25519PrivateKey:
    key_file = _key_file()
    key_file.parent.mkdir(parents=True, exist_ok=True)

    if key_file.exists():
        with key_file.open("rb") as fh:
            return serialization.load_pem_private_key(fh.read(), password=None)

    private_key = ed25519.Ed25519PrivateKey.generate()
    pem = private_key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    )
    with key_file.open("wb") as fh:
        fh.write(pem)
    _tighten_permissions(key_file)
    return private_key


__all__ = ["JWTService", "load_service"]
