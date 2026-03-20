# ======================================================
# Data\Engine\crypto\aegis.py
# Description: Low-level helpers for Aegis Cipher key derivation and AES-GCM secret envelopes.
#
# API Endpoints (if applicable): None
# ======================================================

"""Aegis Cipher cryptographic helpers for the Borealis Engine runtime."""

from __future__ import annotations

import base64
import binascii
import os
from typing import Final

from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.kdf.scrypt import Scrypt

ENVELOPE_PREFIX: Final[str] = "aegis:v1:"
KEY_LENGTH_BYTES: Final[int] = 32
NONCE_LENGTH_BYTES: Final[int] = 12
SALT_LENGTH_BYTES: Final[int] = 16
SCRYPT_N: Final[int] = 32768
SCRYPT_R: Final[int] = 8
SCRYPT_P: Final[int] = 1
SCRYPT_NAME: Final[str] = "scrypt"


class AegisCryptoError(ValueError):
    """Raised when an Aegis secret cannot be parsed or decrypted."""


def random_salt() -> bytes:
    """Return a new random salt for Aegis key derivation."""

    return os.urandom(SALT_LENGTH_BYTES)


def derive_key(
    cipher: str,
    *,
    salt: bytes,
    n: int = SCRYPT_N,
    r: int = SCRYPT_R,
    p: int = SCRYPT_P,
    length: int = KEY_LENGTH_BYTES,
) -> bytes:
    """Derive an AES-256 key from the operator-provided Aegis Cipher."""

    text = str(cipher or "")
    if text == "":
        raise AegisCryptoError("Aegis Cipher is required.")
    kdf = Scrypt(salt=salt, length=length, n=int(n), r=int(r), p=int(p))
    return kdf.derive(text.encode("utf-8"))


def encrypt_text(plaintext: str, *, key: bytes) -> str:
    """Encrypt UTF-8 text into an ASCII Aegis envelope."""

    nonce = os.urandom(NONCE_LENGTH_BYTES)
    ciphertext = AESGCM(key).encrypt(nonce, str(plaintext or "").encode("utf-8"), None)
    payload = base64.b64encode(nonce + ciphertext).decode("ascii")
    return f"{ENVELOPE_PREFIX}{payload}"


def decrypt_text(value: str, *, key: bytes) -> str:
    """Decrypt an ASCII Aegis envelope into UTF-8 text."""

    text = str(value or "")
    if not text.startswith(ENVELOPE_PREFIX):
        raise AegisCryptoError("Stored value is not an Aegis envelope.")
    encoded = text[len(ENVELOPE_PREFIX) :].strip()
    try:
        raw = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise AegisCryptoError("Stored Aegis envelope is not valid Base64.") from exc
    if len(raw) <= NONCE_LENGTH_BYTES:
        raise AegisCryptoError("Stored Aegis envelope is truncated.")
    nonce = raw[:NONCE_LENGTH_BYTES]
    ciphertext = raw[NONCE_LENGTH_BYTES:]
    try:
        plaintext = AESGCM(key).decrypt(nonce, ciphertext, None)
    except Exception as exc:
        raise AegisCryptoError("Stored Aegis envelope could not be decrypted.") from exc
    try:
        return plaintext.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise AegisCryptoError("Stored Aegis envelope does not contain UTF-8 text.") from exc


def is_aegis_envelope(value: object) -> bool:
    """Return whether the provided value is an ASCII Aegis envelope."""

    if value is None:
        return False
    if isinstance(value, memoryview):
        value = value.tobytes()
    if isinstance(value, bytes):
        try:
            value = value.decode("utf-8")
        except Exception:
            return False
    return str(value).startswith(ENVELOPE_PREFIX)


__all__ = [
    "AegisCryptoError",
    "ENVELOPE_PREFIX",
    "KEY_LENGTH_BYTES",
    "NONCE_LENGTH_BYTES",
    "SALT_LENGTH_BYTES",
    "SCRYPT_NAME",
    "SCRYPT_N",
    "SCRYPT_P",
    "SCRYPT_R",
    "decrypt_text",
    "derive_key",
    "encrypt_text",
    "is_aegis_envelope",
    "random_salt",
]
