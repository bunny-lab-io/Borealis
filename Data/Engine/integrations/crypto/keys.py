"""Key utilities mirrored from the legacy crypto helpers."""

from __future__ import annotations

import base64
import hashlib
import re
from typing import Tuple

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519
from cryptography.hazmat.primitives.serialization import load_der_public_key

__all__ = [
    "base64_from_spki_der",
    "fingerprint_from_base64_spki",
    "fingerprint_from_spki_der",
    "generate_ed25519_keypair",
    "normalize_base64",
    "private_key_to_pem",
    "public_key_to_pem",
    "spki_der_from_base64",
]


def generate_ed25519_keypair() -> Tuple[ed25519.Ed25519PrivateKey, bytes]:
    private_key = ed25519.Ed25519PrivateKey.generate()
    public_key = private_key.public_key().public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    return private_key, public_key


def normalize_base64(data: str) -> str:
    cleaned = re.sub(r"\s+", "", data or "")
    return cleaned.replace("-", "+").replace("_", "/")


def spki_der_from_base64(spki_b64: str) -> bytes:
    return base64.b64decode(normalize_base64(spki_b64), validate=True)


def base64_from_spki_der(spki_der: bytes) -> str:
    return base64.b64encode(spki_der).decode("ascii")


def fingerprint_from_spki_der(spki_der: bytes) -> str:
    digest = hashlib.sha256(spki_der).hexdigest()
    return digest.lower()


def fingerprint_from_base64_spki(spki_b64: str) -> str:
    return fingerprint_from_spki_der(spki_der_from_base64(spki_b64))


def private_key_to_pem(private_key: ed25519.Ed25519PrivateKey) -> bytes:
    return private_key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    )


def public_key_to_pem(public_spki_der: bytes) -> bytes:
    public_key = load_der_public_key(public_spki_der)
    return public_key.public_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
