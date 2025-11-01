"""
Code-signing helpers for delivering scripts to agents.
"""

from __future__ import annotations

from pathlib import Path
from typing import Tuple

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

from Modules.runtime import (
    ensure_server_certificates_dir,
    server_certificates_path,
    runtime_path,
)

from .keys import base64_from_spki_der

_KEY_DIR = server_certificates_path("Code-Signing")
_SIGNING_KEY_FILE = _KEY_DIR / "borealis-script-ed25519.key"
_SIGNING_PUB_FILE = _KEY_DIR / "borealis-script-ed25519.pub"
_LEGACY_KEY_FILE = runtime_path("keys") / "borealis-script-ed25519.key"
_LEGACY_PUB_FILE = runtime_path("keys") / "borealis-script-ed25519.pub"
_OLD_RUNTIME_KEY_DIR = runtime_path("script_signing_keys")
_OLD_RUNTIME_KEY_FILE = _OLD_RUNTIME_KEY_DIR / "borealis-script-ed25519.key"
_OLD_RUNTIME_PUB_FILE = _OLD_RUNTIME_KEY_DIR / "borealis-script-ed25519.pub"


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
    ensure_server_certificates_dir("Code-Signing")
    _migrate_legacy_material_if_present()

    if _SIGNING_KEY_FILE.exists():
        with _SIGNING_KEY_FILE.open("rb") as fh:
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


def _migrate_legacy_material_if_present() -> None:
    if _SIGNING_KEY_FILE.exists():
        return

    # First migrate from legacy runtime path embedded in Server runtime.
    try:
        if _OLD_RUNTIME_KEY_FILE.exists() and not _SIGNING_KEY_FILE.exists():
            ensure_server_certificates_dir("Code-Signing")
            try:
                _OLD_RUNTIME_KEY_FILE.replace(_SIGNING_KEY_FILE)
            except Exception:
                _SIGNING_KEY_FILE.write_bytes(_OLD_RUNTIME_KEY_FILE.read_bytes())
            if _OLD_RUNTIME_PUB_FILE.exists() and not _SIGNING_PUB_FILE.exists():
                try:
                    _OLD_RUNTIME_PUB_FILE.replace(_SIGNING_PUB_FILE)
                except Exception:
                    _SIGNING_PUB_FILE.write_bytes(_OLD_RUNTIME_PUB_FILE.read_bytes())
    except Exception:
        pass

    if not _LEGACY_KEY_FILE.exists() or _SIGNING_KEY_FILE.exists():
        return

    try:
        ensure_server_certificates_dir("Code-Signing")
        try:
            _LEGACY_KEY_FILE.replace(_SIGNING_KEY_FILE)
        except Exception:
            _SIGNING_KEY_FILE.write_bytes(_LEGACY_KEY_FILE.read_bytes())

        if _LEGACY_PUB_FILE.exists() and not _SIGNING_PUB_FILE.exists():
            try:
                _LEGACY_PUB_FILE.replace(_SIGNING_PUB_FILE)
            except Exception:
                _SIGNING_PUB_FILE.write_bytes(_LEGACY_PUB_FILE.read_bytes())
    except Exception:
        return
