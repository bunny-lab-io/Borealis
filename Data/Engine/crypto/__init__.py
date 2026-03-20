# ======================================================
# Data\Engine\crypto\__init__.py
# Description: Engine cryptographic helpers and key utilities.
#
# API Endpoints (if applicable): None
# ======================================================

"""Cryptographic helper utilities for the Borealis Engine runtime."""

from .aegis import (
    AegisCryptoError,
    ENVELOPE_PREFIX,
    SCRYPT_NAME,
    SCRYPT_N,
    SCRYPT_P,
    SCRYPT_R,
    decrypt_text,
    derive_key,
    encrypt_text,
    is_aegis_envelope,
    random_salt,
)
from .keys import (
    generate_ed25519_keypair,
    normalize_base64,
    spki_der_from_base64,
    base64_from_spki_der,
    fingerprint_from_spki_der,
    fingerprint_from_base64_spki,
    private_key_to_pem,
    public_key_to_pem,
)

__all__ = [
    "AegisCryptoError",
    "ENVELOPE_PREFIX",
    "SCRYPT_NAME",
    "SCRYPT_N",
    "SCRYPT_P",
    "SCRYPT_R",
    "decrypt_text",
    "derive_key",
    "encrypt_text",
    "generate_ed25519_keypair",
    "is_aegis_envelope",
    "normalize_base64",
    "random_salt",
    "spki_der_from_base64",
    "base64_from_spki_der",
    "fingerprint_from_spki_der",
    "fingerprint_from_base64_spki",
    "private_key_to_pem",
    "public_key_to_pem",
]
