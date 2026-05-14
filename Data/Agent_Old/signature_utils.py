from __future__ import annotations

import base64
from typing import Any, Optional, Sequence

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

_BASE64_HINTS: Sequence[str] = ("base64", "b64", "base-64")


def _decode_base64_bytes(value: Optional[str]) -> Optional[bytes]:
    if not isinstance(value, str):
        return None
    stripped = value.strip()
    if not stripped:
        return b""
    cleaned = "".join(stripped.split())
    if not cleaned:
        return b""
    try:
        return base64.b64decode(cleaned, validate=True)
    except Exception:
        return None


def decode_script_bytes(script_content: Any, encoding_hint: Optional[str]) -> Optional[bytes]:
    """
    Normalize a script payload into UTF-8 bytes.

    Returns None only when the payload is unusable (e.g., invalid base64,
    unsupported type). Empty content is represented as b"".
    """
    if isinstance(script_content, (bytes, bytearray)):
        return bytes(script_content)

    if script_content is None:
        return b""

    encoding = str(encoding_hint or "").strip().lower()
    if isinstance(script_content, str):
        if encoding in _BASE64_HINTS:
            decoded = _decode_base64_bytes(script_content)
            return decoded

        decoded = _decode_base64_bytes(script_content)
        if decoded is not None:
            return decoded

        try:
            return script_content.encode("utf-8")
        except Exception:
            return None

    return None


def verify_and_store_script_signature(
    client: Any,
    script_bytes: bytes,
    signature_b64: Optional[str],
    signing_key_hint: Optional[str] = None,
) -> bool:
    """
    Verify a script payload against the provided signature and persist the server
    signing key when verification succeeds.
    """
    if not isinstance(script_bytes, (bytes, bytearray)):
        return False

    signature = _decode_base64_bytes(signature_b64)
    if signature is None:
        return False

    candidates = []
    if isinstance(signing_key_hint, str):
        hint = signing_key_hint.strip()
        if hint:
            candidates.append(hint)

    stored_key = None
    if client is not None and hasattr(client, "load_server_signing_key"):
        try:
            stored_key_raw = client.load_server_signing_key()
        except Exception:
            stored_key_raw = None
        if isinstance(stored_key_raw, str):
            stored_key = stored_key_raw.strip()
            if stored_key and stored_key not in candidates:
                candidates.append(stored_key)

    payload = bytes(script_bytes)
    for key_b64 in candidates:
        try:
            key_der = base64.b64decode(key_b64, validate=True)
        except Exception:
            continue
        try:
            public_key = serialization.load_der_public_key(key_der)
        except Exception:
            continue
        if not isinstance(public_key, ed25519.Ed25519PublicKey):
            continue
        try:
            public_key.verify(signature, payload)
        except Exception:
            continue

        if client is not None and hasattr(client, "store_server_signing_key"):
            try:
                if stored_key and stored_key != key_b64:
                    client.store_server_signing_key(key_b64)
                elif not stored_key:
                    client.store_server_signing_key(key_b64)
            except Exception:
                pass
        return True

    return False
