"""JWT issuance utilities for the Engine."""

from __future__ import annotations

import hashlib
import time
from typing import Any, Dict, Optional

import jwt
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

from Data.Engine.runtime import ensure_runtime_dir, runtime_path

__all__ = ["JWTService", "load_service"]


_KEY_DIR = runtime_path("auth_keys")
_KEY_FILE = _KEY_DIR / "engine-jwt-ed25519.key"
_LEGACY_KEY_FILE = runtime_path("keys") / "borealis-jwt-ed25519.key"


class JWTService:
    def __init__(self, private_key: ed25519.Ed25519PrivateKey, key_id: str) -> None:
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
    ensure_runtime_dir("auth_keys")

    if _KEY_FILE.exists():
        with _KEY_FILE.open("rb") as fh:
            return serialization.load_pem_private_key(fh.read(), password=None)

    if _LEGACY_KEY_FILE.exists():
        with _LEGACY_KEY_FILE.open("rb") as fh:
            return serialization.load_pem_private_key(fh.read(), password=None)

    private_key = ed25519.Ed25519PrivateKey.generate()
    pem = private_key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    )
    _KEY_DIR.mkdir(parents=True, exist_ok=True)
    with _KEY_FILE.open("wb") as fh:
        fh.write(pem)
    try:
        if hasattr(_KEY_FILE, "chmod"):
            _KEY_FILE.chmod(0o600)
    except Exception:
        pass
    return private_key
