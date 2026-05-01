# ======================================================
# Data\Engine\security\signing.py
# Description: Manages Engine code-signing keys under the api-backend service certificate root without legacy fallbacks.
#
# API Endpoints (if applicable): None
# ======================================================

"""Engine code-signing helper utilities."""

from __future__ import annotations

import base64
import os
from pathlib import Path

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

from .certificates import engine_code_signing_root

__all__ = ["ScriptSigner", "load_signer", "base64_from_spki_der"]

_KEY_DIR = engine_code_signing_root()
_SIGNING_KEY_FILE = _KEY_DIR / "borealis-script-ed25519.key"
_SIGNING_PUB_FILE = _KEY_DIR / "borealis-script-ed25519.pub"


def base64_from_spki_der(spki_der: bytes) -> str:
    """Return Base64 representation for SubjectPublicKeyInfo DER bytes."""

    return base64.b64encode(spki_der).decode("ascii")


def _tighten_permissions(path: Path) -> None:
    try:
        if os.name != "nt":
            path.chmod(0o600)
    except Exception:
        pass


class ScriptSigner:
    """Wrap an Ed25519 private key with convenience helpers."""

    def __init__(self, private_key: ed25519.Ed25519PrivateKey):
        self._private = private_key
        self._public = private_key.public_key()

    def sign(self, payload: bytes) -> bytes:
        return self._private.sign(payload)

    def public_spki_der(self) -> bytes:
        return self._public.public_bytes(
            encoding=serialization.Encoding.DER,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )

    def public_base64_spki(self) -> str:
        return base64_from_spki_der(self.public_spki_der())


def load_signer() -> ScriptSigner:
    """Load (or create) the Engine script-signing key pair."""

    private_key = _load_or_create()
    return ScriptSigner(private_key)


def _load_or_create() -> ed25519.Ed25519PrivateKey:
    _KEY_DIR.mkdir(parents=True, exist_ok=True)
    if _SIGNING_KEY_FILE.exists():
        with _SIGNING_KEY_FILE.open("rb") as handle:
            return serialization.load_pem_private_key(handle.read(), password=None)

    private_key = ed25519.Ed25519PrivateKey.generate()
    pem = private_key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    )
    _SIGNING_KEY_FILE.write_bytes(pem)
    _tighten_permissions(_SIGNING_KEY_FILE)

    pub_der = private_key.public_key().public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    _SIGNING_PUB_FILE.write_bytes(pub_der)

    return private_key
