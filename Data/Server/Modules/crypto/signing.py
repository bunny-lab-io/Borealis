"""
Code-signing helpers for delivering scripts to agents.
"""

from __future__ import annotations

from pathlib import Path
from typing import Tuple

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

from Modules.runtime import ensure_runtime_dir, runtime_path

from .keys import base64_from_spki_der

_KEY_DIR = runtime_path("keys")
_SIGNING_KEY_FILE = _KEY_DIR / "borealis-script-ed25519.key"
_SIGNING_PUB_FILE = _KEY_DIR / "borealis-script-ed25519.pub"


class ScriptSigner:
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
    private_key = _load_or_create()
    return ScriptSigner(private_key)


def _load_or_create() -> ed25519.Ed25519PrivateKey:
    ensure_runtime_dir("keys")
    if _SIGNING_KEY_FILE.exists():
        with _SIGNING_KEY_FILE.open("rb") as fh:
            return serialization.load_pem_private_key(fh.read(), password=None)

    private_key = ed25519.Ed25519PrivateKey.generate()
    pem = private_key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    )
    with _SIGNING_KEY_FILE.open("wb") as fh:
        fh.write(pem)
    try:
        if hasattr(_SIGNING_KEY_FILE, "chmod"):
            _SIGNING_KEY_FILE.chmod(0o600)
    except Exception:
        pass

    pub_der = private_key.public_key().public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    _SIGNING_PUB_FILE.write_bytes(pub_der)

    return private_key

